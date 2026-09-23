# Feature Specification: 工作流拖拽编辑器八项增强

**Feature Branch**: `012-workflow-editor-enhancements`

**Created**: 2026-09-22

**Status**: Draft

**Revision**: 2026-09-23 裁定——取消画布伪「开始」节点，entry 改由左面板「起始节点」下拉直接指定，并与双击节点、检查器「设为起始」按钮三入口并存；画布内 schema 配置入口改挂左面板「入参 / 出参」按钮（US5 / FR-002 / Key Entities / SC-003 / Assumptions 已同步改写）。

**Input**: 用户 2026-09-22 反馈的拖拽编辑器八项可用性缺口（删除入口 / 伪开始节点入参出参配置 / 节点 key 改名 / LLM 双输入框 / API 节点 headers+auth+body 表单 / 子工作流入参自动渲染 / 变量引用下拉 / 模板字段可写），含三项已拍板契约分叉：开始节点 = 左面板「起始节点」下拉直接指定 entry（2026-09-23 修订：取消伪节点方案，后端零改动，schema 走既有 input_schema/output_schema 契约字段）；LLM 拆分 = 消费 spec 011 后端 system_prompt；API auth = headers + auth 预设纯前端（序列化进 config.headers，无独立 auth 字段）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 画布结构编辑：可见删除入口与 key 改名 (Priority: P1)

编排者在拖拽模式选中节点后，通过检查器可见的「删除节点」按钮删除节点——关联连线清理、位置清理、起始迁移等既有级联语义不变，不再依赖不可见的 Backspace 快捷键；通过检查器 Key 输入框改节点 key（非空、不冲突），节点 id、连线端点、起始指向、位置、选中态级联更新，不再切 JSON 模式手改。

**Why this priority**: 用户反馈第一条——节点删不掉是编辑器基础操作残缺；key 拖入即固定迫使编排者离开拖拽模式，两者都是结构编辑的地基。

**Independent Test**: 拖入节点 → 检查器点「删除节点」→ 核对画布与切 JSON 后的图主体；改 key → 切 JSON 核对旧 key 零残留。

**Acceptance Scenarios**:

1. **Given** 拖拽模式有节点 A 及其出边，**When** 选中 A 点「删除节点」，**Then** A 与关联连线消失，起始若是 A 则迁移到剩余首个无入边节点（图入口；全成环回退剩余首个），画布选区清空
2. **Given** 节点 llm_1，**When** 检查器把 Key 改为 classify，**Then** 节点 id、连线端点、起始指向、位置随动更新，切 JSON 核对旧 key 零残留
3. **Given** 已有 key classify，**When** 把另一节点 Key 改为 classify 或清空，**Then** 前端拦截（提示冲突/非空），图不变

---

### User Story 2 - 变量引用下拉与可写模板字段 (Priority: P2)

编排者在任何模板字段（LLM 双 prompt、end output、condition expression、API url/body/header 值、子工作流入参值）点开变量下拉：分组列出 input（task 型按入参 schema 展开 `{{input.x}}`）、上游祖先节点 key、上游子工作流节点按其声明的 output_schema 展开 `{{key.field}}`；选中插入到光标处；文本仍可自由手写、与插入内容混排。

**Why this priority**: 第 7/8 条反馈——手写 `{{var}}` 记不住变量名，是每天使用的日常痛点；该组件是 US3/US4 新增表单字段的公共底座，先行交付避免返工。

**Independent Test**: 搭含 task 入参 schema 与上游 workflow 节点的图，在既有 prompt 字段下拉选取与手写混排，切 JSON 核对文本逐字符一致。

**Acceptance Scenarios**:

1. **Given** task 型图且已配 input_schema 字段 q，**When** 在 prompt 字段点变量下拉，**Then** 列出 `{{input}}`、`{{input.q}}`、上游祖先节点 key、上游 workflow 节点的 `{{key.field}}`
2. **Given** 光标位于 prompt 文本中间，**When** 选中某变量，**Then** 插入发生在光标处，光标前后手写文本不受影响
3. **Given** chat 型图（无 input_schema），**When** 打开下拉，**Then** 列出 `{{input}}` 与上游节点引用，无入参展开分组

---

### User Story 3 - 检查器节点表单补全：LLM 双输入框与 API 全要素 (Priority: P3)

