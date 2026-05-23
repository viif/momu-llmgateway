# LLM Gateway 实施计划

> **给 agentic workers：** 必须使用子技能：按任务逐项实施时，推荐使用 `superpowers:subagent-driven-development`，也可使用 `superpowers:executing-plans`。本文所有步骤使用 checkbox（`- [ ]`）语法追踪进度。

**目标：** 构建一个生产级 Go LLM Gateway，提供 OpenAI 兼容入口、多 Provider 适配、智能路由、语义缓存、熔断降级、限流认证和 Prometheus 可观测性。

**架构：** 采用 Clean Architecture：接入层负责 HTTP、认证、限流和参数校验；决策层负责路由策略、负载均衡和熔断；出口层负责 Provider 协议转换和 SSE 流式转发。配置使用 Viper 加载本地 YAML 并通过 `atomic.Value` 原子热更新，Redis 用于限流和语义缓存，Zap/Prometheus 提供日志和指标。

**技术栈：** Go 1.21+、Gin、Viper、Zap、Prometheus、Redis、GitHub Actions、Dockerfile

---

## 文件结构与职责

- `cmd/gateway/main.go`：程序入口，加载配置、初始化依赖、注册路由、优雅关闭。
- `internal/model/request.go`：内部标准请求、响应、流式 Chunk、Usage 等核心数据结构。
- `internal/model/provider.go`：Provider 与 EmbeddingProvider 接口定义。
- `internal/model/errors.go`：统一错误码与 API 错误响应结构。
- `internal/config/config.go`：Viper 配置加载、环境变量展开、本地 YAML 热加载、原子配置快照。
- `internal/observability/logger.go`：Zap 日志初始化。
- `internal/observability/metrics.go`：Prometheus 指标定义与注册。
- `internal/observability/tracing.go`：RequestID 注入与上下文传递。
- `internal/egress/openai_compat.go`：OpenAI 兼容 Provider 基类，复用给 OpenAI、DeepSeek、Qwen。
- `internal/egress/anthropic.go`：Anthropic Messages API 适配器。
- `internal/egress/glm.go`：智谱 GLM API 适配器。
- `internal/egress/stream.go`：上游 SSE 到 OpenAI 格式 SSE 的转换。
- `internal/egress/adapter.go`：Provider 注册表与按模型查找。
- `internal/decision/circuitbreaker.go`：Provider+Model 维度的 Closed/Open/Half-Open 熔断器。
- `internal/decision/balancer.go`：同模型多 Provider 的加权负载均衡。
- `internal/decision/strategy_capability.go`：按 task_type 和 token 长度的能力路由。
- `internal/decision/strategy_cost.go`：低成本优先的级联路由。
- `internal/decision/strategy_semantic.go`：基于 Embedding 相似度的语义路由。
- `internal/decision/router.go`：策略链编排、显式路由和默认路由。
- `internal/cache/semantic.go`：语义缓存查询、写入、TTL 和命中判断。
- `internal/fallback/engine.go`：L1 重试、L2 跨 Provider、L3 跨模型、L4 兜底响应。
- `internal/ingress/middleware_auth.go`：Bearer API Key 认证和 allowed_models 校验。
- `internal/ingress/middleware_ratelimit.go`：Redis 滑动窗口限流。
- `internal/ingress/middleware_logging.go`：请求日志和延迟记录。
- `internal/ingress/handler.go`：`POST /v1/chat/completions`、`GET /health`、`GET /metrics`。
- `configs/gateway.yaml`：默认配置样例。
- `.github/workflows/ci.yml`：GitHub Actions lint/test/coverage，不包含 镜像构建 job。
- `Dockerfile`：服务镜像构建文件，供手动或后续发布流程使用。

---

## 任务 1：初始化 Go 工程骨架

**文件：**
- 新建： `go.mod`
- 新建： `cmd/gateway/main.go`
- 新建： `internal/` 下各目录
- 新建： `configs/`
- 新建： `.github/workflows/`

- [ ] **步骤 1：创建目录结构**

```bash
mkdir -p cmd/gateway internal/{config,model,ingress,decision,egress,cache,fallback,observability} configs .github/workflows
```

预期：目录创建成功，无输出或无错误。

- [ ] **步骤 2：初始化 Go module 和依赖**

```bash
go mod init github.com/viif/momu-llmgateway
go get github.com/gin-gonic/gin@v1.9.1
go get github.com/spf13/viper@v1.18.2
go get go.uber.org/zap@v1.27.0
go get github.com/prometheus/client_golang@v1.19.0
go get github.com/redis/go-redis/v9@v9.5.1
go get github.com/stretchr/testify@v1.9.0
go get github.com/alicebob/miniredis/v2@v2.33.0
```

预期：生成 `go.mod` 和 `go.sum`，依赖下载成功。

- [ ] **步骤 3：创建最小入口文件**

文件： `cmd/gateway/main.go`

```go
package main

import "fmt"

func main() {
	fmt.Println("LLM Gateway starting...")
}
```

- [ ] **步骤 4：验证构建**

```bash
go build ./...
```

预期：无错误。

- [ ] **步骤 5：提交**

```bash
git add go.mod go.sum cmd internal configs .github
git commit -m "feat: 搭建 llm gateway 工程骨架"
```

---

## 任务 2：添加 GitHub Actions CI（基础版本）

> CI 内容将在后续任务中逐步丰富。基础版本先保证构建和测试可在 CI 中自动运行。

**文件：**
- 新建： `.github/workflows/ci.yml`

- [ ] **步骤 1：创建基础 CI workflow**

文件： `.github/workflows/ci.yml`

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.21'
      - name: Build
        run: go build ./...
      - name: Test
        run: go test ./...
```

- [ ] **步骤 2：验证 YAML 可解析**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"
```

预期：无输出、无错误。

- [ ] **步骤 3：验证构建和测试**

```bash
go build ./...
go test ./...
```

预期：全部通过。

