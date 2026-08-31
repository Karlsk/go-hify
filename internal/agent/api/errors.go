package api

import "errors"

// agent 模块哨兵错误：code = Error() 字符串，与 CLAUDE.md 错误码表命名空间一致
// （MODULE_REASON）。跨模块消费方与 handler 一律用 errors.Is 判断。
var (
	// ErrAgentNotFound Agent 不存在（404；软删行同报此错——下架后新会话被拒）。
	ErrAgentNotFound = errors.New("AGENT_NOT_FOUND")

	// ErrToolNotFound 绑定的工具不存在（404；tool_ids 撞 FK 23503 的翻译——
	// mcp 模块未建时这是工具存在性的唯一校验，建成后作为兜底保留）。
	ErrToolNotFound = errors.New("TOOL_NOT_FOUND")
)
