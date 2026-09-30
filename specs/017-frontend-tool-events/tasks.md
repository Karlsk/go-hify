# Tasks: Chat 前端工具事件渲染（tool_call / tool_result 折叠卡片 + 历史回放分组）

**Input**: Design documents from `/specs/017-frontend-tool-events/`

**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/tool-events.md ✅, quickstart.md ✅

**Tests**: 无前端单测框架——实现完成的定义 = 每任务过 `cd web && npm run type-check`、全篇收尾过 `cd web && npm run type-check && npm run build` 全绿（前端适配模式门禁）；行为验证 = 手测六条人工项（用户执行，quickstart §5）。

**Organization**: 按用户故事分组（US1 流式路径可视化 / US2 历史回放分组 / US3 零回归与防御兜底），任务内嵌冻结契约与三零边界约束。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to
- 红线约束（贯穿所有实现任务）：三零边界——零后端改动（Go 源 / migrations / go.mod 不碰）、零新依赖（package.json 不动）、零新增设计 token（tokens.css 不动，卡片只用既有六个语义 token）；SSE 帧契约与历史行结构为冻结上游只消费（json tag / `tool_calls[0]={id,tool,args}` 锚点不自造）；卡片数据更新一律不可变整数组替换；props/emits 全 type-based。

---

## Phase 1: Setup（基线确认）

**Purpose**: 门禁基线与三零前提核实——不动任何代码

- [x] T001 基线确认：`cd web && npm run type-check && npm run build` 全绿（既有 >500kB chunk 提示是历史噪音）；`git status --short web/package.json` 无改动（零新依赖前提）

---

## Phase 2: Foundational（api 层契约修正）

**Purpose**: 帧类型与分发就位——US1（流式回调）与 US2（历史行类型注释修正）的共同编译前提

- [x] T002 [US1] api 契约修正：`web/src/api/chat.ts`——增 `ToolResultPayload { content: string; is_error: boolean }` 接口，`ToolResultFrame.result` 类型从 `unknown` 收敛为 `ToolResultPayload`（FR-007）；`StreamCallbacks` 增可选 `onToolCall?: (frame: ToolCallFrame) => void` / `onToolResult?: (frame: ToolResultFrame) => void`（未传回调的既有调用点零改动）；帧分发 switch 补 `tool_call` / `tool_result` 两 case（回调缺省时忽略，与既有 case 缺省行为一致）；删「后端 v1 预留不触发」过期注释、修正 ToolCall interface 过时注释（:26 现注释已过期——后端已触发）；任务级门禁 type-check 过

---

## Phase 3: User Story 1 - 流式路径的工具活动可视化 (Priority: P1) 🎯 MVP

**Goal**: 绑工具 Agent 对话中，tool_call / tool_result 两帧驱动折叠卡片在 assistant 气泡顶部完成 running→done 状态流转；并行多卡、is_error 红警示、流收尾残留卡标中断（US1 / FR-001 / FR-002 / FR-003 / FR-004 / FR-006）

**Independent Test**: 对绑工具 Agent 发触发工具调用的消息，断言卡片 running→done 流转、展开可见 args JSON 与 result 全文、终答到达卡片折叠正文在下（quickstart §5 场景 1–3 的手测等价；代码侧 = T003–T006 落地后 type-check 绿 + 浏览器手测由用户过）

### Implementation for User Story 1