编排者配 LLM 节点时在 System Prompt（角色设定，可选）与 Prompt（用户输入模板，必填）两个文本域分开编辑，均接入变量下拉；配 API 节点 POST 时填 Body 模板、加 Header 行（如 Content-Type）、Auth 预设选 Bearer Token / Basic 填凭据——全部序列化进 config 既有键（system_prompt / headers / body），无新增后端字段。

**Why this priority**: API 节点只有 Method/URL，POST 场景功能不可用（阻断级）；LLM system_prompt 后端已由 spec 011 交付（commit 3c82c3f），前端消费是既定下一步。

**Independent Test**: 配一个 POST API 节点（Content-Type header + Bearer token + body 模板）与带 system_prompt 的 LLM 节点，保存后详情回读核对 config 键值形态。

**Acceptance Scenarios**:

1. **Given** LLM 节点，**When** 填 System Prompt 与 Prompt，**Then** 切 JSON 核对 config 含 system_prompt / prompt 两键；System Prompt 留空时 config 不携带 system_prompt 键
2. **Given** API 节点，**When** 加 Header 行 Content-Type=application/json、Auth 选 Bearer 填 token、填 Body 模板，**Then** config.headers 含该 header 与 `Authorization: Bearer <token>`，config.body 为模板文本
3. **Given** API 节点已配 Bearer，**When** Auth 切到 Basic 重填，**Then** headers 只保留一条新 Authorization 行，旧值无残留

---

### User Story 4 - 子工作流入参自动渲染 (Priority: P4)

编排者在子工作流节点选定子流程后，检查器按该子流程 input_schema 自动渲染入参行——字段名来自 schema 不可改，每行值用变量下拉填模板；切换子流程重建行并尽量保留同名字段已填值；子流程无 schema 给提示；子工作流下拉排除工作流自身（编辑态）。

**Why this priority**: 第 6 条反馈——inputs 手写字段名不知道子流程要什么入参，错字到运行期才暴露；可绕（手写正确字段名）但易错。

**Independent Test**: 建带 input_schema 的子流程，在父图选中它核对入叧行；切换另一个子流程核对重建与同名值保留。

**Acceptance Scenarios**:

1. **Given** 子流程声明 input_schema [question, top_k]，**When** 父图节点选中它，**Then** 检查器渲染两行入参（字段名锁定），值可经变量下拉填模板
2. **Given** 已为 question 填值，**When** 切换到同样声明 question 字段的另一子流程，**Then** question 行保留已填值，其余行按新 schema 重建
3. **Given** 编辑工作流 W 自身，**When** 打开子工作流下拉，**Then** 列表不含 W
4. **Given** 子流程无 input_schema，**When** 选中它，**Then** 检查器显示提示而非空表单

---

### User Story 5 - entry 下拉指定与画布内 schema 配置 (Priority: P5)

entry（start_node_key）由画布左面板「起始节点」下拉直接指定（2026-09-23 裁定：取消画布伪「开始」节点）——选项 = 画布现存节点 key（`key（类型名）` 形态），首个拖入自动默认、删除起始迁移；双击节点与检查器「设为起始」按钮为等价入口，三入口并存、全部汇到同一 startKey 单源。左面板「入参 / 出参」按钮打开检查器 schema 面板——task 型为入参/出参 Schema 行表单（复用既有行表单语义与校验），chat 型显示单一 input 说明；schema 编辑与宿主页既有表单状态同源，一处改动另一处同步。

**Why this priority**: 第 2 条反馈——入参/出参配置散落在两步式第一步或编辑页抽屉，画布看不到流程入口；拍板为下拉指定 entry、面板按钮进 schema 配置，后端零改动。非阻断（抽屉路径仍可用），排后。

**Independent Test**: task 型在两步式第二步与编辑页分别核对：面板改 schema → 第一步表单/抽屉同步（反向亦然）；下拉切换起始 → 主色强调边框迁移、start_node_key 随动。

**Acceptance Scenarios**:

