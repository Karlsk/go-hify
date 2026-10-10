# Feature Specification: Workflow 画布开始/结束节点与入参默认值（伪 start 节点 + end 输出映射 + SchemaField default）

**Feature Branch**: `018-workflow-start-end-nodes`

**Created**: 2026-10-09

**Status**: Approved（2026-10-09 用户定稿；D2 `output_fields` 后端加法键偏离已随定稿确认）

**Input**: 2026-10-09 会话决策 D1–D5（用户批准按推荐定）；上位契约 [docs/changelog/workflow/](../../../docs/changelog/workflow/) 系列——impl_spec_06（O1 单一 input string 终形）、impl_spec_08（分型与嵌套、SchemaField 简化形态）、impl_spec_012（编辑器八项增强）、impl_spec_013（试运行三态入参）、impl_spec_014（llm output_schema 加法键先例）；图校验 R1–R12 与执行契约见 db_model.md / api_contract.md。

**裁定反转记录**: D1 部分反转 spec 012（2026-09-23）「取消画布伪开始节点」裁定。反转理由：当时裁定语境是简化 entry 指定方式，未预见任务型 workflow 的「入参配置需要画布可视化锚点」诉求；本篇恢复画布伪开始节点仅作为 input_schema 配置入口与视觉锚点，entry 语义（`start_node_key` + 三入口指定）不变，后端契约零改动。

## Clarifications

### Session 2026-10-09

- Q: FR-004「task 型限一个 end 启用映射模式」是否维持？ → A: **反转——多 end 映射放开**（用户修正，对齐 Dify 多输出终态：终稿 JSON 内容取决于运行时走到哪个 end；Hify 线性游走一次 run 恰好终止在一个 end，`buildOutput` 按节点自身 `output_fields` 构造天然支持）；图级 output_schema = 全部映射 end 字段并集，同名同型合并、同名不同型冲突前端保存期拦截；后端仅 EndConfig 行级校验（name 非空不重名、ref 为单一 `{{var}}`），无跨 end 规则。
- Q: 并集 schema 的 required 标记如何确定？ → A: 自动推导——全部映射 end 都产出 = required、部分产出 = optional；映射行不设手工 required 开关（与 validateOutputSchema 运行期语义严格一致：required = 任何路径必然在场）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 画布开始节点：入参配置可视化（Priority: P1）

用户编辑 task 型 workflow：画布左侧固定渲染一个「开始」节点，角标显示入参数（如「3 入参」）；点击打开检查器编辑 input_schema（含默认值列）。chat 型 workflow 的开始节点点击后显示只读说明「单一入参 {{input}}」。

**Why this priority**: 当前入参配置藏在左面板「入参/出参」按钮后面，画布上看不到流程吃什么进来——任务型 workflow 的核心信息不可见。本篇基础体验项。

**Independent Test**: 新建 task 型 workflow 进画布，断言开始节点默认渲染、不可删除不可拖入回收、点击可编辑入参、保存后重新打开入参数角标正确；chat 型显示只读说明（手测清单 §1）。

**Acceptance Scenarios**:

1. **Given** task 型 workflow 画布，**When** 画布加载，**Then** 左侧固定位置渲染「开始」节点（不可删除、不可从面板拖拽、不参与连线校验），角标显示 input_schema 字段数；点击打开检查器 schema 面板（复用现有表单 + 新默认值列）
2. **Given** chat 型 workflow 画布，**When** 点击开始节点，**Then** 检查器显示只读说明「单一入参 {{input}}」，无编辑表单（现有行为不变）
3. **Given** 任意 workflow，**When** 保存，**Then** 开始节点不进 `nodes` 数组、不进序列化产物，后端契约零感知

### User Story 2 - end 节点输出映射：output_schema 结构化编辑 + 类型推导（Priority: P1）

用户配置 task 型 workflow 的结束节点：检查器以「输出映射」字段行编辑——每行 = 字段名 + 引用变量（变量下拉）+ 描述；字段类型由被引变量自动推导、只读展示。保存后工作流终稿是符合 output_schema 的 JSON 对象，试运行输出可直接被下游程序消费。

