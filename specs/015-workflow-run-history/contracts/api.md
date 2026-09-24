# API Contract: workflow 运行历史与节点轨迹查询（spec 015）

**Date**: 2026-09-24 | **Spec**: [spec.md](../spec.md) | **Plan**: [plan.md](../plan.md)

本篇 API 契约冻结面：**2 条 GET 路由 + 1 新哨兵**。既有 8 端点（CRUD / publish / disable / execute）**零变化**（SC-005）。落地时同步 `docs/changelog/workflow/api_contract.md`（路由总表 2 行 + §9 追加注记 + §8 错误码 1 行）与 CLAUDE.md（资源清单 runs 子路由 + 错误码表 RUN_NOT_FOUND 行）。

## 路由

| 方法 | 路径 | 语义 | 成功码 |
|---|---|---|---|
| GET | `/api/v1/workflows/{id}/runs` | 运行历史列表（游标分页，最新在前） | 200 |
| GET | `/api/v1/workflows/{id}/runs/{runId}` | 单次运行详情（全字段 + 节点轨迹） | 200 |

全链路只读（FR-007）——无任何写路由。全部经 auth 中间件（通用登录门槛，不按用户隔离——一期既有约定）。

## 通用约定（继承 workflow 模块既有面）

- 信封 `respond.Result`；成功响应 `Cache-Control: no-store`。
- bigint ID 字符串化：`"id,string"`；可空外键（conversation_id / message_id / parent_run_id）`*string`——null 或字符串 id。
- 时间 RFC 3339 UTC（started_at / created_at 均 timestamptz）。
- 空列表 `[]` 非 null；空字符串 `""` 非 null。
- 状态枚举字符串：run `"succeeded" / "failed"`（两终态，无 RUNNING）；trigger_source `"console" / "chat" / "workflow"`。

## 端点 1: GET /workflows/{id}/runs

**Query**（`ListRunsReq`，`form` tag，GET 列表）：

```go
type ListRunsReq struct {
    WorkflowID uint64 `form:"-"`              // 路由参数注入（handler strconv 后填），不参与绑定
    Limit      int    `form:"limit"`          // ≤0→20、>100→100（service 层 page.NewCursor 归一，不报错）
    Cursor     string `form:"cursor"`         // 不透明游标，前端原样回传；首页省略
}
```

**分页语义**（FR-001，游标 A 模式）：默认 20、上限 100、最新在前 = `created_at DESC, id DESC` keyset（行值比较 `(created_at, id) < (?, ?)`，首页省略该条件）；`LIMIT n+1` 判 has_more；不算精确 COUNT。列表项**不含 input / output 大文本**（FR-002——SQL 显式摘要列，非取后丢弃）。

**响应 data**：`RunListResult`（api 自定义，不 import platform/page——D1）：

```jsonc
{
  "items": [ /* RunSummarySchema[] */ ],
  "limit": 20,
  "has_more": true,
  "next_cursor": "eyJ..."   // has_more=false 时为 "" → 序列化 null，前端停止翻页
}
```

**RunSummarySchema**（摘要面 9 字段）：

```go
type RunSummarySchema struct {
    ID            string `json:"id,string"`  // 运行编号
    Status        string `json:"status"`     // succeeded / failed
    TriggerSource string `json:"trigger_source"` // console / chat / workflow
    IsTrial       bool   `json:"is_trial"`
    DurationMs    int    `json:"duration_ms"`
    ErrorNode     string `json:"error_node"` // 成功运行为 ""
    ErrorMsg      string `json:"error_msg"`  // 运行级错误摘要
    StartedAt     time.Time `json:"started_at"` // 「调用时间」列
    CreatedAt     time.Time `json:"created_at"` // 落库时刻 = 排序键
}
```

## 端点 2: GET /workflows/{id}/runs/{runId}

**Req**：`GetRunReq{ WorkflowID uint64; RunID uint64 }`（两路由参数，handler strconv 注入；非法数字由绑定层 400 兜底）。

**响应 data**：`RunDetailSchema`（全字段）+ 嵌套轨迹：

