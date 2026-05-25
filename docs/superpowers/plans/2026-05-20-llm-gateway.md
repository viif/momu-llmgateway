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

- [x] **步骤 1：创建目录结构**
- [x] **步骤 2：初始化 Go module 和依赖**（gin、viper、zap、prometheus、redis、testify、miniredis）
- [x] **步骤 3：创建最小入口文件** `cmd/gateway/main.go` — 打印启动消息。
- [x] **步骤 4：验证构建** `go build ./...`
- [x] **步骤 5：提交** `feat: 搭建 llm gateway 工程骨架`

---

## 任务 2：添加 GitHub Actions CI（基础版本）

> CI 内容将在后续任务中逐步丰富。基础版本先保证构建和测试可在 CI 中自动运行。

**文件：**
- 新建： `.github/workflows/ci.yml`

- [x] **步骤 1：创建基础 CI workflow**（build + test，go 1.21，ubuntu-latest）
- [x] **步骤 2：验证 YAML 可解析**
- [x] **步骤 3：验证构建和测试**
- [x] **步骤 4：提交** `ci: 添加 github actions 基础构建和测试 workflow`

> CI 逐步丰富说明：本任务建立 CI 基础骨架。后续每个新任务在增加代码和测试的同时，CI 也会随之验证更多内容。最终会在 CI 中逐步补充 lint 检查、竞态检测、覆盖率上报等增强步骤（参见任务 20）。

---

## 任务 3：定义核心数据模型

**文件：**
- 新建： `internal/model/request.go`
- 新建： `internal/model/provider.go`
- 新建： `internal/model/errors.go`
- 新建： `internal/model/request_test.go`

关键类型：
- `StandardRequest` / `Message` — 标准化请求体，含 model、messages、stream、temperature、max_tokens 等字段。
- `StandardResponse` / `Choice` / `Usage` / `StreamChunk` / `Delta` — 标准化响应体和流式块结构。
- `Provider` / `EmbeddingProvider` 接口 — 定义 `Send` / `SendStream` / `Models` / `Embed` 方法签名。
- `Error` 类型 — 统一错误码常量（`invalid_request`、`authentication_error`、`rate_limit_exceeded` 等），实现 `error` 接口。

- [x] **步骤 1：先写请求解析测试** `TestParseStandardRequest` / `TestStandardResponseToJSON`
- [x] **步骤 2：运行测试确认失败**
- [x] **步骤 3：实现核心类型**（`StandardRequest`、`StandardResponse`、`ParseStandardRequest`、`ToJSON`）
- [x] **步骤 4：实现 Provider 接口**
- [x] **步骤 5：实现统一错误类型**
- [x] **步骤 6：验证测试通过** `go test ./internal/model -v`
- [x] **步骤 7：提交** `feat: 添加核心网关数据模型`

---

## 任务 4：实现配置加载、环境变量展开和原子热更新

**文件：**
- 新建： `internal/config/config.go`
- 新建： `internal/config/config_test.go`
- 新建： `configs/gateway.yaml`

关键设计：
- `Config` 结构体包含 server、redis、auth、providers、routing、semantic_routing、semantic_cache、fallback、circuit_breaker、balancer、embedding 子配置。
- `Load(path)` 使用 Viper 读取 YAML，通过 `os.ExpandEnv` 展开 `${ENV_VAR}` 占位符，将解析结果存入 `atomic.Value`。
- `GetConfig()` 从 `atomic.Value` 读取当前配置快照。
- `WatchAndReload(path, onChange)` 监听本地文件变化，变化时重新加载并回调通知。
- `configs/gateway.yaml` 包含完整的默认配置：5 个 Provider（openai、anthropic、deepseek、qwen、glm）、路由策略链、语义缓存、熔断器、负载均衡、嵌入引擎参数等。API Key 通过环境变量占位符引用。

- [x] **步骤 1：先写配置测试** `TestLoadExpandsEnvVars`（验证环境变量展开和类型转换）
- [x] **步骤 2：运行测试确认失败**
- [x] **步骤 3：实现配置结构和 Load**
- [x] **步骤 4：创建默认配置文件** `configs/gateway.yaml`
- [x] **步骤 5：验证测试通过** `go test ./internal/config -v`
- [x] **步骤 6：提交** `feat: 添加 viper 配置加载和热更新`

---

## 任务 5：实现可观测性基础设施

**文件：**
- 新建： `internal/observability/logger.go`
- 新建： `internal/observability/metrics.go`
- 新建： `internal/observability/tracing.go`
- 新建： `internal/observability/tracing_test.go`

关键设计：
- Logger：Zap 初始化（`InitLogger(production bool)`），默认 `zap.NewNop()` 兜底。
- Metrics：Prometheus 指标定义（`RequestDuration`、`RequestTotal`、`TokensTotal`、`FallbackTotal`、`CircuitBreakerState`、`CacheHitTotal`），通过 `RegisterMetrics` 注册。
- Tracing：RequestID 上下文注入（`WithRequestID` / `RequestIDFromContext`），使用 `google/uuid` 生成唯一 ID。

