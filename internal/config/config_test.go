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

func TestLoadEnrichedRoutingConfig(t *testing.T) {
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
routing:
  strategies: ["explicit", "capability", "semantic", "cost_cascade"]
  rules:
    - task_type: "long_context"
      condition: "input_tokens > 100000"
      target_models: ["claude-sonnet-4-20250514", "deepseek-chat"]
    - task_type: "reasoning"
      condition: "input_tokens > 4000"
      target_models: ["deepseek-reasoner", "gpt-4o"]
    - task_type: "fast_response"
      condition: "input_tokens < 500"
      target_models: ["gpt-4o-mini", "glm-4-flash"]
  cascade:
    default: ["deepseek-chat", "gpt-4o-mini", "gpt-4o"]
    gpt-4o: ["gpt-4o-mini", "deepseek-chat"]
    claude-sonnet-4-20250514: ["deepseek-chat", "gpt-4o"]
    deepseek-reasoner: ["deepseek-chat", "gpt-4o-mini"]
semantic_routing:
  similarity_threshold: 0.75
  categories:
    - name: "code_generation"
      target_models: ["deepseek-chat", "gpt-4o"]
      exemplars:
        - "Write a Python function that..."
        - "帮我写一个 REST API 接口"
    - name: "creative_writing"
      target_models: ["claude-sonnet-4-20250514", "gpt-4o"]
      exemplars:
        - "Write a story about..."
        - "写一篇关于春天的散文"
    - name: "data_analysis"
      target_models: ["gpt-4o", "deepseek-chat"]
      exemplars:
        - "Analyze this dataset..."
        - "分析这些日志中的错误分布"
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	require.Len(t, cfg.Routing.Rules, 3)
	require.Equal(t, "long_context", cfg.Routing.Rules[0].TaskType)
	require.Equal(t, "input_tokens > 100000", cfg.Routing.Rules[0].Condition)
	require.Equal(t, []string{"claude-sonnet-4-20250514", "deepseek-chat"}, cfg.Routing.Rules[0].TargetModels)

	require.Equal(t, "reasoning", cfg.Routing.Rules[1].TaskType)
	require.Equal(t, "input_tokens > 4000", cfg.Routing.Rules[1].Condition)
	require.Equal(t, []string{"deepseek-reasoner", "gpt-4o"}, cfg.Routing.Rules[1].TargetModels)

	require.Equal(t, "fast_response", cfg.Routing.Rules[2].TaskType)
	require.Equal(t, "input_tokens < 500", cfg.Routing.Rules[2].Condition)
	require.Equal(t, []string{"gpt-4o-mini", "glm-4-flash"}, cfg.Routing.Rules[2].TargetModels)

	require.Len(t, cfg.Routing.Cascade, 4)
	require.Equal(t, []string{"deepseek-chat", "gpt-4o-mini", "gpt-4o"}, cfg.Routing.Cascade["default"])
	require.Equal(t, []string{"gpt-4o-mini", "deepseek-chat"}, cfg.Routing.Cascade["gpt-4o"])
	require.Equal(t, []string{"deepseek-chat", "gpt-4o"}, cfg.Routing.Cascade["claude-sonnet-4-20250514"])
	require.Equal(t, []string{"deepseek-chat", "gpt-4o-mini"}, cfg.Routing.Cascade["deepseek-reasoner"])

	require.Equal(t, 0.75, cfg.SemanticRouting.SimilarityThreshold)
	require.Len(t, cfg.SemanticRouting.Categories, 3)

	require.Equal(t, "code_generation", cfg.SemanticRouting.Categories[0].Name)
	require.Equal(t, []string{"deepseek-chat", "gpt-4o"}, cfg.SemanticRouting.Categories[0].TargetModels)
	require.Len(t, cfg.SemanticRouting.Categories[0].Exemplars, 2)

	require.Equal(t, "creative_writing", cfg.SemanticRouting.Categories[1].Name)
	require.Equal(t, []string{"claude-sonnet-4-20250514", "gpt-4o"}, cfg.SemanticRouting.Categories[1].TargetModels)
	require.Len(t, cfg.SemanticRouting.Categories[1].Exemplars, 2)

	require.Equal(t, "data_analysis", cfg.SemanticRouting.Categories[2].Name)
	require.Equal(t, []string{"gpt-4o", "deepseek-chat"}, cfg.SemanticRouting.Categories[2].TargetModels)
	require.Len(t, cfg.SemanticRouting.Categories[2].Exemplars, 2)
}
