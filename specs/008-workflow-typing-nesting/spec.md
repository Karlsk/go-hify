# Feature Specification: workflow 分型与子工作流嵌套（chat/task 两型 + sub-workflow 节点）

**Feature Branch**: `008-workflow-typing-nesting`

**Created**: 2026-09-20

**Status**: Draft

**Input**: 冻结契约 [docs/changelog/workflow/impl_spec_08_typing_nesting.md](../../../docs/changelog/workflow/impl_spec_08_typing_nesting.md)（2026-09-20 冻结，O1-O8 已全部拍板）。

## Clarifications

### Session 2026-09-20

- Q: O1「Update 拒改类型」在 API 上如何呈现？ → A: UpdateWorkflowReq 增加type 字段，请求携带 type（同值异值均算）→ 400 拒绝（不比对当前值，与不可变语义严格一致）。
- Q: chat 型请求携带非空 input_schema / output_schema 时行为？ → A: 400 拒绝——「chat 型置空」为强不变量（防脏数据入库被工具篇误消费）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 管理员创建并嵌套 task 型工作流（Priority: P1）

管理员创建 task 型工作流（声明简化 input/output schema），再在父图（chat 型或 task 型均可）里加 sub-workflow 节点引用它——保存时非法嵌套即被拦截：引用 chat 型拒、自嵌/间接环拒、引用链深度超 3 拒、引用不存在拒、inputs 键集缺 required 或多余拒。

**Why this priority**: 嵌套规则系于分型（可被嵌的必须是纯函数语义的 task 型），type 列的真实消费者本篇只有嵌套规则与管理面 CRUD；非法引用在保存期拦截是本篇全部价值的前提。

**Independent Test**: 创建 task 子图 + 父图含 sub-workflow 节点保存成功；构造五类非法引用（引 chat 型 / 自嵌 / A→B→A / 链深超 3 / 引用不存在）逐一被 400 拒绝。

**Acceptance Scenarios**:

1. **Given** task 型子图已存在，**When** 管理员在父图加 sub-workflow 节点（inputs 键集恰好覆盖子 required 字段），**Then** 保存成功。
2. **Given** 被引 workflow 为 chat 型，**When** 保存父图，**Then** 保存被拒（图缺陷）。
3. **Given** 父图引用的目标（直接或间接）又引用回父图自身，**When** 保存，**Then** 保存被拒（环检测）。
4. **Given** 引用链深度超过 3（顶层 + 2 层），**When** 保存，**Then** 保存被拒。
5. **Given** sub-workflow 节点 inputs 键集缺少子 required 字段或含多余字段，**When** 保存，**Then** 保存被拒。

---

### User Story 2 - 执行嵌套图（Priority: P2）

父图执行走到 sub-workflow 节点时逐值渲染 inputs 映射、按子 input_schema 组装 JSON 文本入参、进程内递归执行子图（断连整链取消、每层自包 5min）；子终稿过 output schema 校验后落父变量池（node_key 可引、JSON 值可一级下钻）；子 run 独立落库（trigger_source='workflow'、conversation_id/message_id 与父相同），父收尾回填 parent_run_id——一次查询还原整棵执行树。

**Why this priority**: 嵌套的运行时语义——隔离（父子仅经 input/output 通信）、把关（trial 跟随 / 正式须 published）、轨迹（parent_run_id 树）——是消费方可依赖的契约。

**Independent Test**: 执行含 sub-workflow 节点的父图（execute 端点或绑定 agent 管道），父终稿含子输出；psql 查 workflow_runs 见子行 trigger_source='workflow' 且 parent_run_id 指向父行。

**Acceptance Scenarios**:

1. **Given** 合法嵌套图，**When** 执行父图，**Then** 子图以渲染后的 inputs 运行、子终稿落父变量池、下游模板可引用。
2. **Given** 客户端断连，**When** 父图在子图执行中，**Then** 整链取消，各层 run 行照写。
3. **Given** 正式运行且子图为 draft，**When** 执行到 sub-workflow 节点，**Then** 失败（WORKFLOW_NOT_PUBLISHED 带父 node 前缀）；父试运行则子放开 draft/disabled。
4. **Given** 子终稿不合其 output_schema（required 缺失 / 类型不符 / 声明了 schema 而终稿非合法 JSON），**When** 子图收尾，**Then** 图缺陷 400 带前缀链。
5. **Given** 子图内部节点失败，**When** 错误上抛，**Then** 错误带 `node a: node b:` 前缀链，父记 sub-workflow 节点行 failed、父 run 行 error_node 定位到父节点。

---

### User Story 3 - 分型管理（Priority: P3）

管理员创建工作流时必填 type（chat/task）；类型不可变（Update 拒改，换型 = 删了重建）；存量工作流回填 chat；两型状态机同款（draft/published/disabled 不分叉）；控制台 execute + trial 两型照旧；chat 型管道（spec 07）对两型绑定一视同仁照走管道。