- [x] **步骤 1：先写 RequestID 测试**
- [x] **步骤 2：运行测试确认失败**
- [x] **步骤 3：实现日志、指标和追踪**
- [x] **步骤 4：补充 uuid 依赖并验证** `go test ./internal/observability -v`
- [x] **步骤 5：提交** `feat: 添加可观测性基础设施`

---

## 任务 6：实现 OpenAI 兼容 Provider 适配器

**文件：**
- 新建： `internal/egress/openai.go`
- 新建： `internal/egress/openai_test.go`

关键设计：
- `OpenAICompatible` 结构体持有 name、baseURL、apiKey、models、http.Client。
- `Send` 将 `StandardRequest` 序列化为 OpenAI Chat Completions API 请求体，发送 POST 请求，解析 JSON 响应为 `StandardResponse`。
- `SendStream` 委托给 `StreamOpenAICompatible` 函数（任务 7 实现）。

- [x] **步骤 1：先写请求转换测试** `TestOpenAICompatibleBuildRequest`
- [x] **步骤 2：运行测试确认失败**
- [x] **步骤 3：实现 OpenAI 兼容适配器**
- [x] **步骤 4：验证测试通过** `go test ./internal/egress -run TestOpenAICompatibleBuildRequest -v`
- [x] **步骤 5：提交** `feat: 添加 openai 兼容 provider 适配器`

---

## 任务 7：实现 SSE 流式转换

> 实现后 `parseSSELine` 和 `streamOpenAI` 已合并入 `internal/egress/openai.go`，不再维护独立文件。

**文件：**
- 新建： `internal/egress/stream_openai.go`（后合并入 `openai.go`）
- 新建： `internal/egress/stream_openai_test.go`（后合并入 `openai_test.go`）

关键设计：
- `parseSSELine` 解析 SSE `data:` 行：前缀检查 → `[DONE]` 检测 → JSON 反序列化 → 返回 `StreamChunk`。
- `StreamOpenAICompatible` 发送 `stream: true` 的 POST 请求，在 goroutine 中使用 `bufio.Scanner` 逐行读取 SSE 事件，通过 `parseSSELine` 解析后写入 channel，最终 `close(channel)`。

- [x] **步骤 1：先写 SSE 解析测试** `TestParseSSELine`
- [x] **步骤 2：运行测试确认失败**
- [x] **步骤 3：实现 SSE 解析和转发入口**
- [x] **步骤 4：验证测试通过** `go test ./internal/egress -run TestParseSSELine -v`
- [x] **步骤 5：提交** `feat: 添加 sse 流式转换`

---

## 任务 8：实现 Anthropic Provider 适配器（含流式）

**注：GLM 已确认与 DeepSeek/Qwen 同为 OpenAI 兼容，直接在配置中使用 `type: "openai"` 复用，无需独立适配器。**

**文件：**
- 新建： `internal/egress/anthropic.go`
- 新建： `internal/egress/anthropic_test.go`

本任务分两阶段：
- 阶段 A：实现 Anthropic 基础适配器（system 消息提升、Messages API 协议转换）
- 阶段 B：实现 Anthropic 独立 SSE 流式转换

### 阶段 A：基础适配器

关键设计：
- `Anthropic` 结构体持有 baseURL、apiKey、models、http.Client。
- `buildRequestBody` 遍历消息，将 `role: "system"` 提取为独立 `system` 字段，其余消息保留在 `messages` 数组中（Anthropic API 不支持 system role 消息）。
- `Send` 发送 POST 到 `/v1/messages`，Header 使用 `x-api-key` 和 `anthropic-version: 2023-06-01`。解析 Anthropic 响应结构（`content[].text`、`usage.input_tokens/output_tokens`）转换为 `StandardResponse`。
- `SendStream` 初始为 stub。

- [x] **步骤 1：先写协议转换测试** `TestAnthropicExtractsSystemMessage`
- [x] **步骤 2：运行测试确认失败**
- [x] **步骤 3：实现 Anthropic 适配器**
- [x] **步骤 4：验证测试通过并提交** `feat: 添加 anthropic 适配器`

### 阶段 B：流式响应实现

**Anthropic SSE 格式与 OpenAI 不同**，使用 `event:` + `data:` 双行结构：

| SSE Event | 用途 | 转换到 StreamChunk |
|-----------|------|-------------------|
| `message_start` | 返回 message id/model | 设置 `chunk.ID`、`chunk.Model`，发出 `Delta.Role: "assistant"` |
| `content_block_delta` | 携带 `delta.text`（文本增量） | 映射到 `chunk.Delta.Content` |
| `message_delta` | 携带 `delta.stop_reason`、`usage` | 提取 finish_reason |
| `message_stop` | 流结束 | 发送 `chunk.Done: true` |
| `ping` | 心跳 | 忽略 |

关键设计：
- `parseAnthropicSSEEvent(eventType, data)` 根据 event type 分派解析：`message_start` → 提取 id/model/role；`content_block_delta` → 提取 `delta.text`；`message_stop` → 发送 Done。
- `StreamAnthropic` 使用 `bufio.Scanner` 逐行读取，维护 `currentEvent` 状态变量（event line 与 data line 分离），配对后调用 `parseAnthropicSSEEvent`。
- `SendStream` 委托给 `StreamAnthropic`。

