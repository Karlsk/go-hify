# Feature Specification: platform 公共 ReAct 工具循环抽取 + chat 重构换用

**Feature Branch**: `019-platform-reactloop`

**Created**: 2026-10-10

**Status**: Draft（待 clarify / 定稿）

**Input**: 2026-10-10 用户拍板 D1=B（抽公共循环下沉 platform、chat 重构换用——统一升级与修复 bug）；上位契约 [docs/changelog/mcp/chat_tool_loop_spec.md](../../../docs/changelog/mcp/chat_tool_loop_spec.md)（2026-09-29 定稿已实施：轮数上限 / nudge / 错误即上下文 / 截断 / 判定纪律 / 串行 Invoke 冻结）；循环现状基线 `internal/chat/service/turn.go`（runTurn 轮循环）+ `tools.go`（Registry 装配 / 工具轮执行 / 常量族），commit `0d6293f`。

**拆分记录**: 本篇 = 循环下沉 + chat 换用（零对外变化）；workflow react 节点 / ragNode 画布接入 / tool 节点表单 = spec 020（依赖本篇循环）。builtin 工具机制经用户 2026-10-10 剔除（deferred_items 留痕）。

## Clarifications

### Session 2026-10-10

- Q: 回调缝是「每轮一个工具钩子」还是「每调用一对钩子」？ → A: **每调用一对**（OnToolCall 前 / OnToolResult 后）+ 每轮一个 OnToolRoundStart——代码取证（chat/service/tools.go runToolRound）：现状 tool_call SSE 事件在参数合法性判定**之前**逐调用发出（非法参数也发），多调用轮事件序为 call→result→call→result 交错；每轮钩子会改变帧序、违背 US1 逐帧一致。
- Q: 参数解析与三处消费的边界在哪层？ → A: 循环内一次解析（合法性判定 + 归一 {} 形态），解析产物经 OnToolRoundStart / OnToolCall 回调传给调用方（assistant 中间行与 SSE 事件消费归一后参数）——「一次解析三处消费」语义迁入循环，调用方不再自行解析。
- Q: 逐帧一致的其余耦合点（取证确认，换用时经回调保持）：assistant 中间行落库失败 → 该轮 tool 行跳过（midPersisted 语义在 chat 回调闭包内）；ToolCallEvent emit 失败 → 中止剩余调用（回调错误 = 中止信号，FR-005）；ToolResultEvent emit 失败 → 同样中止（结果已落库，仅事件断）。
- Q: Stream.Final 聚合失败的处理？ → A: 零值终态、不单独上抛错误（代码取证 turn.go consumeStream：`if f, ferr := stream.Final(); ferr == nil` 才填产物——聚合失败 = 空终态按终答轮收尾）；Edge Cases 对应条目已按此修正。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - chat 换用零感知（Priority: P1）

绑定 MCP 工具的 Agent 对话触发多轮工具调用：流式 delta / tool_call / tool_result / done 事件逐帧、assistant 中间行与 tool 行落库序、executions 逐轮记录、8 轮上限 nudge 收尾——全部与重构前一致；chat 对外契约（SSE 事件形态 / 路由 / 落库形态）零变化。

**Why this priority**: 重构的唯一目的是「循环单点维护」，前提是换用零行为差异——chat 回归与手测零差异是本篇的成立条件，排 P1 且为验收门。

**Independent Test**: 绑定工具的 Agent 跑多轮工具对话（流式 + 一次输出两模式）+ 触发 8 轮上限场景 + 工具失败场景，断言 SSE 帧序 / 落库行序 / executions 行数与重构前一致（手测 chat-manual-test.md 工具批小节零差异复测）。

**Acceptance Scenarios**:

