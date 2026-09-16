# Workflow 模块 CRUD 与接口契约（api_contract）

> 状态：**接口契约定稿，未实现**（2026-09-15）；前置数据模型见 [db_model.md](./db_model.md)（三表结构 / 图校验 / 状态机均已定稿）。实现时同步：CLAUDE.md 错误码表新增 `WORKFLOW_NAME_CONFLICT` / `WORKFLOW_NOT_PUBLISHED`（资源清单已含 `/workflows` 与 `/workflows/{id}/execute` 代表路由，无需改）。
> 本文锁定 HTTP 契约（路由 / 请求响应 / 错误）与 service-store 分层约定；execute 的引擎细节（上下文、模板求值、路由、超时、executions 记录）另行执行器 spec，本文只锁其路由、前置检查与错误。

## 1. 路由总表

| 方法 | 路径 | 语义 | 成功码 |
|---|---|---|---|
| POST | `/api/v1/workflows` | 创建（整图入参，默认 `draft`） | 201 |
| GET | `/api/v1/workflows` | 列表（偏移分页，摘要不含图） | 200 |
| GET | `/api/v1/workflows/{id}` | 详情（三表组装还原） | 200 |
| PUT | `/api/v1/workflows/{id}` | 整图替换（**不改 status**） | 200 |
| DELETE | `/api/v1/workflows/{id}` | 硬删 + CASCADE | 204 |
| POST | `/api/v1/workflows/{id}/publish` | 状态动作 → `published` | 200 |
| POST | `/api/v1/workflows/{id}/disable` | 状态动作 → `disabled` | 200 |
| POST | `/api/v1/workflows/{id}/execute` | 执行（仅 `published`，非流式 JSON） | 200 |

全部经 auth 中间件（`/api/v1/*` 通用登录门槛）。REST 动词命名：非 CRUD 动作用 `/动词` 子路径（publish / disable / execute），符合接口规范。

## 2. 通用约定

- 信封 `respond.Result`（success / data / error / meta）；成功响应 `Cache-Control: no-store`。
- ID 字符串化（`json:"id,string"`）；时间 RFC 3339 UTC；`node_key` / `source_node_key` 等本就是字符串。
- 列表字段空 → `[]` 不返回 `null`；edge 的 `condition` 为 `null` = 无条件直走。
- 状态枚举字符串：`"draft"` / `"published"` / `"disabled"`（与 DB `text+CHECK` 对齐，不传数字）。
- 分页：**B 模式偏移分页**（`page` / `page_size`，默认 20 上限 100，`total` 精确算）——workflows 是极小静态配置表，属接口规范允许 OFFSET 的例外，且原生适配 Element Plus 分页组件。

## 3. Schema（workflow/api）

请求（POST / PUT **同构**，`UpsertReq`；config 延迟到 service 按 type 分发解析）：

```go
type UpsertReq struct {
    Name         string    `json:"name" binding:"required,max=128"`
    Description  string    `json:"description"`
    StartNodeKey string    `json:"start_node_key" binding:"required"`
    Nodes        []NodeReq `json:"nodes" binding:"required,min=1,max=50"`
    Edges        []EdgeReq `json:"edges" binding:"required,max=100"` // 纯线性可传 []
}
type NodeReq struct {
    Key    string          `json:"key" binding:"required,max=64"`
    Type   NodeType        `json:"type" binding:"required"`
    Name   string          `json:"name" binding:"omitempty,max=128"`
    Config json.RawMessage `json:"config" binding:"required"`
}
type EdgeReq struct {
    SourceNodeKey string  `json:"source_node_key" binding:"required,max=64"`
    TargetNodeKey string  `json:"target_node_key" binding:"required,max=64"`
    Condition     *string `json:"condition" binding:"omitempty,max=128"` // nil = 无条件；指针区分"没传"与"空串"
}
```

> 〔2026-09-16 修订（实施 spec 02 时用户拍板）：① NodeReq.Name 与 EdgeReq.Condition 原写 `json:"name,max=128"` / `json:"condition,max=128"` 系笔误——`max=128` 落在 json tag 里会被 encoding/json 当未知选项静默忽略，长度上限不生效，已改为 binding tag（`omitempty,max=128`）。② 下方 WorkflowSummarySchema.ID 原写 `json:"id,string"` 同系笔误——`,string` 选项只用于数字字段，挂在 string 字段上会双重编码（`"id":"\"42\""`），已改为 `json:"id"`，与 platform/schema.BaseSchema 一致。〕
>
> 〔2026-09-16 追加（用户拍板）：节点类型加宽 `api` / `end`——密封 config 新增 `ApiCallConfig{url, method, headers?, body?, timeout_sec?, ssl_verify?}`（直接 HTTP 调用；ssl_verify 默认 false = 跳过证书校验，内网自签场景）与 `EndConfig{output?}`（显式终止，可选）；图校验新增 R9（end 节点不得有出边）；DB CHECK 由迁移 00017 加宽为六值。end **不强制每图必有**——既有图（无出边 = 隐式结束）不受影响。〕