- [x] **步骤 5：先写 Anthropic SSE 事件解析测试** `TestAnthropicParseSSEEvent`
- [x] **步骤 6：运行测试确认失败**
- [x] **步骤 7：实现 Anthropic SSE 解析和流式连接**
- [x] **步骤 8：验证全部测试通过** `go test ./internal/egress -v`
- [x] **步骤 9：提交** `feat: 添加 anthropic 流式适配`

---

## 任务 9：实现 Provider 注册表

**文件：**
- 新建： `internal/egress/adapter.go`
- 新建： `internal/egress/adapter_test.go`

关键设计：
- `Registry` 结构体：切片 `providers`（全量列表）+ map `byName`（O(1) 按名称查找）+ map `byModel`（O(1) 按模型查找），`sync.RWMutex` 保证并发安全。
- `Register(p Provider)`：持有写锁，追加到切片和两个 map 中。
- `ProvidersForModel(modelID)` / `ProviderByName(name)`：持有读锁，返回防御性拷贝。
- `Providers()`：返回全量 Provider 切片拷贝。

- [x] **步骤 1：先写注册表测试**（6 个测试：按模型查找、按名称查找、拷贝保护、未找到、多 Provider 同模型、并发安全）
- [x] **步骤 2：运行测试确认失败**（含 `-race` 检测）
- [x] **步骤 3：实现注册表（切片 + 双 map + RWMutex）**
- [x] **步骤 4：验证测试通过** `go test -race ./internal/egress -run TestRegistry -v`
- [x] **步骤 5：提交** `feat: 添加 provider 注册表`

---

## 任务 10：实现熔断器

**文件：**
- 新建： `internal/decision/circuitbreaker.go`
- 新建： `internal/decision/circuitbreaker_test.go`

关键设计：
- `CircuitState` 枚举：`StateClosed` / `StateOpen` / `StateHalfOpen`。
- `CircuitBreaker` 结构体：`threshold`（失败阈值）、`cooldown`（冷却时间）、`failures`（当前失败计数）、`state`、`openedAt`，通过 `sync.Mutex` 保护。
- `Allow()`：Open 状态且冷却时间已过 → 转 Half-Open 并放行；否则仅 Closed/Half-Open 放行。
- `RecordSuccess()`：清零失败次数，恢复 Closed 状态。
- `RecordFailure()`：递增失败计数，达到阈值 → 转 Open 状态并记录时间。
- `State()`：返回当前状态。

- [x] **步骤 1：先写状态转换测试** `TestCircuitBreakerOpensAfterFailures`
- [x] **步骤 2：运行测试确认失败**
- [x] **步骤 3：实现熔断器**
- [x] **步骤 4：验证测试通过** `go test ./internal/decision -run TestCircuitBreakerOpensAfterFailures -v`
- [x] **步骤 5：提交** `feat: 添加熔断器`

---

## 任务 11：实现平滑动态加权负载均衡（SWRR）

**文件：**
- 新建： `internal/decision/balancer.go`
- 新建： `internal/decision/balancer_test.go`

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

### 高并发优化设计

- 槽位索引：`[]nodeState` + `nameToSlot map[string]int`，Select 热路径用切片下标直接访问，免字符串 hash
- 锁外预计算：`EffectiveWeight`（纯函数）在锁外完成，锁仅覆盖 SWRR 状态操作
- 单循环合并：累加 currentWeight + 选择最大合为一次遍历
- `Register()` 预分配：启动时批量注册槽位，热路径跳过 map 写入

### 关键类型

- `BalancerConfig`：并发/延迟惩罚系数、预热开关和时长、健康窗口参数。
- `ProviderCandidate`：ProviderName、Model、BaseWeight、ActiveConnections、NormalizedP99Latency、HealthScore、WarmupFactor。
- `Balancer`：持有配置、`sync.Mutex`、`[]nodeState` 槽位切片、`nameToSlot` 映射。
- `NewBalancer(cfg)` / `Register(name)` / `EffectiveWeight(c)` / `Select(candidates)`。

- [x] **步骤 1a：先写有效权重计算测试**（含预热因子测试）
- [x] **步骤 1b：先写 SWRR 调度与边界条件测试**（公平性、空候选、单候选）
- [x] **步骤 1c：先写 Register 预分配与槽位索引测试**
- [x] **步骤 2：运行测试确认失败**
- [x] **步骤 3：实现负载均衡器与 SWRR 调度**
- [x] **步骤 4：验证测试通过** `go test ./internal/decision -v`
- [x] **步骤 5：提交** `feat: 实现平滑动态加权负载均衡（SWRR）`

---

## 任务 12：实现本地嵌入引擎

**文件：**
- 新建： `internal/embedding/embedding.go`
- 新建： `internal/embedding/engine.go`
- 新建： `internal/embedding/onnx.go`
- 新建： `internal/embedding/embedding_test.go`
- 新建： `internal/embedding/onnx_integration_test.go`

**依赖：** `onnxruntime_go@v1.30.1` + `go-huggingface/tokenizers/hftokenizer@v0.3.5`

### BGE 模型

从 `onnx-community/bge-small-zh-v1.5-ONNX` 下载以下文件至 `.models/bge-small-zh-v1.5/`：

