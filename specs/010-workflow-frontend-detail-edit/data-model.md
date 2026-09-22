# Data Model: 工作流详情 / 编辑前端（010-workflow-frontend-detail-edit）

前端数据模型与转换语义。后端契约零改动——本文只定义**前端消费形态**与三处转换；API 字段级契约见 [contracts/workflow-frontend-api.md](contracts/workflow-frontend-api.md)。

## 1. API 消费类型（api/workflow.ts 增量）

```ts
/** 详情响应的节点（后端 NodeSchema：config 原样透传——库里存的 JSON 原文） */
export interface WorkflowDetailNode {
  key: string
  type: string          // 后端 7 类全集字符串（画布面板只提供 5 类，未知类型照渲染）
  name: string          // 后端恒序列化；空串合法（前端转换时省略键，见 §4）
  config: Record<string, unknown>
}

/** 详情响应的连线（后端 EdgeSchema：condition null = 无条件直走） */
export interface WorkflowDetailEdge {
  source_node_key: string
  target_node_key: string
  condition: string | null
}

/** 详情响应（后端 WorkflowDetailSchema：摘要 + 图组装）；GET / PUT 均返回此形 */
export interface WorkflowDetail {
  id: string
  name: string
  description: string   // 后端 string 恒序列化（空串 ≠ null）
  type: WorkflowType
  status: WorkflowStatus
  input_schema: SchemaField[] | null   // null = 未声明（chat 型恒 null）
  output_schema: SchemaField[] | null
  created_at: string    // RFC 3339 UTC
  updated_at: string
  start_node_key: string
  nodes: WorkflowDetailNode[]          // 后端保证非 nil（空返 []）
  edges: WorkflowDetailEdge[]
}

/** 更新请求体（PUT /workflows/:id）——不含 type 键（后端携带即拒，同值也拒） */
export interface UpdateWorkflowData {
  name: string
  description: string
  start_node_key: string
  nodes: WorkflowNodeData[]
  edges: WorkflowEdgeData[]            // 无条件连线省略 condition 键（后端 nil 语义）
  input_schema?: SchemaField[]         // 仅 task 型携带
  output_schema?: SchemaField[]
}
```

既有类型零改动：`WorkflowItem` / `SchemaField` / `WorkflowNodeData` / `WorkflowEdgeData` / `CreateWorkflowData`（009 已定义，本篇直接复用）。

## 2. GraphConfig（既有，009 交付）

双模式共享的图配置模型不变（`start_node_key` / `nodes` / `edges`，聚合形态含可选 `input_schema` / `output_schema`）。本篇新增三个纯函数（graph.ts）：

| 函数 | 签名 | 语义 |
|---|---|---|
| `detailToGraphConfig` | `(d: WorkflowDetail) => GraphConfig` | 详情 → 图配置：`name === ''` 省略键、`condition === null` 省略键、`config` 引用直传（未知键透传的根基）；schema 不进 GraphConfig（表单/详情表单独持有） |
| `schemaFieldsError` | `(fields: SchemaField[]) => string \| null` | 行表单校验：name trim 非空、不重名、type ∈ string/number/boolean；错误文案带行号（对齐后端 ValidateSchemaFields 三规则） |
| `buildUpdatePayload` | `(form, graph, schemas) => UpdateWorkflowData` | 编辑提交组装：无 type；task 型带 schema 数组、chat 型不带；外键字符串保形零转换（config 引用直传） |

退役：`parseSchemaFields`（JSON 文本 → SchemaField[]）——SchemaFieldsEditor 行表单取代文本路径，删除（唯一调用方 WorkflowCreate 同步重构）。

## 3. 创建草稿 store（stores/workflowCreateDraft.ts，新）

```ts
interface CreateDraftState {
  name: string
  description: string
  type: WorkflowType
  inputSchema: SchemaField[]    // task 型编辑面（chat 型恒 []）
  outputSchema: SchemaField[]
  graph: GraphConfig | null     // null = 未进过第二步（下次进入用预填示例深拷贝）
}
```

- actions：`saveForm(partial)`（第一步跳转前写）、`saveGraph(g)`（第二步离开路由时回写）、`clear()`（POST 成功后清）。
- getter `hasForm`：`name !== ''`——第二步直访/刷新判定（刷新 = 内存清零 = 回第一步）。
- Pinia 内存态，无持久化（research #1）。

