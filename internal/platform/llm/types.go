package llm

import (
	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// 本文件是 platform/llm 的公开消息模型：chat / workflow 等消费方组装上下文与
// 消费结果的唯一形态（CLAUDE.md《统一工具与消息模型》）。eino schema.Message
// 止步于本包内部（convert.go 转换，一处实现全局复用）。

// Role 消息角色（与 DB messages.role 的 CHECK 值一致）。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 统一消息模型。ToolCalls 仅 assistant 消息携带（工具调用请求）；
// ToolCallID / ToolName 仅 tool 消息携带（部分供应商要求回填工具名）。
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []tool.ToolCall
	ToolCallID string
	ToolName   string
}

// Usage token 用量（一次生成的最终计量；chat 多轮循环逐轮累加后对外）。
type Usage struct {
	Input  int64
	Output int64
}

// Final 一次生成的终态：流式（Stream.Final，chunk 聚合）与非流式（Generate 返回）
// 同一形态。FinishReason 供应商原样透传（stop / tool_calls / STOP / …）仅作遥测——
// 是否进入工具轮的判定纪律：只看 len(ToolCalls) > 0，不看 finish_reason
//（各厂字符串不同，vendor 差异留在 adapter）。
type Final struct {
	Content      string
	Usage        Usage
	FinishReason string
	ToolCalls    []tool.ToolCall
}