| 文件 | 用途 |
|---|---|
| `model.onnx` + `model.onnx_data` | ONNX 模型（外部数据格式） |
| `tokenizer.json` | HuggingFace 分词配置 |
| `tokenizer_config.json` | 特殊 token / 参数配置 |

模型输入 (`int64`)：`input_ids [batch, 512]`、`attention_mask [batch, 512]`、`token_type_ids [batch, 512]`。
输出：`last_hidden_state [batch, 512, 512]` float32 → mean pooling → L2 归一化 → `[batch, 512]` float64。

### 架构设计

- `embedding.go`：纯函数 — `CosineSimilarity`、`NormalizeVector`、`MeanPooling`。
- `engine.go`：`EmbeddingEngine` 单例（`sync.Once`），`Init(libPath, modelPath)` 加载 tokenizer 和 ONNX session，`Embed(texts)` 进行推理，`Close()` 释放资源。
- `onnx.go`：`onnxConcrete` 结构体持有 tokenizer 和 `DynamicAdvancedSession`，实现 tokenize → 构造 tensor → `session.Run()` → mean pooling → L2 normalize 流水线。

### CI 配置

`.github/workflows/ci.yml` 的 test job 中：
1. 安装 ONNX Runtime v1.25.0 预编译 `.so` 至 `/usr/local/lib/`
2. 从 HuggingFace 下载 BGE 模型 4 个文件至 `.models/bge-small-zh-v1.5/`
3. 运行全量测试：`go test -race ./...`

### 测试

| 文件 | 内容 |
|---|---|
| `embedding_test.go` | `TestCosineSimilarity` / `TestNormalizeVector` / `TestMeanPooling`（纯函数，3 项） |
| `onnx_integration_test.go` | `TestONNXEmbedding` — 真实加载 ONNX 模型推理，验证 512 维输出 + L2 归一化 |

- [x] **步骤 1-7**：添加依赖 → 实现纯函数和测试 → 实现引擎类型定义 → 实现 ONNX 嵌入引擎 → 写 ONNX 集成测试 → 更新 CI 配置 → 验证全量测试通过
- [x] **步骤 8：提交** `feat: 实现本地 onnx 嵌入引擎`

### 配置项

`configs/gateway.yaml` 新增 `embedding.onnx_library_path` 和 `embedding.model_path`。`Config` 结构体新增 `EmbeddingConfig`，已有字段中不再包含 embedding_provider / embedding_model 配置。

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

### 架构

路由策略链按 `gateway.yaml` 中 `routing.strategies` 配置的顺序依次执行：

```
Route(req)
  │
  ├─ req.Model 含 "/" → 显式路由，直接返回（不走策略链）
  │
  ├─ 按 config.routing.strategies 顺序遍历:
  │   ├─ "capability":   CapabilityRouter.Route(req, tokenEstimate)
  │   ├─ "semantic":     SemanticRouter.Route(req)
  │   └─ "cost_cascade": CostRouter.CascadeFor(req.Model)
  │
  ├─ 全部未命中 → routing.cascade.default 兜底
  │
  └─ 仍失败 → model_not_found error
```

每个策略输出"候选模型列表"。`resolveModelList` 遍历列表，对每个模型查询 Provider 注册表，跳过熔断状态为 Open 的 Provider，找到可用 Provider 后通过 `Balancer.Select` 选出最终节点。

`RouteDecision` 增加 `Strategy` 字段记录匹配的策略名，便于日志和指标打标。

### Embedder 接口

在 decision 包中定义 `Embedder` 接口（`Embed(texts []string) ([][]float64, error)`），`EmbeddingEngine` 隐式实现此接口。语义路由依赖此接口做 mock 测试。

### 初始化顺序（main.go 必读）

```
load config → init embedding engine → init registry → init balancer
  → init circuit breakers
  → NewSemanticRouter(cfg.SemanticRouting, eng)
  → NewCapabilityRouter(cfg.Routing.Rules)
  → NewCostRouter(cfg.Routing.Cascade)
  → NewRouter(strategies, defaultCascade, balancer, ...)
  → 注入 handler
```

语义路由需要在 Router 之前初始化，因为 `NewSemanticRouter` 中会调用嵌入引擎批量预计算类别原型向量。

---

### 阶段 A：语义路由（strategy_semantic）

关键设计：
- `CategoryPrototype` 结构体：Name、TargetModels、Vector（exemplars 的平均向量经 L2 归一化）。
- `NewSemanticRouter(cfg, eng)`：遍历配置类别，对每个类别的 exemplars 批量 embedding，求平均向量，L2 归一化后保存为原型。
- `Route(req)`：提取最后一条用户消息内容 → embed → 与所有类别原型计算 Cosine 相似度 → 返回超过 threshold 且相似度最高的类别对应的 target_models。
- 边界处理：engine 为 nil 或 categories 为空或消息为空 → 返回 nil（未命中）。

- [x] **步骤 A1-A5**：写测试 → 确认失败 → 实现语义路由 → 验证通过 → 提交 `feat: 添加语义路由策略`

### 阶段 B：能力路由（strategy_capability）

