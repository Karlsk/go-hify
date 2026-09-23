# Workflow 实现 spec 13：agent 绑定工作流与试运行（前端消费面）

> 状态：**已实施（2026-09-23，随本批提交）**——规划产物 `4d68c63`（`specs/013-agent-bind-trial-run/` 七件套）已提交，实现待提交。本篇为回溯性 changelog，实现事实以代码与 specs/013-agent-bind-trial-run/ 为准。
> 上位契约：[impl_spec_05_agent_binding.md](./impl_spec_05_agent_binding.md)（agent `workflow_id` 绑定：可空携带、PUT 全量解绑语义）、[impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md)（`POST /workflows/{id}/execute?trial=true` 与 RunResultSchema 含 node_trace）、[impl_spec_07_chat_pipeline.md](./impl_spec_07_chat_pipeline.md)（O3 chat 工作流不带历史）。
> 前置依赖：spec 012 首版已同分支提交（`9af8602`/`feee5b8`）；spec 05/06/07 后端契约已交付。
> 迁移假设：**零迁移、零后端改动、零新增第三方依赖**——git 变更禁触 `internal/` 与 `migrations/`。

## 1. 背景

spec 05/06 后端契约早已交付（agent.workflow_id 绑定、execute?trial=true 试运行），但前端两处零消费：agent 表单无绑定入口、workflow 全线无试运行 UI——用户无法把 agent 接到工作流上，也无法在发布前验证工作流行为。本篇补齐消费面，虽触 agent 侧 UI（AgentList.vue），但试运行为 workflow 中心，changelog 归本目录。

## 2. 做什么（范围）

- agent 表单「绑定工作流」下拉：仅列 chat 型工作流（名称 + 既有列表数据源，前端过滤）、「不绑定」清空态、空态提示、编辑回显 `AgentSchema.workflow_id`；提交走 PUT 全量语义（清空即解绑）（FR-001）。
- 试运行双入口：编辑页工具栏 + 详情页操作区，共用 `WorkflowTrialDialog`；编辑页有未保存改动先 ElMessageBox 提示保存（试运行跑已落库版本）；两步式创建第二步无入口（未落库无 id）（FR-002/FR-008）。
- 对话框三态入参：chat 型 = 单文本框必填 ≤16384（模拟用户消息）；task 型有 input_schema = 按字段行渲染、值组装为 JSON 对象文本作 input、必填字段前端拦截；task 型无 schema = 单文本框直传（FR-003/FR-004）。
- 执行与结果：`executeWorkflow(id, input)` → `POST /workflows/{id}/execute?trial=true`；loading 防重复触发；成功展示 output 文本 + node_trace 表（node_key/type/status/duration_ms，执行序）；失败走拦截器错误信封（WORKFLOW_NOT_FOUND / WORKFLOW_NOT_PUBLISHED / WORKFLOW_EXECUTION_FAILED 等文案可读）（FR-004/FR-005）。

## 3. 不做什么（边界）

- 后端零改动：无新路由 / 新字段 / 新迁移 / 新哨兵，只消费既有冻结契约。
- 不做运行历史列表 / 运行详情页（runs 查询 API 未暴露；node_trace 即同步结果）。
- 不改 chat 管道语义（O3 维持不带历史）；不做 agent 绑定后的会话界面改动（spec 07 已交付，会话 UI 已能渲染 workflow 回复）。
- 不做两步式第二步试运行（未落库无 id）、不做流式试运行（execute 是同步 REST 非 SSE）、不做 task 型绑定入口（后端虽不限制，前端语义过滤）。

## 4. 关键决策与保真不变量

| 决策 | 内容 |
|---|---|
| 三项拍板（2026-09-23） | 试运行入口 = 编辑页 + 详情页（两步式第二步不做）；chat 上下文维持不带历史（spec 07 O3 冻结决策不动）；绑定下拉仅 chat 型（前端过滤——chat 型 input = 纯文本消息语义匹配，task 型入参是 JSON 对象、聊天纯文本对不上）。 |
| task 入参组装 | schema 字段行值在提交时组装为 JSON 对象文本，走 execute body 单一 `input` 字段通道——不为 task 型另开契约面。 |
| node_trace 键名 | 以后端 `workflow/api/schema.go` 的 NodeRunSummary 实际 json 键为准（node_key/type/status/duration_ms），不自造。 |
| 脏态提示 | 编辑页试运行前 dirty → ElMessageBox 提示先保存——试运行执行的始终是已落库版本，避免「改了却跑旧图」的认知错位。 |

**保真不变量**：请求载荷对齐后端契约（`workflow_id` 可空字符串、execute body `{input}`、query `trial=true`）；agent 表单既有字段与校验零变化；workflow 列表 / 两步式创建 / 编辑 / 详情 / 双模式编辑器行为零变化。

## 5. 交付物

| 层 | 交付物 |
|---|---|
| api | `web/src/api/agent.ts`（AgentSchema/CreateAgentReq/UpdateAgentReq 补 `workflow_id?: string \| null`）；`web/src/api/workflow.ts`（新增 `executeWorkflow` + RunResult/NodeRunSummary 类型） |
| 视图 | `web/src/views/workflow/WorkflowTrialDialog.vue`（**新组件**：三态入参表单 / loading / output + node_trace 表）；`WorkflowEdit.vue`（工具栏试运行按钮 + 脏态提示）；`WorkflowDetail.vue`（操作区试运行按钮）；`web/src/views/agent/AgentList.vue`（绑定下拉 + 回显 + 解绑） |
| 文档 | `docs/testing/workflow-frontend-manual-test.md` §9 增补（双入口 / 三态入参 / 载荷逐键 / 状态机例外）；`docs/testing/agent-manual-test.md` §13 增补（绑定前端链路）；`specs/013.../tasks.md` 12/12 勾选 |

## 6. 验收门

- 自动化：`cd web && npm run type-check && npm run build` 全绿。
- 人工：manual-test §9 + §13 走查（含 SC-003 载荷 Network 逐键核对）；agent 创建 / 编辑既有字段与校验零回归；workflow 既有页面零回归。
- 待人工项：`make start` 冒烟走两份 manual-test 增补小节（真实绑定 / 解绑 / 试运行链路）。
