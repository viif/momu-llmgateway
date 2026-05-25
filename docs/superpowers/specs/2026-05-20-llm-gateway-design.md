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
- **本地嵌入**: ONNX Runtime (`github.com/yalue/onnxruntime_go`)、HuggingFace Tokenizer (`github.com/gomlx/go-huggingface/tokenizers/hftokenizer`)、BGE-small-zh-v1.5
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
│   ├── embedding/
│   │   ├── embedding.go              # 纯函数：CosineSimilarity / NormalizeVector / MeanPooling
│   │   ├── engine.go                 # EmbeddingEngine 类型与单例管理
│   │   └── onnx.go                   # ONNX 嵌入引擎实现（路由与缓存共享）
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
1. **显式路由**：请求指定具体 Provider（model 名前缀如 `openai/gpt-4o`），直接路由。
2. **基于能力的路由**：根据 `task_type` 标签和 `condition` 条件选择模型组。
3. **语义路由**：调用网关内置的轻量级本地 Embedding 模型（ONNX）将用户最新指令向量化，与内存中预定义的 " 任务类别原型向量 " 计算余弦相似度。
	- 命中：相似度超过安全阈值（如 0.75），路由至该类别绑定的最优模型组。
	- 拒识：低于阈值则判定为 " 未知意图 "，自动降级并传递至下一策略，防止路由幻觉。
4. **基于成本的级联路由**：按配置的模型级联链优先使用低成本模型组。
5. **默认路由**：未匹配任何策略时使用默认模型组。

