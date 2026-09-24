# Workflow 模块 CRUD 与接口契约（api_contract）

> 状态：**接口契约定稿，未实现**（2026-09-15）；前置数据模型见 [db_model.md](./db_model.md)（三表结构 / 图校验 / 状态机均已定稿）。实现时同步：CLAUDE.md 错误码表新增 `WORKFLOW_NAME_CONFLICT` / `WORKFLOW_NOT_PUBLISHED`（资源清单已含 `/workflows` 与 `/workflows/{id}/execute` 代表路由，无需改）。
> 本文锁定 HTTP 契约（路由 / 请求响应 / 错误）与 service-store 分层约定；execute 的引擎细节（上下文、模板求值、路由、超时、executions 记录）另行执行器 spec，本文只锁其路由、前置检查与错误。
> 〔2026-09-17 追加、**2026-09-18 随 spec 06 冻结**：§5 execute 执行契约（ExecuteWorkflowReq / RunResultSchema / 试运行 / 错误语义 / per-node 进度原则）与 §8 错误码 `WORKFLOW_EXECUTION_FAILED` 均为定稿，执行语义见 [impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md)。〕

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
| GET | `/api/v1/workflows/{id}/runs` | 运行历史列表（游标分页，摘要面） | 200 |
| GET | `/api/v1/workflows/{id}/runs/{runId}` | 运行详情（全字段 + 节点轨迹） | 200 |

> 〔2026-09-24 追加（spec 015）：上表后两行为运行历史只读查询端点（纯读、零迁移、执行引擎与落库零改动），行为明细见 §5「GET runs 运行历史查询」；既有 8 路由零变化。〕

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
    Name         string        `json:"name" binding:"required,max=128"`
    Description  string        `json:"description"`
    Type         WorkflowType  `json:"type"`          // chat / task（spec 08）；Create 必填（Validate oneof），Update 不携带
    InputSchema  []SchemaField `json:"input_schema"`  // 仅 task 型；chat 型携带非空由 service 拒
    OutputSchema []SchemaField `json:"output_schema"` // 仅 task 型
    StartNodeKey string        `json:"start_node_key" binding:"required"`
    Nodes        []NodeReq     `json:"nodes" binding:"required,min=1,max=50"`
    Edges        []EdgeReq     `json:"edges" binding:"required,max=100"` // 纯线性可传 []
}
type NodeReq struct {
    Key    string          `json:"key" binding:"required,max=64"`
    Type   NodeType        `json:"type" binding:"required"` // llm/tool/condition/knowledge_retrieval/api/end/workflow（spec 08 加第七类）
    Name   string          `json:"name" binding:"omitempty,max=128"`
    Config json.RawMessage `json:"config" binding:"required"`
}
type EdgeReq struct {
    SourceNodeKey string  `json:"source_node_key" binding:"required,max=64"`
    TargetNodeKey string  `json:"target_node_key" binding:"required,max=64"`
    Condition     *string `json:"condition" binding:"omitempty,max=128"` // nil = 无条件；指针区分"没传"与"空串"
}
// task 型结构化 I/O 契约字段（spec 08 §4.5 简化形态）
type SchemaField struct {
    Name        string `json:"name"`
    Type        string `json:"type"` // string / number / boolean
    Required    bool   `json:"required"`
    Description string `json:"description"`
}
// sub-workflow 节点密封 config（spec 08；键集 / 存在性 / 分型 / 环 / 链深校验归 service R11）
type WorkflowNodeConfig struct {
    WorkflowID uint64            `json:"workflow_id,string"` // 字符串化弱引用 workflows.id
    Inputs     map[string]string `json:"inputs,omitempty"`   // 子 schema 字段 → 父图 {{var}} 模板映射
}
// Update 专用请求：Type *string 遮蔽嵌入 UpsertReq.Type（浅字段优先）——携带即拒
type UpdateWorkflowReq struct {
    ID   uint64  `json:"-"`
    Type *string `json:"type"` // 携带即拒（spec 08 §4.1：分型不可变，同值 / 异值均拒；换型 = 删了重建）
    UpsertReq
}
```

> 〔2026-09-16 修订（实施 spec 02 时用户拍板）：① NodeReq.Name 与 EdgeReq.Condition 原写 `json:"name,max=128"` / `json:"condition,max=128"` 系笔误——`max=128` 落在 json tag 里会被 encoding/json 当未知选项静默忽略，长度上限不生效，已改为 binding tag（`omitempty,max=128`）。② 下方 WorkflowSummarySchema.ID 原写 `json:"id,string"` 同系笔误——`,string` 选项只用于数字字段，挂在 string 字段上会双重编码（`"id":"\"42\""`），已改为 `json:"id"`，与 platform/schema.BaseSchema 一致。〕
>
> 〔2026-09-16 追加（用户拍板）：节点类型加宽 `api` / `end`——密封 config 新增 `ApiCallConfig{url, method, headers?, body?, timeout_sec?, ssl_verify?}`（直接 HTTP 调用；ssl_verify 默认 false = 跳过证书校验，内网自签场景）与 `EndConfig{output?}`（显式终止，可选）；图校验新增 R9（end 节点不得有出边）；DB CHECK 由迁移 00017 加宽为六值。end **不强制每图必有**——既有图（无出边 = 隐式结束）不受影响。〕

- 请求体**不含 `status`**——状态只能经 publish / disable 动作改变（编辑不降级，db_model 决策 #6）。
- `binding` tag 管字段格式，`UpsertReq.Validate()` 管跨字段图规则（引用 db_model.md §7 十二条，不在此重复）。
- 〔spec 08：`Type` 必填 oneof 只在 `Validate` 管（Create 路径）——Update 走 `UpdateWorkflowReq`，body 携带 `type` 即 400 `VALIDATION_FAILED`（分型不可变，同值 / 异值均拒）；`UpdateWorkflowReq.Type *string` 遮蔽嵌入 `UpsertReq.Type`（encoding/json 浅字段优先），nil = 未携带。`InputSchema` / `OutputSchema` 形态校验（name 非空不重名、type ∈ string/number/boolean）由 `api.ValidateSchemaFields` 管，chat 型携带非空 schema 由 service 拒。〕

响应：

```go
// 摘要（列表用，不带图）
type WorkflowSummarySchema struct {
    ID           string        `json:"id"`
    Name         string        `json:"name"`
    Description  string        `json:"description"`
    Type         string        `json:"type"`          // chat / task（spec 08）
    Status       string        `json:"status"`
    InputSchema  []SchemaField `json:"input_schema"`  // task 型入参契约；null = 未声明
    OutputSchema []SchemaField `json:"output_schema"` // task 型出参契约；null = 未声明
    CreatedAt    time.Time     `json:"created_at"`
    UpdatedAt    time.Time     `json:"updated_at"`
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
      "config": { "model_id": "3", "system_prompt": "你是客服路由分类器，只输出类别码", "prompt": "判断用户意图，只输出 ORDER_QUERY 或 POLICY_QUERY：{{input}}", "temperature": 0 } },
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

