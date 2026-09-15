package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 契约钉住测试：枚举常量与 DB CHECK / 前端契约对齐（防漂移）；SSE 事件 marshal 形态
// 与接口规范《对话接口》的事件表逐字段一致。

func TestRoleConstants(t *testing.T) {
	// 与 migrations/00006 的 CHECK (role IN ('user', 'assistant', 'tool')) 一致
	assert.Equal(t, []string{RoleUser, RoleAssistant, RoleTool},
		[]string{"user", "assistant", "tool"})
}

func TestEventConstants(t *testing.T) {
	// 与接口规范《对话接口》事件表一致；前端按 type 分支渲染
	assert.Equal(t, "delta", EventDelta)
	assert.Equal(t, "tool_call", EventToolCall)
	assert.Equal(t, "tool_result", EventToolResult)
	assert.Equal(t, "citations", EventCitations)
	assert.Equal(t, "done", EventDone)
	assert.Equal(t, "error", EventError)
}

func TestSentinelCodes(t *testing.T) {
	// Error() 即 error.code（MODULE_REASON 命名空间），须与 CLAUDE.md 错误码表一致
	assert.Equal(t, "CONVERSATION_NOT_FOUND", ErrConversationNotFound.Error())
	assert.Equal(t, "MODEL_CONTEXT_TOO_LONG", ErrModelContextTooLong.Error())
}

func TestStreamEventJSON(t *testing.T) {
	cases := []struct {
		name string
		evt  StreamEvent
		want string
	}{
		{"delta", DeltaEvent("你"), `{"type":"delta","content":"你","retryable":false}`},
		{"tool_call", ToolCallEvent("call_1", "query_orders", map[string]any{"day": "yesterday"}),
			`{"type":"tool_call","id":"call_1","tool":"query_orders","args":{"day":"yesterday"},"retryable":false}`},
		{"tool_result", ToolResultEvent("call_1", "query_orders", "3 rows"),
			`{"type":"tool_result","id":"call_1","tool":"query_orders","result":"3 rows","retryable":false}`},
		{"citations", CitationsEvent([]Citation{
			{DocumentID: "7", DocumentName: "deploy.md", Similarity: 0.87},
			{DocumentID: "9", DocumentName: "faq.md", Similarity: 0.82},
		}), `{"type":"citations","citations":[{"document_id":"7","document_name":"deploy.md","similarity":0.87},{"document_id":"9","document_name":"faq.md","similarity":0.82}],"retryable":false}`},
		{"done", DoneEvent("102", Usage{Input: 120, Output: 80}, "stop"),
			`{"type":"done","message_id":"102","usage":{"input":120,"output":80},"finish_reason":"stop","retryable":false}`},
		{"error", ErrorEvent("RATE_LIMITED", "供应商限流，请稍后再试", true),
			`{"type":"error","code":"RATE_LIMITED","message":"供应商限流，请稍后再试","retryable":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.evt)
			assert.NoError(t, err)
			assert.JSONEq(t, tc.want, string(b))
		})
	}
}

func TestSendMessageReqWantStream(t *testing.T) {
	assert.True(t, (&SendMessageReq{}).WantStream()) // 缺省 true
	f, f2 := false, true
	assert.False(t, (&SendMessageReq{Stream: &f}).WantStream()) // 显式 false
	assert.True(t, (&SendMessageReq{Stream: &f2}).WantStream()) // 显式 true
}
