package observability

import (
	"context"

	"github.com/google/uuid"
)

type requestIDKey struct{}

func NewRequestID() string { return uuid.NewString() }

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}