> 〔2026-09-22 追加（spec 011，用户批准·加法修订）：llm 节点 config 增可选 `system_prompt`（string，`omitempty`——空串 / 缺省 / null 均合法且行为一致）。非空时执行发 `[system, user]` 两条消息，内容为各自模板 strict 渲染结果（`{{var}}` 与 `{{base.field}}` 一级下钻语义与 `prompt` 完全一致，缺失变量 fail-fast 报 `VALIDATION_FAILED`）；为空保持单 user 消息现状。保存期校验不变（`prompt` 仍必填、`system_prompt` 可选，`Validate` 无新增拒绝路径）；既有图（无该字段）的消息序列、executions 记录形态与保存往返零影响。执行语义细节见 [impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md) callLLM 节。〕
>
> 〔2026-09-24 追加（spec 014，用户批准·加法修订）：llm 节点 config 增可选 `output_schema`（`[]SchemaField`，形态同 task 型 workflow 级 schema 字段，`omitempty`——空数组 / 缺省均合法且行为一致）。非空时执行期两处生效：① `prompt` 模板 strict 渲染成功后在 user 消息末尾自动追加系统固定 JSON 输出指令（固定文案不做模板渲染、不含用户变量；`node_in` 与 `executions` 记录追加后的实际发送文本——记录即实发；`system_prompt` 不注入）；② 模型回复按声明严格校验（语义对齐 task 型 `validateOutputSchema` 家族：回复须为合法 JSON 对象——null / 数组 / 纯文本 / markdown 围栏均拒，required 缺失拒，类型探针不符拒，多余字段宽容），不合规节点失败报 `VALIDATION_FAILED`（错误链带节点 key 前缀与具体字段名）。保存期新增字段集形态校验（name 非空不重名、type 枚举，仅新键新增拒绝路径）；未声明节点的消息序列、记录形态与序列化往返逐字节零影响。〕

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
- 〔spec 08：body 携带 `type` 即 400 `VALIDATION_FAILED`（分型不可变，同值 / 异值均拒、不比对当前值——换型 = 删了重建）；`input_schema` / `output_schema` 可改（仍受形态校验与 chat 型置空强不变量约束）。〕
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

### POST execute（契约已冻结，2026-09-18 随 spec 06）
- 前置：存在且 `status = published`；否则 503 `ErrWorkflowNotPublished`（文案区分 draft / disabled）。
- 加载整图快照（缓存或三查）→ 执行器（spec 06）；非流式 JSON 一次性返回，总时长受 nginx 读超时（300s）约束。
- chat 模块触发工作流执行复用本接口语义（跨模块走 workflow api，不重复建设）。