关键设计：
- `CapabilityRouter` 持有 `[]RoutingRuleConfig` 规则列表。
- `Route(req, estimatedTokens)`：遍历规则，按 task_type 匹配和 condition 条件评估（如 `"input_tokens > 100000"`），首个匹配规则返回其 target_models。
- `evaluateCondition` 解析 `"input_tokens <operator> <threshold>"` 格式，支持 `>`、`<`、`>=`、`<=` 四种操作符。

- [x] **步骤 B1-B5**：写测试 → 确认失败 → 实现能力路由 → 验证通过 → 提交 `feat: 添加能力路由策略`

### 阶段 C：成本级联路由（strategy_cost）

关键设计：
- `CostRouter` 持有 `map[string][]string` 级联链配置。
- `CascadeFor(model)`：先查找该 model 的专属链；未找到则回退到 `"default"` 链；都没有则返回 nil。

- [x] **步骤 C1-C5**：写测试 → 确认失败 → 实现成本级联路由 → 验证通过 → 提交 `feat: 添加成本级联路由策略`

### 阶段 D：整合 Router 策略链

关键设计：
- `RouterConfig`：策略名称列表 + 默认级联链。
- `ModelProvidersFunc` / `BuildCandidatesFunc` 回调函数类型，桥接 Registry / Config / Balancer。
- `Router` 结构体聚合 balancer、各策略 router、回调函数。
- `Route(req)`：按策略顺序执行，通过 `resolveModelList` 将候选模型列表解析为具体 Provider，`resolveWithBalancer` 使用 balancer 从候选中选择最优节点。
- 显式路由（`provider/model` 格式）绕过所有策略链，直接返回。
- `estimateInputTokens`：简单按字符数 / 4 估算 token 数。

- [x] **步骤 D1-D5**：写集成测试 → 确认失败 → 实现 Router 策略链编排 → 验证全量测试通过 → 提交 `feat: 实现路由策略链编排`

---

## 任务 14：实现语义缓存

**架构：** 混合模式 — 内存切片为主查询引擎（CPU cache 友好）+ Redis 为可选持久化后端（软依赖，故障不阻塞服务）。向量数据由本地 ONNX 嵌入引擎（`embedding.Instance()`）生成。TTL 分两层：内存层惰性过期 + Redis 层原生 `EXPIRE`。LRU 淘汰使用 O(n) 找最旧 `LastAccess` + swap-remove O(1) 删除。

### CacheStore 接口

定义在 `semantic.go` 中：`Save` / `LoadAll` / `Close`，方便测试时替换为 fake。

### 子任务

**14-1：添加 Redis 依赖与接口定义**
- 依赖：`redis/go-redis/v9@v9.5.1` + `alicebob/miniredis/v2@v2.33.0`

**14-2：缓存核心数据结构与查询存储（TDD）**

关键设计：
- `SemanticCacheConfig`：Enabled、SimilarityThreshold、MaxEntries、TTL。
- `CacheEntry`：Model、Key、Vector、ResponseJSON、StoredAt、LastAccess。
- `SemanticCache`：`sync.RWMutex` 保护 `map[string][]CacheEntry`（按 model 分组），含 embedder 和可选的 cacheStore。
- `New(cfg, embedder, store)`：初始化缓存实例。
- `Lookup(ctx, req)`：提取最后一条 user 消息 → embed → 在对应 model 的 entries 中扫描余弦相似度 → 超过阈值且最高 → 命中。惰性清理过期条目，更新 LastAccess。记录 CacheHitTotal 指标。
- `Store(ctx, req, resp)`：跳过 streaming 请求 → embed 用户消息 → 构造 CacheEntry → 追加到内存切片 → 超限时 LRU 驱逐最旧条目 → 可选写 Redis。
- `removeExpired` / `evictOne` / `compactExpired`：过期清理、LRU 驱逐、批量压缩方法。
- 辅助函数：`concatenateUserMessages`（取最后一条 user 消息）、`hashContent`（SHA256 截取 16 字节 hex）、`EncodeVector` / `DecodeVector`（float64 数组与字节序列互转）。

**14-3：Redis 持久层实现（TDD）**

关键设计：
- `RedisStore` 封装 `*redis.Client`。
- `Save`：Pipeline 模式原子写入 3 个 key — `sc:v:{model}:{key}`（向量字节）、`sc:r:{model}:{key}`（响应 JSON）、`sc:idx:{model}`（ZSET 索引，score 为时间戳）。
- `LoadAll`：ZRANGE 获取模型所有 key → Pipeline 批量 GET 向量和响应 → 解码为 `[]CacheEntry`。

**14-4：启动恢复与边界补齐**

- `LoadFromStore`：从 Redis 恢复内存缓存条目。
- 向量编解码往返测试。

### 集成点契约（后续任务实现）

在 `internal/ingress/handler.go` 中：
- 请求进入时先 `cache.Lookup`，命中则直接返回（CacheHit=true）。
- Provider 调用成功后异步 `cache.Store` 写入。
- 流式请求不缓存（Store 内置跳过逻辑）。

### 性能参考

