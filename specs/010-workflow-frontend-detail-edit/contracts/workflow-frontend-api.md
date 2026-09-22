# Contract: 工作流前端 API 消费（010-workflow-frontend-detail-edit）

本篇消费的**既有冻结契约**（workflow spec 01~08 交付，`internal/workflow/api/schema.go` 只读核实）+ `web/src/api/workflow.ts` 导出面增量。后端零改动零重定义；错误提示统一由 request.ts 拦截器弹（页面不重复）。

## 1. 消费的端点（本篇新增消费两个，既有四个沿用 009）

| 端点 | 方法 | 用途 | 响应 |
|---|---|---|---|
| `/workflows/{id}` | GET | 详情页渲染 / 编辑页回填 | `WorkflowDetail`（detail 含图组装） |
| `/workflows/{id}` | PUT | 编辑页保存（**整图替换**） | `WorkflowDetail` |
| `/workflows` | GET / POST | 列表 / 创建（009 沿用；POST 第二步「保存并创建」） | `WorkflowItem` / `WorkflowDetail` |
| `/workflows/{id}` | DELETE | 列表删除（009 沿用） | 204 |
| `/workflows/{id}/publish`、`/disable` | POST | 生命周期（009 沿用） | `WorkflowItem` |

## 2. GET /workflows/{id} —— 响应字段（WorkflowDetailSchema 冻结形态）

```jsonc
{
  "id": "1",
  "name": "客服分流",
  "description": "",              // 后端 string 恒序列化：空串，非 null
  "type": "chat",                 // chat / task（spec 08 分型，不可变）
  "status": "published",          // draft / published / disabled
  "input_schema": null,           // task 型字段数组或 null（未声明；chat 型恒 null）
  "output_schema": null,
  "created_at": "2026-09-20T08:00:00Z",
  "updated_at": "2026-09-21T09:00:00Z",
  "start_node_key": "classify",
  "nodes": [
    { "key": "classify", "type": "llm", "name": "分类节点",
      "config": { "model_id": "1", "prompt": "..." } }   // config 原样透传（未知键含在内）
  ],
  "edges": [
    { "source_node_key": "classify", "target_node_key": "end",
      "condition": null }         // null = 无条件直走（后端 *string）
  ]
}
```

前端要点：

- `nodes[].name` 恒存在、可为 `""`（转换 `detailToGraphConfig` 时省略键）。
- `nodes[].config` 是对象（axios 已 JSON.parse）；`config.model_id` / `config.workflow_id` 为**字符串**（后端 `,string` tag）——全链路保形零转换。
- `nodes[].type` 是后端 7 类全集（画布面板只有 5 类；`tool` / `knowledge_retrieval` 照渲染为默认节点）。

## 3. PUT /workflows/{id} —— 请求体（UpdateWorkflowReq 冻结形态）

```jsonc
{
  "name": "客服分流",             // required，max 128
  "description": "…",             // 可空串
  "start_node_key": "classify",   // required
  "nodes": [ /* NodeReq[]：key/type/name?/config，1-50 个 */ ],
  "edges": [ /* EdgeReq[]：source_node_key/target_node_key/condition?，0-100 个；无条件省略 condition 键 */ ],
  "input_schema":  [ /* SchemaField[]，仅 task 型 */ ],
  "output_schema": [ /* SchemaField[]，仅 task 型 */ ]
}
```

**硬红线：请求体不得出现 `type` 键**——后端 `Type *string` 携带即拒（不比对当前值，同值也拒，HTTP 400）。前端由 `UpdateWorkflowData` 类型（无 type 字段）+ `buildUpdatePayload`（不组装 type）双重保证。

语义冻结：整图替换（后保存者覆盖，无冲突检测）；编辑不降级 status；错误码见 §5。

## 4. SchemaField（task 型 I/O 契约，双向同形）

```jsonc
{ "name": "query", "type": "string", "required": true, "description": "查询词" }
```

- `type` ∈ `string` / `number` / `boolean`（后端 ValidateSchemaFields 强校验）。
- 前端行表单校验 `schemaFieldsError` 对齐三规则：name 非空不重名、type 限三值——**后端规则可判的非法输入 100% 前端拦截**（SC-004）。
- 非法历史数据（老数据 type 越界）照常回填行表单（el-select 显示原值），保存前要求修正（Edge Case）。

## 5. 错误码（既有哨兵，拦截器统一弹）

| code | HTTP | 场景（本篇） |
|---|---|---|
| `WORKFLOW_NOT_FOUND` | 404 | 详情/编辑页访问不存在或已删除的 id（页面 catch 后回列表） |
| `WORKFLOW_NAME_CONFLICT` | 409 | 编辑保存名称与他人冲突（留页内容不丢） |
| `VALIDATION_FAILED` | 400 | 图规则拒（环 / 不可达 / 悬挂边 / config 非法 / Update 携带 type）；`details` 含后端定位文案 |
| `UNAUTHORIZED` / `SESSION_EXPIRED` | 401 | 拦截器跳登录（既有钩子） |

## 6. api/workflow.ts 导出面（本篇增量）

```ts
// 类型（既有零改动 + 新增）
export interface WorkflowDetailNode { key; type; name; config }
export interface WorkflowDetailEdge { source_node_key; target_node_key; condition: string | null }
export interface WorkflowDetail { id; name; description; type; status; input_schema; output_schema;
                                  created_at; updated_at; start_node_key; nodes; edges }
export interface UpdateWorkflowData { name; description; start_node_key; nodes; edges;
                                      input_schema?; output_schema? }   // 无 type

// 方法（既有 5 个零改动 + 新增 2 个）
export function getWorkflowDetail(id: string): Promise<WorkflowDetail>   // GET /workflows/{id}
export function updateWorkflow(id: string, data: UpdateWorkflowData): Promise<WorkflowDetail>
                                                                          // PUT /workflows/{id}
```

约定沿用：URL 路径参数原样字符串；`put` helper 复用 `utils/request.ts`（信封拆包 + 拦截器错误处理与 009 完全一致）。
