package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/viif/momu-llmgateway/internal/embedding"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)

type Embedder interface {
	Embed(texts []string) ([][]float64, error)
}

type CacheStore interface {
	Save(ctx context.Context, model, key string, vector []float64, respJSON []byte, ttl time.Duration) error
	LoadAll(ctx context.Context, model string) ([]CacheEntry, error)
	Close() error
}

type CacheEntry struct {
	Model        string    `json:"model"`
	Key          string    `json:"key"`
	Vector       []float64 `json:"vector"`
	ResponseJSON []byte    `json:"response"`
	StoredAt     time.Time `json:"stored_at"`
	LastAccess   time.Time `json:"last_access"`
}

type SemanticCache struct {
	mu                sync.RWMutex
	entries           map[string][]CacheEntry
	maxEntries        int
	threshold         float64
	ttl               time.Duration
	maxPromptLength   int
	enabled           bool
	embedder          Embedder
	store             CacheStore
	recoverableModels []string
}

type SemanticCacheConfig struct {
	Enabled             bool
	SimilarityThreshold float64
	MaxEntries          int
	TTL                 time.Duration
	MaxPromptLength     int
}

func New(cfg SemanticCacheConfig, embedder Embedder, store CacheStore) *SemanticCache {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 10000
	}
	return &SemanticCache{
		entries:         make(map[string][]CacheEntry),
		maxEntries:      cfg.MaxEntries,
		threshold:       cfg.SimilarityThreshold,
		ttl:             cfg.TTL,
		maxPromptLength: cfg.MaxPromptLength,
		enabled:         cfg.Enabled,
		embedder:        embedder,
		store:           store,
	}
}

func (c *SemanticCache) SetModels(models []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recoverableModels = append([]string(nil), models...)
}

func (c *SemanticCache) Lookup(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, bool) {
	if !c.enabled || c.embedder == nil {
		return nil, false
	}
	text := concatenateUserMessages(req.Messages)
	if text == "" {
		return nil, false
	}
	numRunes := len([]rune(text))
	if c.maxPromptLength > 0 && numRunes > c.maxPromptLength {
		return nil, false
	}
	vecs, err := c.embedder.Embed([]string{text})
	if err != nil || len(vecs) == 0 {
		return nil, false
	}

	c.mu.RLock()
	entries := c.entries[req.Model]
	c.mu.RUnlock()

	now := time.Now()
	var best *CacheEntry
	bestScore := c.threshold
	var expired []int

	for i := range entries {
		if now.Sub(entries[i].StoredAt) > c.ttl {
			expired = append(expired, i)
			continue
		}
		score := embedding.CosineSimilarity(vecs[0], entries[i].Vector)
		if score >= bestScore {
			bestScore = score
			best = &entries[i]
		}
	}

	if len(expired) > 0 {
		c.removeExpired(req.Model, expired)
	}
	if best == nil {
		return nil, false
	}

	c.mu.Lock()
	best.LastAccess = now
	c.mu.Unlock()

	observability.CacheHitTotal.WithLabelValues(req.Model, "semantic").Inc()

	var resp model.StandardResponse
	if err := json.Unmarshal(best.ResponseJSON, &resp); err != nil {
		return nil, false
	}
	resp.CacheHit = true
	return &resp, true
}

func (c *SemanticCache) Store(ctx context.Context, req *model.StandardRequest, resp *model.StandardResponse) error {
	if !c.enabled || c.embedder == nil || req.Stream {
		return nil
	}
	text := concatenateUserMessages(req.Messages)
	if text == "" {
		return nil
	}
	numRunes := len([]rune(text))
	if c.maxPromptLength > 0 && numRunes > c.maxPromptLength {
		return nil
	}
	vecs, err := c.embedder.Embed([]string{text})
	if err != nil || len(vecs) == 0 {
		return err
	}
	respJSON, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	key := hashContent(text)

	entry := CacheEntry{
		Model:        req.Model,
		Key:          key,
		Vector:       vecs[0],
		ResponseJSON: respJSON,
		StoredAt:     time.Now(),
		LastAccess:   time.Now(),
	}

	c.mu.Lock()
	c.entries[req.Model] = append(c.entries[req.Model], entry)
	if len(c.entries[req.Model]) > c.maxEntries {
		c.evictOne(req.Model)
	}
	if len(c.entries[req.Model]) > c.maxEntries*3/2 {
		c.compactExpired(req.Model)
	}
	c.mu.Unlock()

	if c.store != nil {
		_ = c.store.Save(ctx, req.Model, key, vecs[0], respJSON, c.ttl)
	}
	return nil
}

func (c *SemanticCache) removeExpired(model string, indices []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := c.entries[model]
	for i := len(indices) - 1; i >= 0; i-- {
		idx := indices[i]
		if idx >= len(entries) {
			continue
		}
		last := len(entries) - 1
		entries[idx] = entries[last]
		entries = entries[:last]
	}
	c.entries[model] = entries
}

func (c *SemanticCache) evictOne(model string) {
	entries := c.entries[model]
	if len(entries) == 0 {
		return
	}
	oldest := 0
	for i := 1; i < len(entries); i++ {
		if entries[i].LastAccess.Before(entries[oldest].LastAccess) {
			oldest = i
		}
	}
	entries[oldest] = entries[len(entries)-1]
	c.entries[model] = entries[:len(entries)-1]
}

func (c *SemanticCache) compactExpired(model string) {
	entries := c.entries[model]
	now := time.Now()
	keep := 0
	for i := range entries {
		if now.Sub(entries[i].StoredAt) <= c.ttl {
			entries[keep] = entries[i]
			keep++
		}
	}
	c.entries[model] = entries[:keep]
}

func (c *SemanticCache) LoadFromStore(ctx context.Context) error {
	if c.store == nil {
		return nil
	}
	c.mu.RLock()
	models := append([]string(nil), c.recoverableModels...)
	c.mu.RUnlock()

	for _, m := range models {
		entries, err := c.store.LoadAll(ctx, m)
		if err != nil {
			continue
		}
		if len(entries) > 0 {
			now := time.Now()
			c.mu.Lock()
			for _, e := range entries {
				e.StoredAt = now
				e.LastAccess = now
				c.entries[m] = append(c.entries[m], e)
			}
			c.mu.Unlock()
		}
	}
	return nil
}

func concatenateUserMessages(messages []model.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Content != "" {
			return messages[i].Content
		}
	}
	return ""
}

func hashContent(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:16])
}

func EncodeVector(v []float64) []byte {
	buf := make([]byte, len(v)*8)
	for i, f := range v {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(f))
	}
	return buf
}

func DecodeVector(data []byte) []float64 {
	v := make([]float64, len(data)/8)
	for i := range v {
		v[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[i*8:]))
	}
	return v
}