| 操作 | 10k 条目 | 100k 条目 |
|------|---------|----------|
| Lookup 扫描（512 维余弦） | <2ms | <20ms |
| 淘汰（O(n) 找最旧 + swap-remove） | ~0.1ms | ~1ms |
| 定期压缩（O(n) 全量过滤过期） | ~0.5ms | ~5ms |
| Redis 写入（Pipeline 3 命令） | <1ms | <1ms |
| 内存占用 | ~40MB | ~400MB |

- [x] **步骤**：各子任务按 TDD 流程完成，总计 12+ 个测试全部 PASS → 分步提交

---

## 任务 15：实现 Fallback 引擎

> **设计参考：** `docs/superpowers/specs/2026-05-20-llm-gateway-design.md` 第 5 节"容错与高可用"。

### 四层降级体系

- **L1（同实例重试）**：引擎提供退避策略（指数退避 `base * 2^attempt`），Handler 侧调用 `Send` 时执行重试。
- **L2（跨 Provider 降级）**：由 Handler 层利用 Registry + Balancer 尝试同模型的不同 Provider。
- **L3（跨模型降级）**：引擎提供 `Chain(model)` 返回备选模型列表，Handler 逐项尝试。
- **L4（兜底响应）**：引擎提供 `DefaultResponse()` 构造预设的错误回复。

### 关键设计

- `SendFunc` 函数类型：`func(ctx, providerName, model) (*StandardResponse, error)`，由 Handler 构造闭包注入。
- `Attempt` 结构体：ProviderName、Model、Level（primary / retry / fallback）。
- `Engine`：chains（降级链配置）、retryMax、retryBackoff、defaultMsg。
- `Attempts(providerName, model)`：输出完整尝试计划 — primary + N 次 retry + chain 中每个 model（Level=fallback，ProviderName 为空由调用方解析）。
- `Execute(ctx, providerName, model, sendFn)`：遍历 Attempts 计划，执行 sendFn（L1 retry 含指数退避），遇非可重试错误跳过后续重试直接走 chain；全部失败返回 `DefaultResponse()`（level="fallback_exhausted"，err=nil）。
- `IsRetryable(err)`：按错误码（认证/参数/模型不存在 → false）和错误消息模式匹配（连接拒绝/超时/EOF → true）判断。
- `BackoffDuration(attempt)`：`baseBackoff * 2^attempt` 指数退避。
- Engine 不持有 Registry/Balancer 引用，保持纯逻辑层关注点分离。

**文件：**
- 新建： `internal/fallback/engine.go`
- 新建： `internal/fallback/engine_test.go`

- [x] **步骤 1a-4**：写测试（14 个）→ 确认失败 → 实现引擎 → 验证全量通过 → 提交 `feat: 实现 fallback 四层降级引擎`

---

## 任务 16：实现接入层中间件（RequestID、认证、限流、参数校验、日志）

**中间件链顺序（与设计文档一致）：**

```
Request → RequestID注入 → 请求日志 → API Key认证 → 滑动窗口限流 → 参数校验 → Handler
```

**文件：**
- 新建： `internal/ingress/middleware_requestid.go` + `_test.go`
- 新建： `internal/ingress/middleware_logging.go` + `_test.go`
- 新建： `internal/ingress/middleware_auth.go` + `_test.go`
- 新建： `internal/ingress/middleware_ratelimit.go` + `_test.go`
- 新建： `internal/ingress/middleware_validation.go` + `_test.go`
- 新建： `internal/ingress/middleware_chain_test.go`

---

### 阶段 A：RequestID 中间件

作为中间件链的第一个中间件，为每个请求生成 UUID 作为 request_id，注入 Gin context（`c.Set("request_id", id)`）和响应头 `X-Request-ID`。

- [x] **步骤 A1-A5**：写测试 → 确认失败 → 实现（调用 `observability.NewRequestID()`）→ 验证通过 → 提交

### 阶段 B：日志中间件

记录每个请求的结构化日志，包含 `request_id`（从 context 取）、`method`、`path`、`status`、`latency`、`content_length`。错误响应（status >= 400）使用 `Warn` 级别，正常使用 `Info` 级别。

- [x] **步骤 B1-B5**：写测试 → 确认失败 → 实现（记录 `c.Next()` 前后的时间和状态码）→ 验证通过 → 提交

### 阶段 C：认证中间件

校验 `Authorization: Bearer <key>` 请求头。认证成功后，将 `api_key`、`api_key_name`、`api_key_rate_limit` 注入 context。认证失败返回 401。

在认证通过后，还需校验 `allowed_models`：读取请求体中的 `model` 字段，若不在该 key 的允许列表中（且列表不为 `["*"]`），拒绝请求并返回 403。

关键设计：
- `AuthMiddleware(keys)`：构建 `apiKey -> config` 的 map 用于 O(1) 查找。
- `modelAllowed(allowedModels, c)`：通配符 `"*"` 放行所有；否则读取请求体 JSON 的 `model` 字段，与 allowed list 比对。
- `extractModelFromBody(c)`：`io.ReadAll` + `json.Unmarshal` + `io.NopCloser` 恢复 body，供后续中间件读取。

- [x] **步骤 C1-C5**：写测试（6 个）→ 确认失败 → 实现 → 验证通过 → 提交

### 阶段 D：限流中间件