1. **Given** 绑定 MCP 工具的 Agent 会话，**When** 消息触发两轮工具调用后终答，**Then** SSE 帧序 = delta* → tool_call → tool_result → delta* → tool_call → tool_result → delta* → done（usage 为各轮累计），与重构前逐帧一致
2. **Given** 工具调用轮，**When** 循环执行工具轮，**Then** assistant 中间行（正文 + tool_calls）先于工具调用落库、tool 行按调用序落库；assistant 行落库失败时该轮 tool 行跳过（孤儿行防护语义不变）
3. **Given** 工具返回错误（IsError），**When** 循环继续，**Then** 错误文本喂回 LLM 自纠、对话不中断（错误即上下文），tool_result 事件照常推送
4. **Given** 工具参数非法（非 JSON 对象），**When** 循环处理该调用，**Then** 不发起真实工具调用、非法参数提示文本喂回 LLM（文案与现状一致）
5. **Given** 循环达轮数上限，**When** 收尾轮执行，**Then** nudge 提示瞬态注入（不落库、不进 SSE 正文流）、工具被摘除重发一轮逼出文本终答、done 事件 finish_reason 带上限标识（与现状一致）

### User Story 2 - 循环语义单点维护（Priority: P1）

循环本体（轮结构 / 上限收尾 / 错误喂回 / 截断 / 判定纪律 / 常量文案）只存在于 platform 新包一处；chat 不再持有任何循环逻辑与语义常量。改任一条语义只动 platform，chat 与未来 react 节点同步生效。

**Why this priority**: 用户拍板 D1=B 的动机本身——双份维护是本次重构要消灭的对象；与 US1 同为核心（重构 = 搬家 + 换用，两半缺一不可）。

**Independent Test**: platform 新包单测独立运行（不 import chat、不依赖 chat 装配）覆盖场景族：轮上限 / nudge 收尾 / 错误喂回 / 参数非法不发起调用 / 断连不空转 / Registry 空清单降级 / 截断保真；grep 证明 chat/service 无循环常量与轮循环体残留。

**Acceptance Scenarios**:

1. **Given** platform 新包单测，**When** 运行轮上限用例，**Then** 第 9 轮起进入收尾形态（nudge + 摘工具）、终答携带上限触发标识
2. **Given** 工具结果超截断上限，**When** 循环回喂与产出结果，**Then** 回喂文本与调用方可见文本为同一份截断产物（截一次、两处共用）
3. **Given** chat/service 源码，**When** grep 循环语义常量（轮上限 / nudge 文案 / 截断上限 / 非法参数文案），**Then** 零命中（常量唯一家在 platform）
4. **Given** 循环判定工具轮，**When** 模型返回终态，**Then** 判定依据恒为工具调用清单非空，不出现按 finish_reason 分支的代码路径

### User Story 3 - 双消费模式与取消语义（Priority: P2）

循环同一本体支撑流式消费（逐 delta 回调，chat 用）与非流式消费（整体返回终态，020 的 workflow react 节点用）；调用方取消（ctx）在轮间被感知并停止空转，调用方可区分「我方取消」与「循环自身错误」；流中段失败时部分产物照常返回（调用方按自身契约收尾）。

**Why this priority**: 双模式是 020 的前置（react 节点消费非流式）；本篇以单测证明非流式模式可用（生产消费者在 020），故排 P2。

**Independent Test**: 单测对同一图景分别以流式 / 非流式模式运行，断言终态内容 / usage / 工具轮消息序一致（仅 delta 回调有无之差）；ctx 取消用例断言轮间停止 + 取消标识；流中段失败用例断言部分产物返回 + 错误上抛。

**Acceptance Scenarios**:

1. **Given** 非流式模式运行两轮工具循环，**When** 结束，**Then** 返回终稿内容 / 累计 usage / 上限标识，无 delta 回调发生
2. **Given** 流式模式运行同场景，**When** 结束，**Then** delta 回调按到达序触发且拼接 = 终稿内容，其余产物与非流式一致
3. **Given** 首轮结束后调用方取消 ctx，**When** 循环进入下一轮前检查，**Then** 循环停止、返回「调用方取消」标识（区别于循环自身错误），不再发起 LLM 调用
4. **Given** 流式第二轮中段上游失败，**When** 循环终止，**Then** 已消费的部分内容与累计 usage 照常返回、错误上抛调用方（调用方按自身契约收尾：chat = error 事件 / 一次输出 = 信封）

