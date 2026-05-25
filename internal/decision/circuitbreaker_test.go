package decision

import (
	"sync"
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

func TestCircuitBreakerHalfOpenAfterCooldown(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	require.Equal(t, StateOpen, cb.State())
	require.False(t, cb.Allow())

	time.Sleep(60 * time.Millisecond)
	require.True(t, cb.Allow())
	require.Equal(t, StateHalfOpen, cb.State())
}

func TestCircuitBreakerHalfOpenClosesAfterSuccess(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	require.Equal(t, StateOpen, cb.State())

	time.Sleep(60 * time.Millisecond)
	require.True(t, cb.Allow())
	require.Equal(t, StateHalfOpen, cb.State())

	cb.RecordSuccess()
	require.Equal(t, StateClosed, cb.State())
	require.True(t, cb.Allow())
}

func TestCircuitBreakerHalfOpenFailsBackToOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()

	time.Sleep(60 * time.Millisecond)
	require.True(t, cb.Allow())

	cb.RecordFailure()
	require.Equal(t, StateOpen, cb.State())
	require.False(t, cb.Allow())
}

func TestCircuitBreakerRecordSuccessResetsFailures(t *testing.T) {
	cb := NewCircuitBreaker(2, 30*time.Second)
	cb.RecordFailure()
	cb.RecordSuccess()
	require.Equal(t, StateClosed, cb.State())
	cb.RecordFailure()
	require.True(t, cb.Allow())
}

func TestCircuitBreakerConcurrentAllow(t *testing.T) {
	cb := NewCircuitBreaker(100, 10*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.Allow()
		}()
	}
	wg.Wait()
}

func TestCircuitBreakerConcurrentRecordFailure(t *testing.T) {
	cb := NewCircuitBreaker(100, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.RecordFailure()
		}()
	}
	wg.Wait()
	require.Equal(t, StateOpen, cb.State())
}