1. **Given** task 型画布，**When** 点左面板「入参 / 出参」按钮，**Then** 检查器展示入参/出参两段行表单，与宿主页表单同源同步
2. **Given** chat 型画布，**When** 点「入参 / 出参」按钮，**Then** 面板显示单一 input 说明，无 schema 编辑
3. **Given** 面板改过 schema，**When** 保存，**Then** 提交载荷含最新 input_schema/output_schema，start_node_key 指向下拉选定的真实节点 key
4. **Given** 起始节点 A 与另一节点 B，**When** 双击 B / 在 B 的检查器点「设为起始」/ 下拉选 B，**Then** 主色强调边框迁移到 B、start_node_key 随动（三入口等价）
5. **Given** readonly 态，**When** 查看画布，**Then** 左面板与检查器不渲染，仅起始节点强调边框与「起始」角标可见

---

### User Story 6 - 存量回归与往返一致性保护 (Priority: P6)

既有行为零差异的证据链：JSON 模式语义、双模式往返一致（含未知 config 键透传）、readonly 态、脏态守卫、两步式创建、编辑保存链路全部不变；本 story 是验证型，无独立实现。

**Why this priority**: 八项增强全部落在拖拽模式检查器与画布，回归面大（九类既有能力），须显式验证既有契约不破——验证收口放最后。

**Independent Test**: 走 docs/testing/workflow-frontend-manual-test.md 既有 EC 用例（§5 EC-1~11、§7 EC-12~23）全数复测通过。

**Acceptance Scenarios**:

1. **Given** 含未知 config 键的工作流，**When** 拖拽 ↔ JSON 往返，**Then** 未知键透传不丢
2. **Given** readonly 详情，**When** 检查所有新增交互（删除/改名/schema 面板/auth/变量下拉），**Then** 全部禁用或不可达
3. **Given** 编辑页修改后未保存，**When** 离开，**Then** 脏态守卫照常拦截

### Edge Cases

- 删除起始节点 → 起始迁移至剩余首个无入边节点（图入口；全成环回退剩余首个，migrateStartKey 语义）；删到空图 → startKey 置空，保存期前端预检照常拦截
- key 改名后 positions 残留 → 改名迁移位置条目、删除清位置条目（会话级无泄漏）
- key 改名为与原值相同 → 无变化（幂等）
- LLM system_prompt 空串与缺省等价（config 不携带该键，对齐后端 omitempty）
- API headers 重复键以后写覆盖（与后端 map 语义一致）；空键行不产出 config 条目
- 子工作流切换后旧值无同名字段可保留 → 丢弃
- 变量下拉不校验引用合法性（手写非祖先引用由保存期后端 R10 校验兜底，走既有错误提示通道）
- 子工作流下拉在创建态（工作流未落库无 id）无从排除自身 → 不排除（此时无自身可引用）

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 检查器 MUST 提供节点/连线可见删除入口（按钮），删除节点 MUST 沿用既有级联语义（悬挂边清理、位置清理、起始迁移、选中态清空）。
- **FR-002**: entry MUST 由左面板「起始节点」下拉直接指定（选项 = 画布现存节点 key，`key（类型名）` 形态），双击节点与检查器「设为起始」按钮 MUST 为等价入口（三入口汇到同一 startKey 单源，readonly 均不可用）；左面板「入参 / 出参」按钮 MUST 打开检查器 schema 面板——task 型为入参/出参 Schema 行表单（复用既有行表单语义与校验），chat 型显示单一 input 说明；schema 编辑 MUST 与宿主页既有表单状态同源（一处改动另一处同步）。
- **FR-003**: 检查器 MUST 支持编辑节点 Key：非空、不与画布内其他 key 冲突、不超后端 key 长度上限；保存 MUST 级联更新节点 id、连线端点、起始指向、位置 Map、选中态；非法输入 MUST 前端拦截。
- **FR-004**: LLM 检查器 MUST 拆 System Prompt + Prompt 两文本域，读写 config.system_prompt / config.prompt；system_prompt 可选（空 = 不携带）。
- **FR-005**: API 检查器 MUST 补全：Headers 键值行编辑 + Auth 预设（无 / Bearer Token / Basic 用户名密码，选择即向 headers 注入对应 Authorization 行，切预设清旧行）+ Body 模板文本域；全部 MUST 序列化进 config 既有键，无新增后端字段。
- **FR-006**: 子工作流节点选中子流程后 MUST 自动按其 input_schema 渲染入参行：字段名来自 schema 不可改，值为模板；切换子流程 MUST 重建行并尽量保留同名字段已填值；子流程无 schema MUST 给提示；下拉 MUST 排除工作流自身（编辑态）。
- **FR-007**: 模板字段通用组件 MUST 提供：文本域 + 变量引用下拉，变量源 = input（task 型按入参 schema 展开）∪ 上游节点 key（祖先）∪ 上游子工作流节点按其声明的 output_schema 展开；选中 MUST 插入到光标处；手写 MUST 不受限。
- **FR-008**: 全部检查器模板字段（LLM 双 prompt、end output、condition expression、API url/body/header 值、子工作流入参值）MUST 统一接入 FR-007 组件。
- **FR-009**: 既有行为 MUST 不变：JSON 模式语义、双模式往返一致（含未知 config 键透传）、readonly 态、脏态守卫、两步式创建、编辑保存链路。

