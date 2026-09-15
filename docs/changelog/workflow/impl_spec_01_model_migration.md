# Workflow 实现 spec 01：00016 迁移 + service model

> 状态：**已实施**（2026-09-15 定稿并当日实施；含旧表替换修订，见 §2.1）。
> 上位契约：[db_model.md](./db_model.md)（数据模型终稿，本文契约的直接来源）、[api_contract.md](./api_contract.md)（HTTP 契约终稿）。本文/上位契约/代码现状三者冲突时：停下来问用户，不自行折中。
> 规范引用（实施逐条对照）：CLAUDE.md《数据库规范》——SQL 通用字段约定、建表执行清单、索引设计原则、事务与 DDL 规范；《代码组织规范》——模块内部结构（model 在 `service/model.go`，模块私有、无 json tag）。

## 1. 交付物

| # | 交付物 | 路径 |
|---|---|---|
| 1 | 工作流三表迁移（goose Up/Down 成对） | `migrations/00016_workflow_schema.sql` |
| 2 | GORM 实体三结构体 + TableName | `internal/workflow/service/model.go`（替换占位） |

## 2. 冻结契约

### 2.1 迁移 SQL

以 **db_model.md §3 为唯一文本源，逐字落盘**（含全部 `COMMENT ON`、两处 CHECK、三个索引、三个 FK CASCADE、`-- +goose Down`）。本文不复制第二份，防两处漂移；验收时 diff 校验与 §3 完全一致。

〔2026-09-15 修订（实施时用户拍板）：迁移 00006 已建旧单表 `workflows`（`config jsonb` 整图存储，即 db_model 决策 #1 否决的形态），上表 `CREATE TABLE workflows` 会撞名。00016 的 **Up 在本 DDL 前增加 `DROP TABLE IF EXISTS workflows;`**（全仓无代码读写旧表、无 FK 指向它，替换零风险），**Down 在删三表后按 00006 原样建回旧单表**（对称回滚）。三表 DDL 本体仍逐字取自 db_model §3。〕

不变量清单（验收对照）：

- 3 张表：`workflows` / `workflow_nodes` / `workflow_edges`；
- 2 个 CHECK：`status IN ('draft','published','disabled')`、`type IN ('llm','tool','condition','knowledge_retrieval')`；
- 3 个索引：`uq_workflows_name`、`uq_workflow_nodes_wf_key(workflow_id, node_key)`、`idx_workflow_edges_workflow_id`；
- 2 个 FK（nodes / edges → workflows）全部 `ON DELETE CASCADE`；
- 无 `deleted_at`（00013 软删退役）、无预留字段。

### 2.2 model（internal/workflow/service/model.go）

```go
package service

import "github.com/Karlsk/go-hify/internal/platform/db"

// Workflow 对应 workflows 表：基本信息 + 入口 + 状态。图主体在 workflow_nodes /
// workflow_edges（db_model.md §1）。Status 常量在 workflow/api（api.WorkflowStatus）。
type Workflow struct {
    db.BaseMutable
    Name         string `gorm:"not null"`  // 唯一（uq_workflows_name，db_model 决策 #10）
    Description  string `gorm:"not null"`  // 默认空串
    StartNodeKey string `gorm:"column:start_node_key;not null"` // 入口节点 key，图校验保证存在
    Status       string `gorm:"not null"`  // draft/published/disabled（DB CHECK 兜底）
}

func (Workflow) TableName() string { return "workflows" }

// WorkflowNode 对应 workflow_nodes 表：append-only 形态——整图保存 = 事务内删了重插
// （db_model 决策 #8），行只 INSERT/DELETE，故 BaseAppendOnly 无 updated_at。
// Config 是入库前已过 api.ParseNodeConfig 强校验的 JSON 文本（db_model §8）。
type WorkflowNode struct {
    db.BaseAppendOnly
    WorkflowID uint64 `gorm:"column:workflow_id;not null"` // workflows.id，ON DELETE CASCADE
    NodeKey    string `gorm:"column:node_key;not null"`    // 图内唯一（uq + 应用层前置校验）
    Type       string `gorm:"not null"`                    // llm/tool/condition/knowledge_retrieval
    Name       string `gorm:"not null"`                    // 展示名，默认空串
    Config     string `gorm:"type:jsonb;not null"`         // 已校验 JSON 原文，store 直存
}

func (WorkflowNode) TableName() string { return "workflow_nodes" }

// WorkflowEdge 对应 workflow_edges 表：连线。Condition nil = NULL = 无条件直走；
// 非空 = 匹配 source（condition 节点）的求值结果（db_model 决策 #4）。
type WorkflowEdge struct {
    db.BaseAppendOnly
    WorkflowID     uint64  `gorm:"column:workflow_id;not null"` // CASCADE
    SourceNodeKey  string  `gorm:"column:source_node_key;not null"`
    TargetNodeKey  string  `gorm:"column:target_node_key;not null"`
    Condition      *string // nil = 无条件
}

func (WorkflowEdge) TableName() string { return "workflow_edges" }
```

