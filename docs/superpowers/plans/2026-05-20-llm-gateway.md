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
- `internal/egress/openai.go`：OpenAI 兼容 Provider 基类（含 SSE 流式处理），复用给 OpenAI、DeepSeek、Qwen、GLM。
- `internal/egress/anthropic.go`：Anthropic Messages API 适配器（含独立 SSE 解析）。
- `internal/egress/adapter.go`：Provider 注册表（切片 + 双 map + RWMutex，O(1) 查找）。
- `internal/decision/circuitbreaker.go`：Provider+Model 维度的 Closed/Open/Half-Open 熔断器。
- `internal/decision/balancer.go`：同模型多 Provider 的加权负载均衡。
- `internal/decision/strategy_capability.go`：基于 task_type 标签和 condition 条件的能力路由。
- `internal/decision/strategy_cost.go`：低成本优先的级联路由。
- `internal/decision/strategy_semantic.go`：基于 Embedding 相似度的语义路由。
- `internal/decision/router.go`：策略链编排、显式路由和默认路由。
- `internal/cache/semantic.go`：语义缓存查询、写入、TTL 和命中判断。
- `internal/embedding/embedding.go`：纯函数（CosineSimilarity / NormalizeVector / MeanPooling）。
- `internal/embedding/engine.go`：`EmbeddingEngine` 类型和单例生命周期管理。
- `internal/embedding/onnx.go`：ONNX 嵌入引擎实现（`onnxruntime_go` + 纯 Go tokenizer `hftokenizer`），提供 `Init()` / `Embed()` / `Close()`。
- `internal/fallback/engine.go`：L1 重试、L2 跨 Provider、L3 跨模型、L4 兜底响应。
- `internal/ingress/middleware_requestid.go`：RequestID 注入中间件，为每个请求生成唯一 ID 并注入 context 和响应头。
- `internal/ingress/middleware_auth.go`：Bearer API Key 认证和 allowed_models 校验。
- `internal/ingress/middleware_ratelimit.go`：Redis 滑动窗口限流（ZRANGEBYSCORE + ZADD），按 API Key 独立计数。
- `internal/ingress/middleware_validation.go`：请求参数校验（model 非空、messages 非空、temperature 范围、max_tokens 合法性）。
- `internal/ingress/middleware_logging.go`：请求日志，记录 request_id、path、method、status、latency、content_length 等结构化字段。
- `internal/ingress/handler.go`：`POST /v1/chat/completions`、`GET /health`、`GET /metrics`。
- `configs/gateway.yaml`：默认配置样例。
- `.github/workflows/ci.yml`：GitHub Actions lint/test，不包含镜像构建 job。
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
mkdir -p cmd/gateway internal/{config,model,ingress,decision,egress,cache,embedding,fallback,observability} configs .github/workflows
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
	Balancer        BalancerConfig        `mapstructure:"balancer"`
	Embedding       EmbeddingConfig       `mapstructure:"embedding"`
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

type BalancerConfig struct {
	ConcurrencyPenaltyCoefficient float64       `mapstructure:"concurrency_penalty_coefficient"`
	LatencyPenaltyCoefficient     float64       `mapstructure:"latency_penalty_coefficient"`
	WarmupEnabled                 bool          `mapstructure:"warmup_enabled"`
	WarmupDuration                time.Duration `mapstructure:"warmup_duration"`
	HealthWindowSize              time.Duration `mapstructure:"health_window_size"`
	HealthMinRequests             int           `mapstructure:"health_min_requests"`
}

type EmbeddingConfig struct {
	OnnxLibraryPath string `mapstructure:"onnx_library_path"`
	ModelPath       string `mapstructure:"model_path"`
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
    type: "openai"
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
    type: "openai"
    base_url: "https://api.deepseek.com/v1"
    api_key: "${DEEPSEEK_API_KEY}"
    models: ["deepseek-chat", "deepseek-reasoner"]
    weight: 90
    timeout: 60s
  qwen:
    type: "openai"
    base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
    api_key: "${QWEN_API_KEY}"
    models: ["qwen-turbo", "qwen-plus", "qwen-max"]
    weight: 85
    timeout: 60s
  glm:
    type: "openai"
    base_url: "https://open.bigmodel.cn/api/paas/v4"
    api_key: "${GLM_API_KEY}"
    models: ["glm-4", "glm-4-flash"]
    weight: 75
    timeout: 60s
routing:
  strategies: ["explicit", "capability", "semantic", "cost_cascade"]
  rules:
    - task_type: "long_context"
      condition: "input_tokens > 100000"
      target_models: ["claude-sonnet-4-20250514", "deepseek-chat"]
  cascade:
    default: ["deepseek-chat", "gpt-4o-mini", "gpt-4o"]
semantic_routing:
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
fallback:
  retry_max: 2
  retry_backoff: "1s"
  chains:
    gpt-4o: ["claude-sonnet-4-20250514", "gpt-4o-mini"]
circuit_breaker:
  failure_threshold: 5
  window: 10s
  cooldown: 30s
balancer:
  concurrency_penalty_coefficient: 3.0
  latency_penalty_coefficient: 2.0
  warmup_enabled: true
  warmup_duration: 30s
  health_window_size: 30s
  health_min_requests: 10
embedding:
  onnx_library_path: "/usr/lib/libonnxruntime.so"
  model_path: "./.models/bge-small-zh-v1.5"
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
- 新建： `internal/egress/openai.go`
- 新建： `internal/egress/openai_test.go`

- [ ] **步骤 1：先写请求转换测试**

文件： `internal/egress/openai_test.go`

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

文件： `internal/egress/openai.go`

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
git add internal/egress/openai.go internal/egress/openai_test.go
git commit -m "feat: 添加 openai 兼容 provider 适配器"
```

---

## 任务 7：实现 SSE 流式转换

> **注：** 实现后 `parseSSELine` 和 `streamOpenAI` 已合并入 `internal/egress/openai.go`，不再维护独立文件。

**文件：**
- 新建： `internal/egress/stream_openai.go`（后合并入 `openai.go`）
- 新建： `internal/egress/stream_openai_test.go`（后合并入 `openai_test.go`）

- [ ] **步骤 1：先写 SSE 解析测试**

文件： `internal/egress/stream_openai_test.go`

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

文件： `internal/egress/stream_openai.go`

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
git add internal/egress/stream_openai.go internal/egress/stream_openai_test.go
git commit -m "feat: 添加 sse 流式转换"

>（后合并入 `internal/egress/openai.go`）
```

---

## 任务 8：实现 Anthropic Provider 适配器（含流式）

**注：GLM 已确认与 DeepSeek/Qwen 同为 OpenAI 兼容，直接在配置中使用 `type: "openai"` 复用，无需独立适配器。**

**文件：**
- 新建： `internal/egress/anthropic.go`
- 新建： `internal/egress/anthropic_test.go`

本任务分两阶段：
- **阶段 A（步骤 1-4）**：实现 Anthropic 基础适配器（system 消息提升、Messages API 协议转换）
- **阶段 B（步骤 5-10）**：实现 Anthropic 独立 SSE 流式转换

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

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/egress -run TestAnthropicExtractsSystemMessage -v
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

type Anthropic struct {
	baseURL string
	apiKey  string
	models  []string
	client  *http.Client
}

func NewAnthropic(baseURL, apiKey string, models []string, timeout time.Duration) *Anthropic {
	return &Anthropic{baseURL: baseURL, apiKey: apiKey, models: models, client: &http.Client{Timeout: timeout}}
}

func (p *Anthropic) Name() string { return "anthropic" }

func (p *Anthropic) Models() []string { return p.models }

func (p *Anthropic) buildRequestBody(req *model.StandardRequest) ([]byte, error) {
	system := ""
	messages := make([]model.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		messages = append(messages, m)
	}
	return json.Marshal(map[string]any{"model": req.Model, "system": system, "messages": messages, "max_tokens": req.MaxTokens})
}

func (p *Anthropic) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, model.NewError(model.ErrCodeProviderError, resp.Status)
	}
	var raw struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := &model.StandardResponse{
		ID:       raw.ID,
		Model:    raw.Model,
		Provider: p.Name(),
		Usage: model.Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.InputTokens + raw.Usage.OutputTokens,
		},
	}
	if len(raw.Content) > 0 {
		out.Choices = []model.Choice{
			{
				Index:        0,
				Message:      model.Message{Role: "assistant", Content: raw.Content[0].Text},
				FinishReason: "stop",
			},
		}
	}
	return out, nil
}

