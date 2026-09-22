# Feature Specification: 工作流 LLM 节点 system_prompt 支持

**Feature Branch**: `011-workflow-llm-system-prompt`

**Created**: 2026-09-22

**Status**: Draft

**Input**: 用户在「工作流编辑器前端增强」需求（8 项，将立 spec 012）中要求 LLM 节点拆 system_prompt / user_prompt 两个输入。经勘察：引擎 LLM 节点现只有单一 `prompt` 字段、executor 只发单条 user 消息——不改后端则 system 语义无法真正生效。用户 2026-09-22 拍板（三选一）：**后端加 system_prompt，加法兼容**。本篇为纯后端加法：`LLMConfig` 加可选 `system_prompt` 字段（`{{var}}` 模板语义与 `prompt` 一致），executor 有值时先渲染再作为 system 消息先于 user 消息发出。本篇是对 impl_spec_06（执行引擎）已冻结 LLM 节点契约的**加法修订**，经用户显式批准立项；既有图（无该字段）行为不变；config 为 jsonb，无迁移。

**前置依赖**: spec 05/06（执行引擎）与 spec 08（typing/nesting）已交付合入 main（工作流引擎全量能力在位）；当前迁移 applied 20 条（下一号 00021，本篇不用）。

**下游消费者**: spec 012（工作流编辑器前端增强）的 LLM 检查器将渲染 system_prompt / user_prompt 两输入框并提交本字段。

## Clarifications

### Session 2026-09-22

- Q: 「start 节点 / LLM 拆分 / api auth」三项含后端契约影响，如何取舍？ → A: 用户拍板：start 节点走前端伪节点（后端零改动）；**LLM 拆分走后端加 system_prompt（本篇）**；api auth 走 headers + 前端预设（后端零改动）。
- 默认决策（未问、按既有契约语义定，第 5 步任务比对停点可推翻）：
  - `system_prompt` 为空串与缺省等价（不发 system 消息）——与 `omitempty` 序列化语义对齐。
  - executions 行 `Input` 记录：有值时加 `system_prompt` 键，无值时不加（旧行为逐字节不变）；node_in 排障摘要同理。
  - 渲染顺序：system_prompt 先于 prompt 渲染（同为 strict；任一缺失变量即 fail-fast，报首个缺失）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 带 system_prompt 的 LLM 节点执行（Priority: P1）

工作流作者给 LLM 节点配置 `system_prompt`（角色设定/输出格式约束等稳定指令，值可含 `{{var}}` 模板）与 `prompt`（user 消息）。执行到该节点时：两个模板先按 strict 语义渲染，模型收到 **system 在前、user 在后** 的消息序列；节点输出（池变量 key）仍为模型回复文本，下游引用方式不变。运行排障记录（node_in 摘要与 executions 行）能读到此节点实际生效的 system_prompt 与 prompt 渲染后值。

**Why this priority**: 本篇唯一的新增价值面——没有它整个 spec 无意义；也是 spec 012 前端两输入框的前提。

**Independent Test**: 构造带 system_prompt 的单 LLM 节点图执行（stub 模型），断言收到的消息序列恰为 [system, user] 且内容为渲染后文本。

**Acceptance Scenarios**:

1. **Given** LLM 节点 config 含非空 system_prompt 与 prompt，**When** 执行到该节点，**Then** 模型收到两条消息：第一条 role=system、内容为 system_prompt 渲染结果，第二条 role=user、内容为 prompt 渲染结果。
2. **Given** system_prompt 含合法 `{{var}}` 引用（如 {{input.question}}），**When** 执行，**Then** 引用按既有 strict 渲染语义替换后再发出。
3. **Given** 带 system_prompt 的 LLM 节点，**When** 执行完成，**Then** node_in 摘要与 executions 行 Input 含渲染后的 system_prompt 值（无值时不含该键）。
4. **Given** 子工作流（嵌套图）内的 LLM 节点带 system_prompt，**When** 父工作流执行触发子图，**Then** 行为与顶层一致（嵌套自然继承，无特判）。

---

### User Story 2 - 既有工作流零影响兼容（Priority: P2）

既有工作流（LLM 节点无 system_prompt 字段，或字段为空串）在创建/编辑保存、trial 执行、正式执行、Agent 绑定调用全链路上的行为与本篇合入前**逐字节一致**：消息序列仍为单条 user 消息；executions 行 Input 形态不变（不新增空值键）；保存期校验（prompt 必填等）无新增拒绝路径。