---

### Edge Cases

- reg 为 nil 或工具清单空（agent 未绑工具 / 全被停用过滤）：循环退化为单轮直答（无工具 opts），不进入工具轮——与 chat 现状 `reg == nil` 分支一致
- 回调返回错误（chat 语义 = SSE emit 失败 / 前端断连）：循环中止当前轮剩余工具调用并整体退出，取消标识返回调用方（chat 映射为静默收尾）
- nudge 收尾轮的 opts 必须新建（eino 转换缓存前提：CallOptions 构造后不可变）——摘工具 = 重建无工具 opts，不复用带工具实例
- usage 累计含收尾轮；recordExecution 类逐轮记录由调用方经回调实现（循环不记 executions）
- 同一轮多工具调用：串行 Invoke（chat_tool_loop_spec §10 冻结，不做并发）
- Registry.Invoke 未知名：IsError 结果喂回（Registry 既有语义，循环不特判）
- MaxRounds 缺省 / ≤0：取默认 8；上限值域校验归调用方（react 节点 020 保存期校验 1-16）
- Stream.Final 聚合失败：零值终态产物、不单独上抛错误（忠实 chat 现状 consumeStream：`if ferr == nil` 才填 res，聚合失败 = 空终态按终答轮收尾）

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: platform 新包 MUST 承载 ReAct 循环本体：轮循环（单轮 LLM 调用 → 判定工具调用清单非空 → 逐调用经 tool.Registry 串行 Invoke → assistant 消息与 tool 消息追加进上下文 → 下一轮）；判定纪律 MUST 只看工具调用清单非空，禁止按 finish_reason 分支
- **FR-002**: 轮数上限与收尾 MUST 对齐 chat_tool_loop_spec 冻结决策：默认 8 轮（调用方可配，缺省 / ≤0 回退默认）；上限轮 = 瞬态 nudge 提示（不经回调持久化面、不进终稿）+ 摘除工具新建 opts 重发一轮 + 终答携带上限触发标识；nudge 文案为冻结常量
- **FR-003**: 错误即上下文 MUST 保持：工具调用失败（IsError）与参数非法（非 JSON 对象）均不中断循环——IsError 结果文本喂回 LLM 自纠；参数非法 MUST 不发起真实工具调用（非法参数提示文案为冻结常量）
- **FR-004**: 工具结果截断 MUST 单点：回喂 LLM 与经回调产出给调用方的结果文本共用同一份截断产物（上限为冻结常量，集中 platform 新包）
- **FR-005**: 循环 MUST 经回调缝暴露过程事件：流式 delta / 每轮终态（含该轮错误）/ 工具轮开始（assistant 正文 + 调用清单，先于该轮任何 Invoke，供调用方落中间行）/ **单调用前**（每次 Invoke 尝试前逐调用触发，含参数非法不发起真实调用的情形——保证多调用轮的对外事件序与现状逐帧一致）/ **单调用后**（该调用结果产出后逐调用触发）；回调返回错误 = 调用方中止信号（循环停当前轮剩余调用并整体退出）；循环本体 MUST 不感知 SSE / DB / 会话 / executions
- **FR-006**: 双消费模式 MUST 同一循环本体：流式（逐 delta 回调 + Stream.Final 聚合终态）与非流式（Generate 整体终态、零 delta 回调）；模式由调用参数选择；两模式产物（终稿 / 累计 usage / 上限标识 / 工具轮消息序）除 delta 回调外 MUST 一致
- **FR-007**: 取消语义 MUST 可区分：轮间 ctx 检查命中取消 → 停止空转并返回调用方取消标识（不再发起 LLM 调用）；流中段上游失败 → 已消费部分产物（内容 / 累计 usage）照常返回 + 错误上抛；两类不得混同
- **FR-008**: chat MUST 重构换用：chat/service 删除自有循环本体与语义常量（轮循环 / nudge / 截断 / 非法参数文案 / 上限常量），改为消费 platform 循环 + 回调接线（SSE 推送 / 中间行与 tool 行落库序 / executions 逐轮记录 / 降级链全经回调保持现行为）；chat 对外契约（SSE 事件形态 / 路由 / messages 与 executions 落库形态）MUST 零变化
- **FR-009**: 依赖红线 MUST 保持：platform 新包只 import `platform/llm`、`platform/tool` 与标准库；chat 经该包消费循环；`cloudwego/eino` 不出 platform/llm、`modelcontextprotocol/go-sdk` 不出 mcp/service 的 grep 纪律不变

