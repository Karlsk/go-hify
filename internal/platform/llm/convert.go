package llm

import (
	"encoding/json"
	"fmt"

	"github.com/eino-contrib/jsonschema"
	"github.com/cloudwego/eino/schema"

	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// 本文件是 eino ↔ Hify 类型的唯一转换点（eino 收敛决策的落点）：
//   - 出向：llm.Message → eino schema.Message；tool.ToolDef → eino schema.ToolInfo；
//   - 入向：eino 生成结果（尾帧 ResponseMeta / 增量 ToolCalls）→ llm.Final。
// 除本包（与内部 Streamer 适配层）外，全仓禁止 import eino。

// toEinoMessages Hify 消息序列 → eino 消息序列（一次转换全调用链复用）。
func toEinoMessages(msgs []Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(msgs))
	for i := range msgs {
		out = append(out, toEinoMessage(&msgs[i]))
	}
	return out
}

// toEinoMessage 单条转换。tool 消息的 ToolCallID / ToolName 回填（部分供应商
//——如 Gemini——要求 tool 结果携带工具名才能对上请求里的调用）。
func toEinoMessage(m *Message) *schema.Message {
	switch m.Role {
	case RoleSystem:
		return &schema.Message{Role: schema.System, Content: m.Content}
	case RoleUser:
		return &schema.Message{Role: schema.User, Content: m.Content}
	case RoleTool:
		em := schema.ToolMessage(m.Content, m.ToolCallID)
		em.ToolName = m.ToolName
		return em
	default: // assistant（未知 role 按 assistant 兜底，与 chat 侧历史转换同款防御）
		em := &schema.Message{Role: schema.Assistant, Content: m.Content}
		if len(m.ToolCalls) > 0 {
			em.ToolCalls = make([]schema.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				em.ToolCalls = append(em.ToolCalls, toEinoToolCall(tc))
			}
		}
		return em
	}
}

// toEinoToolCall 统一调用请求 → eino FunctionCall 形态（Arguments 是 JSON 字符串）。
func toEinoToolCall(tc tool.ToolCall) schema.ToolCall {
	return schema.ToolCall{
		ID:   tc.ID,
		Type: "function",
		Function: schema.FunctionCall{
			Name:      tc.Name,
			Arguments: string(tc.Arguments),
		},
	}
}

// toolDefsToEino 统一工具定义 → eino ToolInfo。InputSchema 经 eino-contrib/jsonschema
// 解析为完整 JSON Schema（嵌套 properties 走 orderedmap，实现 json.Unmarshaler）；
// 解析失败返回 error——调用方（Client 入口 / ValidateTools）据此降级，不进保护链。
func toolDefsToEino(defs []tool.ToolDef) ([]*schema.ToolInfo, error) {
	out := make([]*schema.ToolInfo, 0, len(defs))
	for _, d := range defs {
		s := &jsonschema.Schema{}
		if len(d.InputSchema) > 0 && string(d.InputSchema) != "null" {
			if err := json.Unmarshal(d.InputSchema, s); err != nil {
				return nil, fmt.Errorf("tool %q input_schema: %w", d.Name, err)
			}
		}
		out = append(out, &schema.ToolInfo{
			Name: d.Name,
			Desc: d.Description,
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(s),
		})
	}
	return out, nil
}

// einoFinal eino 生成结果（Generate 返回 / ConcatMessages 聚合后的流终态）→ llm.Final。
func einoFinal(m *schema.Message) Final {
	f := Final{Content: m.Content}
	if m.ResponseMeta != nil {
		if u := m.ResponseMeta.Usage; u != nil {
			f.Usage = Usage{Input: int64(u.PromptTokens), Output: int64(u.CompletionTokens)}
		}
		f.FinishReason = m.ResponseMeta.FinishReason
	}
	if len(m.ToolCalls) > 0 {
		f.ToolCalls = make([]tool.ToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			f.ToolCalls = append(f.ToolCalls, tool.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}
	}
	return f
}