```go
type RunDetailSchema struct {
    ID             string    `json:"id,string"`
    Status         string    `json:"status"`
    TriggerSource  string    `json:"trigger_source"`
    IsTrial        bool      `json:"is_trial"`
    ConversationID *string   `json:"conversation_id"` // chat 触发关联，null = 非对话触发
    MessageID      *string   `json:"message_id"`
    TraceID        string    `json:"trace_id"`
    ParentRunID    *string   `json:"parent_run_id"`   // 子工作流运行的父编号；null = 顶层
    Input          string    `json:"input"`           // 截断保真文本（16KB 上限 + 截断标记）
    Output         string    `json:"output"`
    ErrorNode      string    `json:"error_node"`
    ErrorMsg       string    `json:"error_msg"`
    DurationMs     int       `json:"duration_ms"`
    StartedAt      time.Time `json:"started_at"`
    CreatedAt      time.Time `json:"created_at"`
    Nodes          []NodeRunSchema `json:"nodes"` // 按执行序（seq ASC）；空轨迹 = []
}

type NodeRunSchema struct {
    Seq        int    `json:"seq"`
    NodeKey    string `json:"node_key"`
    NodeType   string `json:"node_type"`
    Status     string `json:"status"`
    Input      string `json:"input"`
    Output     string `json:"output"`
    ErrorMsg   string `json:"error_msg"` // 落库恒空（既有形态），契约面保留字段位
    DurationMs int    `json:"duration_ms"`
}
```

轨迹组装在 service 层（run 查询 + node_runs 查询分次后组装，D5）；空轨迹返回 `[]` 不报错（Edge Case）。

## 错误码（本篇唯一新增：RUN_NOT_FOUND）

| HTTP | error.code | 哨兵 | 触发 |
|---|---|---|---|
| 404 | `RUN_NOT_FOUND` | `workflowapi.ErrRunNotFound` | runId 不存在，**或**存在于其他工作流下——查询带 `WHERE workflow_id = ? AND id = ?`，两情形同判不可区分（D3，不泄露存在性） |
| 400 | `VALIDATION_FAILED` | `errs.ErrValidationFailed`（既有） | cursor 篡改/非法（DecodeCursor 失败路径，chat 先例同款）；query 参数类型非法（绑定层） |
| 400 | `VALIDATION_FAILED` | 同上 | 路由 id / runId 非法数字（绑定层兜底） |

limit 越界**不报错**（归一）；workflow 本身不存在时列表返回空 `[]`（runs 为子资源查询，无独立 WORKFLOW_NOT_FOUND 判定——弱引用下空工作流与已删工作流不可区分，同 D3 语义，列表无泄露面）。

## service / store 分层约定（对齐模块既有 + chat 游标先例）

- `WorkflowService` 接口增两方法：`ListRuns(ctx, ListRunsReq) (*RunListResult, error)`、`GetRun(ctx, GetRunReq) (*RunDetailSchema, error)`——`(ctx, req) (*resp, error)` 家族签名，跨模块与 HTTP 复用（本篇下游仅前端自身）。
- service `Store` 接口增三方法：
  - `ListRuns(ctx, workflowID uint64, beforeCreatedAt time.Time, beforeID uint64, limit int) ([]WorkflowRun, error)`——keyset 行值比较；
  - `GetRunByID(ctx, workflowID, runID uint64) (*WorkflowRun, error)`——带 workflow_id 条件；
  - `ListNodeRuns(ctx, runID uint64) ([]WorkflowNodeRun, error)`——seq ASC。
- service 实现：`page.NewCursor` 归一 → `page.DecodeCursor[runCursorKey]`（失败 `fmt.Errorf("%w: cursor: %v", errs.ErrValidationFailed, err)`）→ store 查询 → `page.NewCursorResult` → api Result 组装（make 兜底空 slice）；`gorm.ErrRecordNotFound` → `ErrRunNotFound` 翻译。
- store 显式列常量：`selectRunSummary`（无 input/output）/ `selectRun`（全列）/ `selectNodeRun`——对齐本文件既有 selectWorkflow 纪律。
- handler：`respond.BindQuery` + `respond.OKWithCursor(c, items, limit, hasMore, nextCursor)`（列表）/ `respond.OK`（详情）；哨兵 `errors.Is` → `respond.Fail(c, 404, ErrRunNotFound.Error(), "运行记录不存在")`；其余 `respond.FailFromSentinel`。

## 冻结边界（SC-005 回归面）

- 既有 8 端点路由 / 请求 / 响应 / 错误零变化；执行引擎与落库链路零触碰（FR-008）。
- 游标对前端不透明：`next_cursor` 原样回传是唯一合法用法；篡改 → 400（Edge Case）。
- 父运行详情不反查子运行列表（parent_run_id 只透出值，spec 边界）。