基于 Redis 实现滑动窗口限流。每个 API Key 独立计数，窗口大小固定为 1 分钟，限额从配置的 `api_keys[].rate_limit` 读取。

算法：使用 Redis `ZSET`，key 格式为 `ratelimit:{api_key}`。每次请求：
1. `ZREMRANGEBYSCORE` 清理过期成员（now - 60s 之前）
2. `ZCARD` 计数当前窗口内成员
3. 超过限额 → 返回 429 Rate Limit Exceeded
4. 未超限额 → `ZADD` 添加成员并 `EXPIRE` 设置 TTL，放行

Redis 不可用时优雅降级，放行请求（不阻塞业务）。限额为 0 或不存在的 key 跳过检查。

- [x] **步骤 D1-D5**：写测试（5 个，使用 miniredis）→ 确认失败 → 实现（Pipeline 原子操作）→ 验证通过 → 提交

### 阶段 E：参数校验中间件

仅对 `POST /v1/chat/completions` 路径生效。校验规则：
- `model` 非空且存在于所有 Provider 的模型列表中
- `messages` 非空数组
- `temperature`（如提供）∈ [0, 2]
- `max_tokens`（如提供）为正整数

校验失败返回 400 Bad Request，附带具体错误描述。非 chat 路径（如 `/health`）直接放行。

- [x] **步骤 E1-E5**：写测试（7 个）→ 确认失败 → 实现（读取 body → 解析 → 校验 → 恢复 body）→ 验证通过 → 提交

### 阶段 F：中间件链集成测试

验证完整中间件链的组装顺序和交互正确性：
- 完整请求流程：RequestID → Logging → Auth → RateLimit → Validation → Handler 全部通过
- 认证失败 → 后续中间件不执行（应返回 401，不是 400）
- `/health` 路径绕过所有中间件（仅受 RequestID + Logging 影响）

- [x] **步骤 F1-F4**：写测试 → 确认失败 → 验证全部通过（23 个测试）→ 提交

---

## 任务 17：实现聊天补全 Handler 完整编排

> 本任务实现 `POST /v1/chat/completions` 的完整请求处理链路，串联路由决策、熔断检查、语义缓存、Fallback 降级、Provider 调用、SSE 流式转发和 Prometheus 指标记录。

**文件：**
- 新建： `internal/ingress/service.go`（`ChatService` 接口与编排实现 + `CircuitBreakerManager`）
- 新建： `internal/ingress/service_test.go`（编排逻辑单元测试，含 mock）
- 新建： `internal/ingress/handler.go`（路由注册 + 非流式 / 流式 Handler）
- 新建： `internal/ingress/handler_test.go`（HTTP 集成测试）
- 修改： `internal/model/request.go`（新增 `StreamChunk.ToJSON()`）

### 请求处理链路

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
    │   │   └─ sendFn 内部调用 provider.Send(ctx, req)（捕获原始请求，仅替换 Model）
    │   ├─ cb.RecordSuccess / RecordFailure
    │   ├─ cache.Store(ctx, req, resp) → 异步写入
    │   ├─ 记录指标（RequestDuration / RequestTotal / TokensTotal / FallbackTotal）
    │   └─ 返回 JSON 响应
    │
    └─ [流式] handleStream(c, svc, req)
        ├─ 设置 SSE 响应头（Content-Type: text/event-stream）
        ├─ router.Route(req) → RouteDecision
        ├─ cb.Allow(...) → 未通过返回 SSE error 并关闭
        ├─ provider.SendStream(ctx, req) → 消费 chunk channel
        ├─ 逐 chunk 写入 c.Writer: data: {...}\n\n, 每次 Flush()
        ├─ cb.RecordSuccess / RecordFailure
        └─ 发送 data: [DONE]
