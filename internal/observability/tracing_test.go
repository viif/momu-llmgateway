package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestIDContext(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-1")
	require.Equal(t, "req-1", RequestIDFromContext(ctx))
	require.NotEmpty(t, NewRequestID())
}