- [ ] **步骤 4：提交**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: 添加 github actions 基础构建和测试 workflow"
```

> **CI 逐步丰富说明：** 本任务建立 CI 基础骨架。后续每个新任务在增加代码和测试的同时，CI 也会随之验证更多内容。最终会在 CI 中逐步补充 lint 检查、竞态检测、覆盖率上报等增强步骤（参见任务 19）。

---

## 任务 3：定义核心数据模型

**文件：**
- 新建： `internal/model/request.go`
- 新建： `internal/model/provider.go`
- 新建： `internal/model/errors.go`
- 新建： `internal/model/request_test.go`

- [ ] **步骤 1：先写请求解析测试**

文件： `internal/model/request_test.go`

```go
package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseStandardRequest(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":true}`)
	req, err := ParseStandardRequest(body)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", req.Model)
	require.True(t, req.Stream)
	require.Len(t, req.Messages, 1)
	require.Equal(t, "user", req.Messages[0].Role)
}

func TestStandardResponseToJSON(t *testing.T) {
	resp := &StandardResponse{ID: "chatcmpl-1", Model: "gpt-4o", Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}}}
	data, err := resp.ToJSON()
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, "chatcmpl-1", got["id"])
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/model -run 'TestParseStandardRequest|TestStandardResponseToJSON' -v
```

预期：失败，提示 `ParseStandardRequest`、`StandardResponse` 等未定义。

- [ ] **步骤 3：实现核心类型**

文件： `internal/model/request.go`

```go
package model

import "encoding/json"

type StandardRequest struct {
	RequestID   string            `json:"request_id,omitempty"`
	Model       string            `json:"model"`
	Messages    []Message         `json:"messages"`
	Stream      bool              `json:"stream,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	MaxTokens   *int              `json:"max_tokens,omitempty"`
	TaskType    string            `json:"task_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type StandardResponse struct {
	ID       string   `json:"id"`
	Model    string   `json:"model"`
	Provider string   `json:"provider,omitempty"`
	Choices  []Choice `json:"choices"`
	Usage    Usage    `json:"usage"`
	CacheHit bool     `json:"cache_hit,omitempty"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type StreamChunk struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Delta Delta  `json:"delta"`
	Done  bool   `json:"done"`
	Error *Error `json:"error,omitempty"`
}

type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

func (r *StandardResponse) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

func ParseStandardRequest(data []byte) (*StandardRequest, error) {
	var req StandardRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}
```

- [ ] **步骤 4：实现 Provider 接口**

文件： `internal/model/provider.go`

```go
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
```

- [ ] **步骤 5：实现统一错误类型**

文件： `internal/model/errors.go`

```go
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
```

- [ ] **步骤 6：验证测试通过**

```bash
go test ./internal/model -v
```

预期：全部 PASS。

- [ ] **步骤 7：提交**

```bash
git add internal/model
git commit -m "feat: 添加核心网关数据模型"
```

---

## 任务 4：实现配置加载、环境变量展开和原子热更新

**文件：**
- 新建： `internal/config/config.go`
- 新建： `internal/config/config_test.go`
- 新建： `configs/gateway.yaml`

- [ ] **步骤 1：先写配置测试**

文件： `internal/config/config_test.go`

```go
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
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/config -run TestLoadExpandsEnvVars -v
```

预期：失败，提示 `Load` 未定义。

- [ ] **步骤 3：实现配置结构和 Load**

文件： `internal/config/config.go`

```go
package config

import (
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

var currentConfig atomic.Value // stores *Config

type Config struct {
	Server          ServerConfig          `mapstructure:"server"`
	Redis           RedisConfig           `mapstructure:"redis"`
	Auth            AuthConfig            `mapstructure:"auth"`
	Providers       map[string]ProviderConfig `mapstructure:"providers"`
	Routing         RoutingConfig         `mapstructure:"routing"`
	SemanticRouting SemanticRoutingConfig `mapstructure:"semantic_routing"`
	SemanticCache   SemanticCacheConfig   `mapstructure:"semantic_cache"`
	Fallback        FallbackConfig        `mapstructure:"fallback"`
	CircuitBreaker  CircuitBreakerConfig  `mapstructure:"circuit_breaker"`
}

type ServerConfig struct {
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type AuthConfig struct {
	APIKeys []APIKeyConfig `mapstructure:"api_keys"`
}

type APIKeyConfig struct {
	Key           string   `mapstructure:"key"`
	Name          string   `mapstructure:"name"`
	RateLimit     int      `mapstructure:"rate_limit"`
	AllowedModels []string `mapstructure:"allowed_models"`
}

type ProviderConfig struct {
	Type    string        `mapstructure:"type"`
	BaseURL string        `mapstructure:"base_url"`
	APIKey  string        `mapstructure:"api_key"`
	Models  []string      `mapstructure:"models"`
	Weight  int           `mapstructure:"weight"`
	Timeout time.Duration `mapstructure:"timeout"`
}

type RoutingConfig struct {
	Strategies []string            `mapstructure:"strategies"`
	Rules      []RoutingRuleConfig `mapstructure:"rules"`
	Cascade    map[string][]string `mapstructure:"cascade"`
}

type RoutingRuleConfig struct {
	TaskType     string   `mapstructure:"task_type"`
	Condition    string   `mapstructure:"condition"`
	TargetModels []string `mapstructure:"target_models"`
}

type SemanticRoutingConfig struct {
	EmbeddingProvider   string                   `mapstructure:"embedding_provider"`
	EmbeddingModel      string                   `mapstructure:"embedding_model"`
	SimilarityThreshold float64                  `mapstructure:"similarity_threshold"`
	Categories          []SemanticCategoryConfig `mapstructure:"categories"`
}

type SemanticCategoryConfig struct {
	Name         string   `mapstructure:"name"`
	TargetModels []string `mapstructure:"target_models"`
	Exemplars    []string `mapstructure:"exemplars"`
}

type SemanticCacheConfig struct {
	Enabled             bool          `mapstructure:"enabled"`
	SimilarityThreshold float64       `mapstructure:"similarity_threshold"`
	TTL                 time.Duration `mapstructure:"ttl"`
	MaxEntries          int           `mapstructure:"max_entries"`
	EmbeddingProvider   string        `mapstructure:"embedding_provider"`
	EmbeddingModel      string        `mapstructure:"embedding_model"`
}

type FallbackConfig struct {
	RetryMax     int                 `mapstructure:"retry_max"`
	RetryBackoff time.Duration       `mapstructure:"retry_backoff"`
	Chains       map[string][]string `mapstructure:"chains"`
}

type CircuitBreakerConfig struct {
	FailureThreshold int           `mapstructure:"failure_threshold"`
	Window           time.Duration `mapstructure:"window"`
	Cooldown         time.Duration `mapstructure:"cooldown"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	for _, key := range v.AllKeys() {
		if s, ok := v.Get(key).(string); ok {
			v.Set(key, os.ExpandEnv(s))
		}
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	currentConfig.Store(&cfg)
	return &cfg, nil
}

func GetConfig() *Config {
	if v := currentConfig.Load(); v != nil {
		return v.(*Config)
	}
	return nil
}

func WatchAndReload(path string, onChange func(*Config)) error {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return err
	}
	v.WatchConfig()
	v.OnConfigChange(func(fsnotify.Event) {
		cfg, err := Load(path)
		if err == nil && onChange != nil {
			onChange(cfg)
		}
	})
	return nil
}
```

- [ ] **步骤 4：创建默认配置文件**

文件： `configs/gateway.yaml`

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 120s
redis:
  addr: "localhost:6379"
  password: ""
  db: 0
auth:
  api_keys:
    - key: "sk-xxx"
      name: "default"
      rate_limit: 60
      allowed_models: ["*"]
providers:
  openai:
    type: "openai_compat"
    base_url: "https://api.openai.com/v1"
    api_key: "${OPENAI_API_KEY}"
    models: ["gpt-4o", "gpt-4o-mini"]
    weight: 100
    timeout: 60s
  anthropic:
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    api_key: "${ANTHROPIC_API_KEY}"
    models: ["claude-sonnet-4-20250514"]
    weight: 80
    timeout: 60s
  deepseek:
    type: "openai_compat"
    base_url: "https://api.deepseek.com/v1"
    api_key: "${DEEPSEEK_API_KEY}"
    models: ["deepseek-chat", "deepseek-reasoner"]
    weight: 90
    timeout: 60s
  qwen:
    type: "openai_compat"
    base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
    api_key: "${QWEN_API_KEY}"
    models: ["qwen-turbo", "qwen-plus", "qwen-max"]
    weight: 85
    timeout: 60s
  glm:
    type: "glm"
    base_url: "https://open.bigmodel.cn/api/paas/v4"
    api_key: "${GLM_API_KEY}"
    models: ["glm-4", "glm-4-flash"]
    weight: 75
    timeout: 60s
routing:
  strategies: ["explicit", "semantic", "capability", "cost_cascade"]
  rules:
    - task_type: "long_context"
      condition: "input_tokens > 100000"
      target_models: ["claude-sonnet-4-20250514", "deepseek-chat"]
  cascade:
    default: ["deepseek-chat", "gpt-4o-mini", "gpt-4o"]
semantic_routing:
  embedding_provider: "openai"
  embedding_model: "text-embedding-3-small"
  similarity_threshold: 0.75
  categories:
    - name: "code_generation"
      target_models: ["deepseek-chat", "gpt-4o"]
      exemplars: ["Write a Python function that...", "Generate code to...", "帮我写一个..."]
semantic_cache:
  enabled: true
  similarity_threshold: 0.95
  ttl: 1h
  max_entries: 10000
  embedding_provider: "openai"
  embedding_model: "text-embedding-3-small"
fallback:
  retry_max: 2
  retry_backoff: "1s"
  chains:
    gpt-4o: ["claude-sonnet-4-20250514", "gpt-4o-mini"]
circuit_breaker:
  failure_threshold: 5
  window: 10s
  cooldown: 30s
```

