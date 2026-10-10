package reactloop

// 循环本体测试（spec 019 §SC-002，场景族移植自 chat toolloop_test）：轮循环 /
// 上限 nudge 收尾 / 错误即上下文 / 参数非法不发起调用 / 回调中止 / nil 与空清单
// 降级 / 截断保真。stub llm 上游（eino schema 流）+ stub tool.Source——零真实
// LLM / MCP / PG（test 包 import eino 属 grep 纪律豁免面）。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// ---- stub 基建 ----

// stubUpstream 可编程 fake 上游：Stream / Generate 双脚本（双模式测试共用），
// 记录每轮收到的消息与选项（上下文回放与 opts 断言用）。
type stubUpstream struct {
	mu     sync.Mutex
	calls  int
	msgs   [][]*schema.Message
	opts   []*llm.CallOptions
	stream func(call int) (*schema.StreamReader[*schema.Message], error)
	gen    func(call int) (*schema.Message, error)
}

func (f *stubUpstream) record(msgs []*schema.Message, opts *llm.CallOptions) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.msgs = append(f.msgs, msgs)
	f.opts = append(f.opts, opts)
	return f.calls
}

func (f *stubUpstream) Stream(_ context.Context, msgs []*schema.Message, opts *llm.CallOptions) (*schema.StreamReader[*schema.Message], error) {
	call := f.record(msgs, opts)
	if f.stream == nil {
		return nil, errors.New("stubUpstream: stream not scripted")
	}
	return f.stream(call)
}

func (f *stubUpstream) Generate(_ context.Context, msgs []*schema.Message, opts *llm.CallOptions) (*schema.Message, error) {
	call := f.record(msgs, opts)
	if f.gen == nil {
		return nil, errors.New("stubUpstream: generate not scripted")
	}
	return f.gen(call)
}

func (f *stubUpstream) lastMsgs() []*schema.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.msgs) == 0 {
		return nil
	}
	return f.msgs[len(f.msgs)-1]
}

func (f *stubUpstream) lastOpts() *llm.CallOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.opts) == 0 {
		return nil
	}
	return f.opts[len(f.opts)-1]
}

// chunkStream 把既定 chunks 包装成 eino 流（发完即 EOF）。
func chunkStream(chunks ...*schema.Message) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](len(chunks) + 1)
	go func() {
		for _, c := range chunks {
			w.Send(c, nil)
		}
		w.Close()
	}()
	return r
}

// failAfterStream 发出 first 后流中报错（流中段失败场景）。
func failAfterStream(first *schema.Message, err error) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](2)
	go func() {
		w.Send(first, nil)
		w.Send(nil, err)
		w.Close()
	}()
	return r
}

func delta(s string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: s}
}

func tailChunk(prompt, completion int, finish string) *schema.Message {
	return &schema.Message{
		Role:         schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{FinishReason: finish, Usage: &schema.TokenUsage{PromptTokens: prompt, CompletionTokens: completion}},
	}
}

func toolCallChunk(id, name, args string) *schema.Message {
	return &schema.Message{
		Role:      schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}},
	}
}

// genMsg Generate 模式的整帧消息（content + 可选 tool calls + usage/finish）。
func genMsg(content string, calls []schema.ToolCall, prompt, completion int, finish string) *schema.Message {
	return &schema.Message{
		Role:         schema.Assistant,
		Content:      content,
		ToolCalls:    calls,
		ResponseMeta: &schema.ResponseMeta{FinishReason: finish, Usage: &schema.TokenUsage{PromptTokens: prompt, CompletionTokens: completion}},
	}
}

// stubSource 可控工具来源（清单 / Invoke 结果 / 调用记录）。
type stubSource struct {
	defs    []tool.ToolDef
	result  tool.ToolResult // 固定结果（CallID/Name 回填）
	invokes []tool.ToolCall
}

func (s *stubSource) Tools(context.Context) ([]tool.ToolDef, error) { return s.defs, nil }