### Key Entities *(include if feature involves data)*

- **起始节点指定（entry）**: start_node_key 的画布编辑面——左面板「起始节点」下拉（选项 `key（类型名）`）、双击节点、检查器「设为起始」按钮三入口等价，汇到同一 startKey 单源；起始节点以主色强调边框标识（readonly 态附「起始」角标）；左面板「入参 / 出参」按钮承载 schema 配置入口（task 型入参/出参）。
- **变量引用条目**: 变量下拉的最小单元——插入文本（如 `{{input.q}}`、`{{classify}}`、`{{fetch.field}}`）+ 展示名 + 来源分组（入参 / 上游节点 / 子流程出参）。
- **模板字段**: 支持变量引用的可写文本域（prompt/system_prompt/output/expression/url/body/header 值/子流程入参值），手写与下拉插入混排。
- **Auth 预设**: API 节点的鉴权快捷配置（无 / Bearer Token / Basic），序列化表现为 config.headers 的 Authorization 行，无独立存储形态。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 用户反馈的八项可用性缺口全部可经画布/检查器完成（删除走可见按钮、key 画布内改名、LLM 双输入框、API POST 全要素可配、子工作流入参自动渲染、变量经下拉插入、模板字段可写）——manual-test 增补小节逐项人工验证通过。
- **SC-002**: 双模式往返一致性（含未知 config 键透传）与既有 EC-1~EC-23 人工用例全数复测通过（零回归）。
- **SC-003**: 提交载荷形态与后端契约对齐——图主体节点均为真实节点、start_node_key 指向现存节点 key，config 键集不超出后端已交付契约（system_prompt/headers/body/inputs 等既有键），保存后详情回读逐键核对通过。
- **SC-004**: readonly 态零新增可交互元素（新增交互全部禁用）。
- **SC-005**: 前端质量门禁（类型检查 + 构建）全绿。

## Assumptions

### 范围边界（明确不做）

- 后端零改动（无新路由/新字段/新迁移；本篇纯 web/ 前端）。
- 不做画布撤销/重做、多选、框选、对齐线、缩略图。
- 不做 tool / knowledge_retrieval 节点检查器表单（面板无此两类）。
- 不做 API timeout_sec / ssl_verify 表单（保持 JSON 模式编辑）。
- 不做变量值的运行时预览 / 试执行联动。
- 不改 JSON 模式文本编辑器本身（增强全部落在拖拽模式检查器与画布）。

### 其余假设

- 后端前提已满足：spec 011 system_prompt 已合入（commit 3c82c3f）；工作流详情 GET 已返回 input_schema/output_schema（spec 008 交付），子工作流入参渲染与出参展开直接消费，无需后端配合。
- 前端不引入新依赖（不新增组件库 / 测试框架）；验收面 = 质量门禁 + manual-test 人工项。
- schema 单源：宿主页表单（两步式第一步 / 编辑页抽屉）是既有持有者，「入参 / 出参」面板与其同源同步（具体绑定方式留 plan 决定）。
- key 长度上限对齐后端 key 契约（≤64）；非法字符集不额外收紧。
- key 改名级联只覆盖结构性引用（节点 id、连线端点、起始指向、位置、选中态），不改写其他节点模板文本中的 `{{old_key}}` 引用——模板是自由文本，程序化改写有误伤风险；改名后旧引用由保存期 R10 后端校验暴露，走既有错误提示通道。
- 变量下拉不校验引用合法性（保存期 R10 后端兜底，前端复用既有错误提示通道）。
- API Headers 重复键以后写覆盖（与后端 map 语义一致）。