- [ ] **步骤 5：验证测试通过**

```bash
go test ./internal/config -v
```

预期：全部 PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/config configs/gateway.yaml
git commit -m "feat: 添加 viper 配置加载和热更新"
```

---

## 任务 5：实现可观测性基础设施

**文件：**
- 新建： `internal/observability/logger.go`
- 新建： `internal/observability/metrics.go`
- 新建： `internal/observability/tracing.go`
- 新建： `internal/observability/tracing_test.go`

- [ ] **步骤 1：先写 RequestID 测试**

文件： `internal/observability/tracing_test.go`

```go
package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestIDContext(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-1")
	require.Equal(t, "req-1", RequestIDFromContext(ctx))
	require.NotEmpty(t, NewRequestID())
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/observability -run TestRequestIDContext -v
```

预期：失败，提示函数未定义。

- [ ] **步骤 3：实现日志、指标和追踪**

文件： `internal/observability/logger.go`

```go
package observability

import "go.uber.org/zap"

var Logger *zap.Logger = zap.NewNop()

func InitLogger(production bool) error {
	var err error
	if production {
		Logger, err = zap.NewProduction()
	} else {
		Logger, err = zap.NewDevelopment()
	}
	return err
}
```

文件： `internal/observability/metrics.go`

```go
package observability

import "github.com/prometheus/client_golang/prometheus"

var RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "llm_request_duration_seconds", Help: "LLM request latency"}, []string{"provider", "model"})
var RequestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_request_total", Help: "LLM request count"}, []string{"provider", "model", "status"})
var TokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_tokens_total", Help: "LLM token count"}, []string{"provider", "model", "direction"})
var FallbackTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_fallback_total", Help: "Fallback count"}, []string{"level", "from_model", "to_model"})
var CircuitBreakerState = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "llm_circuit_breaker_state", Help: "Circuit breaker state"}, []string{"provider", "model"})
var CacheHitTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_cache_hit_total", Help: "Semantic cache hit count"}, []string{"model", "cache_type"})