**Why this priority**: 分型的管理面语义决定前端呈现与数据契约；管道回归保证 spec 07 行为零改动。

**Independent Test**: 创建时缺 type 被 400 拒；Update 携带 type 字段被拒；Get/List 返回 type；绑 task 型 workflow 的 agent 发消息照走管道。

**Acceptance Scenarios**:

1. **Given** 创建请求不带 type，**When** 提交，**Then** 400（VALIDATION_FAILED）。
2. **Given** 已存在 workflow，**When** Update 请求携带 type 字段（无论同值异值），**Then** 拒改。
3. **Given** 存量工作流（迁移前创建），**When** 迁移 00020 后查询，**Then** type='chat'。
4. **Given** agent 绑定 task 型 workflow，**When** 发消息，**Then** 照走 spec 07 管道（语义零改动）。

---

### Edge Cases

- 被嵌 task workflow 被删除：保存期预检挡新引用 + 执行期 fail-fast（WORKFLOW_NOT_FOUND 带父 node 前缀）；引用已删 workflow 的存量图在下次编辑保存时被存在性预检拦截。
- 并发互引竞态窗口（A、B 同时保存互相引用）：接受（单管理员内部规模），执行期深度兜底拦截。
- 被引子图无 input_schema：inputs 映射要求恰为 `{input}`（回退单一入参语义）。
- 入参为合法 JSON 对象且子声明了 input_schema → 按对象解析入池；否则整串落 `input`（原行为不变）；池一级下钻 `{{input.x}}` / `{{node.field}}`，string 值行为不变，深度一层为止。
- R10 保存期只校验基名（点号前 ∈ {input} ∪ 祖先 node_key），字段名运行期 strict（缺失/非 JSON → 图缺陷 400 带 node 前缀）。
- 子 run 写入失败：降级同既有语义（重试一次，仍败照常返回，链断处 trace_id 兜底）；父写失败跳过 parent_run_id 回填。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 分型列——workflows 加 type（text NOT NULL CHECK IN ('chat','task')，存量回填 'chat'——存量绑定全为管道用法，回填语义精确）；Create 必填 type（oneof 校验）；Update 拒改类型——UpdateWorkflowReq 含 type 字段，请求携带 type（同值异值均算）即 400 拒绝（被嵌合法性与消费语义系于类型，中途换型等于暗中改写所有引用方的合法性）；Get/List 暴露 type。
- **FR-002**: 结构化 schema 列——workflows 加 input_schema / output_schema（jsonb 可空）；简化形态 `[{name, type, required, description}]`，type ∈ string/number/boolean；仅 task 型消费，chat 型置空——chat 型请求携带非空 schema 即 400 拒绝（强不变量）；Create/Update 校验形态、字段重名与 type 合法性。
- **FR-003**: sub-workflow 节点——类型值 "workflow"（与既有六类值同风格）；config 密封 `{workflow_id（字符串化外键）, inputs（字段→模板映射）}`——键集须逐一覆盖子 input_schema 全部 required 字段、多余字段拒，每个值为 `{{var}}` 模板（既有 R10 模板校验天然覆盖）；NodeType 常量 + ParseNodeConfig 分发 + workflow_nodes.type CHECK 加值。
- **FR-004**: 保存期图校验 R11——对每个 sub-workflow 节点：① workflow_id 存在（同模块 store 直查）；② 被引 workflow 必须 task 型；③ 环检测（从被保存图 DFS 沿被引图 sub-workflow 引用链展开，链上出现被保存图自身 id 即拒，自嵌是长度 1 特例）；④ 引用链深度上限 3（顶层 + 2 层嵌套，与执行期兜底同值）；⑤ inputs 键集覆盖（被引图无 schema 时要求映射恰为 `{input}`——回退单一入参语义）。并发互引竞态窗口接受（单管理员内部规模），执行期深度兜底。
- **FR-005**: 执行引擎 sub-workflow 分支——逐值渲染 inputs 映射（strict，缺失即图缺陷）→ 按子 input_schema 组装 JSON 文本入参 → 进程内递归执行子图（同 goroutine、共享父请求 ctx——断连整链取消；每层自包 5min 超时；深度计数随执行传递，超上限图缺陷 400 带父 node 前缀）→ 子终稿过 output schema 校验后落父变量池（node_key 即该节点 key，下游模板可引、JSON 值可一级下钻）；子 vars 池全新起步（只含自身 input，不可引用父 vars，父也不能引用子内部 var——父子仅经 input/output 通信）。
- **FR-006**: 子图错误上抛——错误二分法原样延伸、带父 node 前缀链（`node a: node b: …` 可读定位）——子图缺陷 → 图缺陷 400；子环境限制 → WORKFLOW_EXECUTION_FAILED 500；下游哨兵透传。父记失败 step（sub-workflow 节点行 failed），父 run 行 error_node 定位到父节点。子终稿 output schema 校验失败 → 图缺陷 400（子作者契约）。
- **FR-007**: 子执行把关——Trial 跟随父（父试运行 → 子放开 draft/disabled）；正式运行子必须 published（否则 WORKFLOW_NOT_PUBLISHED 带父 node 前缀）。
- **FR-008**: 子 run 轨迹——独立 run 行 + trigger_source='workflow'（CHECK 加值）+ parent_run_id 列（父收尾统一回填，append-only 一次窄 UPDATE 例外）；conversation_id / message_id 透传（与父相同，因果归属语义）；子 run 写入失败降级同既有语义（重试一次，仍败照常返回，链断处 trace_id 兜底）。
- **FR-009**: 结构化执行契约——Execute 签名不动（入参仍单一 string）——task 型入参 = 按 input_schema 组装的 JSON 文本；引擎侧检测入参为合法 JSON 对象且声明了 input_schema 时按对象解析入池，否则整串落 input（原行为）；池一级下钻 `{{input.x}}` / `{{node.field}}`（池值为 JSON 时可下钻一层字段，string 值行为不变，深度一层为止）；R10 保存期只校验基名（点号前 ∈ {input} ∪ 祖先 node_key），字段名运行期 strict（缺失/非 JSON → 图缺陷 400 带 node 前缀）；task 型终稿为 JSON 时按 output_schema 校验（required 缺失或类型不符 → 图缺陷 400，声明了 output_schema 而终稿非合法 JSON 同拒）。
- **FR-010**: 删除语义——被嵌 task workflow 删除不扫描引用（jsonb config 引用无法 FK）——保存期预检挡新引用 + 执行期 fail-fast（WORKFLOW_NOT_FOUND 带父 node 前缀）；引用已删 workflow 的存量图在下次编辑保存时被存在性预检拦截。
- **FR-011**: spec 07 管道回归——分型后绑定不 gate——chat 型或 task 型绑定 agent 都照走管道，管道语义零改动。

