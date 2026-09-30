package service

// 工具循环测试（chat_tool_loop_spec.md §9）：绑定工具 agent 的多轮 ReAct——事件序列、
// 中间行落库、下一轮上下文回放、上限 nudge 收尾、降级链、错误轮语义、断连静默。
// 工具侧纯函数（parseToolArgs / truncateToolContent）一并在此覆盖。
// 未绑工具的回归由 turn_test.go 全体既有用例承担（toolSources 恒 nil、单轮路径）。

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"

	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// newToolService 绑定工具 agent + 可编程工具来源的标准装配。
func newToolService(t *testing.T, script func(call int) (*schema.StreamReader[*schema.Message], error), src tool.Source) *chatServiceForTest {
	t.Helper()
	return newServiceWithTools(t, toolAgent("7"), script, &stubToolSources{source: src})
}

// countingInvoke 记录调用次数的固定结果 invoke。
func countingInvoke(calls *int, result tool.ToolResult) func(tool.ToolCall) tool.ToolResult {
	return func(call tool.ToolCall) tool.ToolResult {
		*calls++
		result.CallID, result.Name = call.ID, call.Name
		return result
	}
}

// ---- 正常链路：触发 → 执行 → 回喂 → 终答 ----

func TestStreamToolRoundTrip(t *testing.T) {
	var invokeCalls int
	src := &stubToolSource{
		defs:  []tool.ToolDef{testToolDef("s1__weather")},
		invoke: countingInvoke(&invokeCalls, tool.ToolResult{Content: "北京 晴 25 度"}),
	}
	script := func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "s1__weather", `{"city":"北京"}`), tailChunk(10, 0, "tool_calls")), nil
		}
		if call == 2 {
			return chunkStream(delta("北京是"), delta("晴天"), tailChunk(8, 4, "stop")), nil
		}
		return nil, errors.New("script: unexpected third call")
	}
	h := newToolService(t, script, src)
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "北京天气怎么样"}, collectEvents(&events, ""))
	assert.NoError(t, err)

	// 事件序列：tool_call → tool_result → delta×2 → done（无 citations：无 KB 绑定）
	if assert.Len(t, events, 5) {
		assert.Equal(t, chatapi.EventToolCall, events[0].Type)
		assert.Equal(t, "c1", events[0].ID)
		assert.Equal(t, "s1__weather", events[0].Tool)
		assert.Equal(t, map[string]any{"city": "北京"}, events[0].Args)
		assert.Equal(t, chatapi.EventToolResult, events[1].Type)
		assert.Equal(t, "c1", events[1].ID)
		assert.Equal(t, chatapi.ToolResultPayload{Content: "北京 晴 25 度", IsError: false}, events[1].Result)
		assert.Equal(t, chatapi.EventDelta, events[2].Type)
		assert.Equal(t, chatapi.EventDone, events[4].Type)
	}
	done := events[4]
	assert.Equal(t, chatapi.Usage{Input: 18, Output: 4}, *done.Usage) // 两轮累计（10+8 / 0+4）
	assert.Equal(t, "stop", done.FinishReason)
	assert.NotEmpty(t, done.MessageID)

	// 工具来源按 agent 绑定 id 子集构造
	assert.Equal(t, 1, h.toolSources.(*stubToolSources).calls)
	assert.Equal(t, []uint64{7}, h.toolSources.(*stubToolSources).lastReq.ToolIDs)
	assert.Equal(t, 1, invokeCalls)

	// 第二轮上下文：system + user + assistant(空 content 带 tool_calls) + tool(结果)
	lastMsgs := h.streamer.lastMsgs()
	if assert.Len(t, lastMsgs, 4) {
		assert.Equal(t, "system:你是 Hify 助手 | user:北京天气怎么样 | assistant: | tool:北京 晴 25 度", roles(lastMsgs))
		if assert.Len(t, lastMsgs[2].ToolCalls, 1) {
			assert.Equal(t, "s1__weather", lastMsgs[2].ToolCalls[0].Function.Name)
			assert.JSONEq(t, `{"city":"北京"}`, lastMsgs[2].ToolCalls[0].Function.Arguments)
		}
		assert.Equal(t, "c1", lastMsgs[3].ToolCallID)
		assert.Equal(t, "s1__weather", lastMsgs[3].ToolName)
	}
	// 两轮的 CallOptions：首轮带工具、次轮同样带（工具清单贯穿循环直至上限）
	assert.Len(t, h.streamer.opts[0].Tools, 1)
	assert.Equal(t, "s1__weather", h.streamer.opts[0].Tools[0].Name)
	assert.Len(t, h.streamer.opts[1].Tools, 1)

	// 落库：user + assistant 中间行 + tool 行 + assistant 终答
	ms := h.store.messagesOf(convID)
	if assert.Len(t, ms, 4) {
		assert.Equal(t, chatapi.RoleUser, ms[0].Role)
		assert.Equal(t, chatapi.RoleAssistant, ms[1].Role)
		if assert.Len(t, ms[1].ToolCalls, 1) {
			assert.Equal(t, "c1", ms[1].ToolCalls[0]["id"])
			assert.Equal(t, "s1__weather", ms[1].ToolCalls[0]["tool"])
			assert.Equal(t, map[string]any{"city": "北京"}, ms[1].ToolCalls[0]["args"])
		}
		assert.Equal(t, chatapi.RoleTool, ms[2].Role)
		assert.Equal(t, "北京 晴 25 度", ms[2].Content)
		if assert.Len(t, ms[2].ToolCalls, 1) {
			assert.Equal(t, "c1", ms[2].ToolCalls[0]["id"])
			assert.Equal(t, "s1__weather", ms[2].ToolCalls[0]["tool"])
		}
		assert.Equal(t, chatapi.RoleAssistant, ms[3].Role)
		assert.Equal(t, "北京是晴天", ms[3].Content)
	}

	// executions：每轮一行（round1 tool_calls / round2 stop）
	assert.Len(t, h.execs.rows, 2)
	assert.Equal(t, "tool_calls", h.execs.rows[0].FinishReason)
	assert.Equal(t, "stop", h.execs.rows[1].FinishReason)
}

