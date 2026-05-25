package fallback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestChainReturnsDefensiveCopy(t *testing.T) {
	e := NewEngine(map[string][]string{
		"gpt-4o": {"claude-sonnet-4-20250514", "gpt-4o-mini"},
	}, 2, time.Second, "")

	chain := e.Chain("gpt-4o")
	require.Equal(t, []string{"claude-sonnet-4-20250514", "gpt-4o-mini"}, chain)
	require.Empty(t, e.Chain("unknown"))

	chain[0] = "modified"
	require.Equal(t, "claude-sonnet-4-20250514", e.Chain("gpt-4o")[0])
}

func TestAttemptsIncludesRetriesAndChain(t *testing.T) {
	e := NewEngine(map[string][]string{
		"gpt-4o": {"claude-sonnet-4-20250514", "gpt-4o-mini"},
	}, 2, time.Second, "")

	attempts := e.Attempts("openai", "gpt-4o")
	require.Len(t, attempts, 5)

	require.Equal(t, "primary", attempts[0].Level)
	require.Equal(t, "openai", attempts[0].ProviderName)
	require.Equal(t, "gpt-4o", attempts[0].Model)

	require.Equal(t, "retry", attempts[1].Level)
	require.Equal(t, attempts[0].ProviderName, attempts[1].ProviderName)
	require.Equal(t, attempts[0].Model, attempts[1].Model)

	require.Equal(t, "retry", attempts[2].Level)

	require.Equal(t, "fallback", attempts[3].Level)
	require.Equal(t, "", attempts[3].ProviderName)

	require.Equal(t, "fallback", attempts[4].Level)
}

func TestAttemptsNoChainDefaultRetries(t *testing.T) {
	e := NewEngine(nil, 0, 0, "")
	attempts := e.Attempts("openai", "gpt-4o")
	require.Len(t, attempts, 3)
}

func TestAttemptsCustomRetryCount(t *testing.T) {
	e := NewEngine(nil, 5, 0, "")
	attempts := e.Attempts("openai", "gpt-4o")
	require.Len(t, attempts, 6)
}

func TestAttemptsEmptyChainsNil(t *testing.T) {
	e := NewEngine(nil, 2, time.Second, "")
	attempts := e.Attempts("openai", "no-fallback-model")
	require.Len(t, attempts, 3)
}

func TestDefaultResponse(t *testing.T) {
	e := NewEngine(nil, 0, 0, "所有服务暂时不可用，请稍后重试。")
	resp := e.DefaultResponse()
	require.Len(t, resp.Choices, 1)
	require.Equal(t, "assistant", resp.Choices[0].Message.Role)
	require.Equal(t, "所有服务暂时不可用，请稍后重试。", resp.Choices[0].Message.Content)
	require.Equal(t, "stop", resp.Choices[0].FinishReason)
}

func TestDefaultResponseBuiltin(t *testing.T) {
	e := NewEngine(nil, 0, 0, "")
	resp := e.DefaultResponse()
	require.NotEmpty(t, resp.Choices[0].Message.Content)
}

func TestIsRetryable(t *testing.T) {
	e := NewEngine(nil, 0, 0, "")
	require.True(t, e.IsRetryable(errors.New("connection refused")))
	require.True(t, e.IsRetryable(errors.New("read tcp: i/o timeout")))
	require.True(t, e.IsRetryable(errors.New("context deadline exceeded")))
	require.True(t, e.IsRetryable(errors.New("unexpected EOF")))
	require.False(t, e.IsRetryable(nil))
	require.False(t, e.IsRetryable(model.NewError(model.ErrCodeInvalidRequest, "bad")))
	require.False(t, e.IsRetryable(model.NewError(model.ErrCodeAuthentication, "unauthorized")))
}

