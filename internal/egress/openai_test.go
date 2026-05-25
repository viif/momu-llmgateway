package egress

import (
	"context"
	"encoding/json"
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

func TestOpenAICompatibleSendSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, r.URL.Path, "/chat/completions")
		require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var reqBody map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		require.Equal(t, "gpt-4o", reqBody["model"])
		require.False(t, reqBody["stream"].(bool))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(model.StandardResponse{
			ID:      "chatcmpl-1",
			Model:   "gpt-4o",
			Choices: []model.Choice{{Index: 0, Message: model.Message{Role: "assistant", Content: "Hello"}, FinishReason: "stop"}},
			Usage:   model.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		})
	}))
	defer srv.Close()

	p := NewOpenAICompatible("openai", srv.URL, "sk-test", []string{"gpt-4o"}, 2*time.Second)
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	resp, err := p.Send(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "chatcmpl-1", resp.ID)
	require.Equal(t, "openai", resp.Provider)
	require.Equal(t, "Hello", resp.Choices[0].Message.Content)
	require.Equal(t, 5, resp.Usage.PromptTokens)
}

func TestOpenAICompatibleSendHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatible("openai", srv.URL, "sk-test", []string{"gpt-4o"}, 2*time.Second)
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	_, err := p.Send(context.Background(), req)

	require.Error(t, err)
	me, ok := err.(*model.Error)
	require.True(t, ok)
	require.Equal(t, model.ErrCodeProviderError, me.Code)
	require.Contains(t, me.Message, "429")
}

func TestOpenAICompatibleSendUnreachable(t *testing.T) {
	p := NewOpenAICompatible("openai", "http://127.0.0.1:19999", "sk-test", []string{"gpt-4o"}, 100*time.Millisecond)
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	_, err := p.Send(context.Background(), req)
	require.Error(t, err)
}

func TestOpenAICompatibleSendStreamSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		chunks := []string{
			`data: {"id":"1","model":"gpt-4o","choices":[{"delta":{"role":"assistant"}}]}`,
			`data: {"id":"1","model":"gpt-4o","choices":[{"delta":{"content":"Hi"}}]}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			w.Write([]byte(c + "\n\n"))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := NewOpenAICompatible("openai", srv.URL, "sk-test", []string{"gpt-4o"}, 2*time.Second)
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	ch, err := p.SendStream(context.Background(), req)

	require.NoError(t, err)
	var chunks []model.StreamChunk
	for c := range ch {
		require.Nil(t, c.Error)
		chunks = append(chunks, c)
	}
	require.Len(t, chunks, 3)
	require.Equal(t, "assistant", chunks[0].Delta.Role)
	require.Equal(t, "Hi", chunks[1].Delta.Content)
	require.True(t, chunks[2].Done)
}

func TestOpenAICompatibleSendStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := NewOpenAICompatible("openai", srv.URL, "sk-test", []string{"gpt-4o"}, 2*time.Second)
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	_, err := p.SendStream(context.Background(), req)

	require.Error(t, err)
	me, ok := err.(*model.Error)
	require.True(t, ok)
	require.Equal(t, model.ErrCodeProviderError, me.Code)
}
