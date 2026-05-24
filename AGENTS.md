# AGENTS.md

## 项目状态

仓库已进入实现阶段。设计文档和分步实施计划：

- `docs/superpowers/specs/llm-gateway-design.md` — 产品与架构设计文档
- `docs/superpowers/plans/llm-gateway.md` — 分任务实施计划（20 个任务、按 TDD 逐项实施）

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
- [x] 任务 11：实现加权负载均衡 — `internal/decision/balancer.go`、`balancer_test.go`
- [x] 任务 12：实现本地嵌入引擎 — `internal/embedding/embedding.go`、`engine.go`、`onnx.go`
- [x] 任务 13：实现路由策略链 — `internal/decision/router.go`、`strategy_semantic.go`、`strategy_capability.go`、`strategy_cost.go`
- [ ] 任务 14-21：待依次执行

### 实现约束

- 严格按 plan 文档逐项执行，不偏离或重写计划
- 每个任务遵循 TDD 流程：先写测试 → 确认失败 → 最小实现 → 确认通过
- 每项任务完成后提交，不跨任务混合提交

## 计划中的开发命令

Go module 计划为 `github.com/viif/momu-llmgateway`，目标 Go 版本为 1.21+。

`go.mod` 创建后，常用命令如下：

```bash
# 初始化脚手架阶段添加计划依赖
go mod init github.com/viif/momu-llmgateway
go get github.com/gin-gonic/gin@v1.9.1
go get github.com/spf13/viper@v1.18.2
go get go.uber.org/zap@v1.27.0
go get github.com/prometheus/client_golang@v1.19.0
go get github.com/redis/go-redis/v9@v9.5.1
go get github.com/stretchr/testify@v1.9.0
go get github.com/alicebob/miniredis/v2@v2.33.0

# 构建
go build ./...
go build ./cmd/gateway

# 测试
go test ./...
go test -race ./...
go test ./internal/model -run TestParseStandardRequest -v

# 格式检查
test -z "$(gofmt -l .)"

# 实现后启动服务
GATEWAY_CONFIG=configs/gateway.yaml go run ./cmd/gateway
```

设计文档中的手动冒烟检查：

```bash
curl http://localhost:8080/health
curl http://localhost:8080/metrics
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'
```

## 高层架构

本项目计划实现一个生产级 Go LLM Gateway。它提供统一的 OpenAI 兼容 API 入口，并屏蔽不同上游 LLM Provider 的协议差异。

计划架构采用 Clean Architecture，核心分为三层：

1. **接入层**（`internal/ingress`）：Gin HTTP handlers 和 middleware。负责 `POST /v1/chat/completions`、`/health`、`/metrics`、RequestID 注入、Zap 请求日志、Bearer API Key 认证、Redis 滑动窗口限流、请求参数校验，以及转换为 `model.StandardRequest`。
2. **决策层**（`internal/decision`）：路由与韧性能力。负责显式 Provider 路由（`provider/model`）、语义路由、能力路由、成本级联路由、默认路由、加权负载均衡，以及 Provider+Model 维度熔断。
3. **出口层**（`internal/egress`）：Provider 适配器。`OpenAICompatible` 是 OpenAI、DeepSeek、Qwen 的共享适配器；Anthropic 和 GLM 有独立协议转换；SSE 响应需要转换回 OpenAI 风格事件。

支撑包职责：

- `internal/model`：内部标准请求/响应、流式 chunk、Provider 接口和统一错误码。
- `internal/config`：Viper YAML 加载、`${ENV_VAR}` 展开、本地文件热加载、`atomic.Value` 配置快照。热加载仅针对本地 YAML 文件。
- `internal/cache`：基于 Embedding 和 Redis 的语义缓存；只缓存非流式成功响应。
- `internal/fallback`：L1 同实例重试、L2 跨 Provider 降级、L3 跨模型降级、L4 兜底响应。
- `internal/observability`：Zap 日志、Prometheus 指标和 RequestID 辅助函数。

## Provider 模型

设计文档中支持的 Provider：

- OpenAI：原生 OpenAI 格式
- DeepSeek：OpenAI 兼容格式，独立 base URL 和 API Key
- Qwen：OpenAI 兼容 DashScope endpoint，独立 base URL 和 API Key
- Anthropic：Messages API；system 消息提升为顶层 `system`
- GLM：独立 API 格式，需要消息和 tool-call 转换

Provider API Key 在 `configs/gateway.yaml` 中使用 `${OPENAI_API_KEY}` 等环境变量占位符配置，并在配置加载时展开。

## CI 与打包

计划中的 GitHub Actions workflow 只包含 `lint` 和 `test` jobs。除非先修改 spec/plan，否则不要添加 Docker 镜像构建 job。

`Dockerfile` 只用于本地或后续发布流程的手动镜像构建，与 CI 镜像发布分离。

## Git 提交规范

**重要：必须使用中文书写提交信息。**

提交信息格式：`type: 中文描述`（type 用小写英文，描述必须用中文）

常用 type：
- `feat`：新功能
- `fix`：缺陷修复
- `refactor`：重构（非功能变更、非缺陷修复）
- `test`：测试相关
- `chore`：构建、依赖、CI 等维护性改动
- `style`：代码格式调整（不影响逻辑）
- `docs`：文档变更

## 工作流程

- 每项任务完成后，必须同步更新 AGENTS.md 中的实施进度（将对应的 `[ ]` 改为 `[x]`），确保进度与实际完成情况一致
- 每次修改代码或文件后，**暂停等待用户确认**，不自动提交
- 只有在用户明确要求时，才执行 `git add` / `git commit` / `git push` 等操作