**Why this priority**: 任务型 workflow 的产出必须是结构化数据，当前 end 节点只有单一自由文本模板、output_schema 只是运行期校验，配置割裂且易错。与 US1 同为本篇核心。

**Independent Test**: 画一个「llm 节点（声明 output_schema）→ end」的 task 型 workflow，end 映射两行：`summary` 引用 `{{llm1.summary}}`（推导 string）、`score` 引用 `{{llm1.score}}`（推导 number）；试运行断言终稿为合法 JSON 对象、字段值类型正确、含引号等特殊字符的字符串不破坏 JSON（手测清单 §2）。

**Acceptance Scenarios**:

1. **Given** task 型 workflow 的 end 节点，**When** 打开检查器，**Then** 显示输出映射编辑器：字段行 {字段名, 引用变量, 描述}，引用变量走既有变量下拉（仅祖先节点 + input，整值限单一 `{{ref}}`）；类型列只读展示推导结果
2. **Given** 映射行引用 `{{input.x}}` / `{{llmKey.field}}` / `{{workflowKey.field}}`，**Then** 类型分别取自 input_schema / llm 节点 output_schema / 子工作流 output_schema 的声明类型；裸引用 `{{nodeKey}}`（tool / api / knowledge_retrieval / 无 schema 的 llm）推导为 string
3. **Given** 保存映射模式 end 节点，**Then** config 写入加法键 `output_fields`（name + ref），workflow 级 `output_schema` 同步写入（含推导类型）；后端按引用构造终稿 JSON 对象（字符串值正确转义），`validateOutputSchema` 兜底校验
4. **Given** 既有 end 节点带自由 `output` 模板，**When** 打开检查器，**Then** 默认进「高级模式」显示原模板原文，可手动切换映射模式；映射模式与高级模式互斥
5. **Given** chat 型 workflow 的 end 节点，**When** 打开检查器，**Then** 保持现有单一 output 模板编辑（chat 型无 output_schema，映射模式不出现）
6. **Given** task 型 workflow 含 condition 分流到两个各自启用映射的 end 节点，**When** 试运行分别触发两分支，**Then** 两次终稿分别为各自 end 的映射产出，均通过图级 output_schema（并集）校验——两 end 都产出的字段为 required、仅单侧产出的字段为 optional

### User Story 3 - 入参默认值：后端存储 + 引擎补全（Priority: P1）

用户在 input_schema 的 optional 字段上配默认值（按类型切换输入控件）；试运行表单预填默认值（可改）；子工作流传参与顶层执行时，optional 字段缺失由后端按 schema 补默认值——任何调用方行为一致，不依赖调用方拼参。

**Why this priority**: 默认值是数据契约不是调用方便利糖（2026-10-09 用户拍板）；靠前端预填等于每个调用方自己实现补全，漏一处就是隐性 bug。

**Independent Test**: task 型 workflow 声明 `{keyword: required, limit: optional number default 10}`，试运行只填 keyword 断言 `{{input.limit}}` 渲染为 10；作为子工作流被父图引用、父图 Inputs 未给 limit 时同样断言补 10（手测清单 §3）。

**Acceptance Scenarios**:

1. **Given** input_schema 编辑（创建第一步表单 / 画布开始节点面板），**When** 添加 optional 字段，**Then** 可填默认值（string→文本框 / number→数字输入 / boolean→开关）；required 字段禁配默认值（保存期校验拒）
2. **Given** 试运行对话框（task 型有 schema），**When** 表单渲染，**Then** 每行预填该字段默认值，用户可改可清空
3. **Given** 子工作流节点传参，**When** 父图 Inputs 对某 optional 字段未提供模板，**Then** 后端 `assembleInputJSON` 按子 schema 补默认值（无默认值才省略）
4. **Given** 顶层 Execute（task 型且声明 input_schema），**When** input 为合法 JSON 对象且缺某带默认值的 optional 字段，**Then** 后端归一化补默认后进变量池；input 非合法 JSON、含未声明字段或类型不符时照旧放行不新增拒绝（纯加法，模板引用期 strict 渲染自然报错）

