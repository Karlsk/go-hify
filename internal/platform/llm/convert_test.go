package llm

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// ---- toEinoMessages ----

func TestToEinoMessages(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "你是助手"},
		{Role: RoleUser, Content: "问题"},
		{Role: RoleAssistant, Content: "前答"},
		{Role: RoleAssistant, ToolCalls: []tool.ToolCall{
			{ID: "call_1", Name: "search", Arguments: json.RawMessage(`{"q":"hi"}`)},
		}},
		{Role: RoleTool, Content: "结果", ToolCallID: "call_1", ToolName: "search"},
	}
	got := toEinoMessages(msgs)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	if got[0].Role != schema.System || got[0].Content != "你是助手" {
		t.Fatalf("system = %+v", got[0])
	}
	if got[1].Role != schema.User || got[1].Content != "问题" {
		t.Fatalf("user = %+v", got[1])
	}
	if got[2].Role != schema.Assistant || got[2].Content != "前答" {
		t.Fatalf("assistant = %+v", got[2])
	}
	// assistant 工具调用：ToolCall → eino FunctionCall（Arguments 是 JSON 字符串）
	tc := got[3].ToolCalls
	if len(tc) != 1 || tc[0].ID != "call_1" || tc[0].Function.Name != "search" ||
		tc[0].Function.Arguments != `{"q":"hi"}` || tc[0].Type != "function" {
		t.Fatalf("tool call = %+v", tc)
	}
	// tool 结果：ToolCallID / ToolName 回填（Gemini 等供应商要求）
	tm := got[4]
	if tm.Role != schema.Tool || tm.Content != "结果" || tm.ToolCallID != "call_1" || tm.ToolName != "search" {
		t.Fatalf("tool msg = %+v", tm)
	}
}

// 未知 role 按 assistant 兜底（防御：DB 不会产生，但转换层不 panic）。
func TestToEinoMessageUnknownRole(t *testing.T) {
	em := toEinoMessage(&Message{Role: Role("weird"), Content: "x"})
	if em.Role != schema.Assistant {
		t.Fatalf("role = %s, want assistant 兜底", em.Role)
	}
}

// ---- toolDefsToEino ----

func TestToolDefsToEino(t *testing.T) {
	defs := []tool.ToolDef{
		{
			Name:        "search",
			Description: "搜索",
			InputSchema: []byte(`{"type":"object","properties":{"q":{"type":"string","description":"关键词"}},"required":["q"]}`),
		},
		{Name: "noop"},                              // 空 schema = 无参
		{Name: "nullish", InputSchema: []byte(nil)}, // nil = 无参
	}
	infos, err := toolDefsToEino(defs)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("len = %d, want 3", len(infos))
	}
	if infos[0].Name != "search" || infos[0].Desc != "搜索" {
		t.Fatalf("info[0] = %+v", infos[0])
	}
	// 嵌套 properties 正确解析（orderedmap 实现 json.Unmarshaler）
	s, err := infos[0].ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema: %v", err)
	}
	if s == nil || s.Properties == nil || s.Properties.Len() != 1 {
		t.Fatalf("schema properties = %+v, want 1", s.Properties)
	}
	if s.Properties.Value("q") == nil {
		t.Fatalf("property q missing: %+v", s.Properties)
	}
	for i := 1; i < 3; i++ {
		es, err := infos[i].ParamsOneOf.ToJSONSchema()
		if err != nil || es == nil {
			t.Fatalf("info[%d]: 空 schema 应产生空 JSONSchema: (%v, %v)", i, es, err)
		}
	}
}

func TestToolDefsToEinoInvalidJSON(t *testing.T) {
	if _, err := toolDefsToEino([]tool.ToolDef{{Name: "bad", InputSchema: []byte(`{oops`)}}); err == nil {
		t.Fatal("want error for invalid json")
	}
	// 顶层非对象：json.Unmarshal 到 Schema 成功但语义坏，由 Registry 层挡（此处只管 JSON 层）
}

// ---- einoFinal ----

func TestEinoFinal(t *testing.T) {
	m := &schema.Message{
		Role:    schema.Assistant,
		Content: "答案",
		ResponseMeta: &schema.ResponseMeta{
			Usage:        &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 20},
			FinishReason: "stop",
		},
	}
	f := einoFinal(m)
	if f.Content != "答案" || f.Usage.Input != 10 || f.Usage.Output != 20 || f.FinishReason != "stop" {
		t.Fatalf("final = %+v", f)
	}
	if len(f.ToolCalls) != 0 {
		t.Fatalf("tool calls = %+v, want empty", f.ToolCalls)
	}
}

