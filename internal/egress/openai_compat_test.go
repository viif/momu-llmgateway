package egress

import (
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
