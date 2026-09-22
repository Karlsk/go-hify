# Data Model: 工作流管理前端（009-workflow-frontend）

**Date**: 2026-09-22 | **Status**: Complete | **Plan**: [plan.md](plan.md)

前端无自有存储；本文定义**消费的 API 数据形态**与**页面私有数据模型**（图配置模型 + 画布模型）。API 字段契约以后端冻结契约为准（[contracts/workflow-frontend-api.md](contracts/workflow-frontend-api.md)），此处不重定义。

## 1. API 数据形态（api/workflow.ts 类型）

### WorkflowItem（列表行，GET /workflows 响应 data[]）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | `string` | bigint 字符串化（JSON 惯例，`"123"`） |
| name | `string` | |
| description | `string` \| `null` | 后端可空；展示兜底 `''` |
| type | `'chat'` \| `'task'` | spec 08 起一等维度 |
| status | `'draft'` \| `'published'` \| `'disabled'` | 三态小写（[internal/workflow/api/schema.go](../../../internal/workflow/api/schema.go)） |
| created_at | `string` | RFC 3339 UTC |

### WorkflowStatus / WorkflowType

联合类型常量：`WORKFLOW_STATUSES`、`WORKFLOW_TYPES` + 中文映射（draft→草稿 / published→已发布 / disabled→已停用；chat→对话型 / task→任务型）。

### CreateWorkflowData（POST /workflows 请求体）

```ts
interface CreateWorkflowData {
  name: string
  description?: string
  type: WorkflowType
  start_node_key: string
  nodes: WorkflowNodeData[]
  edges: WorkflowEdgeData[]
  input_schema?: SchemaField[]    // 仅 task 型；chat 型不带（强不变量）
  output_schema?: SchemaField[]
}
```

**外键字符串保形**（FR-011，2026-09-22 实现期修正）：`nodes[].config.model_id` / `nodes[].config.workflow_id` 在请求体保持**字符串**（后端 NodeConfig 带 `,string` tag，数值形 400——[internal/workflow/api/schema.go](../../../internal/workflow/api/schema.go) 与两份手测文档一致）；JSON 编辑器与画布下拉本就持字符串，组装零转换。

### SchemaField（task 型 input/output schema 元素）

`{ name: string; type: 'string' | 'number' | 'boolean'; required: boolean; description?: string }`

## 2. 页面私有模型：图配置（graph.ts，双模式共享数据源）

```ts
interface GraphConfig {
  start_node_key: string            // 必有节点 key；画布清空后置 ''
  nodes: GraphNode[]
  edges: GraphEdge[]
  input_schema?: SchemaField[]      // 仅 task 型（由表单 type 决定是否携带）
  output_schema?: SchemaField[]
}

interface GraphNode {
  key: string                       // 前端生成，画布内唯一
  type: NodeType                    // 'llm' | 'end' | 'condition' | 'api' | 'workflow'
  name?: string                     // 展示名，缺省显示类型中文名
  config: Record<string, unknown>   // 整体持有；未知键往返透传（SC-006）
}

interface GraphEdge {
  source_node_key: string
  target_node_key: string
  condition?: string                // 出边标签，condition 节点常用；其余类型可选
}
```

与 `CreateWorkflowData` 的关系：图配置不含 type/name/description（表单持有，Clarifications 拍板单一入口）；`input_schema`/`output_schema` 同样由表单区独立编辑持有（FR-006），**不进 JSON 编辑器文本**——JSON 编辑器文本只序列化 start_node_key/nodes/edges，GraphConfig 含 schema 字段是提交组装的聚合形态（analyze F1 定读）；提交时 `{...表单字段, ...图配置}` 合并组装。

### 节点类型与 config 已知字段

