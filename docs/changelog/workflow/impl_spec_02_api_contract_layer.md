# Workflow 实现 spec 02：api 契约层

> 状态：**待实施**（2026-09-15 定稿；供 rdp-implementation 以 TDD 消费）。
> 上位契约：[db_model.md](./db_model.md) §8（类型安全解析设计）、[api_contract.md](./api_contract.md) §1/§3（路由与 Schema）。冲突时停下来问用户。
> 前置依赖：无硬依赖（不 import service 层）；建议在 spec 01 后实施保持篇序。
> 规范引用（实施逐条对照）：CLAUDE.md《接口规范》——字段命名与类型（ID 字符串化 / snake_case / 枚举字符串 / 时间 RFC 3339）、空值约定（列表 `[]` 不 `null`）、错误处理（哨兵 Error() = error.code）；《代码组织规范》——api/ 叶子包规则（只 import 标准库，无 gin / gorm，`binding` 是纯字符串 tag）。

## 1. 交付物

| # | 交付物 | 路径 |
|---|---|---|
| 1 | 跨模块调用接口 `WorkflowService`（7 方法） | `internal/workflow/api/api.go`（替换占位） |
| 2 | 常量 / 密封接口 / 四类 config / ParseNodeConfig / Req / Schema / 图校验 Validate | `internal/workflow/api/schema.go`（替换占位） |
| 3 | 补两个哨兵 | `internal/workflow/api/errors.go`（`ErrWorkflowNotFound` 已存在，保留） |

**范围红线**：`WorkflowService` 本期**不含 Execute**（执行引擎后续 spec，届时扩接口 + handler + 装配，不回头改本篇）。

## 2. 冻结契约

### 2.1 api/api.go

```go
type WorkflowService interface {
    Create(ctx context.Context, req UpsertReq) (*WorkflowDetailSchema, error)
    Get(ctx context.Context, req GetWorkflowReq) (*WorkflowDetailSchema, error)
    List(ctx context.Context, req ListWorkflowsReq) (*WorkflowListResult, error)
    Update(ctx context.Context, req UpdateWorkflowReq) (*WorkflowDetailSchema, error)
    Delete(ctx context.Context, req DeleteWorkflowReq) error
    Publish(ctx context.Context, req PublishWorkflowReq) (*WorkflowSummarySchema, error)
    Disable(ctx context.Context, req DisableWorkflowReq) (*WorkflowSummarySchema, error)
}
```

### 2.2 api/errors.go（补两个；哨兵文本 = error.code，对齐既有 `ErrWorkflowNotFound` 风格）

```go
// ErrWorkflowNameConflict 名称冲突（409，uq_workflows_name 23505 翻译）。
var ErrWorkflowNameConflict = errors.New("WORKFLOW_NAME_CONFLICT")
// ErrWorkflowNotPublished 未发布态不可执行（503；execute 时 draft/disabled 被拒）。
var ErrWorkflowNotPublished = errors.New("WORKFLOW_NOT_PUBLISHED")
```

### 2.3 api/schema.go

