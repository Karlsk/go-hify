# Feature Specification: Workflow 管道接线（chat-pipeline）

**Feature Branch**: `007-chat-pipeline`

**Created**: 2026-09-20

**Status**: Draft（O1-O8 设计决策已全部拍板并冻结 2026-09-18，见 impl spec 07 §5）

**Input**: User description: "chat spec 07 需求：workflow 管道接线——绑定 agent 的消息确定性先过工作流"

## Clarifications

### Session 2026-09-20

- Q: 下游哨兵 `MODEL_NOT_FOUND` 在 chat 侧现状无映射分支（落 500 `INTERNAL_ERROR`），与 CLAUDE.md 错误码表 `MODEL_NOT_FOUND→404` 冲突（既有缺口，管道路径经 workflow llm 节点模型解析同样可达）——如何处理？ → A: 拍板 Option A：chat 错误映射补 `MODEL_NOT_FOUND → 404` 分支，同时修复原路径与管道路径两处缺口（依据：CLAUDE.md 错误码表上位权威、workflow execute 端点已上线的同错误 404 既有行为、OpenAI/Anthropic 域先例；超出 impl spec 07 §6「补三哨兵」字面范围，经用户拍板纳入本篇错误映射交付物；CLAUDE.md 错误码表零改动——既有行补上实现即准确）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 绑定 agent 的消息确定性先过工作流，终稿即回复 (Priority: P1)

用户在绑定了 workflow 的 agent 会话里发消息：消息确定性先过工作流（入参 = 当前消息内容，每轮独立、历史不喂），工作流终稿整段一次性作为本轮 assistant 回复。流式模式收单条整段 delta + done（usage 全零、finish_reason="workflow"、done 不带 content）；一次输出模式收标准信封 reply。agent 的系统提示词 / RAG 检索 / MCP 工具 / 模型循环全部不参与本轮（绑定即接管，替代语义）——agent 模型配坏不挡管道（模型解析被跳过）。

**Why this priority**: spec 05 递延的 E1 触发形态在本篇闭环——workflow 从「可执行」变成「被对话真实消费」的核心价值入口；管道形态是三者中最简单的消费形态（零模型循环依赖、零 SSE 契约变更），且为 spec 08 分型后所有 chat 型工作流的运行时形态定调。

**Independent Test**: stub 工作流执行接口，断言绑定 agent 的会话发消息 → 执行被调且入参正确（input=当前消息、会话/消息引用回传、非试运行）→ 终稿作为 assistant 回复在两模式下呈现正确（流式 delta+done 序列 / 一次输出 reply 全字段）——交付「绑定即接管、终稿即回复」的完整价值。

**Acceptance Scenarios**:

1. **Given** agent 绑定了 published workflow 42、用户在其会话发「查一下订单」，**When** 流式模式发消息，**Then** 工作流以 input="查一下订单" 被执行，SSE 事件序列恰为 delta(终稿整段) → done（usage 全零、finish_reason="workflow"、不带 content），无 citations 事件。
2. **Given** 同一会话以一次输出模式发消息，**Then** 标准信封返回完整 reply：content=终稿、usage 全零、finish_reason="workflow"、citations=[]。
3. **Given** agent 未绑定 workflow，**When** 发消息，**Then** 原路径零改动（模型循环照常，既有测试全绿即证）。
4. **Given** agent 绑定了 workflow 但其模型配置已失效（模型被删 / 提供商停用），**When** 发消息，**Then** 管道照常执行——模型解析不发生，错误仅来自工作流自身。
5. **Given** 会话首条消息，**When** 管道路径执行，**Then** 会话标题照常按首条用户消息回填（位置语义同原路径）。

---

### User Story 2 - 管道失败的显式错误呈现（不静默降级） (Priority: P2)

绑定的 workflow 未发布 / 已停用 / 图有缺陷 / 环境受限时，用户发消息收到显式错误：503 硬错误（不降级普通对话）、400 图缺陷（错误信息带失败节点 key 前缀，作者可行动）、500 环境限制（可重试语义）、404 防御。管道路径全部失败发生在首次输出之前——流式与一次输出两模式统一走标准错误信封（HTTP 状态码可用，零 SSE error 事件）；下游哨兵（供应商限流 / 模型不存在等）原样透传沿用既有映射。

**Why this priority**: 显式报错可排障——静默降级普通对话会让「workflow 被停用 / 未发布」数周无人发现（O4 拍板理由）；错误呈现是管道形态可用性的另一半。

**Independent Test**: stub 工作流执行接口返回各错误类，断言四类映射（503 / 400 / 500 / 404）在两模式下呈现一致、未绑路径不受影响——交付「失败可诊断、语义不漂移」的完整价值。

**Acceptance Scenarios**:

