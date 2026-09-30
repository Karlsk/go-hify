package service

// tools：chat 工具循环的工具侧编排（chat_tool_loop_spec.md §5）——Registry 装配
//（含降级链：工具不是对话硬依赖，mirror RAG 先例）与工具轮执行（assistant 中间行 +
// tool 行落库、SSE 事件、结果截断喂回）。多轮循环本体在 turn.go。
//
// 中间行落库两模式一致：一次输出模式（emit=nil）同样落 assistant(tool_calls) / tool 行
//（下轮上下文完整是硬需求），只是不推送事件。

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/tool"
)

const (
	// maxToolRounds 工具调用轮上限（chat 引擎常量，不进 DB）：8 轮工具调用后强制收尾
	//（追加 nudge 摘掉工具逼出文本终答）；每轮可含多个并行调用（一条 assistant 消息的多个 ToolCalls）。
	maxToolRounds = 8
	// finishReasonMaxIterations 上限收尾的 done.finish_reason（区别于模型原生 stop / STOP）。
	finishReasonMaxIterations = "max_iterations"
	// maxToolResultRunes 工具结果文本截断上限：回喂 LLM / 落库 tool 行 / SSE tool_result
	// 事件三处共用同一份截断文本（先截断再分发，三处一致）。
	maxToolResultRunes = 20000
	// maxToolRoundsNudge 上限触发的收尾引导（瞬态：只进本轮 LLM 请求，不落库——
	// 下轮 loadHistory 从库里取，天然不含）。
	maxToolRoundsNudge = "请基于以上工具返回的信息，直接回答用户的问题；不要再调用工具。"
	// invalidToolArgsMsg 参数非法（非 JSON 对象）的 IsError 结果文本：不发起调用直接喂回 LLM。
	invalidToolArgsMsg = "工具参数不是合法 JSON 对象"
)

// loadToolRegistry 按绑定工具构造 Registry；nil = 无工具（原路径语义：不查 mcp、
// opts.Tools 为空，与 v1 完全一致）。降级链（工具不是对话硬依赖）：
//   - ToolIDs 空 → 零 mcp 交互，直接 nil；
//   - id 非数字（脏数据防御）/ Registry 构建失败（DB 错 / 垃圾 schema / 调用名撞车）/
//     eino 转换试跑失败（垃圾 schema 在这里挡下，不流到 LLM 调用里炸）→ WARN 后照常对话；
//   - 清单全被过滤（server 停用等）→ nil，同无工具路径。
func (s *chatService) loadToolRegistry(ctx context.Context, a *agentapi.AgentDetailSchema) *tool.Registry {
	if len(a.ToolIDs) == 0 {
		return nil
	}
	if s.toolSources == nil {
		slog.WarnContext(ctx, "chat: tool source factory missing; degrade to no tools", "agent_id", a.ID)
		return nil
	}
	ids := make([]uint64, 0, len(a.ToolIDs))
	for _, id := range a.ToolIDs {
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			slog.WarnContext(ctx, "chat: agent tool id not numeric; degrade to no tools", "agent_id", a.ID, "tool_id", id)
			return nil
		}
		ids = append(ids, n)
	}
	reg, err := tool.NewRegistry(ctx, s.toolSources.ToolSourceFor(mcpapi.ToolSourceReq{ToolIDs: ids}))
	if err != nil {
		slog.WarnContext(ctx, "chat: build tool registry failed; degrade to no tools", "agent_id", a.ID, "err", err)
		return nil
	}
	defs := reg.Defs()
	if len(defs) == 0 {
		return nil // 全部被过滤（server 停用等）：同无工具路径
	}
	if err := llm.ValidateTools(defs); err != nil {
		slog.WarnContext(ctx, "chat: tool schema not convertible; degrade to no tools", "agent_id", a.ID, "err", err)
		return nil
	}
	return reg
}

// parsedCall 一次工具调用 + 预解析参数（args 只解一次：SSE 事件 / DB 行 / 合法性判定三处消费）。
type parsedCall struct {
	call tool.ToolCall
	args map[string]any
	ok   bool // false = 参数非合法 JSON 对象，不发起调用
}