```

### 关键设计

| 决策点 | 方案 |
|--------|------|
| 熔断器管理 | `CircuitBreakerManager` 维护 `map[provider/model] *CircuitBreaker`，double-check 加锁创建 |
| 指标记录位置 | 在 handler 中显式记录，不在 fallback engine 内部（关注点分离） |
| 缓存的流式语义 | `SemanticCache.Store()` 已内置跳过 `req.Stream == true` 的逻辑 |
| `sendFn` 闭包 | 在 handler 中构造，捕获原始 req（只替换 Model 字段），封装熔断检查 → Provider 调用 → 熔断记录 |
| `Fallback.Execute` 兜底 | 全部失败时返回兜底消息（error=nil），非流式返回 200 + 兜底内容 |
| RequestID 穿透 | 从 Gin context 取 `request_id`，透传至 `StandardRequest.RequestID` |

### 接口定义

- `ChatService` 接口：`HandleChatCompletion` / `HandleChatCompletionStream`。
- `Router` / `CircuitBreakerManager` / `SemanticCache` / `FallbackEngine` / `ProviderLookupFunc` — 类型别名或接口，解耦依赖，便于 mock 测试。
- `errorToHTTPStatus(err)` — 将 `model.Error` 错误码映射为 HTTP 状态码。

### 阶段执行

- [x] **阶段 A**：ChatService 接口与服务实现骨架（6 个测试）
- [x] **阶段 B**：CircuitBreakerManager 实现（4 个测试）
- [x] **阶段 C**：HTTP Handler 集成实现（6 个测试：health、metrics、非流式、无 service、错误格式化、熔断打开）
- [x] **阶段 D**：修复 `sendFn` 设计缺陷（捕获原始 req，仅替换 Model 字段）
- [x] **阶段 E**：补充 `StreamChunk.ToJSON()` 方法
- [x] **阶段 F**：import 整理与全量验证 → 提交 `feat: 实现聊天补全 handler 完整编排`

---

## 任务 18：在 main.go 中组装服务

**文件：**
- 修改： `cmd/gateway/main.go`

### 组装顺序

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
| `config.BalancerConfig` | `decision.BalancerConfig` | `WarmupDuration.Seconds()` → `float64` |
| `config.FallbackConfig` | `fallback.NewEngine()` | 直接传递 Chains、RetryMax、RetryBackoff |
| `config.CircuitBreakerConfig` | `ingress.NewCircuitBreakerManager()` | 直接传递 FailureThreshold、Cooldown |
| `config.SemanticRoutingConfig` | `decision.NewSemanticRouter()` | 直接传递 |
| `config.RoutingConfig.Rules` | `decision.NewCapabilityRouter()` | 直接传递 |
| `config.RoutingConfig.Cascade` | `decision.NewCostRouter()` | 直接传递 |

### 闭包函数

- `modelProviders(modelName)`：通过 Registry.ProvidersForModel 按模型名查找所有可用 Provider。
- `buildCandidates(providers, modelName)`：将 Provider 列表转为 Balancer 候选，从配置提取静态权重；HealthScore 和 WarmupFactor 初始为 1.0（后续由健康检查和预热逻辑动态更新）。

### 优雅关闭

监听 SIGINT/SIGTERM → 10s 超时关闭 HTTP server → 关闭 Redis 连接 → 关闭嵌入引擎 → 关闭缓存后端 → 退出。

- [x] **步骤 1-4**：替换 main.go → 验证构建 → 运行全量测试 → 提交 `feat: 组装网关服务完整启动流程`

---

## 任务 19：添加 Dockerfile 和 .dockerignore

**背景：** 本项目依赖 CGO 构建（`onnxruntime_go`），运行时需要 ONNX Runtime 共享库和嵌入模型文件，不可使用 `CGO_ENABLED=0`。

**文件：**
- 新建： `.dockerignore`
- 新建： `Dockerfile`

### 多阶段构建要点

- **Builder 阶段**：基于 `golang:bookworm`，安装 ONNX Runtime 动态库（v1.21.0），`CGO_ENABLED=1` 编译二进制，通过 `ldflags` 注入 VERSION 和 BUILD_TIME。
- **Runtime 阶段**：基于 `debian:bookworm-slim`，安装 ca-certificates、tzdata、curl，复制 ONNX Runtime 库文件并 `ldconfig`，创建非 root 用户 `gateway`。
- 复制 configs 和 `.models/` 目录，设置 `chown` 保证运行时可读。
- 暴露 8080 端口，Healthcheck HTTP 端点 `/health`，STOPSIGNAL SIGTERM，OCI 标签。
- `.dockerignore` 排除 git、docs、日志、IDE 配置等无关文件。

- [x] **步骤 1-4**：创建文件 → 验证构建 → 提交 `feat: 添加 dockerfile 和 .dockerignore`

---

## 任务 20：完善 GitHub Actions CI（加入 lint、竞态检测、ONNX 支持）

> 任务 2 已建立基础 CI（build + test），本任务在其基础上增加 lint 检查、竞态检测（`-race`）、ONNX Runtime 共享库安装和 BGE 模型下载。

**文件：**
- 修改： `.github/workflows/ci.yml`

### CI 升级内容

- 拆分为 `lint` 和 `test` 两个独立 job
- **Lint job**：`gofmt -l .` 格式检查 + `golangci-lint` 静态分析
- **Test job**：添加 Redis services 容器、安装 ONNX Runtime v1.21.0 动态库、从 HuggingFace 下载 BGE 模型 4 个文件、`go build ./...` + `go test -race ./...`
- 不包含 Docker 镜像构建 job

- [x] **步骤 1-4**：更新 CI workflow → 验证 YAML → 确认无 docker job → 提交 `ci: 添加 lint、竞态检测和 onnx 支持`

---

## 任务 21：端到端手动验证

- [ ] **步骤 1：运行全量测试** `go test -race ./...`
- [ ] **步骤 2：启动服务** `GATEWAY_CONFIG=configs/gateway.yaml go run ./cmd/gateway`
- [ ] **步骤 3：健康检查** `curl http://localhost:8080/health` → `{"status":"ok"}`
- [ ] **步骤 4：指标检查** `curl http://localhost:8080/metrics` → Prometheus 格式指标
- [ ] **步骤 5：普通请求认证路径检查** → 返回 200 或上游错误（非 401）
- [ ] **步骤 6：流式请求验证** → SSE 流式响应，以 `data: [DONE]` 结束
- [ ] **步骤 7：认证失败场景验证**（无 Authorization、错误 Key、非 Bearer 前缀 → 全部 401）
- [ ] **步骤 8：提交最终验证修正**（如有代码变更）

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