1. **Given** 绑定的 workflow 处于 draft 或 disabled，**When** 发消息，**Then** 503 `WORKFLOW_NOT_PUBLISHED`（文案区分未发布 / 已停用两态），不降级普通对话。
2. **Given** 工作流图缺陷（如 condition 无命中出边），**When** 发消息，**Then** 400 `VALIDATION_FAILED`，message 带 `node <key>:` 前缀透传。
3. **Given** 环境限制类失败（api 节点 SSRF 拦截 / 总时长超 5min），**When** 发消息，**Then** 500 `WORKFLOW_EXECUTION_FAILED`（可重试语义）。
4. **Given** 工作流内部下游哨兵（`MODEL_NOT_FOUND` / `PROVIDER_BUSY` / `RATE_LIMITED` / `PROVIDER_UNAVAILABLE` 等），**When** 发消息，**Then** 原样透传；`MODEL_NOT_FOUND` 按错误码表映射 404（chat 侧既有缺口随本篇补齐），其余沿用既有映射。
5. **Given** 被绑 workflow 不存在（理论不可达——既有外键约束挡删除），**When** 发消息，**Then** 404 防御。

---

### User Story 3 - 排障：对话链与执行链可互溯 (Priority: P3)

每次管道触发的执行在运行历史可回溯：run 行触发来源为 chat、conversation_id / message_id 回填触发的会话与触发 user 消息（一次查询串起两链）；客户端断连时执行随取消中断而 run 行照写、user 消息已落 assistant 无（与原路径 LLM 流中断同款，不新设规则）；trace_id 串起结构化日志与工作流内部 LLM 节点的调用明细。

**Why this priority**: 「运行日志是排障唯一线索」护栏在 chat 触发路径的延伸——run 行引用列不回填则对话与执行两链断连（O6 拍板理由）；相对 P1/P2 它是伴随性能力（引用回传是执行入参的一部分）。

**Independent Test**: 发一条绑定 agent 的消息（stub 下游），断言运行历史新增行：触发来源=chat、会话 / 消息引用列非空、非试运行；断连场景断言 user 消息已落、assistant 无、run 行照写——交付「两链互溯」的完整价值。

**Acceptance Scenarios**:

1. **Given** 一次成功的管道轮，**When** 查运行历史，**Then** 该次 run 行触发来源为 chat、conversation_id / message_id 等于触发会话与触发 user 消息 id、非试运行。
2. **Given** 客户端在执行中途断连，**When** 执行因取消中断，**Then** run 行照写（轨迹写入与请求 ctx 脱钩的既有语义），user 消息已落库、assistant 无。
3. **Given** 输出事件发出时前端已断连，**Then** 静默收尾（assistant 已落库无碍），无错误呈现。

---

### Edge Cases

- agent 的工作流绑定字段为脏数据（非数字字符串）→ 内部错误防御（同既有模型 id 解析处理，不 panic）。
- 用户消息落库失败 → 整轮失败走标准错误信封，工作流不被调。
- assistant 落库失败 → 仅告警不阻断（终稿已发出无法撤回，同原路径既有语义）。
- 会话标题回填失败 → 仅告警不阻断（既有语义）。
- 等待期无心跳、无字节（惰性提交期间响应头未写）——与 LLM 首 token 等待同形态，前端既有 loading 态复用。
- 管道路径 chat 层零 LLM 调用 → 不记调用明细；工作流内部 llm 节点自记（调用上下文标识置空的既有契约不变），trace_id 串两链。
- agent 绑定的 workflow 在会话中途被停用 / 撤销发布 → 下一轮发消息即显式报错（每轮实时判定，无缓存态）。
- 绑定了 workflow 的 agent 同时配置了系统提示词 / RAG / 工具 → 配置照常保存（绑定校验层面不互斥），但本轮运行时全部不参与（替代语义）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: agent 绑定 workflow 时，消息 MUST 确定性先过工作流且绑定即接管本轮（替代语义）：agent 的系统提示词 / RAG 检索 / MCP 工具 / 模型循环 MUST NOT 参与；未绑定时原对话路径 MUST 零改动。
- **FR-002**: 会话属主校验 + agent 存在且启用检查 MUST 与模型解析 + LLM client 构造拆为两段装配；管道路径 MUST 只走前段（模型配置失效不挡管道）。
- **FR-003**: 管道执行入参 MUST 为：input = 当前消息内容（每轮独立，历史不喂）；触发会话 id 与触发 user 消息 id MUST 回传（run 行引用回填）；试运行标记恒为否。user 消息 MUST 在全部校验通过后落库，首条消息标题回填位置语义同原路径。
- **FR-004**: 终稿呈现 MUST 满足：assistant 消息落库（引用为空）后更新会话活跃时间；流式模式单条整段 delta + done（done 不带 content、usage 全零、finish_reason="workflow"）；一次输出模式返回完整 reply（content / usage 全零 / finish_reason="workflow" / citations 空数组）。
- **FR-005**: 管道路径错误 MUST 在首次输出前失败并以标准错误信封呈现（两模式一致，SSE error 事件零使用）：未发布 / 已停用 → 503 硬错误不降级；图缺陷类 → 400（message 带失败节点 key 前缀透传）；环境限制类 → 500；目标不存在 → 404 防御；下游哨兵原样透传沿用既有映射（`MODEL_NOT_FOUND` 按错误码表映射 404，chat 侧既有缺口随本篇补齐，见 Clarifications）。错误翻译 MUST 在 service 层作为两模式单一事实源，形态对齐既有 LLM 错误翻译。
- **FR-006**: 工作流执行 MUST 感知 ctx 取消（客户端断连 / 请求取消 → 游走中断，run 行照写——轨迹写入与请求 ctx 脱钩的既有语义）；输出时前端已断连 MUST 静默收尾（assistant 已落库无碍）。
- **FR-007**: chat 服务构造 MUST 增注入工作流执行能力：消费方按小接口惯例收窄（单方法），组合根注入实现（结构化类型天然满足）；组合根装配顺序保持 chat 最后（依赖图最外层）。