注释风格对齐 `internal/agent/service/model.go`（表用途 / CASCADE 语义 / 决策引用）；字段一律不带 json tag。

## 3. 行为语义

- 表结构唯一来源 = 本迁移；**禁止 GORM AutoMigrate**（顶层红线）。
- `Config string` 直存原文：校验发生在 api 层（spec 02 ParseNodeConfig），store 与 model 都不做二次加工。
- `TableName` 显式声明（全模块约定：GORM 复数化不可靠）。

## 4. 数据规范引用对照（建表执行清单 → 本篇落点）

| 规范条款（CLAUDE.md《数据库规范》） | 本篇落点 |
|---|---|
| 表名 snake_case 复数；外键列 `<单数>_id` | `workflow_nodes.workflow_id` / `workflow_edges.workflow_id` |
| `id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY` | 三表（model 侧由 `db.BaseMutable` / `db.BaseAppendOnly` mixin 对应） |
| `created_at timestamptz`；可变表加 `updated_at` | workflows 双时间戳；nodes / edges 仅 created_at（整图替换无行级 UPDATE） |
| 字符串一律 `text`；枚举 `text + CHECK` 非 PG enum | 全部 text 列；status / type 两处 CHECK |
| 列尽量 NOT NULL；FK 默认 RESTRICT、真子表 CASCADE | nodes / edges 是 workflows 真子表 → CASCADE（db_model §2） |
| 每个外键必须单独建索引 | `uq_workflow_nodes_wf_key` 最左前缀覆盖 + `idx_workflow_edges_workflow_id` |
| 表与非常规列写 `COMMENT ON` | db_model §3 已含全部文案，随迁移落盘 |
| 禁止预留字段 | 无 |
| 向量表 HNSW / executions 分区 | 不适用（本模块无向量列、无 executions 归属） |

## 5. 任务清单与实施顺序

本篇**无可 RED 的行为代码**（DDL + GORM tag 声明），无 TDD 循环——与仓库惯例一致（agent / provider 的 model 无独立单测，行为测试由 spec 03 store / spec 04 service+handler 覆盖）：

1. 迁移文件落盘（逐字自 db_model §3）；
2. `model.go` 三结构体 + TableName + 注释；
3. 迁移验证三步：`make migrate up` → `make migrate down` → 再 up（幂等）。

## 6. 验收门

- [ ] `make migrate up` 成功，`make migrate-status` 显示 00016 applied
- [ ] 三表存在，CHECK / 唯一索引 / 普通索引 / COMMENT 齐全（`\d workflow_nodes` 抽查）
- [ ] `make migrate down` 后三表消失，再 up 幂等
- [ ] `go build ./... && go vet ./...` 全绿
- [ ] 基线测试不受影响：`go test ./... -race -count=1`

## 7. 可提交节点

本篇独立可提交（迁移与 model 是原子单元）：`feat(workflow): 00016 三表迁移 + service model`。
