# Frontend Contract: LLM 节点输出字段声明与变量下拉展开

**Feature**: [spec.md](./spec.md) | **Date**: 2026-09-23 | **Phase 1**

本篇前端契约面 = NodeInspector 两处新增 + api/workflow.ts 类型补齐；TemplateField / SchemaFieldsEditor / CanvasEditor 组件 props/emits **零改动**。冻结条目对齐 spec FR-003/004、SC-001/004 与 [research.md](../research.md) D4/D5。

## 1. api/workflow.ts（类型补齐）

llm 节点 config 类型增 `output_schema?: SchemaField[]`（键名与后端 jsonb 契约逐字对齐；SchemaField 用既有前端形态）。无新端点、无请求/响应类型变化。

## 2. NodeInspector.vue —— llm 表单「输出字段」区（FR-003）

- **位置**：llm 表单 Prompt 区之后；**readonly 态不渲染**。
- **组件**：复用 SchemaFieldsEditor（与伪节点 schema 面板同组件同 props 形态：`modelValue` + `@update:model-value` patch）。
- **读写键**：`config.output_schema`；omitempty 对齐——空数组 / 全空行 → 删键（与 `useOptStrConfigField`「空=删键」同纪律），非空 → 写数组。
- 面板标题文案：「输出字段」（可选声明，未声明 = 现状行为）。

## 3. NodeInspector.vue —— 变量下拉展开（FR-004 / SC-001）

- **插入点**：变量源计算（ancestorNodes 经 ancestorsOf 反向 BFS 得祖先集）的 upstream 分组构造处。
- **规则**：祖先 llm 节点 `config.output_schema` 非空 → 在该节点既有整体条目 `{{key}}` **之后**追加字段条目；整体条目保留；未声明 llm 节点条目形态零变化；非 llm 祖先与 input / subflow-output 分组零变化。
- **条目形态**（data-model §2 冻结）：
  - `insert`: `{{key.field}}`（完整可插文本，经 TemplateField 既有 selectionStart 插入）
  - `label`: `{key}.{name}（{type}·{必填|可选}）`
  - `group`: `'upstream'`（不新增分组值）

## 4. 不变量（SC-004，红线）

既有变量下拉三分组结构、既有表单与校验行为零回归；新增表单区全用既有设计 token 与既有组件（token 冻结合规——不新增不修改 token 值）；序列化载荷 config 键集 ⊆ 后端契约键集（本篇新增读写仅 `output_schema`）。