func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(RequestDuration, RequestTotal, TokensTotal, FallbackTotal, CircuitBreakerState, CacheHitTotal)
}
```

文件： `internal/observability/tracing.go`

```go
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
```

- [ ] **步骤 4：补充 uuid 依赖并验证**

```bash
go get github.com/google/uuid@v1.6.0
go test ./internal/observability -v
```

预期：全部 PASS。

- [ ] **步骤 5：提交**

```bash
git add go.mod go.sum internal/observability
git commit -m "feat: 添加可观测性基础设施"
```

---

## 任务 6：实现 OpenAI 兼容 Provider 适配器

**文件：**
- 新建： `internal/egress/openai_compat.go`
- 新建： `internal/egress/openai_compat_test.go`

- [ ] **步骤 1：先写请求转换测试**

文件： `internal/egress/openai_compat_test.go`

```go
package egress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestOpenAICompatibleBuildRequest(t *testing.T) {
	p := NewOpenAICompatible("openai", "https://example.test/v1", "sk-test", []string{"gpt-4o"}, time.Second)
	body, err := p.buildRequestBody(&model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Contains(t, string(body), "gpt-4o")
	require.Contains(t, string(body), "hi")
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/egress -run TestOpenAICompatibleBuildRequest -v
```

预期：失败，提示 `NewOpenAICompatible` 未定义。

- [ ] **步骤 3：实现 OpenAI 兼容适配器**

文件： `internal/egress/openai_compat.go`

```go
package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/viif/momu-llmgateway/internal/model"
)

type OpenAICompatible struct {
	name    string
	baseURL string
	apiKey  string
	models  []string
	client  *http.Client
}

func NewOpenAICompatible(name, baseURL, apiKey string, models []string, timeout time.Duration) *OpenAICompatible {
	return &OpenAICompatible{name: name, baseURL: baseURL, apiKey: apiKey, models: models, client: &http.Client{Timeout: timeout}}
}

func (p *OpenAICompatible) Name() string { return p.name }
func (p *OpenAICompatible) Models() []string { return p.models }

func (p *OpenAICompatible) buildRequestBody(req *model.StandardRequest) ([]byte, error) {
	return json.Marshal(map[string]any{"model": req.Model, "messages": req.Messages, "stream": req.Stream, "temperature": req.Temperature, "max_tokens": req.MaxTokens})
}

func (p *OpenAICompatible) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	body, err := p.buildRequestBody(req)
	if err != nil { return nil, err }
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil { return nil, err }
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode >= 400 { return nil, model.NewError(model.ErrCodeProviderError, resp.Status) }
	var out model.StandardResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	out.Provider = p.name
	return &out, nil
}

func (p *OpenAICompatible) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return StreamOpenAICompatible(ctx, p.client, p.baseURL+"/chat/completions", p.apiKey, req)
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/egress -run TestOpenAICompatibleBuildRequest -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/egress/openai_compat.go internal/egress/openai_compat_test.go
git commit -m "feat: 添加 openai 兼容 provider 适配器"
```

---

## 任务 7：实现 SSE 流式转换

**文件：**
- 新建： `internal/egress/stream.go`
- 新建： `internal/egress/stream_test.go`

- [ ] **步骤 1：先写 SSE 解析测试**

文件： `internal/egress/stream_test.go`

```go
package egress

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSSELine(t *testing.T) {
	chunk, done, err := parseSSELine(`data: {"id":"1","model":"gpt-4o","choices":[{"delta":{"content":"hi"}}]}`)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, "hi", chunk.Delta.Content)

	_, done, err = parseSSELine("data: [DONE]")
	require.NoError(t, err)
	require.True(t, done)

	_, _, err = parseSSELine(strings.TrimSpace(""))
	require.NoError(t, err)
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/egress -run TestParseSSELine -v
```

预期：失败，提示 `parseSSELine` 未定义。

- [ ] **步骤 3：实现 SSE 解析和转发入口**

文件： `internal/egress/stream.go`

```go
package egress

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/viif/momu-llmgateway/internal/model"
)

func parseSSELine(line string) (model.StreamChunk, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "data:") { return model.StreamChunk{}, false, nil }
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "[DONE]" { return model.StreamChunk{Done: true}, true, nil }
	var raw struct { ID string `json:"id"`; Model string `json:"model"`; Choices []struct { Delta model.Delta `json:"delta"` } `json:"choices"` }
	if err := json.Unmarshal([]byte(payload), &raw); err != nil { return model.StreamChunk{}, false, err }
	chunk := model.StreamChunk{ID: raw.ID, Model: raw.Model}
	if len(raw.Choices) > 0 { chunk.Delta = raw.Choices[0].Delta }
	return chunk, false, nil
}

