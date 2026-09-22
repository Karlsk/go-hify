# Contract: 工作流前端 ↔ 后端 API（009-workflow-frontend）

**Date**: 2026-09-22 | **Status**: Complete | **Plan**: [plan.md](plan.md)

本篇**纯消费**后端既有契约（workflow spec 01~08 已冻结交付），零改动零重定义。本文记录消费面：调用哪些端点、请求如何组装、错误如何呈现，作为实现期对照单。

## 1. 消费的端点

| 端点 | 方法 | 用途 | 消费方 |
|---|---|---|---|
| `/api/v1/workflows` | GET | 列表（偏移分页 page/page_size，默认 20） | WorkflowList；子工作流下拉（page_size=100 前端过滤 task 型） |
| `/api/v1/workflows` | POST | 创建（请求体见 §2） | WorkflowCreate 提交 |
| `/api/v1/workflows/{id}` | DELETE | 删除（204 无响应体） | WorkflowList 删除操作 |
| `/api/v1/workflows/{id}/publish` | POST | 发布：draft/disabled → published（幂等） | WorkflowList 发布操作 |
| `/api/v1/workflows/{id}/disable` | POST | 停用：published → disabled（幂等） | WorkflowList 停用操作 |
| `/api/v1/providers` | GET | 模型下拉数据源第一步（既有） | CanvasEditor llm 节点 |
| `/api/v1/providers/{id}/models` | GET | 模型下拉数据源第二步（既有，逐 provider） | 同上 |

**不消费**（本篇明确不接）：GET /workflows/{id}（详情回显）、PUT /workflows/{id}（编辑页）、POST /workflows/{id}/execute（执行入口与历史）。

## 2. POST /workflows 请求体组装（FR-011）

```jsonc
{
  "name": "…",                       // 表单
  "description": "…",                 // 表单，可选（空则省略）
  "type": "chat",                     // 表单，chat|task 必填
  "start_node_key": "llm_1",          // 图配置
  "nodes": [                          // 图配置；config 外键数值化见下
    { "key": "llm_1", "type": "llm", "name": "分类", "config": { "model_id": 1, "prompt": "…" } }
  ],
  "edges": [
    { "source_node_key": "llm_1", "target_node_key": "end_1", "condition": "ORDER_QUERY" }
  ],
  "input_schema":  [ … ],             // 仅 task 型；chat 型整体不带该键
  "output_schema": [ … ]
}
```

组装规则：

- 图配置（start_node_key/nodes/edges[/schema]）与表单字段（name/description/type）合并——双模式共享单一图配置数据源（FR-007），JSON 模式不含 type/name/description。
- **外键数值化**：组装时对每个 node 的 `config.model_id` / `config.workflow_id` 字符串 → `Number()`（后端 uint64 无 `,string` tag，字符串 400）。编辑态/JSON 态保持字符串（与后端响应一致）。
- schema 键：task 型携带（可为空数组或省略）；chat 型**整体不带**（强不变量，带上非空即 400）。

## 3. 响应消费（既有信封）

- 全部走 `web/src/api/request.ts` 既有封装（信封拆包、错误拦截统一 toast——业务代码不重复弹错）。
- 列表：`getList<WorkflowItem>` 偏移分页（meta.page/page_size/total）。
- ID：路径参数用响应的字符串 id 原样回传（`/workflows/${id}/publish`）。
- 时间：RFC 3339 UTC 字符串，列表直接展示。

## 4. 错误码消费（既有哨兵，零新增）

| code | HTTP | 触发场景 | 前端行为 |
|---|---|---|---|
| WORKFLOW_NAME_CONFLICT | 409 | 创建名称重复 | 拦截器弹错；留在创建页（编辑内容不丢） |
| WORKFLOW_IN_USE | 409 | 删除被 Agent 绑定 | 拦截器弹错；列表行保留 |
| WORKFLOW_NOT_FOUND | 404 | 删除/发布/停用时目标已不存在 | 拦截器弹错；刷新表格 |
| VALIDATION_FAILED | 400 | type 缺失、图校验失败（含 chat 型带 schema） | 拦截器弹错；前端已先做同规则预检减少触达 |
| RATE_LIMITED / BUDGET_EXHAUSTED / 其他 | — | 通用 | 拦截器统一处理 |

规则：错误提示一律由 request.ts 拦截器承担（既有约定「callers never re-toast」）；页面只负责成功后的刷新/跳转。

## 5. api/workflow.ts 导出面（前端内部契约）

```ts
// 类型（data-model §1）
export type WorkflowStatus = 'draft' | 'published' | 'disabled'
export type WorkflowType = 'chat' | 'task'
export interface WorkflowItem { id: string; name: string; description: string | null; type: WorkflowType; status: WorkflowStatus; created_at: string }
export interface SchemaField { name: string; type: 'string' | 'number' | 'boolean'; required: boolean; description?: string }
export interface CreateWorkflowData { name: string; description?: string; type: WorkflowType; start_node_key: string; nodes: …; edges: …; input_schema?: SchemaField[]; output_schema?: SchemaField[] }

// 请求方法（对齐既有 api/ 模块风格）
export function getWorkflowList(params: PageParams): Promise<PageResult<WorkflowItem>>   // 偏移分页
export function createWorkflow(data: CreateWorkflowData): Promise<WorkflowItem>          // 201
export function deleteWorkflow(id: string): Promise<void>                                // 204
export function publishWorkflow(id: string): Promise<WorkflowItem>                        // publish 后实体
export function disableWorkflow(id: string): Promise<WorkflowItem>                        // disable 后实体
```

（具体签名以实现时既有 `api/agent.ts` / `api/provider.ts` 风格为准微调——`PageParams`/`PageResult` 复用 request.ts 既有类型，不自造平行类型。）

## 6. 路由契约（前端内部）

| 路径 | 组件 | meta.title | 守卫 |
|---|---|---|---|
| `/workflows` | views/workflow/WorkflowList.vue | 工作流管理 | 既有登录守卫 |
| `/workflows/create` | views/workflow/WorkflowCreate.vue | 新建工作流 | 同上 |

App.vue 侧边栏菜单新增「工作流管理」入口（default-active = route.path 既有机制自动高亮）。
