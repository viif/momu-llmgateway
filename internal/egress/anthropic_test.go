package egress

import (
	"context"
	"encoding/json"
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

func TestAnthropicSendSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, r.URL.Path, "/v1/messages")
		require.Equal(t, "sk", r.Header.Get("x-api-key"))
		require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		var reqBody map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		require.Equal(t, "You are helpful", reqBody["system"])
		messages := reqBody["messages"].([]interface{})
		require.Len(t, messages, 1)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "msg_1",
			"model": "claude-sonnet-4-20250514",
			"content": []map[string]string{
				{"type": "text", "text": "Hello there"},
			},
			"usage": map[string]int{
				"input_tokens":  10,
				"output_tokens": 5,
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropic("anthropic", srv.URL, "sk", []string{"claude-sonnet-4-20250514"}, 2*time.Second)
	req := &model.StandardRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []model.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "hi"},
		},
	}
	resp, err := p.Send(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "msg_1", resp.ID)
	require.Equal(t, "Hello there", resp.Choices[0].Message.Content)
	require.Equal(t, "anthropic", resp.Provider)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, 5, resp.Usage.CompletionTokens)
	require.Equal(t, 15, resp.Usage.TotalTokens)
}

func TestAnthropicSendHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Rate limit exceeded"}}`))
	}))
	defer srv.Close()

	p := NewAnthropic("anthropic", srv.URL, "sk", []string{"claude-sonnet-4-20250514"}, 2*time.Second)
	req := &model.StandardRequest{Model: "claude-sonnet-4-20250514",
		Messages: []model.Message{{Role: "user", Content: "hi"}}}
	_, err := p.Send(context.Background(), req)

	require.Error(t, err)
	me, ok := err.(*model.Error)
	require.True(t, ok)
	require.Equal(t, model.ErrCodeProviderError, me.Code)
}

func TestAnthropicSendStreamSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		events := [][]string{
			{"event: message_start", `data: {"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"claude-sonnet-4-20250514"}}`},
			{"event: content_block_delta", `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
			{"event: content_block_delta", `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`},
			{"event: message_stop", `data: {"type":"message_stop"}`},
		}
		for _, pair := range events {
			w.Write([]byte(pair[0] + "\n" + pair[1] + "\n\n"))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := NewAnthropic("anthropic", srv.URL, "sk", []string{"claude-sonnet-4-20250514"}, 2*time.Second)
	req := &model.StandardRequest{Model: "claude-sonnet-4-20250514",
		Messages: []model.Message{{Role: "user", Content: "hi"}}}
	ch, err := p.SendStream(context.Background(), req)

	require.NoError(t, err)
	var chunks []model.StreamChunk
	for c := range ch {
		require.Nil(t, c.Error)
		chunks = append(chunks, c)
	}
	require.Len(t, chunks, 4)
	require.Equal(t, "msg_1", chunks[0].ID)
	require.Equal(t, "Hello", chunks[1].Delta.Content)
	require.Equal(t, " world", chunks[2].Delta.Content)
	require.True(t, chunks[3].Done)
}

func TestAnthropicSendStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p := NewAnthropic("anthropic", srv.URL, "sk", []string{"claude-sonnet-4-20250514"}, 2*time.Second)
	req := &model.StandardRequest{Model: "claude-sonnet-4-20250514",
		Messages: []model.Message{{Role: "user", Content: "hi"}}}
	_, err := p.SendStream(context.Background(), req)

	require.Error(t, err)
	me, ok := err.(*model.Error)
	require.True(t, ok)
	require.Equal(t, model.ErrCodeProviderError, me.Code)
}
