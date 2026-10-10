// Package reactloop 承载 ReAct 多轮工具循环本体（spec 019）：LLM 自主调用工具 →
// 结果喂回 → 循环至文本终答。chat 对话引擎（流式）与 workflow react 节点（非流式，
// spec 020）共用同一实现——循环语义（轮数上限 / nudge 收尾 / 错误即上下文 / 截断 /
// 判定纪律）单点维护，冻结决策承继 docs/changelog/mcp/chat_tool_loop_spec.md。
//
// 边界：本包只消费 platform/llm 与 platform/tool 公开面——超时 / 重试 / 熔断在
// llm client 内部（本包不碰）；SSE / 落库 / executions / Registry 装配与降级链在
// 调用方（经 Hooks 回调缝）。
package reactloop

import (
	"context"
	"encoding/json"
	"io"

	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// 冻结常量族（自 chat/service/tools.go 逐字迁入，文案不改——chat_tool_loop_spec 冻结）：
const (
	// DefaultMaxRounds 工具调用轮上限默认值（调用方可经 Config.MaxRounds 覆盖；
	// 值域校验归调用方）：上限轮追加 nudge 摘掉工具逼出文本终答。
	DefaultMaxRounds = 8
	// FinishReasonMaxIterations 上限收尾的终态 finish_reason（区别于模型原生 stop / STOP）。
	FinishReasonMaxIterations = "max_iterations"
	// MaxResultRunes 工具结果文本截断上限：回喂 LLM 与回调产出共用同一份截断文本
	//（先截断再分发，多处一致）。
	MaxResultRunes = 20000
	// NudgeText 上限触发的收尾引导（瞬态：只进收尾轮 LLM 请求，不入循环消息序列持久面）。
	NudgeText = "请基于以上工具返回的信息，直接回答用户的问题；不要再调用工具。"
	// InvalidArgsText 参数非法（非 JSON 对象）的 IsError 结果文本：不发起调用直接喂回 LLM。
	InvalidArgsText = "工具参数不是合法 JSON 对象"
)

// Config 循环调用配置。
type Config struct {
	// MaxRounds 工具轮上限；<=0 → DefaultMaxRounds。
	MaxRounds int
	// Stream true = client.Stream 逐 delta 经 OnDelta 回调；false = client.Generate
	// 整体终态（零 delta 回调）。两模式共享轮循环 / 工具轮 / 收尾语义。
	Stream bool
}

// CallView 单次工具调用视图：Args 为循环内一次解析的产物（非法归一 {}，
// 不落 null 形态——调用方落库 / 事件直接消费）。
type CallView struct {
	Call      tool.ToolCall
	Args      map[string]any
	ArgsValid bool
}

// Hooks 过程事件回调缝（全可 nil = 无操作）；任一回调返回 error = 调用方中止信号
// （chat 语义 = SSE emit 失败 / 前端断连）：循环停当前轮剩余工作并整体返回
// （Result.Aborted = true + 该错误）。
type Hooks struct {
	// OnDelta 流式模式逐 delta（非流式不触发）。
	OnDelta func(delta string) error
	// OnRoundStart 单轮 LLM 调用建立后、delta 消费前（chat：首轮 citations 事件）。
	OnRoundStart func(round int) error
	// OnRound 每轮 LLM 终态（sent = 本轮实发消息含 nudge；res 可能为零值 / nil：
	// 建流失败 nil、流中段失败为部分聚合、Final 聚合失败为零值；callErr = 本轮调用 /
	// 消费错误，含钩子中止错误）。chat 在此记 executions。
	OnRound func(round int, sent []llm.Message, res *llm.Final, callErr error) error
	// OnToolRoundStart 工具轮开始：assistant 正文 + 本轮调用清单，先于任何 Invoke
	//（chat 在此落 assistant 中间行）。
	OnToolRoundStart func(round int, content string, calls []CallView) error
	// OnToolCall 单调用前（逐调用；ArgsValid=false 也触发——事件先于合法性判定）。
	OnToolCall func(view CallView) error
	// OnToolResult 单调用后（逐调用；result.Content 已截断、IsError 语义齐备；
	// 参数非法时为直喂结果、无真实调用发生）。
	OnToolResult func(view CallView, result tool.ToolResult) error
}

// Result 循环产物。
type Result struct {
	Content      string    // 终稿（终答轮内容；流中段失败 = 已消费部分；中途 abort / 取消 = 空）
	Usage        llm.Usage // 各轮累计（含收尾轮与失败轮已消费部分）
	HitLimit     bool      // 上限触发（收尾轮实际执行）
	Cancelled    bool      // 轮间 ctx 取消（调用方静默路径标识）
	Aborted      bool      // 钩子错误中止（chat = 前端断连静默路径）
	Rounds       int       // 实际执行的 LLM 轮数（含收尾轮）
	FinishReason string    // 末轮原样透传；上限触发 = FinishReasonMaxIterations
}

// Run 执行 ReAct 工具循环（spec 019 FR-001~FR-007，契约见 contracts/reactloop-api.md C1/C2）。
// reg 为 nil 或工具清单空 = 无工具单轮直答（幻觉调用不进循环）；opts 带工具清单
// （调用方以 reg.Defs() 装配）；收尾轮循环内部以标量新建无工具 opts（调用方 opts 不被
// 修改——CallOptions 构造后不可变，eino 转换缓存前提）；msgs 入参零副作用（内部副本追加）。
//
// 返回语义：callErr 非 nil = 循环自身错误（建流 / Generate / 流中段失败，Result 携带
// 已消费部分产物）；Result.Cancelled = 轮间 ctx 取消（nil error）；Result.Aborted =
// 钩子中止（error = 钩子错误）。
func Run(ctx context.Context, client *llm.Client, reg *tool.Registry, msgs []llm.Message,
	opts *llm.CallOptions, cfg Config, h Hooks) (Result, error) {
	limit := cfg.MaxRounds
	if limit <= 0 {
		limit = DefaultMaxRounds
	}
	hasTools := reg != nil && len(reg.Defs()) > 0
	seq := append([]llm.Message(nil), msgs...)
	var usage llm.Usage

	for round := 0; ; round++ {
		// 断连不空转：轮间 ctx 检查（首轮不查——调用尚未发生）。
		if round > 0 && ctx.Err() != nil {
			return Result{Cancelled: true, Usage: usage, Rounds: round}, nil
		}
		atLimit := round >= limit
		roundMsgs, roundOpts := seq, opts
		if atLimit {
			// 上限收尾：nudge 瞬态（仅本轮可见，不入 seq 持久面）+ 摘工具新建 opts。
			roundMsgs = append(append([]llm.Message(nil), seq...),
				llm.Message{Role: llm.RoleUser, Content: NudgeText})
			roundOpts = scalarOpts(opts)
		}

		fin, callErr, hookErr := runRound(ctx, client, roundMsgs, roundOpts, cfg, h, round)
		if h.OnRound != nil {
			roundErr := callErr
			if roundErr == nil {
				roundErr = hookErr
			}
			if herr := h.OnRound(round, roundMsgs, fin, roundErr); herr != nil {
				return partialResult(fin, usage, round, true), herr
			}
		}
		if fin != nil {
			usage.Input += fin.Usage.Input
			usage.Output += fin.Usage.Output
		}
		if hookErr != nil {
			return partialResult(fin, usage, round, true), hookErr
		}
		if callErr != nil {
			return partialResult(fin, usage, round, false), callErr
		}

		// 判定纪律：只看工具调用清单非空（finish_reason 各厂字符串不同，仅遥测）。
		// atLimit / 无工具兜底：无工具可用仍幻觉出调用按终答收尾，不再进循环。
		if len(fin.ToolCalls) == 0 || atLimit || !hasTools {
			finish := fin.FinishReason
			if atLimit {
				finish = FinishReasonMaxIterations
			}
			return Result{Content: fin.Content, Usage: usage, HitLimit: atLimit,
				Rounds: round + 1, FinishReason: finish}, nil
		}

		// ---- 工具轮：解析一次 → 钩子序 → 串行 Invoke → 消息追加 ----
		views := parseViews(fin.ToolCalls)
		if h.OnToolRoundStart != nil {
			if herr := h.OnToolRoundStart(round, fin.Content, views); herr != nil {
				return Result{Usage: usage, Aborted: true, Rounds: round + 1}, herr
			}
		}
		seq = append(seq, llm.Message{Role: llm.RoleAssistant, Content: fin.Content, ToolCalls: fin.ToolCalls})
		var abortErr error
		for _, v := range views {
			if h.OnToolCall != nil {
				if herr := h.OnToolCall(v); herr != nil {
					abortErr = herr
					break
				}
			}
			result := invokeOrInvalid(ctx, reg, v)
			if h.OnToolResult != nil {
				if herr := h.OnToolResult(v, result); herr != nil {
					abortErr = herr
					break
				}
			}
			seq = append(seq, llm.Message{Role: llm.RoleTool, Content: result.Content,
				ToolCallID: v.Call.ID, ToolName: v.Call.Name})
		}
		if abortErr != nil {
			return Result{Usage: usage, Aborted: true, Rounds: round + 1}, abortErr
		}
	}
}

// runRound 单轮 LLM 调用（双模式分叉点）：返回（终态 / 调用错误 / 钩子错误）。
// 建流失败 fin=nil；流中段失败 fin=部分聚合；Final 聚合失败 fin=零值（忠实 chat
// consumeStream：聚合失败不单独上抛）。
func runRound(ctx context.Context, client *llm.Client, msgs []llm.Message, opts *llm.CallOptions,
	cfg Config, h Hooks, round int) (fin *llm.Final, callErr error, hookErr error) {
	if !cfg.Stream {
		fin, callErr = client.Generate(ctx, msgs, opts)
		if callErr == nil && h.OnRoundStart != nil {
			hookErr = h.OnRoundStart(round)
		}
		return fin, callErr, hookErr
	}
	stream, serr := client.Stream(ctx, msgs, opts)
	if serr != nil {
		return nil, serr, nil
	}
	if h.OnRoundStart != nil {
		if herr := h.OnRoundStart(round); herr != nil {
			_ = stream.Close() // 取消上游（省 token）+ 释放槽位
			return nil, nil, herr
		}
	}
	fin, callErr, hookErr = consumeStream(stream, h.OnDelta)
	_ = stream.Close() // 幂等：EOF 已自动收尾，错误 / 中止路径显式释放槽位
	return fin, callErr, hookErr
}

// consumeStream 收流：delta 逐个回调（空帧跳过——纯工具调用增量等）；钩子错误 =
// 调用方断连（停止收流、保留已消费部分）；终态从 Stream.Final 聚合。
func consumeStream(stream *llm.Stream, onDelta func(string) error) (fin *llm.Final, callErr error, hookErr error) {
	for {
		delta, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			callErr = rerr // 流中失败（首 token 后不重试，llm 重试纪律）
			break
		}
		if delta == "" {
			continue
		}
		if onDelta != nil {
			if herr := onDelta(delta); herr != nil {
				hookErr = herr
				break
			}
		}
	}
	return finalOf(stream), callErr, hookErr
}

