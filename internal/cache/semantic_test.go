package cache

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

type fakeEmbedder struct {
	vectors map[string][]float64
}

func (f *fakeEmbedder) Embed(texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = v
		} else {
			out[i] = make([]float64, 2)
		}
	}
	return out, nil
}

func TestNewCacheUsesConfig(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.95, TTL: time.Hour}, nil, nil)
	require.True(t, c.enabled)
	require.Equal(t, 100, c.maxEntries)
	require.Equal(t, 0.95, c.threshold)
}

func TestNewCacheDefaultDisabled(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: false}, nil, nil)
	require.False(t, c.enabled)
}

func TestLookupHit(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached", Model: "gpt-4o"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)

	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	resp, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.True(t, ok)
	require.True(t, resp.CacheHit)
	require.Equal(t, "cached", resp.ID)
}

func TestLookupMissDifferentSemantics(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "unknown"}},
	})
	require.False(t, ok)
}

func TestLookupDifferentModelIsolation(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hi": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "claude-sonnet-4-20250514", Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.False(t, ok)
}

func TestLookupDisabled(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: false}, nil, nil)
	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.False(t, ok)
}

func TestLookupEmbedderNil(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8}, nil, nil)
	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.False(t, ok)
}

func TestLookupEmptyUserMessage(t *testing.T) {
	embedder := &fakeEmbedder{}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "system", Content: "you are a helper"}},
	})
	require.False(t, ok)
}

func TestLookupUpdatesAccessTime(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached"})
	oldTime := time.Now().Add(-time.Hour)
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: 2 * time.Hour}, embedder, nil)
	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: oldTime, LastAccess: oldTime},
	}
	c.mu.Unlock()

	c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	c.mu.RLock()
	require.True(t, c.entries["gpt-4o"][0].LastAccess.After(oldTime))
	c.mu.RUnlock()
}

func TestLookupSkipsExpiredEntry(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: 10 * time.Millisecond}, embedder, nil)
	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now().Add(-time.Hour), LastAccess: time.Now().Add(-time.Hour)},
	}
	c.mu.Unlock()

	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.False(t, ok)
	c.mu.RLock()
	require.Len(t, c.entries["gpt-4o"], 0)
	c.mu.RUnlock()
}

func TestStoreThenLookup(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)

	err := c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	}, &model.StandardResponse{ID: "resp-1", Model: "gpt-4o"})
	require.NoError(t, err)

	found, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.True(t, ok)
	require.Equal(t, "resp-1", found.ID)
}

func TestStoreSkipsStreaming(t *testing.T) {
	embedder := &fakeEmbedder{}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	err := c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Stream: true, Messages: []model.Message{{Role: "user", Content: "hi"}},
	}, &model.StandardResponse{ID: "resp"})
	require.NoError(t, err)
	c.mu.RLock()
	require.Len(t, c.entries["gpt-4o"], 0)
	c.mu.RUnlock()
}

func TestStoreEvictsOldestWhenFull(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{
		"a": {1.0, 0.0}, "b": {0.0, 1.0}, "c": {0.5, 0.5},
	}}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 2, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)

	require.NoError(t, c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "a"}},
	}, &model.StandardResponse{ID: "a"}))
	time.Sleep(time.Millisecond)

	require.NoError(t, c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "b"}},
	}, &model.StandardResponse{ID: "b"}))

	resp, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "a"}},
	})
	require.True(t, ok)
	require.Equal(t, "a", resp.ID)

	require.NoError(t, c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "c"}},
	}, &model.StandardResponse{ID: "c"}))

	_, ok = c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "b"}},
	})
	require.False(t, ok)

	_, ok = c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "a"}},
	})
	require.True(t, ok)
}

type fakeStore struct {
	entries map[string][]CacheEntry
}

func (f *fakeStore) Save(ctx context.Context, model, key string, vector []float64, respJSON []byte, ttl time.Duration) error {
	f.entries[model] = append(f.entries[model], CacheEntry{
		Model: model, Key: key, Vector: append([]float64(nil), vector...),
		ResponseJSON: append([]byte(nil), respJSON...), StoredAt: time.Now(),
	})
	return nil
}

func (f *fakeStore) LoadAll(ctx context.Context, model string) ([]CacheEntry, error) {
	return f.entries[model], nil
}

func (f *fakeStore) Close() error { return nil }

func TestLoadFromStoreRecoversEntries(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	store := &fakeStore{entries: make(map[string][]CacheEntry)}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, store)

	respJSON, _ := json.Marshal(&model.StandardResponse{ID: "recovered", Model: "gpt-4o"})
	require.NoError(t, store.Save(context.Background(), "gpt-4o", "k1", []float64{1.0, 0.0}, respJSON, time.Hour))

	c.SetModels([]string{"gpt-4o"})
	require.NoError(t, c.LoadFromStore(context.Background()))

	resp, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.True(t, ok)
	require.Equal(t, "recovered", resp.ID)
}

func TestLoadFromStoreNilStore(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: true}, nil, nil)
	require.NoError(t, c.LoadFromStore(context.Background()))
}

func TestEncodeDecodeVector(t *testing.T) {
	original := []float64{0.1, -0.2, 0.3, 0.0, 1.0}
	encoded := EncodeVector(original)
	decoded := DecodeVector(encoded)
	require.Len(t, decoded, len(original))
	for i := range original {
		require.InDelta(t, original[i], decoded[i], 0.0001)
	}
}

func TestLookupSkipWhenPromptExceedsMaxLength(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached", Model: "gpt-4o"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour, MaxPromptLength: 3}, embedder, nil)

	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello world"}},
	})
	require.False(t, ok, "超长prompt应跳过缓存查询")
}

func TestLookupPromptWithinMaxLength(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hi": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached", Model: "gpt-4o"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour, MaxPromptLength: 5}, embedder, nil)

	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	resp, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.True(t, ok, "未超长的prompt应命中缓存")
	require.True(t, resp.CacheHit)
}

func TestLookupMaxPromptLengthZeroUnlimited(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached", Model: "gpt-4o"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour, MaxPromptLength: 0}, embedder, nil)

	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.True(t, ok, "MaxPromptLength=0时应不做限制")
}

func TestStoreSkipWhenPromptExceedsMaxLength(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello world": {1.0, 0.0}}}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour, MaxPromptLength: 5}, embedder, nil)

	err := c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello world"}},
	}, &model.StandardResponse{ID: "resp-1", Model: "gpt-4o"})
	require.NoError(t, err)

	c.mu.RLock()
	require.Len(t, c.entries["gpt-4o"], 0, "超长prompt不应写入缓存")
	c.mu.RUnlock()
}

func TestCacheConcurrentAccess(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hi": {1.0, 0.0}}}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	err := c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}},
	}, &model.StandardResponse{ID: "resp"})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Lookup(context.Background(), &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}})
		}()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Store(context.Background(), &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}, &model.StandardResponse{ID: "concurrent"})
		}()
	}
	wg.Wait()
}
