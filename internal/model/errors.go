package model

import "fmt"

const (
	ErrCodeInvalidRequest    = "invalid_request"
	ErrCodeAuthentication    = "authentication_error"
	ErrCodeRateLimit         = "rate_limit_exceeded"
	ErrCodeModelNotFound     = "model_not_found"
	ErrCodeProviderError     = "provider_error"
	ErrCodeCircuitOpen       = "circuit_breaker_open"
	ErrCodeTimeout           = "timeout"
	ErrCodeFallbackExhausted = "fallback_exhausted"
	ErrCodeInternal          = "internal_error"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message, Type: code}
}