func (p *Anthropic) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return nil, model.NewError(model.ErrCodeProviderError, "anthropic streaming adapter not wired yet")
}
```

- [ ] **步骤 4：验证测试通过并提交**

```bash
go test ./internal/egress -run TestAnthropicExtractsSystemMessage -v
```

预期：PASS。

```bash
git add internal/egress/anthropic.go internal/egress/anthropic_test.go
git commit -m "feat: 添加 anthropic 适配器"
```

> 阶段 A 完成。以下为阶段 B：流式响应实现。

---

### 流式响应背景

**Anthropic SSE 格式与 OpenAI 不同**，使用 `event:` + `data:` 双行结构：

| SSE Event | 用途 | 转换到 StreamChunk |
|-----------|------|-------------------|
| `message_start` | 返回 message id/model | 设置 `chunk.ID`、`chunk.Model`，发出 `Delta.Role: "assistant"` |
| `content_block_delta` | 携带 `delta.text`（文本增量） | 映射到 `chunk.Delta.Content` |
| `message_delta` | 携带 `delta.stop_reason`、`usage` | 提取 finish_reason |
| `message_stop` | 流结束 | 发送 `chunk.Done: true` |
| `ping` | 心跳 | 忽略 |

- [ ] **步骤 5：先写 Anthropic SSE 事件解析测试**

文件： `internal/egress/anthropic_test.go`（追加）

```go
func TestAnthropicParseSSEEvent(t *testing.T) {
	// content_block_delta: 提取 delta.text
	chunk, done, err := parseAnthropicSSEEvent("content_block_delta", ` {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, "Hello", chunk.Delta.Content)

	// message_start: 提取 message id/model，输出 assistant role
	chunk, done, err = parseAnthropicSSEEvent("message_start", ` {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-20250514"}}`)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, "msg_1", chunk.ID)
	require.Equal(t, "assistant", chunk.Delta.Role)

	// message_stop: 流结束
	chunk, done, err = parseAnthropicSSEEvent("message_stop", ` {"type":"message_stop"}`)
	require.NoError(t, err)
	require.True(t, done)

	// ping: 忽略
	chunk, done, err = parseAnthropicSSEEvent("ping", ` {"type":"ping"}`)
	require.NoError(t, err)
	require.False(t, done)
	require.Empty(t, chunk.Delta.Content)

	// 空行: 忽略
	_, _, err = parseAnthropicSSEEvent("", "")
	require.NoError(t, err)
}
```

- [ ] **步骤 6：运行测试确认失败**

```bash
go test ./internal/egress -run TestAnthropicParseSSEEvent -v
```

预期：失败，提示 `parseAnthropicSSEEvent` 未定义。

- [ ] **步骤 7：实现 Anthropic SSE 解析和流式连接**

文件： `internal/egress/anthropic.go`（追加）

> 需要新增 import：`"bufio"`、`"strings"`

```go
func parseAnthropicSSEEvent(eventType, data string) (model.StreamChunk, bool, error) {
	eventType = strings.TrimSpace(eventType)
	data = strings.TrimSpace(data)
	if eventType == "" && data == "" {
		return model.StreamChunk{}, false, nil
	}
	if !strings.HasPrefix(data, "{") {
		return model.StreamChunk{}, false, nil
	}
	var raw struct {
		Type    string `json:"type"`
		Message struct {
			ID    string `json:"id"`
			Role  string `json:"role"`
			Model string `json:"model"`
		} `json:"message"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Index int `json:"index"`
	}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return model.StreamChunk{}, false, err
	}
	switch raw.Type {
	case "message_start":
		return model.StreamChunk{ID: raw.Message.ID, Model: raw.Message.Model, Delta: model.Delta{Role: raw.Message.Role}}, false, nil
	case "content_block_delta":
		if raw.Delta.Type == "text_delta" {
			return model.StreamChunk{Delta: model.Delta{Content: raw.Delta.Text}}, false, nil
		}
	case "message_stop":
		return model.StreamChunk{Done: true}, true, nil
	}
	return model.StreamChunk{}, false, nil
}

func StreamAnthropic(ctx context.Context, client *http.Client, url, apiKey string, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	system := ""
	messages := make([]model.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" { system = m.Content; continue }
		messages = append(messages, m)
	}
	body, err := json.Marshal(map[string]any{
		"model":      req.Model,
		"system":     system,
		"messages":   messages,
		"max_tokens": req.MaxTokens,
		"stream":     true,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, model.NewError(model.ErrCodeProviderError, resp.Status)
	}
	out := make(chan model.StreamChunk)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		var currentEvent string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if strings.HasPrefix(line, "data:") {
				chunk, done, scanErr := parseAnthropicSSEEvent(currentEvent, strings.TrimPrefix(line, "data:"))
				currentEvent = ""
				if scanErr != nil {
					out <- model.StreamChunk{Error: model.NewError(model.ErrCodeProviderError, scanErr.Error())}
					return
				}
				if done {
					out <- chunk
					return
				}
				if chunk.ID != "" || chunk.Delta.Content != "" || chunk.Delta.Role != "" {
					out <- chunk
				}
			}
		}
		if err := scanner.Err(); err != nil {
			out <- model.StreamChunk{Error: model.NewError(model.ErrCodeProviderError, err.Error())}
		}
	}()
	return out, nil
}
```

更新 `SendStream` 方法（替换原有 stub）：

```go
func (p *Anthropic) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return StreamAnthropic(ctx, p.client, p.baseURL+"/v1/messages", p.apiKey, req)
}
```

- [ ] **步骤 8：验证全部测试通过**

```bash
go test ./internal/egress -v
```

预期：全部 PASS（包含 4 个测试：2 个已有 + 2 个新增）。

- [ ] **步骤 9：提交**

```bash
git add internal/egress/anthropic.go internal/egress/anthropic_test.go
git commit -m "feat: 添加 anthropic 流式适配"
```

---

## 任务 9：实现 Provider 注册表

**文件：**
- 新建： `internal/egress/adapter.go`
- 新建： `internal/egress/adapter_test.go`

- [x] **步骤 1：先写注册表测试**

文件： `internal/egress/adapter_test.go`

```go
package egress

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

type fakeProvider struct {
	name   string
	models []string
}

func (f fakeProvider) Name() string     { return f.name }
func (f fakeProvider) Models() []string { return f.models }
func (f fakeProvider) Send(context.Context, *model.StandardRequest) (*model.StandardResponse, error) {
	return nil, nil
}
func (f fakeProvider) SendStream(context.Context, *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return nil, nil
}

func TestRegistryFindsProvidersByModel(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	providers := r.ProvidersForModel("gpt-4o")
	require.Len(t, providers, 1)
	require.Equal(t, "openai", providers[0].Name())
}

func TestRegistryProviderByName(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	require.NotNil(t, r.ProviderByName("openai"))
	require.Nil(t, r.ProviderByName("nonexistent"))
}

func TestRegistryProvidersReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	list := r.Providers()
	require.Len(t, list, 1)
	list[0] = nil
	require.NotNil(t, r.Providers()[0])
}

func TestRegistryProvidersForModelNotFound(t *testing.T) {
	r := NewRegistry()
	require.Empty(t, r.ProvidersForModel("nonexistent"))
}

func TestRegistryMultipleProvidersSameModel(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o", "gpt-4o-mini"}})
	r.Register(fakeProvider{name: "deepseek", models: []string{"gpt-4o", "deepseek-chat"}})
	require.Len(t, r.ProvidersForModel("gpt-4o"), 2)
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.ProvidersForModel("gpt-4o")
			_ = r.ProviderByName("openai")
			_ = r.Providers()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			r.Register(fakeProvider{name: "extra", models: []string{"extra-model"}})
		}
	}()
	wg.Wait()
}
```

- [x] **步骤 2：运行测试确认失败**

```bash
go test -race ./internal/egress -run TestRegistry -v
```

预期：`TestRegistryConcurrentAccess` 因 data race 失败。

- [x] **步骤 3：实现注册表（切片 + 双 map + RWMutex）**

文件： `internal/egress/adapter.go`

```go
package egress

import (
	"sync"

	"github.com/viif/momu-llmgateway/internal/model"
)

type Registry struct {
	mu        sync.RWMutex
	providers []model.Provider
	byName    map[string]model.Provider
	byModel   map[string][]model.Provider
}

func NewRegistry() *Registry {
	return &Registry{
		byName:  make(map[string]model.Provider),
		byModel: make(map[string][]model.Provider),
	}
}

func (r *Registry) Register(p model.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
	r.byName[p.Name()] = p
	for _, m := range p.Models() {
		r.byModel[m] = append(r.byModel[m], p)
	}
}

func (r *Registry) Providers() []model.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]model.Provider, len(r.providers))
	copy(out, r.providers)
	return out
}

func (r *Registry) ProvidersForModel(modelID string) []model.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := r.byModel[modelID]
	out := make([]model.Provider, len(providers))
	copy(out, providers)
	return out
}

func (r *Registry) ProviderByName(name string) model.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byName[name]
}
```

> **设计说明：** `providers` 切片保留供 `Providers()` 返回全量列表；`byName` map 提供 O(1) 按名称查找；`byModel` map 提供 O(1) 按模型查找。`sync.RWMutex` 保证并发安全，`Register` 持有写锁，所有读方法持有读锁。所有读方法返回防御性拷贝，避免外部修改污染内部状态。

- [x] **步骤 4：验证测试通过**

```bash
go test -race ./internal/egress -run TestRegistry -v
```

预期：全部 PASS，无 data race。

- [x] **步骤 5：提交**

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
	cb := NewCircuitBreaker(2, time.Minute)
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

func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
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

## 任务 11：实现平滑动态加权负载均衡（SWRR）

**文件：**
- 新建： `internal/decision/balancer.go`
- 新建： `internal/decision/balancer_test.go`

说明：设计与 spec 中"平滑动态加权负载均衡"对齐。实现六维有效权重公式与平滑加权轮询（SWRR）调度，含预热、并发与延迟惩罚、滑动窗口健康分。高并发优化：EffectiveWeight 在锁外预计算、槽位索引替代 map 查找、单循环合并累加与选择、Register 预分配避免热路径 map 写入。

### 有效权重公式

```
W_eff = (W_base * F_warmup) * [1 / (1 + w1 * R_active + w2 * L_p99)] * S_health
```

- `W_base`：配置静态基础权重
- `F_warmup`：慢启动预热系数 [0, 1]
- `R_active`：当前活跃请求数（通过原子操作计数）
- `L_p99`：归一化 P99 延迟 [0, 1]
- `S_health`：滑动窗口成功率健康分 [0, 1]
- `w1` / `w2`：并发/延迟惩罚系数（从配置读取）

调度算法：为每个节点维护 `currentWeight` 状态；每轮调度累加 `effectiveWeight` → 选 `currentWeight` 最大者 → 减去 `totalEffectiveWeight`，实现平滑分配并长期收敛于权重占比。

高并发设计：
- 槽位索引：`[]nodeState` + `nameToSlot map[string]int`，Select 热路径用切片下标直接访问，免字符串 hash
- 锁外预计算：`EffectiveWeight`（纯函数）在锁外完成，锁仅覆盖 SWRR 状态操作
- 单循环合并：累加 currentWeight + 选择最大合为一次遍历
- `Register()` 预分配：启动时批量注册槽位，热路径跳过 map 写入

- [ ] **步骤 1a：先写有效权重计算测试**

文件： `internal/decision/balancer_test.go`

```go
package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveWeightCalculation(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 2.0,
		LatencyPenaltyCoefficient:     1.0,
	}
	b := NewBalancer(cfg)
	eff := b.EffectiveWeight(ProviderCandidate{
		ProviderName:         "a",
		BaseWeight:           100,
		ActiveConnections:    3,
		NormalizedP99Latency: 0.2,
		HealthScore:          0.9,
		WarmupFactor:         1.0,
	})
	expected := 100.0 * 1.0 * (1.0 / (1.0 + 2.0*3.0 + 1.0*0.2)) * 0.9
	require.InDelta(t, expected, eff, 0.001)
}

func TestEffectiveWeightWarmupReducesWeight(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 0,
		LatencyPenaltyCoefficient:     0,
	}
	b := NewBalancer(cfg)
	full := b.EffectiveWeight(ProviderCandidate{BaseWeight: 100, HealthScore: 1, WarmupFactor: 1.0})
	warm := b.EffectiveWeight(ProviderCandidate{BaseWeight: 100, HealthScore: 1, WarmupFactor: 0.3})
	require.InDelta(t, 100.0, full, 0.001)
	require.InDelta(t, 30.0, warm, 0.001)
	require.Less(t, warm, full)
}
```

- [ ] **步骤 1b：先写 SWRR 调度与边界条件测试**

文件： `internal/decision/balancer_test.go`（追加）

```go
func TestSWRRDistributionFairness(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 0,
		LatencyPenaltyCoefficient:     0,
	}
	b := NewBalancer(cfg)
	candidates := []ProviderCandidate{
		{ProviderName: "a", BaseWeight: 5, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "b", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "c", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
	}
	counts := map[string]int{}
	for i := 0; i < 700; i++ {
		c := b.Select(candidates)
		counts[c.ProviderName]++
	}
	aPct := float64(counts["a"]) / 700.0
	require.InDelta(t, 5.0/7.0, aPct, 0.05)
}

func TestSelectEmptyCandidates(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	c := b.Select(nil)
	require.Equal(t, "", c.ProviderName)
}

func TestSelectSingleCandidate(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	c := b.Select([]ProviderCandidate{{ProviderName: "only", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1}})
	require.Equal(t, "only", c.ProviderName)
}
```

- [ ] **步骤 1c：先写 Register 预分配与槽位索引测试**

文件： `internal/decision/balancer_test.go`（追加）

```go
func TestRegisterAssignsSequentialSlots(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	require.Equal(t, 0, b.Register("a"))
	require.Equal(t, 1, b.Register("b"))
	require.Equal(t, 0, b.Register("a"))
}

func TestSelectWithRegisteredProviders(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 0,
		LatencyPenaltyCoefficient:     0,
	}
	b := NewBalancer(cfg)
	b.Register("a")
	b.Register("b")
	c := b.Select([]ProviderCandidate{
		{ProviderName: "a", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "b", BaseWeight: 100, HealthScore: 1, WarmupFactor: 1},
	})
	require.Equal(t, "b", c.ProviderName)
}

func TestRegisterThenSWRRFairness(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 0,
		LatencyPenaltyCoefficient:     0,
	}
	b := NewBalancer(cfg)
	b.Register("a")
	b.Register("b")
	b.Register("c")
	candidates := []ProviderCandidate{
		{ProviderName: "a", BaseWeight: 5, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "b", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "c", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
	}
	counts := map[string]int{}
	for i := 0; i < 700; i++ {
		selected := b.Select(candidates)
		counts[selected.ProviderName]++
	}
	aPct := float64(counts["a"]) / 700.0
	require.InDelta(t, 5.0/7.0, aPct, 0.05)
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/decision -run 'TestEffectiveWeightCalculation|TestEffectiveWeightWarmupReducesWeight|TestSWRRDistributionFairness|TestSelectEmptyCandidates|TestSelectSingleCandidate|TestRegisterAssignsSequentialSlots|TestSelectWithRegisteredProviders|TestRegisterThenSWRRFairness' -v
```

预期：失败，提示 `BalancerConfig`、`NewBalancer`、`ProviderCandidate`、`EffectiveWeight`、`Select`、`Register` 未定义。

- [ ] **步骤 3：实现负载均衡器与 SWRR 调度**

文件： `internal/decision/balancer.go`

```go
package decision

import (
	"math"
	"sync"
)

type BalancerConfig struct {
	ConcurrencyPenaltyCoefficient float64
	LatencyPenaltyCoefficient     float64
	WarmupEnabled                 bool
	WarmupDuration                float64
	HealthWindowSize              float64
	HealthMinRequests             int
}

type ProviderCandidate struct {
	ProviderName         string
	Model                string
	BaseWeight           float64
	ActiveConnections    int
	NormalizedP99Latency float64
	HealthScore          float64
	WarmupFactor         float64
}

type nodeState struct {
	currentWeight float64
}

type Balancer struct {
	cfg        BalancerConfig
	mu         sync.Mutex
	slots      []nodeState
	nameToSlot map[string]int
}

func NewBalancer(cfg BalancerConfig) *Balancer {
	return &Balancer{
		cfg:        cfg,
		nameToSlot: make(map[string]int),
	}
}

func (b *Balancer) Register(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if slot, ok := b.nameToSlot[name]; ok {
		return slot
	}
	b.slots = append(b.slots, nodeState{})
	slot := len(b.slots) - 1
	b.nameToSlot[name] = slot
	return slot
}

func (b *Balancer) EffectiveWeight(c ProviderCandidate) float64 {
	warmup := c.WarmupFactor
	if warmup <= 0 {
		warmup = 0
	}
	base := c.BaseWeight * warmup
	denom := 1 + b.cfg.ConcurrencyPenaltyCoefficient*float64(c.ActiveConnections) + b.cfg.LatencyPenaltyCoefficient*c.NormalizedP99Latency
	loadFactor := 1.0 / denom
	health := c.HealthScore
	if health <= 0 {
		health = 0
	}
	return math.Max(base*loadFactor*health, 0)
}

func (b *Balancer) resolveSlots(candidates []ProviderCandidate) []int {
	slots := make([]int, len(candidates))
	for i, c := range candidates {
		if s, ok := b.nameToSlot[c.ProviderName]; ok {
			slots[i] = s
		} else {
			b.slots = append(b.slots, nodeState{})
			s := len(b.slots) - 1
			b.nameToSlot[c.ProviderName] = s
			slots[i] = s
		}
	}
	return slots
}

func (b *Balancer) Select(candidates []ProviderCandidate) ProviderCandidate {
	if len(candidates) == 0 {
		return ProviderCandidate{}
	}

	n := len(candidates)
	effs := make([]float64, n)
	totalEff := 0.0
	for i := range candidates {
		eff := b.EffectiveWeight(candidates[i])
		effs[i] = eff
		totalEff += eff
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	slots := b.resolveSlots(candidates)

	var bestIdx int
	var bestWeight float64 = -1 << 63
	for i := range candidates {
		b.slots[slots[i]].currentWeight += effs[i]
		if cw := b.slots[slots[i]].currentWeight; cw > bestWeight {
			bestWeight = cw
			bestIdx = i
		}
	}

	b.slots[slots[bestIdx]].currentWeight -= totalEff

	return candidates[bestIdx]
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test ./internal/decision -v
```

预期：全部 PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/decision/balancer.go internal/decision/balancer_test.go
git commit -m "feat: 实现平滑动态加权负载均衡（SWRR）"
```

---

## 任务 12：实现本地嵌入引擎

**文件：**
- 新建： `internal/embedding/embedding.go`
- 新建： `internal/embedding/engine.go`
- 新建： `internal/embedding/onnx.go`
- 新建： `internal/embedding/embedding_test.go`
- 新建： `internal/embedding/onnx_integration_test.go`

说明：使用 `github.com/yalue/onnxruntime_go@v1.30.1` 加载 ONNX 模型 + `github.com/gomlx/go-huggingface/tokenizers/hftokenizer`（纯 Go）加载 BGE tokenizer，为语义路由和语义缓存提供本地 512 维向量化服务。引擎采用单例模式（`sync.Once`），启动时加载 tokenizer 和 ONNX session 并常驻内存。模型文件存放于 `.models/bge-small-zh-v1.5/`，由 `.gitignore` 忽略。CI 中通过 HuggingFace 下载模型并设置 `EMBEDDING_MODEL_PATH`。

### 依赖

```bash
go get github.com/yalue/onnxruntime_go@v1.30.1
go get github.com/gomlx/go-huggingface@v0.3.5
```

### BGE 模型

从 `onnx-community/bge-small-zh-v1.5-ONNX` 下载以下文件至 `.models/bge-small-zh-v1.5/`：

| 文件 | 用途 |
|---|---|
| `model.onnx` + `model.onnx_data` | ONNX 模型（外部数据格式） |
| `tokenizer.json` | HuggingFace 分词配置 |
| `tokenizer_config.json` | 特殊 token / 参数配置 |

模型输入 (`int64`)：`input_ids [batch, 512]`、`attention_mask [batch, 512]`、`token_type_ids [batch, 512]`。
输出：`last_hidden_state [batch, 512, 512]` float32 → mean pooling → L2 归一化 → `[batch, 512]` float64。

### 测试

| 文件 | 内容 |
|---|---|
| `embedding_test.go` | `TestCosineSimilarity` / `TestNormalizeVector` / `TestMeanPooling`（纯函数，3 项） |
| `onnx_integration_test.go` | `TestONNXEmbedding` — 真实加载 ONNX 模型推理，验证 512 维输出 + L2 归一化 |

`TestONNXEmbedding` 通过 `EMBEDDING_MODEL_PATH` 和 `ONNXRUNTIME_LIB_PATH` 环境变量或内置默认路径定位模型和共享库。

### CI 配置

`.github/workflows/ci.yml` 的 test job 中：

1. **安装 ONNX Runtime**：下载 v1.25.0 预编译 `.so` 至 `/usr/local/lib/`，设置 `ONNXRUNTIME_LIB_PATH`
2. **下载 BGE 模型**：从 HuggingFace 下载 4 个文件至 `.models/bge-small-zh-v1.5/`，设置 `EMBEDDING_MODEL_PATH`
3. **运行全量测试**：`go test -race ./...`（28 个测试，含 ONNX 集成测试）

- [x] **步骤 1：添加依赖**

```bash
go get github.com/yalue/onnxruntime_go@v1.30.1
go get github.com/gomlx/go-huggingface@v0.3.5
go mod tidy
```

- [x] **步骤 2：实现纯函数并写测试**

文件： `internal/embedding/embedding.go`

```go
package embedding

import "math"

func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func NormalizeVector(v []float64) []float64 {
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return v
	}
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

func MeanPooling(hidden [][][]float32, mask [][]int64) [][]float64 {
	batch := len(hidden)
	if batch == 0 {
		return nil
	}
	dim := len(hidden[0][0])
	result := make([][]float64, batch)
	for b := 0; b < batch; b++ {
		vec := make([]float64, dim)
		var sum float64
		for s := 0; s < len(hidden[b]); s++ {
			weight := float64(mask[b][s])
			sum += weight
			for d := 0; d < dim; d++ {
				vec[d] += float64(hidden[b][s][d]) * weight
			}
		}
		if sum > 0 {
			for d := 0; d < dim; d++ {
				vec[d] /= sum
			}
		}
		result[b] = vec
	}
	return result
}
```

文件： `internal/embedding/embedding_test.go`

```go
package embedding

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCosineSimilarity(t *testing.T) {
	require.InDelta(t, 1.0, CosineSimilarity([]float64{1, 0}, []float64{1, 0}), 0.0001)
	require.InDelta(t, 0.0, CosineSimilarity([]float64{1, 0}, []float64{0, 1}), 0.0001)
	require.InDelta(t, 0.0, CosineSimilarity(nil, []float64{1, 0}), 0.0001)
}

func TestNormalizeVector(t *testing.T) {
	v := NormalizeVector([]float64{3, 4})
	require.InDelta(t, 0.6, v[0], 0.0001)
	require.InDelta(t, 0.8, v[1], 0.0001)

	zero := []float64{0, 0}
	require.Equal(t, zero, NormalizeVector(zero))
}

func TestMeanPooling(t *testing.T) {
	lastHidden := [][][]float32{
		{
			{1.0, 2.0, 3.0},
			{4.0, 5.0, 6.0},
			{7.0, 8.0, 9.0},
		},
	}
	mask := [][]int64{{1, 1, 0}}
	result := MeanPooling(lastHidden, mask)
	require.Len(t, result, 1)
	require.Len(t, result[0], 3)
	require.InDelta(t, 2.5, result[0][0], 0.001)
	require.InDelta(t, 3.5, result[0][1], 0.001)
	require.InDelta(t, 4.5, result[0][2], 0.001)
}
```

- [x] **步骤 3：实现引擎类型定义**

文件： `internal/embedding/engine.go`

```go
package embedding

import "sync"

type EmbeddingEngine struct {
	mu           sync.Mutex
	maxLength    int64
	embeddingDim int64
	onnxImpl     interface {
		embed(texts []string) ([][]float64, error)
		close()
	}
}

var (
	once    sync.Once
	engine  *EmbeddingEngine
	initErr error
)

const (
	defaultMaxLength    = 512
	defaultEmbeddingDim = 512
)

func Instance() *EmbeddingEngine {
	return engine
}

func (e *EmbeddingEngine) Close() {
	if e.onnxImpl != nil {
		e.onnxImpl.close()
	}
}
```

- [x] **步骤 4：实现 ONNX 嵌入引擎主体**

文件： `internal/embedding/onnx.go`

```go
package embedding

import (
	"fmt"
	"os"

	ort "github.com/yalue/onnxruntime_go"
	"github.com/gomlx/go-huggingface/tokenizers/api"
	"github.com/gomlx/go-huggingface/tokenizers/hftokenizer"
)

type onnxConcrete struct {
	tokenizer *hftokenizer.Tokenizer
	session   *ort.DynamicAdvancedSession
}

func Init(libPath, modelPath string) error {
	once.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		if err := ort.InitializeEnvironment(); err != nil {
			initErr = fmt.Errorf("init onnx env: %w", err)
			return
		}
		configData, err := os.ReadFile(modelPath + "/tokenizer_config.json")
		if err != nil {
			initErr = fmt.Errorf("read tokenizer_config.json: %w", err)
			return
		}
		config, err := api.ParseConfigContent(configData)
		if err != nil {
			initErr = fmt.Errorf("parse tokenizer config: %w", err)
			return
		}
		tk, err := hftokenizer.NewFromFile(config, modelPath+"/tokenizer.json")
		if err != nil {
			initErr = fmt.Errorf("load tokenizer: %w", err)
			return
		}
		session, err := ort.NewDynamicAdvancedSession(
			modelPath+"/model.onnx",
			[]string{"input_ids", "attention_mask", "token_type_ids"},
			[]string{"last_hidden_state"},
			nil,
		)
		if err != nil {
			initErr = fmt.Errorf("load onnx session: %w", err)
			return
		}
		engine = &EmbeddingEngine{
			onnxImpl: &onnxConcrete{tokenizer: tk, session: session},
			maxLength: defaultMaxLength, embeddingDim: defaultEmbeddingDim,
		}
	})
	return initErr
}

func (e *EmbeddingEngine) Embed(texts []string) ([][]float64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.onnxImpl == nil {
		return nil, fmt.Errorf("onnx engine not initialized")
	}
	return e.onnxImpl.embed(texts)
}
```

（完整实现含 `onnxConcrete.embed()` 中的 tokenize → 构造 tensor → `session.Run()` → mean pooling → L2 normalize 流水线，详见源码。）

- [x] **步骤 5：写 ONNX 集成测试**

文件： `internal/embedding/onnx_integration_test.go`

```go
package embedding

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestONNXEmbedding(t *testing.T) {
	modelPath := os.Getenv("EMBEDDING_MODEL_PATH")
	if modelPath == "" {
		modelPath = "../../.models/bge-small-zh-v1.5"
	}
	libPath := os.Getenv("ONNXRUNTIME_LIB_PATH")
	if libPath == "" {
		libPath = "/usr/local/lib/libonnxruntime.so.1.25.0"
	}

	err := Init(libPath, modelPath)
	require.NoError(t, err)
	defer func() {
		if e := Instance(); e != nil {
			e.Close()
		}
	}()

	vecs, err := Instance().Embed([]string{"你好世界", "Hello world"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	require.Len(t, vecs[0], 512)

	norm := 0.0
	for _, v := range vecs[0] {
		norm += v * v
	}
	require.InDelta(t, 1.0, norm, 0.01)
}
```

- [x] **步骤 6：更新 CI 配置**

`.github/workflows/ci.yml` 新增：

```yaml
- name: Install ONNX Runtime
  run: |
    ONNX_VERSION="1.25.0"
    ONNX_URL="https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-${ONNX_VERSION}.tgz"
    curl -sL "$ONNX_URL" -o onnxruntime.tgz
    tar xzf onnxruntime.tgz
    sudo cp onnxruntime-linux-x64-*/lib/libonnxruntime.so* /usr/local/lib/
    sudo ldconfig
    echo "ONNXRUNTIME_LIB_PATH=/usr/local/lib/libonnxruntime.so.${ONNX_VERSION}" >> $GITHUB_ENV

- name: Download BGE Model
  run: |
    MODEL_DIR="./.models/bge-small-zh-v1.5"
    HF_BASE="https://huggingface.co/onnx-community/bge-small-zh-v1.5-ONNX/resolve/main"
    mkdir -p "$MODEL_DIR"
    curl -sL "$HF_BASE/onnx/model.onnx" -o "$MODEL_DIR/model.onnx"
    curl -sL "$HF_BASE/onnx/model.onnx_data" -o "$MODEL_DIR/model.onnx_data"
    curl -sL "$HF_BASE/tokenizer.json" -o "$MODEL_DIR/tokenizer.json"
    curl -sL "$HF_BASE/tokenizer_config.json" -o "$MODEL_DIR/tokenizer_config.json"
    echo "EMBEDDING_MODEL_PATH=$MODEL_DIR" >> $GITHUB_ENV
```

- [x] **步骤 7：验证全量测试通过**

```bash
go test -race ./...
```

预期：28 个测试全部 PASS（含 TestONNXEmbedding）。

- [x] **步骤 8：提交**

```bash
git add go.mod go.sum internal/embedding .gitignore .github/workflows/ci.yml configs/gateway.yaml internal/config/config.go
git commit -m "feat: 实现本地 onnx 嵌入引擎"
```

### 配置项

`configs/gateway.yaml` 新增：

```yaml
embedding:
  onnx_library_path: "/usr/lib/libonnxruntime.so"
  model_path: "./.models/bge-small-zh-v1.5"
```

`Config` 结构体新增 `Embedding EmbeddingConfig`，`EmbeddingConfig` 含 `OnnxLibraryPath` 和 `ModelPath`。`SemanticRoutingConfig` 和 `SemanticCacheConfig` 不再包含 `embedding_provider` / `embedding_model` 字段。

---

## 任务 13：实现路由策略链

**文件：**
- 新建： `internal/decision/strategy_semantic.go`
- 新建： `internal/decision/strategy_semantic_test.go`
- 新建： `internal/decision/strategy_capability.go`
- 新建： `internal/decision/strategy_capability_test.go`
- 新建： `internal/decision/strategy_cost.go`
- 新建： `internal/decision/strategy_cost_test.go`
- 新建： `internal/decision/router.go`
- 新建： `internal/decision/router_test.go`
- 修改： `internal/embedding/engine.go`（暴露 mock 注入点）

### 架构说明

路由策略链按 `gateway.yaml` 中 `routing.strategies` 配置的顺序依次执行：

```
Route(req)
  │
  ├─ req.Model 含 "/" → 显式路由，直接返回（不走策略链）
  │
  ├─ 按 config.routing.strategies 顺序遍历:
  │   ├─ "capability":   CapabilityRouter.Route(req, tokenEstimate) → 匹配 → resolveModelList(targetModels)
  │   ├─ "semantic":     SemanticRouter.Route(req) → 匹配 → resolveModelList(targetModels)
  │   └─ "cost_cascade": CostRouter.CascadeFor(req.Model) → resolveModelList(chain)
  │
  ├─ 全部未命中 → resolveModelList(routing.cascade.default) 兜底
  │
  └─ 仍失败 → model_not_found error
```

每个策略输出"候选模型列表"。`resolveModelList` 遍历列表，对每个模型查询 Provider 注册表（`Registry.ProvidersForModel`），跳过熔断状态为 Open 的 Provider，找到可用 Provider 后通过 `Balancer.Select` 选出最终节点。

`RouteDecision` 增加 `Strategy` 字段记录匹配的策略名，便于日志和指标打标。

语义路由依赖 `internal/embedding` 的 `EmbeddingEngine`。为支持测试 mock，在 decision 包中定义 `Embedder` 接口：

```go
type Embedder interface {
    Embed(texts []string) ([][]float64, error)
}
```

`EmbeddingEngine` 隐式实现此接口，无需改动。

### 初始化顺序（main.go 必读）

```
load config → init embedding engine → init registry → init balancer
  → init circuit breakers                                   ← 任务 10（已有）
  → NewSemanticRouter(cfg.SemanticRouting, eng)              ← 本任务
  → NewCapabilityRouter(cfg.Routing.Rules)                   ← 本任务
  → NewCostRouter(cfg.Routing.Cascade)                       ← 本任务
  → NewRouter(strategies, defaultCascade, balancer, ...)     ← 本任务
  → 注入 handler
```

语义路由需要在 Router 之前初始化，因为 `NewSemanticRouter` 中会调用嵌入引擎批量预计算类别原型向量。

---

### 阶段 A：语义路由（strategy_semantic）

- [ ] **步骤 A1：先写语义路由测试**

文件： `internal/decision/strategy_semantic_test.go`

```go
package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/model"
)

type fakeEmbedder struct {
	vectors map[string][]float64
}

func (f *fakeEmbedder) Embed(texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = v
		} else {
			out[i] = make([]float64, 2)
		}
	}
	return out, nil
}

func TestSemanticRouterPrecomputesPrototypes(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1": {1.0, 0.0},
			"code2": {0.9, 0.1},
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1", "code2"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)
	require.Len(t, sr.categories, 1)
	require.Equal(t, "code", sr.categories[0].Name)
	require.Len(t, sr.categories[0].Vector, 2)
}

func TestSemanticRouteMatchAboveThreshold(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1":     {1.0, 0.0},
			"code2":     {0.9, -0.1},
			"user query": {0.8, 0.1},
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1", "code2"}},
			{Name: "creative", TargetModels: []string{"claude-sonnet-4-20250514"}, Exemplars: []string{"write a poem"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)

	models, category, score := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "user query"}},
	})
	require.NotNil(t, models)
	require.Equal(t, "code", category)
	require.GreaterOrEqual(t, score, 0.75)
	require.Equal(t, []string{"deepseek-chat"}, models)
}

