# Feature Specification: Chat 前端工具事件渲染（tool_call / tool_result 折叠卡片 + 历史回放分组）

**Feature Branch**: `017-frontend-tool-events`

**Created**: 2026-09-30

**Status**: Draft

**Input**: 上位 spec [docs/changelog/chat/tool_events_frontend_spec.md](../../../docs/changelog/chat/tool_events_frontend_spec.md)（2026-09-30 用户批准定稿，F1–F5 冻结契约）；上位约定 [frontend_page_spec.md](../../../docs/changelog/chat/frontend_page_spec.md)（§11/§12/§14/§15 布局 / token / 交互约定沿用）

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 流式路径的工具活动可视化（Priority: P1）

用户对绑定 MCP 工具的 Agent 提问：工具执行期间 assistant 气泡顶部出现折叠卡（旋转图标 + 工具名 + 参数摘要），工具返回后卡片转完成态、可点击头部展开查看完整参数与结果文本；同轮并行多工具多卡并列、某工具出错时红警示且对话不中断；流中断后未完成的卡标「（中断）」弱化。

**Why this priority**: 后端已发 tool_call / tool_result 帧，前端忽略这两帧导致工具执行期是黑盒（只有三点等待动画）——这是本篇的核心价值：打开流式路径的工具过程可视化。单独交付即构成可用增量（流式可用，历史回放暂为旧防御渲染）。

**Independent Test**: 选绑定 MCP 工具的 Agent 对话触发一次工具调用，断言卡片经历 running→done 状态流转、展开可见 args / result、终答到达后卡片折叠正文在下（手测清单 §1/§2/§3）。

**Acceptance Scenarios**:

1. **Given** 绑定 MCP 工具的 Agent 会话，**When** 用户发送触发工具调用的消息，**Then** 工具执行期间 assistant 气泡顶部出现 running 折叠卡（旋转图标 + 剥离派生前缀的工具名 + 参数键值摘要），工具返回后卡片转完成态（is_error 时红警示态）、结果首行摘要进折叠头部
2. **Given** 同一轮 LLM 并行发起多个工具调用，**When** 各工具先后返回，**Then** 多卡并列、各自按到达序独立完成状态流转
3. **Given** 某工具返回错误（is_error），**When** 卡片转错误态，**Then** 红警示图标 + 错误首行摘要、展开可见错误全文，对话不中断（后端喂回 LLM 自纠，后续轮正常）
4. **Given** 流在工具卡未完成时结束（done / error 事件、被动断连、主动停止），**When** 流收尾，**Then** 残留 running 卡标「（中断）」弱化并停止动画，无 result 不伪造结果

---

### User Story 2 - 历史回放分组（Priority: P2）

用户刷新页面 / 重进会话：历史端点返回的 assistant 中间行（正文空 + tool_calls）与 tool 行按 id 配对归并进其后首个 assistant 终答气泡的工具卡清单，args / result 完整可展开；错误中断轮（后面没有 assistant 终答行）的工具卡独立成尾部气泡，保住失败轮留痕可查。

**Why this priority**: 历史回放与流式路径同一形态是「一致性体验」的另一半——只做流式不做回放，重进会话后工具过程就消失，失败轮不可追溯（违背 §14 失败留痕哲学）。依赖 US1 的卡片组件，故排 P2。

**Independent Test**: 对一个已含工具调用轮次的会话刷新重进，断言中间行 + tool 行归并进对应终答气泡、卡片可展开见完整 args / result、无终答的错误轮卡片独立成尾部气泡（手测清单 §4）。

**Acceptance Scenarios**:

1. **Given** 会话历史含「assistant 中间行 + tool 行 + assistant 终答行」序列，**When** 用户重进会话，**Then** 中间行与 tool 行不再以独立消息行出现，而是作为终答气泡顶部的工具卡（id 配对：中间行供参数与工具名、tool 行供结果），终答正文正常渲染
2. **Given** 错误中断轮后面没有 assistant 终答行，**When** 历史回放，**Then** 该轮工具卡独立成一个尾部 assistant 气泡（仅卡片无正文），失败轮留痕可查
3. **Given** 历史行数超过截断上限，**When** 回放，**Then** 截断按原始行数（含中间行 / tool 行）计、保尾部最新