〔2026-09-17 增补、2026-09-18 随 spec 06 冻结；执行语义见 impl_spec_06〕

**定稿形状**（`workflow/api/schema.go` 增补；命名沿 `GetWorkflowReq` 惯例）：

```go
// ExecuteWorkflowReq 控制台与进程内调用方（chat）共用。
type ExecuteWorkflowReq struct {
    ID             uint64  // 路径参数（handler 绑定）
    Input          string  `json:"input" binding:"required,max=16384"` // 工作流入参 → vars["input"]（O1 拍板：单一 input，终形）
    ConversationID *uint64 // chat 触发时的调用方引用（弱引用落 run 行；HTTP 调用不传）
    MessageID      *uint64
}

// RunResultSchema 一次执行的结果（非流式）。
type RunResultSchema struct {
    RunID      string           `json:"run_id"`      // workflow_runs.id（字符串化）；轨迹写入降级时置空（O7 ④ 拍板：结果照返）
    Status     string           `json:"status"`      // succeeded / failed
    Output     string           `json:"output"`      // 终稿（end.output 渲染或末节点输出）
    DurationMs int              `json:"duration_ms"`
    NodeTrace  []NodeRunSummary `json:"node_trace"`  // 节点轨迹摘要（key/type/status/耗时），明细查轨迹表
}
```

- **试运行（O3 拍板）**：`?trial=true` 放开 draft/disabled 执行（状态机唯一例外，正式路径 503 语义不变）；试运行照常落 runs（`is_trial = true`）与 executions（成本真实发生）。`ExecuteWorkflowReq` 增 `Trial bool`（HTTP 侧 query 绑定，进程内调用方直传）。
- **错误语义（O4 已拍板二分法）**：下游哨兵（`MODEL_NOT_FOUND` / `PROVIDER_BUSY` / `PROVIDER_UNAVAILABLE` / `RATE_LIMITED` …）原样透传，handler 按既有错误表映射；引擎自身错误二分——**图缺陷类**（condition 无命中出边、模板缺失变量兜底、tool 节点未支持）→ `VALIDATION_FAILED` 400；**环境限制类**（api 节点 SSRF 拦截、总时长超限）→ 新哨兵 `workflowapi.ErrWorkflowExecutionFailed`（`WORKFLOW_EXECUTION_FAILED`，500，已进 §8 表）；哨兵本体随实现落 `workflow/api/errors.go` 并同步 CLAUDE.md 错误码表。失败节点定位统一靠错误 message 的 `node <key>:` 前缀。
- **per-node 进度原则（讨论结论）**：未来 chat 侧 per-node 流式走**同步回调推送**——引擎留回调注入点，chat 在 execute 调用栈内收到回调即推 SSE；**禁止轮询轨迹表状态**——chat 本就阻塞在调用上，轮询等于拿 DB 当消息队列，还得为它造 RUNNING 可变态（db_model 决策 #14 已否决）。回调接缝的具体形态（SSE 事件类型、节流）归 chat 触发 spec（spec 05 E1）。

### sub-workflow 嵌套执行语义（spec 08，2026-09-20 落地）

- **执行链**：父图走到 `workflow` 节点 → 逐值渲染 `inputs` 映射（strict，缺失即图缺陷 400）→ 按子 `input_schema` 组装 JSON 文本入参 → `executeChild` 进程内递归执行子图（同 goroutine、共享父请求 ctx——**断连整链取消**；每层自包 5min 超时；深度计数随执行传递，超上限图缺陷 400 带父 node 前缀）→ 子终稿过 output schema 校验后落父变量池（`node_key` 可引、JSON 值可一级下钻）。
- **子 vars 池全新起步**（只含自身 input）：父子仅经 input/output 通信，子图引用父 vars 报缺失（图缺陷 400）。
- **子执行把关**：trial 跟随父（父试运行 → 子放开 draft / disabled）；正式运行子必须 published，否则 `WORKFLOW_NOT_PUBLISHED` 带父 node 前缀。
- **子 run 轨迹**：独立 run 行 + `trigger_source='workflow'` + `parent_run_id`（父收尾成功后批量回填一次窄 UPDATE，失败跳过 trace_id 兜底）；`conversation_id` / `message_id` 与父相同。一次查询按 `parent_run_id` 关联即还原整棵执行树。
- **错误上抛**：二分法原样延伸、带父 node 前缀链（`node a: node b: …` 可读定位）——子图缺陷 → `VALIDATION_FAILED` 400；子环境限制 → `WORKFLOW_EXECUTION_FAILED` 500；下游哨兵透传。父记失败 step、父 run 行 `error_node` 定位到父节点。子终稿 output schema 校验失败 → 图缺陷 400（子作者契约）。
- **Execute 签名不动**（入参仍单一 string）：task 型入参 = 按 input_schema 组装的 JSON 文本；引擎侧检测入参为合法 JSON 对象且声明了 input_schema 时按对象解析入池，否则整串落 input（原行为）。