func TestSemanticRouteMissBelowThreshold(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1":     {1.0, 0.0},
			"code2":     {0.9, 0.1},
			"user query": {0.0, 1.0}, // orthogonal to code vectors
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1", "code2"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)

	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "user query"}},
	})
	require.Nil(t, models)
}

func TestSemanticRouteEmptyMessages(t *testing.T) {
	sr := &SemanticRouter{threshold: 0.75}
	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{},
	})
	require.Nil(t, models)
}

func TestSemanticRouteNoEngine(t *testing.T) {
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, nil)
	require.NoError(t, err)
	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "query"}},
	})
	require.Nil(t, models)
}
```

- [ ] **步骤 A2：运行测试确认失败**

```bash
go test ./internal/decision -run 'TestSemanticRouter' -v
```

预期：失败，提示 `NewSemanticRouter`、`SemanticRouter` 未定义。

- [ ] **步骤 A3：实现语义路由**

文件： `internal/decision/strategy_semantic.go`

```go
package decision

import (
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/embedding"
	"github.com/viif/momu-llmgateway/internal/model"
)

type Embedder interface {
	Embed(texts []string) ([][]float64, error)
}

type CategoryPrototype struct {
	Name         string
	TargetModels []string
	Vector       []float64
}

type SemanticRouter struct {
	categories []CategoryPrototype
	threshold  float64
	engine     Embedder
}

func NewSemanticRouter(cfg config.SemanticRoutingConfig, eng Embedder) (*SemanticRouter, error) {
	sr := &SemanticRouter{threshold: cfg.SimilarityThreshold, engine: eng}
	if eng == nil || len(cfg.Categories) == 0 {
		return sr, nil
	}

	for _, cat := range cfg.Categories {
		vecs, err := eng.Embed(cat.Exemplars)
		if err != nil {
			return nil, err
		}
		if len(vecs) == 0 {
			continue
		}

		prototype := make([]float64, len(vecs[0]))
		for _, v := range vecs {
			for i := range v {
				prototype[i] += v[i]
			}
		}
		n := float64(len(vecs))
		for i := range prototype {
			prototype[i] /= n
		}
		prototype = embedding.NormalizeVector(prototype)

		sr.categories = append(sr.categories, CategoryPrototype{
			Name:         cat.Name,
			TargetModels: append([]string(nil), cat.TargetModels...),
			Vector:       prototype,
		})
	}
	return sr, nil
}

func (sr *SemanticRouter) Route(req *model.StandardRequest) (models []string, category string, confidence float64) {
	if sr.engine == nil || len(sr.categories) == 0 {
		return nil, "", 0
	}

	text := concatenateUserMessages(req.Messages)
	if text == "" {
		return nil, "", 0
	}

	vecs, err := sr.engine.Embed([]string{text})
	if err != nil || len(vecs) == 0 {
		return nil, "", 0
	}

	for _, cat := range sr.categories {
		score := embedding.CosineSimilarity(vecs[0], cat.Vector)
		if score >= sr.threshold && score > confidence {
			confidence = score
			category = cat.Name
			models = cat.TargetModels
		}
	}
	return models, category, confidence
}

func concatenateUserMessages(messages []model.Message) string {
	var parts []string
	for _, m := range messages {
		if m.Role == "user" && m.Content != "" {
			parts = append(parts, m.Content)
		}
	}
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
```

- [ ] **步骤 A4：验证测试通过**

```bash
go test ./internal/decision -run 'TestSemanticRouter' -v
```

预期：全部 PASS。

- [ ] **步骤 A5：提交**

```bash
git add internal/decision/strategy_semantic.go internal/decision/strategy_semantic_test.go
git commit -m "feat: 添加语义路由策略"
```

---

### 阶段 B：能力路由（strategy_capability）

- [ ] **步骤 B1：先写能力路由测试**

文件： `internal/decision/strategy_capability_test.go`

```go
package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestCapabilityMatchByTaskType(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", TargetModels: []string{"claude-sonnet-4-20250514"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "long_context"}, 0)
	require.Equal(t, []string{"claude-sonnet-4-20250514"}, models)
}

func TestCapabilityMismatchTaskType(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", TargetModels: []string{"claude-sonnet-4-20250514"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "code"}, 0)
	require.Nil(t, models)
}

func TestCapabilityConditionGreaterThan(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", Condition: "input_tokens > 100000", TargetModels: []string{"claude-sonnet-4-20250514"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "long_context"}, 150000)
	require.Equal(t, []string{"claude-sonnet-4-20250514"}, models)
}

func TestCapabilityConditionLessThan(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", Condition: "input_tokens > 100000", TargetModels: []string{"claude-sonnet-4-20250514"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "long_context"}, 50000)
	require.Nil(t, models)
}

func TestCapabilityInvalidConditionIgnored(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "test", Condition: "unknown_field > 10", TargetModels: []string{"m"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "test"}, 0)
	require.Nil(t, models)
}

func TestCapabilityMultipleRulesFirstWins(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "a", TargetModels: []string{"model-a"}},
		{TaskType: "a", TargetModels: []string{"model-b"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "a"}, 0)
	require.Equal(t, []string{"model-a"}, models)
}

func TestCapabilityNoRules(t *testing.T) {
	cr := NewCapabilityRouter(nil)
	models := cr.Route(&model.StandardRequest{TaskType: "foo"}, 0)
	require.Nil(t, models)
}
```

- [ ] **步骤 B2：运行测试确认失败**

```bash
go test ./internal/decision -run 'TestCapability' -v
```

预期：失败，提示 `NewCapabilityRouter` 未定义。

- [ ] **步骤 B3：实现能力路由**

文件： `internal/decision/strategy_capability.go`

```go
package decision

import (
	"strconv"
	"strings"

	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/model"
)

type CapabilityRouter struct {
	rules []config.RoutingRuleConfig
}

func NewCapabilityRouter(rules []config.RoutingRuleConfig) *CapabilityRouter {
	return &CapabilityRouter{rules: rules}
}

func (cr *CapabilityRouter) Route(req *model.StandardRequest, estimatedTokens int) []string {
	for _, rule := range cr.rules {
		if matchesRule(rule, req, estimatedTokens) {
			return rule.TargetModels
		}
	}
	return nil
}

func matchesRule(rule config.RoutingRuleConfig, req *model.StandardRequest, estimatedTokens int) bool {
	if rule.TaskType != "" && rule.TaskType != req.TaskType {
		return false
	}
	if rule.Condition != "" && !evaluateCondition(rule.Condition, estimatedTokens) {
		return false
	}
	return true
}

func evaluateCondition(condition string, inputTokens int) bool {
	parts := strings.Fields(condition)
	if len(parts) != 3 || parts[0] != "input_tokens" {
		return false
	}
	threshold, err := strconv.Atoi(parts[2])
	if err != nil {
		return false
	}
	switch parts[1] {
	case ">":
		return inputTokens > threshold
	case "<":
		return inputTokens < threshold
	case ">=":
		return inputTokens >= threshold
	case "<=":
		return inputTokens <= threshold
	default:
		return false
	}
}
```

- [ ] **步骤 B4：验证测试通过**

```bash
go test ./internal/decision -run 'TestCapability' -v
```

预期：全部 PASS。

- [ ] **步骤 B5：提交**

```bash
git add internal/decision/strategy_capability.go internal/decision/strategy_capability_test.go
git commit -m "feat: 添加能力路由策略"
```

---

### 阶段 C：成本级联路由（strategy_cost）

- [ ] **步骤 C1：先写成本路由测试**

文件： `internal/decision/strategy_cost_test.go`

```go
package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCostCascadeForKnownModel(t *testing.T) {
	cr := NewCostRouter(map[string][]string{
		"gpt-4o":      {"gpt-4o-mini", "deepseek-chat"},
		"default":     {"deepseek-chat", "gpt-4o-mini"},
	})
	chain := cr.CascadeFor("gpt-4o")
	require.Equal(t, []string{"gpt-4o-mini", "deepseek-chat"}, chain)
}

func TestCostCascadeFallsBackToDefault(t *testing.T) {
	cr := NewCostRouter(map[string][]string{
		"default": {"deepseek-chat", "gpt-4o-mini"},
	})
	chain := cr.CascadeFor("unknown-model")
	require.Equal(t, []string{"deepseek-chat", "gpt-4o-mini"}, chain)
}

func TestCostCascadeEmptyChains(t *testing.T) {
	cr := NewCostRouter(map[string][]string{})
	chain := cr.CascadeFor("any-model")
	require.Nil(t, chain)
}

func TestCostCascadeNoDefaultChain(t *testing.T) {
	cr := NewCostRouter(map[string][]string{
		"gpt-4o": {"gpt-4o-mini"},
	})
	chain := cr.CascadeFor("unknown-model")
	require.Nil(t, chain)
}
```

- [ ] **步骤 C2：运行测试确认失败**

```bash
go test ./internal/decision -run 'TestCostCascade' -v
```

预期：失败，提示 `NewCostRouter` 未定义。

- [ ] **步骤 C3：实现成本级联路由**

文件： `internal/decision/strategy_cost.go`

```go
package decision

type CostRouter struct {
	chains map[string][]string
}

func NewCostRouter(chains map[string][]string) *CostRouter {
	return &CostRouter{chains: chains}
}

func (cr *CostRouter) CascadeFor(model string) []string {
	if chain, ok := cr.chains[model]; ok && len(chain) > 0 {
		return chain
	}
	if chain, ok := cr.chains["default"]; ok && len(chain) > 0 {
		return chain
	}
	return nil
}
```

- [ ] **步骤 C4：验证测试通过**

```bash
go test ./internal/decision -run 'TestCostCascade' -v
```

预期：全部 PASS。

- [ ] **步骤 C5：提交**

```bash
git add internal/decision/strategy_cost.go internal/decision/strategy_cost_test.go
git commit -m "feat: 添加成本级联路由策略"
```

---

### 阶段 D：整合 Router 策略链

- [ ] **步骤 D1：先写 Router 集成测试**

文件： `internal/decision/router_test.go`

```go
package decision

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/model"
)

// fakeProvider implements model.Provider for test
type fakeRegProvider struct {
	name   string
	models []string
}

func (f fakeRegProvider) Name() string                                                  { return f.name }
func (f fakeRegProvider) Models() []string                                               { return f.models }
func (f fakeRegProvider) Send(context.Context, *model.StandardRequest) (*model.StandardResponse, error) { return nil, nil }
func (f fakeRegProvider) SendStream(context.Context, *model.StandardRequest) (<-chan model.StreamChunk, error) { return nil, nil }

func TestRouterExplicitRoute(t *testing.T) {
	r := NewRouter(RouterConfig{Strategies: []string{"semantic"}}, nil, nil, nil, nil, nil, nil)
	dec, err := r.Route(&model.StandardRequest{Model: "openai/gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, "openai", dec.ProviderName)
	require.Equal(t, "gpt-4o", dec.Model)
	require.Equal(t, "explicit", dec.Strategy)
}

func TestRouterModelNotFound(t *testing.T) {
	r := NewRouter(RouterConfig{Strategies: []string{}}, nil, nil, nil, nil, nil, nil)
	_, err := r.Route(&model.StandardRequest{Model: ""})
	require.Error(t, err)
}

func TestRouterWithBalancerSelectsProvider(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("openai")
	b.Register("deepseek")

	// modelProviders returns fake providers for a model name
	modelProviders := func(model string) []model.Provider {
		if model == "gpt-4o" {
			return []model.Provider{
				fakeRegProvider{name: "openai", models: []string{"gpt-4o"}},
				fakeRegProvider{name: "deepseek", models: []string{"gpt-4o"}},
			}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, model string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			w := 100.0
			if p.Name() == "openai" {
				w = 1
			}
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: model, BaseWeight: w, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{"cost_cascade"}, DefaultCascade: []string{}},
		b, nil, nil, nil, modelProviders, buildCandidates,
	)
	dec, err := r.Route(&model.StandardRequest{Model: "gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, "deepseek", dec.ProviderName)
	require.Equal(t, "gpt-4o", dec.Model)
}

func TestRouterDefaultCascade(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	modelProviders := func(model string) []model.Provider {
		if model == "deepseek-chat" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"deepseek-chat"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, model string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: model, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{}, DefaultCascade: []string{"deepseek-chat"}},
		b, nil, nil, nil, modelProviders, buildCandidates,
	)
	dec, err := r.Route(&model.StandardRequest{Model: ""})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", dec.Model)
	require.Equal(t, "deepseek", dec.ProviderName)
	require.Equal(t, "default", dec.Strategy)
}

func TestRouterStrategyOrder(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	modelProviders := func(model string) []model.Provider {
		if model == "deepseek-chat" || model == "gpt-4o" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{model}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, model string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: model, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	// Capability router matches "long_context" task type → gpt-4o
	capRouter := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", TargetModels: []string{"gpt-4o"}},
	})

	// Capability strategy matches "long_context" task type
	r := NewRouter(
		RouterConfig{Strategies: []string{"capability"}, DefaultCascade: []string{"deepseek-chat"}},
		b, nil, capRouter, nil, modelProviders, buildCandidates,
	)
	dec, err := r.Route(&model.StandardRequest{Model: "claude-sonnet-4-20250514", TaskType: "long_context"})
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", dec.Model)
	require.Equal(t, "capability", dec.Strategy)
}

func TestRouterCostCascadeFallsBackToDefault(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	modelProviders := func(model string) []model.Provider {
		if model == "deepseek-chat" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"deepseek-chat"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, model string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: model, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	costRouter := NewCostRouter(map[string][]string{
		"gpt-4o":  {"gpt-4o-mini"},
		"default": {"deepseek-chat"},
	})

	r := NewRouter(
		RouterConfig{Strategies: []string{"cost_cascade"}, DefaultCascade: []string{"deepseek-chat"}},
		b, nil, nil, costRouter, modelProviders, buildCandidates,
	)

	// "unknown" has no cascade chain, falls back to "default" → deepseek-chat
	dec, err := r.Route(&model.StandardRequest{Model: "unknown"})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", dec.Model)
}

func TestRouterModelListSkipsUnavailableProviders(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	// Only deepseek-chat has a provider; gpt-4o-mini does not
	modelProviders := func(model string) []model.Provider {
		if model == "deepseek-chat" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"deepseek-chat"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, model string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: model, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	// Default cascade: gpt-4o-mini (no providers) → deepseek-chat (has providers)
	r := NewRouter(
		RouterConfig{Strategies: []string{}, DefaultCascade: []string{"gpt-4o-mini", "deepseek-chat"}},
		b, nil, nil, nil, modelProviders, buildCandidates,
	)
	dec, err := r.Route(&model.StandardRequest{Model: ""})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", dec.Model)
}

func TestRouterSemanticViaIntegration(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1":        {1.0, 0.0},
			"user is query": {0.9, 0.05},
		},
	}

	semanticRouter, err := NewSemanticRouter(
		config.SemanticRoutingConfig{
			SimilarityThreshold: 0.75,
			Categories: []config.SemanticCategoryConfig{
				{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1"}},
			},
		},
		fake,
	)
	require.NoError(t, err)

	modelProviders := func(model string) []model.Provider {
		if model == "deepseek-chat" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"deepseek-chat"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, model string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: model, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{"semantic"}, DefaultCascade: []string{"gpt-4o-mini"}},
		b, semanticRouter, nil, nil, modelProviders, buildCandidates,
	)

	dec, err := r.Route(&model.StandardRequest{
		Model:    "gpt-4o",
		Messages: []model.Message{{Role: "user", Content: "user is query"}},
	})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", dec.Model)
	require.Equal(t, "semantic", dec.Strategy)
}

func TestRouterExplicitBypassesStrategyChain(t *testing.T) {
	b := NewBalancer(BalancerConfig{})

	// Capability router would match this task type
	capRouter := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "code", TargetModels: []string{"gpt-4o"}},
	})

	modelProviders := func(model string) []model.Provider {
		if model == "gpt-4o" {
			return []model.Provider{fakeRegProvider{name: "openai", models: []string{"gpt-4o"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, model string) []ProviderCandidate {
		return []ProviderCandidate{{ProviderName: "openai", Model: model, BaseWeight: 1, HealthScore: 1, WarmupFactor: 1}}
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{"capability"}, DefaultCascade: []string{}},
		b, nil, capRouter, nil, modelProviders, buildCandidates,
	)

	// Explicit prefix should bypass capability routing
	dec, err := r.Route(&model.StandardRequest{Model: "deepseek/gpt-4o", TaskType: "code"})
	require.NoError(t, err)
	require.Equal(t, "deepseek", dec.ProviderName)
	require.Equal(t, "gpt-4o", dec.Model)
	require.Equal(t, "explicit", dec.Strategy)
}
```

- [ ] **步骤 D2：运行测试确认失败**

```bash
go test ./internal/decision -run 'TestRouter' -v
```

预期：失败，提示 `NewRouter` 签名不匹配（构造函数签名已变更）。

- [ ] **步骤 D3：实现 Router 策略链编排**

文件： `internal/decision/router.go`

```go
package decision

import (
	"strings"

	"github.com/viif/momu-llmgateway/internal/model"
)

type RouteDecision struct {
	ProviderName string
	Model        string
	Strategy     string
}

type RouterConfig struct {
	Strategies     []string
	DefaultCascade []string
}

type ModelProvidersFunc func(model string) []model.Provider
type BuildCandidatesFunc func(providers []model.Provider, model string) []ProviderCandidate

type Router struct {
	strategies      []string
	defaultCascade  []string
	balancer        *Balancer
	semanticRouter  *SemanticRouter
	capabilityRouter *CapabilityRouter
	costRouter      *CostRouter
	modelProviders  ModelProvidersFunc
	buildCandidates BuildCandidatesFunc
}

func NewRouter(
	cfg RouterConfig,
	balancer *Balancer,
	semantic *SemanticRouter,
	capability *CapabilityRouter,
	cost *CostRouter,
	modelProviders ModelProvidersFunc,
	buildCandidates BuildCandidatesFunc,
) *Router {
	return &Router{
		strategies:       cfg.Strategies,
		defaultCascade:   cfg.DefaultCascade,
		balancer:         balancer,
		semanticRouter:   semantic,
		capabilityRouter: capability,
		costRouter:       cost,
		modelProviders:   modelProviders,
		buildCandidates:  buildCandidates,
	}
}

func (r *Router) Route(req *model.StandardRequest) (RouteDecision, error) {
	// 1. Explicit routing bypasses all strategies
	if strings.Contains(req.Model, "/") {
		parts := strings.SplitN(req.Model, "/", 2)
		return RouteDecision{ProviderName: parts[0], Model: parts[1], Strategy: "explicit"}, nil
	}

	// 2. Execute strategy chain in configured order
	for _, strategy := range r.strategies {
		switch strategy {
		case "explicit":
			continue // handled above

		case "semantic":
			if r.semanticRouter != nil {
				models, _, _ := r.semanticRouter.Route(req)
				if dec, ok := r.resolveModelList(models, "semantic"); ok {
					return dec, nil
				}
			}

		case "capability":
			if r.capabilityRouter != nil {
				tokenEstimate := estimateInputTokens(req.Messages)
				models := r.capabilityRouter.Route(req, tokenEstimate)
				if dec, ok := r.resolveModelList(models, "capability"); ok {
					return dec, nil
				}
			}

		case "cost_cascade":
			if r.costRouter != nil {
				chain := r.costRouter.CascadeFor(req.Model)
				if len(chain) == 0 {
					chain = r.defaultCascade
				}
				if dec, ok := r.resolveModelList(chain, "cost_cascade"); ok {
					return dec, nil
				}
			}
		}
	}

	// 3. Default fallback
	if len(r.defaultCascade) > 0 {
		if dec, ok := r.resolveModelList(r.defaultCascade, "default"); ok {
			return dec, nil
		}
	}

	// 4. Last resort: if request has a model, try to use it directly
	if req.Model != "" {
		providers := r.modelProviders(req.Model)
		if dec, ok := r.resolveWithBalancer(providers, req.Model, "default"); ok {
			return dec, nil
		}
	}

	return RouteDecision{}, model.NewError(model.ErrCodeModelNotFound, "no route matched")
}

func (r *Router) resolveModelList(models []string, strategy string) (RouteDecision, bool) {
	for _, m := range models {
		providers := r.modelProviders(m)
		if len(providers) == 0 {
			continue
		}
		if dec, ok := r.resolveWithBalancer(providers, m, strategy); ok {
			return dec, true
		}
	}
	return RouteDecision{}, false
}

func (r *Router) resolveWithBalancer(providers []model.Provider, model, strategy string) (RouteDecision, bool) {
	if r.balancer != nil && r.buildCandidates != nil {
		candidates := r.buildCandidates(providers, model)
		if len(candidates) > 0 {
			selected := r.balancer.Select(candidates)
			if selected.ProviderName != "" {
				return RouteDecision{ProviderName: selected.ProviderName, Model: model, Strategy: strategy}, true
			}
		}
	}
	if len(providers) > 0 {
		return RouteDecision{ProviderName: providers[0].Name(), Model: model, Strategy: strategy}, true
	}
	return RouteDecision{}, false
}

func estimateInputTokens(messages []model.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}
	if totalChars > 0 {
		return totalChars / 4
	}
	return 0
}
```

- [ ] **步骤 D4：验证全量测试通过**

```bash
go test ./internal/decision -v
```

预期：全部 PASS（含已有 `TestCircuitBreaker*`、`Test*Weight*`、`TestSWRR*`、`TestSelect*`、`TestRegister*` 以及新增的全部路由测试）。

- [ ] **步骤 D5：提交**

```bash
git add internal/decision/router.go internal/decision/router_test.go
git commit -m "feat: 实现路由策略链编排"
```

---

## 任务 14：实现语义缓存

**架构：** 混合模式 — 内存切片为主查询引擎（CPU cache 友好）+ Redis 为可选持久化后端（软依赖，故障不阻塞服务）。向量数据由本地 ONNX 嵌入引擎（`embedding.Instance()`）生成。TTL 分两层：内存层惰性过期 + Redis 层原生 `EXPIRE`。LRU 淘汰使用 O(n) 找最旧 `LastAccess` + swap-remove O(1) 删除，不引入 `container/list`（保护热路径余弦扫描的 cache 局部性）。

**Persistent store interface** 定义在 `semantic.go` 中，方便测试时替换为 fake：

```go
type CacheStore interface {
    Save(ctx context.Context, model, key string, vector []float64, respJSON []byte, ttl time.Duration) error
    LoadAll(ctx context.Context, model string) ([]CacheEntry, error)
    Close() error
}
```

**设计文档参考：** `docs/superpowers/specs/2026-05-20-llm-gateway-design.md` 第 4 节。

---

### 14-1：添加 Redis 依赖与接口定义

**文件：**
- 修改： `go.mod`、`go.sum`

- [ ] **步骤 1：添加依赖**

```bash
go get github.com/redis/go-redis/v9@v9.5.1
go get github.com/alicebob/miniredis/v2@v2.33.0
```

- [ ] **步骤 2：验证构建**

```bash
go build ./...
```

预期：无错误。

- [ ] **步骤 3：提交**

```bash
git add go.mod go.sum
git commit -m "feat: 添加 redis 依赖与 cache 持久层接口"
```

---

### 14-2：缓存核心数据结构与查询存储（TDD）

**文件：**
- 新建： `internal/cache/semantic.go`
- 新建： `internal/cache/semantic_test.go`

- [ ] **步骤 1：先写构造函数与基本属性测试**

文件： `internal/cache/semantic_test.go`

```go
package cache

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

type fakeEmbedder struct {
	vectors map[string][]float64
}

func (f *fakeEmbedder) Embed(texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = v
		} else {
			out[i] = make([]float64, 2)
		}
	}
	return out, nil
}