| type | 中文名 | config 已知字段（画布表单编辑） | 未知键 |
|---|---|---|---|
| llm | LLM 节点 | `model_id`（string，模型下拉）、`prompt`（string） | 透传 |
| end | 结束节点 | `output`（string） | 透传 |
| condition | 条件节点 | `expression`（string） | 透传 |
| api | API 节点 | `url`（string）、`method`（string，下拉 GET/POST/PUT/DELETE） | 透传 |
| workflow | 子工作流节点 | `workflow_id`（string，task 型下拉）、`inputs`（`Record<string, string>` 模板映射） | 透传 |

规则：

- **key 生成**：`${type}_${n}`（n 为该类型递增计数器），画布会话内唯一；JSON 模式手写的 key 原样保留（不重写）。
- **起始节点**：默认第一个放入的节点；删除起始节点时迁移到剩余 nodes 首个（spec Edge Cases）；全部删除后 `start_node_key = ''`，提交前校验非空。
- **空值**：config 已知字段未填时省略键（不给后端发空字符串占位）；列表空 `[]`、字符串空 `""` 遵循空值约定。

## 3. 画布模型（Vue Flow 形态，CanvasEditor 内部）

```ts
// Vue Flow Node（v-model:nodes 元素）
{
  id: GraphNode['key'],             // 画布节点 id = 配置节点 key（往返锚点）
  position: { x: number; y: number },
  data: { nodeType: NodeType; name?: string; config: Record<string, unknown> },
  class: `hf-wf-node-${type}` + start 标识
}
// Vue Flow Edge
{
  id: `${source}->${target}`,
  source, target,                   // = key
  label: condition                  // 可选
}
```

映射规则：

- **配置 → 画布**：key→id 直传；config 整体进 data（引用共享，画布表单改 config 即改图配置单源）；位置优先取位置 Map（research #6），无则自动网格布局（`x = col*260, y = row*120`，按 nodes 序）。
- **画布 → 配置**：id/key 回填，`data.config` 即单源引用（无需拷贝回写）；edges 的 label ↔ `condition`。
- **位置 Map**：`Map<key, {x, y}>` 组件内存；节点拖动更新；删除节点时同步清理；不序列化进配置。

## 4. 状态机（列表页生命周期操作，FR-014）

```text
draft ──publish──▶ published ──disable──▶ disabled
  ▲                                            │
  └────────────── publish ─────────────────────┘
```

- 发布入口：draft / disabled 行显示「发布」；停用入口：published 行显示「停用」（按行状态渲染操作按钮）。
- 后端幂等（重复调用同态无副作用）；成功后刷新表格。
- 操作列按钮矩阵：删除（恒显）+ 发布（非 published）+ 停用（published）。

## 5. 校验规则（前端侧，后端 400 兜底）

| 校验点 | 规则 | 失败行为 |
|---|---|---|
| 名称 | 必填、去空格后非空 | 表单红字，阻断提交 |
| JSON 合法性（图配置） | `JSON.parse` 成功 | 格式化：提示错误、原文不变；切拖拽：阻断；提交：阻断 |
| 图配置结构 | 顶层为对象；`nodes` 为数组（可为空）；`edges` 为数组；元素含必需键（key/type、source/target） | 同 JSON 非法三处阻断 |
| 空图 | 提交时 `nodes.length ≥ 1` 且 `start_node_key` 非空 | 阻断提交（后端图校验 400 兜底） |
| task 型 schema | JSON 数组、元素 `{name, type, required, description?}`、type 合法 | 复用 JSON 校验阻断 |
| chat 型 | 不展示 schema 编辑、提交不带 schema 字段 | 后端强不变量（带非空 schema 即 400），前端不给制造机会 |

## 6. 关系与依赖

- `api/workflow.ts`（类型 + 5 方法）← WorkflowList / WorkflowCreate / graph.ts（类型引用）。
- `api/provider.ts`（既有 getProviderList / getModelList）← CanvasEditor 模型下拉（AgentList 先例）。
- `api/workflow.ts` getWorkflowList ← 子工作流下拉（前端过滤 task 型）。
- 页面私有组件（JsonConfigEditor / CanvasEditor / NodeInspector）仅被 WorkflowCreate 消费，不进全局 components/。
