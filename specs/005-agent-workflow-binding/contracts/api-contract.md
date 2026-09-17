# API Contract: Agent → Workflow 绑定

**Date**: 2026-09-17 | **权威契约**: [impl_spec_05 §4.2-§4.6](../../docs/changelog/workflow/impl_spec_05_agent_binding.md)（逐字对齐，哨兵文案 = `error.code`，前端直接消费）

本特性**零新路由**——全部经既有 agent 端点字段化完成。

## 请求契约

### POST /api/v1/agents（CreateAgentReq 增量字段）

```jsonc
{
  "name": "客服助手",
  "model_id": 1,
  "workflow_id": 3,        // 新增：可空；snake_case；绑定的工作流
  ...                      // 其余既有字段不变（fallback_model_id / system_prompt / tool_ids / knowledge_base_ids / rag_* 等）
}
```

### PUT /api/v1/agents/{id}（UpdateAgentReq 增量字段，全量语义）

- 携带 `workflow_id: 3` → 绑定 / 重绑
- `workflow_id: null` 或缺省 → **解绑**（PUT 全量提交约定：只发一个字段会把 name 等置零导致 400）

字段规则（两 Req 同款）：

```go
WorkflowID *uint64 `json:"workflow_id" binding:"omitempty,gt=0"`
```

- `0` / 负数 → 400（请求绑定校验拒绝）
- `validateAgent` 签名与规则不变（拍板 C2：与 model_id / tool_ids / knowledge_base_ids 等**不互斥**，叠加语义）

## 响应契约

### AgentSchema 增量字段（GET /agents、GET /agents/{id}、POST、PUT 响应）

```jsonc
{
  "workflow_id": "3",      // 已绑定：字符串化外键（与 id / fallback_model_id 同款）
  "workflow_id": null      // 未绑定（存量 agent 默认态）
}
```

```go
WorkflowID *string `json:"workflow_id"`
```

## 错误契约（本特性新增两条码）

| HTTP | error.code | 哨兵位置 | 触发场景 |
|---|---|---|---|
| 404 | `WORKFLOW_NOT_FOUND` | `agentapi.ErrWorkflowNotFound`（与 `workflowapi.ErrWorkflowNotFound` 同码各持一份，KB 先例） | 绑定的 workflow 不存在（FK 23503 `fk_agents_workflow` 翻译，含并发删除兜底） |
| 409 | `WORKFLOW_IN_USE` | `workflowapi.ErrWorkflowInUse` | `DELETE /workflows/{id}` 被绑定时拦截；先解绑（PUT agents `workflow_id=null`）或删 agent 后可删 |

不变化的语义：workflow 的 PUT 编辑 / 发布 / 停用不受绑定影响（编辑不降级，执行读实时版本——拍板 B2）；draft/disabled 的 workflow 可被绑定（拍板 B）。

## 行为边界（明确不做）

- `POST /workflows/{id}/execute` 路由与执行器（后续 spec）
- chat 会话期消费绑定（后续 spec，E1/E2/E3 递延）
- agent 列表按 workflow 聚合展示名（只回 id，防跨模块 N+1）
