package ingress

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/fallback"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)

type Router interface {
	Route(req *model.StandardRequest) (decision.RouteDecision, error)
}

type CircuitBreakerManager interface {
	Allow(provider, model string) bool
	RecordSuccess(provider, model string)
	RecordFailure(provider, model string)
}

type SemanticCache interface {
	Lookup(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, bool)
	Store(ctx context.Context, req *model.StandardRequest, resp *model.StandardResponse) error
}

type FallbackEngine interface {
	Execute(ctx context.Context, provider, model string, sendFn fallback.SendFunc) (*model.StandardResponse, string, error)
}

type ProviderLookupFunc func(name string) model.Provider

type ChatService interface {
	HandleChatCompletion(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error)
	HandleChatCompletionStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error)
}

type chatServiceImpl struct {
	router         Router
	cbManager      CircuitBreakerManager
	cache          SemanticCache
	fallbackEng    FallbackEngine
	providerLookup ProviderLookupFunc
}

func NewChatService(
	router Router,
	cbManager CircuitBreakerManager,
	cache SemanticCache,
	fallbackEng FallbackEngine,
	providerLookup ProviderLookupFunc,
) ChatService {
	return &chatServiceImpl{
		router:         router,
		cbManager:      cbManager,
		cache:          cache,
		fallbackEng:    fallbackEng,
		providerLookup: providerLookup,
	}
}

func (s *chatServiceImpl) HandleChatCompletion(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	start := time.Now()

	if req.Stream {
		return nil, model.NewError(model.ErrCodeInvalidRequest, "use HandleChatCompletionStream for streaming requests")
	}

	if s.cache != nil {
		if cachedResp, hit := s.cache.Lookup(ctx, req); hit {
			observability.CacheHitTotal.WithLabelValues(req.Model, "semantic").Inc()
			observability.RequestTotal.WithLabelValues("cache", req.Model, "success").Inc()
			observability.RequestDuration.WithLabelValues("cache", req.Model).Observe(time.Since(start).Seconds())
			return cachedResp, nil
		}
	}

	routeDecision, err := s.router.Route(req)
	if err != nil {
		return nil, err
	}

	if !s.cbManager.Allow(routeDecision.ProviderName, routeDecision.Model) {
		return nil, model.NewError(model.ErrCodeCircuitOpen,
			"circuit breaker open for "+routeDecision.ProviderName+"/"+routeDecision.Model)
	}

	sendFn := s.buildSendFn(req)

	resp, level, err := s.fallbackEng.Execute(ctx, routeDecision.ProviderName, routeDecision.Model, sendFn)
	if err != nil {
		return nil, err
	}

	observability.RequestDuration.WithLabelValues(routeDecision.ProviderName, routeDecision.Model).Observe(time.Since(start).Seconds())

	status := "success"
	if level == "fallback_exhausted" {
		status = "fallback_exhausted"
	} else if level != "primary" {
		observability.FallbackTotal.WithLabelValues(level, req.Model, routeDecision.Model).Inc()
	}
	observability.RequestTotal.WithLabelValues(routeDecision.ProviderName, routeDecision.Model, status).Inc()

	observability.TokensTotal.WithLabelValues(routeDecision.ProviderName, routeDecision.Model, "prompt").Add(float64(resp.Usage.PromptTokens))
	observability.TokensTotal.WithLabelValues(routeDecision.ProviderName, routeDecision.Model, "completion").Add(float64(resp.Usage.CompletionTokens))

	if resp.Provider == "" {
		resp.Provider = routeDecision.ProviderName
	}

	if s.cache != nil && level != "fallback_exhausted" {
		_ = s.cache.Store(ctx, req, resp)
	}

	return resp, nil
}

func (s *chatServiceImpl) HandleChatCompletionStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	if !req.Stream {
		req.Stream = true
	}

	routeDecision, err := s.router.Route(req)
	if err != nil {
		return nil, err
	}

	if !s.cbManager.Allow(routeDecision.ProviderName, routeDecision.Model) {
		return nil, model.NewError(model.ErrCodeCircuitOpen,
			"circuit breaker open for "+routeDecision.ProviderName+"/"+routeDecision.Model)
	}

	provider := s.providerLookup(routeDecision.ProviderName)
	if provider == nil {
		return nil, model.NewError(model.ErrCodeProviderError,
			"provider not found: "+routeDecision.ProviderName)
	}

	ch, err := provider.SendStream(ctx, req)
	if err != nil {
		s.cbManager.RecordFailure(routeDecision.ProviderName, routeDecision.Model)
		return nil, err
	}

	out := make(chan model.StreamChunk)
	go func() {
		defer close(out)
		var gotError bool
		for chunk := range ch {
			if chunk.Error != nil {
				s.cbManager.RecordFailure(routeDecision.ProviderName, routeDecision.Model)
				gotError = true
			}
			out <- chunk
		}
		if !gotError {
			s.cbManager.RecordSuccess(routeDecision.ProviderName, routeDecision.Model)
		}
	}()

	return out, nil
}

func (s *chatServiceImpl) buildSendFn(originalReq *model.StandardRequest) fallback.SendFunc {
	return func(ctx context.Context, providerName, modelName string) (*model.StandardResponse, error) {
		provider := s.providerLookup(providerName)
		if provider == nil {
			return nil, model.NewError(model.ErrCodeProviderError, "provider not found: "+providerName)
		}

		if !s.cbManager.Allow(providerName, modelName) {
			return nil, model.NewError(model.ErrCodeCircuitOpen,
				"circuit breaker open for "+providerName+"/"+modelName)
		}

		req := *originalReq
		req.Model = modelName
		req.Stream = false

		resp, err := provider.Send(ctx, &req)
		if err != nil {
			s.cbManager.RecordFailure(providerName, modelName)
			return nil, err
		}

		s.cbManager.RecordSuccess(providerName, modelName)

		resp.Provider = providerName
		if resp.Model == "" {
			resp.Model = modelName
		}
		return resp, nil
	}
}

var _ prometheus.Collector = observability.RequestDuration

type circuitBreakerManagerImpl struct {
	mu        sync.RWMutex
	breakers  map[string]*decision.CircuitBreaker
	threshold int
	cooldown  time.Duration
}

func NewCircuitBreakerManager(threshold int, cooldown time.Duration) CircuitBreakerManager {
	return &circuitBreakerManagerImpl{
		breakers:  make(map[string]*decision.CircuitBreaker),
		threshold: threshold,
		cooldown:  cooldown,
	}
}

func (m *circuitBreakerManagerImpl) key(provider, model string) string {
	return provider + "/" + model
}

func (m *circuitBreakerManagerImpl) getOrCreate(key string) *decision.CircuitBreaker {
	m.mu.RLock()
	if cb, ok := m.breakers[key]; ok {
		m.mu.RUnlock()
		return cb
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if cb, ok := m.breakers[key]; ok {
		return cb
	}
	cb := decision.NewCircuitBreaker(m.threshold, m.cooldown)
	m.breakers[key] = cb
	return cb
}

func (m *circuitBreakerManagerImpl) Allow(provider, model string) bool {
	return m.getOrCreate(m.key(provider, model)).Allow()
}

func (m *circuitBreakerManagerImpl) RecordSuccess(provider, model string) {
	m.getOrCreate(m.key(provider, model)).RecordSuccess()
}

func (m *circuitBreakerManagerImpl) RecordFailure(provider, model string) {
	m.getOrCreate(m.key(provider, model)).RecordFailure()
}
