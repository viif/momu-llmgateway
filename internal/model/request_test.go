package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseStandardRequest(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":true}`)
	req, err := ParseStandardRequest(body)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", req.Model)
	require.True(t, req.Stream)
	require.Len(t, req.Messages, 1)
	require.Equal(t, "user", req.Messages[0].Role)
}

func TestStandardResponseToJSON(t *testing.T) {
	resp := &StandardResponse{ID: "chatcmpl-1", Model: "gpt-4o", Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}}}
	data, err := resp.ToJSON()
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, "chatcmpl-1", got["id"])
}

func TestParseStandardRequestEmptyBody(t *testing.T) {
	_, err := ParseStandardRequest([]byte{})
	require.Error(t, err)
}

func TestParseStandardRequestMalformedJSON(t *testing.T) {
	_, err := ParseStandardRequest([]byte(`{bad json`))
	require.Error(t, err)
}

func TestParseStandardRequestExtraFields(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"temperature":0.7,"max_tokens":100,"metadata":{"key":"val"}}`)
	req, err := ParseStandardRequest(body)
	require.NoError(t, err)
	require.NotNil(t, req.Temperature)
	require.InDelta(t, 0.7, *req.Temperature, 0.001)
	require.NotNil(t, req.MaxTokens)
	require.Equal(t, 100, *req.MaxTokens)
	require.Equal(t, "val", req.Metadata["key"])
}

func TestParseStandardRequestEmptyMessages(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[]}`)
	req, err := ParseStandardRequest(body)
	require.NoError(t, err)
	require.Len(t, req.Messages, 0)
}

func TestStandardResponseToJSONWithUsage(t *testing.T) {
	resp := &StandardResponse{
		ID:       "chatcmpl-2",
		Model:    "gpt-4o",
		Provider: "openai",
		Choices:  []Choice{},
		Usage:    Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}
	data, err := resp.ToJSON()
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, float64(30), got["usage"].(map[string]any)["total_tokens"])
}

func TestStreamChunkToJSON(t *testing.T) {
	chunk := StreamChunk{ID: "chunk-1", Delta: Delta{Role: "assistant", Content: "hi"}}
	data, err := chunk.ToJSON()
	require.NoError(t, err)
	require.Contains(t, string(data), "chunk-1")
	require.Contains(t, string(data), "hi")
}

func TestStreamChunkWithErrorToJSON(t *testing.T) {
	chunk := StreamChunk{Error: NewError(ErrCodeProviderError, "timeout")}
	data, err := chunk.ToJSON()
	require.NoError(t, err)
	require.Contains(t, string(data), ErrCodeProviderError)
}