### Key Entities

- **Workflow.type**: chat / task 两型（text + CHECK，存量回填 chat）；消费者为嵌套规则（chat 型不可被嵌）与管理面 CRUD；不可变。
- **Workflow.input_schema / output_schema**: jsonb 可空，简化字段数组形态；仅 task 型消费；作为 sub-workflow 节点 inputs 键集校验与结构化执行契约的依据。
- **sub-workflow 节点（NodeWorkflow）**: 第七种节点类型；config = workflow_id + inputs 模板映射；执行语义为进程内递归执行子图。
- **WorkflowRun.parent_run_id / trigger_source='workflow'**: 子 run 行的树形关联锚点；父收尾回填，一次查询还原整棵执行树。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: `go build ./...` / `go vet ./...` / `go test ./... -race -count=1` 全绿；workflow 模块各包覆盖率 ≥80%。
- **SC-002**: 依赖方向 grep 不变——internal/workflow/ 无 internal/chat import；api 包无 gin/gorm import。
- **SC-003**: 哨兵错误零新增（四类复用：NOT_FOUND / NOT_PUBLISHED / VALIDATION_FAILED / EXECUTION_FAILED）；CLAUDE.md 错误码表零新行。
- **SC-004**: 既有 spec 06 引擎用例与 spec 07 管道用例全绿（回归零破坏）。
- **SC-005**: 零真实依赖同包测试（service stub Store、store sqlmock、handler httptest）；迁移 00020 applied 且既有迁移文件未动。
- **SC-006**: 嵌套图执行冒烟 + psql 查 runs 树（parent_run_id 关联、各 run seq 顺序）人工通过（docs/testing/workflow-engine-manual-test.md §7 嵌套冒烟小节）。

## Assumptions

- 前置 spec 07 已合入（分支 007-chat-pipeline 本地 8 commits，本 feature 基于其 HEAD——007 未合并 main，两分支均不 push 不合并）。
- 下一迁移号 00020（make migrate-status 已核 19 条 applied）；迁移含：workflows type + CHECK + 回填、input_schema/output_schema jsonb + COMMENT、workflow_nodes.type CHECK 加 'workflow'（重建命名约束 workflow_nodes_type_check）、workflow_runs.trigger_source CHECK 加 'workflow' + parent_run_id + COMMENT。
- 组合根零改动预期（执行器内部递归，无新依赖注入）。
- 下游消费者：chat 管道（spec 07 语义不变）、前端（类型选择 UI 归前端篇）、工具篇（备忘录 Option 2——参数 schema 从 input_schema 生成）。
- 不做：agent 关联 / 工具注册（task 型虚拟工具、绑定按类型把关，归工具篇——前置 chat 工具循环 + mcp 执行能力）；mcp tool 节点执行（既有 fail-fast 语义不变）；工具侧结构化消费；并行分支 / 合并 / 循环节点、定时 / 事件触发；chat 型管道语义改动；异步执行 + 轮询 / 按节点 SSE 流式（递延 deferred_items #8）；前端。
