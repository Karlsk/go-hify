// Package tool 是全局统一工具契约：MCP 工具与未来内置工具（builtin tools）共用
// 同一组模型（ToolDef / ToolCall / ToolResult / Source），业务编排层（chat ReAct
// 循环、workflow）只见本包类型——eino 收敛在 platform/llm、go-sdk 收敛在
// mcp/service，均不出各自边界（CLAUDE.md《统一工具与消息模型》）。
//
// 纯标准库叶子包（同 platform/schema 形态）：mcp/api 等契约层可 import。
package tool

import (
	"context"
	"encoding/json"
)

// ToolDef 统一工具定义（全局）：发给 LLM 的最终形态。
// Name 是调用名，须在单次调用的工具清单内唯一（MCP 侧派生 s{serverID}__{toolName}）。
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"` // 参数 JSON Schema；顶层须为对象，空为 {}
}

// ToolCall LLM 发起的一次工具调用请求（模型输出，vendor 无关）。
type ToolCall struct {
	ID        string          `json:"id"`        // 调用 id（LLM 生成；tool 结果消息回填的锚点）
	Name      string          `json:"name"`      // = ToolDef.Name
	Arguments json.RawMessage `json:"arguments"` // 调用参数；空 / null / {} = 无参
}

// ToolResult 工具执行结果（回喂 LLM 的观察）。
// 失败不通过 error 中断对话——IsError=true 且错误文本在 Content：错误即上下文，
// LLM 下一轮可换工具 / 换参数 / 直接向用户说明（chat 工具循环的设计约束）。
type ToolResult struct {
	CallID  string `json:"call_id"`  // 对应 ToolCall.ID
	Name    string `json:"name"`     // 对应 ToolCall.Name
	Content string `json:"content"`  // 归一文本（MCP：TextContent 多条 "\n" 拼接）
	IsError bool   `json:"is_error"` // true = 执行失败（喂回 LLM 自纠，不是异常）
}

// Source 工具来源：供出一组工具并执行调用。实现：mcp/service（MCP，经
// mcpapi.ToolSourceFor 返回）；未来内置工具直接实现本接口，chat / workflow 侧零改动。
// 生命周期 = 一次对话轮（chat 每轮构建，不做跨轮缓存——绑定变更下一轮立即生效）。
type Source interface {
	// Tools 供出全部工具定义（空切片 = 无工具）。惰性加载：首次调用时查库。
	Tools(ctx context.Context) ([]ToolDef, error)
	// Invoke 执行一次调用，按 Name 路由回真实工具。恒返回 ToolResult：
	// 未知工具名 / 调用失败 / 上游 isError 一律 IsError=true 的结果，不返回 error。
	Invoke(ctx context.Context, call ToolCall) ToolResult
}
