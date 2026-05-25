# AGENTS.md

## 项目状态

核心实现已全部完成（21/21 任务）。设计文档和分步实施计划：

- `docs/superpowers/specs/2026-05-20-llm-gateway-design.md` — 产品与架构设计文档
- `docs/superpowers/plans/2026-05-20-llm-gateway.md` — 分任务实施计划（21 个任务）

### 实施进度

- [x] 任务 1：初始化 Go 工程骨架 — `go.mod`、目录结构、最小入口 `cmd/gateway/main.go`
- [x] 任务 2：添加 GitHub Actions CI（基础版本）
- [x] 任务 3：定义核心数据模型 — `internal/model/request.go`、`provider.go`、`errors.go`
- [x] 任务 4：实现配置加载、环境变量展开和原子热更新 — `internal/config/config.go`、`configs/gateway.yaml`
- [x] 任务 5：实现可观测性基础设施 — `internal/observability/logger.go`、`metrics.go`、`tracing.go`
- [x] 任务 6：实现 OpenAI 兼容 Provider 适配器 — `internal/egress/openai.go`、`openai_test.go`
- [x] 任务 7：实现 SSE 流式转换 — （已合并入 `internal/egress/openai.go`）
- [x] 任务 8：实现 Anthropic Provider 适配器（含流式） — `internal/egress/anthropic.go`、`anthropic_test.go`
- [x] 任务 9：实现 Provider 注册表 — `internal/egress/adapter.go`、`adapter_test.go`
- [x] 任务 10：实现熔断器 — `internal/decision/circuitbreaker.go`、`circuitbreaker_test.go`
- [x] 任务 11：实现平滑动态加权负载均衡（SWRR） — `internal/decision/balancer.go`、`balancer_test.go`
- [x] 任务 12：实现本地嵌入引擎 — `internal/embedding/embedding.go`、`engine.go`、`onnx.go`
- [x] 任务 13：实现路由策略链 — `internal/decision/router.go`、`strategy_semantic.go`、`strategy_capability.go`、`strategy_cost.go`
- [x] 任务 14：实现语义缓存 — `internal/cache/semantic.go`、`redis_store.go`
- [x] 任务 15：实现 Fallback 引擎 — `internal/fallback/engine.go`、`engine_test.go`
- [x] 任务 16：实现接入层中间件（RequestID、认证、限流、参数校验、日志） — `internal/ingress/middleware_*.go`
- [x] 任务 17：实现聊天补全 Handler 完整编排 — `internal/ingress/service.go`、`handler.go`（路由决策→熔断检查→语义缓存→Fallback 降级→Provider 调用→SSE 流式→指标记录）
- [x] 任务 18：在 main.go 中组装服务 — `cmd/gateway/main.go`（配置加载→嵌入引擎→Redis→Provider 注册表→负载均衡→熔断器→路由策略→语义缓存→Fallback 引擎→ChatService→Prometheus→HTTP 服务→优雅关闭）
- [x] 任务 19：添加 Dockerfile 和 .dockerignore — 多阶段构建（CGO + ONNX Runtime + 非 root）
- [x] 任务 20：完善 GitHub Actions CI — lint（gofmt + golangci-lint）+ test（-race + ONNX + BGE 模型下载）
- [x] 任务 21：端到端手动验证 — 健康检查、指标、普通/流式请求、认证失败场景

## 常用开发命令

Go module `github.com/viif/momu-llmgateway`，Go 1.21+。

```bash
# 构建
go build ./...
go build ./cmd/gateway

# 测试
go test ./...
go test -race ./...
go test ./internal/model -run TestParseStandardRequest -v

# 格式检查
test -z "$(gofmt -l .)"

# 启动服务
GATEWAY_CONFIG=configs/gateway.yaml go run ./cmd/gateway

# Docker 构建
docker build -t momu-llmgateway:local .
```

### 手动冒烟检查

```bash
curl http://localhost:8080/health
curl http://localhost:8080/metrics
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'
```

## 架构

本项目是一个生产级 Go LLM Gateway，提供统一的 OpenAI 兼容 API 入口，屏蔽不同上游 LLM Provider 的协议差异。

采用 Clean Architecture，核心分为三层 + 支撑包：

1. **接入层**（`internal/ingress`）：Gin HTTP handlers 和 middleware。负责 `POST /v1/chat/completions`、`/health`、`/metrics`、RequestID 注入、Zap 请求日志、Bearer API Key 认证、Redis 滑动窗口限流、请求参数校验，以及请求解析为 `model.StandardRequest`。
2. **决策层**（`internal/decision`）：路由与韧性能力。负责显式 Provider 路由（`provider/model`）、语义路由、能力路由、成本级联路由、默认路由、平滑加权负载均衡（SWRR），以及 Provider+Model 维度三态熔断（Closed → Open → Half-Open）。
3. **出口层**（`internal/egress`）：Provider 适配器。`OpenAICompatible` 是 OpenAI、DeepSeek、Qwen、GLM 的共享适配器；Anthropic 有独立 Messages API 转换（system 消息提升 + `event:`/`data:` 双行 SSE 解析）；所有 SSE 响应统一转换为 OpenAI 风格事件。

支撑包：

- `internal/model`：内部标准请求/响应、流式 chunk、Provider 接口和统一错误码。
- `internal/config`：Viper YAML 加载、`${ENV_VAR}` 展开、本地文件热加载、`atomic.Value` 原子快照。
- `internal/cache`：混合模式语义缓存（内存 LRU + Redis 持久化）；仅缓存非流式成功响应。
- `internal/embedding`：本地 ONNX 嵌入引擎（BGE-small-zh-v1.5），共享用于语义路由和语义缓存。
- `internal/fallback`：四层降级引擎（L1 重试 → L2 跨 Provider → L3 跨模型 → L4 兜底响应）。
- `internal/observability`：Zap 结构化日志、Prometheus 指标（6 种）、RequestID 全链路追踪。

## Provider 模型

| Provider | API 格式 | 适配方式 |
|----------|---------|---------|
| OpenAI | 原生 OpenAI | `OpenAICompatible` 直接转发 |
| DeepSeek | OpenAI 兼容 | `OpenAICompatible`，独立 base URL / API Key |
| Qwen (DashScope) | OpenAI 兼容 | `OpenAICompatible`，独立 base URL / API Key |
| GLM (智谱) | OpenAI 兼容 | `OpenAICompatible`，官方已兼容，直接复用 |
| Anthropic | 独立 Messages API | `Anthropic` 适配器，system 提升 + 独立 SSE 解析 |

Provider API Key 在 `configs/gateway.yaml` 中使用 `${OPENAI_API_KEY}` 等环境变量占位符，配置加载时自动展开。

## CI 与打包

- GitHub Actions workflow 包含 `lint`（gofmt + golangci-lint）和 `test`（-race + ONNX + BGE 模型）两个 job。
- CI 不包含 Docker 镜像构建 job。
- `Dockerfile` 供本地或发布流程手动构建：多阶段构建，CGO 编译，ONNX Runtime 动态库，debian:bookworm-slim，非 root 用户，健康检查。

## Git 提交规范

**必须使用中文书写提交信息。**

格式：`type: 中文描述`（type 用小写英文，描述必须用中文）

常用 type：`feat` / `fix` / `refactor` / `test` / `chore` / `style` / `docs`

## 工作流程

- 每次修改代码或文件后，**暂停等待用户确认**，不自动提交
- 只有在用户明确要求时，才执行 `git add` / `git commit` / `git push` 操作