- [x] T003 [US1] 状态编排：`web/src/composables/useChat.ts`——增 `ToolActivity` 类型（`{id: string; tool: string; args: Record<string, unknown>; result?: string; isError?: boolean; state: 'running' | 'done' | 'interrupted'}`）与 `ChatMessage.toolActivities: ToolActivity[]` 字段（三个构造点默认 `[]`：`toChatMessage` / `send()` 乐观双气泡 / `loadMessages` 历史行）；`send()` 回调接线：`onToolCall` 按 frame.id 建 running 卡、`onToolResult` 按 id 匹配置 `result` + `isError` → `done`（id 无匹配卡忽略不建卡不崩，FR-004 防御）；所有卡片变化（新增 / 置结果）替换整个数组引用（不可变纪律，v-memo 身份判定的前提——注意既有 `assistant.content +=` 正文直改路径不动，只有 toolActivities 走整数组替换）；任务级门禁 type-check 过
- [x] T004 [US1] 中断兜底：`web/src/composables/useChat.ts`——流收尾四路径（`done` 正常结束 / `error` 事件 / 被动中断 EOF / 主动 abort 的 `settleAborted`）统一把残留 running 卡置 `state: 'interrupted'`（弱化停动画，无 result 不伪造结果，FR-002；与 assistant 行既有「（连接中断）」标注同哲学）；不可变替换；任务级门禁 type-check 过
- [x] T005 [US1] 卡片组件：新建 `web/src/components/chat/ToolActivityCard.vue`（F1/F5 全部视觉）——`defineProps<{ activity: ToolActivity }>()`（`import type { ToolActivity } from '@/composables/useChat'`，emits 无——折叠/展开为组件本地 ref 态、默认折叠、点击头部切换）；状态图标 running=`Loading` + class `is-loading` / done 正常=`CircleCheck` / done+isError=`Warning` 红态 / interrupted=停动画文本「（中断）」弱化（图标具名 import from `@element-plus/icons-vue`，仓内先例 MessageList.vue:89）；显示名剥离 `s<数字>__` 派生前缀（正则 `/^s\d+__/`，剥离失败原样显示——非 MCP 名不误伤），完整调用名进 title；折叠头部 = 状态图标 + 显示名 + 参数键值摘要（args 键值对拼接，空 `{}` 摘要为空）+ 结果首行摘要 + 展开箭头；展开态 = args 完整 JSON 等宽 pretty（`JSON.stringify(args, null, 2)` 纯文本不经 v-html）+ result 全文等宽 pre-wrap 限高内部滚动；样式 scoped 且只用既有 token（`--hf-bg-muted` 底 / `--hf-radius-sm` 圆角 / `--hf-font-mono` 等宽 / `--hf-font-size-sm` 字号 / `--hf-text-3` 正常态辅助色与中断弱化 / `--hf-danger` 错误态），零新增；任务级门禁 type-check 过
- [x] T006 [US1] 气泡内渲染：`web/src/components/chat/MessageList.vue`——assistant 分支链（loading 三点 → error → 正文）**之前**插入卡片列 `v-for`（`msg.toolActivities` 传 props 给 `ToolActivityCard`，列表空则零渲染——未绑工具对话零卡片零回归）；v-memo 依赖数组追加 `msg.toolActivities`（现状五项 `[msg.content, msg.state, msg.errorText, msg.passiveInterrupt, msg.citations]` → 六项，FR-003）；import 新组件；任务级门禁 `cd web && npm run type-check && npm run build` 过

**Checkpoint**: US1 独立可测——绑工具 Agent 对话手测场景 1–3（状态流转 / 并行多卡 / 错误卡）+ 中断场景 5 前半（手测由用户过，代码侧门禁全绿）

---

## Phase 4: User Story 2 - 历史回放分组 (Priority: P2)

**Goal**: 刷新 / 重进会话时中间行与 tool 行归并进其后首个 assistant 终答气泡的卡片清单，args / result 完整可展开；错误中断轮独立成尾部气泡（US2 / FR-005 / FR-001 同形态）

**Independent Test**: 对已含工具调用轮次的会话刷新重进，断言中间行 + tool 行不再以独立消息行出现、归并进终答气泡且卡片可展开见完整 args / result、无终答错误轮独立尾部气泡（quickstart §5 场景 4）

### Implementation for User Story 2

- [x] T007 [US2] 历史分组：`web/src/composables/useChat.ts` `loadMessages`——**先按原始行数截断再分组**（`MESSAGE_MAX` 截断保尾部最新，先分组后截断会把工具行顶出尾窗历史失真，FR-005）；分组算法：连续 assistant 中间行（判定仅「content 空 + tool_calls 非空」，正文非空的行按普通消息行渲染防误吞）与其后 tool 行按 id 配对归并进其后**首个** assistant 终答行 `toolActivities`（中间行供 args / 工具名，tool 行供 result，直接落 done 态、不重演 running、不标 isError——messages 行未存，后端 schema 冻结）；后面没有 assistant 终答行的错误中断轮工具卡独立成尾部气泡（仅卡片无正文）；分组产出行数 ≤ 原始行数（归并只减不增）；产出行照旧 `messages.value = <分组结果>`；不可变纪律（分组是纯函数式重排，新数组新对象）；任务级门禁 type-check 过

**Checkpoint**: US2 独立可测——重进含工具轮会话手测场景 4（手测由用户过，代码侧门禁绿）

---

## Phase 5: User Story 3 - 零回归与防御兜底 (Priority: P3)

**Goal**: 未绑工具 Agent 零卡片、既有聊天行为零回归；历史孤立 tool 行防御分支保留为「分组异常兜底」不删（US3 / FR-008）

**Independent Test**: 未绑工具 Agent 完整对话流（流式 + 刷新回放），断言零卡片、消息三态 / 中断标注 / citations / 重发等既有行为全部不变（quickstart §5 场景 6 + §4 代码侧核对）

### Implementation for User Story 3