```go
// ── 状态与节点类型常量（与 DB CHECK 一一对应，加值 = 迁移 + 此处同步）──
type WorkflowStatus string
const (
    StatusDraft     WorkflowStatus = "draft"
    StatusPublished WorkflowStatus = "published"
    StatusDisabled  WorkflowStatus = "disabled"
)
type NodeType string
const (
    NodeLLM                NodeType = "llm"
    NodeTool               NodeType = "tool"
    NodeCondition          NodeType = "condition"
    NodeKnowledgeRetrieval NodeType = "knowledge_retrieval"
)

// ── NodeConfig 密封接口：实现集封闭本包，引擎 type switch 穷举（db_model §8）──
type NodeConfig interface{ isNodeConfig() }

type LLMConfig struct {
    ModelID     uint64  `json:"model_id,string"` // models.id；存在性 service 经 provider api 预检
    Prompt      string  `json:"prompt"`          // 支持 {{var}} 模板
    Temperature float64 `json:"temperature,omitempty"` // 0 = 跟随模型默认
}
func (LLMConfig) isNodeConfig()                {}
type ToolConfig struct {
    ToolID uint64            `json:"tool_id,string"` // mcp_tools.id；存在性推迟执行器（2026-09-15 拍板）
    Args   map[string]string `json:"args,omitempty"` // 值支持 {{var}} 模板
}
func (ToolConfig) isNodeConfig()              {}
type ConditionConfig struct{ Expression string `json:"expression"` } // 如 {{classify}} == 'ORDER_QUERY'
func (ConditionConfig) isNodeConfig()         {}
type KnowledgeRetrievalConfig struct {
    KnowledgeBaseID uint64 `json:"knowledge_base_id,string"`
    TopK            int    `json:"top_k,omitempty"` // 0 = 跟随 Agent/KB 默认；1-20
}
func (KnowledgeRetrievalConfig) isNodeConfig() {}

// ── 分发解析：保存与加载共用唯一入口；未知类型进不了库 ──
func ParseNodeConfig(t NodeType, raw json.RawMessage) (NodeConfig, error)
// 每个 config 实现 Validate() error（必填与界：ModelID/Prompt/Expression/KnowledgeBaseID 非零非空；
// TopK 为 0 或 1-20）。ErrInvalidNodeConfig 为包内私有错误变量，错误文案带节点类型。

// ── 请求（api_contract §3 冻结；config 延迟解析）──
type UpsertReq struct {
    Name         string    `json:"name" binding:"required,max=128"`
    Description  string    `json:"description"`
    StartNodeKey string    `json:"start_node_key" binding:"required"`
    Nodes        []NodeReq `json:"nodes" binding:"required,min=1,max=50"`
    Edges        []EdgeReq `json:"edges" binding:"required,max=100"`
}
type NodeReq struct {
    Key    string          `json:"key" binding:"required,max=64"`
    Type   NodeType        `json:"type" binding:"required"`
    Name   string          `json:"name,max=128"`
    Config json.RawMessage `json:"config" binding:"required"`
}
type EdgeReq struct {
    SourceNodeKey string  `json:"source_node_key" binding:"required,max=64"`
    TargetNodeKey string  `json:"target_node_key" binding:"required,max=64"`
    Condition     *string `json:"condition,max=128"` // nil = 无条件
}

// UpdateWorkflowReq：ID 由 handler BindUri 后赋值（provider UpdateModelReq 同款，body 不含 id）。
type UpdateWorkflowReq struct {
    ID uint64 `json:"-"`
    UpsertReq
}
type GetWorkflowReq struct {
    ID uint64 `uri:"id" binding:"required"`
}
type DeleteWorkflowReq struct {
    ID uint64 `uri:"id" binding:"required"`
}
type PublishWorkflowReq struct {
    ID uint64 `uri:"id" binding:"required"`
}
type DisableWorkflowReq struct {
    ID uint64 `uri:"id" binding:"required"`
}
type ListWorkflowsReq struct {
    Page     int `form:"page"`
    PageSize int `form:"page_size"`
}
// 各 Req 的 Validate()：Get/Delete/Publish/Disable/List 返回 nil（service 归一化分页参数）。

// UpsertReq.Validate 实现 §3.2 的 R1-R8 纯图规则（binding tag 已管字段格式与数量界，不重复）。

// ── 响应 Schema（api_contract §3 冻结）──
type WorkflowSummarySchema struct {
    ID          string    `json:"id,string"`
    Name        string    `json:"name"`
    Description string    `json:"description"`
    Status      string    `json:"status"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