## 4. 转换链与保真不变量（SC-002 / SC-003 的机制保证）

```
【详情】GET → WorkflowDetail ──detailToGraphConfig──▶ GraphConfig ──GraphModeEditor──▶ 画布/JSON 双模式渲染
【编辑】GET → WorkflowDetail ──detailToGraphConfig──▶ GraphConfig（初始快照基准）
        编辑（表单/Schema 行/图编排）──buildUpdatePayload──▶ PUT（不含 type）
【创建】第一步表单 ──saveForm──▶ store ──第二步──▶ 初始 = store.graph ?? 深拷贝(PREFILL_GRAPH)
        第二步编辑 ──buildCreatePayload(既有)──▶ POST
```

不变量（每条都有实现锚点）：

1. **未知 config 键往返保留**：config 整对象引用从响应直通到 PUT 请求体（detailToGraphConfig 不拷贝、检查器只改已知键、buildUpdatePayload 引用直传）。
2. **连线条件标签往返保留**：`condition: null` → 键省略 → PUT 无 condition → 后端 nil；非空字符串双向保形。
3. **外键字符串保形**：`config.model_id` / `config.workflow_id` 全链路字符串（后端 `,string` tag），零转换——009 FR-011 语义延续。
4. **name 空串双向对齐**：后端恒序列化（`""`）、前端 GraphNode 可选键（省略 = 后端零值 `""`），序列化结果等价。
5. **type 双轨隔离**：POST 携带 type（创建必填）；PUT 请求体**不出现 type 键**（后端携带即拒）——两套 payload 组装函数分离，TypeScript 类型层面即不可混用。
6. **PREFILL 不污染**：预填示例经 serialize→parse 深拷贝进入第二步（画布 config 引用共享，直传常量会被编辑污染——research #7）。

## 5. 状态与守卫模型

| 状态 | 归属 | 语义 |
|---|---|---|
| `mode`（json / canvas） | GraphModeEditor 内部 | 双模式切换；切画布前非法 JSON 阻断（notify + 回退） |
| 节点位置 Map | 页面持有（props 传入 CanvasEditor） | 会话级、不序列化（009 语义延续）；编辑页 / 编排页各自持有一份 |
| 编辑页 dirty 快照 | WorkflowEdit 内部 | `JSON.stringify({ name, description, inputSchema, outputSchema, graphText })` 加载时基准 vs 当前 computed；双通道守卫（onBeforeRouteLeave + beforeunload）消费 |
| 编排页离开回写 | WorkflowOrchestrate 内部 | onBeforeRouteLeave 把 `getGraph()` 写回 store（JSON 非法跳过）；不弹确认 |
| 保存后跳转标记 | WorkflowEdit 内部 | `saved = true` 后路由守卫放行（成功跳详情不弹确认） |

## 6. 组件数据流（props / expose 面）

| 组件 | 输入 | 输出 | 只读态 |
|---|---|---|---|
| GraphModeEditor（新） | `initial: GraphConfig`（挂载一次性）、`defaultMode?`、`readonly?`、`fill?` | expose `getGraph(): GraphConfig \| null`（JSON 非法 notify + null） | `readonly`：模式切换保留、画布/文本均只读 |
| SchemaFieldsEditor（新） | `v-model: SchemaField[]`、`disabled?` | — | 详情页不用它（只读表直接渲染） |
| CanvasEditor（改造） | 既有 + `readonly?`、`fill?` | 既有 expose `getGraph()` | 藏面板/检查器、禁拖/禁连线/drop 与双击起始短路；平移缩放保留 |
| JsonConfigEditor（改造） | 既有 + `readonly?` | 既有 expose `validate()` | textarea disabled + 藏格式化按钮 |

页面组装关系：WorkflowDetail = 基础信息 + Schema 只读表 + GraphModeEditor(readonly, defaultMode canvas)；WorkflowEdit = 工具栏（表单内联 + Schema 抽屉）+ GraphModeEditor(fill)；WorkflowOrchestrate = 工具栏（只读展示 + 动作）+ GraphModeEditor(fill)。
