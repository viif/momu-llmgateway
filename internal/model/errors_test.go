package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewError(t *testing.T) {
	e := NewError(ErrCodeInvalidRequest, "bad input")
	require.Equal(t, ErrCodeInvalidRequest, e.Code)
	require.Equal(t, "bad input", e.Message)
	require.Equal(t, ErrCodeInvalidRequest, e.Type)
	require.Equal(t, "invalid_request: bad input", e.Error())
}

func TestErrorJSONRoundTrip(t *testing.T) {
	e := NewError(ErrCodeAuthentication, "invalid key")
	data, err := json.Marshal(e)
	require.NoError(t, err)

	var decoded Error
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, e.Code, decoded.Code)
	require.Equal(t, e.Message, decoded.Message)
}

func TestAllErrorCodes(t *testing.T) {
	codes := []string{
		ErrCodeInvalidRequest,
		ErrCodeAuthentication,
		ErrCodeRateLimit,
		ErrCodeModelNotFound,
		ErrCodeProviderError,
		ErrCodeCircuitOpen,
		ErrCodeTimeout,
		ErrCodeFallbackExhausted,
		ErrCodeInternal,
	}
	for _, code := range codes {
		e := NewError(code, "test")
		require.Equal(t, code, e.Code)
		require.Equal(t, code, e.Type)
		require.Contains(t, e.Error(), code)
	}
}