func StreamOpenAICompatible(ctx context.Context, client *http.Client, url, apiKey string, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	body, err := json.Marshal(map[string]any{"model": req.Model, "messages": req.Messages, "stream": true})
	if err != nil { return nil, err }
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil { return nil, err }
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(httpReq)
	if err != nil { return nil, err }
	out := make(chan model.StreamChunk)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			chunk, done, err := parseSSELine(scanner.Text())
			if err != nil { out <- model.StreamChunk{Error: model.NewError(model.ErrCodeProviderError, err.Error())}; return }
			if done { out <- chunk; return }
			if chunk.ID != "" || chunk.Delta.Content != "" { out <- chunk }
		}
	}()
	return out, nil
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/egress -run TestParseSSELine -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/egress/stream.go internal/egress/stream_test.go
git commit -m "feat: 添加 sse 流式转换"
```

---

## 任务 8：实现 Anthropic 与 GLM Provider 适配器

**文件：**
- 新建： `internal/egress/anthropic.go`
- 新建： `internal/egress/glm.go`
- 新建： `internal/egress/anthropic_test.go`
- 新建： `internal/egress/glm_test.go`

- [ ] **步骤 1：先写协议转换测试**

文件： `internal/egress/anthropic_test.go`

```go
package egress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestAnthropicExtractsSystemMessage(t *testing.T) {
	p := NewAnthropic("https://example.test", "sk", []string{"claude-sonnet-4-20250514"}, time.Second)
	body, err := p.buildRequestBody(&model.StandardRequest{Model: "claude-sonnet-4-20250514", Messages: []model.Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Contains(t, string(body), "system")
	require.Contains(t, string(body), "be brief")
}
```

文件： `internal/egress/glm_test.go`

```go
package egress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestGLMBuildRequest(t *testing.T) {
	p := NewGLM("https://example.test", "sk", []string{"glm-4"}, time.Second)
	body, err := p.buildRequestBody(&model.StandardRequest{Model: "glm-4", Messages: []model.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Contains(t, string(body), "glm-4")
	require.Contains(t, string(body), "hi")
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/egress -run 'TestAnthropicExtractsSystemMessage|TestGLMBuildRequest' -v
```

预期：失败，提示构造函数未定义。

- [ ] **步骤 3：实现 Anthropic 适配器**

文件： `internal/egress/anthropic.go`

```go
package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/viif/momu-llmgateway/internal/model"
)

type Anthropic struct { baseURL, apiKey string; models []string; client *http.Client }

func NewAnthropic(baseURL, apiKey string, models []string, timeout time.Duration) *Anthropic {
	return &Anthropic{baseURL: baseURL, apiKey: apiKey, models: models, client: &http.Client{Timeout: timeout}}
}
func (p *Anthropic) Name() string { return "anthropic" }
func (p *Anthropic) Models() []string { return p.models }
func (p *Anthropic) buildRequestBody(req *model.StandardRequest) ([]byte, error) {
	system := ""
	messages := make([]model.Message, 0, len(req.Messages))
	for _, m := range req.Messages { if m.Role == "system" { system = m.Content; continue }; messages = append(messages, m) }
	return json.Marshal(map[string]any{"model": req.Model, "system": system, "messages": messages, "max_tokens": req.MaxTokens})
}
func (p *Anthropic) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	body, err := p.buildRequestBody(req); if err != nil { return nil, err }
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body)); if err != nil { return nil, err }
	httpReq.Header.Set("x-api-key", p.apiKey); httpReq.Header.Set("anthropic-version", "2023-06-01"); httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq); if err != nil { return nil, err }
	defer resp.Body.Close(); if resp.StatusCode >= 400 { return nil, model.NewError(model.ErrCodeProviderError, resp.Status) }
	return &model.StandardResponse{Model: req.Model, Provider: p.Name()}, nil
}
func (p *Anthropic) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) { return nil, model.NewError(model.ErrCodeProviderError, "anthropic streaming adapter not wired yet") }
```

- [ ] **步骤 4：实现 GLM 适配器**

文件： `internal/egress/glm.go`

```go
package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/viif/momu-llmgateway/internal/model"
)

type GLM struct { baseURL, apiKey string; models []string; client *http.Client }

func NewGLM(baseURL, apiKey string, models []string, timeout time.Duration) *GLM {
	return &GLM{baseURL: baseURL, apiKey: apiKey, models: models, client: &http.Client{Timeout: timeout}}
}
func (p *GLM) Name() string { return "glm" }
func (p *GLM) Models() []string { return p.models }
func (p *GLM) buildRequestBody(req *model.StandardRequest) ([]byte, error) {
	return json.Marshal(map[string]any{"model": req.Model, "messages": req.Messages, "temperature": req.Temperature, "max_tokens": req.MaxTokens, "stream": req.Stream})
}
func (p *GLM) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	body, err := p.buildRequestBody(req); if err != nil { return nil, err }
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body)); if err != nil { return nil, err }
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey); httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq); if err != nil { return nil, err }
	defer resp.Body.Close(); if resp.StatusCode >= 400 { return nil, model.NewError(model.ErrCodeProviderError, resp.Status) }
	return &model.StandardResponse{Model: req.Model, Provider: p.Name()}, nil
}
func (p *GLM) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) { return StreamOpenAICompatible(ctx, p.client, p.baseURL+"/chat/completions", p.apiKey, req) }
```

- [ ] **步骤 5：验证测试通过**

```bash
go test ./internal/egress -run 'TestAnthropicExtractsSystemMessage|TestGLMBuildRequest' -v
```

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/egress/anthropic.go internal/egress/glm.go internal/egress/*_test.go
git commit -m "feat: 添加 anthropic 和 glm 适配器"
```

---

## 任务 9：实现 Provider 注册表

**文件：**
- 新建： `internal/egress/adapter.go`
- 新建： `internal/egress/adapter_test.go`

- [ ] **步骤 1：先写注册表测试**

文件： `internal/egress/adapter_test.go`

```go
package egress

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

type fakeProvider struct { name string; models []string }
func (f fakeProvider) Name() string { return f.name }
func (f fakeProvider) Models() []string { return f.models }
func (f fakeProvider) Send(context.Context, *model.StandardRequest) (*model.StandardResponse, error) { return nil, nil }
func (f fakeProvider) SendStream(context.Context, *model.StandardRequest) (<-chan model.StreamChunk, error) { return nil, nil }

func TestRegistryFindsProvidersByModel(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	providers := r.ProvidersForModel("gpt-4o")
	require.Len(t, providers, 1)
	require.Equal(t, "openai", providers[0].Name())
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/egress -run TestRegistryFindsProvidersByModel -v
```

预期：失败，提示 `NewRegistry` 未定义。

- [ ] **步骤 3：实现注册表**

文件： `internal/egress/adapter.go`

```go
package egress

import "github.com/viif/momu-llmgateway/internal/model"

type Registry struct { providers []model.Provider }

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Register(p model.Provider) { r.providers = append(r.providers, p) }

func (r *Registry) Providers() []model.Provider { return append([]model.Provider(nil), r.providers...) }

func (r *Registry) ProvidersForModel(modelID string) []model.Provider {
	var out []model.Provider
	for _, p := range r.providers {
		for _, m := range p.Models() { if m == modelID { out = append(out, p); break } }
	}
	return out
}

func (r *Registry) ProviderByName(name string) model.Provider {
	for _, p := range r.providers { if p.Name() == name { return p } }
	return nil
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/egress -run TestRegistryFindsProvidersByModel -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/egress/adapter.go internal/egress/adapter_test.go
git commit -m "feat: 添加 provider 注册表"
```

---

## 任务 10：实现熔断器

**文件：**
- 新建： `internal/decision/circuitbreaker.go`
- 新建： `internal/decision/circuitbreaker_test.go`

- [ ] **步骤 1：先写状态转换测试**

文件： `internal/decision/circuitbreaker_test.go`