// 执行失败不中断对话：IsError 结果照常落库 / 事件 / 喂回，LLM 下一轮自纠。
func TestStreamToolFailureIsErrorFedBack(t *testing.T) {
	var invokeCalls int
	src := &stubToolSource{
		defs:  []tool.ToolDef{testToolDef("s1__weather")},
		invoke: countingInvoke(&invokeCalls, tool.ToolResult{Content: "连接失败: server unreachable", IsError: true}),
	}
	script := func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c9", "s1__weather", `{}`), tailChunk(5, 0, "tool_calls")), nil
		}
		return chunkStream(delta("工具暂时不可用"), tailChunk(5, 2, "stop")), nil
	}
	h := newToolService(t, script, src)
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "天气"}, collectEvents(&events, ""))
	assert.NoError(t, err)
	assert.Equal(t, 1, invokeCalls)

	assert.Equal(t, chatapi.EventToolResult, events[1].Type)
	payload, ok := events[1].Result.(chatapi.ToolResultPayload)
	if assert.True(t, ok) {
		assert.Equal(t, "连接失败: server unreachable", payload.Content)
		assert.True(t, payload.IsError)
	}

	// tool 行照常落库（内容即错误文本），终答照常产生
	ms := h.store.messagesOf(convID)
	if assert.Len(t, ms, 4) {
		assert.Equal(t, chatapi.RoleTool, ms[2].Role)
		assert.Equal(t, "连接失败: server unreachable", ms[2].Content)
		assert.Equal(t, "工具暂时不可用", ms[3].Content)
	}
}