- **组内负载均衡**：各路由策略最终输出的是 " 候选模型组 " 而非单一节点，由底层的平滑加权负载均衡器（Balancer）根据实时健康度与延迟选出最终执行节点。
- **本地化加速**：语义路由的 Embeding 计算采用网关内置的 ONNX 轻量模型（详见 [4.5 本地嵌入引擎](#45-本地嵌入引擎-local-embedding-engine)），避免网络调用延迟，类别原型向量在启动时预计算并缓存在内存中。

### 平滑动态加权负载均衡

同一模型多个 Provider 实例时的权重计算与调度：

动态有效权重计算：

$$
W_{eff} = (W_{base} \times F_{warmup}) \times \left[ \frac{1}{1 + w_1 \cdot R_{active} + w_2 \cdot L_{p99}} \right] \times S_{health}
$$

- $W_{eff}$：动态有效权重 (effective_weight)，最终参与平滑加权轮询（SWRR）调度的实时权重值。该值越高，该 Provider 节点在本轮调度中被选中的概率越大。
- $W_{base}$：配置的静态基础权重 (base_weight)，由运维人员在配置中心预先设定的固定权重，通常用于区分不同 Provider 的硬件算力等级（如 A100 集群与 T4 集群）或业务优先级（如付费账号与免费账号）。
- $F_{warmup}$：慢启动预热系数 \[0, 1] (warmup_factor)，用于保护刚上线或刚从熔断中恢复的节点。取值范围为 \[0, 1]。节点启动初期该值较小（如 0.1），随后随时间线性或指数递增至 1，防止冷节点因初始负载极低而瞬间被流量洪峰击垮。
- $R_{active}$：当前活跃请求数 (active_requests)，该节点当前正在处理且尚未返回的并发请求总数。使用原子操作（Atomic）进行计数，能够实时反映节点的瞬时并发压力。
- $L_{p99}$：归一化的 P99 响应延迟 (normalized_p99_latency)，反映节点处理长文本或复杂推理时的真实耗时压力。取该节点近期（如过去 1 分钟）响应时间的 P99 分位值，并映射到 \[0, 1] 区间。延迟越高，该项对权重的衰减作用越明显。
- $S_{health}$：基于滑动窗口成功率的动态健康分 \[0, 1] (sliding_window_health_score)，基于过去 N 次请求或过去 T 时间窗口内的成功率计算得出的动态分数，取值范围为 \[0, 1]。用于快速感知并规避出现高频 5xx 错误或超时的故障节点。
- $w_1$ (并发惩罚系数) 用于调节 " 活跃请求数 " 对总权重的衰减力度。$w_1$ 越大，负载均衡器对节点的并发数越敏感，流量越倾向于分散到空闲节点。
- $w_2$ (延迟惩罚系数) 用于调节 " 响应延迟 " 对总权重的衰减力度。$w_2$ 越大，负载均衡器越倾向于避开处理速度慢的节点，优先保障整体响应时效。

平滑加权轮询调度机制（Smooth Weighted Round-Robin）：
- 维护动态状态变量： 为每个节点维护一个动态的 current_weight（当前有效权重）状态变量。该变量用于记录节点在连续调度过程中的权重累积情况，是实现流量平滑分配的核心。
- 权重累积阶段： 在每次发起调度请求时，先将所有候选节点的 current_weight 分别累加上其上一轮计算出的 effective_weight（动态有效权重）。这一步确保了权重高的节点，其 current_weight 的增长速度更快。
- 最优节点选取： 遍历所有节点，选取当前 current_weight 值最大的节点作为本次请求的目标节点。若存在多个节点 current_weight 相同且均为最大值，则选取遍历顺序中的第一个。
- 权重扣减与平滑化： 被选中的节点，其 current_weight 需要减去所有候选节点的 total_effective_weight（即所有节点 effective_weight 的总和）。这一步是算法的精髓，它让被选中的节点权重瞬间回落，从而在下一轮调度中让出机会给其他节点，避免了流量的集中突发。
- 周期性收敛： 通过上述 " 累加 - 选取 - 扣减 " 的循环，各个节点的 current_weight 会在一个周期内动态波动。长期来看，每个节点被选中的频率将严格收敛于其 effective_weight 在总权重中的占比，实现了既平滑又精准的流量分配。

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

### Provider 注册表

```go
type Registry struct {
    mu        sync.RWMutex
    providers []model.Provider
    byName    map[string]model.Provider
    byModel   map[string][]model.Provider
}
```

注册表采用**切片 + 双 map + RWMutex** 设计：

- `providers` 切片保留供 `Providers()` 一次性返回全量 Provider 列表
- `byName` map 提供 O(1) 按 Provider 名称查找
- `byModel` map 提供 O(1) 按模型名称查找该模型的所有可用 Provider
- `sync.RWMutex` 保证并发安全：`Register` 持有写锁，所有读方法持有读锁
- 所有返回集合的读方法均返回防御性拷贝，外部修改不影响内部状态

接口：`Register`、`Providers`、`ProvidersForModel`、`ProviderByName`

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

### 架构：混合模式（内存 LRU + Redis 持久化）

```
                 ┌──────────────────────────────────────┐
                 │          SemanticCache               │
                 │                                      │
 request ──────► │  Lookup(ctx, req) → (resp, bool)    │──► cached response (hit)
                 │                                      │
 response ─────► │  Store(ctx, req, resp)               │──► nil (cache miss)
                 │                                      │
                 │  ┌─────────────────────────────────┐ │
                 │  │  内存层 (热路径, 主查询引擎)      │ │
                 │  │  map[model][]CacheEntry          │ │
                 │  │  + 惰性 TTL 过期                 │ │
                 │  │  + O(n) 找最旧 + swap-remove LRU │ │
                 │  └──────────┬──────────────────────┘ │
                 │             │ 持久化写入 + 启动恢复   │
                 │  ┌──────────▼──────────────────────┐ │
                 │  │  Redis 层 (可选, 持久化后端)     │ │
                 │  │  RedisStore (CacheStore 接口)     │ │
                 │  │  + 原生 EXPIRE TTL               │ │
                 │  │  + Pipeline 批量操作             │ │
                 │  └─────────────────────────────────┘ │
                 └──────────────────────────────────────┘
```

**设计理由：** 内存切片在主查询路径上比链表有更好的 CPU cache 亲和性（热路径是余弦扫描而非淘汰）。Redis 作为可选持久化后端，故障不影响缓存服务。

### 内存层实现

1. **数据结构**：`map[model][]CacheEntry` 切片，每个 `CacheEntry` 包含 `Model`、`Key`（用户消息 SHA256 前 16 字节）、`Vector`（512 维 float64）、`ResponseJSON`、`StoredAt`、`LastAccess`。

2. **缓存写入**: 非流式请求成功后，调用本地嵌入引擎生成 Embedding，构造 `CacheEntry` 追加到对应 model 的切片尾部。超出 `maxEntries` 时 O(n) 线性找最旧（按 `LastAccess`）并 swap-remove 淘汰。

3. **缓存查询**: 请求到来时通过本地嵌入引擎生成 Embedding，遍历对应 model 的切片计算余弦相似度。相似度超过阈值（默认 0.95）时命中，更新 `LastAccess` 并上报 `CacheHitTotal` 指标。

4. **TTL 策略**:
   - **惰性过期**：查询遍历时跳过 `time.Since(StoredAt) > ttl` 的条目，收集过期索引并用 swap-remove 清理
   - **定期压缩**：插入时当条目数超过 `maxEntries * 1.5`，触发一次 O(n) 全量过期过滤
   - 不在后台起 goroutine 扫描

5. **LRU 淘汰**: 使用 `LastAccess` 字段追踪访问顺序。溢出时 O(n) 线性找最小 `LastAccess` → swap-remove（O(1) 删除）。不采用 `container/list`，避免热路径余弦扫描的指针跳转开销。

### Redis 持久层实现

通过 `CacheStore` 接口抽象 Redis 操作，方便测试时替换为 fake。

**Key schema：**

| Key 模式 | 类型 | 内容 |
|----------|------|------|
| `sc:v:{model}:{hash}` | String | 向量二进制编码（512 × 8 bytes，小端序 float64） |
| `sc:r:{model}:{hash}` | String | 响应 JSON |
| `sc:idx:{model}` | ZSET | score=timestamp, member=hash，用于遍历恢复 + LRU 辅助 |

- **写入**：Redis Pipeline 批量 SET（向量 + 响应 + ZADD 索引），统一 `EXPIRE` = 配置 TTL
- **启动恢复**：`ZRANGE sc:idx:{model} 0 -1` 获取所有 key → `MGET` 批量读取 → 反序列化到内存切片
- **Redis 写入失败不阻塞服务**，仅静默丢弃
- **Redis 不可用时缓存仍可用**（纯内存模式）

### 缓存策略

- 可配置开关（`semantic_cache.enabled`）
- TTL 可配置（默认 1 小时），内存层惰性过期 + Redis 层原生 EXPIRE
- 仅缓存非流式请求（`req.Stream == true` 时跳过）
- 容量限制 + LRU 淘汰（默认 10000 条）
- 按 model 分区缓存
- 嵌入引擎为 nil 时优雅降级（查/存均为 no-op）
- prompt 超长豁免（`semantic_cache.max_prompt_length`）：当用户消息（最后一条 user role 内容）字符数超过阈值时，跳过缓存查询与写入。超长 prompt 的嵌入相似度噪声大、缓存复用价值低，且嵌入计算开销高。值为 0 或不配置时表示不限制，向后兼容。

### 嵌入引擎集成

- 使用与语义路由相同的本地 ONNX 引擎（`embedding.Instance()`）
- 嵌入内容为最后一条 user role 消息（`concatenateUserMessages`，与 semantic routing 同逻辑）
- 余弦相似度复用 `embedding.CosineSimilarity`

## 4.5 本地嵌入引擎 (Local Embedding Engine)

语义路由和语义缓存的嵌入向量化均使用本地 ONNX 模型，避免网络调用带来的延迟抖动。

### 技术选型

| 组件 | 选型 | 说明 |
|---|---|---|
| 运行时 | `github.com/yalue/onnxruntime_go` | Go 绑定的 ONNX Runtime C API，支持 CPU 推理 |
| Tokenizer | `github.com/gomlx/go-huggingface/tokenizers/hftokenizer` | 纯 Go 实现的 HuggingFace tokenizer，支持 WordPiece/BPE/Unigram，无需 CGO |
| 模型 | BGE-small-zh-v1.5 | 512 维输出向量，中文语义理解优化，模型体积小（~100MB） |
| 模型加载 | 启动时从配置 `embedding.model_path` 指定的路径加载 | 常驻内存，运行时无需重复加载 |

### 架构

```
启动 → EmbeddingEngine 初始化 → 加载 ONNX 模型至内存 → 单例注册
                                                 ↓
          ┌──────────────────────────────────────┴────────────────────────────────────┐
          ↓                                                                              ↓
   strategy_semantic.go                                                          semantic.go
   (语义路由：用户输入向量化)                                                    (语义缓存：请求向量化)
          ↓                                                                              ↓
   与类别原型向量计算余弦相似度                                                  与 Redis 中缓存向量计算余弦相似度
```

- **单例共享**：语义路由和语义缓存共享同一个 `EmbeddingEngine` 实例，避免重复加载模型浪费内存
- **并发安全**：ONNX 推理 session 加锁保护，支持并发调用

### 性能考量

- 初始化耗时 < 2 秒（模型加载），不影响服务就绪后的请求延迟
- 单次推理耗时 < 5ms（BGE-small 512 维向量，CPU 推理）
- 支持输入截断：超过模型最大 token 限制时自动截取尾部

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
  max_prompt_length: 8192  # 字符数（rune）; 0 表示不限制

fallback:
  retry_max: 2
  retry_backoff: "1s"
  chains:
    gpt-4o: ["claude-sonnet-4-20250514", "gpt-4o-mini"]
    claude-sonnet-4-20250514: ["gpt-4o", "deepseek-chat"]
    deepseek-chat: ["gpt-4o-mini", "qwen-turbo"]

balancer:
  concurrency_penalty_coefficient: 3.0   # w1, 越大则对并发越敏感
  latency_penalty_coefficient: 2.0       # w2, 越大则对延迟越敏感
  warmup_enabled: true
  warmup_duration: 30s
  health_window_size: 30s
  health_min_requests: 10

circuit_breaker:
  failure_threshold: 5
  window: 10s
  cooldown: 30s

embedding:
  onnx_library_path: "/usr/lib/libonnxruntime.so"
  model_path: "./.models/bge-small-zh-v1.5"
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
      - run: go test -race ./...
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
