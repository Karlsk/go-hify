# Contract: LLM 节点 config（system_prompt 加法修订）

**Date**: 2026-09-22 | **Spec**: [spec.md](spec.md) | **上位契约**: [data-model.md](../data-model.md)

## 契约性质

`internal/workflow/api/schema.go` 的 `LLMConfig` 是 LLM 节点 config 的唯一权威形态，跨模块消费面 = **REST API**（`POST/PUT /api/v1/workflows` 图配置 jsonb 透传，前端/调用方直接读写）。本篇为加法修订（impl_spec_06 冻结契约之上，用户 2026-09-22 批准）：**只加可选字段，不改不删既有字段与语义**。

## REST 呈现（nodes[].config 在 type=llm 时）

```jsonc
{
  "nodes": [
    {
      "key": "classify",
      "type": "llm",
      "config": {
        "model_id": "3",
        "system_prompt": "你是客服路由分类器，只输出类别码",   // 新增·可选·omitempty
        "prompt": "{{input.question}}",                      // 既有·必填
        "temperature": 0.2                                    // 既有·可选
      }
    }
  ]
}
```

- `system_prompt` 缺省/空串/null 均合法且行为一致：模型收到单条 user 消息（现状）。
- `system_prompt` 非空：模型收到 [system, user] 两条消息，内容为各自模板 strict 渲染结果。
- 模板语义：与 `prompt` 完全一致——`{{var}}` 替换、`{{base.field}}` 一级下钻、缺失变量执行期报错（fail-fast 于模型调用前，错误码 `VALIDATION_FAILED`，message 含变量名与节点 key）。

## Go 契约（内部消费者）

```go
// internal/workflow/api/schema.go —— 加法修订
type LLMConfig struct {
    ModelID     uint64  `json:"model_id,string"`
    SystemPrompt string `json:"system_prompt,omitempty"` // 本篇新增；空串=不发 system 消息
    Prompt      string  `json:"prompt"`
    Temperature float64 `json:"temperature,omitempty"`
}
```

- `Validate()`：签名与既有规则零改动（system_prompt 可选，无新增拒绝路径）。
- 哨兵错误：零新增——渲染失败复用 `errs.ErrValidationFailed`（HTTP 400 `VALIDATION_FAILED`，既有映射）。
- 序列化保证（FR-006）：无 system_prompt 的配置经 POST/PUT 保存再 GET 读回，jsonb 不引入 `system_prompt` 键。

## 兼容性承诺（验收基线）

| 消费方 | 影响 |
|---|---|
| 既有工作流（无 system_prompt） | 零——消息序列、executions/node_in 记录形态、保存往返均逐字节不变 |
| 新工作流（带 system_prompt） | 新行为：[system, user] 消息序；记录多一个键 |
| spec 012 前端 LLM 检查器 | 下游消费者——两输入框回填/提交本字段，JSON 模式直接可编辑 |
| 手测文档基线 | 不受影响（无 system_prompt 的用例行为不变） |
