# Frontend Component Contracts: 工作流拖拽编辑器八项增强

**Feature**: [spec.md](./spec.md) | **Date**: 2026-09-22

本篇无后端接口变化（提交载荷契约不变见 spec SC-003）。本文冻结**组件间契约**——新增组件 props/emits、既有组件新增的对外面、以及 config 序列化键的前端读写面。实现必须逐字对齐本文与 [spec.md](../spec.md) FR-001~009。

---

## 1. TemplateField.vue（新组件，通用模板字段）

**定位**：哑组件——收渲染好的变量条目，无图依赖、无 api 依赖（research 决策 5）。

```ts
// Props
{
  modelValue: string            // 模板文本（含 {{}} 引用与手写混排）
  variables: VariableGroups     // 分组下拉数据源（data-model §2 形态）
  disabled?: boolean            // readonly 态禁用全部交互
  placeholder?: string
  rows?: number                 // textarea 行数，默认 3
}

// Emits
'update:modelValue': (value: string) => void
```

**行为契约**：
- 主体为 textarea（v-model = modelValue）；变量下拉为独立触发控件，选中条目按 textarea `selectionStart` 光标位置插入 `option.insert`（含 `{{}}` 包裹的完整文本），拼接后整体 emit。
- 手写不受限：下拉不格式化/不校验/不清洗既有文本。
- disabled 时下拉与 textarea 均禁用（SC-004）。

## 2. NodeInspector.vue（新增对外面）

```ts
// 新增 Emits（既有 props——node/graph/readonly 等——不变）
'delete-node': () => void                    // 删除节点按钮（FR-001），级联由 CanvasEditor 走既有 remove 链
'delete-edge': (edgeId: string) => void      // 选中连线时的删除按钮（FR-001）

'rename-node-key': (oldKey: string, newKey: string) => void
// Key 编辑提交（FR-003）：校验（非空/≤64/不冲突）在 Inspector 内拦截，
// 通过才 emit；级联应用在 CanvasEditor

// 新增 Props
edge?: InspectorEdge | null                  // 连线选中态（连线信息 + 删除入口）
startNodeKey?: string | null                 // 起始指向（isStartKey 提示 / 「设为起始」按钮禁用）
graphKind?: 'task' | 'chat'                  // schema 面板模式（task 两段行表单 / chat 只读说明）
inputSchema?: SchemaField[]                  // schema 面板同源（透传至面板）
outputSchema?: SchemaField[]
selfWorkflowId?: string | null               // 子工作流下拉排除自身（编辑态）
// 注：variables（VariableGroups）不是 props——变量条目在 Inspector 层内部计算
//（其拥有图上下文与 getWorkflowDetail 访问，research 决策 4），经 computed 喂给 TemplateField

// 新增 Emits（schema 面板同源，FR-002）
'update:inputSchema': (rows: SchemaField[]) => void
'update:outputSchema': (rows: SchemaField[]) => void

'set-start': (key: string) => void
// 「设为起始」按钮（entry 三入口之一）：画布 onSetStart 改 startKey 单源（readonly 时按钮不渲染）
```

**面板模式**（selectedNode === 伪节点上下文时）：task 型 = 两段 SchemaFieldsEditor（入参/出参）；chat 型 = 单一 input 说明（只读）。

**节点类型表单面（FR-004/005/006/008）**：

| 类型 | 字段与控件 | config 读写键 |
|---|---|---|
| llm | model_id 下拉（既有）；System Prompt（TemplateField，可选）；Prompt（TemplateField，必填提示） | `model_id` / `system_prompt`（空=不携带）/ `prompt` |
| api | Method 下拉、URL（TemplateField）；Headers KV 行（值域 TemplateField）；Auth 预设（无/Bearer/Basic → Authorization 行，research 决策 6 映射）；Body（TemplateField，方法=POST 时显示） | `method` / `url` / `headers` / `body` |
| workflow | 子工作流下拉（排除自身）；入参行：schema 字段名（只读标签）+ TemplateField 值域；无 schema 提示；加载 loading | `workflow_id` / `inputs` |
| end | Output（TemplateField） | `output` |
| condition | Expression（TemplateField） | `expression` |

