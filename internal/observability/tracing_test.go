package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestIDContext(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-1")
	require.Equal(t, "req-1", RequestIDFromContext(ctx))
}

func TestRequestIDFromContextEmpty(t *testing.T) {
	require.Empty(t, RequestIDFromContext(context.Background()))
}

func TestNewRequestIDMultipleUnique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := NewRequestID()
		require.NotEmpty(t, id)
		require.False(t, ids[id], "duplicate ID: %s", id)
		ids[id] = true
	}
}