// 参数非法（非 JSON 对象）：不发起调用，IsError 结果直接喂回。
func TestStreamToolInvalidArgsNoInvoke(t *testing.T) {
	var invokeCalls int
	src := &stubToolSource{
		defs:  []tool.ToolDef{testToolDef("s1__weather")},
		invoke: countingInvoke(&invokeCalls, tool.ToolResult{Content: "unreachable"}),
	}
	script := func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "s1__weather", `{"city":`), tailChunk(3, 0, "tool_calls")), nil
		}
		return chunkStream(delta("重新组织了参数"), tailChunk(3, 1, "stop")), nil
	}
	h := newToolService(t, script, src)
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "天气"}, collectEvents(&events, ""))
	assert.NoError(t, err)

	assert.Zero(t, invokeCalls) // 未触达工具：解析失败即挡下
	assert.Equal(t, chatapi.EventToolResult, events[1].Type)
	payload, ok := events[1].Result.(chatapi.ToolResultPayload)
	if assert.True(t, ok) {
		assert.Equal(t, invalidToolArgsMsg, payload.Content)
		assert.True(t, payload.IsError)
	}

	// 中间行 args 归一 {}（DB 行不落 null）；tool 行内容即错误文本
	ms := h.store.messagesOf(convID)
	if assert.Len(t, ms, 4) {
		assert.Equal(t, map[string]any{}, ms[1].ToolCalls[0]["args"])
		assert.Equal(t, invalidToolArgsMsg, ms[2].Content)
	}
}

// 轮次上限：8 轮工具调用后第 9 轮摘工具 + nudge 逼出文本终答（finish=max_iterations，
// nudge 瞬态不落库）；即便上限轮仍幻觉出调用也强制收尾。
func TestStreamToolRoundsLimit(t *testing.T) {
	src := &stubToolSource{
		defs: []tool.ToolDef{testToolDef("s1__weather")},
		invoke: func(call tool.ToolCall) tool.ToolResult {
			return tool.ToolResult{CallID: call.ID, Name: call.Name, Content: "第" + call.ID + "次结果"}
		},
	}
	script := func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call <= 8 {
			// 每轮一个新调用 id（同 id 聚合语义不受影响）
			id := "c" + strings.Repeat("x", call)
			return chunkStream(toolCallChunk(id, "s1__weather", `{"n":1}`), tailChunk(1, 0, "tool_calls")), nil
		}
		if call == 9 {
			// 上限轮仍返回调用决策：防御路径——必须强制收尾
			return chunkStream(toolCallChunk("c9", "s1__weather", `{}`), tailChunk(1, 0, "tool_calls")), nil
		}
		return nil, errors.New("script: unexpected tenth call")
	}
	h := newToolService(t, script, src)
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "查天气"}, collectEvents(&events, ""))
	assert.NoError(t, err)

	assert.Equal(t, 9, h.streamer.calls) // 8 工具轮 + 1 上限收尾轮

	// 上限轮请求：末条是 nudge（瞬态）、Tools 被摘掉
	lastMsgs := h.streamer.lastMsgs()
	assert.Equal(t, schema.User, lastMsgs[len(lastMsgs)-1].Role)
	assert.Equal(t, maxToolRoundsNudge, lastMsgs[len(lastMsgs)-1].Content)
	assert.Empty(t, h.streamer.opts[8].Tools)
	assert.Len(t, h.streamer.opts[7].Tools, 1) // 常规轮照常带工具

	// done：强制 finish_reason（覆盖模型原值 tool_calls）
	done := events[len(events)-1]
	assert.Equal(t, chatapi.EventDone, done.Type)
	assert.Equal(t, finishReasonMaxIterations, done.FinishReason)

	// 落库：user + 8×(assistant+tool) + 空终答；nudge 不在其中
	ms := h.store.messagesOf(convID)
	assert.Len(t, ms, 18)
	for _, m := range ms {
		assert.NotEqual(t, maxToolRoundsNudge, m.Content) // nudge 瞬态：只进 LLM 请求不落库
	}
	assert.Equal(t, chatapi.RoleAssistant, ms[17].Role)
	assert.Equal(t, "", ms[17].Content) // 上限轮无文本产出：空终答行（answered=true）

	// executions：9 行（每轮各记）
	assert.Len(t, h.execs.rows, 9)
}