### User Story 4 - 零回归与兼容（Priority: P2）

chat 型 workflow、既有自由模板 end 节点、无默认值的存量 schema、JSON 模式编辑——全部零回归；映射模式产出的 config 在 JSON 模式下可见可编辑（双向一致）。

**Why this priority**: 本篇触碰 workflow 编辑器核心链路与执行引擎入参路径，零回归是交付底线；后端改动全部为加法键 / 纯函数归一化，存量数据零迁移。

**Independent Test**: 打开一个 spec 017 之前创建的存量 task / chat workflow，断言编辑、试运行、发布、运行历史全部正常（手测清单 §4）。

**Acceptance Scenarios**:

1. **Given** 存量 workflow（无 default、end 带自由 output 模板、无 output_fields），**When** 编辑保存 + 试运行，**Then** 行为与升级前完全一致
2. **Given** 映射模式保存的 end 节点，**When** 切 JSON 模式，**Then** output_fields 与图级 output_schema 以原文可见可编辑；改 JSON 后切回画布，检查器按反解析规则正确呈现（可解析为映射形态的 output_fields 进映射模式，否则高级模式）

---

### Edge Cases

- 多 end 节点：各 end 各自映射、终稿 = 运行时实际到达 end 的产出；同名同型合并进并集 schema、同名不同型前端保存期拦截；隐式终止路径（无出边裸输出）不变——声明 required 字段时该路径运行期校验拒（仅部分 end 产出的字段推导为 optional，正常分流路径不受影响）
- 映射字段引用的变量值含双引号 / 换行：后端按引用构造时经 `json.Marshal` 转义，终稿恒合法 JSON——这是 D2 采用后端构造而非前端模板生成的根因（模板渲染无转义，`"{{ref}}"` 遇引号即碎）
- 裸引用值形似数字的字符串（如 `"123"`）：按 output_schema 声明类型处理值（声明 string 按字符串、声明 number/boolean 按原 JSON 文本解析）；字段未出现在 schema 时回退「可解析 JSON 则原样、否则按字符串」
- 顶层归一化遇 input 是合法 JSON 但非对象（如 `"12"` / `"[1,2]"`）：照旧放行不补（只有 JSON 对象才走补全路径）
- 试运行表单清空带默认值的 optional 字段：该行从提交的 JSON 中省略 → 顶层归一化补回默认值（「清空」语义 = 使用默认，仅对带 default 的行成立）；未配 default 的 optional 行空值照传 `""`（现状不变——省略会令 strict 渲染报变量未定义，属回归）
- chat 型 workflow：input 恒为纯文本消息，schema 强制 NULL，归一化 / 默认值 / 映射模式全部不生效
- 子工作流无 schema：键集必须恰为 `{input}` 的既有规则不变，默认值不适用

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 画布 MUST 固定渲染伪开始节点（复用 `START_NODE_ID = '__start__'` 哨兵）：task 型角标显示 input_schema 字段数、chat 型无角标；不可删除、不可拖拽移动、不可从面板创建、不参与连线、不进序列化 `nodes`；点击打开现有检查器 schema 面板（入参编辑）；左面板「入参/出参」按钮 MUST 移除（由节点取代）
- **FR-002**: entry 语义 MUST 不变：`start_node_key` 显式列、三入口指定（面板下拉 / 双击 / 检查器「设为起始」）、删除起始节点迁移逻辑、`hf-wf-node--start` 视觉标注——全部保持现状；伪开始节点不承担 entry 语义、不画到首节点的边
- **FR-003**: task 型 end 节点检查器 MUST 提供输出映射模式：字段行 {name, ref, description}，ref 走既有变量下拉且整值限单一 `{{ref}}`（禁止混排文本）；类型列只读展示推导结果；映射模式保存时 MUST 写入 EndConfig 加法键 `output_fields: [{name, ref}]` 并同步图级 `output_schema`（name + 推导 type + description）；既有自由 `output` 模板编辑 MUST 保留为「高级模式」，两模式互斥，初始模式按存量 config 反解析判定（有 `output_fields` → 映射模式；仅有 `output` → 高级模式；全新节点 → 映射模式）
- **FR-004**: task 型 workflow 的多个 end 节点可各自启用映射模式（2026-10-09 clarify 反转原「限一个」裁定，对齐 Dify 多输出终态：终稿 JSON 内容取决于运行时到达的 end）；图级 output_schema MUST 同步为全部映射 end 字段的并集——同名同型合并、同名不同型冲突前端保存期拦截（运行期 validateOutputSchema 兜底）；字段 required MUST 自动推导（全部映射 end 都产出 = required、部分产出 = optional），映射行不设手工 required 开关；后端 EndConfig 行级校验（name 非空不重名、ref 为单一 `{{var}}`）；chat 型 end 节点 MUST 保持现状（单一 output 模板，无映射模式）
- **FR-005**: output_schema 编辑入口 MUST 收敛：创建第一步表单与编辑页 drawer 的 output_schema 表单移除，end 节点映射模式为唯一入口；input_schema 编辑入口 = 创建第一步表单 + 画布开始节点面板（同一数据源，既有同步机制）；JSON 模式契约不变（output_schema 仍是图级字段，映射模式是它的结构化编辑器）
- **FR-006**: 后端 `SchemaField` MUST 加 `default` 字段（`json:"default,omitempty"`，值形态 string / number / boolean，与 field type 一致）；`ValidateSchemaFields` MUST 校验：default 类型与 field type 匹配、`required: true` 的字段禁配 default（不一致 / 违规保存期 400）；jsonb 列无 SQL 迁移
- **FR-007**: 后端 `assembleInputJSON`（子工作流传参）MUST 调整：optional 字段缺失时取该字段 default（有则补、无则维持现状省略）；required 校验、未声明字段拒、无 schema 回退 `{input}` 等既有规则不变
- **FR-008**: 后端顶层 Execute MUST 增加入参归一化（纯函数）：task 型且 `InputSchema` 非空时，input 可解析为 JSON 对象则对「缺失且带 default 的 optional 字段」补默认后重新序列化进变量池；input 非合法 JSON、非对象、含未声明字段、类型不符时 MUST 照旧放行（本篇只引入「补默认」一个新行为，严格校验留给 task 型虚拟工具 spec）；归一化 MUST 在 `newExecContext` 之前
- **FR-009**: 后端 EndConfig MUST 加 `output_fields` 加法键（参照 spec 011 / 014 加法先例，jsonb config 无迁移）：`buildOutput` 优先按 `output_fields` 构造——逐字段 `lookup(ref)` 取值，按图级 output_schema 声明类型处理（string 按 `json.Marshal` 字符串、number / boolean 取原 JSON 文本；字段不在 schema 时回退「可解析 JSON 则原样、否则按字符串」），`json.Marshal` 整对象产出终稿；无 `output_fields` 时回退既有 `output` 模板路径（含空 output 取末节点输出）；R10 保存期校验 MUST 覆盖 `output_fields` 的 ref 基名（∈ `{input}` ∪ 祖先 node_key）
- **FR-010**: 前端 `SchemaFieldsEditor` MUST 加默认值列：仅 optional 行可填，按 type 切换控件（文本框 / 数字输入 / 开关）；api 层 `SchemaField` 类型加 `default?: string | number | boolean`、EndConfig 类型加 `output_fields?: { name: string; ref: string }[]`
- **FR-011**: 试运行对话框（task 型有 schema）MUST 按字段默认值预填表单（可改可清空）；「清空 → 从提交对象省略」的语义 MUST 限定于「optional 且配置了 default」的行（省略后由顶层归一化补回默认值）；未配置 default 的 optional 行 MUST 维持现状——空值照常组装进提交 JSON（`{{input.x}}` 渲染空串，零回归）；chat 型与 task 型无 schema 的两态不变