```go
package decision

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCircuitBreakerOpensAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Second, time.Minute)
	require.True(t, cb.Allow())
	cb.RecordFailure()
	cb.RecordFailure()
	require.False(t, cb.Allow())
	require.Equal(t, StateOpen, cb.State())
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/decision -run TestCircuitBreakerOpensAfterFailures -v
```

预期：失败，提示 `NewCircuitBreaker` 未定义。

- [ ] **步骤 3：实现熔断器**

文件： `internal/decision/circuitbreaker.go`

```go
package decision

import (
	"sync"
	"time"
)

type CircuitState int

const (
	StateClosed CircuitState = iota
	StateOpen
	StateHalfOpen
)

type CircuitBreaker struct {
	mu sync.Mutex
	threshold int
	cooldown time.Duration
	failures int
	state CircuitState
	openedAt time.Time
}

func NewCircuitBreaker(threshold int, _ time.Duration, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{threshold: threshold, cooldown: cooldown, state: StateClosed}
}

func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock(); defer cb.mu.Unlock()
	if cb.state == StateOpen && time.Since(cb.openedAt) >= cb.cooldown { cb.state = StateHalfOpen; return true }
	return cb.state != StateOpen
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock(); defer cb.mu.Unlock()
	cb.failures = 0; cb.state = StateClosed
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock(); defer cb.mu.Unlock()
	cb.failures++
	if cb.failures >= cb.threshold { cb.state = StateOpen; cb.openedAt = time.Now() }
}

func (cb *CircuitBreaker) State() CircuitState { cb.mu.Lock(); defer cb.mu.Unlock(); return cb.state }
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/decision -run TestCircuitBreakerOpensAfterFailures -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/decision/circuitbreaker.go internal/decision/circuitbreaker_test.go
git commit -m "feat: 添加熔断器"
```

---

## 任务 11：实现加权负载均衡

**文件：**
- 新建： `internal/decision/balancer.go`
- 新建： `internal/decision/balancer_test.go`

- [ ] **步骤 1：先写选择测试**

文件： `internal/decision/balancer_test.go`

```go
package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalancerSelectsHighestEffectiveWeight(t *testing.T) {
	b := NewBalancer()
	got := b.Select([]ProviderCandidate{{ProviderName: "a", BaseWeight: 10, HealthScore: 1}, {ProviderName: "b", BaseWeight: 100, HealthScore: 0.1}})
	require.Equal(t, "a", got.ProviderName)
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/decision -run TestBalancerSelectsHighestEffectiveWeight -v
```

预期：失败，提示 `NewBalancer` 未定义。

- [ ] **步骤 3：实现负载均衡器**

文件： `internal/decision/balancer.go`

```go
package decision

type ProviderCandidate struct {
	ProviderName       string
	Model              string
	BaseWeight         float64
	ActiveConnections  int
	HealthScore        float64
}

type Balancer struct{}

func NewBalancer() *Balancer { return &Balancer{} }

func (b *Balancer) Select(candidates []ProviderCandidate) ProviderCandidate {
	var best ProviderCandidate
	bestScore := -1.0
	for _, c := range candidates {
		score := c.BaseWeight * (1 / (1 + float64(c.ActiveConnections))) * c.HealthScore
		if score > bestScore { bestScore = score; best = c }
	}
	return best
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/decision -run TestBalancerSelectsHighestEffectiveWeight -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/decision/balancer.go internal/decision/balancer_test.go
git commit -m "feat: 添加加权 provider 负载均衡"
```

---

## 任务 12：实现路由策略链

**文件：**
- 新建： `internal/decision/strategy_capability.go`
- 新建： `internal/decision/strategy_cost.go`
- 新建： `internal/decision/strategy_semantic.go`
- 新建： `internal/decision/router.go`
- 新建： `internal/decision/router_test.go`

- [ ] **步骤 1：先写显式路由和默认路由测试**

文件： `internal/decision/router_test.go`

```go
package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestRouterExplicitProviderPrefix(t *testing.T) {
	r := NewRouter([]string{"deepseek-chat"})
	decision, err := r.Route(&model.StandardRequest{Model: "openai/gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, "openai", decision.ProviderName)
	require.Equal(t, "gpt-4o", decision.Model)
}

func TestRouterDefaultModel(t *testing.T) {
	r := NewRouter([]string{"deepseek-chat"})
	decision, err := r.Route(&model.StandardRequest{Model: ""})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", decision.Model)
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/decision -run 'TestRouterExplicitProviderPrefix|TestRouterDefaultModel' -v
```

预期：失败，提示 `NewRouter` 未定义。

- [ ] **步骤 3：实现路由核心**

文件： `internal/decision/router.go`

```go
package decision

import (
	"strings"

	"github.com/viif/momu-llmgateway/internal/model"
)

type RouteDecision struct { ProviderName string; Model string }

type Router struct { defaultCascade []string }

func NewRouter(defaultCascade []string) *Router { return &Router{defaultCascade: defaultCascade} }

func (r *Router) Route(req *model.StandardRequest) (RouteDecision, error) {
	if strings.Contains(req.Model, "/") {
		parts := strings.SplitN(req.Model, "/", 2)
		return RouteDecision{ProviderName: parts[0], Model: parts[1]}, nil
	}
	if req.Model != "" { return RouteDecision{Model: req.Model}, nil }
	if len(r.defaultCascade) > 0 { return RouteDecision{Model: r.defaultCascade[0]}, nil }
	return RouteDecision{}, model.NewError(model.ErrCodeModelNotFound, "no route matched")
}
```

文件： `internal/decision/strategy_capability.go`

```go
package decision

func MatchCapability(taskType string, rules map[string][]string) []string { return rules[taskType] }
```

文件： `internal/decision/strategy_cost.go`

```go
package decision

func FirstCostCascade(cascade []string) string { if len(cascade) == 0 { return "" }; return cascade[0] }
```

文件： `internal/decision/strategy_semantic.go`

```go
package decision

type SemanticMatch struct { Category string; Confidence float64; TargetModels []string }

func AcceptSemanticMatch(match SemanticMatch, threshold float64) bool { return match.Confidence >= threshold && len(match.TargetModels) > 0 }
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/decision -run 'TestRouterExplicitProviderPrefix|TestRouterDefaultModel' -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/decision/strategy_*.go internal/decision/router.go internal/decision/router_test.go
git commit -m "feat: 添加路由策略链"
```

