package decision

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCircuitBreakerOpensAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Minute)
	require.True(t, cb.Allow())
	cb.RecordFailure()
	cb.RecordFailure()
	require.False(t, cb.Allow())
	require.Equal(t, StateOpen, cb.State())
}