// ---- 降级链：工具不是对话硬依赖 ----

// Registry 构建失败（工具来源报错）→ WARN 后照常单轮对话。
func TestStreamToolRegistryFailureDegrades(t *testing.T) {
	h := newServiceWithTools(t, toolAgent("7"), happyScript,
		&stubToolSources{err: errors.New("mcp: db down")})
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "打个招呼"}, collectEvents(&events, ""))
	assert.NoError(t, err)

	assert.Equal(t, 1, h.streamer.calls)          // 单轮：工具不可用不进循环
	assert.Empty(t, h.streamer.opts[0].Tools)     // 无工具下发
	assert.Equal(t, chatapi.EventDone, events[len(events)-1].Type)
	ms := h.store.messagesOf(convID)
	assert.Len(t, ms, 2) // user + assistant：无中间行
}

// 工具来源工厂未注入（装配缺位）+ agent 绑了工具 → 同样降级照常对话。
func TestStreamToolSourcesMissingDegrades(t *testing.T) {
	h := newServiceWithTools(t, toolAgent("7"), happyScript, nil)
	convID := createConv(t, h.svc)

	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "打个招呼"}, func(chatapi.StreamEvent) error { return nil })
	assert.NoError(t, err)
	assert.Equal(t, 1, h.streamer.calls)
	assert.Empty(t, h.streamer.opts[0].Tools)
	assert.Len(t, h.store.messagesOf(convID), 2)
}

// ---- 错误轮（≥1）与断连 ----

// 轮次 ≥1 建流失败：error 事件收尾（SSE 200 已提交），中间行保留、无终答行。
func TestStreamToolRoundStreamError(t *testing.T) {
	src := &stubToolSource{
		defs: []tool.ToolDef{testToolDef("s1__weather")},
		invoke: func(call tool.ToolCall) tool.ToolResult {
			return tool.ToolResult{CallID: call.ID, Name: call.Name, Content: "北京 晴"}
		},
	}
	script := func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "s1__weather", `{}`), tailChunk(4, 0, "tool_calls")), nil
		}
		return nil, &llm.Error{Class: llm.ClassNetwork, Err: errors.New("connection reset")}
	}
	h := newToolService(t, script, src)
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "天气"}, collectEvents(&events, ""))
	assert.NoError(t, err) // 流中错误：error 事件表达，error 返回恒 nil

	// 事件：tool_call / tool_result 之后是 error（无 done）
	last := events[len(events)-1]
	assert.Equal(t, chatapi.EventError, last.Type)
	assert.Equal(t, "PROVIDER_UNAVAILABLE", last.Code)
	assert.True(t, last.Retryable)

	// 中间行保留（重放合法：assistant(tool_calls) 后有 tool 行），无终答行
	ms := h.store.messagesOf(convID)
	if assert.Len(t, ms, 3) {
		assert.Equal(t, chatapi.RoleAssistant, ms[1].Role)
		assert.Equal(t, chatapi.RoleTool, ms[2].Role)
	}

	// executions：两轮各一行，第二轮带 error_class
	assert.Len(t, h.execs.rows, 2)
	if assert.NotNil(t, h.execs.rows[1].ErrorClass) {
		assert.Equal(t, "Network", *h.execs.rows[1].ErrorClass)
	}
}

