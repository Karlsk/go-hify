// Package api 是 chat 模块的契约层（纯包，无实现）：请求/响应契约、SSE 事件类型与哨兵错误。
// 跨模块调用与 HTTP 请求复用同一套接口；实现在 service 包，由组合根注入 handler 与上游模块。
package api

import (
	"time"

	"github.com/Karlsk/go-hify/internal/platform/schema"
)

// ID 约定（对齐 agent/api）：请求侧 FK 用数字（前端把字符串 id 转 Number 后提交），
// 响应侧一律字符串（JS 2^53 精度保护）；路径参数由 handler BindUri 注入、不进 body。

// 消息角色（与 migrations/00006 的 CHECK (role IN (...)) 对齐；钉住测试防漂移）。
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// SSE 事件类型（data 帧内 type 判别，接口规范《对话接口》）；钉住测试防前端契约漂移。
const (
	EventDelta      = "delta"       // 每个 token 片段
	EventToolCall   = "tool_call"   // 触发 MCP 工具调用（v1 预留：契约先定，工具循环后补）
	EventToolResult = "tool_result" // 工具返回（v1 预留）
	EventDone       = "done"        // 正常结束（message_id + usage + finish_reason）
	EventError      = "error"       // 异常结束（code + retryable）
)

// ---- 响应 Schema ----

// ConversationSchema 会话响应。只含元数据；user_id 一期不暴露（不按用户隔离，前端无需感知）。
type ConversationSchema struct {
	schema.BaseSchema        // id 字符串化 + created_at / updated_at（可变表双时间戳）
	AgentID           string `json:"agent_id"` // 字符串化外键；创建时绑定，中途不换
	Title             string `json:"title"`    // 首条用户消息截断生成
}

// ToolCall assistant 消息发起的一次工具调用请求（messages.tool_calls jsonb 数组元素）。
type ToolCall struct {
	ID   string         `json:"id"` // 调用标识（SSE 侧 tool_call/tool_result 配对键）
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"` // 由 service 保证非 nil（空参返 {} 不返 null）
}

// MessageSchema 消息响应。append-only 表无 updated_at，不 embed schema.BaseSchema，自行声明表头。
type MessageSchema struct {
	ID        string     `json:"id"`   // 字符串化；单调递增 = 消息顺序 = 翻页游标
	Role      string     `json:"role"` // user / assistant / tool
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls"` // assistant 中间行专用；service 保证非 nil（空返 []）
	CreatedAt time.Time  `json:"created_at"`
}

// ConversationListResult 会话列表（keyset 游标分页：conversations 是增长表）。
// handler 经 respond.OKWithCursor 写 meta；Items 由 service 保证非 nil。
type ConversationListResult struct {
	Items      []ConversationSchema `json:"items"`
	Limit      int                  `json:"limit"`
	HasMore    bool                 `json:"has_more"`
	NextCursor string               `json:"next_cursor"` // has_more=false 时为 ""（序列化为 null）
}

// MessageListResult 历史消息（正序 after_id 游标）。消息游标是单列 id——无需不透明 base64，
// 直接数字回传（区别于会话列表的复合排序键 cursor）；NextAfterID=0 表示没有下一页。
type MessageListResult struct {
	Items       []MessageSchema `json:"items"`
	Limit       int             `json:"limit"`
	HasMore     bool            `json:"has_more"`
	NextAfterID uint64          `json:"next_after_id"`
}

// Usage token 用量（done 事件与一次输出模式共用）。
type Usage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

// AssistantReplySchema 一次输出模式（stream:false）的响应：最终 assistant 消息。
// 错误走标准错误信封 + 正常 HTTP 状态码（流未开始，状态码可用）。
type AssistantReplySchema struct {
	MessageID    string `json:"message_id"` // 落库后的 assistant messages.id（重新生成/反馈锚定）
	Content      string `json:"content"`
	Usage        Usage  `json:"usage"`
	FinishReason string `json:"finish_reason"`
}

// ---- SSE 事件 ----

