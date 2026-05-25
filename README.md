# momu-llmgateway

Go LLM Gateway，统一的 OpenAI 兼容 API 入口，屏蔽底层不同 LLM Provider 的协议差异，提供智能路由、负载均衡、熔断降级、语义缓存和可观测性。

## 特性

- **多 Provider 支持**：OpenAI、Anthropic、DeepSeek、Qwen（DashScope）、智谱 GLM
- **OpenAI 兼容 API**：标准 `POST /v1/chat/completions` 接口，零迁移成本
- **智能路由**：显式路由 / 能力路由 / 语义路由 / 成本级联路由，五层策略链
- **平滑加权负载均衡**：SWRR 调度，含预热、并发/延迟惩罚、滑动窗口健康分
- **熔断降级**：Provider+Model 维度三态熔断 + L1-L4 四层降级（重试→跨Provider→跨模型→兜底）
- **语义缓存**：本地 ONNX 嵌入引擎 + 内存 LRU + Redis 持久化，相似问题命中
- **流量治理**：Bearer API Key 认证 + Redis 滑动窗口限流 + 模型白名单
- **可观测性**：Zap 结构化日志 + Prometheus 指标 + RequestID 全链路追踪
- **热加载**：Viper 配置热加载，`${ENV_VAR}` 环境变量展开，`atomic.Value` 原子快照

## 架构

```
Request → 中间件链（RequestID → Logging → Auth → RateLimit → Validation）
    → 语义缓存查询 → 路由决策 → 熔断检查 → Fallback 降级
    → Provider 调用 → SSE 流式转发 → 指标记录 → 响应
```

采用 Clean Architecture 三层结构：

- **接入层** (`internal/ingress`)：Gin HTTP handlers 和中间件
- **决策层** (`internal/decision`)：路由策略、SWRR 负载均衡、熔断器
- **出口层** (`internal/egress`)：Provider 适配器与 SSE 流式转换

支撑包：`config`（配置）、`model`（数据模型）、`cache`（语义缓存）、`embedding`（ONNX 嵌入引擎）、`fallback`（降级引擎）、`observability`（日志/指标/追踪）

## 快速开始

### 前置条件

