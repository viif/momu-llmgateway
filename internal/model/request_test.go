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