// finalOf Stream.Final 聚合（值类型 → 指针统一面）；聚合失败零值终态（不单独上抛——
// chat 现状忠实）。
func finalOf(stream *llm.Stream) *llm.Final {
	if f, ferr := stream.Final(); ferr == nil {
		return &f
	}
	return &llm.Final{}
}

// partialResult 失败 / 中止路径的部分产物（fin 非 nil 时携带已消费内容与终态遥测）。
func partialResult(fin *llm.Final, usage llm.Usage, round int, aborted bool) Result {
	res := Result{Usage: usage, Aborted: aborted, Rounds: round + 1}
	if fin != nil {
		res.Content = fin.Content
		res.FinishReason = fin.FinishReason
	}
	return res
}

// scalarOpts 标量复制新建无工具 opts（收尾轮摘工具；不动调用方 opts 与其 eino 缓存）。
func scalarOpts(opts *llm.CallOptions) *llm.CallOptions {
	if opts == nil {
		return &llm.CallOptions{}
	}
	return &llm.CallOptions{Temperature: opts.Temperature, TopP: opts.TopP, MaxTokens: opts.MaxTokens}
}

// parseViews 调用参数一次解析（合法性判定 + 归一 {} 形态）：空 / null / {} = 无参合法；
// 非 JSON 对象（数组 / 字符串 / 截断 JSON）→ ArgsValid=false 且 Args 归一 {}。
func parseViews(calls []tool.ToolCall) []CallView {
	views := make([]CallView, 0, len(calls))
	for _, c := range calls {
		args, ok := parseArgs(c.Arguments)
		views = append(views, CallView{Call: c, Args: args, ArgsValid: ok})
	}
	return views
}

func parseArgs(raw json.RawMessage) (args map[string]any, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, true
	}
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return map[string]any{}, false
	}
	return args, true
}

// invokeOrInvalid 单调用执行：参数非法不发起真实调用（IsError 直喂）；Invoke 失败
// 也是 IsError 结果（错误即上下文）；结果截断单点（回喂与回调同一份文本）。
func invokeOrInvalid(ctx context.Context, reg *tool.Registry, v CallView) tool.ToolResult {
	var result tool.ToolResult
	if !v.ArgsValid {
		result = tool.ToolResult{CallID: v.Call.ID, Name: v.Call.Name, IsError: true, Content: InvalidArgsText}
	} else {
		result = reg.Invoke(ctx, v.Call)
	}
	result.Content = truncateRunes(result.Content)
	return result
}

// truncateRunes 工具结果按 rune 截断（中文不裂半字），超长以省略号标记。
func truncateRunes(s string) string {
	r := []rune(s)
	if len(r) <= MaxResultRunes {
		return s
	}
	return string(r[:MaxResultRunes]) + "…"
}