func TestEinoFinalWithToolCalls(t *testing.T) {
	m := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:   "call_9",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "search",
				Arguments: `{"q":"x"}`,
			},
		}},
	}
	f := einoFinal(m)
	if len(f.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(f.ToolCalls))
	}
	tc := f.ToolCalls[0]
	if tc.ID != "call_9" || tc.Name != "search" || string(tc.Arguments) != `{"q":"x"}` {
		t.Fatalf("tool call = %+v", tc)
	}
}

// ResponseMeta 缺失（部分供应商流式不回 usage）：零值不 panic。
func TestEinoFinalNoMeta(t *testing.T) {
	f := einoFinal(&schema.Message{Content: "x"})
	if f.Content != "x" || f.Usage != (Usage{}) || f.FinishReason != "" {
		t.Fatalf("final = %+v, want zero meta", f)
	}
}

// ---- Stream.Final 聚合（真实链路：chunks 经 Recv 收集 → ConcatMessages → einoFinal）----

// intPtr 测试辅助：schema.ToolCall.Index 是 *int。
func intPtr(i int) *int { return &i }

func TestStreamFinalAggregates(t *testing.T) {
	p := shortProfile()
	c := NewClient("p", p, okStreamer(
		&schema.Message{Role: schema.Assistant, Content: "你"},
		&schema.Message{Role: schema.Assistant, Content: "好"},
	))
	s, err := c.Stream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got []string
	for {
		delta, err := s.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		got = append(got, delta)
	}
	if !reflect.DeepEqual(got, []string{"你", "好"}) {
		t.Fatalf("deltas = %v", got)
	}
	f, err := s.Final()
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	if f.Content != "你好" {
		t.Fatalf("final content = %q, want 你好", f.Content)
	}
	// 幂等
	f2, _ := s.Final()
	if f2.Content != f.Content {
		t.Fatalf("Final 不幂等: %q vs %q", f2.Content, f.Content)
	}
}

// 工具调用增量（流式分片到达：先空 content 带 Index 0 的 ToolCall，再 Arguments 增量）
// → Final.ToolCalls 按 Index 合并、Arguments 拼接。语义来自 eino ConcatMessages。
func TestStreamFinalAggregatesToolCalls(t *testing.T) {
	p := shortProfile()
	c := NewClient("p", p, okStreamer(
		&schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "call_1", Type: "function",
			Function: schema.FunctionCall{Name: "search", Arguments: `{"q":`},
			Index:    intPtr(0),
		}}},
		&schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			Function: schema.FunctionCall{Arguments: `"hi"}`},
			Index:    intPtr(0),
		}}},
		&schema.Message{
			Role: schema.Assistant,
			ResponseMeta: &schema.ResponseMeta{
				Usage:        &schema.TokenUsage{PromptTokens: 5, CompletionTokens: 7},
				FinishReason: "tool_calls",
			},
		},
	))
	s, err := c.Stream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drain(t, s)
	f, err := s.Final()
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	if len(f.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1（Index 合并）: %+v", len(f.ToolCalls), f.ToolCalls)
	}
	tc := f.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "search" || string(tc.Arguments) != `{"q":"hi"}` {
		t.Fatalf("merged tool call = %+v", tc)
	}
	if f.Usage.Input != 5 || f.Usage.Output != 7 || f.FinishReason != "tool_calls" {
		t.Fatalf("usage/finish = %+v", f)
	}
}

// 未读尽 / 错误后调用 Final：返回已收 chunk 的部分聚合，不 panic。
func TestStreamFinalPartialAfterError(t *testing.T) {
	p := shortProfile()
	c := NewClient("p", p, chunkThenHangStreamer(msg("部分")))
	s, err := c.Stream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if _, err := s.Recv(); err != nil {
		t.Fatalf("first recv: %v", err)
	}
	_, _ = s.Recv() // idle 超时 → error
	f, ferr := s.Final()
	if ferr != nil {
		t.Fatalf("partial final err: %v", ferr)
	}
	if f.Content != "部分" {
		t.Fatalf("partial content = %q, want 部分", f.Content)
	}
}
