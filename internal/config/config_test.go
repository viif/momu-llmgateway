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
    type: openai_compat
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