- [x] T008 [US3] 防御兜底注释：`web/src/components/chat/MessageList.vue` 历史 tool 行防御分支（:80 附近）注释更新为「分组异常兜底」语义（孤立 tool 行降级旧弱化渲染，页面不崩）——分支**保留不删**（FR-008），仅注释与（如需）条件表述对齐分组后语义；任务级门禁 type-check 过
- [x] T009 [US3] 零回归代码侧核对：三构造点 `toolActivities` 默认 `[]`（未绑工具恒空数组）、v-memo 依赖六项无遗漏、`MessageList` 卡片列对空数组零渲染零副作用；`cd web && npm run type-check && npm run build` 全绿（手测 §5/§6 人工项由用户过，收尾报告列入人工项清单）

**Checkpoint**: 全部用户故事独立功能就绪

---

## Phase 6: Polish & Cross-Cutting Concerns

- [x] T010 手测文档：`docs/testing/chat-manual-test.md` 新增「工具事件渲染」小节——上位 spec §7 六条手测场景照搬（状态流转 / 并行多卡 / 错误卡 / 历史分组 / 中断兜底 / 零回归），含前置（绑工具 Agent）与逐条断言，形态对齐该文档既有小节
- [x] T011 [P] 文档同步：`web/README.md` 组件目录补 `components/chat/` 分组（`ToolActivityCard.vue` 新增；`ChatInput` / `ConversationSidebar` / `MessageList` 按现状目录形态补齐落位——README 树当前缺整个 chat/ 子组）；`docs/changelog/mcp/chat_tool_loop_impl.md` §4 前端工具事件项指向本篇（spec §8 文档同步）
- [x] T012 全量验收门（quickstart §1–4）：`cd web && npm run type-check && npm run build` 全绿；三零边界核对——`git status --short` 无 Go 源 / migrations / go.mod / package.json / tokens.css 改动；交付物存在性核对（§2 五项 + 文档同步两项）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始
- **Foundational (Phase 2)**: 依赖 Phase 1——T002 帧类型与回调签名是 T003 的编译前提
- **US1 (Phase 3)**: 依赖 Phase 2；内部顺序 T003 → T004（同文件先后）→ T005（依赖 T003 的 ToolActivity 类型定义）→ T006（依赖 T005 组件）
- **US2 (Phase 4)**: 依赖 T003（ToolActivity 类型与 ChatMessage 扩展）与 T005/T006（卡片渲染就位——分组产出的卡才有渲染面）
- **US3 (Phase 5)**: 依赖 T007（分组落地后防御分支语义才成立）；T008 → T009
- **Polish (Phase 6)**: 依赖全部用户故事完成；T010 ∥ T011 可并行，T012 收尾

### User Story Dependencies

- **US1 (P1)**: Foundational 后即可开始，无跨故事依赖——单独交付即 MVP（流式可用，历史回放暂为旧防御渲染）
- **US2 (P2)**: 复用 US1 的卡片组件与消息扩展（T005/T006 后开始），独立可测
- **US3 (P3)**: 复用 US2 的分组结果（T007 后开始），纯防御与回归验证

### Parallel Opportunities

- T004 ∥ T005（不同文件：useChat.ts 中断兜底 ∥ 新组件卡片——T005 仅依赖 T003 类型）
- T010 ∥ T011（手测文档 ∥ README / changelog 同步，不同文件）
- T008 ∥ T010/T011（防御注释 ∥ 文档，不同文件——但按故事顺序串行更稳）

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 基线确认 → Phase 2 api 契约修正
2. Phase 3 US1：T003 → T004 → T005 → T006（状态编排 → 中断兜底 → 卡片组件 → 气泡挂载）
3. **STOP and VALIDATE**: 手测场景 1–3 独立验证（用户执行；代码侧 type-check + build 绿）
4. Phase 4 US2（历史分组）→ Phase 5 US3（防御与零回归）→ Phase 6 文档 + 验收门

### 增量交付说明

US1 交付即流式路径工具过程可视化（核心价值，MVP）；US2 补齐一致性体验的另一半（重进会话不丢工具过程、失败轮留痕）；US3 是零回归验收线。无单测框架——每任务 type-check 门禁驱动（逐文件交付节奏见 quickstart §2），全篇收尾 build 全绿 + 三零边界核对 + 手测六条人工项用户过。

---

## Notes

- 任务级 DoD：每个 [T] 完成前过 `cd web && npm run type-check`；红了当场修不攒批
- 冻结契约逐字保真：ToolResultPayload 两字段（`content` / `is_error`）、`ToolActivity` 六字段、帧回调签名、六个语义 token 名——全部对齐 contracts/tool-events.md
- 反作弊红线：不得为过门禁注释掉卡片渲染 / 绕过 type 错误（`any` / `@ts-ignore`）/ 删防御分支——T008 注释更新是唯一授权的既有分支触碰面（不删只改注释语义）
- commit 由用户明示；可提交节点 message：`feat(chat-web): 工具事件渲染——tool_call/tool_result 折叠卡片 + 历史回放分组`