// 工具阶段前端断连：静默收尾——中间 assistant 行已落、不执行剩余调用、无终答行。
func TestStreamToolRoundClientGone(t *testing.T) {
	var invokeCalls int
	src := &stubToolSource{
		defs:  []tool.ToolDef{testToolDef("s1__weather")},
		invoke: countingInvoke(&invokeCalls, tool.ToolResult{Content: "unreachable"}),
	}
	script := func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "s1__weather", `{}`), tailChunk(4, 0, "tool_calls")), nil
		}
		return nil, errors.New("script: unexpected second call")
	}
	h := newToolService(t, script, src)
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "天气"},
		collectEvents(&events, chatapi.EventToolCall)) // tool_call 事件写失败 = 断连
	assert.NoError(t, err)                             // 静默收尾
	assert.Empty(t, events)                            // 首个事件即失败：无任何事件送达
	assert.Zero(t, invokeCalls)                        // 不执行剩余调用（省一次工具 IO）

	// user + assistant 中间行；无 tool 行、无终答行
	ms := h.store.messagesOf(convID)
	if assert.Len(t, ms, 2) {
		assert.Equal(t, chatapi.RoleUser, ms[0].Role)
		assert.Equal(t, chatapi.RoleAssistant, ms[1].Role)
		assert.Len(t, ms[1].ToolCalls, 1)
	}
	assert.Equal(t, 1, h.streamer.calls)
	assert.Len(t, h.execs.rows, 1) // 首轮成功记录
}

// 一次输出模式：中间行照常落库（不推送事件），终答经标准信封返回。
func TestSendMessageToolMiddleRows(t *testing.T) {
	src := &stubToolSource{
		defs: []tool.ToolDef{testToolDef("s1__weather")},
		invoke: func(call tool.ToolCall) tool.ToolResult {
			return tool.ToolResult{CallID: call.ID, Name: call.Name, Content: "北京 晴 25 度"}
		},
	}
	script := func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "s1__weather", `{"city":"北京"}`), tailChunk(10, 0, "tool_calls")), nil
		}
		return chunkStream(delta("北京是晴天"), tailChunk(8, 4, "stop")), nil
	}
	h := newToolService(t, script, src)
	convID := createConv(t, h.svc)

	reply, err := h.svc.SendMessage(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "北京天气怎么样"})
	assert.NoError(t, err)
	assert.Equal(t, "北京是晴天", reply.Content)
	assert.Equal(t, chatapi.Usage{Input: 18, Output: 4}, reply.Usage)
	assert.Equal(t, "stop", reply.FinishReason)
	assert.NotEmpty(t, reply.MessageID)

	// 中间行与流式模式完全一致（两模式共用 runToolRound）
	ms := h.store.messagesOf(convID)
	if assert.Len(t, ms, 4) {
		assert.Equal(t, chatapi.RoleAssistant, ms[1].Role)
		assert.Equal(t, chatapi.RoleTool, ms[2].Role)
		assert.Equal(t, "北京 晴 25 度", ms[2].Content)
		assert.Equal(t, chatapi.RoleAssistant, ms[3].Role)
	}
}

// ---- 纯函数 ----

func TestParseToolArgs(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    map[string]any
		wantOK  bool
	}{
		{"空", "", map[string]any{}, true},
		{"null", "null", map[string]any{}, true},
		{"空对象", "{}", map[string]any{}, true},
		{"合法对象", `{"city":"北京"}`, map[string]any{"city": "北京"}, true},
		{"截断 JSON", `{"city":`, map[string]any{}, false},
		{"数组非对象", `[1,2]`, map[string]any{}, false},
		{"字符串非对象", `"x"`, map[string]any{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args, ok := parseToolArgs(json.RawMessage(c.raw))
			assert.Equal(t, c.wantOK, ok)
			assert.Equal(t, c.want, args)
		})
	}
}

func TestTruncateToolContent(t *testing.T) {
	assert.Equal(t, "短文本", truncateToolContent("短文本"))

	long := strings.Repeat("长", maxToolResultRunes+100)
	got := truncateToolContent(long)
	assert.Len(t, []rune(got), maxToolResultRunes+1) // 截断 + 省略号
	assert.True(t, strings.HasSuffix(got, "…"))
	assert.Equal(t, strings.Repeat("长", maxToolResultRunes), strings.TrimSuffix(got, "…"))
}
