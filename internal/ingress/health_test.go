package ingress

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/cache"
	"github.com/viif/momu-llmgateway/internal/egress"
	"github.com/viif/momu-llmgateway/internal/model"
)

type pingableStore struct {
	err error
}

func (s *pingableStore) Ping(ctx context.Context) error { return s.err }
func (s *pingableStore) Save(ctx context.Context, model, key string, vector []float64, respJSON []byte, ttl time.Duration) error {
	return nil
}
func (s *pingableStore) LoadAll(ctx context.Context, model string) ([]cache.CacheEntry, error) {
	return nil, nil
}
func (s *pingableStore) Close() error { return nil }

type healthyProvider struct{ name string }

func (p *healthyProvider) Name() string     { return p.name }
func (p *healthyProvider) Models() []string { return nil }
func (p *healthyProvider) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	return nil, nil
}
func (p *healthyProvider) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return nil, nil
}
func (p *healthyProvider) HealthCheck(ctx context.Context) error { return nil }

type unhealthyProvider struct{ name string }

func (p *unhealthyProvider) Name() string     { return p.name }
func (p *unhealthyProvider) Models() []string { return nil }
func (p *unhealthyProvider) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	return nil, nil
}
func (p *unhealthyProvider) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return nil, nil
}
func (p *unhealthyProvider) HealthCheck(ctx context.Context) error {
	return errors.New("connection refused")
}

func TestHealthCheckerAllHealthy(t *testing.T) {
	store := &pingableStore{}
	reg := egress.NewRegistry()
	reg.Register(&healthyProvider{name: "openai"})
	reg.Register(&healthyProvider{name: "anthropic"})

	checker := NewHealthChecker(store, reg)
	result := checker.Check(context.Background())

	require.Equal(t, "ok", result.Status)
	require.Equal(t, "ok", result.Checks["redis"].Status)
	require.Equal(t, "ok", result.Checks["openai"].Status)
	require.Equal(t, "ok", result.Checks["anthropic"].Status)
}

func TestHealthCheckerDegraded(t *testing.T) {
	store := &pingableStore{err: errors.New("redis down")}
	reg := egress.NewRegistry()
	reg.Register(&unhealthyProvider{name: "openai"})

	checker := NewHealthChecker(store, reg)
	result := checker.Check(context.Background())

	require.Equal(t, "degraded", result.Status)
	require.Equal(t, "error", result.Checks["redis"].Status)
	require.Equal(t, "redis down", result.Checks["redis"].Error)
	require.Equal(t, "error", result.Checks["openai"].Status)
	require.Equal(t, "connection refused", result.Checks["openai"].Error)
}

func TestHealthCheckerNilStore(t *testing.T) {
	reg := egress.NewRegistry()
	reg.Register(&healthyProvider{name: "openai"})

	checker := NewHealthChecker(nil, reg)
	result := checker.Check(context.Background())

	require.Equal(t, "ok", result.Status)
	require.Equal(t, "not_configured", result.Checks["redis"].Status)
	require.Equal(t, "ok", result.Checks["openai"].Status)
}
