# Contracts: reactloop 公开面（spec 019 Phase 1）

**Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md) | **决策依据**: [research.md](../research.md)

本篇对外可观察面 = **一个 platform 包的函数式公开面**（chat 与 spec 020 react 节点的消费契约）。零新路由 / 零新哨兵 / 零迁移 / 零配置项；chat 对外契约（SSE 事件、路由、落库形态）零变化（硬红线，SC-001）。

## C1. 入口签名

```go
package reactloop // internal/platform/reactloop

// Run 执行 ReAct 工具循环（spec 019 FR-001~FR-007）：轮循环消费 client 与 reg，
// 过程事件经 hooks 回调；reg 为 nil 或工具清单空 = 无工具单轮直答。
// opts 带工具清单（调用方以 reg.Defs() 装配）；收尾轮循环内部新建无工具 opts
//（标量复制，调用方 opts 不被修改——eino 缓存不可变前提，R3）。
func Run(ctx context.Context, client *llm.Client, reg *tool.Registry,
    msgs []llm.Message, opts *llm.CallOptions, cfg Config, h Hooks) (Result, error)
```

- `msgs` 为初始上下文（调用方组装，含 system/user）；循环内部追加 assistant/tool 消息，**不修改调用方 slice**（内部 append 副本）。
- 错误返回 = 循环自身错误（LLM 调用失败 / 流聚合失败）；钩子错误以 `Result.Aborted=true` + 该钩子错误返回；轮间 ctx 取消以 `Result.Cancelled=true` + nil error 返回（调用方区分静默路径，FR-007）。

## C2. Config / Result / Hooks / CallView

```go
type Config struct {
    MaxRounds int  // 工具轮上限；<=0 → DefaultMaxRounds(8)；值域校验归调用方（020：1-16 保存期）
    Stream    bool // true = Stream + OnDelta 逐帧；false = Generate 整体终态（零 delta 回调）
}

type Result struct {
    Content      string    // 终稿（流中段失败 = 已消费部分）
    Usage        llm.Usage // 各轮累计（含收尾轮）
    HitLimit     bool      // 上限触发（收尾轮实际执行）
    Cancelled    bool      // 轮间 ctx 取消（调用方静默路径标识）
    Aborted      bool      // 钩子错误中止（chat = 前端断连静默路径）
    Rounds       int       // 实际执行轮数（含收尾轮）
    FinishReason string    // 末轮原样透传；上限触发 = FinishReasonMaxIterations
}

// CallView 单调用视图：Args 为循环内一次解析产物（map 形态，非法归一 {}，不落 null——
// chat ToolCallEvent / DB 行直接消费 map）
type CallView struct {
    Call      tool.ToolCall
    Args      map[string]any
    ArgsValid bool
}

type Hooks struct {
    // 流式模式逐 delta（非流式不触发）；错误 = 中止
    OnDelta func(delta string) error
    // 每轮 LLM 终态（含该轮调用错误，callErr 非 nil 时 res 可能为零值）；chat 在此记 executions
    OnRound func(round int, res *llm.Final, callErr error) error
    // 工具轮开始：assistant 正文 + 本轮调用清单，先于任何 Invoke；chat 在此落 assistant 中间行
    OnToolRoundStart func(round int, content string, calls []CallView) error
    // 单调用前（逐调用；ArgsValid=false 也触发——现状 tool_call 事件先于合法性判定）
    OnToolCall func(view CallView) error
    // 单调用后（逐调用；result.Content 已截断、IsError 语义齐备）；chat 在此落 tool 行 + 推事件
    OnToolResult func(view CallView, result tool.ToolResult) error
}
```

钩子触发序（冻结，逐帧一致依据）：`OnRound(轮终态)` → 若有工具调用：`OnToolRoundStart` → 逐调用 `OnToolCall → (Invoke|非法直喂) → OnToolResult` → 下一轮。全部钩子 nil 合法（无操作）。

## C3. 冻结常量族（自 chat/service/tools.go 逐字迁入，文案不改）

| 常量 | 值 | 现状出处 |
|---|---|---|
| `DefaultMaxRounds` | 8 | tools.go `maxToolRounds` |
| `NudgeText` | 「请基于以上工具返回的信息，直接回答用户的问题；不要再调用工具。」 | tools.go `maxToolRoundsNudge` |
| `FinishReasonMaxIterations` | `"max_iterations"` | tools.go 同名 |
| `MaxResultRunes` | 20000 | tools.go `maxToolResultRunes` |
| `InvalidArgsText` | 「工具参数不是合法 JSON 对象」 | tools.go `invalidToolArgsMsg` |

截断单点：回喂 LLM 的 tool 消息 Content 与 OnToolResult 收到的 result.Content 为**同一份截断产物**（FR-004）。

## C4. 语义承继清单（chat_tool_loop_spec 冻结 → 本篇落点）

| 冻结决策 | 落点 |
|---|---|
| 判定纪律只看 `len(ToolCalls) > 0` | Run 轮判定（禁 finish_reason 分支） |
| 上限轮 = nudge 瞬态 + 摘工具重发 | Run 收尾轮（nudge 消息仅当轮可见，不追加进 msgs 持久面） |
| 错误即上下文 / 参数非法不发起调用 | Run 工具轮（IsError 喂回；InvalidArgs 直喂） |
| 串行 Invoke | Run 工具轮逐调用顺序执行 |
| 断连不空转（轮间 ctx 检查） | Run 轮首检查 → Result.Cancelled |
| Registry 装配与降级链 | **留 chat**（loadToolRegistry 不动；循环只收 nil/非nil） |
| 20k 截断三处共用 | MaxResultRunes 单点（回喂/回调/020 落库同源） |

## C5. chat 换用红线（SC-001 判定面）

- SSE 帧序不变：delta* →（tool_call → tool_result）* → delta* → done（usage 累计、finish_reason 上限标识同现状）
- messages 落库不变：assistant 中间行（正文+tool_calls 归一 args）先于 tool 行；midPersisted 失败跳 tool 行；role 枚举与 ToolCalls[0] 锚点形态不变
- executions 不变：每轮一行（ConversationID=nil 标识）、pre-attempt 失败不记（判定留 chat OnRound 闭包）
- 一次输出模式与流式模式共用循环（现 runTurn 双模式同体语义保持）
