# 路由配置增强设计文档

## 概述

丰富 `configs/gateway.yaml` 中 `routing` 和 `semantic_routing` 两个配置段的数据，使路由策略在生产环境中拥有更完善的规则覆盖和语义分类能力。本次改动不涉及 Go 结构体新增字段，仅利用已有的配置模型填入更丰富的数据，并补充测试验证。

## 范围

- **仅配置 + 测试**：不新增 config struct 字段，不修改路由策略代码逻辑
- 现有代码已支持的所有配置维度均在本次计划中补全

## routing 段增强

### 策略顺序

保持不变：`["explicit", "capability", "semantic", "cost_cascade"]`

### capability rules 扩展

从 1 条规则扩展为 3 条，覆盖不同任务场景：

| task_type | condition | target_models | 场景说明 |
|---|---|---|---|
| `long_context`（保留） | `input_tokens > 100000` | claude-sonnet-4-20250514, deepseek-chat | 超长上下文 |
| `reasoning`（新增） | `input_tokens > 4000` | deepseek-reasoner, gpt-4o | 复杂推理 |
| `fast_response`（新增） | `input_tokens < 500` | gpt-4o-mini, glm-4-flash | 短问答快速响应 |

### cost cascade 扩展

从仅全局 `default` 链扩展为 4 条模型级链：

```yaml
cascade:
  default: ["deepseek-chat", "gpt-4o-mini", "gpt-4o"]
  gpt-4o: ["gpt-4o-mini", "deepseek-chat"]
  claude-sonnet-4-20250514: ["deepseek-chat", "gpt-4o"]
  deepseek-reasoner: ["deepseek-chat", "gpt-4o-mini"]
```

CostRouter 代码已通过 `chains[model]` 支持按模型查找级联链，无需改动。

## semantic_routing 段增强

### 分类从 1 个扩展为 3 个

| 分类名 | target_models | 意图 |
|---|---|---|
| `code_generation`（已有） | deepseek-chat, gpt-4o | 代码编写/重构 |
| `creative_writing`（从 spec 补回） | claude-sonnet-4-20250514, gpt-4o | 创意写作/文案 |
| `data_analysis`（从 spec 补回） | gpt-4o, deepseek-chat | 数据分析/报表/日志处理 |

### exemplars 丰富化

每类从 3 条扩展到 5-7 条，中英混合，覆盖更广泛的实际用户输入模式。更多 exemplars 可提升原型向量（取均值后归一化）的语义代表性，降低语义路由误判率。

## 测试增强

`internal/config/config_test.go` 新增一个测试用例，验证：

1. 3 条 routing rules 正确解析，包括 `task_type`、`condition`、`target_models`
2. 4 条 cascade 链正确解析，按模型查找
3. 3 个 semantic categories 正确解析，包括 `name`、`target_models`、`exemplars`
4. `similarity_threshold` 保持为 0.75

测试采用内联 YAML 配置字符串方式，与 `TestLoadExpandsEnvVars` 风格一致。

## 不改动的部分

- `SemanticRoutingConfig` 结构体：不添加 `Priority`、`ThresholdOverride` 等字段
- `RoutingRuleConfig` 结构体：不添加 `Description` 字段
- `evaluateCondition` 解析器：保持 `input_tokens` 单条件比较语法
- 路由策略实现代码（`router.go`、`strategy_*.go`）：零改动
