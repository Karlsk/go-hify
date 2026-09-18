package service

import (
	"time"

	"github.com/Karlsk/go-hify/internal/platform/db"
)

// Workflow 对应 workflows 表：基本信息 + 入口 + 状态。图主体在 workflow_nodes /
// workflow_edges（db_model.md §1）。Status 常量在 workflow/api（api.WorkflowStatus）。
type Workflow struct {
	db.BaseMutable
	Name         string `gorm:"not null"`                       // 唯一（uq_workflows_name，db_model 决策 #10）
	Description  string `gorm:"not null"`                       // 默认空串
	StartNodeKey string `gorm:"column:start_node_key;not null"` // 入口节点 key，图校验保证存在
	Status       string `gorm:"not null"`                       // draft/published/disabled（DB CHECK 兜底）
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
	WorkflowID    uint64  `gorm:"column:workflow_id;not null"` // CASCADE
	SourceNodeKey string  `gorm:"column:source_node_key;not null"`
	TargetNodeKey string  `gorm:"column:target_node_key;not null"`
	Condition     *string // nil = 无条件
}

func (WorkflowEdge) TableName() string { return "workflow_edges" }

// WorkflowRun 对应 workflow_runs 表（db_model §12 冻结，spec 06）：每次执行一行，
// append-only 收尾统一写（执行结束一次性落库，无 RUNNING 态）。workflow_id /
// conversation_id / message_id 全弱引用（无 FK——保留期日志表不阻碍业务删除；
// workflow_name 快照保 workflow 删除后轨迹可读）。
type WorkflowRun struct {
	db.BaseAppendOnly
	WorkflowID     uint64     // 弱引用 workflows.id（无 FK）
	WorkflowName   string     // 快照
	TriggerSource  string     // console / chat
	IsTrial        bool       // 试运行标记（O3）：区分测试与真实流量
	ConversationID *uint64    // chat 触发时填；console 为 nil（弱引用，无 FK）
	MessageID      *uint64
	TraceID        string
	Status         string // succeeded / failed
	Input          string // jsonb 文本（截断后）
	Output         string
	ErrorNode      string // 失败节点 key，成功 = ""
	ErrorMsg       string
	DurationMs     int
	StartedAt      time.Time // 执行起点（created_at = 收尾写入时刻）
}

func (WorkflowRun) TableName() string { return "workflow_runs" }

// WorkflowNodeRun 对应 workflow_node_runs 表：每个执行节点一行（含失败节点自身的行）。
// seq 是执行顺序唯一事实源（run 内从 1 递增，UNIQUE (run_id, seq) 兜底）；input/output
// 为截断摘要（非 execContext 全量快照）。run_id 是两张轨迹表之间唯一的 FK（CASCADE）。
type WorkflowNodeRun struct {
	db.BaseAppendOnly
	RunID      uint64 // workflow_runs.id，ON DELETE CASCADE（同模块真子表）
	Seq        int    // 执行序号
	NodeKey    string
	NodeType   string
	Status     string // succeeded / failed
	Input      string // jsonb 文本（截断摘要，非 ctx 快照）
	Output     string
	ErrorMsg   string
	DurationMs int
}

func (WorkflowNodeRun) TableName() string { return "workflow_node_runs" }