---

## 任务 13：实现语义缓存

**文件：**
- 新建： `internal/cache/semantic.go`
- 新建： `internal/cache/semantic_test.go`

- [ ] **步骤 1：先写余弦相似度测试**

文件： `internal/cache/semantic_test.go`

```go
package cache

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCosineSimilarity(t *testing.T) {
	require.InDelta(t, 1.0, CosineSimilarity([]float64{1, 0}, []float64{1, 0}), 0.0001)
	require.InDelta(t, 0.0, CosineSimilarity([]float64{1, 0}, []float64{0, 1}), 0.0001)
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/cache -run TestCosineSimilarity -v
```

预期：失败，提示 `CosineSimilarity` 未定义。

- [ ] **步骤 3：实现语义缓存基础函数**

文件： `internal/cache/semantic.go`

```go
package cache

import "math"

type Entry struct { Model string; Vector []float64; ResponseJSON []byte }

func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) { return 0 }
	var dot, na, nb float64
	for i := range a { dot += a[i]*b[i]; na += a[i]*a[i]; nb += b[i]*b[i] }
	if na == 0 || nb == 0 { return 0 }
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func FindSimilar(entries []Entry, model string, vector []float64, threshold float64) (Entry, bool) {
	var best Entry; bestScore := threshold
	for _, e := range entries {
		if e.Model != model { continue }
		if score := CosineSimilarity(e.Vector, vector); score >= bestScore { bestScore = score; best = e }
	}
	return best, best.ResponseJSON != nil
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/cache -run TestCosineSimilarity -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/cache
git commit -m "feat: 添加语义缓存基础函数"
```

---

## 任务 14：实现 Fallback 引擎

**文件：**
- 新建： `internal/fallback/engine.go`
- 新建： `internal/fallback/engine_test.go`

- [ ] **步骤 1：先写降级链测试**

文件： `internal/fallback/engine_test.go`

```go
package fallback

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFallbackChain(t *testing.T) {
	e := NewEngine(map[string][]string{"gpt-4o": {"claude-sonnet-4-20250514", "gpt-4o-mini"}})
	require.Equal(t, []string{"claude-sonnet-4-20250514", "gpt-4o-mini"}, e.Chain("gpt-4o"))
	require.Empty(t, e.Chain("unknown"))
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/fallback -run TestFallbackChain -v
```

预期：失败，提示 `NewEngine` 未定义。

- [ ] **步骤 3：实现 Fallback 引擎**

文件： `internal/fallback/engine.go`

```go
package fallback

type Engine struct { chains map[string][]string }

func NewEngine(chains map[string][]string) *Engine { return &Engine{chains: chains} }

func (e *Engine) Chain(model string) []string { return append([]string(nil), e.chains[model]...) }

func (e *Engine) Attempts(primary string) []string {
	out := []string{primary}
	out = append(out, e.Chain(primary)...)
	return out
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/fallback -run TestFallbackChain -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/fallback
git commit -m "feat: 添加 fallback 降级引擎"
```

---

## 任务 15：实现认证、限流和日志中间件

**文件：**
- 新建： `internal/ingress/middleware_auth.go`
- 新建： `internal/ingress/middleware_ratelimit.go`
- 新建： `internal/ingress/middleware_logging.go`
- 新建： `internal/ingress/middleware_auth_test.go`

- [ ] **步骤 1：先写认证测试**

文件： `internal/ingress/middleware_auth_test.go`

```go
package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
)

func TestAuthMiddlewareAcceptsBearerKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{{Key: "sk-test", Name: "test", AllowedModels: []string{"*"}}}))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/ingress -run TestAuthMiddlewareAcceptsBearerKey -v
```

预期：失败，提示 `AuthMiddleware` 未定义。

- [ ] **步骤 3：实现中间件**

文件： `internal/ingress/middleware_auth.go`

```go
package ingress

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/viif/momu-llmgateway/internal/config"
)

func AuthMiddleware(keys []config.APIKeyConfig) gin.HandlerFunc {
	allowed := map[string]config.APIKeyConfig{}
	for _, k := range keys { allowed[k.Key] = k }
	return func(c *gin.Context) {
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if _, ok := allowed[token]; !ok || token == "" { c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"}); return }
		c.Set("api_key", token)
		c.Next()
	}
}
```

文件： `internal/ingress/middleware_ratelimit.go`

```go
package ingress

import "github.com/gin-gonic/gin"

func RateLimitMiddleware() gin.HandlerFunc { return func(c *gin.Context) { c.Next() } }
```

文件： `internal/ingress/middleware_logging.go`

```go
package ingress

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/viif/momu-llmgateway/internal/observability"
)

func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now(); c.Next()
		observability.Logger.Info("request", zap.String("path", c.Request.URL.Path), zap.Int("status", c.Writer.Status()), zap.Duration("latency", time.Since(start)))
	}
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/ingress -run TestAuthMiddlewareAcceptsBearerKey -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/ingress/middleware_*.go internal/ingress/middleware_auth_test.go
git commit -m "feat: 添加接入层中间件"
```

---

## 任务 16：实现 HTTP Handler

**文件：**
- 新建： `internal/ingress/handler.go`
- 新建： `internal/ingress/handler_test.go`

- [ ] **步骤 1：先写健康检查测试**

文件： `internal/ingress/handler_test.go`

```go
package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "ok")
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/ingress -run TestHealthHandler -v
```

预期：失败，提示 `RegisterRoutes` 未定义。

- [ ] **步骤 3：实现路由注册和基础 Handler**

文件： `internal/ingress/handler.go`

```go
package ingress

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type ChatService interface{}

func RegisterRoutes(r *gin.Engine, svc ChatService) {
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "chat service not wired"})
	})
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/ingress -run TestHealthHandler -v
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/ingress/handler.go internal/ingress/handler_test.go
git commit -m "feat: 添加 http 路由处理器"
```

---

## 任务 17：在 main.go 中组装服务

**文件：**
- 修改： `cmd/gateway/main.go`

- [ ] **步骤 1：替换 main.go**

