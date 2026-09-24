# Data Model: LLM 节点输出字段声明与变量下拉展开

**Feature**: [spec.md](./spec.md) | **Date**: 2026-09-23 | **Phase 1**

## 1. 输出字段声明（llm 节点 config 内嵌，无新表无迁移）

存于 `workflows.config`（jsonb）llm 节点 `config` 对象内，键 `output_schema`，值为 `SchemaField[]`（复用 task 型形态，api 包既有导出）：

```go
// internal/workflow/api/schema.go —— LLMConfig 加法字段
OutputSchema []SchemaField `json:"output_schema,omitempty"`
```

```jsonc
// config 示例（持久化形态）
{
  "model_id": "3",
  "system_prompt": "你是分类器",
  "prompt": "分类：{{input}}",
  "output_schema": [
    { "name": "code",    "type": "string",  "required": true,  "description": "分类码" },
    { "name": "score",   "type": "number",  "required": false, "description": "" }
  ]
}
```

**验证规则**（FR-002，仅新键生效）：`ValidateSchemaFields` 既有语义——name 非空、节点内不重名、type ∈ {string, number, boolean}；description/required 无跨字段规则。

**三态**：缺省 / 空数组 → omitempty 序列化零键（存量往返不变）；非空 → 上示数组。声明不进任何运行记录新键（FR-007）。

## 2. 前端变量条目（VariableOption 扩展用法，类型零改动）

既有 `VariableOption {insert, label, group}`（TemplateField.vue:40-44）不加字段——llm 字段条目只是 upstream 分组的新增实例：

```ts
// 声明了 output_schema 的祖先 llm 节点（key=llm1）在 variableGroups upstream 分组产出：
{ insert: '{{llm1}}',         label: 'llm1（LLM）',           group: 'upstream' }   // 既有整体条目，保留
{ insert: '{{llm1.code}}',    label: 'llm1.code（string·必填）', group: 'upstream' } // 新增字段条目
{ insert: '{{llm1.score}}',   label: 'llm1.score（number·可选）', group: 'upstream' }
```

label 标注格式冻结：`{key}.{name}（{type}·{必填|可选}）`；insert 为完整可插文本（TemplateField 按 selectionStart 插入，既有行为）。

## 3. 实体关系与不变量

```text
workflows.config (jsonb)
└── nodes[].config.output_schema: SchemaField[]   ← 本篇唯一数据落点（加法键）
     ↓ 运行期
executor.callLLM 读取 cfg.OutputSchema
├── len>0 → buildJSONDirective(fields) → 追加至渲染后 user 消息 → 实发文本
└── len>0 → validateLLMOutput(reply, fields) → 不合规节点失败（VALIDATION_FAILED）
```

不变量：①未声明（len==0）节点消息序列 / node_in / executions / 序列化逐字节不变（SC-003）；②声明不产生任何独立实体、迁移、记录键（FR-001/007）；③前端 `web/src/api/workflow.ts` llm config 类型补 `output_schema?: SchemaField[]` 与后端键名逐字对齐。
