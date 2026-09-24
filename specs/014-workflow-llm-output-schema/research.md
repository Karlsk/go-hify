# Research: LLM 节点输出字段声明与变量下拉展开

**Feature**: [spec.md](./spec.md) | **Date**: 2026-09-23 | **Phase 0**

事实核验于工作区（分支 `014-workflow-llm-output-schema`，基线 5fa8778）；引用行号以该基线为准。

## D1. 注入点与记录时序（FR-006 / FR-007）

**Decision**: `buildJSONDirective(fields []api.SchemaField) string` 纯函数在 callLLM 内、strict 渲染成功后、`setNodeIn` **之前**调用；追加到渲染后的 user 消息末尾，再以追加后的完整文本进 `nodeIn["prompt"]` 与消息组装。

**Rationale**: 现状 executor.go:119-169 顺序为「system 渲染 → prompt 渲染 → nodeIn 构造 → setNodeIn → ResolveLLMConfig → client.Generate」。注入放在 nodeIn 构造前，node_in 与 executions 记录的就是实际发送文本（排障所见即所发，FR-006/007 拍板语义）；注入不碰 system_prompt、不做模板渲染、不含用户变量（纯系统固定文案）。

**Alternatives**: ①渲染前改写 prompt 模板（污染作者文本、模板字符可能被转义，拒）；②注入进 system 消息（拍板文本明写 user 消息末尾，拒）。

## D2. 注入文案格式冻结（FR-006 / SC-005）

**Decision**: 中文固定文案，格式冻结为（`\n` 换行）：

```
\n\n请只输出一个 JSON 对象（不要使用 markdown 代码块，不要包含 JSON 以外的任何文本），对象包含以下字段：\n
```

其后每个声明字段一行：`- {name}（{type}，{必填|可选}）`（type 原样 string/number/boolean；required=true → 必填，false → 可选）。**description 不进指令**（spec FR-006 只列名称/类型/必填性）。测试以 `strings.HasSuffix(prompt, 期望指令全文)` + 逐字段行包含断言。

**Rationale**: 文案由 buildJSONDirective 确定性生成，字段集相同输出逐字节相同，可直接做断言锚点；指令要求纯 JSON 与校验「不剥围栏」形成闭环（edge case 1）。

**Alternatives**: 含 description（超出拍板文案范围，拒）；英文文案（全仓面向中文用户，拒）。

## D3. validateLLMOutput 形态（FR-005）

**Decision**: 新纯函数 `validateLLMOutput(output string, fields []api.SchemaField) error`，与 execute.go:477 `validateOutputSchema` 语义逐字对齐但输入为结构化字段（后者收 workflow 级 schema 文本）：`len(fields)==0` 零校验 → `json.Unmarshal` 到 `map[string]json.RawMessage`（err / nil 拒：非 JSON、null、数组、纯文本均在此拦）→ required 缺失拒 → `probeSchemaType`（execute.go 既有私有函数，同包复用）类型探针不符拒 → 多余字段宽容。错误文案含字段名（「缺少必填字段 X」「字段 X 类型应为 Y」），callLLM 包装 `errs.ErrValidationFailed` 并带节点 key 前缀——与既有节点失败链一致，零新增哨兵。

**Rationale**: 不直接调用 validateOutputSchema：其签名收 `*string` schema 文本（从 DB 列来），llm 侧声明已是结构化 []SchemaField，绕道序列化成文本再解析是多余往返。语义对齐由测试四分支（合规/缺必填/类型不符/非 JSON）+ 零变化分支守护。

**Alternatives**: 把声明序列化成 schema 文本喂 validateOutputSchema（多余往返，拒）；放宽为数组也收（spec 明写拒，拒）。

## D4. 前端声明区读写（FR-003）

**Decision**: NodeInspector llm 表单在 Prompt 区之后加「输出字段」区，复用 SchemaFieldsEditor（NodeInspector.vue:29-37 伪节点面板同组件同 props 形态：modelValue + `update:model-value` patch 模式）。读写键 `config.output_schema`，对齐 omitempty 语义：空数组/全空行 → 删键；readonly 态不渲染（FR-003）。

**Rationale**: 组件零新 props、零改 SchemaFieldsEditor 本体；config 键读写与 `useOptStrConfigField`（L446 同款「空=删键」纪律）一致。数组字段不复用 str helper，Inspector 内以局部 computed + emit patch 直写。

**Alternatives**: 给 SchemaFieldsEditor 加 llm 模式 prop（组件无差别需求，拒）；独立新组件（违反 Simplicity，拒）。

## D5. 变量下拉展开插入点（FR-004 / SC-001）

**Decision**: NodeInspector 变量源计算（L514-575：ancestorNodes 经 ancestorsOf 反向 BFS）处的 upstream 分组内：祖先 llm 节点 `config.output_schema` 非空 → 在该节点既有整体引用条目 `{{key}}` 之后追加字段条目，每字段一条：`label: key.field（type·必填|可选）`、`insert: {{key.field}}`、group 不变 `'upstream'`；整体引用条目保留。未声明 llm 节点条目形态零变化。api/workflow.ts llm config 类型补 `output_schema?: SchemaField[]`（键名对齐后端契约）。

**Rationale**: 展开只加条目不动分组结构，TemplateField/VariableGroup 类型零改动（TemplateField.vue:40-56）；条目标注满足 spec「标注类型与必填」。

**Alternatives**: 单独「字段」分组（破坏三分组既有心智，拒）；展开替代整体条目（spec 明写保留，拒）。

## D6. NodeInspector.vue NUL 字节缺陷（范围外，待裁定）

**Decision**: 不在本篇顺手修，挂起至 tasks 软停点呈报用户。事实：L856 模板字符串 `${props.node.id}<NUL>...` 含原始 0x00 字节（HEAD 5fa8778 与工作区均有），运行时无害（JS 合法分隔符）但 `file` 判二进制、grep 需 `-a`。候选修法：原始字节替换为 `\x00` 转义（运行时逐字节等价、文件回归纯文本），单行独立小修或作 T000 前置。

**Rationale**: 范围红线「顺手改进记下来问用户」；本篇将大改该文件，修在改前可让后续 diff 干净可 grep。

**Alternatives**: 本篇内静默修复（违反红线，拒）；永久不修（污染工具链，不建议）。

## D7. 测试形态与落位（SC-001~005）

**Decision**: 后端 TDD：`api/schema_test.go` 扩——OutputSchema 序列化往返三态（带值/空数组/缺省零键）+ LLMConfig.Validate 挂 ValidateSchemaFields 的拒绝路径（name 空/重名/type 越枚举）与存量零拒绝；`service/executor_test.go` 扩——注入位置与文案（HasSuffix + 字段行）、校验四分支、未声明节点消息序列与 node_in 形态逐字节断言、嵌套子图内生效（子图独立游走）、记录实发文本（stub client 收到的消息 == node_in 记录）。前端无单测基建：`type-check && build` 双门禁 + manual-test 增补小节人工验收。

**Rationale**: 同包 *_test.go + stub llm client / stub store 既有模式；「逐字节零变化」用 golden 对照（改造前先落 fixture）保证。