func (s *stubSource) Invoke(_ context.Context, c tool.ToolCall) tool.ToolResult {
	s.invokes = append(s.invokes, c)
	r := s.result
	r.CallID, r.Name = c.ID, c.Name
	return r
}

func testToolDef(name string) tool.ToolDef {
	return tool.ToolDef{
		Name:        name,
		Description: "测试工具 " + name,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
	}
}

// fastProfile 测试用快 Profile：单次尝试、毫秒级超时（对齐 chat/workflow 测试先例）。
func fastProfile() llm.Profile {
	p := llm.DefaultProfile()
	p.MaxRetries = 0
	p.TTFT, p.Idle, p.Overall = 500*time.Millisecond, 500*time.Millisecond, 2*time.Second
	p.AcquireTimeout, p.BackoffBase, p.BackoffCap = 100*time.Millisecond, time.Millisecond, 5*time.Millisecond
	p.BreakerAfter, p.BreakerCooldown = 3, 100*time.Millisecond
	return p
}

func newTestClient(up *stubUpstream) *llm.Client {
	return llm.NewClient("test", fastProfile(), up)
}

// recorder 五钩记录器（断言触发序与产物）。
type recorder struct {
	deltas          []string
	roundStarts     []int
	rounds          []roundRec
	toolRoundStarts []toolRoundStartRec
	toolCalls       []CallView
	toolResults     []tool.ToolResult
	abortOnCall     int // >0：第 N 次 OnToolCall 返回错误（中止语义测试）
	callCount       int
}

type roundRec struct {
	round   int
	content string
	calls   int
	err     error
}

type toolRoundStartRec struct {
	round   int
	content string
	calls   []CallView
}

func (r *recorder) hooks() Hooks {
	return Hooks{
		OnDelta: func(d string) error {
			r.deltas = append(r.deltas, d)
			return nil
		},
		OnRoundStart: func(round int) error {
			r.roundStarts = append(r.roundStarts, round)
			return nil
		},
		OnRound: func(round int, _ []llm.Message, res *llm.Final, callErr error) error {
			rec := roundRec{round: round, err: callErr}
			if res != nil {
				rec.content = res.Content
				rec.calls = len(res.ToolCalls)
			}
			r.rounds = append(r.rounds, rec)
			return nil
		},
		OnToolRoundStart: func(round int, content string, calls []CallView) error {
			r.toolRoundStarts = append(r.toolRoundStarts, toolRoundStartRec{round: round, content: content, calls: calls})
			return nil
		},
		OnToolCall: func(v CallView) error {
			r.callCount++
			if r.abortOnCall > 0 && r.callCount == r.abortOnCall {
				return errors.New("hook abort")
			}
			r.toolCalls = append(r.toolCalls, v)
			return nil
		},
		OnToolResult: func(_ CallView, res tool.ToolResult) error {
			r.toolResults = append(r.toolResults, res)
			return nil
		},
	}
}

func testOpts(defs []tool.ToolDef) *llm.CallOptions {
	return &llm.CallOptions{Tools: defs}
}

// ---- 场景族 ----

