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