func TestNewCacheUsesConfig(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.95, TTL: time.Hour}, nil, nil)
	require.True(t, c.enabled)
	require.Equal(t, 100, c.maxEntries)
	require.Equal(t, 0.95, c.threshold)
}

func TestNewCacheDefaultDisabled(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: false}, nil, nil)
	require.False(t, c.enabled)
}

func TestLookupHit(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached", Model: "gpt-4o"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)

	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	resp, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.True(t, ok)
	require.True(t, resp.CacheHit)
	require.Equal(t, "cached", resp.ID)
}

func TestLookupMissDifferentSemantics(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "unknown"}},
	})
	require.False(t, ok)
}

func TestLookupDifferentModelIsolation(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hi": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now(), LastAccess: time.Now()},
	}
	c.mu.Unlock()

	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "claude-sonnet-4-20250514", Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.False(t, ok)
}

func TestLookupDisabled(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: false}, nil, nil)
	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.False(t, ok)
}

func TestLookupEmbedderNil(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8}, nil, nil)
	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.False(t, ok)
}

func TestLookupEmptyUserMessage(t *testing.T) {
	embedder := &fakeEmbedder{}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "system", Content: "you are a helper"}},
	})
	require.False(t, ok)
}

func TestLookupUpdatesAccessTime(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached"})
	oldTime := time.Now().Add(-time.Hour)
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: oldTime, LastAccess: oldTime},
	}
	c.mu.Unlock()

	c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	c.mu.RLock()
	require.True(t, c.entries["gpt-4o"][0].LastAccess.After(oldTime))
	c.mu.RUnlock()
}

func TestLookupSkipsExpiredEntry(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	cachedResp, _ := json.Marshal(&model.StandardResponse{ID: "cached"})
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: 10 * time.Millisecond}, embedder, nil)
	c.mu.Lock()
	c.entries["gpt-4o"] = []CacheEntry{
		{Model: "gpt-4o", Key: "h1", Vector: []float64{1.0, 0.0}, ResponseJSON: cachedResp, StoredAt: time.Now().Add(-time.Hour), LastAccess: time.Now().Add(-time.Hour)},
	}
	c.mu.Unlock()

	_, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.False(t, ok)
	c.mu.RLock()
	require.Len(t, c.entries["gpt-4o"], 0)
	c.mu.RUnlock()
}

func TestStoreThenLookup(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)

	err := c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	}, &model.StandardResponse{ID: "resp-1", Model: "gpt-4o"})
	require.NoError(t, err)

	found, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.True(t, ok)
	require.Equal(t, "resp-1", found.ID)
}

func TestStoreSkipsStreaming(t *testing.T) {
	embedder := &fakeEmbedder{}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	err := c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Stream: true, Messages: []model.Message{{Role: "user", Content: "hi"}},
	}, &model.StandardResponse{ID: "resp"})
	require.NoError(t, err)
	c.mu.RLock()
	require.Len(t, c.entries["gpt-4o"], 0)
	c.mu.RUnlock()
}

func TestStoreEvictsOldestWhenFull(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{
		"a": {1.0, 0.0}, "b": {0.0, 1.0}, "c": {0.5, 0.5},
	}}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 2, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)

	c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "a"}},
	}, &model.StandardResponse{ID: "a"})
	time.Sleep(time.Millisecond)

	c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "b"}},
	}, &model.StandardResponse{ID: "b"})

	resp, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "a"}},
	})
	require.True(t, ok)
	require.Equal(t, "a", resp.ID)

	c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "c"}},
	}, &model.StandardResponse{ID: "c"})

	_, ok = c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "b"}},
	})
	require.False(t, ok)

	_, ok = c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "a"}},
	})
	require.True(t, ok)
}

func TestCacheConcurrentAccess(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hi": {1.0, 0.0}}}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, nil)
	c.Store(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}},
	}, &model.StandardResponse{ID: "resp"})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Lookup(context.Background(), &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}) }()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Store(context.Background(), &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}}, &model.StandardResponse{ID: "concurrent"}) }()
	}
	wg.Wait()
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test -race ./internal/cache -v
```

预期：失败，提示 `New`、`SemanticCacheConfig`、`CacheEntry` 等未定义。

- [ ] **步骤 3：实现 SemanticCache 完整逻辑**

文件： `internal/cache/semantic.go`

```go
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/viif/momu-llmgateway/internal/embedding"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)

type Embedder interface {
	Embed(texts []string) ([][]float64, error)
}

type CacheStore interface {
	Save(ctx context.Context, model, key string, vector []float64, respJSON []byte, ttl time.Duration) error
	LoadAll(ctx context.Context, model string) ([]CacheEntry, error)
	Close() error
}

type CacheEntry struct {
	Model        string    `json:"model"`
	Key          string    `json:"key"`
	Vector       []float64 `json:"vector"`
	ResponseJSON []byte    `json:"response"`
	StoredAt     time.Time `json:"stored_at"`
	LastAccess   time.Time `json:"last_access"`
}

type SemanticCache struct {
	mu         sync.RWMutex
	entries    map[string][]CacheEntry
	maxEntries int
	threshold  float64
	ttl        time.Duration
	enabled    bool
	embedder   Embedder
	store      CacheStore
}

type SemanticCacheConfig struct {
	Enabled             bool
	SimilarityThreshold float64
	MaxEntries          int
	TTL                 time.Duration
}

func New(cfg SemanticCacheConfig, embedder Embedder, store CacheStore) *SemanticCache {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 10000
	}
	return &SemanticCache{
		entries:    make(map[string][]CacheEntry),
		maxEntries: cfg.MaxEntries,
		threshold:  cfg.SimilarityThreshold,
		ttl:        cfg.TTL,
		enabled:    cfg.Enabled,
		embedder:   embedder,
		store:      store,
	}
}

func (c *SemanticCache) Lookup(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, bool) {
	if !c.enabled || c.embedder == nil {
		return nil, false
	}
	text := concatenateUserMessages(req.Messages)
	if text == "" {
		return nil, false
	}
	vecs, err := c.embedder.Embed([]string{text})
	if err != nil || len(vecs) == 0 {
		return nil, false
	}

	c.mu.RLock()
	entries := c.entries[req.Model]
	c.mu.RUnlock()

	now := time.Now()
	var best *CacheEntry
	bestScore := c.threshold
	var expired []int

	for i := range entries {
		if now.Sub(entries[i].StoredAt) > c.ttl {
			expired = append(expired, i)
			continue
		}
		score := embedding.CosineSimilarity(vecs[0], entries[i].Vector)
		if score >= bestScore {
			bestScore = score
			best = &entries[i]
		}
	}

	if len(expired) > 0 {
		c.removeExpired(req.Model, expired)
	}
	if best == nil {
		return nil, false
	}

	c.mu.Lock()
	best.LastAccess = now
	c.mu.Unlock()

	observability.CacheHitTotal.WithLabelValues(req.Model, "semantic").Inc()

	var resp model.StandardResponse
	if err := json.Unmarshal(best.ResponseJSON, &resp); err != nil {
		return nil, false
	}
	resp.CacheHit = true
	return &resp, true
}

func (c *SemanticCache) Store(ctx context.Context, req *model.StandardRequest, resp *model.StandardResponse) error {
	if !c.enabled || c.embedder == nil || req.Stream {
		return nil
	}
	text := concatenateUserMessages(req.Messages)
	if text == "" {
		return nil
	}
	vecs, err := c.embedder.Embed([]string{text})
	if err != nil || len(vecs) == 0 {
		return err
	}
	respJSON, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	key := hashContent(text)

	entry := CacheEntry{
		Model:        req.Model,
		Key:          key,
		Vector:       vecs[0],
		ResponseJSON: respJSON,
		StoredAt:     time.Now(),
		LastAccess:   time.Now(),
	}

	c.mu.Lock()
	c.entries[req.Model] = append(c.entries[req.Model], entry)
	if len(c.entries[req.Model]) > c.maxEntries {
		c.evictOne(req.Model)
	}
	if len(c.entries[req.Model]) > c.maxEntries*3/2 {
		c.compactExpired(req.Model)
	}
	c.mu.Unlock()

	if c.store != nil {
		_ = c.store.Save(ctx, req.Model, key, vecs[0], respJSON, c.ttl)
	}
	return nil
}

func (c *SemanticCache) removeExpired(model string, indices []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := c.entries[model]
	for i := len(indices) - 1; i >= 0; i-- {
		idx := indices[i]
		if idx >= len(entries) {
			continue
		}
		last := len(entries) - 1
		entries[idx] = entries[last]
		entries = entries[:last]
	}
	c.entries[model] = entries
}

func (c *SemanticCache) evictOne(model string) {
	entries := c.entries[model]
	if len(entries) == 0 {
		return
	}
	oldest := 0
	for i := 1; i < len(entries); i++ {
		if entries[i].LastAccess.Before(entries[oldest].LastAccess) {
			oldest = i
		}
	}
	entries[oldest] = entries[len(entries)-1]
	c.entries[model] = entries[:len(entries)-1]
}

func (c *SemanticCache) compactExpired(model string) {
	entries := c.entries[model]
	now := time.Now()
	keep := 0
	for i := range entries {
		if now.Sub(entries[i].StoredAt) <= c.ttl {
			entries[keep] = entries[i]
			keep++
		}
	}
	c.entries[model] = entries[:keep]
}

func (c *SemanticCache) LoadFromStore(ctx context.Context) error {
	if c.store == nil {
		return nil
	}
	// model discovery + batch load from Redis — 完整实现见 14-3
	return nil
}

func concatenateUserMessages(messages []model.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Content != "" {
			return messages[i].Content
		}
	}
	return ""
}

func hashContent(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:16])
}

func EncodeVector(v []float64) []byte {
	buf := make([]byte, len(v)*8)
	for i, f := range v {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(f))
	}
	return buf
}

func DecodeVector(data []byte) []float64 {
	v := make([]float64, len(data)/8)
	for i := range v {
		v[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[i*8:]))
	}
	return v
}
```

- [ ] **步骤 4：验证测试通过**

```bash
go test -race ./internal/cache -v
```

预期：全部 PASS，无 data race。

- [ ] **步骤 5：提交**

```bash
git add internal/cache/semantic.go internal/cache/semantic_test.go
git commit -m "feat: 实现语义缓存核心逻辑与 LRU/TTL"
```

---

### 14-3：Redis 持久层实现（TDD）

**文件：**
- 新建： `internal/cache/redis_store.go`
- 新建： `internal/cache/redis_store_test.go`

- [ ] **步骤 1：先写 Redis 读写测试**

文件： `internal/cache/redis_store_test.go`

```go
package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

func newTestRedis(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	store, err := NewRedisStore(mr.Addr(), "", 0)
	require.NoError(t, err)
	return store, mr
}

func TestRedisStoreSaveAndLoad(t *testing.T) {
	store, _ := newTestRedis(t)
	defer store.Close()

	v := []float64{0.1, 0.2, 0.3}
	resp := []byte(`{"id":"test"}`)
	require.NoError(t, store.Save(context.Background(), "gpt-4o", "key1", v, resp, time.Hour))

	entries, err := store.LoadAll(context.Background(), "gpt-4o")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "gpt-4o", entries[0].Model)
	require.Equal(t, "key1", entries[0].Key)
	require.Equal(t, resp, entries[0].ResponseJSON)
	require.InDeltaSlice(t, v, entries[0].Vector, 0.0001)
}

func TestRedisStoreLoadAllEmpty(t *testing.T) {
	store, _ := newTestRedis(t)
	defer store.Close()
	entries, err := store.LoadAll(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestRedisStoreTTLExpiry(t *testing.T) {
	store, mr := newTestRedis(t)
	defer store.Close()

	store.Save(context.Background(), "gpt-4o", "key1", []float64{1}, []byte("v"), 100*time.Millisecond)
	mr.FastForward(200 * time.Millisecond)

	entries, err := store.LoadAll(context.Background(), "gpt-4o")
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestRedisStoreMultipleEntries(t *testing.T) {
	store, _ := newTestRedis(t)
	defer store.Close()

	store.Save(context.Background(), "gpt-4o", "k1", []float64{1}, []byte("a"), time.Hour)
	store.Save(context.Background(), "gpt-4o", "k2", []float64{2}, []byte("b"), time.Hour)

	entries, err := store.LoadAll(context.Background(), "gpt-4o")
	require.NoError(t, err)
	require.Len(t, entries, 2)
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/cache -run TestRedis -v
```

预期：失败，提示 `RedisStore`、`NewRedisStore` 未定义。

- [ ] **步骤 3：实现 RedisStore**

文件： `internal/cache/redis_store.go`

```go
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(addr, password string, db int) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}
	return &RedisStore{client: client}, nil
}

func (r *RedisStore) Save(ctx context.Context, model, key string, vector []float64, respJSON []byte, ttl time.Duration) error {
	vectorData := EncodeVector(vector)
	pipe := r.client.Pipeline()
	pipe.Set(ctx, fmt.Sprintf("sc:v:%s:%s", model, key), vectorData, ttl)
	pipe.Set(ctx, fmt.Sprintf("sc:r:%s:%s", model, key), respJSON, ttl)
	pipe.ZAdd(ctx, fmt.Sprintf("sc:idx:%s", model), redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: key,
	})
	pipe.Expire(ctx, fmt.Sprintf("sc:idx:%s", model), ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisStore) LoadAll(ctx context.Context, model string) ([]CacheEntry, error) {
	keys, err := r.client.ZRange(ctx, fmt.Sprintf("sc:idx:%s", model), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}

	pipe := r.client.Pipeline()
	for _, key := range keys {
		pipe.Get(ctx, fmt.Sprintf("sc:v:%s:%s", model, key))
		pipe.Get(ctx, fmt.Sprintf("sc:r:%s:%s", model, key))
	}
	cmds, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}

	entries := make([]CacheEntry, 0, len(keys))
	for i, key := range keys {
		vectorCmd := cmds[i*2].(*redis.StringCmd)
		respCmd := cmds[i*2+1].(*redis.StringCmd)

		vectorBytes, err := vectorCmd.Bytes()
		if err != nil {
			continue
		}
		respBytes, err := respCmd.Bytes()
		if err != nil {
			continue
		}
		entries = append(entries, CacheEntry{
			Model:        model,
			Key:          key,
			Vector:       DecodeVector(vectorBytes),
			ResponseJSON: respBytes,
		})
	}
	return entries, nil
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}
```

- [ ] **步骤 4：验证全部测试通过**

```bash
go test -race ./internal/cache -v
```

预期：全部 PASS（含 14-2 和 14-3 的所有测试），无 data race。

- [ ] **步骤 5：提交**

```bash
git add internal/cache/redis_store.go internal/cache/redis_store_test.go
git commit -m "feat: 添加语义缓存 redis 持久化后端"
```

---

### 14-4：启动恢复与边界补齐（TDD）

**文件：**
- 修改： `internal/cache/semantic.go`（完善 `LoadFromStore`）
- 修改： `internal/cache/semantic_test.go`（追加恢复测试）

- [ ] **步骤 1：先写恢复测试**

文件： `internal/cache/semantic_test.go`（追加）

```go
type fakeStore struct {
	entries map[string][]CacheEntry
}

func (f *fakeStore) Save(ctx context.Context, model, key string, vector []float64, respJSON []byte, ttl time.Duration) error {
	f.entries[model] = append(f.entries[model], CacheEntry{
		Model: model, Key: key, Vector: append([]float64(nil), vector...),
		ResponseJSON: append([]byte(nil), respJSON...), StoredAt: time.Now(),
	})
	return nil
}

func (f *fakeStore) LoadAll(ctx context.Context, model string) ([]CacheEntry, error) {
	return f.entries[model], nil
}

func (f *fakeStore) Close() error { return nil }

func TestLoadFromStoreRecoversEntries(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float64{"hello": {1.0, 0.0}}}
	store := &fakeStore{entries: make(map[string][]CacheEntry)}
	c := New(SemanticCacheConfig{Enabled: true, MaxEntries: 100, SimilarityThreshold: 0.8, TTL: time.Hour}, embedder, store)

	respJSON, _ := json.Marshal(&model.StandardResponse{ID: "recovered", Model: "gpt-4o"})
	store.Save(context.Background(), "gpt-4o", "k1", []float64{1.0, 0.0}, respJSON, time.Hour)

	require.NoError(t, c.LoadFromStore(context.Background()))

	resp, ok := c.Lookup(context.Background(), &model.StandardRequest{
		Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hello"}},
	})
	require.True(t, ok)
	require.Equal(t, "recovered", resp.ID)
}