// runToolRound 执行一轮工具调用：落 assistant 中间行（content + tool_calls jsonb，
// 下轮上下文重放锚点）→ 逐调用（tool_call 事件 → 执行 → 落 tool 行 → tool_result 事件）。
// 返回追加给下一轮请求的消息（assistant + 各 tool）；clientGone = 前端断连
//（emit 失败，静默收尾、不再执行剩余调用）。
//
// 执行失败不中断对话：IsError 结果照样落库 / 喂回（错误即上下文，LLM 下一轮自纠）。
// 落库失败只 WARN（mirror persistAssistant 先例）——但 assistant 中间行失败时跳过
// tool 行（无锚点的孤儿 tool 行会让下轮重放非法：tool 消息必须紧跟携带该调用的
// assistant 消息）。
func (s *chatService) runToolRound(ctx context.Context, convID uint64, reg *tool.Registry, res consumeResult, emit func(chatapi.StreamEvent) error) (appendMsgs []llm.Message, clientGone bool) {
	parsed := make([]parsedCall, 0, len(res.toolCalls))
	for _, c := range res.toolCalls {
		args, ok := parseToolArgs(c.Arguments)
		parsed = append(parsed, parsedCall{call: c, args: args, ok: ok})
	}

	// assistant 中间行：本轮 LLM 的调用决策 + 可能伴随的 content，一并落库
	midToolCalls := make([]map[string]any, 0, len(parsed))
	for _, p := range parsed {
		midToolCalls = append(midToolCalls, map[string]any{"id": p.call.ID, "tool": p.call.Name, "args": p.args})
	}
	midPersisted := true
	if err := s.store.CreateMessage(ctx, &Message{
		ConversationID: convID,
		Role:           chatapi.RoleAssistant,
		Content:        res.content,
		ToolCalls:      midToolCalls,
	}); err != nil {
		slog.WarnContext(ctx, "chat: persist assistant tool-call row failed", "conversation_id", convID, "err", err)
		midPersisted = false
	}

	appendMsgs = make([]llm.Message, 0, len(parsed)+1)
	appendMsgs = append(appendMsgs, llm.Message{Role: llm.RoleAssistant, Content: res.content, ToolCalls: res.toolCalls})
	for _, p := range parsed {
		if emit != nil {
			if eerr := emit(chatapi.ToolCallEvent(p.call.ID, p.call.Name, p.args)); eerr != nil {
				return appendMsgs, true // 前端断连：不执行剩余调用（省一次工具 IO）
			}
		}
		var result tool.ToolResult
		if !p.ok {
			// 参数非法：不发起调用，IsError 结果直接喂回（LLM 自纠参数）
			result = tool.ToolResult{CallID: p.call.ID, Name: p.call.Name, IsError: true, Content: invalidToolArgsMsg}
		} else {
			result = reg.Invoke(ctx, p.call) // 失败也是 IsError 结果（未知工具 / 连接失败 / 上游 isError）
		}
		content := truncateToolContent(result.Content) // 三处共用同一份截断文本
		if midPersisted {
			// tool 行：调用引用存 ToolCalls[0]（messages 表无独立列，重放锚点）
			if err := s.store.CreateMessage(ctx, &Message{
				ConversationID: convID,
				Role:           chatapi.RoleTool,
				Content:        content,
				ToolCalls:      []map[string]any{{"id": p.call.ID, "tool": p.call.Name, "args": p.args}},
			}); err != nil {
				slog.WarnContext(ctx, "chat: persist tool row failed", "conversation_id", convID, "tool", p.call.Name, "err", err)
			}
		}
		if emit != nil {
			if eerr := emit(chatapi.ToolResultEvent(p.call.ID, p.call.Name, chatapi.ToolResultPayload{Content: content, IsError: result.IsError})); eerr != nil {
				return appendMsgs, true // 结果已执行已落库：断连只影响事件推送
			}
		}
		appendMsgs = append(appendMsgs, llm.Message{Role: llm.RoleTool, Content: content, ToolCallID: p.call.ID, ToolName: p.call.Name})
	}
	return appendMsgs, false
}

// parseToolArgs 调用参数反序列化（一次解析，三处消费）。空 / null / {} = 无参形态（合法）；
// 非 JSON 对象（数组 / 字符串 / 截断的 JSON）→ ok=false 且 args 归一 {}（DB 行不落 null）。
func parseToolArgs(raw json.RawMessage) (args map[string]any, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, true
	}
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return map[string]any{}, false
	}
	return args, true
}

// truncateToolContent 工具结果文本按 rune 截断（中文不裂半字），超长以省略号标记。
func truncateToolContent(s string) string {
	r := []rune(s)
	if len(r) <= maxToolResultRunes {
		return s
	}
	return string(r[:maxToolResultRunes]) + "…"
}
