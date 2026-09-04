package logging

import (
	"github.com/Karlsk/go-hify/internal/platform/db"
)

// Execution 运行日志（GORM 实体）：每次 LLM 调用一行（不是每条消息——一轮带工具的对话 = 多行），
// append-only、按月分区、90 天保留。表结构见 migrations/00007_executions.sql；
// 唯一约束与 CHECK 由迁移 SQL 持有，model 不重复声明。
//
// 弱引用：conversation_id / model_id 不建 FK——90 天保留的日志表不能反过来阻碍会话/模型删除
// （引用完整性由业务层哨兵挡）；workflow LLM 节点的调用 conversation_id 为 nil。
type Execution struct {
	db.BaseAppendOnly                  // id + created_at；真实主键 (id, created_at) 由迁移持有（分区键必须在主键里），GORM 侧声明 id 即可
	ConversationID    *uint64          // 所属会话；nil = workflow 节点调用
	ModelID           *uint64          // 所用模型；配合 ModelName 冗余快照
	ModelName         string           `gorm:"not null"`                   // 模型删除后记录仍可读
	Input             map[string]any   `gorm:"type:jsonb;serializer:json"` // 请求侧（如 messages 摘要）；查询禁 SELECT *（大文本 TOAST）
	Output            map[string]any   `gorm:"type:jsonb;serializer:json"` // 响应侧
	ToolChain         []map[string]any `gorm:"type:jsonb;serializer:json"` // 工具调用链：每轮 {tool, args, result}，与 messages.tool_calls 互补
	PromptTokens      int64            `gorm:"not null"`
	CompletionTokens  int64            `gorm:"not null"`
	TotalTokens       int64            `gorm:"not null"`
	DurationMs        int32            `gorm:"not null"` // 本次调用耗时；端到端时延由前端本地计时
	FinishReason      string           `gorm:"not null"` // stop/length/tool_use…；失败流为空串
	ErrorClass        *string          // 七类（见 platform/llm 错误分类）；nil = 成功
}

func (Execution) TableName() string { return "executions" }