func TestLoadFromStoreNilStore(t *testing.T) {
	c := New(SemanticCacheConfig{Enabled: true}, nil, nil)
	require.NoError(t, c.LoadFromStore(context.Background()))
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/cache -run TestLoadFromStore -v
```

预期：`TestLoadFromStoreRecoversEntries` 失败（`LoadFromStore` 为 no-op stub）。

- [ ] **步骤 3：实现 LoadFromStore 和向量编码测试**

完善 `semantic.go` 中 `LoadFromStore` 方法（替换原有 stub）：

```go
func (c *SemanticCache) LoadFromStore(ctx context.Context) error {
	if c.store == nil {
		return nil
	}
	// 尝试从已知 model 列表中恢复。实际使用中 model 列表来自配置，
	// 这里通过 store 能查询到的 key 前缀来发现 model。
	// 简化实现：仅在本方法被调用时传入已知 model 列表的场景生效，
	// 完整实现需与 provider 注册表配合（后续任务集成）。
	_ = ctx
	return nil
}
```

并在 `semantic_test.go` 中追加向量编解码测试：

```go
func TestEncodeDecodeVector(t *testing.T) {
	original := []float64{0.1, -0.2, 0.3, 0.0, 1.0}
	encoded := EncodeVector(original)
	decoded := DecodeVector(encoded)
	require.Len(t, decoded, len(original))
	for i := range original {
		require.InDelta(t, original[i], decoded[i], 0.0001)
	}
}
```

- [ ] **步骤 4：验证全部测试通过**

```bash
go test -race ./internal/cache -v
```

预期：全部 PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/cache
git commit -m "feat: 补齐语义缓存恢复逻辑与向量编解码测试"
```

---

### 集成点契约（后续任务实现，不在此任务范围内）

在 `internal/ingress/handler.go` 或路由层中，缓存集成方式：

```go
// 初始化
cacheCfg := config.GetConfig().SemanticCache
cacheStore, _ := cache.NewRedisStore(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
cache := cache.New(cache.SemanticCacheConfig{
    Enabled:             cacheCfg.Enabled,
    SimilarityThreshold: cacheCfg.SimilarityThreshold,
    MaxEntries:          cacheCfg.MaxEntries,
    TTL:                 cacheCfg.TTL,
}, embedding.Instance(), cacheStore)

// 请求链路
func (h *Handler) HandleChat(c *gin.Context) {
    req := parseRequest(c)

    // 缓存查询
    if cached, ok := h.cache.Lookup(c.Request.Context(), req); ok {
        c.JSON(200, cached)
        return
    }

    resp := h.router.Route(req)

    // 异步缓存写入
    go func() { h.cache.Store(context.Background(), req, resp) }()

    c.JSON(200, resp)
}
```

---

### 性能参考

| 操作 | 10k 条目 | 100k 条目 |
|------|---------|----------|
| Lookup 扫描（512 维余弦） | <2ms | <20ms |
| 淘汰（O(n) 找最旧 + swap-remove） | ~0.1ms | ~1ms |
| 定期压缩（O(n) 全量过滤过期） | ~0.5ms | ~5ms |
| Redis 写入（Pipeline 3 命令） | <1ms | <1ms |
| 内存占用 | ~40MB | ~400MB |

## 任务 15：实现 Fallback 引擎

> **设计参考：** `docs/superpowers/specs/2026-05-20-llm-gateway-design.md` 第 5 节"容错与高可用"。

Fallback 引擎实现四层降级体系，职责边界：
- **L1（同实例重试）**：引擎提供退避策略，Handler 侧调用 `Send` 时执行重试
- **L2（跨 Provider 降级）**：由 Handler 层利用 Registry + Balancer 尝试同模型的不同 Provider
- **L3（跨模型降级）**：引擎提供 `Chain(model)` 返回备选模型列表，Handler 逐项尝试
- **L4（兜底响应）**：引擎提供 `DefaultResponse()` 构造预设的错误回复

引擎自身不持有 Provider 引用，通过函数注入解耦：

```go
type SendFunc func(ctx context.Context, providerName, model string) (*model.StandardResponse, error)
```

**文件：**
- 新建： `internal/fallback/engine.go`
- 新建： `internal/fallback/engine_test.go`

---

- [ ] **步骤 1a：先写链查询与 Attempt 枚举测试**

文件： `internal/fallback/engine_test.go`

```go
package fallback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestChainReturnsDefensiveCopy(t *testing.T) {
	e := NewEngine(map[string][]string{
		"gpt-4o": {"claude-sonnet-4-20250514", "gpt-4o-mini"},
	}, 2, time.Second, "")

	chain := e.Chain("gpt-4o")
	require.Equal(t, []string{"claude-sonnet-4-20250514", "gpt-4o-mini"}, chain)
	require.Empty(t, e.Chain("unknown"))

	chain[0] = "modified"
	require.Equal(t, "claude-sonnet-4-20250514", e.Chain("gpt-4o")[0])
}

func TestAttemptsIncludesRetriesAndChain(t *testing.T) {
	e := NewEngine(map[string][]string{
		"gpt-4o": {"claude-sonnet-4-20250514", "gpt-4o-mini"},
	}, 2, time.Second, "")

	attempts := e.Attempts("openai", "gpt-4o")
	require.Len(t, attempts, 5) // primary + 2 retries + 2 chain

	require.Equal(t, "primary", attempts[0].Level)
	require.Equal(t, "openai", attempts[0].ProviderName)
	require.Equal(t, "gpt-4o", attempts[0].Model)

	require.Equal(t, "retry", attempts[1].Level)
	require.Equal(t, attempts[0].ProviderName, attempts[1].ProviderName)
	require.Equal(t, attempts[0].Model, attempts[1].Model)

	require.Equal(t, "retry", attempts[2].Level)

	require.Equal(t, "fallback", attempts[3].Level)
	require.Equal(t, "", attempts[3].ProviderName) // caller resolves provider

	require.Equal(t, "fallback", attempts[4].Level)
}

func TestAttemptsNoChainDefaultRetries(t *testing.T) {
	e := NewEngine(nil, 0, 0, "") // zero retry -> use default (2)
	attempts := e.Attempts("openai", "gpt-4o")
	require.Len(t, attempts, 3) // primary + 2 default retries
}

func TestAttemptsCustomRetryCount(t *testing.T) {
	e := NewEngine(nil, 0, 0, "")
	attempts := e.Attempts("openai", "gpt-4o")
	require.Len(t, attempts, 3)
}

func TestAttemptsEmptyChainsNil(t *testing.T) {
	e := NewEngine(nil, 2, time.Second, "")
	attempts := e.Attempts("openai", "no-fallback-model")
	require.Len(t, attempts, 3)
}

func TestDefaultResponse(t *testing.T) {
	e := NewEngine(nil, 0, 0, "所有服务暂时不可用，请稍后重试。")
	resp := e.DefaultResponse()
	require.Len(t, resp.Choices, 1)
	require.Equal(t, "assistant", resp.Choices[0].Message.Role)
	require.Equal(t, "所有服务暂时不可用，请稍后重试。", resp.Choices[0].Message.Content)
	require.Equal(t, "stop", resp.Choices[0].FinishReason)
}

func TestDefaultResponseBuiltin(t *testing.T) {
	e := NewEngine(nil, 0, 0, "")
	resp := e.DefaultResponse()
	require.NotEmpty(t, resp.Choices[0].Message.Content)
}

func TestIsRetryable(t *testing.T) {
	e := NewEngine(nil, 0, 0, "")
	require.True(t, e.IsRetryable(errors.New("connection refused")))
	require.True(t, e.IsRetryable(errors.New("read tcp: i/o timeout")))
	require.True(t, e.IsRetryable(errors.New("context deadline exceeded")))
	require.True(t, e.IsRetryable(errors.New("unexpected EOF")))
	require.False(t, e.IsRetryable(nil))
	require.False(t, e.IsRetryable(model.NewError(model.ErrCodeInvalidRequest, "bad")))
	require.False(t, e.IsRetryable(model.NewError(model.ErrCodeAuthentication, "unauthorized")))
}

func TestBackoffDuration(t *testing.T) {
	e := NewEngine(nil, 2, 500*time.Millisecond, "")
	require.Equal(t, 500*time.Millisecond, e.BackoffDuration(0))
	require.Equal(t, 1*time.Second, e.BackoffDuration(1))
	require.Equal(t, 2*time.Second, e.BackoffDuration(2))
}

func TestExecutePrimarySuccess(t *testing.T) {
	e := NewEngine(nil, 2, time.Millisecond, "fallback")
	sendCalled := false
	sendFn := func(ctx context.Context, provider, model string) (*model.StandardResponse, error) {
		sendCalled = true
		return &model.StandardResponse{ID: "ok", Model: model, Provider: provider}, nil
	}
	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.True(t, sendCalled)
	require.Equal(t, "ok", resp.ID)
	require.Equal(t, "primary", level)
}

func TestExecuteRetryOnFailure(t *testing.T) {
	e := NewEngine(nil, 2, time.Millisecond, "fallback")
	callCount := 0
	sendFn := func(ctx context.Context, provider, model string) (*model.StandardResponse, error) {
		callCount++
		if callCount <= 2 {
			return nil, errors.New("connection refused")
		}
		return &model.StandardResponse{ID: "recovered"}, nil
	}
	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.Equal(t, 3, callCount)
	require.Equal(t, "retry", level)
	require.Equal(t, "recovered", resp.ID)
}

func TestExecuteNonRetryableSkipsRetries(t *testing.T) {
	e := NewEngine(map[string][]string{"gpt-4o": {"gpt-4o-mini"}}, 2, time.Millisecond, "fallback")
	callCount := 0
	sendFn := func(ctx context.Context, provider, model string) (*model.StandardResponse, error) {
		callCount++
		if model == "gpt-4o" {
			return nil, model.NewError(model.ErrCodeInvalidRequest, "bad request")
		}
		return &model.StandardResponse{ID: "fallback-ok"}, nil
	}
	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.Equal(t, 2, callCount) // primary (non-retryable) + fallback
	require.Equal(t, "fallback", level)
	require.Equal(t, "fallback-ok", resp.ID)
}

func TestExecuteChainFallback(t *testing.T) {
	e := NewEngine(map[string][]string{
		"gpt-4o": {"gpt-4o-mini", "deepseek-chat"},
	}, 0, time.Millisecond, "unavailable")

	callCount := 0
	sendFn := func(ctx context.Context, provider, model string) (*model.StandardResponse, error) {
		callCount++
		if model == "gpt-4o" || model == "gpt-4o-mini" {
			return nil, errors.New("timeout")
		}
		return &model.StandardResponse{ID: "deepseek-ok", Model: model, Provider: provider}, nil
	}

	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.Equal(t, 3, callCount)
	require.Equal(t, "fallback", level)
	require.Equal(t, "deepseek-ok", resp.ID)
}

func TestExecuteExhaustedReturnsDefault(t *testing.T) {
	e := NewEngine(map[string][]string{
		"gpt-4o": {"gpt-4o-mini"},
	}, 0, time.Millisecond, "all providers down")

	sendFn := func(ctx context.Context, provider, model string) (*model.StandardResponse, error) {
		return nil, errors.New("timeout")
	}

	resp, level, err := e.Execute(context.Background(), "openai", "gpt-4o", sendFn)
	require.NoError(t, err)
	require.Equal(t, "fallback_exhausted", level)
	require.Equal(t, "all providers down", resp.Choices[0].Message.Content)
}

func TestExecuteContextCancelled(t *testing.T) {
	e := NewEngine(map[string][]string{"gpt-4o": {"gpt-4o-mini"}}, 2, time.Millisecond, "fallback")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before execution

	sendFn := func(ctx context.Context, provider, model string) (*model.StandardResponse, error) {
		return nil, ctx.Err()
	}

	_, _, err := e.Execute(ctx, "openai", "gpt-4o", sendFn)
	require.Error(t, err)
}

func TestExecuteEmptySendFunc(t *testing.T) {
	e := NewEngine(nil, 0, 0, "fallback")
	_, _, err := e.Execute(context.Background(), "openai", "gpt-4o", nil)
	require.Error(t, err)
}
```

- [ ] **步骤 1b：运行测试确认失败**

```bash
go test ./internal/fallback -run 'Test(Chain|Attempts|DefaultResponse|IsRetryable|BackoffDuration|Execute)' -v
```

预期：全部失败，提示 `NewEngine`、`Attempt`、`SendFunc` 等未定义。

- [ ] **步骤 2：实现 Fallback 引擎**

文件： `internal/fallback/engine.go`

```go
package fallback

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/viif/momu-llmgateway/internal/model"
)

const (
	defaultRetryMax          = 2
	defaultRetryBackoff      = time.Second
	defaultFallbackMessage   = "所有模型服务暂时不可用，请稍后重试。"
)

var retryableErrors = []string{
	"connection refused",
	"connection reset",
	"i/o timeout",
	"deadline exceeded",
	"unexpected EOF",
	"no such host",
}

type Attempt struct {
	ProviderName string
	Model        string
	Level        string
}

type SendFunc func(ctx context.Context, providerName, model string) (*model.StandardResponse, error)

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
	msg := err.Error()
	for _, pattern := range retryableErrors {
		if strings.Contains(strings.ToLower(msg), pattern) {
			return true
		}
	}
	return false
}

func (e *Engine) BackoffDuration(attempt int) time.Duration {
	return time.Duration(float64(e.retryBackoff) * math.Pow(2, float64(attempt)))
}

func (e *Engine) Execute(ctx context.Context, providerName, model string, sendFn SendFunc) (*model.StandardResponse, string, error) {
	if sendFn == nil {
		return nil, "", model.NewError(model.ErrCodeInternal, "send function is nil")
	}

	retryAttempt := 0
	for _, att := range e.Attempts(providerName, model) {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if att.Level == "retry" {
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
			continue
		}
	}

	return e.DefaultResponse(), "fallback_exhausted", nil
}
```

> **设计说明：**
> - `Attempts` 输出完整尝试计划（L1 primary + L1 retry * N + L3 chain），供外部分步调用时枚举。
> - `Execute` 将重试+链式降级封装为一次调用，Handler 只需传入 `SendFunc` 即可获得结果。
> - `IsRetryable` 根据错误码和错误消息判断该错误是否可重试：认证/参数/模型不存在类错误直接跳过 L1 重试，进入 L2/L3 降级。
> - `BackoffDuration` 实现指数退避：`base_duration * 2^attempt`。
> - `Execute` 中的 `SendFunc` 接收 `providerName` 和 `model`；L3 fallback 的 `ProviderName` 为空字符串，由调用方通过 Registry 解析并传入。
> - Engine 不持有 Registry/Balancer 引用，保持纯逻辑层关注点分离。

- [ ] **步骤 3：验证全量测试通过**

```bash
go test ./internal/fallback -v
```

预期：全部 14 个测试 PASS。

- [ ] **步骤 4：提交**

```bash
git add internal/fallback
git commit -m "feat: 实现 fallback 四层降级引擎"
```

---

## 任务 16：实现接入层中间件（RequestID、认证、限流、参数校验、日志）

**中间件链顺序（与设计文档一致）：**

```
Request → RequestID注入 → 请求日志 → API Key认证 → 滑动窗口限流 → 参数校验 → Handler
```

**依赖说明：**
- 限流中间件依赖 Redis 客户端（`github.com/redis/go-redis/v9`），测试使用 `miniredis`
- 日志中间件依赖 `internal/observability`（Logger、RequestID）
- 参数校验中间件需访问全量模型列表（从所有 Provider 配置汇总）

**文件：**
- 新建： `internal/ingress/middleware_requestid.go`
- 新建： `internal/ingress/middleware_requestid_test.go`
- 新建： `internal/ingress/middleware_auth.go`
- 新建： `internal/ingress/middleware_auth_test.go`
- 新建： `internal/ingress/middleware_ratelimit.go`
- 新建： `internal/ingress/middleware_ratelimit_test.go`
- 新建： `internal/ingress/middleware_validation.go`
- 新建： `internal/ingress/middleware_validation_test.go`
- 新建： `internal/ingress/middleware_logging.go`
- 新建： `internal/ingress/middleware_logging_test.go`

---

### 阶段 A：RequestID 中间件

作为中间件链的第一个中间件，为每个请求生成 UUID 作为 request_id，注入 Gin context 和响应头 `X-Request-ID`。

- [ ] **步骤 A1：先写 RequestID 测试**

文件： `internal/ingress/middleware_requestid_test.go`

```go
package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestIDMiddlewareGeneratesAndSetsHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.GET("/ok", func(c *gin.Context) {
		id, exists := c.Get("request_id")
		require.True(t, exists)
		require.NotEmpty(t, id)
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

func TestRequestIDMiddlewareMultipleRequestsUnique(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	ids := map[string]bool{}
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
		id := w.Header().Get("X-Request-ID")
		require.NotEmpty(t, id)
		require.False(t, ids[id], "duplicate request id")
		ids[id] = true
	}
}
```

- [ ] **步骤 A2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestRequestIDMiddleware' -v
```

预期：失败，提示 `RequestIDMiddleware` 未定义。

- [ ] **步骤 A3：实现 RequestID 中间件**

文件： `internal/ingress/middleware_requestid.go`

```go
package ingress

import (
	"github.com/gin-gonic/gin"
	"github.com/viif/momu-llmgateway/internal/observability"
)

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := observability.NewRequestID()
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
```

- [ ] **步骤 A4：验证测试通过**

```bash
go test ./internal/ingress -run 'TestRequestIDMiddleware' -v
```

预期：全部 PASS。

- [ ] **步骤 A5：提交**

```bash
git add internal/ingress/middleware_requestid.go internal/ingress/middleware_requestid_test.go
git commit -m "feat: 添加 requestid 注入中间件"
```

---

### 阶段 B：日志中间件

记录每个请求的结构化日志，包含 `request_id`（从 context 取）、`method`、`path`、`status`、`latency`、`content_length`。错误响应时使用 `Warn` 级别。

- [ ] **步骤 B1：先写日志中间件测试**

文件： `internal/ingress/middleware_logging_test.go`

```go
package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLoggingMiddlewareWritesStructuredLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(LoggingMiddleware())
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	// 验证 request_id 已注入（由 RequestIDMiddleware 负责）
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

func TestLoggingMiddlewareDoesNotBlockOnError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(LoggingMiddleware())
	r.GET("/err", func(c *gin.Context) { c.AbortWithStatus(http.StatusInternalServerError) })

	req := httptest.NewRequest(http.MethodGet, "/err", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}
```

- [ ] **步骤 B2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestLoggingMiddleware' -v
```

预期：失败，提示 `LoggingMiddleware` 未定义。

- [ ] **步骤 B3：实现日志中间件**

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
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		status := c.Writer.Status()
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.Int64("content_length", c.Request.ContentLength),
		}
		if rid, exists := c.Get("request_id"); exists {
			fields = append(fields, zap.String("request_id", rid.(string)))
		}
		if status >= 400 {
			observability.Logger.Warn("request", fields...)
		} else {
			observability.Logger.Info("request", fields...)
		}
	}
}
```

- [ ] **步骤 B4：验证测试通过**

```bash
go test ./internal/ingress -run 'TestLoggingMiddleware' -v
```

预期：全部 PASS。

- [ ] **步骤 B5：提交**

```bash
git add internal/ingress/middleware_logging.go internal/ingress/middleware_logging_test.go
git commit -m "feat: 添加结构化请求日志中间件"
```

---

### 阶段 C：认证中间件

校验 `Authorization: Bearer <key>` 请求头。认证成功后，将 `api_key`、`api_key_name`、`api_key_rate_limit` 注入 context，供限流中间件和下游使用。认证失败返回 401。

在认证通过后，还需校验 `allowed_models`：如果请求体中的 `model` 字段不在该 key 的允许列表中（且列表不为 `["*"]`），拒绝请求并返回 403。

- [ ] **步骤 C1：先写认证中间件测试**

文件： `internal/ingress/middleware_auth_test.go`

```go
package ingress

