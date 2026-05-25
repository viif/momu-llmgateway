package fallback

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/viif/momu-llmgateway/internal/model"
)

const (
	defaultRetryMax        = 2
	defaultRetryBackoff    = time.Second
	defaultFallbackMessage = "所有模型服务暂时不可用，请稍后重试。"
)

var retryableErrors = []string{
	"connection refused",
	"connection reset",
	"i/o timeout",
	"deadline exceeded",
	"unexpected eof",
	"no such host",
	"timeout",
}

type Attempt struct {
	ProviderName string
	Model        string
	Level        string
}

type SendFunc func(ctx context.Context, providerName, modelName string) (*model.StandardResponse, error)

type Engine struct {
	chains       map[string][]string
	retryMax     int
	retryBackoff time.Duration
	defaultMsg   string
}

func NewEngine(chains map[string][]string, retryMax int, retryBackoff time.Duration, defaultMsg string) *Engine {
	if retryMax <= 0 {
		retryMax = defaultRetryMax
	}
	if retryBackoff <= 0 {
		retryBackoff = defaultRetryBackoff
	}
	if defaultMsg == "" {
		defaultMsg = defaultFallbackMessage
	}
	if chains == nil {
		chains = make(map[string][]string)
	}
	return &Engine{
		chains:       chains,
		retryMax:     retryMax,
		retryBackoff: retryBackoff,
		defaultMsg:   defaultMsg,
	}
}

func (e *Engine) Chain(model string) []string {
	chain := e.chains[model]
	out := make([]string, len(chain))
	copy(out, chain)
	return out
}

func (e *Engine) Attempts(providerName, model string) []Attempt {
	out := []Attempt{{ProviderName: providerName, Model: model, Level: "primary"}}
	for i := 0; i < e.retryMax; i++ {
		out = append(out, Attempt{
			ProviderName: providerName,
			Model:        model,
			Level:        "retry",
		})
	}
	for _, m := range e.Chain(model) {
		out = append(out, Attempt{Model: m, Level: "fallback"})
	}
	return out
}

func (e *Engine) DefaultResponse() *model.StandardResponse {
	return &model.StandardResponse{
		Choices: []model.Choice{{
			Index:        0,
			Message:      model.Message{Role: "assistant", Content: e.defaultMsg},
			FinishReason: "stop",
		}},
	}
}

func (e *Engine) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if me, ok := err.(*model.Error); ok {
		switch me.Code {
		case model.ErrCodeInvalidRequest,
			model.ErrCodeAuthentication,
			model.ErrCodeRateLimit,
			model.ErrCodeModelNotFound:
			return false
		}
		if me.Code == model.ErrCodeProviderError ||
			me.Code == model.ErrCodeTimeout ||
			me.Code == model.ErrCodeCircuitOpen {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range retryableErrors {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

func (e *Engine) BackoffDuration(attempt int) time.Duration {
	return time.Duration(float64(e.retryBackoff) * math.Pow(2, float64(attempt)))
}

func (e *Engine) Execute(ctx context.Context, providerName, modelName string, sendFn SendFunc) (*model.StandardResponse, string, error) {
	if sendFn == nil {
		return nil, "", model.NewError(model.ErrCodeInternal, "send function is nil")
	}

	retryAttempt := 0
	skipRetries := false
	for _, att := range e.Attempts(providerName, modelName) {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if att.Level == "retry" {
			if skipRetries {
				continue
			}
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(e.BackoffDuration(retryAttempt)):
			}
			retryAttempt++
		}
		resp, err := sendFn(ctx, att.ProviderName, att.Model)
		if err == nil {
			return resp, att.Level, nil
		}
		if att.Level == "primary" && !e.IsRetryable(err) {
			skipRetries = true
			continue
		}
	}

	return e.DefaultResponse(), "fallback_exhausted", nil
}
