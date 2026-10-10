# Research: platform 公共 ReAct 循环抽取（spec 019 Phase 0）

**Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md) | **基线**: chat 循环现状 `internal/chat/service/turn.go`（runTurn）+ `tools.go`（runToolRound / loadToolRegistry / 常量族），commit `0d6293f`；冻结决策上位 [chat_tool_loop_spec.md](../../docs/changelog/mcp/chat_tool_loop_spec.md)。

产品级决策（D1=B 抽公共循环、chat 换用）用户 2026-10-10 拍板；本文只记实现级决策（Decision / Rationale / Alternatives）。

## R1：包名与位置 = `internal/platform/reactloop`，无状态 `Run` 函数

- **Decision**: 新包 `internal/platform/reactloop`；公开面单入口 `Run(ctx, client, reg, msgs, opts, cfg, hooks) (Result, error)`；无 struct 状态、无组合根注入。
- **Rationale**: 目录全小写单词仓规（`reactloop`）；platform 不依赖业务域 ✓；循环的全部依赖（llm client、registry、消息、选项）都是调用方持有的既有对象——纯函数式消费无需装配，server.go 零改动（spec 零改动面承诺）。
- **Alternatives**: `platform/agentloop`（名字更泛但本篇只承载 ReAct 工具循环，泛名误导）；struct 实例持 client+reg（多一轮装配无收益，弃）。

## R2：回调缝五钩 + 中止语义（逐帧一致推导）

- **Decision**: `Hooks{OnDelta, OnRound, OnToolRoundStart, OnToolCall, OnToolResult}`，全部可 nil（无操作）；任一钩子返回 error = 调用方中止信号 → 循环停当前轮剩余调用、整体返回（Result.Aborted=true + err=钩子错误）。钩子触发序 = 代码取证序（tools.go runToolRound）：OnToolRoundStart（assistant 正文+调用清单，先于任何 Invoke）→ 逐调用 OnToolCall（**参数非法情形也触发**——现状 tool_call SSE 事件在合法性判定前发）→ Invoke（或非法直接 IsError）→ OnToolResult。
- **Rationale**: 多调用轮现状事件序为 call→result→call→result 交错（US1 逐帧一致硬要求）；每轮单钩会改成 call,call→result,result 帧序，违背 SC-001。midPersisted（assistant 行落库失败 → 跳过该轮 tool 行）是 chat 闭包态，钩子只保证顺序、语义留在 chat。
- **Alternatives**: 事件通道（chan）代替回调（多 goroutine 与背压复杂度，弃）；循环返回完整轨迹由调用方事后重放（SSE 必须实时推、不可事后，弃）。

## R3：摘工具轮新建 opts（eino 缓存不可变前提）

- **Decision**: nudge 收尾轮不复用调用方 opts——以标量字段（Temperature/TopP/MaxTokens）新建 `&llm.CallOptions{}`（Tools 留空）；带工具轮用调用方 opts 原样。
- **Rationale**: CallOptions 的 eino 转换缓存私有能力（options.go 注释「构造后不得再改字段」）——复制 struct 会连带复制缓存指针，Tools=nil + 旧缓存 = 收尾轮仍带工具。标量新建是唯一安全路径。
- **Alternatives**: 让调用方传 opts 工厂函数（API 变复杂，chat 也要改两处构造，弃）；暴露 llm 的 CloneWithoutTools 方法（改 platform/llm 公开面，越界，弃）。

## R4：双模式 = `Config.Stream bool`，Stream 消费走 Recv + Final

- **Decision**: Stream=true：`client.Stream` → Recv 循环逐 delta 回调 OnDelta → `stream.Final()` 聚合终态；Stream=false：`client.Generate` 直取 Final（零 delta 回调）。两模式共享轮循环 / 工具轮 / 收尾逻辑。
- **Rationale**: llm.Client 两入口同链路（抢槽/熔断/超时在 client 内部），循环只在「消费形态」分叉，分叉点收敛在单轮调用helper；chat 现行为即 Stream+consumeStream 语义（delta 实时 emit + Final 聚合），逐帧一致天然保持。
- **Alternatives**: 循环恒 Stream、非流式调用方丢弃 delta（workflow 白占流式槽位时长与内存 chunks 聚合，弃）。

## R5：冻结常量族逐字迁入（文案不改）

- **Decision**: `DefaultMaxRounds=8` / `NudgeText`（「请基于以上工具返回的信息，直接回答用户的问题；不要再调用工具。」）/ `FinishReasonMaxIterations="max_iterations"` / `MaxResultRunes=20000` / `InvalidArgsText`（「工具参数不是合法 JSON 对象」）迁入 reactloop 包级常量；chat 侧同名常量删除。
- **Rationale**: chat_tool_loop_spec 冻结文案；迁移动作 = 搬家不改字（SC-003 grep 证明唯一家）。
- **Alternatives**: 常量留 chat、platform 参数传入（文案双份维护复发，违背 D1 动机，弃）。

## R6：chat 保留面与删除面清单

- **Decision**: **保留**在 chat：loadToolRegistry 装配与五级降级链（ToolIDs 空/非数字/Registry 失败/全过滤/eino 转换失败 → nil+WARN）、recordExecution 与 isPreAttemptErr 判定、SSE emit 链、messages/executions 落库、consumeStream 删除（其语义入循环 Stream 分支）、setup/历史/系统提示组装。**删除**：runTurn 轮循环体、runToolRound、parseToolArgs、truncateToolContent、常量族、toolloop 相关测试（移植后）。
- **Rationale**: 装配与降级是业务决策（agent 绑定面），循环是通用语义——分界即「是否依赖 chat 的 store/agent/SSE 类型」。
- **Alternatives**: 降级链也下沉（需要 mcpapi 依赖进 platform，违反 FR-009 红线，弃）。

## R7：Result 形态（最小完备）

- **Decision**: `Result{Content, Usage llm.Usage, HitLimit, Cancelled, Aborted bool, Rounds int, FinishReason string}`。HitLimit = 收尾轮实际执行（上限触发）；Cancelled = 轮间 ctx 取消；Aborted = 钩子错误中止；FinishReason = 末轮原样透传（上限触发时为 FinishReasonMaxIterations，chat 映射 done 事件同现状）。流中段失败：Content/Usage 为已消费部分、err 上抛。
- **Rationale**: chat 消费 Content/Usage/HitLimit/FinishReason/Cancelled/Aborted 即可重建全部现行为；020 react 节点同面消费（trace 需求届时加法扩展，不预埋）。
- **Alternatives**: Result 携带全量消息轨迹（chat 不需要、020 未定形态，预埋即猜测，弃）。

## R8：参数解析归循环（一次解析三处消费迁入）

- **Decision**: 循环内解析 ToolCall.Arguments（合法 JSON 对象判定 + 非法归一 `{}`），解析产物经 `CallView{Call, Args, ArgsValid}` 传 OnToolRoundStart / OnToolCall；OnToolResult 收 `tool.ToolResult`（Content 已截断）。chat 不再自行解析。
- **Rationale**: 现状 parseToolArgs「一次解析三处消费」（SSE 事件/DB 行/合法性判定）——三处中两处（事件/行）在 chat 回调、一处（判定）在循环，共享解析产物是唯一不裂开的搬法；DB 行 args 归一 `{}` 形态保持（现状不落 null）。
- **Alternatives**: 回调传原始 Arguments、chat 自行解析（解析逻辑双份，弃）。
