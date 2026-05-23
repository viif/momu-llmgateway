package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func TestLoadExpandsEnvVars(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	path := writeConfig(t, `
server:
  port: 8080
redis:
  addr: localhost:6379
auth:
  api_keys:
    - key: sk-local
      name: local
      rate_limit: 60
      allowed_models: ["*"]
providers:
  openai:
    type: openai
    base_url: https://api.openai.com/v1
    api_key: ${OPENAI_API_KEY}
    models: ["gpt-4o"]
    weight: 100
    timeout: 60s
routing:
  strategies: ["explicit", "cost_cascade"]
  cascade:
    default: ["gpt-4o"]
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, 8080, cfg.Server.Port)
	require.Equal(t, "sk-test", cfg.Providers["openai"].APIKey)
	require.Equal(t, 60*time.Second, cfg.Providers["openai"].Timeout)
}

func TestLoadBalancerConfig(t *testing.T) {
	path := writeConfig(t, `
server:
  port: 8080
redis:
  addr: localhost:6379
auth:
  api_keys:
    - key: sk-local
      name: local
      rate_limit: 60
      allowed_models: ["*"]
providers: {}
balancer:
  concurrency_penalty_coefficient: 3.0
  latency_penalty_coefficient: 2.5
  warmup_enabled: true
  warmup_duration: 30s
  health_window_size: 60s
  health_min_requests: 10
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, 3.0, cfg.Balancer.ConcurrencyPenaltyCoefficient)
	require.Equal(t, 2.5, cfg.Balancer.LatencyPenaltyCoefficient)
	require.True(t, cfg.Balancer.WarmupEnabled)
	require.Equal(t, 30*time.Second, cfg.Balancer.WarmupDuration)
	require.Equal(t, 60*time.Second, cfg.Balancer.HealthWindowSize)
	require.Equal(t, 10, cfg.Balancer.HealthMinRequests)
}