**Why this priority**: 加法兼容是本篇立项前提（用户拍板「加法兼容」）；它保护全部存量图与手测文档基线，价值独立于 US1 可单独回归验证。

**Independent Test**: 跑既有工作流引擎全量回归测试（不改动任何既有断言），全绿即证明。

**Acceptance Scenarios**:

1. **Given** 无 system_prompt 的既有 LLM 节点，**When** 执行，**Then** 模型收到恰一条 user 消息（序列与内容与合入前一致）。
2. **Given** 无 system_prompt 的既有 LLM 节点，**When** 执行完成，**Then** executions 行 Input 恰为 {"prompt": ...}（无 system_prompt 键）。
3. **Given** 任意既有工作流图配置，**When** 经保存（POST/PUT）再读回，**Then** 序列化往返不引入 system_prompt 键（omitempty 语义）。

---

### Edge Cases

- system_prompt 含缺失变量（strict 渲染失败）→ 在调用模型之前 fail-fast：错误映射图缺陷类（ErrValidationFailed），文案含缺失变量名与节点 key，不产生任何上游模型调用与 executions pre-attempt 记录——与 prompt 渲染失败同语义。
- system_prompt 为空串 → 等同未设置（不发 system 消息、记录不新增键）。
- system_prompt 有值但 prompt 为空 → 保存期即被既有校验拒绝（prompt 必填不变），执行期不可达。
- trial 执行（?trial=true）→ 与正式执行同行为（trial 只影响子图 status 把关，不影响消息组装）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: LLM 节点 config MUST 支持可选 `system_prompt` 字符串字段（json `system_prompt`，omitempty）；缺省/空串均合法，`prompt` 必填校验不变，Validate 无新增拒绝路径。
- **FR-002**: `system_prompt` 的模板渲染语义 MUST 与 `prompt` 完全一致（strict `{{var}}` 替换、一级下钻、缺失即错、报首个缺失变量）。
- **FR-003**: 执行期 system_prompt 非空 → 渲染后作为 system 消息置于 user 消息（渲染后的 prompt）之前发给模型；为空/缺省 → 模型收到的消息序列 MUST 与现状逐字节一致（仅单条 user 消息）。
- **FR-004**: system_prompt 或 prompt 任一渲染失败 MUST 在下游模型调用之前 fail-fast（错误映射 errs.ErrValidationFailed，文案含变量名与节点 key），不触发上游调用、不落 executions（pre-attempt 路径）。
- **FR-005**: 排障记录 MUST 在 system_prompt 有值时一并落：node_in 入参摘要与 executions 行 Input 各含渲染后的 system_prompt；无值时两者不新增键（旧形态不变）。
- **FR-006**: 序列化往返（保存再读回）对无 system_prompt 的既有配置 MUST 不引入该键（omitempty）。

### Key Entities

- **LLM 节点 config（LLMConfig）**: 工作流图 nodes[].config 在 type=llm 时的形态；本篇新增可选属性 system_prompt（string，`{{var}}` 模板）；既有属性 model_id / prompt / temperature 语义不变。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 带 system_prompt 的 LLM 节点执行时，模型侧收到的消息序列恰为 [system, user]，内容由对应模板渲染产出——单测断言消息条数、顺序与内容。
- **SC-002**: 本篇合入后，工作流引擎既有全量测试（含执行引擎、typing/nesting、chat pipeline 回归）零改动全绿——证明存量行为零差异。
- **SC-003**: system_prompt 渲染失败时，错误在模型调用前返回且文案可定位（变量名 + 节点 key）；上游调用次数为 0。
- **SC-004**: 无 system_prompt 的执行记录（executions.Input / node_in）形态与合入前一致（不含空值键）。

## Assumptions

- `system_prompt` 渲染顺序在 `prompt` 之前（同为 strict；首个缺失变量决定报错文案），对可观测行为无差异，仅固定实现顺序。
- eino 消息模型支持 system 角色（执行调用点直接组装，无适配层改动）。
- 前端 UI（两输入框回填/提交）归 spec 012，本篇交付后字段即可被 JSON 配置直接使用。
- 无迁移：config 为 jsonb 列内容演进，存储层无感知。
