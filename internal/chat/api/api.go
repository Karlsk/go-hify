package api

import "context"

// ChatService 是 chat 模块对外的唯一接口。方法签名固定
// (ctx context.Context, req XxxReq) (*XxxSchema, error)；实现在 service 包，
// 由组合根注入 handler 与上游模块。跨模块调用与 HTTP 请求复用同一套接口。
type ChatService interface {
	// CreateConversation 建会话（绑 Agent，中途不换）。
	// 错误：agentapi.ErrAgentNotFound（不存在 / 已删）、agentapi.ErrAgentDisabled（停用，新会话被拒）。
	CreateConversation(ctx context.Context, req CreateConversationReq) (*ConversationSchema, error)

	// ListConversations 当前用户的会话列表（keyset 游标，updated_at DESC）。
	ListConversations(ctx context.Context, req ListConversationsReq) (*ConversationListResult, error)

	// ListMessages 会话内历史消息（正序 after_id 游标）。
	// 错误：ErrConversationNotFound。
	ListMessages(ctx context.Context, req ListMessagesReq) (*MessageListResult, error)

	// DeleteConversation 删除会话（messages 经 FK 级联删）。
	// 错误：ErrConversationNotFound。
	DeleteConversation(ctx context.Context, req DeleteConversationReq) error

	// SendMessage 一次输出模式（stream:false）：内部跑同一条 Agent 循环，生成完毕返回最终
	// assistant 消息（标准 respond 信封；错误走标准错误信封 + 正常 HTTP 状态码）。
	// 错误：ErrConversationNotFound、ErrModelContextTooLong、agentapi / providerapi / llm 哨兵透传。
	SendMessage(ctx context.Context, req SendMessageReq) (*AssistantReplySchema, error)

	// Stream 流式模式（stream:true）：svc 产出 StreamEvent 并逐个调 emit——emit 由 handler
	// 提供（写 SSE 帧 + Flush），service 全程不碰 gin。返回 error 只用于流开始前的失败
	//（此时尚未写 200 头，handler 仍可回标准错误信封）；流一旦开始，后续错误经
	// ErrorEvent emit 后返回 nil。
	// 前置错误：ErrConversationNotFound、agentapi / providerapi 哨兵（emit 之前返回）。
	Stream(ctx context.Context, req SendMessageReq, emit func(StreamEvent) error) error
}
