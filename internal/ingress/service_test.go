package ingress

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/fallback"
	"github.com/viif/momu-llmgateway/internal/model"
)

type mockProvider struct {
	name      string
	models    []string
	resp      *model.StandardResponse
	err       error
	streamCh  chan model.StreamChunk
	streamErr error
}

func (m *mockProvider) Name() string     { return m.name }
func (m *mockProvider) Models() []string { return m.models }
func (m *mockProvider) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	return m.resp, m.err
}
func (m *mockProvider) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	if m.streamCh != nil {
		out := make(chan model.StreamChunk)
		go func() {
			defer close(out)
			for chunk := range m.streamCh {
				select {
				case out <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}()
		return out, nil
	}
	ch := make(chan model.StreamChunk)
	close(ch)
	return ch, nil
}

func (m *mockProvider) HealthCheck(ctx context.Context) error { return nil }

type mockRouter struct {
	decision decision.RouteDecision
	err      error
}

func (m *mockRouter) Route(req *model.StandardRequest) (decision.RouteDecision, error) {
	return m.decision, m.err
}

type mockCircuitBreaker struct {
	allow bool
	calls int
}

type mockCBManager struct {
	breakers map[string]*mockCircuitBreaker
}

func (m *mockCBManager) Allow(prov, model string) bool {
	key := prov + "/" + model
	if cb, ok := m.breakers[key]; ok {
		cb.calls++
		return cb.allow
	}
	return true
}
func (m *mockCBManager) RecordSuccess(prov, model string) {}
func (m *mockCBManager) RecordFailure(prov, model string) {}

type mockCache struct {
	hit  bool
	resp *model.StandardResponse
}

func (m *mockCache) Lookup(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, bool) {
	return m.resp, m.hit
}
func (m *mockCache) Store(ctx context.Context, req *model.StandardRequest, resp *model.StandardResponse) error {
	return nil
}

type mockFallback struct {
	resp  *model.StandardResponse
	level string
	err   error
}

func (m *mockFallback) Execute(ctx context.Context, provider, model string, sendFn fallback.SendFunc) (*model.StandardResponse, string, error) {
	return m.resp, m.level, m.err
}

func TestChatServiceNonStreamingSuccess(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o", Strategy: "capability"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		&mockCache{hit: false},
		&mockFallback{resp: &model.StandardResponse{ID: "resp-1", Model: "gpt-4o", Choices: []model.Choice{{Index: 0, Message: model.Message{Role: "assistant", Content: "hi"}, FinishReason: "stop"}}}, level: "primary"},
		func(name string) model.Provider { return &mockProvider{name: name} },
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "resp-1", resp.ID)
}

func TestChatServiceCacheHit(t *testing.T) {
	cachedResp := &model.StandardResponse{ID: "cached-1", Model: "gpt-4o", CacheHit: true, Choices: []model.Choice{{Message: model.Message{Content: "cached"}}}}
	svc := NewChatService(
		&mockRouter{},
		&mockCBManager{},
		&mockCache{hit: true, resp: cachedResp},
		nil,
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.NoError(t, err)
	require.True(t, resp.CacheHit)
	require.Equal(t, "cached-1", resp.ID)
}

func TestChatServiceCircuitBreakerOpen(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: false}}},
		&mockCache{hit: false},
		nil,
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.Error(t, err)
	require.Nil(t, resp)
	me, ok := err.(*model.Error)
	require.True(t, ok)
	require.Equal(t, model.ErrCodeCircuitOpen, me.Code)
}

func TestChatServiceRouteError(t *testing.T) {
	svc := NewChatService(
		&mockRouter{err: model.NewError(model.ErrCodeModelNotFound, "no route")},
		&mockCBManager{},
		&mockCache{hit: false},
		nil,
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "unknown", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.Error(t, err)
	require.Nil(t, resp)
}

func TestChatServiceFallbackExhausted(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		&mockCache{hit: false},
		&mockFallback{resp: &model.StandardResponse{ID: "fallback", Choices: []model.Choice{{Message: model.Message{Content: "fallback msg"}, FinishReason: "stop"}}}, level: "fallback_exhausted", err: nil},
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "fallback", resp.ID)
}

func TestChatServiceNonStreamingRecordsMetrics(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o", Strategy: "cost_cascade"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		&mockCache{hit: false},
		&mockFallback{resp: &model.StandardResponse{ID: "r", Model: "gpt-4o", Provider: "openai", Choices: []model.Choice{{Message: model.Message{Content: "ok"}, FinishReason: "stop"}}, Usage: model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, level: "primary"},
		func(name string) model.Provider { return &mockProvider{name: name} },
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false, RequestID: "req-123"}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "r", resp.ID)
	require.Equal(t, "openai", resp.Provider)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, 5, resp.Usage.CompletionTokens)
	require.Equal(t, 15, resp.Usage.TotalTokens)
}

