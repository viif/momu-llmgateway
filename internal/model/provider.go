package model

import "context"

type Provider interface {
	Name() string
	Send(ctx context.Context, req *StandardRequest) (*StandardResponse, error)
	SendStream(ctx context.Context, req *StandardRequest) (<-chan StreamChunk, error)
	Models() []string
}

type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}