type WorkflowDetailSchema struct {
    WorkflowSummarySchema
    StartNodeKey string       `json:"start_node_key"`
    Nodes        []NodeSchema `json:"nodes"`
    Edges        []EdgeSchema `json:"edges"`
}
type NodeSchema struct { // config 原样透传（库里即校验过的原文）
    Key    string          `json:"key"`
    Type   string          `json:"type"`
    Name   string          `json:"name"`
    Config json.RawMessage `json:"config"`
}
type EdgeSchema struct {
    SourceNodeKey string  `json:"source_node_key"`
    TargetNodeKey string  `json:"target_node_key"`
    Condition     *string `json:"condition"`
}
type WorkflowListResult struct {
    Items    []WorkflowSummarySchema
    Page     int
    PageSize int
    Total    int64
}
```

## 3. 行为语义

### 3.1 ParseNodeConfig

- switch `NodeType` 分发到四个 config 之一，`json.Unmarshal` 后调 `Validate()`；未知 type / 坏 JSON / 校验失败统一返回错误，**文案带类型与原因**（供上层拼节点 key）。
- 这是 config 的唯一强校验入口：保存路径（UpsertReq.Validate）与执行器加载路径共用（db_model §8）。

### 3.2 UpsertReq.Validate —— db_model §7 图校验的纯函数子集

| 规则 | 内容 | db_model §7 对应 |
|---|---|---|
| R1 | 节点 key 请求内唯一 | （uq 的前置早暴露） |
| R2 | 逐节点 config 经 ParseNodeConfig 强校验，错误包装出节点 key | 条 1 |
| R3 | start_node_key 在节点集合内 | 条 2 |
| R4 | 每条边 source / target 均指向存在节点 | 条 3 |
| R5 | condition 节点出边 condition 必填；其余节点出边必须 nil | 条 4 |
| R6 | 非 condition 节点出边 ≤ 1 | 条 5 |
| R7 | 无环：从 start 沿边遍历，重访即拒绝 | 条 6 |
| R8 | 无不可达节点 | 条 7 |

数量界（§7 条 8）由 binding tag 管（min=1,max=50 / max=100）；引用存在性（§7 条 9）归 service（spec 04）——**三层各管一段，不重复校验**。

### 3.3 空值与序列化

- `Nodes` / `Edges` 构造时 `make(..., 0)` 兜底，禁 `null`（《接口规范》空值约定）。
- ID 字符串化（`json:"id,string"`）；时间 `time.Time` 默认 RFC 3339。
- `WorkflowDetailSchema` 满足「详情结构与创建入参一致」（api_contract §4 round-trip）。

## 4. 任务清单与实施顺序（TDD 粒度）

1. 常量 + 哨兵（RED：引用未定义）→ GREEN；
2. 四 config + Validate（表驱动 RED）→ GREEN；
3. ParseNodeConfig（表驱动 RED）→ GREEN；
4. Req/Schema/ListResult 定义 + 各 Validate（RED）→ GREEN；
5. UpsertReq.Validate R1-R8 逐规则用例（RED）→ GREEN → REFACTOR（图遍历辅助函数去重）。

每步门禁：`go build ./... && go vet ./... && go test ./... -race -count=1`。

## 5. 测试清单（表驱动，同包 `schema_test.go`）

- ParseNodeConfig：4 类型 happy path；未知 type；坏 JSON；各 config Validate 失败（缺 model_id / prompt / expression / knowledge_base_id；TopK 越界 21 与负数）。
- UpsertReq.Validate：R1 重复 key；R2 坏 config 且错误文案含节点 key；R3 start 不存在；R4 悬挂边（source 与 target 各一例）；R5 condition 出边缺 condition / 非 condition 出边带 condition；R6 非 condition 双出边；R7 环（a→b→a）；R8 不可达孤立节点；happy = api_contract §4 智能客服示例图通过。
- 序列化：Detail 的 id 为字符串、空 edges 为 `[]`、condition 为 `null`。

## 6. 验收门

- [ ] `go test ./internal/workflow/api/... -race -cover` ≥ **80%**
- [ ] `go build ./... && go vet ./... && go test ./... -race -count=1` 全绿
- [ ] api 包零 gin / gorm import（`go list -deps` 抽查或代码评审）

## 7. 可提交节点

`feat(workflow): api 契约层（WorkflowService + 密封 config + 图校验）`。
