package service

import (
	"github.com/Karlsk/go-hify/internal/platform/db"
)

// Agent 对应 agents 表：Agent 配置 = 身份定义（name / description / system_prompt）
// + 模型绑定（model_id / fallback_model_id）+ 运行参数（temperature / max_output_tokens）。
// 参数打散列不用 jsonb——暴露旋钮仅 2 个，强类型 + DB CHECK 可查可校验（db_model.md 决策 #1）。
//
// 软删除（db.BaseSoftDelete）= 下架语义（决策 #9）：GORM 自动给查询加
// WHERE deleted_at IS NULL、把 DELETE 改写为 UPDATE；绑定行（agent_tools）保留——
// CASCADE 只在硬删触发，恢复时绑定还在；conversations 不受影响，新会话经
// Get→ErrAgentNotFound 拒绝。
//
// 字段一律不加 GORM 数值 / 布尔 default tag：temperature=0（严谨）是合法零值，
// 加 default 会被 GORM 在 INSERT 时替换成默认值（provider 模块踩坑 #1 的数值变体）；
// 创建路径显式设值，DB 列 DEFAULT 只作直插 SQL 兜底。
type Agent struct {
	db.BaseSoftDelete
	Name            string  `gorm:"not null"` // 展示名（不唯一，决策 #5）
	Description     string  `gorm:"not null"` // 用途说明，默认空串
	ModelID         uint64  `gorm:"not null"` // 主模型 models.id（RESTRICT）
	FallbackModelID *uint64 // 备用模型（一期配置位不启用；nil=未设置）
	SystemPrompt    string  `gorm:"not null"`                   // 角色指令（Agent 的"灵魂"）
	Temperature     float64 `gorm:"type:numeric(3,2);not null"` // 0.00-2.00（DB CHECK 兜底）
	MaxOutputTokens *int64  // NULL=跟随模型默认（chat 引擎读 nil 不设 option）
}

// TableName 显式表名（全模块约定：GORM 复数化不可靠，一律显式声明）。
func (Agent) TableName() string { return "agents" }

// AgentTool 对应 agent_tools 表：Agent ↔ MCP 工具绑定，append-only，绑定 = 授权。
// 代理 id 主键 + uq(agent_id, tool_id)（db_model.md 决策 #7）；绑定粒度 = 工具，不是
// server（决策 #6）。tool 指向 mcp_tools.id——mcp 模块未建，存在性由 FK 23503 翻译兜底。
type AgentTool struct {
	db.BaseAppendOnly
	AgentID uint64 `gorm:"not null"` // agents.id，ON DELETE CASCADE（仅硬删触发）
	ToolID  uint64 `gorm:"not null"` // mcp_tools.id，ON DELETE CASCADE
}

// TableName 显式表名。
func (AgentTool) TableName() string { return "agent_tools" }