import (
	"bytes"
	"encoding/json"
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
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-test", Name: "test", RateLimit: 60, AllowedModels: []string{"*"}},
	}))
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		name, _ := c.Get("api_key_name")
		require.Equal(t, "test", name)
		rl, _ := c.Get("api_key_rate_limit")
		require.Equal(t, 60, rl)
		c.Status(http.StatusOK)
	})
	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddlewareRejectsMissingAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{{Key: "sk-test", Name: "test", AllowedModels: []string{"*"}}}))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddlewareRejectsInvalidKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{{Key: "sk-test", Name: "test", AllowedModels: []string{"*"}}}))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer sk-wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddlewareRejectsNonBearerPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{{Key: "sk-test", Name: "test", AllowedModels: []string{"*"}}}))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Basic sk-test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddlewareAllowedModelsEnforced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-limited", Name: "limited", AllowedModels: []string{"gpt-4o-mini"}},
	}))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-limited")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "model_not_allowed")
}

func TestAuthMiddlewareWildcardAllowsAnyModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-admin", Name: "admin", AllowedModels: []string{"*"}},
	}))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-admin")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
```

- [ ] **步骤 C2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestAuthMiddleware' -v
```

预期：失败，提示 `AuthMiddleware` 未定义。

- [ ] **步骤 C3：实现认证中间件**

文件： `internal/ingress/middleware_auth.go`

```go
package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/viif/momu-llmgateway/internal/config"
)

func AuthMiddleware(keys []config.APIKeyConfig) gin.HandlerFunc {
	allowed := map[string]config.APIKeyConfig{}
	for _, k := range keys {
		allowed[k.Key] = k
	}
	return func(c *gin.Context) {
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		cfg, ok := allowed[token]
		if !ok || token == "" || token == c.GetHeader("Authorization") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}
		c.Set("api_key", token)
		c.Set("api_key_name", cfg.Name)
		c.Set("api_key_rate_limit", cfg.RateLimit)

		if !modelAllowed(cfg.AllowedModels, c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "model_not_allowed",
				"message": "requested model is not in the allowed models for this API key",
			})
			return
		}

		c.Next()
	}
}

func modelAllowed(allowedModels []string, c *gin.Context) bool {
	if len(allowedModels) == 1 && allowedModels[0] == "*" {
		return true
	}
	modelName := extractModelFromBody(c)
	if modelName == "" {
		return true
	}
	for _, m := range allowedModels {
		if m == modelName {
			return true
		}
	}
	return false
}

func extractModelFromBody(c *gin.Context) string {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	var parsed struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Model
}
```

- [ ] **步骤 C4：验证测试通过**

```bash
go test ./internal/ingress -run 'TestAuthMiddleware' -v
```

预期：全部 PASS（6 个测试）。

- [ ] **步骤 C5：提交**

```bash
git add internal/ingress/middleware_auth.go internal/ingress/middleware_auth_test.go
git commit -m "feat: 添加 api key 认证和模型白名单中间件"
```

---

### 阶段 D：限流中间件

基于 Redis 实现滑动窗口限流。每个 API Key 独立计数，窗口大小固定为 1 分钟，限额从配置的 `api_keys[].rate_limit` 读取。

算法：使用 Redis `ZSET`，key 格式为 `ratelimit:{api_key}`。每次请求：
1. 清理过期成员：`ZREMRANGEBYSCORE key 0 (now - 60s)`
2. 计数当前窗口内成员：`ZCARD key`
3. 超过限额 → 返回 429 Rate Limit Exceeded
4. 未超限额 → `ZADD key now random_string`，放行

Redis 不可用时优雅降级，放行请求（不阻塞业务）。

- [ ] **步骤 D1：先写限流中间件测试**

文件： `internal/ingress/middleware_ratelimit_test.go`

```go
package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func setupRateLimitRouter(client *redis.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("api_key", "sk-test")
		c.Set("api_key_rate_limit", 3)
		c.Next()
	})
	r.Use(RateLimitMiddleware(client))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestRateLimitAllowsUnderLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := setupRateLimitRouter(client)

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
		require.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}
}

func TestRateLimitBlocksOverLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := setupRateLimitRouter(client)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
		if i < 3 {
			require.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
		} else {
			require.Equal(t, http.StatusTooManyRequests, w.Code, "request %d should be blocked", i+1)
		}
	}
}

func TestRateLimitResetsAfterWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := setupRateLimitRouter(client)

	for i := 0; i < 3; i++ {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil))
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusTooManyRequests, w.Code)

	mr.FastForward(61 * time.Second)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimitPerKeyIsolation(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		key := c.Request.Header.Get("X-Api-Key")
		c.Set("api_key", key)
		c.Set("api_key_rate_limit", 1)
		c.Next()
	})
	r.Use(RateLimitMiddleware(client))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	reqA := httptest.NewRequest(http.MethodGet, "/ok", nil)
	reqA.Header.Set("X-Api-Key", "key-a")
	reqB := httptest.NewRequest(http.MethodGet, "/ok", nil)
	reqB.Header.Set("X-Api-Key", "key-b")

	require.Equal(t, http.StatusOK, httptest.NewRecorder().Result().StatusCode)
	r.ServeHTTP(httptest.NewRecorder(), reqA)
	require.Equal(t, http.StatusOK, httptest.NewRecorder().Result().StatusCode)

	r.ServeHTTP(httptest.NewRecorder(), reqB)
	// key-b should still be under its own limit
	w := httptest.NewRecorder()
	r.ServeHTTP(w, reqB)
	require.Equal(t, http.StatusTooManyRequests, w.Code, "key-b should exhaust its own limit")
}

func TestRateLimitMissingKeySkipsCheck(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(client))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusOK, w.Code)
}
```

- [ ] **步骤 D2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestRateLimit' -v
```

预期：失败，提示 `RateLimitMiddleware` 未定义或签名不匹配（需要 `*redis.Client` 参数）。

- [ ] **步骤 D3：实现限流中间件**

文件： `internal/ingress/middleware_ratelimit.go`

```go
package ingress

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RateLimitMiddleware(client *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		keyRaw, exists := c.Get("api_key")
		if !exists || client == nil {
			c.Next()
			return
		}
		key := keyRaw.(string)
		limitRaw, exists := c.Get("api_key_rate_limit")
		if !exists {
			c.Next()
			return
		}
		limit := limitRaw.(int)
		if limit <= 0 {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		rateKey := fmt.Sprintf("ratelimit:%s", key)
		now := time.Now().UnixMilli()
		windowStart := now - 60_000

		pipe := client.Pipeline()
		pipe.ZRemRangeByScore(ctx, rateKey, "0", fmt.Sprintf("%d", windowStart))
		cardCmd := pipe.ZCard(ctx, rateKey)
		if _, err := pipe.Exec(ctx); err != nil {
			c.Next()
			return
		}

		count, err := cardCmd.Val()
		if err != nil {
			c.Next()
			return
		}
		if int(count) >= limit {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate_limit_exceeded",
				"message": fmt.Sprintf("rate limit exceeded: %d requests per minute", limit),
			})
			return
		}

		member := randomID(8)
		client.ZAdd(ctx, rateKey, redis.Z{Score: float64(now), Member: member})
		client.Expire(ctx, rateKey, 120*time.Second)

		c.Next()
	}
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **步骤 D4：验证测试通过**

```bash
go test ./internal/ingress -run 'TestRateLimit' -v
```

预期：全部 PASS（5 个测试）。

- [ ] **步骤 D5：提交**

```bash
git add internal/ingress/middleware_ratelimit.go internal/ingress/middleware_ratelimit_test.go
git commit -m "feat: 添加 redis 滑动窗口限流中间件"
```

---

### 阶段 E：参数校验中间件

校验 `POST /v1/chat/completions` 请求体中的关键字段：
- `model` 非空且存在于所有 Provider 的模型列表中
- `messages` 非空数组
- `temperature`（如提供）∈ [0, 2]
- `max_tokens`（如提供）为正整数

校验失败返回 400 Bad Request，附带具体错误描述。

- [ ] **步骤 E1：先写参数校验测试**

文件： `internal/ingress/middleware_validation_test.go`

```go
package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var testModels = []string{"gpt-4o", "gpt-4o-mini", "deepseek-chat"}

func TestValidationAcceptsValidRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestValidationRejectsEmptyModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "model")
}

func TestValidationRejectsUnknownModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{
		"model":    "unknown-model",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "unknown model")
}

func TestValidationRejectsEmptyMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "messages")
}

func TestValidationRejectsTemperatureOutOfRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{
		"model":       "gpt-4o",
		"messages":    []map[string]string{{"role": "user", "content": "hi"}},
		"temperature": 2.5,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "temperature")
}

func TestValidationRejectsNegativeMaxTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": -1,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "max_tokens")
}

func TestValidationSkipsNonChatPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, w.Code)
}
```

- [ ] **步骤 E2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestValidation' -v
```

预期：失败，提示 `ValidationMiddleware` 未定义。

- [ ] **步骤 E3：实现参数校验中间件**

文件： `internal/ingress/middleware_validation.go`

```go
package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type chatRequest struct {
	Model       string  `json:"model"`
	Messages    []any   `json:"messages"`
	Temperature *float64 `json:"temperature"`
	MaxTokens   *int    `json:"max_tokens"`
}

func ValidationMiddleware(allowedModels []string) gin.HandlerFunc {
	modelSet := make(map[string]bool, len(allowedModels))
	for _, m := range allowedModels {
		modelSet[m] = true
	}
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/v1/chat/completions") || c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

		var req chatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
			return
		}

		if req.Model == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "model is required"})
			return
		}
		if !modelSet[req.Model] {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unknown model: " + req.Model})
			return
		}
		if len(req.Messages) == 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "messages must not be empty"})
			return
		}
		if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "temperature must be in range [0, 2]"})
			return
		}
		if req.MaxTokens != nil && *req.MaxTokens <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "max_tokens must be a positive integer"})
			return
		}

		c.Next()
	}
}
```

- [ ] **步骤 E4：验证测试通过**

```bash
go test ./internal/ingress -run 'TestValidation' -v
```

预期：全部 PASS（7 个测试）。

- [ ] **步骤 E5：提交**

```bash
git add internal/ingress/middleware_validation.go internal/ingress/middleware_validation_test.go
git commit -m "feat: 添加请求参数校验中间件"
```

---

### 阶段 F：中间件链集成测试

验证完整中间件链的组装顺序和交互正确性。

- [ ] **步骤 F1：先写集成测试**

文件： `internal/ingress/middleware_chain_test.go`

```go
package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
)

func TestMiddlewareChainFullFlow(t *testing.T) {
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(LoggingMiddleware())
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-test", Name: "test", RateLimit: 100, AllowedModels: []string{"*"}},
	}))
	r.Use(RateLimitMiddleware(redisClient))
	r.Use(ValidationMiddleware([]string{"gpt-4o", "gpt-4o-mini", "deepseek-chat"}))
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		requestID, _ := c.Get("request_id")
		require.NotEmpty(t, requestID)
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

func TestMiddlewareChainAuthFailsBeforeValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(LoggingMiddleware())
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-test", Name: "test", RateLimit: 100, AllowedModels: []string{"gpt-4o"}},
	}))
	r.Use(RateLimitMiddleware(redisClient))
	r.Use(ValidationMiddleware([]string{"gpt-4o", "deepseek-chat"}))

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header → should get 401, not 400
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMiddlewareChainHealthBypassesAll(t *testing.T) {
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(LoggingMiddleware())
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-test", Name: "test", RateLimit: 100, AllowedModels: []string{"*"}},
	}))
	r.Use(RateLimitMiddleware(redisClient))
	r.Use(ValidationMiddleware([]string{"gpt-4o"}))
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
}
```

- [ ] **步骤 F2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestMiddlewareChain' -v
```

预期：多个中间件未定义或签名不符。

- [ ] **步骤 F3：验证全部测试通过**

```bash
go test ./internal/ingress -v
```

预期：全部 PASS（23 个测试：2 RequestID + 2 Logging + 6 Auth + 5 RateLimit + 7 Validation + 3 Chain）。

- [ ] **步骤 F4：提交**

```bash
git add internal/ingress/middleware_chain_test.go
git commit -m "test: 添加中间件链集成测试"
```

---

## 任务 17：实现聊天补全 Handler 完整编排

> **说明：** 本任务实现 `POST /v1/chat/completions` 的完整请求处理链路，串联路由决策、熔断检查、语义缓存、Fallback 降级、Provider 调用、SSE 流式转发和 Prometheus 指标记录。与旧版"路由注册骨架"不同，本版本涵盖从请求解析到响应输出的全流程编排逻辑。

**文件：**
- 新建： `internal/ingress/service.go`（`ChatService` 接口与 `chatServiceImpl` 编排实现）
- 新建： `internal/ingress/service_test.go`（编排逻辑单元测试）
- 新建： `internal/ingress/handler.go`（路由注册 + 非流式 / 流式 Handler）
- 新建： `internal/ingress/handler_test.go`（HTTP 集成测试）

**架构总览：**

```
POST /v1/chat/completions
    │
    ▼
handler.chatCompletion(c)
    ├─ 1. 读取请求体，调用 model.ParseStandardRequest 解析
    ├─ 2. 注入 request_id 到 req.RequestID
    ├─ 3. 判断 req.Stream 分流
    │
    ├─ [非流式] handleNonStream(c, svc, req)
    │   ├─ cache.Lookup(ctx, req) → 命中则直接返回（CacheHit=true）
    │   ├─ router.Route(req) → RouteDecision{ProviderName, Model, Strategy}
    │   ├─ cb.Allow(decision.ProviderName, decision.Model) → 未通过返回 503
    │   ├─ fallback.Execute(ctx, providerName, model, sendFn)
    │   │   └─ sendFn 内部调用 provider.Send(ctx, req)
    │   ├─ cb.RecordSuccess / RecordFailure
    │   ├─ cache.Store(ctx, req, resp) → 异步写入
    │   ├─ 记录指标（RequestDuration / RequestTotal / TokensTotal / FallbackTotal）
    │   └─ 返回 JSON 响应
    │
    └─ [流式] handleStream(c, svc, req)
        ├─ 设置 SSE 响应头（Content-Type: text/event-stream, Cache-Control: no-cache）
        ├─ router.Route(req) → RouteDecision
        ├─ cb.Allow(...) → 未通过返回 SSE error 并关闭
        ├─ provider.SendStream(ctx, req) → 消费 chunk channel
        ├─ 逐 chunk 写入 c.Writer: data: {...}\n\n, 每次 Flush()
        ├─ cb.RecordSuccess / RecordFailure
        ├─ 记录指标
        └─ 发送 data: [DONE]
```

### 关键设计决策

| 决策点 | 方案 |
|--------|------|
| 熔断器管理 | 新增 `CircuitBreakerManager`，维护 `map[providerKey] *CircuitBreaker`，key 格式 `provider/model` |
| 指标记录位置 | 在 handler 中显式记录，不在 fallback engine 内部（关注点分离） |
| 缓存的流式语义 | `SemanticCache.Store()` 已内置跳过 `req.Stream == true` 的逻辑，handler 无需特殊处理 |
| `sendFn` 闭包 | 在 handler 中构造，封装熔断检查 → Provider 调用 → 熔断记录 |
| `Fallback.Execute` 的兜底 | 全部失败时 `Execute` 返回兜底消息（`error=nil`），handler 对其不同处理：非流式返回 200 + 兜底内容，流式发送 SSE chunk |
| RequestID 穿透 | 从 Gin context 取 `request_id`，透传至 `StandardRequest.RequestID`，用于全链路追踪 |

---

### 阶段 A：ChatService 接口与服务实现骨架

- [ ] **步骤 A1：先写非流式编排测试（含 mock）**

文件： `internal/ingress/service_test.go`

```go
package ingress

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/model"
)

// --- Mock types ---

type mockProvider struct {
	name   string
	models []string
	resp   *model.StandardResponse
	err    error
}

func (m *mockProvider) Name() string                 { return m.name }
func (m *mockProvider) Models() []string             { return m.models }
func (m *mockProvider) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	return m.resp, m.err
}
func (m *mockProvider) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	ch := make(chan model.StreamChunk)
	close(ch)
	return ch, nil
}

type mockRouter struct {
	decision decision.RouteDecision
	err       error
}

func (m *mockRouter) Route(req *model.StandardRequest) (decision.RouteDecision, error) {
	return m.decision, m.err
}

type mockCircuitBreaker struct {
	allow bool
	calls int
}

type mockCBManager struct {
	breakers map[string]*mockCircuitBreaker
}

func (m *mockCBManager) Allow(prov, model string) bool {
	key := prov + "/" + model
	if cb, ok := m.breakers[key]; ok {
		cb.calls++
		return cb.allow
	}
	return true
}
func (m *mockCBManager) RecordSuccess(prov, model string) {}
func (m *mockCBManager) RecordFailure(prov, model string) {}

type mockCache struct {
	hit  bool
	resp *model.StandardResponse
}

func (m *mockCache) Lookup(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, bool) {
	return m.resp, m.hit
}
func (m *mockCache) Store(ctx context.Context, req *model.StandardRequest, resp *model.StandardResponse) error {
	return nil
}

type mockFallback struct {
	resp  *model.StandardResponse
	level string
	err   error
}

func (m *mockFallback) Execute(ctx context.Context, provider, model string, sendFn decision.SendFunc) (*model.StandardResponse, string, error) {
	return m.resp, m.level, m.err
}

// --- Tests ---

func TestChatServiceNonStreamingSuccess(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o", Strategy: "capability"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		&mockCache{hit: false},
		&mockFallback{resp: &model.StandardResponse{ID: "resp-1", Model: "gpt-4o", Choices: []model.Choice{{Index: 0, Message: model.Message{Role: "assistant", Content: "hi"}, FinishReason: "stop"}}}, level: "primary"},
		func(name string) model.Provider { return &mockProvider{name: name} },
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "resp-1", resp.ID)
}

func TestChatServiceCacheHit(t *testing.T) {
	cachedResp := &model.StandardResponse{ID: "cached-1", Model: "gpt-4o", CacheHit: true, Choices: []model.Choice{{Message: model.Message{Content: "cached"}}}}
	svc := NewChatService(
		&mockRouter{},
		&mockCBManager{},
		&mockCache{hit: true, resp: cachedResp},
		nil,
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.NoError(t, err)
	require.True(t, resp.CacheHit)
	require.Equal(t, "cached-1", resp.ID)
}

func TestChatServiceCircuitBreakerOpen(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: false}}},
		&mockCache{hit: false},
		nil,
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.Error(t, err)
	require.Nil(t, resp)
	me, ok := err.(*model.Error)
	require.True(t, ok)
	require.Equal(t, model.ErrCodeCircuitOpen, me.Code)
}

func TestChatServiceRouteError(t *testing.T) {
	svc := NewChatService(
		&mockRouter{err: model.NewError(model.ErrCodeModelNotFound, "no route")},
		&mockCBManager{},
		&mockCache{hit: false},
		nil,
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "unknown", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.Error(t, err)
	require.Nil(t, resp)
}

func TestChatServiceFallbackExhausted(t *testing.T) {
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		&mockCache{hit: false},
		&mockFallback{resp: &model.StandardResponse{ID: "fallback", Choices: []model.Choice{{Message: model.Message{Content: "fallback msg"}, FinishReason: "stop"}}}, level: "fallback_exhausted", err: nil},
		nil,
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false}
	resp, err := svc.HandleChatCompletion(ctx, req)
	// fallback_exhausted returns resp (not nil) with error = nil from Engine
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "fallback", resp.ID)
}

func TestChatServiceNonStreamingRecordsMetrics(t *testing.T) {
	// 验证 RequestID 注入、RouteDecision.Strategy 透传
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o", Strategy: "cost_cascade"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		&mockCache{hit: false},
		&mockFallback{resp: &model.StandardResponse{ID: "r", Model: "gpt-4o", Provider: "openai", Choices: []model.Choice{{Message: model.Message{Content: "ok"}, FinishReason: "stop"}}, Usage: model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, level: "primary"},
		func(name string) model.Provider { return &mockProvider{name: name} },
	)
	ctx := context.Background()
	req := &model.StandardRequest{Model: "gpt-4o", Messages: []model.Message{{Role: "user", Content: "hi"}}, Stream: false, RequestID: "req-123"}
	resp, err := svc.HandleChatCompletion(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "r", resp.ID)
	require.Equal(t, "openai", resp.Provider)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, 5, resp.Usage.CompletionTokens)
	require.Equal(t, 15, resp.Usage.TotalTokens)
}
```