func TestCircuitBreakerManagerAllowAndRecord(t *testing.T) {
	mgr := NewCircuitBreakerManager(2, 30*time.Second)
	require.True(t, mgr.Allow("openai", "gpt-4o"))
	require.True(t, mgr.Allow("openai", "gpt-4o"))
	mgr.RecordFailure("openai", "gpt-4o")
	require.True(t, mgr.Allow("openai", "gpt-4o"))
	mgr.RecordFailure("openai", "gpt-4o")
	require.False(t, mgr.Allow("openai", "gpt-4o"))
}

func TestCircuitBreakerManagerPerProviderModelIsolation(t *testing.T) {
	mgr := NewCircuitBreakerManager(1, 30*time.Second)
	mgr.RecordFailure("openai", "gpt-4o")
	require.True(t, mgr.Allow("deepseek", "deepseek-chat"))
}

func TestCircuitBreakerManagerRecordSuccessResets(t *testing.T) {
	mgr := NewCircuitBreakerManager(1, 30*time.Second)
	mgr.RecordFailure("openai", "gpt-4o")
	require.False(t, mgr.Allow("openai", "gpt-4o"))
	mgr.RecordSuccess("openai", "gpt-4o")
	require.True(t, mgr.Allow("openai", "gpt-4o"))
}

func TestCircuitBreakerManagerHalfOpenAfterCooldown(t *testing.T) {
	mgr := NewCircuitBreakerManager(1, 50*time.Millisecond)
	mgr.RecordFailure("openai", "gpt-4o")
	require.False(t, mgr.Allow("openai", "gpt-4o"))
	time.Sleep(60 * time.Millisecond)
	require.True(t, mgr.Allow("openai", "gpt-4o"))
}

func TestChatServiceStreamingSuccess(t *testing.T) {
	streamCh := make(chan model.StreamChunk, 3)
	streamCh <- model.StreamChunk{ID: "s1", Delta: model.Delta{Role: "assistant"}}
	streamCh <- model.StreamChunk{ID: "s1", Delta: model.Delta{Content: "Hello"}}
	streamCh <- model.StreamChunk{ID: "s1", Done: true}
	close(streamCh)

	prov := &mockProvider{name: "openai", streamCh: streamCh}
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o", Strategy: "explicit"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		nil,
		nil,
		func(name string) model.Provider { return prov },
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	ch, err := svc.HandleChatCompletionStream(ctx, req)

	require.NoError(t, err)
	var chunks []model.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}
	require.Len(t, chunks, 3)
	require.Equal(t, "assistant", chunks[0].Delta.Role)
	require.Equal(t, "Hello", chunks[1].Delta.Content)
	require.True(t, chunks[2].Done)
}

func TestChatServiceStreamingCircuitBreakerOpen(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: false}}},
		nil,
		nil,
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	ch, err := svc.HandleChatCompletionStream(ctx, req)

	require.Error(t, err)
	require.Nil(t, ch)
	me, ok := err.(*model.Error)
	require.True(t, ok)
	require.Equal(t, model.ErrCodeCircuitOpen, me.Code)
}

func TestChatServiceStreamingRouteError(t *testing.T) {
	svc := NewChatService(
		&mockRouter{err: model.NewError(model.ErrCodeModelNotFound, "no route")},
		&mockCBManager{},
		nil,
		nil,
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "unknown", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	ch, err := svc.HandleChatCompletionStream(ctx, req)

	require.Error(t, err)
	require.Nil(t, ch)
}

func TestChatServiceStreamingProviderNotFound(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "nonexistent", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"nonexistent/gpt-4o": {allow: true}}},
		nil,
		nil,
		func(name string) model.Provider { return nil },
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	ch, err := svc.HandleChatCompletionStream(ctx, req)

	require.Error(t, err)
	require.Nil(t, ch)
	me, ok := err.(*model.Error)
	require.True(t, ok)
	require.Equal(t, model.ErrCodeProviderError, me.Code)
	require.Contains(t, me.Message, "provider not found")
}

func TestChatServiceStreamingSendStreamError(t *testing.T) {
	prov := &mockProvider{name: "openai", streamErr: model.NewError(model.ErrCodeProviderError, "timeout")}
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		nil,
		nil,
		func(name string) model.Provider { return prov },
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	ch, err := svc.HandleChatCompletionStream(ctx, req)

	require.Error(t, err)
	require.Nil(t, ch)
}