## 3. CanvasEditor.vue（新增对外面）

```ts
// 新增 Props（伪节点与 schema 同源透传，FR-002）
inputSchema: SchemaField[]
outputSchema: SchemaField[]
graphKind: 'task' | 'chat'        // 伪节点面板模式

// 新增 Emits
'update:inputSchema': (rows: SchemaField[]) => void
'update:outputSchema': (rows: SchemaField[]) => void

// 既有对外面不变：v-model:nodes / v-model:edges / startKey 同步、readonly props
```

**内部行为契约**：
- entry 三入口（FR-002，2026-09-23 裁定后形态）：左面板「起始节点」下拉（选项 `key（类型名）`）/ 双击节点 / 检查器「设为起始」按钮，全部经画布 onSetStart 汇 startKey 单源；readonly 三入口均不可用，起始节点以主色强调边框 + 「起始」角标标识。
- 「入参 / 出参」面板入口：左面板按钮置伪节点上下文（sentinel，不进 nodes 数组）→ Inspector 切 schema 面板模式。
- 删除：接收 Inspector 的 delete-node → 构造与 Backspace 相同的 remove changes 走 `onNodesChange`（既有级联链零改动）；delete-edge 同理走 `onEdgesChange`。
- key 改名：接收 rename-node-key → 调 graph.ts `renameNodeKey` 应用返回的新状态。
- 选中连线：`onEdgeClick` 置 selectedEdge、`onPaneClick` 清空（节点选中互斥）。

## 4. graph.ts（纯逻辑层新增函数）

```ts
/** key 改名结构性级联——不可变更新，返回新画布状态（data-model §5） */
renameNodeKey(state: CanvasGraphState, oldKey: string, newKey: string): CanvasGraphState

/** 沿 edges 反向 BFS 求祖先节点 key 集合（不含自身；data-model §2 变量源） */
ancestorsOf(nodes: CanvasNode[], edges: CanvasEdge[], nodeKey: string): Set<string>
```

**不变量**：两函数均为纯函数（无副作用、无 DOM/网络）；`getGraph`/`serialize` 输出形态**不变**（伪节点结构上不可能泄入）。

## 5. GraphModeEditor.vue / 两宿主（接线层）

```ts
// GraphModeEditor 新增 Props（宿主 → 画布透传）
inputSchema: SchemaField[]; outputSchema: SchemaField[]
// 新增 Emits
'update:inputSchema' | 'update:outputSchema'
```

- `WorkflowEdit.vue`：inputSchema/outputSchema 既有页面 ref 即单源——抽屉表单与伪节点面板 props/emits 绑同一 ref；脏态守卫 snapshot 自动覆盖面板编辑。
- `WorkflowOrchestrate.vue`：两步式第一步 store 字段为单源，第二步画布同源接线；store 回写核对（提交载荷两宿主一致）。

## 6. api/workflow.ts（预期零改动）

`getWorkflowDetail` 返回的 `WorkflowDetail.input_schema / output_schema`（spec 008 交付）被决策 4/8 消费；详情缓存为 NodeInspector 模块级 `Map<workflowId, SchemaField[]>`（会话级，不进 store）。**无新增端点、无类型变更**；若实现中出现类型适配需要，只允许窄化本地类型别名，不改导出契约。

## 7. 序列化对齐（载荷契约，SC-003）

- config 键集 ⊆ api_contract.md §3 既有键；本篇新增读写的键：`system_prompt`（spec 011 已交付后端）、`headers`、`body`、`inputs`（既有契约键）。
- 图主体（start_node_key/nodes/edges）形态零变化：伪节点不入、key 改名仅结构性替换。
- headers 序列化：空键行不产出条目；重复键后写覆盖（map 语义）。