### Key Entities *(include if feature involves data)*

- **循环配置（调用入参）**: LLM client + tool.Registry（nil = 无工具直答）+ 初始消息序列 + 调用选项（含工具清单）+ 轮数上限 + 消费模式 + 回调集合
- **循环产物（调用返回）**: 终稿内容 / 累计 usage / 上限触发标识 / 调用方取消标识 / 部分产物（流中段失败时）+ 错误
- **冻结常量族（迁入 platform 新包，文案逐字不改）**: 默认轮上限 8、nudge 文案、上限 finish_reason 标识、工具结果截断上限 20000 rune、非法参数提示文案
- **回调集合（过程事件缝）**: OnDelta / OnRound（每轮终态 + 该轮错误）/ OnToolRoundStart（assistant 正文 + 调用清单，先于该轮任何 Invoke）/ OnToolCall（单调用前，逐调用，含参数非法情形）/ OnToolResult（单调用后，逐调用）；返回值错误 = 中止信号

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: chat 换用零差异——chat 模块全量测试回归绿；绑定工具 Agent 的流式 / 一次输出 / 上限 / 工具失败四场景手测与重构前零差异（chat-manual-test.md 工具批小节）
- **SC-002**: 循环独立可测——platform 新包单测不 import chat、不依赖业务装配，覆盖场景族（轮上限 / nudge / 错误喂回 / 参数非法 / 断连 / Registry 空 / 截断 / 双模式一致 / 取消区分）全绿
- **SC-003**: 语义单点——grep 证明循环语义常量与轮循环体在 chat/service 零残留、唯一家在 platform 新包
- **SC-004**: 自动化门禁全绿：`go build ./... && go vet ./... && go test ./... -race -count=1`；platform 新包与 chat/service 覆盖率各 ≥80%；`make migrate-status` 恒 21 applied（零迁移）
- **SC-005**: 依赖红线零越界——platform 新包 import 面 grep 仅 llm/tool/标准库；eino / go-sdk 既有 grep 纪律三条全空

## Assumptions

- 前置：chat 工具循环批（`0d6293f`）已合入为循环现状基线；chat_tool_loop_spec 冻结决策逐字承继（常量迁入不改文案、串行 Invoke、判定纪律）
- chat 的 Registry 装配与降级链（ToolIDs 空 / 非数字 / Registry 失败 / 全过滤 / eino 转换失败 → nil + WARN）留在 chat（装配属业务），循环只消费「registry 或 nil」
- 零迁移、零新路由、零新哨兵错误、零新配置项；chat 对外契约任何变化 = 本篇失败
- 非流式模式本篇以单测证明（生产消费者 = spec 020 react 节点）；react 节点的轮数可配（1-16 保存期校验）在 020 落地，本篇循环只接受调用方传入值
- 明确不做：workflow react 节点（020）；builtin 工具机制与内置工具（2026-10-10 剔除）；tool 节点 builtin 扩展；循环内并发工具调用；会话级 ClientSession 复用
- 人工验收 = chat 工具对话手测零差异复测（用户执行）；自动化门禁不覆盖真实 LLM / MCP server 侧行为