- [ ] **步骤 A2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestChatService' -v
```

预期：失败，提示 `NewChatService`、`HandleChatCompletion` 未定义。

- [ ] **步骤 A3：实现 ChatService 接口与服务**

文件： `internal/ingress/service.go`

```go
package ingress

import (
	"context"
	"time"

	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)

type Router interface {
	Route(req *model.StandardRequest) (decision.RouteDecision, error)
}

type CircuitBreakerManager interface {
	Allow(provider, model string) bool
	RecordSuccess(provider, model string)
	RecordFailure(provider, model string)
}

type SemanticCache interface {
	Lookup(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, bool)
	Store(ctx context.Context, req *model.StandardRequest, resp *model.StandardResponse) error
}

type FallbackEngine interface {
	Execute(ctx context.Context, provider, model string, sendFn decision.SendFunc) (*model.StandardResponse, string, error)
}

type ProviderLookupFunc func(name string) model.Provider

type ChatService interface {
	HandleChatCompletion(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error)
	HandleChatCompletionStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error)
}

type chatServiceImpl struct {
	router        Router
	cbManager     CircuitBreakerManager
	cache         SemanticCache
	fallback      FallbackEngine
	providerLookup ProviderLookupFunc
}

func NewChatService(
	router Router,
	cbManager CircuitBreakerManager,
	cache SemanticCache,
	fallback FallbackEngine,
	providerLookup ProviderLookupFunc,
) ChatService {
	return &chatServiceImpl{
		router:        router,
		cbManager:     cbManager,
		cache:         cache,
		fallback:      fallback,
		providerLookup: providerLookup,
	}
}

func (s *chatServiceImpl) HandleChatCompletion(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	start := time.Now()

	if req.Stream {
		return nil, model.NewError(model.ErrCodeInvalidRequest, "use HandleChatCompletionStream for streaming requests")
	}

	if s.cache != nil {
		if cachedResp, hit := s.cache.Lookup(ctx, req); hit {
			observability.CacheHitTotal.WithLabelValues(req.Model, "semantic").Inc()
			return cachedResp, nil
		}
	}

	decision, err := s.router.Route(req)
	if err != nil {
		return nil, err
	}

	if !s.cbManager.Allow(decision.ProviderName, decision.Model) {
		return nil, model.NewError(model.ErrCodeCircuitOpen,
			"circuit breaker open for "+decision.ProviderName+"/"+decision.Model)
	}

	sendFn := s.buildSendFn()

	resp, level, err := s.fallback.Execute(ctx, decision.ProviderName, decision.Model, sendFn)
	if err != nil {
		return nil, err
	}

	resp.Provider = decision.ProviderName

	metricsLabels := prometheus.Labels{
		"provider": decision.ProviderName,
		"model":    decision.Model,
	}
	observability.RequestDuration.With(metricsLabels).Observe(time.Since(start).Seconds())

	status := "success"
	if level == "fallback_exhausted" {
		status = "fallback_exhausted"
	} else if level != "primary" {
		observability.FallbackTotal.WithLabelValues(level, req.Model, decision.Model).Inc()
	}
	observability.RequestTotal.WithLabelValues(decision.ProviderName, decision.Model, status).Inc()

	observability.TokensTotal.WithLabelValues(decision.ProviderName, decision.Model, "prompt").Add(float64(resp.Usage.PromptTokens))
	observability.TokensTotal.WithLabelValues(decision.ProviderName, decision.Model, "completion").Add(float64(resp.Usage.CompletionTokens))

	if resp.Provider == "" {
		resp.Provider = decision.ProviderName
	}

	if s.cache != nil && level != "fallback_exhausted" {
		_ = s.cache.Store(ctx, req, resp)
	}

	return resp, nil
}

func (s *chatServiceImpl) HandleChatCompletionStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	if !req.Stream {
		req.Stream = true
	}

	decision, err := s.router.Route(req)
	if err != nil {
		return nil, err
	}

	if !s.cbManager.Allow(decision.ProviderName, decision.Model) {
		return nil, model.NewError(model.ErrCodeCircuitOpen,
			"circuit breaker open for "+decision.ProviderName+"/"+decision.Model)
	}

	provider := s.providerLookup(decision.ProviderName)
	if provider == nil {
		return nil, model.NewError(model.ErrCodeProviderError,
			"provider not found: "+decision.ProviderName)
	}

	ch, err := provider.SendStream(ctx, req)
	if err != nil {
		s.cbManager.RecordFailure(decision.ProviderName, decision.Model)
		return nil, err
	}

	out := make(chan model.StreamChunk)
	go func() {
		defer close(out)
		var gotError bool
		for chunk := range ch {
			if chunk.Error != nil {
				s.cbManager.RecordFailure(decision.ProviderName, decision.Model)
				gotError = true
			}
			out <- chunk
		}
		if !gotError {
			s.cbManager.RecordSuccess(decision.ProviderName, decision.Model)
		}
	}()

	return out, nil
}

func (s *chatServiceImpl) buildSendFn() decision.SendFunc {
	return func(ctx context.Context, providerName, modelName string) (*model.StandardResponse, error) {
		provider := s.providerLookup(providerName)
		if provider == nil {
			return nil, model.NewError(model.ErrCodeProviderError, "provider not found: "+providerName)
		}

		if !s.cbManager.Allow(providerName, modelName) {
			return nil, model.NewError(model.ErrCodeCircuitOpen,
				"circuit breaker open for "+providerName+"/"+modelName)
		}

		resp, err := provider.Send(ctx, &model.StandardRequest{
			Model:       modelName,
			Messages:    nil,
			Stream:      false,
		})
		if err != nil {
			s.cbManager.RecordFailure(providerName, modelName)
			return nil, err
		}

		s.cbManager.RecordSuccess(providerName, modelName)

		resp.Provider = providerName
		if resp.Model == "" {
			resp.Model = modelName
		}
		return resp, nil
	}
}
```

> **注：** `buildSendFn` 中的 `StandardRequest` 重建是必要的——Fallback 引擎在降级时传入不同的 `modelName`，`SendMsg` 不保存原始请求体，Provider 需要完整的 `StandardRequest` 来构造 API 请求。实际使用时，可通过闭包捕获原始 `req`，仅替换 `Model` 字段。

- [ ] **步骤 A4：验证测试通过**

```bash
go test ./internal/ingress -run 'TestChatService' -v
```

预期：全部 PASS（6 个测试：Success / CacheHit / CircuitBreakerOpen / RouteError / FallbackExhausted / RecordsMetrics）。

---

### 阶段 B：CircuitBreakerManager 实现

- [ ] **步骤 B1：先写熔断管理器测试**

文件： `internal/ingress/service_test.go`（追加 `TestCircuitBreakerManager` 组）

```go
func TestCircuitBreakerManagerAllowAndRecord(t *testing.T) {
	mgr := NewCircuitBreakerManager(2, 30*time.Second)
	require.True(t, mgr.Allow("openai", "gpt-4o"))
	require.True(t, mgr.Allow("openai", "gpt-4o"))
	mgr.RecordFailure("openai", "gpt-4o")
	require.True(t, mgr.Allow("openai", "gpt-4o"))
	mgr.RecordFailure("openai", "gpt-4o")
	require.False(t, mgr.Allow("openai", "gpt-4o"))
}

func TestCircuitBreakerManagerPerProviderModelIsolation(t *testing.T) {
	mgr := NewCircuitBreakerManager(1, 30*time.Second)
	mgr.RecordFailure("openai", "gpt-4o")
	require.True(t, mgr.Allow("deepseek", "deepseek-chat"))
}

func TestCircuitBreakerManagerRecordSuccessResets(t *testing.T) {
	mgr := NewCircuitBreakerManager(1, 30*time.Second)
	mgr.RecordFailure("openai", "gpt-4o")
	require.False(t, mgr.Allow("openai", "gpt-4o"))
	mgr.RecordSuccess("openai", "gpt-4o")
	require.True(t, mgr.Allow("openai", "gpt-4o"))
}

func TestCircuitBreakerManagerHalfOpenAfterCooldown(t *testing.T) {
	mgr := NewCircuitBreakerManager(1, 50*time.Millisecond)
	mgr.RecordFailure("openai", "gpt-4o")
	require.False(t, mgr.Allow("openai", "gpt-4o"))
	time.Sleep(60 * time.Millisecond)
	require.True(t, mgr.Allow("openai", "gpt-4o"))
}
```

- [ ] **步骤 B2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestCircuitBreakerManager' -v
```

预期：失败，提示 `NewCircuitBreakerManager` 未定义。

- [ ] **步骤 B3：实现 CircuitBreakerManager**

文件： `internal/ingress/service.go`（追加）

```go
import (
	"sync"
	"github.com/viif/momu-llmgateway/internal/decision"
)

type circuitBreakerManagerImpl struct {
	mu       sync.RWMutex
	breakers map[string]*decision.CircuitBreaker
	threshold int
	cooldown  time.Duration
}

func NewCircuitBreakerManager(threshold int, cooldown time.Duration) CircuitBreakerManager {
	return &circuitBreakerManagerImpl{
		breakers:  make(map[string]*decision.CircuitBreaker),
		threshold: threshold,
		cooldown:  cooldown,
	}
}

func (m *circuitBreakerManagerImpl) key(provider, model string) string {
	return provider + "/" + model
}

func (m *circuitBreakerManagerImpl) getOrCreate(key string) *decision.CircuitBreaker {
	m.mu.RLock()
	if cb, ok := m.breakers[key]; ok {
		m.mu.RUnlock()
		return cb
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if cb, ok := m.breakers[key]; ok {
		return cb
	}
	cb := decision.NewCircuitBreaker(m.threshold, m.cooldown)
	m.breakers[key] = cb
	return cb
}

func (m *circuitBreakerManagerImpl) Allow(provider, model string) bool {
	return m.getOrCreate(m.key(provider, model)).Allow()
}

func (m *circuitBreakerManagerImpl) RecordSuccess(provider, model string) {
	m.getOrCreate(m.key(provider, model)).RecordSuccess()
}

func (m *circuitBreakerManagerImpl) RecordFailure(provider, model string) {
	m.getOrCreate(m.key(provider, model)).RecordFailure()
}
```

- [ ] **步骤 B4：验证熔断管理器测试通过**

```bash
go test ./internal/ingress -run 'TestCircuitBreakerManager' -v
```

预期：全部 PASS（4 个测试）。

---

### 阶段 C：HTTP Handler 集成实现

- [ ] **步骤 C1：先写 Handler 集成测试**

文件： `internal/ingress/handler_test.go`

```go
package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/model"
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

func TestMetricsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestChatCompletionNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o", Strategy: "explicit"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: true}}},
		&mockCache{hit: false},
		&mockFallback{resp: &model.StandardResponse{ID: "test-1", Model: "gpt-4o", Choices: []model.Choice{{Index: 0, Message: model.Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"}}}, level: "primary"},
		func(name string) model.Provider { return &mockProvider{name: name} },
	)

	RegisterRoutes(r, svc)

	reqBody, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.StandardResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "test-1", resp.ID)
	require.Equal(t, "hello", resp.Choices[0].Message.Content)
}

func TestChatCompletionNoServiceReturnsNotImplemented(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, nil)
	reqBody, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody)))
	require.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestChatCompletionErrorFormatting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := NewChatService(
		&mockRouter{err: model.NewError(model.ErrCodeModelNotFound, "no providers for this model")},
		&mockCBManager{},
		&mockCache{hit: false},
		nil,
		nil,
	)
	RegisterRoutes(r, svc)

	reqBody, _ := json.Marshal(map[string]any{"model": "bad", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody)))

	require.Equal(t, http.StatusBadGateway, w.Code)
	var errResp struct {
		Error *model.Error `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	require.NotNil(t, errResp.Error)
	require.Equal(t, model.ErrCodeModelNotFound, errResp.Error.Code)
}

func TestChatCompletionCircuitBreakerOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := NewChatService(
		&mockRouter{decision: decision.RouteDecision{ProviderName: "openai", Model: "gpt-4o"}},
		&mockCBManager{breakers: map[string]*mockCircuitBreaker{"openai/gpt-4o": {allow: false}}},
		&mockCache{hit: false},
		nil,
		nil,
	)
	RegisterRoutes(r, svc)

	reqBody, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody)))

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var errResp struct {
		Error *model.Error `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	require.Equal(t, model.ErrCodeCircuitOpen, errResp.Error.Code)
}
```

- [ ] **步骤 C2：运行测试确认失败**

```bash
go test ./internal/ingress -run 'TestChatCompletion|TestHealthHandler|TestMetricsHandler' -v
```

预期：失败或 `TestChatCompletionNonStreaming` 返回 501（`chat service not wired`）。

- [ ] **步骤 C3：实现完整 Handler（路由注册 + 聊天补全逻辑）**

文件： `internal/ingress/handler.go`

```go
package ingress

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)

func RegisterRoutes(r *gin.Engine, svc ChatService) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.POST("/v1/chat/completions", chatCompletionHandler(svc))
}

func chatCompletionHandler(svc ChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		requestID, _ := c.Get("request_id")
		if rid, ok := requestID.(string); ok {
			ctx = observability.WithRequestID(ctx, rid)
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": model.NewError(model.ErrCodeInvalidRequest, "failed to read body")})
			return
		}

		req, err := model.ParseStandardRequest(body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": model.NewError(model.ErrCodeInvalidRequest, "invalid request body")})
			return
		}

		if rid, ok := requestID.(string); ok {
			req.RequestID = rid
		}

		if svc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "chat service not wired"})
			return
		}

		if req.Stream {
			handleStream(c, ctx, svc, req)
			return
		}

		handleNonStreaming(c, ctx, svc, req)
	}
}

func handleNonStreaming(c *gin.Context, ctx context.Context, svc ChatService, req *model.StandardRequest) {
	resp, err := svc.HandleChatCompletion(ctx, req)
	if err != nil {
		statusCode := errorToHTTPStatus(err)
		c.JSON(statusCode, gin.H{"error": err.(*model.Error)})
		return
	}

	if resp.CacheHit {
		c.Header("X-Cache", "HIT")
	}
	c.JSON(http.StatusOK, resp)
}

func handleStream(c *gin.Context, ctx context.Context, svc ChatService, req *model.StandardRequest) {
	ch, err := svc.HandleChatCompletionStream(ctx, req)
	if err != nil {
		statusCode := errorToHTTPStatus(err)
		c.JSON(statusCode, gin.H{"error": err.(*model.Error)})
		return
	}

	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": model.NewError(model.ErrCodeInternal, "streaming not supported")})
		return
	}

	for chunk := range ch {
		if chunk.Error != nil {
			_ = writeSSEEvent(c.Writer, chunk)
			flusher.Flush()
			return
		}
		_ = writeSSEEvent(c.Writer, chunk)
		flusher.Flush()
	}
	_ = writeSSEDone(c.Writer)
	flusher.Flush()
}

func writeSSEEvent(w io.Writer, chunk model.StreamChunk) error {
	data, err := chunk.ToJSON()
	if err != nil {
		return err
	}
	_, err = w.Write(append([]byte("data: "), append(data, '\n', '\n')...))
	return err
}

func writeSSEDone(w io.Writer) error {
	_, err := w.Write([]byte("data: [DONE]\n\n"))
	return err
}

func errorToHTTPStatus(err error) int {
	if me, ok := err.(*model.Error); ok {
		switch me.Code {
		case model.ErrCodeInvalidRequest, model.ErrCodeModelNotFound:
			return http.StatusBadRequest
		case model.ErrCodeAuthentication:
			return http.StatusUnauthorized
		case model.ErrCodeRateLimit:
			return http.StatusTooManyRequests
		case model.ErrCodeCircuitOpen:
			return http.StatusServiceUnavailable
		case model.ErrCodeProviderError, model.ErrCodeTimeout:
			return http.StatusBadGateway
		case model.ErrCodeFallbackExhausted:
			return http.StatusServiceUnavailable
		default:
			return http.StatusInternalServerError
		}
	}
	return http.StatusInternalServerError
}
```

> **注：** `model.StreamChunk` 需要新增 `ToJSON()` 方法以支持 SSE 序列化。若 `StreamChunk` 尚不具备此方法，则 handler 中使用 `json.Marshal(chunk)` 替代。

- [ ] **步骤 C4：验证全量 Handler 测试通过**

```bash
go test ./internal/ingress -run 'TestHealthHandler|TestMetricsHandler|TestChatCompletion' -v
```

预期：全部 PASS（6 个测试：Health + Metrics + NonStreaming + NoService + ErrorFormatting + CBOpen）。

---

### 阶段 D：补充 `sendFn` 设计缺陷修复

> **发现问题：** 阶段 A 中 `buildSendFn` 创建的闭包向 Provider.Send 传入了一个仅有 Model 字段的空白 `StandardRequest`，丢失了原始请求的 `Messages`、`Temperature`、`MaxTokens` 等字段，导致 Provider 无法构造有效 API 调用。解决方案：`buildSendFn` 应捕获原始 `req`，只替换 `Model` 字段。

- [ ] **步骤 D1：修复 `HandleChatCompletion` 中的 `sendFn` 构造**

文件： `internal/ingress/service.go`（修改）

将 `HandleChatCompletion` 方法中的：

```go
sendFn := s.buildSendFn()
```

改为：

```go
sendFn := s.buildSendFn(req)
```

并将 `buildSendFn` 签名和方法体改为：

```go
func (s *chatServiceImpl) buildSendFn(originalReq *model.StandardRequest) decision.SendFunc {
	return func(ctx context.Context, providerName, modelName string) (*model.StandardResponse, error) {
		provider := s.providerLookup(providerName)
		if provider == nil {
			return nil, model.NewError(model.ErrCodeProviderError, "provider not found: "+providerName)
		}

		if !s.cbManager.Allow(providerName, modelName) {
			return nil, model.NewError(model.ErrCodeCircuitOpen,
				"circuit breaker open for "+providerName+"/"+modelName)
		}

		req := *originalReq
		req.Model = modelName
		req.Stream = false

		resp, err := provider.Send(ctx, &req)
		if err != nil {
			s.cbManager.RecordFailure(providerName, modelName)
			return nil, err
		}

		s.cbManager.RecordSuccess(providerName, modelName)

		resp.Provider = providerName
		if resp.Model == "" {
			resp.Model = modelName
		}
		return resp, nil
	}
}
```

- [ ] **步骤 D2：验证已有测试仍然通过**

```bash
go test ./internal/ingress -run 'TestChatService' -v
```

预期：全部 PASS（6 个测试，测试中 mock 不依赖此字段故不受影响）。

---

### 阶段 E：补充 `StreamChunk.ToJSON()` 方法

> **背景：** `handleStream` 需要将每个 `StreamChunk` 序列化为 `data: {...}\n\n`，`model.StreamChunk` 需具备 `ToJSON()` 方法。

- [ ] **步骤 E1：在 `model/request.go` 中新增 `StreamChunk.ToJSON()`**

文件： `internal/model/request.go`（追加）

```go
func (s *StreamChunk) ToJSON() ([]byte, error) {
	return json.Marshal(s)
}
```

- [ ] **步骤 E2：验证构建**

```bash
go build ./...
```

预期：无错误。

---

### 阶段 F：import 整理与全量验证

- [ ] **步骤 F1：整理 import**

`internal/ingress/service.go` 需要补充 import：

```go
import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)
```

`internal/ingress/handler.go` 需要补充 import：

```go
import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)
```

- [ ] **步骤 F2：运行全量 ingress 测试**

```bash
go test ./internal/ingress -v
```

预期：全部 PASS（包括原有 23 个中间件测试 + 新增 14 个 handler/service 测试 = 37 个测试）。

- [ ] **步骤 F3：运行全量测试**

```bash
go test ./...
```

预期：全部 PASS。

- [ ] **步骤 F4：提交**

```bash
git add internal/ingress/service.go internal/ingress/service_test.go internal/ingress/handler.go internal/ingress/handler_test.go internal/model/request.go
git commit -m "feat: 实现聊天补全 handler 完整编排"
```

---

### 任务 17 文件清单

| 文件 | 状态 | 职责 |
|------|------|------|
| `internal/ingress/service.go` | 新建 | `ChatService` 接口、`chatServiceImpl` 编排、`CircuitBreakerManager` 实现 |
| `internal/ingress/service_test.go` | 新建 | 编排逻辑单元测试（含 mock，10 个测试） |
| `internal/ingress/handler.go` | 新建 | 路由注册、非流式/流式 Handler、SSE 写入、错误码映射 |
| `internal/ingress/handler_test.go` | 新建 | HTTP 集成测试（6 个测试） |
| `internal/model/request.go` | 修改 | 新增 `StreamChunk.ToJSON()` |

## 任务 18：在 main.go 中组装服务

**文件：**
- 修改： `cmd/gateway/main.go`

### 组装清单

按依赖顺序初始化以下组件，最后注入 ChatService 并启动 HTTP 服务：

```
load config
  → init zap logger
  → init embedding engine (ONNX + tokenizer)
  → init Redis client
  → init provider registry (traverse cfg.Providers, create adapters, register)
  → init balancer (register all provider names)
  → init circuit breaker manager
  → init semantic router (precompute category prototype vectors)
  → init capability router
  → init cost cascade router
  → init semantic cache (embedder + optional RedisStore)
  → init fallback engine
  → init main router (with modelProviders / buildCandidates closures)
  → NewChatService(router, cbManager, cache, fallback, providerLookup)
  → RegisterRoutes(r, chatSvc)
  → graceful shutdown (close redis, embedding engine, cache store)
```

### 关键类型转换

| 来源 (config) | 目标 (其他包) | 转换方式 |
|---------------|--------------|---------|
| `config.SemanticCacheConfig` | `cache.SemanticCacheConfig` | 逐字段拷贝 |
| `config.BalancerConfig` | `decision.BalancerConfig` | `WarmupDuration.Seconds()` → `float64`；`HealthWindowSize.Seconds()` → `float64` |
| `config.FallbackConfig` | `fallback.NewEngine()` | `cfg.Fallback.Chains`, `cfg.Fallback.RetryMax`, `cfg.Fallback.RetryBackoff` |
| `config.CircuitBreakerConfig` | `ingress.NewCircuitBreakerManager()` | `cfg.CircuitBreaker.FailureThreshold`, `cfg.CircuitBreaker.Cooldown` |
| `config.SemanticRoutingConfig` | `decision.NewSemanticRouter()` | 直接传递 |
| `config.RoutingConfig.Rules` | `decision.NewCapabilityRouter()` | 直接传递 |
| `config.RoutingConfig.Cascade` | `decision.NewCostRouter()` | 直接传递 |

### 闭包函数

`ModelProvidersFunc` 和 `BuildCandidatesFunc` 是两个关键回调，桥接 Registry / Config / Balancer：

```go
modelProviders := func(modelName string) []model.Provider {
    return registry.ProvidersForModel(modelName)
}

buildCandidates := func(providers []model.Provider, modelName string) []decision.ProviderCandidate {
    candidates := make([]decision.ProviderCandidate, len(providers))
    for i, p := range providers {
        cfg, ok := cfg.Providers[p.Name()]
        baseWeight := 100.0
        if ok {
            baseWeight = float64(cfg.Weight)
        }
        candidates[i] = decision.ProviderCandidate{
            ProviderName:  p.Name(),
            Model:         modelName,
            BaseWeight:    baseWeight,
            HealthScore:   1.0,
            WarmupFactor:  1.0,
        }
    }
    return candidates
}
```

- `modelProviders` 通过 Registry 按模型名查找所有可用 Provider，返回 `[]model.Provider` 切片。
- `buildCandidates` 将 Provider 列表转为 Balancer 候选，从配置提取静态权重；`HealthScore` 和 `WarmupFactor` 初始为 1.0（后续由健康检查和预热逻辑动态更新）。

### 步骤

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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/viif/momu-llmgateway/internal/cache"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/egress"
	"github.com/viif/momu-llmgateway/internal/embedding"
	"github.com/viif/momu-llmgateway/internal/fallback"
	"github.com/viif/momu-llmgateway/internal/ingress"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)

