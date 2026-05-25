package egress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestAnthropicExtractsSystemMessage(t *testing.T) {
	p := NewAnthropic("test-anthropic", "https://example.test", "sk", []string{"claude-sonnet-4-20250514"}, time.Second)
	body, err := p.buildRequestBody(&model.StandardRequest{Model: "claude-sonnet-4-20250514", Messages: []model.Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Contains(t, string(body), "system")
	require.Contains(t, string(body), "be brief")
}

func TestAnthropicParseSSEEvent(t *testing.T) {
	chunk, done, err := parseAnthropicSSEEvent("content_block_delta", ` {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, "Hello", chunk.Delta.Content)

	chunk, done, err = parseAnthropicSSEEvent("message_start", ` {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-20250514"}}`)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, "msg_1", chunk.ID)
	require.Equal(t, "assistant", chunk.Delta.Role)

	chunk, done, err = parseAnthropicSSEEvent("message_stop", ` {"type":"message_stop"}`)
	require.NoError(t, err)
	require.True(t, done)

	chunk, done, err = parseAnthropicSSEEvent("ping", ` {"type":"ping"}`)
	require.NoError(t, err)
	require.False(t, done)
	require.Empty(t, chunk.Delta.Content)

	_, _, err = parseAnthropicSSEEvent("", "")
	require.NoError(t, err)
}

func TestAnthropicHealthCheckSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewAnthropic("anthropic", srv.URL, "sk", []string{"claude-sonnet-4-20250514"}, 2*time.Second)
	err := p.HealthCheck(context.Background())
	require.NoError(t, err)
}

func TestAnthropicHealthCheckUnreachable(t *testing.T) {
	p := NewAnthropic("anthropic", "http://127.0.0.1:19999", "sk", []string{"claude-sonnet-4-20250514"}, 500*time.Millisecond)
	err := p.HealthCheck(context.Background())
	require.Error(t, err)
}
