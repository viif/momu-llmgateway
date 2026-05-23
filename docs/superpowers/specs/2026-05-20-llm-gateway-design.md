# LLM Gateway 设计文档

## 概述

生产级 Go 语言 LLM Gateway，作为企业级 AI 应用的统一入口，屏蔽底层不同 LLM 供应商的协议差异，提供智能路由、负载均衡、熔断降级、语义缓存和可观测性能力。

## 技术栈

- **语言**: Go 1.21+
- **Web 框架**: Gin
- **配置管理**: Viper (本地 YAML 文件热加载)
- **日志**: Zap (结构化日志)
- **监控**: Prometheus
- **缓存/限流**: Redis (必需依赖)
- **CI/CD**: GitHub Actions
- **Go Module**: `github.com/viif/momu-llmgateway`

## 支持的 Provider

| Provider | API 格式 | 特殊处理 / 备注 |
| :--- | :--- | :--- |
| OpenAI | 原生基准格式 | 网关的适配基准，直接转发。 |
| Anthropic | 独立格式 | 需提取 `system` 为顶层字段；流式响应需解析 `event:` 事件流。 |
| DeepSeek | OpenAI 兼容 | 仅需更换 `base_url` 和 `api_key`。 |
| 通义千问 (Qwen) | OpenAI 兼容 | 需使用 DashScope 的兼容模式 endpoint。 |
| 智谱 (GLM) | OpenAI 兼容 | 官方已兼容 OpenAI 接口，无需复杂的格式转换，直接复用 OpenAI 适配器即可。 |

## 架构: Clean Architecture + 接口驱动

### 目录结构

```
momu-llmgateway/
├── cmd/
│   └── gateway/
│       └── main.go                 # 启动入口
├── internal/
│   ├── config/
│   │   └── config.go               # Viper 配置加载 + 热更新
│   ├── model/
│   │   ├── request.go              # StandardRequest / StandardResponse
│   │   ├── provider.go             # Provider 接口定义
│   │   └── errors.go               # 统一错误码
│   ├── ingress/
│   │   ├── handler.go              # /v1/chat/completions handler
│   │   ├── middleware_auth.go      # API Key 认证
│   │   ├── middleware_ratelimit.go  # Redis 滑动窗口限流
│   │   └── middleware_logging.go   # 请求日志
│   ├── decision/
│   │   ├── router.go               # 路由引擎入口
│   │   ├── strategy_capability.go  # 基于能力的路由
│   │   ├── strategy_cost.go        # 基于成本的级联路由
│   │   ├── strategy_semantic.go    # 语义路由（Embedding 分类）
│   │   ├── balancer.go             # 加权负载均衡
│   │   └── circuitbreaker.go       # 熔断器
│   ├── egress/
│   │   ├── adapter.go              # 适配器注册中心
│   │   ├── openai.go              # OpenAI 兼容适配器（OpenAI/DeepSeek/Qwen/GLM）
│   │   ├── anthropic.go            # Anthropic/Claude 适配器
│   ├── cache/
│   │   └── semantic.go             # 语义缓存（Embedding 相似度匹配）
│   ├── fallback/
│   │   └── engine.go               # L1-L4 多层降级引擎
│   └── observability/
│       ├── logger.go               # Zap 日志初始化
│       ├── metrics.go              # Prometheus 指标定义
│       └── tracing.go              # RequestID 全链路追踪
├── configs/
│   └── gateway.yaml                # 默认配置文件
├── .github/
│   └── workflows/
│       └── ci.yml                  # GitHub Actions CI
├── Dockerfile
├── go.mod
└── go.sum
```

## 0. 配置管理 (Config)

### 热加载机制

Viper `WatchConfig()` 监听本地 `gateway.yaml` 文件变更。不涉及远程配置中心。

### 原子性保证

使用**不可变对象 + atomic.Value** 模式，避免 RWMutex 的读锁竞争：

1. 配置加载时，构建完整的不可变配置对象（`*Config` struct）
2. 通过 `atomic.Value.Store()` 原子替换
3. 读取方通过 `atomic.Value.Load()` 获取当前配置快照
4. 配置变更不会产生"中间状态"——旧请求使用旧配置快照，新请求使用新配置快照

```go
var currentConfig atomic.Value // stores *Config

func GetConfig() *Config {
    return currentConfig.Load().(*Config)
}
```

### 环境变量展开

配置文件中的 `${ENV_VAR}` 占位符在加载时展开为环境变量值，用于 Provider API Key 等敏感信息。

## 1. 接入层 (Ingress Layer)

### HTTP 端点

- `POST /v1/chat/completions` — 兼容 OpenAI API 格式的统一入口
- `GET /health` — 健康检查
- `GET /metrics` — Prometheus 指标

