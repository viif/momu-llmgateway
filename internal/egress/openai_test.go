package egress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestOpenAICompatibleBuildRequest(t *testing.T) {
	p := NewOpenAICompatible("openai", "https://example.test/v1", "sk-test", []string{"gpt-4o"}, time.Second)
	body, err := p.buildRequestBody(&model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Contains(t, string(body), "gpt-4o")
	require.Contains(t, string(body), "hi")
}

func TestParseSSELine(t *testing.T) {
	chunk, done, err := parseSSELine(`data: {"id":"1","model":"gpt-4o","choices":[{"delta":{"content":"hi"}}]}`)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, "hi", chunk.Delta.Content)

	_, done, err = parseSSELine("data: [DONE]")
	require.NoError(t, err)
	require.True(t, done)

	_, _, err = parseSSELine(strings.TrimSpace(""))
	require.NoError(t, err)
}

func TestOpenAIHealthCheckSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewOpenAICompatible("openai", srv.URL, "sk-test", []string{"gpt-4o"}, 2*time.Second)
	err := p.HealthCheck(context.Background())
	require.NoError(t, err)
}

func TestOpenAIHealthCheckUnreachable(t *testing.T) {
	p := NewOpenAICompatible("openai", "http://127.0.0.1:19999", "sk-test", []string{"gpt-4o"}, 500*time.Millisecond)
	err := p.HealthCheck(context.Background())
	require.Error(t, err)
}