- Go 1.21+
- Redis（限流和语义缓存持久化）
- （可选）ONNX Runtime 动态库 + [BGE-small-zh-v1.5](https://huggingface.co/onnx-community/bge-small-zh-v1.5-ONNX) 模型（启用语义路由和语义缓存）

### 从源码运行

```bash
git clone https://github.com/viif/momu-llmgateway.git
cd momu-llmgateway

# 下载嵌入模型（可选）
mkdir -p .models/bge-small-zh-v1.5
# 从 HuggingFace 下载 model.onnx, model.onnx_data, tokenizer.json, tokenizer_config.json

# 设置环境变量
export OPENAI_API_KEY="sk-your-key"
export ANTHROPIC_API_KEY="sk-your-key"
# ...

# 编辑配置
vim configs/gateway.yaml

# 启动
GATEWAY_CONFIG=configs/gateway.yaml go run ./cmd/gateway
```

### Docker

```bash
# 构建（需要嵌入模型文件在 .models/ 目录）
docker build -t momu-llmgateway:latest .

# 运行
docker run -p 8080:8080 \
  -e OPENAI_API_KEY="sk-your-key" \
  -e GATEWAY_CONFIG=/app/configs/gateway.yaml \
  momu-llmgateway:latest
```

## API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/chat/completions` | POST | 聊天补全，兼容 OpenAI API 格式 |
| `/health` | GET | 健康检查 |
| `/metrics` | GET | Prometheus 指标 |

### 请求示例

```bash
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

# 显式指定 Provider
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"Hello"}]}'
```

## 配置

配置文件位于 `configs/gateway.yaml`，`${ENV_VAR}` 占位符在加载时自动展开为环境变量值。支持本地文件热加载（`fsnotify`），通过 `atomic.Value` 提供无锁原子快照读取。

### server

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 120s
```

| 字段 | 说明 |
|------|------|
| `port` | HTTP 监听端口 |
| `read_timeout` | 请求读取超时 |
| `write_timeout` | 响应写入超时（流式请求依赖此项，建议 ≥ 120s） |

### redis

```yaml
redis:
  addr: "localhost:6379"
  password: ""
  db: 0
```

Redis 用于限流中间件的滑动窗口计数和语义缓存的持久化存储。不可用时限流优雅降级放行，缓存回退为纯内存模式。

### auth

```yaml
auth:
  api_keys:
    - key: "sk-xxx"
      name: "default"
      rate_limit: 60
      allowed_models: ["*"]
```

| 字段 | 说明 |
|------|------|
| `key` | API Key 值，请求头 `Authorization: Bearer <key>` |
| `name` | 标识名，注入 context 供下游使用 |
| `rate_limit` | 每分钟最大请求数（Redis 滑动窗口） |
| `allowed_models` | 允许的模型列表，`["*"]` 表示全部 |

### providers

```yaml
providers:
  openai:
    type: "openai"                              # "openai" 或 "anthropic"
    base_url: "https://api.openai.com/v1"
    api_key: "${OPENAI_API_KEY}"                # ${ENV_VAR} 展开
    models: ["gpt-4o", "gpt-4o-mini"]
    weight: 100                                 # 静态基础权重，越大优先调度
    timeout: 60s
  deepseek:
    type: "openai"                              # 复用 OpenAICompatible 适配器
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
  anthropic:
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    api_key: "${ANTHROPIC_API_KEY}"
    models: ["claude-sonnet-4-20250514"]
    weight: 80
    timeout: 60s
```

每个 Provider 配置项：

| 字段 | 说明 |
|------|------|
| `type` | 适配器类型：`"openai"`（OpenAI 兼容）或 `"anthropic"`（Messages API） |
| `base_url` | Provider API 基础地址 |
| `api_key` | API Key，使用 `${ENV_VAR}` 从环境变量注入 |
| `models` | 该 Provider 提供的模型列表 |
| `weight` | 静态基础权重，负载均衡时参与有效权重计算 |
| `timeout` | HTTP 客户端超时 |

### routing

路由策略链按 `strategies` 配置的顺序依次执行，首个命中即返回：

1. **explicit**（显式路由）：请求 model 含 `provider/` 前缀直接路由，绕过策略链
2. **capability**（能力路由）：按 `task_type` 和 `condition` 条件匹配 rules
3. **semantic**（语义路由）：通过本地 ONNX 嵌入引擎匹配用户意图
4. **cost_cascade**（成本级联）：按模型降级链优先使用低成本模型

```yaml
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
```

| 字段 | 说明 |
|------|------|
| `strategies` | 策略执行顺序，可选值：`explicit`、`capability`、`semantic`、`cost_cascade` |
| `rules[].task_type` | 能力路由匹配键，来自请求的 `task_type` 字段 |
| `rules[].condition` | 条件表达式，支持 `input_tokens >/</>=/<= N` |
| `rules[].target_models` | 命中后优先使用的模型列表 |
| `cascade` | 成本级联链，`default` 为所有策略未命中时的兜底链 |
| `cascade.<model>` | 该模型对应 Fallback L3（跨模型降级）的备选列表 |

### semantic_routing

语义路由通过本地 ONNX 嵌入引擎计算用户输入与预定义类别的余弦相似度。每个类别的 `exemplars` 在启动时批量编码并求平均向量作为原型。

```yaml
semantic_routing:
  similarity_threshold: 0.75
  categories:
    - name: "code_generation"
      target_models: ["deepseek-chat", "gpt-4o"]
      exemplars:
        - "Write a Python function that..."
        - "用 Go 实现一个并发安全的缓存"
        - ...
    - name: "creative_writing"
      target_models: ["claude-sonnet-4-20250514", "gpt-4o"]
      exemplars:
        - "Write a story about a robot learning to paint"
        - "写一篇关于春天的散文"
        - ...
    - name: "data_analysis"
      target_models: ["gpt-4o", "deepseek-chat"]
      exemplars:
        - "Analyze this dataset and find trends"
        - "分析这些日志中的错误分布"
        - ...
```

| 字段 | 说明 |
|------|------|
| `similarity_threshold` | 余弦相似度命中阈值（0~1），低于此值降级至下一策略 |
| `categories[].name` | 类别标识名 |
| `categories[].target_models` | 命中后路由的目标模型列表 |
| `categories[].exemplars` | 示例语句，用于计算类别原型向量（建议 3~6 条） |

### semantic_cache

```yaml
semantic_cache:
  enabled: true
  similarity_threshold: 0.95
  ttl: 1h
  max_entries: 10000
  max_prompt_length: 1024
```

| 字段 | 说明 |
|------|------|
| `enabled` | 是否启用语义缓存 |
| `similarity_threshold` | 相似度命中阈值（0~1），建议 > 0.9 以避免错误命中 |
| `ttl` | 缓存条目过期时间，内存层惰性过期 + Redis 层原生 EXPIRE |
| `max_entries` | 每模型最大缓存条目数，超出按 LRU 淘汰 |
| `max_prompt_length` | 用户消息最大字符数（rune），超长不缓存；0 = 不限制 |

仅缓存非流式成功响应。缓存命中时直接短路返回，跳过路由和 Provider 调用。

### fallback

四层降级体系，`Execute` 封装 L1（同实例重试，指数退避）和 L3（跨模型降级）。L2（跨 Provider）和 L4（兜底响应）由引擎内建处理。

```yaml
fallback:
  retry_max: 2
  retry_backoff: "1s"
  chains:
    gpt-4o: ["claude-sonnet-4-20250514", "gpt-4o-mini"]
```

| 字段 | 说明 |
|------|------|
| `retry_max` | L1 同实例最大重试次数 |
| `retry_backoff` | 重试基础退避间隔（指数倍增：1s → 2s → 4s） |
| `chains.<model>` | L3 跨模型降级链，按顺序尝试备选模型 |

### circuit_breaker

Provider+Model 维度独立熔断，三态模型：Closed → Open → Half-Open。

```yaml
circuit_breaker:
  failure_threshold: 5
  window: 10s
  cooldown: 30s
```

| 字段 | 说明 |
|------|------|
| `failure_threshold` | 窗口内失败次数达到此值 → Open |
| `window` | 滑动窗口大小 |
| `cooldown` | Open 状态冷却时间，过后进入 Half-Open 放行探测请求 |

### balancer

平滑动态加权负载均衡（SWRR），有效权重公式：

$$W_{\text{eff}} = W_{\text{base}} \cdot F_{\text{warmup}} \cdot \frac{1}{1 + w_1 \!\cdot\! R_{\text{active}} + w_2 \!\cdot\! L_{p99}} \cdot S_{\text{health}}$$

```yaml
balancer:
  concurrency_penalty_coefficient: 3.0   # w1，越大对并发越敏感
  latency_penalty_coefficient: 2.0       # w2，越大对延迟越敏感
  warmup_enabled: true
  warmup_duration: 30s
  health_window_size: 30s
  health_min_requests: 10
```

| 字段 | 说明 |
|------|------|
| `concurrency_penalty_coefficient` | 并发惩罚系数 w1 |
| `latency_penalty_coefficient` | 延迟惩罚系数 w2 |
| `warmup_enabled` | 是否启用慢启动预热 |
| `warmup_duration` | 预热时长，期间权重从低位线性升至满额 |
| `health_window_size` | 健康分滑动窗口大小 |
| `health_min_requests` | 窗口内最小请求数，不足此数健康分固定为 1.0 |

### embedding

```yaml
embedding:
  onnx_library_path: "/usr/lib/libonnxruntime.so"
  model_path: "./.models/bge-small-zh-v1.5"
```

| 字段 | 说明 |
|------|------|
| `onnx_library_path` | ONNX Runtime 动态库路径 |
| `model_path` | BGE-small-zh-v1.5 模型目录，包含 `model.onnx`、`tokenizer.json`、`tokenizer_config.json` |

嵌入引擎初始化失败时语义路由和语义缓存自动降级，不影响核心聊天补全功能。

## Prometheus 指标

所有指标通过 `GET /metrics` 端点暴露，格式为 Prometheus text 格式。

### LLM 请求指标

| 指标名 | 类型 | 标签 | 说明 |
|--------|------|------|------|
| `llm_request_duration_seconds` | Histogram | `provider`, `model` | LLM 请求耗时分布 |
| `llm_request_total` | Counter | `provider`, `model`, `status` | LLM 请求计数，`status` 取值为 `success` / `error` |
| `llm_tokens_total` | Counter | `provider`, `model`, `direction` | Token 消耗统计，`direction` 取值为 `input` / `output` |

### 韧性指标

| 指标名 | 类型 | 标签 | 说明 |
|--------|------|------|------|
| `llm_fallback_total` | Counter | `level`, `from_model`, `to_model` | 降级触发计数，`level` 为 `L1`~`L4` |
| `llm_circuit_breaker_state` | Gauge | `provider`, `model` | 熔断器状态：0=Closed，1=Open，2=Half-Open |

### 缓存指标

| 指标名 | 类型 | 标签 | 说明 |
|--------|------|------|------|
| `llm_cache_hit_total` | Counter | `model`, `cache_type` | 语义缓存命中计数，`cache_type` 为 `memory` / `redis` |

## 开发

```bash
# 构建
go build ./...
go build ./cmd/gateway

# 测试
go test ./...
go test -race ./...

# 格式检查
test -z "$(gofmt -l .)"
```