### GET runs 运行历史查询（spec 015，2026-09-24 落地）

> 〔2026-09-24 追加（spec 015）：两查询端点把 spec 07/08 已落库的 `workflow_runs` / `workflow_node_runs` 暴露给查询面——零迁移、纯只读、执行侧零改动；契约逐字对齐 [specs/015-workflow-run-history/contracts/api.md](../../../specs/015-workflow-run-history/contracts/api.md)。〕

- **列表 `GET /workflows/{id}/runs`**：keyset 游标分页，排序键 `(created_at DESC, id DESC)` 双键（最新在前）；`limit` 缺省 20、归一 ≤0 → 20、>100 → 100（service 层 `page.NewCursor` 归一，D7）；响应 `respond.OKWithCursor` → `meta{limit, has_more, next_cursor}`（`has_more=false` 时 `next_cursor` 为 null）；cursor 不透明（base64 排序键），篡改 / 解码失败 → 400 `VALIDATION_FAILED`（`DecodeCursor` 失败路径，chat conversations 同款）。列表项为摘要面 9 字段（id / status / trigger_source / is_trial / duration_ms / error_node / error_msg / started_at / created_at），**不含 input / output 大文本**（store 显式列清单 `selectRunSummary`，FR-002）；空列表 `data.items == []` 非 null。
- **详情 `GET /workflows/{id}/runs/{runId}`**：全字段 16 项（含 input / output 大文本原样透传——截断标记文本如实返回不加工、conversation_id / message_id / parent_run_id 三可空 id 字符串化）+ `nodes` 节点轨迹按执行序（`seq ASC`）；`runId` 非数字 → 400 `VALIDATION_FAILED`。**404 语义（D3）**：run 不存在与属于其他工作流同判 404 `RUN_NOT_FOUND`（store 双条件 `workflow_id = ? AND id = ?`，不泄露存在性）；空轨迹 `nodes == []` 非 null（D5）。节点级 `error_msg` 落库恒空为既有形态（错误定位走 run 级 `error_msg` + `error_node`）。
- **数据窗口语义（FR-004）**：运行历史受保留期清理约束，旧记录可能已被清理——空列表 / 404 均为正常态非错误。

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
| `WORKFLOW_EXECUTION_FAILED` | 500 | `workflowapi.ErrWorkflowExecutionFailed` | execute 引擎环境限制类错误：api 节点 SSRF 拦截 / 工作流总时长超限（O4 二分法，spec 06 冻结新增；图缺陷类走 `VALIDATION_FAILED` 既有行） |
| `RUN_NOT_FOUND` | 404 | `workflowapi.ErrRunNotFound` | runs 详情：run 不存在或不属于所查工作流（同判 404 不泄露存在性，spec 015 新增） |
| `VALIDATION_FAILED` | 400 | `errs.ErrValidationFailed` | 绑定 / 图校验失败，`details` 带节点 key 定位 |

> execute 错误语义（O4 二分法，随 spec 06 冻结）：下游哨兵（`MODEL_NOT_FOUND` / `PROVIDER_BUSY` / `PROVIDER_UNAVAILABLE` / `RATE_LIMITED` …）透传，映射上表现有行；图缺陷类走 `VALIDATION_FAILED` 既有行。

## 9. 前端对接要点

- `status` 驱动操作位：draft → 「发布」；published → 「停用」+「执行」；disabled → 「发布」。
- execute 按钮仅 published 可用；draft/disabled 点执行收到 503 后提示发布。
- config 对象原样回显，前端一期用 JSON 文本编辑节点配置，不需理解各类型内部结构。
- **runs 游标回传（spec 015）**：`GET /workflows/{id}/runs` 走游标分页（chat conversations 同款约定）——`meta.next_cursor` 原样回传作下一页 `?cursor=`（不透明、不解析）；`has_more=false` 时停止加载更多；点行开详情抽屉按需拉 `GET .../runs/{runId}`（404 `RUN_NOT_FOUND` 抽屉内空态「该运行记录已不存在」、不弹全局 toast）。
- 分页用 `page / page_size / total`（Element Plus 原生适配）；创建 / 更新成功返回的 detail 直接刷新页面数据。
- execute 响应（已冻结，随 spec 06）：`RunResultSchema`（run_id / status / output / duration_ms / node_trace）——`node_trace` 供执行测试页展示各节点耗时与失败定位；run_id 在轨迹写入降级时为空串（O7 ④）。