- 请求体**不含 `status`**——状态只能经 publish / disable 动作改变（编辑不降级，db_model 决策 #6）。
- `binding` tag 管字段格式，`UpsertReq.Validate()` 管跨字段图规则（引用 db_model.md §7 九条，不在此重复）。

响应：

```go
// 摘要（列表用，不带图）
type WorkflowSummarySchema struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Description string `json:"description"`
    Status      string `json:"status"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
// 详情（创建/更新/详情接口返回）
type WorkflowDetailSchema struct {
    WorkflowSummarySchema
    StartNodeKey string       `json:"start_node_key"`
    Nodes        []NodeSchema `json:"nodes"` // make(...,0) 兜底，禁 null
    Edges        []EdgeSchema `json:"edges"`
}
type NodeSchema struct {  // config 原样透传：库里存的就是校验过的 JSON 原文，出参不重新序列化
    Key    string          `json:"key"`
    Type   string          `json:"type"`
    Name   string          `json:"name"`
    Config json.RawMessage `json:"config"`
}
type EdgeSchema struct {
    SourceNodeKey string  `json:"source_node_key"`
    TargetNodeKey string  `json:"target_node_key"`
    Condition     *string `json:"condition"` // null = 无条件
}
```

## 4. 请求 / 响应示例

创建（PUT 同构，含改名语义）：

```jsonc
POST /api/v1/workflows
{
  "name": "智能客服分流",
  "description": "意图识别 → 分支 → 查单 / 通用回复",
  "start_node_key": "classify",
  "nodes": [
    { "key": "classify", "type": "llm", "name": "意图识别",
      "config": { "model_id": "3", "prompt": "判断用户意图，只输出 ORDER_QUERY 或 POLICY_QUERY：{{input}}", "temperature": 0 } },
    { "key": "router", "type": "condition", "name": "意图分流",
      "config": { "expression": "{{classify}} == 'ORDER_QUERY'" } },
    { "key": "order_api", "type": "tool", "name": "查询订单",
      "config": { "tool_id": "12", "args": { "order_id": "{{input.order_id}}" } } },
    { "key": "reply", "type": "llm", "name": "生成回复",
      "config": { "model_id": "3", "prompt": "根据 {{order_api}} 的结果回复用户：{{input}}" } }
  ],
  "edges": [
    { "source_node_key": "classify", "target_node_key": "router" },
    { "source_node_key": "router", "target_node_key": "order_api", "condition": "true" },
    { "source_node_key": "router", "target_node_key": "reply", "condition": "false" },
    { "source_node_key": "order_api", "target_node_key": "reply" }
  ]
}
```

详情响应（201 / 200，结构与创建入参一致，另含服务端字段）：

```jsonc
{
  "success": true,
  "data": {
    "id": "42", "name": "智能客服分流", "description": "意图识别 → 分支 → 查单 / 通用回复",
    "status": "draft", "start_node_key": "classify",
    "created_at": "2026-09-15T08:00:00Z", "updated_at": "2026-09-15T08:00:00Z",
    "nodes": [ /* NodeSchema[]，config 原样 */ ],
    "edges": [ /* EdgeSchema[]，与提交一致 */ ]
  },
  "error": null, "meta": null
}
```

## 5. 各接口行为明细

### POST 创建
- `status` 置 `draft`（服务端定，不看请求体）。
- service：图校验十条（db_model.md §7，2026-09-16 追加 end 禁出边）→ jsonb 内引用存在性经下游 api 校验（`model_id`→provider、`knowledge_base_id`→rag；`tool_id` 推迟到执行器 fail-fast——mcp api 未建且 jsonb 无 FK 兜底，2026-09-15 拍板）→ `Store.Create` 一事务写三表（workflows 1 行 + nodes / edges 各一条多 VALUES INSERT，任一失败整体回滚）。
- 撞 `uq_workflows_name`（PG 23505）→ service 翻译 `ErrWorkflowNameConflict` → 409。
- 成功 201 返回 `WorkflowDetailSchema`（组装回读，round-trip 即校验）。

### GET 列表
- workflows 单表查询，**不 JOIN nodes / edges**；返回摘要 + `meta: {page, page_size, total}`。
- 按 `updated_at DESC, id DESC` 排序（最近编辑在前）。

### GET 详情
- 三查组装：`GetByID` + `ListNodes` + `ListEdges`（同模块自己的表，按序三查比 JOIN 简单，行数 ≤150）。
- 走 Cache-Aside（见 §7）；404 → `ErrWorkflowNotFound`。

### PUT 整图替换
- 入参与创建同构；图校验同创建。
- `Store.ReplaceGraph` 一事务：`UPDATE workflows` + `DELETE nodes WHERE workflow_id` + `DELETE edges WHERE workflow_id` + 批量 INSERT（**先删后插、硬删、不做 diff**——软删会让每次保存积累垃圾行且撞 `uq(workflow_id, node_key)`，且表里没有 deleted_at 列，00013 已全面退役）。
- `status` 不受影响；事务提交后删缓存 key。

### DELETE 硬删
- `DELETE workflows WHERE id`，nodes / edges 由 FK **CASCADE** 同步清理（用户拍板 2026-09-15：硬删路线，对齐迁移 00013 软删退役）。
- 可逆下架 = `disable`（图完整保留、再 publish 即恢复）；误删兜底 = PG 每日备份。
- 当前无表引用 workflows（chat 触发执行是运行时调用，非 FK），无 RESTRICT 顾虑；将来若有引用方落 FK，届时按 `AGENT_IN_USE` 模式加 RESTRICT 挡删。
- 204 无响应体；删缓存 key。

### POST publish / disable（状态动作）
- publish：`draft` / `disabled` → `published`；已 `published` 再调用**幂等成功**（无状态变化）。
- disable：`published` → `disabled`；已 `disabled` 幂等；`draft` 幂等 no-op（本就不可执行，状态保持 draft）。
- 状态迁移是单条 `UPDATE ... WHERE status IN (...)`（`Store.UpdateStatus` 返回是否发生迁移），不做 get-then-set 竞态窗口。
- 两者均删缓存 key（status 在缓存对象里）。

### POST execute（仅锁边界，引擎另有 spec）
- 前置：存在且 `status = published`；否则 503 `ErrWorkflowNotPublished`（文案区分 draft / disabled）。
- 加载整图快照（缓存或三查）→ 执行器（后续 spec）；非流式 JSON 一次性返回，总时长受 nginx 读超时（300s）约束。
- chat 模块触发工作流执行复用本接口语义（跨模块走 workflow api，不重复建设）。

## 6. service / store 分层约定

- handler 薄绑定：`respond.BindJSON`（`UpsertReq` 实现 `Validate()`）→ 调本模块 api 接口 → `errors.Is` 映射（§8 错误表）→ `respond.OK / Fail`；一个绑定函数只调一个接口方法。
- 事务边界归 service（决定"何时需要原子"）；原子单元落地为 store 的整图方法（`Create` / `ReplaceGraph` 内部 `Transaction` 包装）——service 不拼 DML，store 不做业务判断，事务内只操作本模块三张表。

```go
// service.Store —— store 包实现（var _ service.Store = (*Store)(nil) 断言）
type Store interface {
    Create(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error // 一事务三表
    GetByID(ctx context.Context, id uint64) (*Workflow, error)        // 未找到原样上抛 gorm.ErrRecordNotFound
    List(ctx context.Context, offset, limit int) ([]Workflow, int64, error) // 行 + 精确 total
    ReplaceGraph(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error
    Delete(ctx context.Context, id uint64) error // 硬删 workflows，nodes/edges CASCADE
    UpdateStatus(ctx context.Context, id uint64, status string) (bool, error) // 返回是否发生迁移（幂等判定）
    ListNodes(ctx context.Context, workflowID uint64) ([]WorkflowNode, error)
    ListEdges(ctx context.Context, workflowID uint64) ([]WorkflowEdge, error)
}
```

## 7. 缓存（Cache-Aside，对齐 agent 模块）

- key `hify:workflow:{id}`（`redisx.Key` 拼前缀），value 为组装后的 `WorkflowDetailSchema`（含 status），TTL 30min。
- 读：详情 / execute 未命中回源三查并回填。
- 写：PUT / DELETE / publish / disable **事务提交后删 key**（配置与状态都在缓存对象里，状态动作也必须删）。
- 列表不缓存（极小表 + name 唯一可直接查）。

## 8. 错误码汇总

| 码 | HTTP | 哨兵 | 触发 |
|---|---|---|---|
| `WORKFLOW_NOT_FOUND` | 404 | `workflowapi.ErrWorkflowNotFound` | 详情 / 更新 / 删除 / 状态动作 / 执行的目标不存在 |
| `WORKFLOW_NAME_CONFLICT` | 409 | `workflowapi.ErrWorkflowNameConflict` | POST / PUT 撞 `uq_workflows_name`（23505 翻译） |
| `WORKFLOW_NOT_PUBLISHED` | 503 | `workflowapi.ErrWorkflowNotPublished` | execute 时 draft / disabled |
| `VALIDATION_FAILED` | 400 | `errs.ErrValidationFailed` | 绑定 / 图校验失败，`details` 带节点 key 定位 |

## 9. 前端对接要点

- `status` 驱动操作位：draft → 「发布」；published → 「停用」+「执行」；disabled → 「发布」。
- execute 按钮仅 published 可用；draft/disabled 点执行收到 503 后提示发布。
- config 对象原样回显，前端一期用 JSON 文本编辑节点配置，不需理解各类型内部结构。
- 分页用 `page / page_size / total`（Element Plus 原生适配）；创建 / 更新成功返回的 detail 直接刷新页面数据。
