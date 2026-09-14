package service

import (
	"github.com/Karlsk/go-hify/internal/platform/db"
)

// Agent 对应 agents 表：Agent 配置 = 身份定义（name / description / system_prompt）
// + 模型绑定（model_id / fallback_model_id）+ 运行参数（temperature / max_output_tokens
// / max_context_turns / enabled）。
// 参数打散列不用 jsonb——暴露旋钮仅 4 个，强类型 + DB CHECK 可查可校验（db_model.md 决策 #1）。
//
// 无软删（决策 #9 修订，deleted_at 已随 00013 退役）：enabled=false = 停用（保留配置、
// 新会话被拒、一键恢复）；DELETE = 真删——agent_tools / agent_knowledge_bases 绑定由
// FK CASCADE 同步清理，有历史会话（conversations.agent_id FK RESTRICT）被挡删 409
// ErrAgentInUse，可先删会话或改停用。
//
// 字段一律不加 GORM 数值 / 布尔 default tag：temperature=0（严谨）是合法零值，
// 加 default 会被 GORM 在 INSERT 时替换成默认值（provider 模块踩坑 #1 的数值变体）；
// 创建路径显式设值，DB 列 DEFAULT 只作直插 SQL 兜底。
type Agent struct {
	db.BaseMutable
	Name            string  `gorm:"not null"` // 展示名（不唯一，决策 #5）
	Description     string  `gorm:"not null"` // 用途说明，默认空串
	ModelID         uint64  `gorm:"not null"` // 主模型 models.id（RESTRICT）
	FallbackModelID *uint64 // 备用模型（一期配置位不启用；nil=未设置）
	SystemPrompt    string  `gorm:"not null"`                   // 角色指令（Agent 的"灵魂"）
	Temperature     float64 `gorm:"type:numeric(3,2);not null"` // 0.00-2.00（DB CHECK 兜底）
	MaxOutputTokens *int64  // NULL=跟随模型默认（chat 引擎读 nil 不设 option）
	MaxContextTurns int     `gorm:"not null"` // 多轮对话携带的最大历史轮数（chat 引擎读）
	Enabled         bool    `gorm:"not null"` // 停用开关：false=保留配置且新会话被拒
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