### Key Entities

- **WorkflowRun 引用回填（既有列，零迁移）**: chat 触发的运行行——触发来源列经会话引用非空判定为 chat；conversation_id / message_id 弱引用回填触发会话与触发 user 消息（执行时 assistant 行尚不存在，引用锚定触发侧）。
- **AssistantReply（既有承载）**: 终稿作为 assistant 消息——usage 全零（工作流结果无用量字段，token 在节点调用明细）、finish_reason="workflow"（前端可分支的新终因值）、citations 空。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 绑定 agent 的会话消息确定性先过工作流：终稿两模式呈现正确（流式 delta+done 序列 / 一次输出 reply 全字段），执行入参断言通过（input / 会话 / 消息引用 / 非试运行）。
- **SC-002**: 错误四类映射正确（未发布 503 / 图缺陷 400 带节点前缀 / 环境限制 500 / 不存在 404）+ 下游哨兵透传沿用既有映射（含 `MODEL_NOT_FOUND → 404` 补齐）；未绑路径零回归（既有测试全绿即证）。
- **SC-003**: 质量门：全量构建 / vet / 竞态测试全绿；chat 模块测试覆盖率 ≥80%（workflow 模块零行为改动）；依赖方向 grep 成立（chat 允许 import workflow/api 白名单内、workflow 无反向依赖）。
- **SC-004**: 以实现 spec 验收门为准：`docs/changelog/workflow/impl_spec_07_chat_pipeline.md` §7 全部满足（含人工项三步走：不绑冒烟 / 绑定冒烟 / 数据库查 run 行引用回填）。

## Assumptions

### 范围边界（明确不做，照搬 impl spec 07 §3）

- 工具形态（task 型虚拟工具、tool_call / tool_result 事件生产）——前置 chat 工具循环（现状 ToolIDs 读到不执行）与 mcp 执行能力；候选 Option 2 已记备忘录（docs/tools/workflow-task-tool-memo.md），归工具篇。
- workflow 分型与嵌套——spec 08（type 列、sub-workflow 节点、嵌套矩阵）；本篇无类型概念，绑定即管道，spec 08 引入分型后本篇语义不变（绑定不按类型把关，归工具篇）。
- memory 开关（管道带历史）——方向已锁（2026-09-18）：上下文组装在 chat 侧拼入 input、执行契约不动；本篇唯一行为为每轮独立，开关落地归后续篇纯增量。
- 进度事件（onNodeDone → SSE 事件、节流）——留缝不动，归工具 / 体验篇。
- 流式终稿——执行流式变体不做（引擎冻结契约不动）；终稿单条整段 delta 一次性呈现，打字机动画归前端。
- NotPublished 降级普通对话——拍板硬错误（显式报错可排障）。
- budget 接线——roadmap 独立项（组合根 TODO 未动）。
- chat 侧调用明细——管道路径 chat 层零 LLM 调用不记；工作流内部 llm 节点自记（调用上下文标识置空契约不变），trace_id 串两链。
- 前端——打字机动画、管道分支 UI 提示归前端篇。

### 其余假设

- 前置 spec 01-06 已合入（执行引擎 commit `5d3297e`；spec 07/08 契约文档 commit `0c34740`）；**零迁移**——19 条 applied 不变，下一迁移号 00020 留给 spec 08。
- O1-O8 设计决策已全部拍板并冻结（2026-09-18，impl spec 07 §5）：管道形态先行、替代语义、每轮独立、统一信封 + 硬错误、单条整段 delta + usage 全零 + finish_reason="workflow"、引用回填（会话 id + 触发 user 消息 id + 非试运行）、进度事件不做、装配拆半管道跳过。
- 下游消费者：前端（既有 SSE 渲染零改动消费 delta / done 事件——finish_reason="workflow" 为新增终因值）。
- chat api 零改动（复用既有发消息接口与回复 schema）；agent 侧零改动（绑定字段每轮已加载）。
- 冻结契约（插入点伪代码、错误映射表、SSE 事件序列、交付物清单）以 `docs/changelog/workflow/impl_spec_07_chat_pipeline.md` §4/§6 为唯一权威，本文件只描述 WHAT/WHY。
- 无前端改动、无新增第三方依赖。
