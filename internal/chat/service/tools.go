package service

// tools：chat 工具循环的工具侧装配（chat_tool_loop_spec.md §5）——Registry 装配
//（含降级链：工具不是对话硬依赖，mirror RAG 先例）。多轮循环本体与工具轮执行
//（assistant 中间行 / tool 行落库、SSE 事件、结果截断喂回）自 spec 019 起归
// internal/platform/reactloop：循环语义单点维护，本层经 Hooks 回调接线保持
// 落库序与事件序零变化。
//
// 中间行落库两模式一致：一次输出模式（emit=nil）同样落 assistant(tool_calls) / tool 行
//（下轮上下文完整是硬需求），只是不推送事件——接线在 turn.go 回调闭包。

import (
	"context"
	"log/slog"
	"strconv"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/tool"
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