---

### User Story 3 - 零回归与防御兜底（Priority: P3）

未绑定工具的 Agent 对话零卡片、现有聊天行为零回归；历史 tool 行的防御渲染分支保留为「分组异常兜底」——分组后正常不产出独立 tool 行，数据异常的孤立行降级旧渲染不崩。

**Why this priority**: 本篇触碰聊天核心链路（发送编排 + 消息渲染），零回归是交付底线；防御分支兜底保证历史数据异常时页面不白屏。非新能力，故排 P3，但作为验收门与 US1/US2 一并判卷。

**Independent Test**: 对未绑定工具的 Agent 跑完整对话流（流式 + 刷新回放），断言零卡片、消息三态 / 中断标注 / citations / 重发等既有行为全部不变（手测清单 §6）。

**Acceptance Scenarios**:

1. **Given** 未绑定工具的 Agent 会话，**When** 完整对话 + 刷新，**Then** 零工具卡出现、既有行为（三态 / 中断标注 / citations / 错误重发）零回归
2. **Given** 历史数据出现分组无法归并的孤立 tool 行，**When** 回放，**Then** 该行走既有防御性弱化渲染，页面不崩、其余分组不受影响

### Edge Cases

- 流在 tool_call 之后、tool_result 之前中断（断网 / 主动停止 / error 事件）：残留 running 卡标「（中断）」弱化停动画——与 assistant 行既有「（连接中断）」标注同哲学；主动 abort 不加（用户主动行为，assistant 行既有语义不变）
- tool_result 帧的 id 无匹配卡（理论上后端契约保证 tool_call 先行；异常序到达）：忽略该帧不建卡不崩
- 中间行判定边界：仅「正文空 + tool_calls 非空」的 assistant 行参与归并；正文非空的行按普通消息行渲染（防御）
- args 为空对象：折叠头部参数摘要为空、展开态显示 `{}`；result 超长：展开态在限高内滚动，不撑爆气泡
- 一次输出模式（stream:false）：中间行照常落库但页面 v1 纯流式，UI 不变（边界，不在本篇）
- 历史回放不标 is_error：messages 行未落 is_error（后端 schema 冻结），回放期卡片一律正常态——错误文本本身可读

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 工具活动 MUST 收进所属 assistant 气泡顶部（正文 / 等待动画 / 错误文本之前），不占独立消息行，按到达序排列；折叠态 = 头部行（状态图标 + 显示名 + 参数键值摘要 + 展开箭头），点击头部切换展开，展开态显示参数完整 JSON 与结果全文（限高内部滚动），默认全部折叠
- **FR-002**: 卡片状态机 MUST 为 running（tool_call 到达：旋转图标 + 工具名 + 参数摘要）→ done（tool_result 到达：正常完成态；is_error 为红警示态 + 错误摘要）；流结束（done / error / 被动中断 / 主动中止收尾）后残留 running 卡 MUST 标「（中断）」弱化并停止动画，不得伪造结果
- **FR-003**: 卡片数据更新 MUST 不可变——任何卡片变化（新增 / 置结果 / 中断标注）替换整个数组引用；消息行渲染跳过依赖（v-memo）MUST 追加工具活动数组项，保证只有受影响行重渲染、其余行不动
- **FR-004**: 流式路径 MUST 以按调用 id 配对的回调消费两帧：tool_call 建卡（running）、tool_result 置结果与错误标志并转 done；id 无匹配卡的结果帧忽略不崩
- **FR-005**: 历史回放 MUST 先分组再入消息列表：连续 assistant 中间行（正文空 + tool_calls 非空）与其后的 tool 行按 id 配对归并进「其后首个 assistant 终答行」的工具卡清单（中间行供参数 / 工具名，tool 行供结果）；后面没有 assistant 行的错误中断轮工具卡 MUST 独立成尾部气泡（仅卡片无正文）；分组产出行数 MUST 不超过原始行数、截断上限按原始行数计（保尾部最新）
- **FR-006**: 卡片显示名 MUST 剥离派生前缀（`s<数字>__`，如 `s1__weather` → `weather`），完整调用名进悬浮提示（title）
- **FR-007**: API 层契约修正 MUST 对齐后端帧载荷：工具结果帧的 result 收敛为「结果文本 + 错误标志」两字段结构（ToolResultPayload）；帧分发 MUST 补 tool_call / tool_result 两分支；「后端 v1 预留不触发」过期注释与工具调用接口的过时注释 MUST 删除 / 修正
- **FR-008**: 历史 tool 行的防御渲染分支 MUST 保留为「分组异常兜底」（孤立行降级旧弱化渲染），注释更新为兜底语义，不得删除
- **FR-009**: 视觉 MUST 零新增设计 token——卡片只用既有语义 token（弱化背景 / 小圆角 / 等宽字体 / 小字号 / 辅助色 / 危险色），状态图标用既有图标库组件（加载 / 完成 / 警告），tokens.css 不动