func TestBackoffDuration(t *testing.T) {
	e := NewEngine(nil, 2, 500*time.Millisecond, "")
	require.Equal(t, 500*time.Millisecond, e.BackoffDuration(0))
	require.Equal(t, 1*time.Second, e.BackoffDuration(1))
	require.Equal(t, 2*time.Second, e.BackoffDuration(2))
}

func TestExecutePrimarySuccess(t *testing.T) {
	e := NewEngine(nil, 2, time.Millisecond, "fallback")
	sendCalled := false
	sendFn := func(ctx context.Context, provider, m string) (*model.StandardResponse, error) {
		sendCalled = true
		return &model.StandardResponse{ID: "ok", Model: m, Provider: provider}, nil
	}
	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.True(t, sendCalled)
	require.Equal(t, "ok", resp.ID)
	require.Equal(t, "primary", level)
}

func TestExecuteRetryOnFailure(t *testing.T) {
	e := NewEngine(nil, 2, time.Millisecond, "fallback")
	callCount := 0
	sendFn := func(ctx context.Context, provider, m string) (*model.StandardResponse, error) {
		callCount++
		if callCount <= 2 {
			return nil, errors.New("connection refused")
		}
		return &model.StandardResponse{ID: "recovered"}, nil
	}
	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.Equal(t, 3, callCount)
	require.Equal(t, "retry", level)
	require.Equal(t, "recovered", resp.ID)
}

func TestExecuteNonRetryableSkipsRetries(t *testing.T) {
	e := NewEngine(map[string][]string{"gpt-4o": {"gpt-4o-mini"}}, 2, time.Millisecond, "fallback")
	callCount := 0
	sendFn := func(ctx context.Context, provider, m string) (*model.StandardResponse, error) {
		callCount++
		if m == "gpt-4o" {
			return nil, model.NewError(model.ErrCodeInvalidRequest, "bad request")
		}
		return &model.StandardResponse{ID: "fallback-ok"}, nil
	}
	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.Equal(t, 2, callCount)
	require.Equal(t, "fallback", level)
	require.Equal(t, "fallback-ok", resp.ID)
}

func TestExecuteChainFallback(t *testing.T) {
	e := NewEngine(map[string][]string{
		"gpt-4o": {"gpt-4o-mini", "deepseek-chat"},
	}, 0, time.Millisecond, "unavailable")

	callCount := 0
	sendFn := func(ctx context.Context, provider, m string) (*model.StandardResponse, error) {
		callCount++
		if m == "gpt-4o" || m == "gpt-4o-mini" {
			return nil, errors.New("timeout")
		}
		return &model.StandardResponse{ID: "deepseek-ok", Model: m, Provider: provider}, nil
	}

	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.Equal(t, 5, callCount)
	require.Equal(t, "fallback", level)
	require.Equal(t, "deepseek-ok", resp.ID)
}

func TestExecuteExhaustedReturnsDefault(t *testing.T) {
	e := NewEngine(map[string][]string{
		"gpt-4o": {"gpt-4o-mini"},
	}, 0, time.Millisecond, "all providers down")

	sendFn := func(ctx context.Context, provider, m string) (*model.StandardResponse, error) {
		return nil, errors.New("timeout")
	}

	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.Equal(t, "fallback_exhausted", level)
	require.Equal(t, "all providers down", resp.Choices[0].Message.Content)
}

func TestExecuteContextCancelled(t *testing.T) {
	e := NewEngine(map[string][]string{"gpt-4o": {"gpt-4o-mini"}}, 2, time.Millisecond, "fallback")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sendFn := func(ctx context.Context, provider, m string) (*model.StandardResponse, error) {
		return nil, ctx.Err()
	}

	_, _, err := e.Execute(ctx, "openai", "gpt-4o", sendFn)
	require.Error(t, err)
}

func TestExecuteEmptySendFunc(t *testing.T) {
	e := NewEngine(nil, 0, 0, "fallback")
	_, _, err := e.Execute(context.Background(), "openai", "gpt-4o", nil)
	require.Error(t, err)
}
