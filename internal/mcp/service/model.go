package service

import (
	"github.com/Karlsk/go-hify/internal/platform/db"
)

// MCPServer 对应 mcp_servers 表：MCP 工具服务器配置（连接方式 + 参数）。
// transport 决定哪个连接参数生效：stdio 用 command/env、http/sse 用 url（按 transport
// 分支的必填规则走 api 层 Validate，DB 只存不校验）。enabled 是可逆下架开关（00021，
// 对齐 providers/agents）：false=停用拒绝新绑定与调用；DELETE=真删——有 Agent 绑定时
// mcp_tools 级联删被 fk_agent_tools_tool_id RESTRICT 挡（23503 翻译 ErrMCPServerInUse）。
//
// 字段不加 GORM 布尔 default tag（provider 踩坑 #1：default:true 会把显式 false 替换成
// 默认值）；创建路径显式设值，DB DEFAULT true 只作直插 SQL 兜底。
type MCPServer struct {
	db.BaseMutable
	Name      string            `gorm:"not null"`                   // 展示名，uq_mcp_servers_name 唯一
	Transport string            `gorm:"not null"`                   // stdio / http / sse（DB CHECK 兜底）
	Command   string            `gorm:"not null"`                   // stdio 启动命令；其他 transport 空串
	URL       string            `gorm:"not null"`                   // http/sse 服务地址；stdio 空串
	Env       map[string]string `gorm:"type:jsonb;serializer:json"` // stdio 子进程环境变量（写库前兜底 nil → 空 map）
	Enabled   bool              `gorm:"not null"`                   // 可逆下架开关（00021）
}

// TableName 显式表名（全模块约定：GORM 复数化不可靠，一律显式声明）。
func (MCPServer) TableName() string { return "mcp_servers" }

// MCPTool 对应 mcp_tools 表：server 暴露的工具清单，discover（连通测试）时按
// uq(mcp_server_id, name) upsert 刷新，只增改不删（绑定稳定性优先，db_model.md §2.4）。
// Agent 绑定的就是这张表（agent_tools.tool_id）。input_schema 是工具参数 JSON Schema
// 顶层对象，供前端表单与 chat 阶段（下批）eino ToolInfo 转换。
type MCPTool struct {
	db.BaseMutable
	MCPServerID uint64         `gorm:"not null"`                   // mcp_servers.id，ON DELETE CASCADE（有绑定时被 agent_tools 侧 RESTRICT 挡，00021）
	Name        string         `gorm:"not null"`                   // 工具原始名（server 内唯一，uq_mcp_tools_server_name）
	Description string         `gorm:"not null"`                   // 工具描述（LLM 选择工具的依据）
	InputSchema map[string]any `gorm:"type:jsonb;serializer:json"` // 参数 JSON Schema（写库前兜底 nil → 空 map）
}

// TableName 显式表名。
func (MCPTool) TableName() string { return "mcp_tools" }