### Key Entities *(include if feature involves data)*

- **ToolActivity（工具活动卡，新数据形态）**: id（调用 id——流式帧配对与历史行配对的锚点）/ tool（调用名，展示层剥离派生前缀）/ args（参数对象）/ result（结果文本，可选）/ isError（错误标志，可选）/ state（`running | done | interrupted`）；挂在 assistant 消息行上，不可变替换纪律（FR-003）
- **ChatMessage（既有消息行，扩展）**: 增工具活动清单字段；其余字段与既有乐观行 / 服务端行结构不变
- **SSE 帧 tool_call / tool_result（后端契约，冻结不碰）**: tool_call 携带 {id, tool, args}；tool_result 携带 {id, tool, result:{content, is_error}}——前端只消费不改
- **历史行（assistant 中间行 / tool 行，后端契约，冻结不碰）**: 中间行正文空、tool_calls[0]={id, tool, args}；tool 行同构 id 锚点——分组配对的唯一依据

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 流式路径工具活动全程可见可读——绑定工具的 Agent 对话中，每轮工具调用均产生状态流转正确的折叠卡（running→done / 错误态 / 中断态），无需任何用户额外操作（手测 §1/§2/§3 逐条通过）
- **SC-002**: 历史回放分组正确——重进会话后中间行 / tool 行不再以独立行出现、归并进对应终答气泡且 args / result 完整可展开，无终答的错误轮留痕为独立尾部气泡（手测 §4 通过）
- **SC-003**: 零回归——未绑定工具的 Agent 对话零卡片，既有聊天行为（三态 / 中断标注 / citations / 错误重发）全部不变（手测 §5/§6 通过）
- **SC-004**: 自动化验收门全绿：前端类型检查与构建门禁通过，且依赖清单不动（package.json 零改动）
- **SC-005**: 改动面收敛：零后端改动、零新依赖、零新增设计 token——全部改动落在前端消息接口层 / 编排层 / 两个消息组件 / 手测文档五个交付物内

## Assumptions

- 前置：chat 工具循环批（`0d6293f`）已合入——后端已发 tool_call / tool_result 帧、历史已落 assistant 中间行与 tool 行；帧结构（json tag）、结果载荷（content + is_error）、行配对锚点（tool_calls[0]={id, tool, args}）均已核实
- 后端契约保证同一调用的 tool_call 帧先于 tool_result 帧到达；前端不做乱序兼容，孤儿结果帧忽略（边界行为，非缺陷）
- 历史回放不标 is_error：messages 行未落 is_error（后端 schema 冻结），回放期卡片一律正常态，补列属未来后端项
- 明确不做（边界照搬上位 spec §3）：卡片无重试 / 重放按钮（后端无重调端点）、无耗时显示（帧不带时间戳不伪造）、workflow 进度事件（另一遗留项）、一次输出模式 UI、工作流编辑器 tool 节点面板（与 spec 016 边界一致另篇）
- 设计 token 只增不改（宪法技术约束）；本篇零新增，仅引用既有语义 token
- 人工验收（六条手测清单）由用户执行，落位 docs/testing/chat-manual-test.md 新增小节；自动化门禁不覆盖真实 LLM / MCP server 侧行为