### Key Entities *(include if feature involves data)*

- **SchemaField（后端契约，加法扩展）**: `{name, type, required, description, default?}`——default 仅 optional 字段可配，值类型 = field type；存 input_schema / output_schema 共用的 jsonb 列，无迁移
- **EndConfig（后端契约，加法扩展）**: `{output?, output_fields?: [{name, ref}]}`——output_fields 存在时优先生效；output 保留为高级模式 / 向后兼容路径
- **伪开始节点（纯前端形态）**: 复用 `START_NODE_ID` / `START_NODE_TYPE` 哨兵与 `openSchemaPanel` 面板，仅恢复画布渲染；不序列化、不进 nodes、无后端形态
- **映射模式产出物**: end 节点 `config.output_fields` + 图级 `output_schema`，同一 UpsertReq 一次提交；类型推导规则——`{{input.x}}` 取 input_schema、`{{llmKey.field}}` 取 llm output_schema、`{{workflowKey.field}}` 取子工作流 output_schema、裸 `{{nodeKey}}` 与无 schema 来源一律 string；多 end 时图级 output_schema = 全部映射 end 字段并集（同名同型合并），required 自动推导：全部映射 end 都产出 = required、部分产出 = optional
- **顶层入参归一化（后端纯函数）**: `normalizeInputJSON(input string, schema []SchemaField) string`——只有合法 JSON 对象走补全，其余原样返回

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 开始节点可视化生效——task 型画布默认渲染开始节点、角标入参数正确、点击编辑入参、序列化零泄漏；chat 型只读说明（手测 §1 通过）
- **SC-002**: 输出映射生效——映射两字段（string + number）的 task 型 workflow 试运行终稿为合法 JSON 对象，类型正确、特殊字符不破坏 JSON；既有自由模板 end 节点行为不变（手测 §2 通过）
- **SC-003**: 默认值三处生效——试运行预填、子工作流传参补默认、顶层执行补默认行为一致；required 禁配默认被保存期拦截（手测 §3 通过）
- **SC-004**: 零回归——存量 workflow（无 default / 自由模板 end / chat 型）编辑、试运行、发布、运行历史全部不变；JSON 模式与画布映射模式双向一致（手测 §4 通过）
- **SC-005**: 自动化门禁全绿——后端 `go build ./... && go vet ./... && go test ./internal/workflow/...` 通过（新增纯函数单测覆盖：ValidateSchemaFields default 校验、assembleInputJSON 补默认、normalizeInputJSON 边界、buildOutput output_fields 构造与转义、R10 覆盖 output_fields）；前端 `npm run type-check && npm run build` 通过
- **SC-006**: 改动面收敛——后端仅 workflow 模块四处加法（SchemaField / EndConfig / execute.go 归一化与构造 / R10 扩展），零 SQL 迁移、零破坏性契约变更；前端零新依赖、零新增设计 token（tokens.css 不动）