### 中间件链

```
Request → RequestID注入 → Zap日志 → API Key认证 → 滑动窗口限流 → 参数校验 → Handler
```

### 认证

配置文件中静态定义 API Key 列表。请求头 `Authorization: Bearer <key>` 校验。每个 Key 可配置：
- 独立的限流配额 (requests/minute)
- 允许的模型列表（`["*"]` 表示全部）

### 限流

Redis 滑动窗口算法：`ZRANGEBYSCORE` + `ZADD`。每个 API Key 独立计数，窗口大小和限额从配置读取。

### 参数校验

- `model` 字段在配置的模型列表中
- `messages` 非空
- `temperature` 在 [0, 2] 范围内
- `max_tokens` 为正整数（如提供）

### 请求转换

OpenAI 格式请求 → 解析为 `StandardRequest` → 注入 RequestID 和元数据 → 传入决策层。

## 2. 决策层 (Decision Layer)

### 路由引擎

按优先级依次执行策略链：

1. **显式路由**: 请求指定具体 Provider（model 名前缀如 `openai/gpt-4o`），直接路由
2. **语义路由**: 通过 Embedding 模型将请求向量化，匹配预定义任务类别
3. **基于能力的路由**: 根据 `task_type` 标签或预估 token 长度选择模型组
4. **基于成本的级联路由**: 按配置的模型级联链优先使用低成本模型
5. **默认路由**: 未匹配任何策略时使用默认模型

### 语义路由 (Semantic Router)

**流程**:
1. 调用 Embedding Provider 对请求最后一条用户消息生成向量
2. 计算与各预定义类别向量的余弦相似度
3. 取最高分且超过阈值的类别，路由至该类别配置的目标模型
4. 未超过阈值则传递至下一路由策略

**类别预定义**: 在配置中为每个路由类别定义 "代表性 prompt"，启动时批量生成 Embedding 并缓存到内存。

**接口**:
```go
type SemanticRouter interface {
    Classify(ctx context.Context, text string) (category string, confidence float64, err error)
}

type EmbeddingProvider interface {
    Embed(ctx context.Context, texts []string) ([][]float64, error)
}
```

### 加权负载均衡

同一模型多个 Provider 实例时的权重计算：

```
权重 = base_weight × (1 / (1 + active_connections)) × health_score
```

- `base_weight`: 配置的静态权重
- `active_connections`: 当前并发请求数（原子计数）
- `health_score`: 基于近期成功率的动态分数 [0, 1]

### 熔断器

三态模型: **Closed → Open → Half-Open**

- **Closed**: 正常转发，记录失败次数
- **Open**: 连续失败达阈值（默认 5 次/10 秒），拒绝请求，进入冷却期（默认 30 秒）
- **Half-Open**: 冷却期结束后放行探测请求，成功恢复 Closed，失败回到 Open

每个 Provider + Model 组合独立维护熔断状态。

## 3. 出口层 (Egress Layer)

### Provider 适配器

每个适配器实现 `Provider` 接口：

```go
type Provider interface {
    Name() string
    Send(ctx context.Context, req *StandardRequest) (*StandardResponse, error)
    SendStream(ctx context.Context, req *StandardRequest) (<-chan StreamChunk, error)
    Models() []string
}
```

**OpenAICompatible 适配器**: 处理 OpenAI 格式的请求/响应，可通过配置 base_url 和 api_key 复用于 OpenAI、DeepSeek、Qwen、GLM。GLM 已兼容 OpenAI 接口，直接配置为 `openai` 类型即可。SSE 流式解析内置在同一文件中。

**Anthropic 适配器**: 将 StandardRequest 转为 Anthropic Messages API 格式（system 提取为顶层字段，流式响应用独立的 `event:` / `data:` 双行 SSE 解析）。

### SSE 流式处理

- `bufio.Scanner` 逐行读取上游 SSE 响应
- 实时转换为 OpenAI 格式 `data: {...}\n\n` 事件
- Gin `c.Stream()` 实时写入客户端
- 处理 `[DONE]` 信号和连接中断

### 流式错误处理

SSE 流一旦开始（HTTP 200 已发送），中途发生的错误（上游报错、违规拦截、超时等）**不能**返回 HTTP 500。处理方式：

1. 通过 SSE `data:` 字段发送包含 error 的 JSON 对象：
   ```
   data: {"error":{"message":"upstream provider error","type":"provider_error","code":"upstream_500"}}\n\n
   ```
2. 发送后立即关闭连接
3. 记录错误日志和 Prometheus 指标（标记为 stream_error）

## 4. 语义缓存 (Semantic Cache)

### 请求链路位置

