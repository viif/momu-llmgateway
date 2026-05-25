package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestHealthHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "ok")
}

func TestMetricsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestChatCompletionNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o", Strategy: "explicit"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		&mockCache{hit: false},
		&mockFallback{resp: &model.StandardResponse{ID: "test-1", Model: "gpt-4o", Choices: []model.Choice{{Index: 0, Message: model.Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"}}}, level: "primary"},
		func(name string) model.Provider { return &mockProvider{name: name} },
	)

	RegisterRoutes(r, svc)

	reqBody, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.StandardResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "test-1", resp.ID)
	require.Equal(t, "hello", resp.Choices[0].Message.Content)
}

func TestChatCompletionNoServiceReturnsNotImplemented(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, nil)
	reqBody, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody)))
	require.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestChatCompletionErrorFormatting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := NewChatService(
		&mockRouter{err: model.NewError(model.ErrCodeModelNotFound, "no providers for this model")},
		&mockCBManager{},
		&mockCache{hit: false},
		nil,
		nil,
	)
	RegisterRoutes(r, svc)

	reqBody, _ := json.Marshal(map[string]any{"model": "bad", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody)))

	require.Equal(t, http.StatusBadRequest, w.Code)
	var errResp struct {
		Error *model.Error `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	require.NotNil(t, errResp.Error)
	require.Equal(t, model.ErrCodeModelNotFound, errResp.Error.Code)
}

func TestChatCompletionCircuitBreakerOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: false}}},
		&mockCache{hit: false},
		nil,
		nil,
	)
	RegisterRoutes(r, svc)

	reqBody, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody)))

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var errResp struct {
		Error *model.Error `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	require.Equal(t, model.ErrCodeCircuitOpen, errResp.Error.Code)
}