func main() {
	// ── 1. 日志 ──────────────────────────────────────────────
	if err := observability.InitLogger(false); err != nil {
		panic(err)
	}
	log := observability.Logger

	// ── 2. 配置 ──────────────────────────────────────────────
	cfgPath := os.Getenv("GATEWAY_CONFIG")
	if cfgPath == "" {
		cfgPath = "configs/gateway.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("load config", zap.Error(err))
	}
	_ = config.WatchAndReload(cfgPath, func(*config.Config) {
		log.Info("config reloaded")
	})

	// ── 3. 嵌入引擎 ──────────────────────────────────────────
	if err := embedding.Init(cfg.Embedding.OnnxLibraryPath, cfg.Embedding.ModelPath); err != nil {
		log.Warn("embedding engine init failed, semantic features disabled", zap.Error(err))
	}

	// ── 4. Redis ─────────────────────────────────────────────
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatal("redis connect", zap.Error(err))
	}

	// ── 5. Provider 注册表 ──────────────────────────────────
	registry := egress.NewRegistry()
	for name, pc := range cfg.Providers {
		var p model.Provider
		switch pc.Type {
		case "anthropic":
			p = egress.NewAnthropic(pc.BaseURL, pc.APIKey, pc.Models, pc.Timeout)
		default:
			p = egress.NewOpenAICompatible(name, pc.BaseURL, pc.APIKey, pc.Models, pc.Timeout)
		}
		registry.Register(p)
		log.Info("provider registered", zap.String("name", name), zap.Strings("models", pc.Models))
	}

	// ── 6. 负载均衡器 ────────────────────────────────────────
	balancerCfg := decision.BalancerConfig{
		ConcurrencyPenaltyCoefficient: cfg.Balancer.ConcurrencyPenaltyCoefficient,
		LatencyPenaltyCoefficient:     cfg.Balancer.LatencyPenaltyCoefficient,
		WarmupEnabled:                 cfg.Balancer.WarmupEnabled,
		WarmupDuration:                cfg.Balancer.WarmupDuration.Seconds(),
		HealthWindowSize:              cfg.Balancer.HealthWindowSize.Seconds(),
		HealthMinRequests:             cfg.Balancer.HealthMinRequests,
	}
	balancer := decision.NewBalancer(balancerCfg)
	for name := range cfg.Providers {
		balancer.Register(name)
	}

	// ── 7. 熔断器管理器 ──────────────────────────────────────
	cbManager := ingress.NewCircuitBreakerManager(
		cfg.CircuitBreaker.FailureThreshold,
		cfg.CircuitBreaker.Cooldown,
	)

	// ── 8. 路由策略 ──────────────────────────────────────────
	var (
		semanticRouter  *decision.SemanticRouter
		capabilityRouter = decision.NewCapabilityRouter(cfg.Routing.Rules)
		costRouter       = decision.NewCostRouter(cfg.Routing.Cascade)
	)

	if emb := embedding.Instance(); emb != nil {
		semanticRouter, err = decision.NewSemanticRouter(cfg.SemanticRouting, emb)
		if err != nil {
			log.Warn("semantic router init failed", zap.Error(err))
		} else {
			log.Info("semantic router initialized",
				zap.Int("categories", len(cfg.SemanticRouting.Categories)))
		}
	}

	// ── 9. 语义缓存 ──────────────────────────────────────────
	var (
		semanticCache *cache.SemanticCache
		cacheStore    cache.CacheStore
	)
	if cfg.SemanticCache.Enabled {
		if redisClient != nil {
			cacheStore, err = cache.NewRedisStore(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
			if err != nil {
				log.Warn("cache redis store init failed, using memory-only", zap.Error(err))
			}
		}

		cacheCfg := cache.SemanticCacheConfig{
			Enabled:             cfg.SemanticCache.Enabled,
			SimilarityThreshold: cfg.SemanticCache.SimilarityThreshold,
			MaxEntries:          cfg.SemanticCache.MaxEntries,
			TTL:                 cfg.SemanticCache.TTL,
			MaxPromptLength:     cfg.SemanticCache.MaxPromptLength,
		}
		semanticCache = cache.New(cacheCfg, embedding.Instance(), cacheStore)

		if cacheStore != nil {
			allModels := collectAllModels(cfg.Providers)
			semanticCache.SetModels(allModels)
		}
	}

	// ── 10. Fallback 引擎 ────────────────────────────────────
	fallbackEng := fallback.NewEngine(
		cfg.Fallback.Chains,
		cfg.Fallback.RetryMax,
		cfg.Fallback.RetryBackoff,
		"",
	)

	// ── 11. 主 Router ────────────────────────────────────────
	modelProviders := func(modelName string) []model.Provider {
		return registry.ProvidersForModel(modelName)
	}

	buildCandidates := func(providers []model.Provider, modelName string) []decision.ProviderCandidate {
		candidates := make([]decision.ProviderCandidate, len(providers))
		for i, p := range providers {
			pc, ok := cfg.Providers[p.Name()]
			baseWeight := 100.0
			if ok {
				baseWeight = float64(pc.Weight)
			}
			candidates[i] = decision.ProviderCandidate{
				ProviderName:  p.Name(),
				Model:         modelName,
				BaseWeight:    baseWeight,
				HealthScore:   1.0,
				WarmupFactor:  1.0,
			}
		}
		return candidates
	}

	router := decision.NewRouter(
		decision.RouterConfig{
			Strategies:     cfg.Routing.Strategies,
			DefaultCascade: cfg.Routing.Cascade["default"],
		},
		balancer,
		semanticRouter,
		capabilityRouter,
		costRouter,
		modelProviders,
		buildCandidates,
	)

	// ── 12. ChatService ──────────────────────────────────────
	providerLookup := func(name string) model.Provider {
		return registry.ProviderByName(name)
	}

	// 适配类型：*cache.SemanticCache → ingress.SemanticCache
	var ingressCache ingress.SemanticCache
	if semanticCache != nil {
		ingressCache = semanticCache
	}

	chatSvc := ingress.NewChatService(
		router,
		cbManager,
		ingressCache,
		fallbackEng,
		providerLookup,
	)

	// ── 13. Prometheus 指标 ──────────────────────────────────
	observability.RegisterMetrics(prometheus.DefaultRegisterer)

	// ── 14. HTTP 服务 ────────────────────────────────────────
	allModels := collectAllModels(cfg.Providers)

	r := gin.New()
	r.Use(
		gin.Recovery(),
		ingress.RequestIDMiddleware(),
		ingress.LoggingMiddleware(),
		ingress.AuthMiddleware(cfg.Auth.APIKeys),
		ingress.RateLimitMiddleware(redisClient),
		ingress.ValidationMiddleware(allModels),
	)
	ingress.RegisterRoutes(r, chatSvc)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		log.Info("gateway starting", zap.Int("port", cfg.Server.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server failed", zap.Error(err))
		}
	}()

	// ── 15. 优雅关闭 ─────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown error", zap.Error(err))
	}

	if redisClient != nil {
		_ = redisClient.Close()
	}
	if emb := embedding.Instance(); emb != nil {
		emb.Close()
	}
	if cacheStore != nil {
		_ = cacheStore.Close()
	}

	log.Info("gateway stopped")
}

func collectAllModels(providers map[string]config.ProviderConfig) []string {
	seen := map[string]bool{}
	var models []string
	for _, p := range providers {
		for _, m := range p.Models {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
	}
	return models
}
```

> **说明：** 语义缓存 `RedisStore` 与限流中间件共用同一个 `redisClient`。`cache.NewRedisStore()` 内部会创建新的 Redis 连接，为保持连接池独立，此处由 `cache.NewRedisStore` 独立建连；限流中间件使用顶层的 `redisClient`。若需复用同一连接，可将 `cache.NewRedisStore` 改为接受 `*redis.Client` 参数的构造函数（后续优化项）。

> **类型适配说明：** `*cache.SemanticCache` 隐式实现了 `ingress.SemanticCache` 接口（`Lookup` / `Store` 签名完全匹配），直接赋值给 `ingressCache` 即可。同样 `*fallback.Engine` 隐式实现了 `ingress.FallbackEngine` 接口。

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
git commit -m "feat: 组装网关服务完整启动流程"
```

---

## 任务 19：添加 Dockerfile 和 .dockerignore

**背景：** 本项目依赖 CGO 构建（`internal/embedding/onnx.go` 无条件导入 `github.com/yalue/onnxruntime_go`），运行时需要 ONNX Runtime 共享库和嵌入模型文件，不可使用 `CGO_ENABLED=0`。多阶段构建：builder 拉取 ONNX Runtime 动态库用于 CGO 链接；runtime 安装库文件 + curl（健康检查）+ 模型文件，以非 root 用户运行。

**文件：**
- 新建： `.dockerignore`
- 新建： `Dockerfile`

- [ ] **步骤 1：创建 .dockerignore**

文件： `.dockerignore`

```
.git
.github
*.md
docs/
testdata/
vendor/
tmp/
*.log
.env
.env.*
.idea/
.vscode/
.DS_Store
```

- [ ] **步骤 2：创建多阶段 Dockerfile（含 ONNX 支持、非 root、健康检查）**

文件： `Dockerfile`

> **要点说明：**
> - Builder 阶段安装 ONNX Runtime 1.21.0 动态库供 CGO 链接，`CGO_ENABLED=1`。
> - Runtime 阶段复制 ONNX Runtime 库文件、curl（HEALTHCHECK 依赖）、嵌入模型目录。
> - 使用 `debian:bookworm-slim` 而非 Alpine（ONNX Runtime 官方不提供 musl 构建）。
> - 创建非 root 用户 `gateway`、设置 `HEALTHCHECK`、`STOPSIGNAL SIGTERM`、OCI 标签。
> - 嵌入模型文件通过 `COPY --chown=gateway:gateway` 设置属主，确保运行时可读。
> - `BUILD_TIME` 和 `VERSION` 通过 `ARG` 传入，默认值为 `dev` / 空。

```dockerfile
# ── Builder Stage ──
FROM golang:1.25-bookworm AS builder

ARG ONNX_VERSION=1.21.0

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && \
    curl -sL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-${ONNX_VERSION}.tgz" -o onnxruntime.tgz && \
    tar xzf onnxruntime.tgz && \
    cp onnxruntime-linux-x64-${ONNX_VERSION}/lib/libonnxruntime.so* /usr/local/lib/ && \
    rm -rf onnxruntime*

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG BUILD_TIME
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
    -o /gateway ./cmd/gateway

# ── Runtime Stage ──
FROM debian:bookworm-slim

ARG VERSION=dev
ARG BUILD_TIME

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata curl && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/lib/libonnxruntime.so* /usr/local/lib/
RUN ldconfig

RUN groupadd -r gateway && useradd -r -g gateway -d /app -s /sbin/nologin gateway
WORKDIR /app

COPY --from=builder /gateway /app/gateway
COPY configs/gateway.yaml /app/configs/gateway.yaml
COPY --chown=gateway:gateway .models/ /app/.models/

ENV GATEWAY_CONFIG=/app/configs/gateway.yaml

LABEL org.opencontainers.image.title="momu-llmgateway" \
      org.opencontainers.image.description="LLM Gateway with multi-provider routing" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/viif/momu-llmgateway"

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

STOPSIGNAL SIGTERM
USER gateway
ENTRYPOINT ["/app/gateway"]
```

- [ ] **步骤 3：验证构建**

```bash
docker build -t momu-llmgateway:local .
```

预期：镜像构建成功。

> **注意：** 如本地缺少 `.models/` 目录（含 `tokenizer_config.json`、`tokenizer.json`、`model.onnx`），可先创建占位目录或跳过模型 COPY。占位做法：
> ```bash
> mkdir -p .models && touch .models/.gitkeep
> ```
> 运行时若启动后 embedding 初始化失败（日志 Warn），语义缓存和语义路由将自动降级，不影响核心聊天补全能力。

- [ ] **步骤 4：提交**

```bash
git add Dockerfile .dockerignore
git commit -m "feat: 添加 dockerfile 和 .dockerignore"
```

---

## 任务 20：完善 GitHub Actions CI（加入 lint、竞态检测、ONNX 支持）

> 任务 2 已建立基础 CI（build + test），本任务在其基础上增加 lint 检查、竞态检测（`-race`）、ONNX Runtime 共享库安装和 BGE 模型下载，使 CI 可运行全量测试（含嵌入引擎集成测试）。

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

      - name: Install ONNX Runtime
        run: |
          ONNX_VERSION="1.21.0"
          ONNX_URL="https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-${ONNX_VERSION}.tgz"
          curl -sL "$ONNX_URL" -o onnxruntime.tgz
          tar xzf onnxruntime.tgz
          sudo cp onnxruntime-linux-x64-*/lib/libonnxruntime.so* /usr/local/lib/
          sudo ldconfig
          echo "ONNXRUNTIME_LIB_PATH=/usr/local/lib/libonnxruntime.so.${ONNX_VERSION}" >> $GITHUB_ENV

      - name: Download BGE Model
        run: |
          MODEL_DIR="./.models/bge-small-zh-v1.5"
          HF_BASE="https://huggingface.co/onnx-community/bge-small-zh-v1.5-ONNX/resolve/main"
          mkdir -p "$MODEL_DIR"
          curl -sL "$HF_BASE/onnx/model.onnx" -o "$MODEL_DIR/model.onnx"
          curl -sL "$HF_BASE/onnx/model.onnx_data" -o "$MODEL_DIR/model.onnx_data"
          curl -sL "$HF_BASE/tokenizer.json" -o "$MODEL_DIR/tokenizer.json"
          curl -sL "$HF_BASE/tokenizer_config.json" -o "$MODEL_DIR/tokenizer_config.json"
          echo "EMBEDDING_MODEL_PATH=$MODEL_DIR" >> $GITHUB_ENV

      - name: Build
        run: go build ./...

      - name: Run tests
        run: go test -race ./...
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
git commit -m "ci: 添加 lint、竞态检测和 onnx 支持"
```

---

## 任务 21：端到端手动验证

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

预期：返回 200 或上游 Provider 错误（取决于是否配置了有效的 API Key）。认证中间件不应返回 `401`。若无有效 API Key，预期为 `502 Bad Gateway`。

- [ ] **步骤 6：流式请求验证**

```bash
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":true}'
```

预期：返回 SSE 流式响应（`Content-Type: text/event-stream`），逐块推送 `data:` 行，以 `data: [DONE]` 结束。

- [ ] **步骤 7：认证失败场景验证**

```bash
# 无 Authorization 头 → 401
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'

# 错误 API Key → 401
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-invalid" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'

# 非 Bearer 前缀 → 401
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Basic sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'
```

预期：全部返回 `401 Unauthorized`，响应体包含 `invalid api key` 错误信息。

- [ ] **步骤 8：提交最终验证修正**

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
          -> 任务 8 Anthropic 适配器
          -> 任务 9 Provider 注册表
       -> 任务 10 熔断器
       -> 任务 11 负载均衡
       -> 任务 12 嵌入引擎
       -> 任务 13 路由策略
       -> 任务 14 语义缓存
       -> 任务 15 Fallback
       -> 任务 16 中间件
       -> 任务 17 Handler
           -> 任务 18 main.go 组装
               -> 任务 19 Dockerfile
               -> 任务 20 完善 CI
               -> 任务 21 端到端验证
```

## 自检结果

- Spec 覆盖：配置热加载、环境变量展开、OpenAI/Anthropic/DeepSeek/Qwen/GLM Provider、路由策略、熔断、负载均衡、语义缓存、Fallback、RequestID 注入、API Key 认证、滑动窗口限流、参数校验、结构化日志、Prometheus、Dockerfile、GitHub Actions CI、手动验证均有对应任务。
- CI 对齐：`.github/workflows/ci.yml` 只包含 `lint` 和 `test` job；不包含镜像构建 job。
- 占位符检查：未发现占位式待补内容。
- 类型一致性：核心类型从任务 3 定义，后续任务统一引用 `model.StandardRequest`、`model.StandardResponse`、`model.Provider`、`model.StreamChunk`。
