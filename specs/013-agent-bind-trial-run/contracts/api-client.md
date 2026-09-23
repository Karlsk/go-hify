# Contract: API 客户端增量（executeWorkflow / workflow_id）

**Feature**: [spec.md](spec.md) | **Date**: 2026-09-23 | **上游冻结契约**: internal/workflow/api/schema.go L470-563（spec 06）、internal/agent/api/schema.go L31-32/L114-117/L145-146（spec 05）

本篇是纯消费方：以下契约**逐字对齐后端既有实现**，键名 / 语义不自造；实现偏差即违规。

## 1. executeWorkflow（web/src/api/workflow.ts 新增）

```typescript
/**
 * 单次执行工作流（POST /workflows/{id}/execute?trial=true，spec 06 冻结契约）。
 * - 试运行模式：放开 draft / disabled 状态机限制（状态机唯一例外）；run 落库 is_trial=true
 * - 同步返回（非 SSE）；耗时上界 = 后端 overall 5min = nginx read timeout 300s
 * - 请求级失败（404 WORKFLOW_NOT_FOUND / 503 WORKFLOW_NOT_PUBLISHED / 500
 *   WORKFLOW_EXECUTION_FAILED 等）走信封 reject（拦截器 toast）；运行级失败
 *   返回 200 + status:"failed"（错误在 node_trace 尾部 error_msg）
 * - timeout 覆盖为 300s：axios 实例默认 30s 会掐断慢工作流（research D1）
 */
export function executeWorkflow(id: string, input: string) {
  return post<WorkflowRunResult>(`/workflows/${id}/execute?trial=true`, { input }, {
    timeout: 300_000,
  })
}
```

**请求 / 响应形态（逐键）**：

| 面 | 形态 | 冻结来源 |
|----|------|----------|
| 路径 | `POST /api/v1/workflows/{id}/execute`，id = 字符串 | handler 路由 |
| query | `trial=true`（固定携带——本客户端唯一用途是试运行） | handler 从 query 绑定 Trial |
| body | `{ "input": string }`——单一字段，必填，≤16384 字符 | `ExecuteWorkflowReq.Input` `binding:"required,max=16384"` |
| 响应 data | `WorkflowRunResult`：`run_id`(string，轨迹降级时空串) / `status`("succeeded"\|"failed") / `output`(string) / `duration_ms`(number) / `node_trace[]` | `RunResultSchema` |
| node_trace 元素 | `node_key` / `node_type` / `status` / `duration_ms` / `error_msg`（成功节点空串） | `NodeRunSummary` |

**禁止**：body 出现第二键（conversation_id / message_id / trial 是后端服务内字段，HTTP 不传）；键名用驼峰变体（nodeKey / nodeType 等自造形）。

## 2. workflow_id 载荷（web/src/api/agent.ts 增量）

| 面 | 形态 | 冻结来源 |
|----|------|----------|
| 响应（AgentBase） | `workflow_id: string \| null`——bigint 字符串化防精度；null = 未绑定 | `AgentSchema.WorkflowID *string`（L31-32） |
| 请求（AgentSaveData） | `workflow_id?: number`——**数值**；省键 / undefined = 创建不绑定 / PUT 解绑 | `CreateAgentReq/UpdateAgentReq.WorkflowID *uint64` `binding:"omitempty,gt=0"`（无 `,string` tag，同 model_id 踩坑 #8） |
| 提交转换 | `form.workflowId === '' ? undefined : Number(form.workflowId)` | PUT 全量语义（缺省 = 解绑，spec 05） |

**错误契约（既有，消费面验证点）**：绑定目标不存在 → 404 `WORKFLOW_NOT_FOUND`（信封 message「工作流不存在」类文案经拦截器呈现）；表单留页内容不丢（`done(false)` 保持弹窗打开）。

## 3. 绑定下拉数据源（既有接口复用，零新端点）

```text
GET /api/v1/workflows?page=1&page_size=100
  → WorkflowItem[]（已声明 type 字段）
  → 前端 .filter(w => w.type === 'chat')   ← 唯一过滤位（research D5：语义过滤，后端不动）
```

workflows 为偏移分页小表（配置表，页大小上限 100）；超过 100 条 chat 型工作流的场景不存在于本产品规模（20-50 人内部工具），不翻页拼接。