```
Request → 认证 → 限流 → 校验 → 【语义缓存查询】→ 路由决策 → Provider调用 → 【语义缓存写入】→ 响应
```

缓存命中时直接短路返回，不进入路由和 Provider 调用。

### 实现

1. **缓存写入**: 非流式请求成功后，将 `(请求 Embedding, 响应)` 存入 Redis
   - Embedding 向量序列化存储
   - 响应 JSON 存储，设置 TTL

2. **缓存查询**: 请求到来时生成 Embedding，在缓存中查找相似向量
   - Redis 存储向量 + 余弦相似度搜索
   - 相似度超过阈值（默认 0.95）时命中
   - 按 model 分区缓存

3. **缓存策略**:
   - TTL 可配置（默认 1 小时）
   - 仅缓存非流式请求
   - 容量限制 + LRU 淘汰

## 5. 容错与高可用 (Fallback)

### 多层降级

```
L1: 同实例重试（指数退避，最多 2 次，仅 5xx/超时）
  ↓ 失败
L2: 跨 Provider 降级（如 GPT-4o → Claude Sonnet）
  ↓ 失败
L3: 跨模型降级（如 GPT-4o → GPT-4o-mini）
  ↓ 失败
L4: 返回预设兜底响应 + 错误码
```

降级链在配置文件中定义，每个模型可配置 L2/L3 备选。

## 6. 可观测性 (Observability)

### Prometheus 指标

| 指标名 | 类型 | 标签 | 说明 |
|--------|------|------|------|
| `llm_request_duration_seconds` | Histogram | provider, model | P50/P95/P99 延迟 |
| `llm_request_total` | Counter | provider, model, status | 请求总数 |
| `llm_tokens_total` | Counter | provider, model, direction | Token 消耗 (input/output) |
| `llm_fallback_total` | Counter | level, from_model, to_model | Fallback 触发次数 |
| `llm_circuit_breaker_state` | Gauge | provider, model | 熔断器状态 (0=closed, 1=open, 2=half-open) |
| `llm_cache_hit_total` | Counter | model, cache_type | 语义缓存命中次数 |

### 结构化日志 (Zap)

每次请求记录: request_id, model, provider, input_tokens, output_tokens, latency_ms, cost_estimate, fallback_reason, cache_hit。

## 7. 核心数据模型

```go
type StandardRequest struct {
    RequestID   string
    Model       string
    Messages    []Message
    Stream      bool
    Temperature *float64
    MaxTokens   *int
    TaskType    string
    Metadata    map[string]string
}

type Message struct {
    Role    string
    Content string
}

type StandardResponse struct {
    ID        string
    Model     string
    Provider  string
    Choices   []Choice
    Usage     Usage
    CacheHit  bool
}

type Choice struct {
    Index        int
    Message      Message
    FinishReason string
}

type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}

type StreamChunk struct {
    ID      string
    Model   string
    Delta   Delta
    Done    bool
}

type Delta struct {
    Role    string
    Content string
}
```

## 8. 配置文件 (gateway.yaml)

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
      exemplars:
        - "Write a Python function that..."
        - "Generate code to..."
        - "帮我写一个..."
    - name: "creative_writing"
      target_models: ["claude-sonnet-4-20250514", "gpt-4o"]
      exemplars:
        - "Write a story about..."
        - "Create a poem..."
        - "写一篇关于..."
    - name: "data_analysis"
      target_models: ["gpt-4o", "deepseek-chat"]
      exemplars:
        - "Analyze this dataset..."
        - "分析这些数据..."

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
    claude-sonnet-4-20250514: ["gpt-4o", "deepseek-chat"]
    deepseek-chat: ["gpt-4o-mini", "qwen-turbo"]

circuit_breaker:
  failure_threshold: 5
  window: 10s
  cooldown: 30s
```

## 9. GitHub Actions CI

```yaml
# .github/workflows/ci.yml
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
      - run: go test -race -coverprofile=coverage.out ./...
      - name: Upload coverage
        uses: codecov/codecov-action@v4
```

## 10. 验证方案

### 单元测试
- 每个 Provider 适配器的请求/响应转换
- 路由策略的匹配逻辑
- 熔断器状态转换
- 限流算法正确性
- 语义缓存命中/未命中
- Fallback 链的级联触发

### 集成测试
- 启动 Redis 容器 + Mock LLM Server
- 端到端请求流程验证
- SSE 流式响应验证
- 熔断 + 降级场景验证

### 手动验证
```bash
# 启动服务
go run cmd/gateway/main.go

# 普通请求
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'

# 流式请求
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":true}'

# 检查 Prometheus 指标
curl http://localhost:8080/metrics

# 健康检查
curl http://localhost:8080/health
```