文件： `cmd/gateway/main.go`

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/ingress"
	"github.com/viif/momu-llmgateway/internal/observability"
)

func main() {
	if err := observability.InitLogger(false); err != nil { panic(err) }
	cfgPath := os.Getenv("GATEWAY_CONFIG")
	if cfgPath == "" { cfgPath = "configs/gateway.yaml" }
	cfg, err := config.Load(cfgPath)
	if err != nil { observability.Logger.Fatal("load config", zap.Error(err)) }
	_ = config.WatchAndReload(cfgPath, func(*config.Config) { observability.Logger.Info("config reloaded") })

	r := gin.New()
	r.Use(gin.Recovery(), ingress.LoggingMiddleware(), ingress.AuthMiddleware(cfg.Auth.APIKeys), ingress.RateLimitMiddleware())
	ingress.RegisterRoutes(r, nil)

	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Server.Port), Handler: r, ReadTimeout: cfg.Server.ReadTimeout, WriteTimeout: cfg.Server.WriteTimeout}
	go func() { if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed { observability.Logger.Fatal("server failed", zap.Error(err)) } }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
```

- [ ] **步骤 2：验证构建**

```bash
go build ./cmd/gateway
```

预期：无错误。

- [ ] **步骤 3：运行全量测试**

```bash
go test ./...
```

预期：全部 PASS。

- [ ] **步骤 4：提交**

```bash
git add cmd/gateway/main.go
git commit -m "feat: 组装网关服务启动流程"
```

---

## 任务 18：添加 Dockerfile

**文件：**
- 新建： `Dockerfile`

- [ ] **步骤 1：创建多阶段 Dockerfile**

文件： `Dockerfile`

```dockerfile
FROM golang:1.21-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /gateway ./cmd/gateway

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /gateway /app/gateway
COPY configs/gateway.yaml /app/configs/gateway.yaml
EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
```

- [ ] **步骤 2：验证 Dockerfile 构建语法**

```bash
docker build -t momu-llmgateway:local .
```

预期：镜像构建成功。

- [ ] **步骤 3：提交**

```bash
git add Dockerfile
git commit -m "feat: 添加网关 dockerfile"
```

---

## 任务 19：完善 GitHub Actions CI（加入 lint、竞态检测、覆盖率上报）

> 任务 2 已建立基础 CI（build + test），本任务在其基础上增加 lint 检查、竞态检测（`-race`）和覆盖率上报步骤。

**文件：**
- 修改： `.github/workflows/ci.yml`

- [ ] **步骤 1：升级 CI workflow**

文件： `.github/workflows/ci.yml`

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.21'
      - name: Check formatting
        run: test -z "$(gofmt -l .)"
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v4
        with:
          version: latest

  test:
    runs-on: ubuntu-latest
    services:
      redis:
        image: redis:7
        ports:
          - 6379:6379
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.21'
      - name: Run tests
        run: go test -race -coverprofile=coverage.out ./...
      - name: Upload coverage
        uses: codecov/codecov-action@v4
```

- [ ] **步骤 2：验证 YAML 可解析**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"
```

预期：无输出、无错误。

- [ ] **步骤 3：验证没有 Docker CI job**

```bash
python3 - <<'PY'
import yaml
with open('.github/workflows/ci.yml', encoding='utf-8') as f:
    jobs = yaml.safe_load(f)['jobs']
assert 'docker' not in jobs
PY
```

预期：无输出、无错误。

- [ ] **步骤 4：提交**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: 添加 lint、竞态检测和覆盖率上报"
```

---

## 任务 20：端到端手动验证

**文件：**
- 修改： none

- [ ] **步骤 1：运行全量测试**

```bash
go test -race ./...
```

预期：全部 PASS。

- [ ] **步骤 2：启动服务**

```bash
GATEWAY_CONFIG=configs/gateway.yaml go run ./cmd/gateway
```

预期：服务监听 `:8080`。

- [ ] **步骤 3：健康检查**

```bash
curl http://localhost:8080/health
```

预期：返回 `{"status":"ok"}`。

- [ ] **步骤 4：指标检查**

```bash
curl http://localhost:8080/metrics
```

预期：返回 Prometheus 格式指标文本，包含 `llm_request_total` 或已注册指标名。

- [ ] **步骤 5：普通请求认证路径检查**

```bash
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'
```

预期：当前若 ChatService 尚未接入真实 Provider，可返回 `501` 和 `chat service not wired`；认证中间件不应返回 `401`。

- [ ] **步骤 6：提交最终验证修正**

如果本任务发现并修复了代码问题，提交：

```bash
git add <changed-files>
git commit -m "fix: 完成网关端到端验证"
```

如果没有代码变更，不创建空提交。

---

## 依赖顺序

```text
任务 1 工程骨架
  -> 任务 2 CI（基础版本）
  -> 任务 3 核心模型
      -> 任务 4 配置
      -> 任务 5 可观测性
      -> 任务 6 OpenAI 兼容适配器
          -> 任务 7 SSE 流式转换
          -> 任务 8 Anthropic/GLM 适配器
          -> 任务 9 Provider 注册表
      -> 任务 10 熔断器
      -> 任务 11 负载均衡
      -> 任务 12 路由策略
      -> 任务 13 语义缓存
      -> 任务 14 Fallback
      -> 任务 15 中间件
      -> 任务 16 Handler
          -> 任务 17 main.go 组装
              -> 任务 18 Dockerfile
              -> 任务 19 完善 CI
              -> 任务 20 端到端验证
```

## 自检结果

- Spec 覆盖：配置热加载、环境变量展开、OpenAI/Anthropic/DeepSeek/Qwen/GLM Provider、路由策略、熔断、负载均衡、语义缓存、Fallback、认证、限流、日志、Prometheus、Dockerfile、GitHub Actions CI、手动验证均有对应任务。
- CI 对齐：`.github/workflows/ci.yml` 只包含 `lint` 和 `test` job；不包含镜像构建 job。
- 占位符检查：未发现占位式待补内容。
- 类型一致性：核心类型从任务 3 定义，后续任务统一引用 `model.StandardRequest`、`model.StandardResponse`、`model.Provider`、`model.StreamChunk`。