## Assumptions

- 前置：spec 016（`5af6727`）已合入——七类节点 / 试运行 / 运行历史链路为基线；graph.ts 哨兵管道（`START_NODE_ID` / `isStartContextNode` / `openSchemaPanel`）仍在，本篇只恢复画布渲染
- D1 反转 spec 012 裁定已经用户 2026-10-09 显式批准（本篇 Input 节记录）；D2 采用后端 `output_fields` 加法键而非「前端生成 output 模板」——起草期发现模板渲染无 JSON 转义、字符串值含引号即碎，纯前端方案存在正确性缺陷，此为对最初推荐方案的唯一实质偏离，2026-10-09 用户定稿时确认
- 变量引用体系（`{{var}}` 语法、一级下钻、祖先限定、R10 保存期校验）不变；本篇不新增顶层变量名，`input` 仍是唯一入参基名
- 明确不做：start / end 真后端节点类型（`start_node_key` 显式列设计不变）；顶层入参严格校验（非 JSON / 未声明字段拒不拒——留给 task 型虚拟工具 spec）；end 节点之外的多终稿出口编排；chat 型结构化入参；output_schema 的 nested 类型（保持 string / number / boolean 三型）
- 设计 token 只增不改；本篇零新增，仅复用现有节点视觉规范（`hf-wf-node` 家族）
- 手测清单四条（§1 开始节点 / §2 输出映射 / §3 默认值 / §4 零回归）由用户执行，落位 docs/testing/ 对应文档；自动化门禁不覆盖真实 LLM 调用侧行为