// StreamEvent SSE data 帧的结构化表示：service 产出、handler 负责帧格式化（data: JSON\n\n）+ Flush。
// 单一扁平结构 + omitempty：每种 type 只填自己的字段，marshal 后与接口规范的事件契约逐字段一致；
// 构造函数（下方 XxxEvent）是唯一入口，字段拼写集中一处。
type StreamEvent struct {
	Type         string         `json:"type"`
	Content      string         `json:"content,omitempty"`       // delta
	ID           string         `json:"id,omitempty"`            // tool_call / tool_result
	Tool         string         `json:"tool,omitempty"`          // tool_call / tool_result
	Args         map[string]any `json:"args,omitempty"`          // tool_call
	Result       any            `json:"result,omitempty"`        // tool_result
	MessageID    string         `json:"message_id,omitempty"`    // done
	Usage        *Usage         `json:"usage,omitempty"`         // done
	FinishReason string         `json:"finish_reason,omitempty"` // done
	Code         string         `json:"code,omitempty"`          // error：机器可读码（哨兵 Error()）
	Message      string         `json:"message,omitempty"`       // error：人类可读
	Retryable    bool           `json:"retryable"`               // error：前端据此决定是否展示"重新生成"按钮（false 也必须显式传）
}

// DeltaEvent 文本片段。
func DeltaEvent(content string) StreamEvent {
	return StreamEvent{Type: EventDelta, Content: content}
}

// ToolCallEvent 触发工具调用（v1 预留：工具循环实现后启用）。
func ToolCallEvent(id, tool string, args map[string]any) StreamEvent {
	return StreamEvent{Type: EventToolCall, ID: id, Tool: tool, Args: args}
}

// ToolResultEvent 工具返回（v1 预留）。
func ToolResultEvent(id, tool string, result any) StreamEvent {
	return StreamEvent{Type: EventToolResult, ID: id, Tool: tool, Result: result}
}

// DoneEvent 正常结束。
func DoneEvent(messageID string, usage Usage, finishReason string) StreamEvent {
	return StreamEvent{Type: EventDone, MessageID: messageID, Usage: &usage, FinishReason: finishReason}
}

// ErrorEvent 异常结束（流已 200 开始，HTTP 状态码不可改，错误只能走本事件）。
func ErrorEvent(code, message string, retryable bool) StreamEvent {
	return StreamEvent{Type: EventError, Code: code, Message: message, Retryable: retryable}
}

// ---- 请求 Req ----

// CreateConversationReq 建会话即绑 Agent（中途不换）。
type CreateConversationReq struct {
	AgentID uint64 `json:"agent_id" binding:"required"`
}

// Validate 跨字段校验（无；binding tag 已覆盖字段格式）。
func (r CreateConversationReq) Validate() error { return nil }

// ListConversationsReq 会话列表（keyset 游标）。Cursor 是上一页 next_cursor 的原样回传；
// limit/page 约束与游标解码在 service 层（api 包不 import 带 gorm 的 platform/page）。
type ListConversationsReq struct {
	Limit  int    `form:"limit"`
	Cursor string `form:"cursor"`
}

// Validate 跨字段校验（无）。
func (r ListConversationsReq) Validate() error { return nil }

// DeleteConversationReq 删除会话（messages 经 FK 级联删）。ID 由 handler BindUri 注入。
type DeleteConversationReq struct {
	ID uint64 `json:"-" uri:"id" binding:"required"`
}

// Validate 跨字段校验（无）。
func (r DeleteConversationReq) Validate() error { return nil }

// ListMessagesReq 历史消息正序查询（after_id 游标；after_id=0 即首页）。
// ConversationID 由 handler BindUri 注入。
type ListMessagesReq struct {
	ConversationID uint64 `json:"-" uri:"id" binding:"required"`
	AfterID        uint64 `form:"after_id"`
	Limit          int    `form:"limit"`
}

// Validate 跨字段校验（无）。
func (r ListMessagesReq) Validate() error { return nil }

// SendMessageReq 发消息（两模式共用）。ConversationID 由 handler BindUri 注入、json:"-" 防 body
// 的 id 键覆盖路径值（provider 模块踩坑 #7）；Content 上限对齐 agent 模块的提示词上限量级。
type SendMessageReq struct {
	ConversationID uint64 `json:"-"` // handler 通过独立 uriReq 绑定路径 :id，不在此绑 binding tag
	Content        string `json:"content" binding:"required,max=32000"`
	Stream         *bool  `json:"stream"` // 缺省 true；指针区分「未传」与显式 false
}

// Validate 跨字段校验（无）。
func (r SendMessageReq) Validate() error { return nil }

// WantStream 是否流式模式（stream 缺省 true）。
func (r SendMessageReq) WantStream() bool { return r.Stream == nil || *r.Stream }
