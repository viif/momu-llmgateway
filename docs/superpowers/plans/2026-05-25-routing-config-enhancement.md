# 路由配置增强实施方案

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 丰富 `configs/gateway.yaml` 中 `routing` 和 `semantic_routing` 的配置数据，使能力路由规则从 1 条扩展到 3 条、成本级联链从 1 条扩展到 4 条、语义分类从 1 个扩展到 3 个并补充 exemplars。

**Architecture:** 仅改动配置文件 (`configs/gateway.yaml`) 和配置测试 (`internal/config/config_test.go`)。不新增 Go struct 字段，不修改路由策略代码。所有配置段已由现有 `RoutingConfig`、`SemanticRoutingConfig`、`CostRouter` 等结构体和代码支持。

**Tech Stack:** Go 1.21+、Viper (YAML)、testify

---

## 文件结构与职责

- `configs/gateway.yaml` — 已有，修改 `routing.rules`、`routing.cascade`、`semantic_routing.categories` 段
- `internal/config/config_test.go` — 已有，新增 `TestLoadEnrichedRoutingConfig` 测试验证新配置的可解析性

---

### 任务 1：丰富 gateway.yaml 路由配置

**文件：**
- 修改：`configs/gateway.yaml:51-58`（routing 段）
- 修改：`configs/gateway.yaml:59-64`（semantic_routing 段）

- [ ] **步骤 1：替换 routing 段**

将当前 `routing` 段（第 51-58 行）替换为：

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

使用 Edit 工具，`oldString` 精确匹配当前 routing 段内容，`newString` 替换为以上内容。

- [ ] **步骤 2：替换 semantic_routing 段**

将当前 `semantic_routing` 段（第 59-64 行）替换为：

```yaml
semantic_routing:
  similarity_threshold: 0.75
  categories:
    - name: "code_generation"
      target_models: ["deepseek-chat", "gpt-4o"]
      exemplars:
        - "Write a Python function that..."
        - "Generate code to implement..."
        - "帮我写一个 REST API 接口"
        - "用 Go 实现一个并发安全的缓存"
        - "Refactor this function to improve performance"
        - "写一个 SQL 查询来统计每日活跃用户"
    - name: "creative_writing"
      target_models: ["claude-sonnet-4-20250514", "gpt-4o"]
      exemplars:
        - "Write a story about a robot learning to paint"
        - "Create a poem in the style of Li Bai"
        - "写一篇关于春天的散文"
        - "帮我润色这段文案，让它更有感染力"
        - "Draft a warm and professional marketing email"
        - "帮我写一段产品发布会的开场白"
    - name: "data_analysis"
      target_models: ["gpt-4o", "deepseek-chat"]
      exemplars:
        - "Analyze this dataset and find trends"
        - "分析这些日志中的错误分布"
        - "Summarize the key findings from this report"
        - "帮我从这段 JSON 数据中提取统计信息"
        - "Compare these two approaches and recommend one"
```

使用 Edit 工具，`oldString` 精确匹配当前 `semantic_routing` 段内容，`newString` 替换为以上内容。

- [ ] **步骤 3：验证构建**

```bash
go build ./...
```

预期：无错误。

- [ ] **步骤 4：验证现有测试不受影响**

```bash
go test ./internal/config -v
```

预期：已有 2 个测试 PASS。

---

### 任务 2：添加增强路由配置的测试

**文件：**
- 修改：`internal/config/config_test.go`（末尾追加新测试函数）

- [ ] **步骤 1：运行当前测试确认通过**

```bash
go test ./internal/config -v
```

预期：`TestLoadExpandsEnvVars` 和 `TestLoadBalancerConfig` 均 PASS。

- [ ] **步骤 2：追加测试函数**

在 `internal/config/config_test.go` 末尾追加：

```go
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

	// routing rules
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

	// routing cascade
	require.Len(t, cfg.Routing.Cascade, 4)
	require.Equal(t, []string{"deepseek-chat", "gpt-4o-mini", "gpt-4o"}, cfg.Routing.Cascade["default"])
	require.Equal(t, []string{"gpt-4o-mini", "deepseek-chat"}, cfg.Routing.Cascade["gpt-4o"])
	require.Equal(t, []string{"deepseek-chat", "gpt-4o"}, cfg.Routing.Cascade["claude-sonnet-4-20250514"])
	require.Equal(t, []string{"deepseek-chat", "gpt-4o-mini"}, cfg.Routing.Cascade["deepseek-reasoner"])

	// semantic routing
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
```

- [ ] **步骤 3：运行测试确认全部通过**

```bash
go test ./internal/config -run TestLoadEnrichedRoutingConfig -v
```

预期：PASS。

- [ ] **步骤 4：运行全部 config 测试确认无回归**

```bash
go test ./internal/config -v
```

预期：3 个测试全部 PASS。

- [ ] **步骤 5：运行全仓测试**

```bash
go test ./...
```

预期：全部 PASS。

- [ ] **步骤 6：提交**

```bash
git add configs/gateway.yaml internal/config/config_test.go
git commit -m "feat: 丰富路由配置，扩展能力规则、成本级联和语义分类"
```
