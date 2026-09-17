package api

import "errors"

// agent 模块哨兵错误：code = Error() 字符串，与 CLAUDE.md 错误码表命名空间一致
// （MODULE_REASON）。跨模块消费方与 handler 一律用 errors.Is 判断。
var (
	// ErrAgentNotFound Agent 不存在（404；真删后新会话 / 配置面均不可见）。
	ErrAgentNotFound = errors.New("AGENT_NOT_FOUND")

	// ErrAgentInUse Agent 有历史会话，无法删除（409；conversations.agent_id FK
	// RESTRICT 的 23503 翻译）。可先删除相关会话，或改用停用（enabled=false）。
	ErrAgentInUse = errors.New("AGENT_IN_USE")

	// ErrToolNotFound 绑定的工具不存在（404；tool_ids 撞 FK 23503 的翻译——
	// mcp 模块未建时这是工具存在性的唯一校验，建成后作为兜底保留）。
	ErrToolNotFound = errors.New("TOOL_NOT_FOUND")

	// ErrKnowledgeBaseNotFound 绑定的知识库不存在（404；knowledge_base_ids 撞 FK
	// 23503 的翻译——agent 不得依赖 rag（依赖清单），FK 是存在性的唯一校验机制，
	// 与 ErrToolNotFound 同款）。码与 ragapi.ErrKnowledgeBaseNotFound 同名同义：
	// agent api 不能 import rag api，各持一份哨兵，前端语义无歧义
	//（rag_injection_spec.md §3.1）。
	ErrKnowledgeBaseNotFound = errors.New("KNOWLEDGE_BASE_NOT_FOUND")

	// ErrWorkflowNotFound 绑定的工作流不存在（404；workflow_id 撞 FK 23503 的翻译——
	// agent 不得依赖 workflow（依赖清单），FK 是存在性的唯一校验机制，
	// 与 ErrToolNotFound / ErrKnowledgeBaseNotFound 同款）。码与 workflowapi.ErrWorkflowNotFound
	// 同名同义：agent api 不能 import workflow api，各持一份哨兵，前端语义无歧义。
	ErrWorkflowNotFound = errors.New("WORKFLOW_NOT_FOUND")

	// ErrAgentDisabled Agent 已停用（503）：保留配置、新会话被拒——区别于 ErrAgentNotFound
	//（不存在）。仅跨模块消费方（chat 建会话 / 发消息）产生与判断；
	// agent 模块自身无对应端点（enabled 是可设置字段，不是错误）。
	ErrAgentDisabled = errors.New("AGENT_DISABLED")
)
