# Data Model: 工作流 LLM 节点 system_prompt 支持

**Date**: 2026-09-22 | **Spec**: [spec.md](spec.md)

## 实体变更：LLM 节点 config（LLMConfig）

唯一受影响实体。**无表结构变更**（config 为 `workflows.graph_config` jsonb 列内容演进，迁移零新增）。

### 修订后字段集（加法修订，+ 号为本篇新增）

| 字段 | JSON 键 | 类型 | 约束 | 说明 |
|---|---|---|---|---|
| ModelID | `model_id` | uint64（JSON 字符串） | 必填 >0 | models.id；存在性经 provider api 预检 |
| **+ SystemPrompt** | **`system_prompt`** | **string** | **可选（空串=缺省）** | **system 消息模板，`{{var}}` strict 渲染语义与 prompt 一致；有值时先渲染、作为 system 消息先于 user 消息发出** |
| Prompt | `prompt` | string | 必填非空 | user 消息模板，`{{var}}` 渲染（现状不变） |
| Temperature | `temperature` | float64 | 可选 0=跟随模型默认 | 现状不变 |

### 校验规则（Validate，不变 + 明确）

- `model_id` 必填、`prompt` 必填——既有规则零改动。
- `system_prompt` **无**必填/长度/格式校验（纯模板文本，渲染语义由执行期 strict 渲染保证）。
- 序列化：`omitempty`——空串不出键；既有配置往返不引入新键。

### 执行期数据流（修订后）

```text
callLLM(cfg):
  system_prompt 非空 → render(cfg.SystemPrompt) ─┐ 失败 → ErrValidationFailed（含变量名+节点 key）
  render(cfg.Prompt) ────────────────────────────┘
  setNodeIn({prompt, [system_prompt]})           # 有值才带键
  msgs = [ {System, rendered_system}?, {User, rendered_prompt} ]
  client.Generate(ctx, msgs, opts)
  recordExecution(... Input: {prompt, [system_prompt]} ...)  # 有值才带键
```

### 关联记录形态（只读消费，无结构变更）

- `executions.Input`（jsonb）：LLM 节点行为 `{"prompt": "...", "system_prompt": "..."}`（后者有值才出现）。
- `workflow_node_runs.node_in`（jsonb）：同上语义（setNodeIn 摘要）。
- 池变量与节点输出：**不变**——节点输出仍为模型回复文本、key 仍为节点 key，下游 `{{var}}` 引用零变化。

## 状态迁移

无——LLMConfig 为值对象（jsonb 内容），无生命周期。
