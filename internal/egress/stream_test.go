package egress

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
