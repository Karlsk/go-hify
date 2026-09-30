# Research: Chat 前端工具事件渲染

> 状态：Phase 0 决策记录（2026-09-30）。
> 本篇所有架构决策由上位 spec [tool_events_frontend_spec.md](../../../docs/changelog/chat/tool_events_frontend_spec.md) F1–F5 冻结（2026-09-30 用户批准）；
> 技术现状在 specify 前已逐行核实（Explore agent + 直读源文件）。
> **零 NEEDS CLARIFICATION**——clarify 阶段 0 问题（拍板项已落定的不重复问）。

## R1 帧契约与历史行结构（冻结上游，只消费）

**Decision**: 前端消费的后端契约按已核实现状对齐，不自造字段。

**Rationale**: 后端 `StreamEvent` json tag 已核实（type/content/id/tool/args/result/citations/message_id/usage/finish_reason/code/message）；tool_call 帧 `{id, tool, args}`、tool_result 帧 `{id, tool, result:{content, is_error}}`；历史 assistant 中间行与 tool 行同构 `tool_calls[0] = {id, tool, args}`（tools.go:102-104 / :139），id 是流式配对与历史配对的共同锚点。

**Alternatives considered**: 前端另建适配层翻译字段名——否，增加一层无收益的映射，json tag 即契约。

## R2 API 层类型修正（F4）

**Decision**: `ToolResultFrame.result` 从 `unknown` 收敛为 `ToolResultPayload { content: string; is_error: boolean }`；`StreamCallbacks` 增可选 `onToolCall` / `onToolResult`；switch 补两 case；删「后端 v1 预留不触发」过期注释。

**Rationale**: `unknown` 是当初后端未定稿时的占位，后端已交付两字段结构；回调可选（`?`）使未传回调的既有调用点（无 tool 消费方）零改动。

**Alternatives considered**: 在 useChat 里 switch 判 type 分发——否，api 层已有帧分发 switch（delta/done/error/citations），同点补齐一致性最好。

## R3 状态编排与不可变纪律（F2）

**Decision**: `ChatMessage` 增 `toolActivities: ToolActivity[]`；`ToolActivity = {id, tool, args, result?, isError?, state: 'running'|'done'|'interrupted'}`；卡片变化一律整数组替换（不可变）；流收尾四路径（done / error / 被动中断 EOF / 主动 abort）统一把残留 running 卡置 interrupted。

**Rationale**: v-memo 依赖数组靠数组身份判定——mutate push 不换引用则受影响行不重渲染；中断兜底与 assistant 行既有「（连接中断）」标注同哲学（无 result 不伪造结果）。

**Alternatives considered**: 沿用仓内既有 mutate 写法（useChat push / `+=`）——否，v-memo 依赖追加后必须配不可变更新才正确；且上位 spec F2 明文冻结。注意：`send()` 现有 `const assistant = messages.value[messages.value.length - 1]` 响应式代理直改正文的路径不动（正文 delta 既有行为），只有 toolActivities 走不可变替换。

## R4 历史回放分组算法（F2 / FR-005）

**Decision**: `loadMessages` 先按原始行数截断（保尾部最新）再分组：连续 assistant 中间行（仅「content 空 + tool_calls 非空」判定）与其后 tool 行按 id 配对归并进其后首个 assistant 终答行 toolActivities（中间行供 args / 工具名、tool 行供 result）；后面没有 assistant 行的错误中断轮工具卡独立成尾部气泡（仅卡片无正文）；分组产出行数 ≤ 原始行数。

**Rationale**: 中间行判定边界收紧到「正文空 + tool_calls 非空」防误吞正常行；截断按原始行数保尾部最新（若先分组再截断，工具行会被中间行顶出尾窗，历史失真）。

**Alternatives considered**: 分组后再截断——否，见上；孤立 tool 行直接丢弃——否，防御分支保留为「分组异常兜底」（FR-008），降级旧弱化渲染不删。

## R5 折叠卡组件与视觉（F1 / F5）

**Decision**: 新组件 `ToolActivityCard.vue`：本地 ref 折叠态（默认折叠）、点击头部切换；状态图标 running=`Loading` + `is-loading` 类 / done=`CircleCheck` / done+is_error=`Warning` 红态 / interrupted=停动画标「（中断）」；显示名剥离 `s<数字>__` 派生前缀（完整名进 title）；折叠头部参数键值摘要 + 结果首行摘要；展开态 args 完整 JSON 等宽 pretty + result 全文等宽 pre-wrap 限高滚动。

**Rationale**: 图标与样式全部有仓内先例（`is-loading` 类 MessageList.vue:10、Loading/CircleCheck/Warning 均在 @element-plus/icons-vue）；token 六项已核实存在（`--hf-bg-muted` tokens.css:81 / `--hf-radius-sm` :132 / `--hf-font-mono` :169 / `--hf-font-size-sm` :174 / `--hf-text-3` :89 / `--hf-danger` :65），零新增；折叠 ref 先例 App.vue:126。

**Alternatives considered**: 用 Element Plus `el-collapse`——否，单卡自管理折叠态不需要手风琴语义，原生 div + ref 更轻且样式完全可控；卡片加耗时显示——否，SSE 帧不带时间戳，前端不伪造（上位 spec 明确不做）。

## R6 渲染挂点与 v-memo（F1 / F3）

**Decision**: MessageList.vue assistant 分支链（loading 三点 → error → 正文）**之前**插入卡片列 v-for；v-memo 依赖数组现状 `[msg.content, msg.state, msg.errorText, msg.passiveInterrupt, msg.citations]` 追加 `msg.toolActivities`。

**Rationale**: 卡片在「正文 / 等待动画 / 错误文本之前」是 FR-001 冻结位置——工具执行期正文未到，卡片区必须在等待动画上方可见；tool 行防御分支（MessageList.vue:80-81）注释更新为兜底语义不删。

**Alternatives considered**: 卡片列放正文之后——否，视觉上工具过程先于回答，且 running 态卡片是工具执行期唯一可见反馈。