// ① 两轮工具循环 RoundTrip：消息序追加正确、终稿 / usage 累计、钩子序完整。
func TestRunRoundTrip(t *testing.T) {
	src := &stubSource{defs: []tool.ToolDef{testToolDef("s1__weather")}, result: tool.ToolResult{Content: "北京 晴 25 度"}}
	up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "s1__weather", `{"city":"北京"}`), tailChunk(10, 0, "tool_calls")), nil
		}
		if call == 2 {
			return chunkStream(delta("北京是"), delta("晴天"), tailChunk(8, 4, "stop")), nil
		}
		return nil, errors.New("script: unexpected third call")
	}}
	reg, err := tool.NewRegistry(context.Background(), src)
	require.NoError(t, err)
	rec := &recorder{}

	res, err := Run(context.Background(), newTestClient(up), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "北京天气怎么样"}},
		testOpts(reg.Defs()), Config{Stream: true}, rec.hooks())
	require.NoError(t, err)

	assert.Equal(t, "北京是晴天", res.Content)
	assert.Equal(t, llm.Usage{Input: 18, Output: 4}, res.Usage, "两轮累计")
	assert.Equal(t, 2, res.Rounds)
	assert.Equal(t, "stop", res.FinishReason)
	assert.False(t, res.HitLimit)
	assert.False(t, res.Cancelled)
	assert.False(t, res.Aborted)

	// 第二轮上下文：user + assistant(tool_calls) + tool(结果)
	last := up.lastMsgs()
	require.Len(t, last, 3)
	assert.Equal(t, schema.Assistant, last[1].Role)
	require.Len(t, last[1].ToolCalls, 1)
	assert.Equal(t, "s1__weather", last[1].ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"city":"北京"}`, last[1].ToolCalls[0].Function.Arguments)
	assert.Equal(t, schema.Tool, last[2].Role)
	assert.Equal(t, "北京 晴 25 度", last[2].Content)
	assert.Equal(t, "c1", last[2].ToolCallID)

	// 钩子序：roundStart(0) → round(0, 1 call) → toolRoundStart → toolCall → toolResult → roundStart(1) → round(1, 0 call)
	assert.Equal(t, []int{0, 1}, rec.roundStarts)
	require.Len(t, rec.rounds, 2)
	assert.Equal(t, 1, rec.rounds[0].calls)
	assert.Equal(t, 0, rec.rounds[1].calls)
	require.Len(t, rec.toolRoundStarts, 1)
	assert.Equal(t, 0, rec.toolRoundStarts[0].round)
	require.Len(t, rec.toolRoundStarts[0].calls, 1)
	assert.True(t, rec.toolRoundStarts[0].calls[0].ArgsValid)
	assert.Equal(t, map[string]any{"city": "北京"}, rec.toolRoundStarts[0].calls[0].Args)
	require.Len(t, rec.toolCalls, 1)
	assert.Equal(t, "c1", rec.toolCalls[0].Call.ID)
	require.Len(t, rec.toolResults, 1)
	assert.Equal(t, "北京 晴 25 度", rec.toolResults[0].Content)
	assert.False(t, rec.toolResults[0].IsError)
	assert.Equal(t, []string{"北京是", "晴天"}, rec.deltas, "终答轮 delta 逐帧")

	// 入参 msgs 不被修改（调用方 slice 零副作用）
	assert.Len(t, up.msgs[0], 1, "首轮仅初始消息")
}

// ② 轮上限 nudge 收尾：nudge 瞬态（仅收尾轮可见）+ 摘工具新建 opts + HitLimit +
// finish 标识；工具清单贯穿至上限轮前。
func TestRunRoundsLimit(t *testing.T) {
	src := &stubSource{defs: []tool.ToolDef{testToolDef("t")}, result: tool.ToolResult{Content: "r"}}
	up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call <= 2 { // 两轮工具调用；第三轮 = 上限收尾轮（摘工具后模型出终答）
			return chunkStream(toolCallChunk("c", "t", `{}`), tailChunk(1, 0, "tool_calls")), nil
		}
		if call == 3 {
			return chunkStream(delta("终答"), tailChunk(2, 1, "stop")), nil
		}
		return nil, errors.New("script: unexpected fourth call")
	}}
	reg, err := tool.NewRegistry(context.Background(), src)
	require.NoError(t, err)
	rec := &recorder{}

	res, err := Run(context.Background(), newTestClient(up), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
		testOpts(reg.Defs()), Config{Stream: true, MaxRounds: 2}, rec.hooks())
	require.NoError(t, err)

	assert.True(t, res.HitLimit, "上限触发标识")
	assert.Equal(t, FinishReasonMaxIterations, res.FinishReason)
	assert.Equal(t, "终答", res.Content)
	assert.Equal(t, 3, res.Rounds, "两轮工具 + 一轮收尾")

	// 收尾轮：nudge 在当轮消息尾、opts 无工具；且 nudge 不在工具轮消息里
	last := up.lastMsgs()
	require.NotEmpty(t, last)
	assert.Equal(t, NudgeText, last[len(last)-1].Content, "nudge 瞬态追加在收尾轮")
	assert.Empty(t, up.lastOpts().Tools, "收尾轮摘工具（新建 opts）")
	assert.Len(t, up.opts[0].Tools, 1, "首轮带工具")
	assert.Len(t, up.opts[1].Tools, 1, "第二轮仍带工具")
	// 第二轮消息不含 nudge（瞬态不累积）
	for _, m := range up.msgs[1] {
		assert.NotEqual(t, NudgeText, m.Content, "nudge 不入后续轮上下文")
	}
}

// ③ 错误即上下文：Invoke IsError 喂回、循环不中断、终答正常。
func TestRunErrorFedBack(t *testing.T) {
	src := &stubSource{defs: []tool.ToolDef{testToolDef("t")}, result: tool.ToolResult{Content: "上游炸了", IsError: true}}
	up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "t", `{}`), tailChunk(1, 0, "tool_calls")), nil
		}
		return chunkStream(delta("抱歉失败"), tailChunk(2, 1, "stop")), nil
	}}
	reg, _ := tool.NewRegistry(context.Background(), src)
	rec := &recorder{}

	res, err := Run(context.Background(), newTestClient(up), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
		testOpts(reg.Defs()), Config{Stream: true}, rec.hooks())
	require.NoError(t, err, "工具失败不中断循环")
	assert.Equal(t, "抱歉失败", res.Content)
	require.Len(t, rec.toolResults, 1)
	assert.True(t, rec.toolResults[0].IsError)
	// 错误文本回喂进第二轮上下文
	last := up.lastMsgs()
	require.Len(t, last, 3)
	assert.Equal(t, "上游炸了", last[2].Content)
}

// ④ 参数非法不发起调用：InvalidArgs 直喂、Invoke 零记录、Args 归一 {}。
func TestRunInvalidArgsNoInvoke(t *testing.T) {
	src := &stubSource{defs: []tool.ToolDef{testToolDef("t")}, result: tool.ToolResult{Content: "r"}}
	up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "t", `not-json`), tailChunk(1, 0, "tool_calls")), nil
		}
		return chunkStream(delta("终答"), tailChunk(2, 1, "stop")), nil
	}}
	reg, _ := tool.NewRegistry(context.Background(), src)
	rec := &recorder{}

	res, err := Run(context.Background(), newTestClient(up), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
		testOpts(reg.Defs()), Config{Stream: true}, rec.hooks())
	require.NoError(t, err)
	assert.Empty(t, src.invokes, "参数非法不发起真实调用")
	require.Len(t, rec.toolResults, 1)
	assert.True(t, rec.toolResults[0].IsError)
	assert.Equal(t, InvalidArgsText, rec.toolResults[0].Content)
	require.Len(t, rec.toolCalls, 1)
	assert.False(t, rec.toolCalls[0].ArgsValid)
	assert.Equal(t, map[string]any{}, rec.toolCalls[0].Args, "非法参数归一 {}（不落 null 形态）")
	assert.Equal(t, "终答", res.Content)
}

// ⑤ 回调错误中止：OnToolCall 返回 error → 剩余调用不执行、Result.Aborted。
func TestRunHookAbort(t *testing.T) {
	src := &stubSource{defs: []tool.ToolDef{testToolDef("t")}, result: tool.ToolResult{Content: "r"}}
	up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(
				toolCallChunk("c1", "t", `{}`),
				toolCallChunk("c2", "t", `{}`),
				tailChunk(1, 0, "tool_calls")), nil
		}
		return nil, errors.New("script: loop must not continue after abort")
	}}
	reg, _ := tool.NewRegistry(context.Background(), src)
	rec := &recorder{abortOnCall: 1}

	res, err := Run(context.Background(), newTestClient(up), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
		testOpts(reg.Defs()), Config{Stream: true}, rec.hooks())
	require.Error(t, err, "钩子错误上抛")
	assert.True(t, res.Aborted)
	assert.Empty(t, src.invokes, "中止后零工具调用")
	assert.Equal(t, 1, up.calls, "中止后不再发起 LLM 轮")
}

// ⑥ Registry nil 与空清单：单轮直答、零工具钩子。
func TestRunNoTools(t *testing.T) {
	t.Run("reg nil", func(t *testing.T) {
		up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
			return chunkStream(toolCallChunk("c1", "ghost", `{}`), tailChunk(1, 0, "tool_calls")), nil
		}}
		rec := &recorder{}
		res, err := Run(context.Background(), newTestClient(up), nil,
			[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
			&llm.CallOptions{}, Config{Stream: true}, rec.hooks())
		require.NoError(t, err)
		assert.Equal(t, 1, res.Rounds, "无工具单轮收尾（幻觉调用不进循环）")
		assert.Empty(t, rec.toolRoundStarts)
	})
	t.Run("空清单 registry", func(t *testing.T) {
		up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
			return chunkStream(delta("直答"), tailChunk(1, 1, "stop")), nil
		}}
		reg, err := tool.NewRegistry(context.Background(), &stubSource{defs: nil})
		require.NoError(t, err)
		rec := &recorder{}
		res, err := Run(context.Background(), newTestClient(up), reg,
			[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
			&llm.CallOptions{}, Config{Stream: true}, rec.hooks())
		require.NoError(t, err)
		assert.Equal(t, "直答", res.Content)
		assert.Empty(t, rec.toolRoundStarts)
	})
}

// ⑦ 截断保真：超 MaxResultRunes 结果——OnToolResult 收到与回喂消息同一份截断产物。
func TestRunTruncation(t *testing.T) {
	long := strings.Repeat("长", MaxResultRunes+5000)
	src := &stubSource{defs: []tool.ToolDef{testToolDef("t")}, result: tool.ToolResult{Content: long}}
	up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "t", `{}`), tailChunk(1, 0, "tool_calls")), nil
		}
		return chunkStream(delta("终答"), tailChunk(2, 1, "stop")), nil
	}}
	reg, _ := tool.NewRegistry(context.Background(), src)
	rec := &recorder{}

	_, err := Run(context.Background(), newTestClient(up), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
		testOpts(reg.Defs()), Config{Stream: true}, rec.hooks())
	require.NoError(t, err)

	require.Len(t, rec.toolResults, 1)
	got := rec.toolResults[0].Content
	assert.LessOrEqual(t, len([]rune(got)), MaxResultRunes+1, "截断 + 省略号")
	assert.True(t, strings.HasSuffix(got, "…"))
	// 回喂消息同一份截断产物
	last := up.lastMsgs()
	require.Len(t, last, 3)
	assert.Equal(t, got, last[2].Content, "回喂与回调同一份截断文本")
}

// ---- spec 019 US3：双模式一致 / 取消语义 / 流中段部分产物（FR-006 / FR-007）----

// twoRoundScripts 双模式共用的两轮工具图景：首轮单调用、次轮终答。
func twoRoundStream(call int) (*schema.StreamReader[*schema.Message], error) {
	if call == 1 {
		return chunkStream(toolCallChunk("c1", "t", `{"k":"v"}`), tailChunk(10, 0, "tool_calls")), nil
	}
	if call == 2 {
		return chunkStream(delta("终答"), tailChunk(8, 4, "stop")), nil
	}
	return nil, errors.New("script: unexpected call")
}

func twoRoundGen(call int) (*schema.Message, error) {
	if call == 1 {
		return genMsg("", []schema.ToolCall{{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "t", Arguments: `{"k":"v"}`}}}, 10, 0, "tool_calls"), nil
	}
	if call == 2 {
		return genMsg("终答", nil, 8, 4, "stop"), nil
	}
	return nil, errors.New("script: unexpected call")
}

// ⑧ 双模式一致：同图景 Stream / Generate 两跑——Content / Usage / Rounds / 工具轮
// 消息序一致，仅 OnDelta 有无之差。
func TestRunDualModeParity(t *testing.T) {
	run := func(stream bool) (Result, *recorder, *stubUpstream) {
		src := &stubSource{defs: []tool.ToolDef{testToolDef("t")}, result: tool.ToolResult{Content: "r"}}
		up := &stubUpstream{}
		if stream {
			up.stream = twoRoundStream
		} else {
			up.gen = twoRoundGen
		}
		reg, err := tool.NewRegistry(context.Background(), src)
		require.NoError(t, err)
		rec := &recorder{}
		res, err := Run(context.Background(), newTestClient(up), reg,
			[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
			testOpts(reg.Defs()), Config{Stream: stream}, rec.hooks())
		require.NoError(t, err)
		return res, rec, up
	}
	sRes, sRec, sUp := run(true)
	gRes, gRec, gUp := run(false)

	assert.Equal(t, sRes.Content, gRes.Content)
	assert.Equal(t, sRes.Usage, gRes.Usage)
	assert.Equal(t, sRes.Rounds, gRes.Rounds)
	assert.Equal(t, sRes.FinishReason, gRes.FinishReason)
	assert.False(t, gRes.HitLimit)
	assert.Equal(t, []string{"终答"}, sRec.deltas, "流式逐 delta")
	assert.Empty(t, gRec.deltas, "非流式零 delta 回调")
	// 工具轮消息序一致：user + assistant(tool_calls) + tool
	assert.Equal(t, len(sUp.msgs[1]), len(gUp.msgs[1]))
	assert.Equal(t, schema.Tool, gUp.msgs[1][2].Role)
	assert.Equal(t, len(sRec.toolResults), len(gRec.toolResults))
}

// ⑨ 轮间取消：首轮工具轮收尾钩子里取消 ctx → 二轮零 LLM 调用、Result.Cancelled、
// nil error（调用方静默路径标识）。
func TestRunCancelledBetweenRounds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &stubSource{defs: []tool.ToolDef{testToolDef("t")}, result: tool.ToolResult{Content: "r"}}
	up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
		return chunkStream(toolCallChunk("c1", "t", `{}`), tailChunk(1, 0, "tool_calls")), nil
	}}
	reg, _ := tool.NewRegistry(context.Background(), src)
	rec := &recorder{}
	hooks := rec.hooks()
	origToolResult := hooks.OnToolResult
	hooks.OnToolResult = func(v CallView, r tool.ToolResult) error {
		cancel() // 调用方在工具结果后断连 / 取消
		return origToolResult(v, r)
	}

	res, err := Run(ctx, newTestClient(up), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
		testOpts(reg.Defs()), Config{Stream: true}, hooks)
	require.NoError(t, err, "取消不报错（静默路径标识）")
	assert.True(t, res.Cancelled)
	assert.False(t, res.Aborted)
	assert.Equal(t, 1, up.calls, "取消后不再发起下一轮")
	assert.Equal(t, "", res.Content, "取消路径无终答")
}

// ⑩ 流中段失败：第二轮 Recv 中途上游错误 → 部分 Content/Usage 返回 + err 上抛 +
// 首轮工具轮产物完整。
func TestRunMidStreamPartial(t *testing.T) {
	src := &stubSource{defs: []tool.ToolDef{testToolDef("t")}, result: tool.ToolResult{Content: "r"}}
	boom := errors.New("upstream boom")
	up := &stubUpstream{stream: func(call int) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return chunkStream(toolCallChunk("c1", "t", `{}`), tailChunk(10, 0, "tool_calls")), nil
		}
		return failAfterStream(delta("部分"), boom), nil
	}}
	reg, _ := tool.NewRegistry(context.Background(), src)
	rec := &recorder{}

	res, err := Run(context.Background(), newTestClient(up), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "q"}},
		testOpts(reg.Defs()), Config{Stream: true}, rec.hooks())
	require.ErrorIs(t, err, boom, "流中段错误上抛")
	assert.Equal(t, "部分", res.Content, "已消费部分产物照常返回")
	assert.Equal(t, llm.Usage{Input: 10}, res.Usage, "累计含失败轮已消费部分")
	assert.False(t, res.Aborted)
	assert.False(t, res.Cancelled)
	require.Len(t, rec.toolResults, 1, "首轮工具轮产物完整")
	assert.Equal(t, []string{"部分"}, rec.deltas)
}
