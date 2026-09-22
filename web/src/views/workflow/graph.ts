/**
 * 工作流图配置的纯逻辑收敛（无 Vue 依赖，本篇全部业务规则的家）：
 * 类型与节点类型常量、预填示例、key 生成、起始节点迁移、
 * JSON 文本 ↔ GraphConfig 解析/序列化（结构校验）、提交组装。
 *
 * 数据源边界（spec FR-007 / analyze F1 定读 b）：JSON 编辑器文本只序列化
 * start_node_key/nodes/edges；type/name/description 与 input_schema/output_schema
 * 由表单控件持有——GraphConfig 含 schema 字段是提交组装的聚合形态。
 *
 * 外键保形（FR-011，2026-09-22 修正）：节点 config 内的 model_id / workflow_id
 * 全链路保持字符串（后端 NodeConfig 带 `,string` tag，数值形 400）——零转换。
 *
 * 解析对未知键宽容：节点 config 整体透传（SC-006 未知键往返保留）；
 * 顶层未知键（如手写的 type/name）解析时丢弃——这些字段由表单持有，表单才是唯一入口。
 */
import type {
  CreateWorkflowData,
  SchemaField,
  WorkflowEdgeData,
  WorkflowNodeData,
  WorkflowType,
} from '@/api/workflow'

// ---- 类型 ----

/** 双模式共享的图配置模型（聚合形态：图主体 + 表单持有的 schema，提交组装用） */
export interface GraphConfig {
  /** 起始节点 key；画布清空后置 ''（提交前校验非空） */
  start_node_key: string
  nodes: GraphNode[]
  edges: GraphEdge[]
  /** 仅 task 型（由表单 type 决定是否携带）；JSON 编辑器文本不含此二字段 */
  input_schema?: SchemaField[]
  output_schema?: SchemaField[]
}

export type GraphNode = WorkflowNodeData
export type GraphEdge = WorkflowEdgeData

export type GraphParseResult =
  | { ok: true; config: GraphConfig }
  | { ok: false; error: string }

export type SchemaParseResult =
  | { ok: true; fields: SchemaField[] }
  | { ok: false; error: string }

// ---- 节点类型常量（画布面板五类；后端全集 7 类，其余手写 JSON 可携带、透传） ----

export const PALETTE_NODE_TYPES = [
  'llm',
  'end',
  'condition',
  'api',
  'workflow',
] as const

export type PaletteNodeType = (typeof PALETTE_NODE_TYPES)[number]

export const NODE_TYPE_NAMES: Record<PaletteNodeType, string> = {
  llm: 'LLM 节点',
  end: '结束节点',
  condition: '条件节点',
  api: 'API 节点',
  workflow: '子工作流节点',
}

/** 节点展示名：name 优先，缺省回退类型中文名（未知类型回退裸 type 字符串） */
export function nodeDisplayName(node: GraphNode): string {
  return (
    node.name ||
    NODE_TYPE_NAMES[node.type as PaletteNodeType] ||
    node.type
  )
}

// ---- 预填示例（workflow-manual-test.md §4 的图配置部分：llm→end 线性图，chat 型） ----

export const PREFILL_GRAPH: GraphConfig = {
  start_node_key: 'classify',
  nodes: [
    {
      key: 'classify',
      type: 'llm',
      name: '分类节点',
      config: {
        model_id: '1',
        prompt: '将消息分类为 ORDER_QUERY 或 OTHER: {{input}}',
      },
    },
    { key: 'end', type: 'end', name: '结束', config: {} },
  ],
  edges: [{ source_node_key: 'classify', target_node_key: 'end' }],
}

// ---- 节点 key 生成：`${type}_${n}`，n 从 1 递增至画布内唯一 ----

export function generateNodeKey(type: string, existingKeys: readonly string[]): string {
  const keys = new Set(existingKeys)
  for (let n = 1; ; n++) {
    const key = `${type}_${n}`
    if (!keys.has(key)) return key
  }
}

// ---- 起始节点迁移（spec Edge Cases：删起始 → 剩余首个；清空 → ''） ----

/** 纯函数：仅起始被删时迁移（start 未删原样返回；剩余空集 → ''）；入参 = 删除后幸存 key 序列 */
export function migrateStartKey(
  startKey: string,
  removedKey: string,
  remainingKeys: readonly string[],
): string {
  if (startKey !== removedKey) return startKey
  return remainingKeys[0] ?? ''
}

// ---- JSON 文本 ↔ GraphConfig ----

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function syntaxErrorMessage(e: unknown): string {
  return e instanceof SyntaxError ? e.message : String(e)
}

/** 解析图配置 JSON：语法 + 结构校验（顶层对象 / nodes・edges 数组 / 元素必需键） */
export function parseGraphConfig(text: string): GraphParseResult {
  if (!text.trim()) return { ok: false, error: 'JSON 内容为空' }
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch (e) {
    return { ok: false, error: syntaxErrorMessage(e) }
  }
  if (!isRecord(raw)) return { ok: false, error: '顶层必须是 JSON 对象' }
  if (typeof raw.start_node_key !== 'string') {
    return { ok: false, error: '缺少字符串字段 start_node_key' }
  }
  if (!Array.isArray(raw.nodes)) return { ok: false, error: 'nodes 必须是数组' }
  if (!Array.isArray(raw.edges)) return { ok: false, error: 'edges 必须是数组' }

  const nodes: GraphNode[] = []
  for (let i = 0; i < raw.nodes.length; i++) {
    const n = raw.nodes[i]
    if (!isRecord(n)) return { ok: false, error: `nodes[${i}] 必须是对象` }
    if (typeof n.key !== 'string' || !n.key) {
      return { ok: false, error: `nodes[${i}] 缺少非空字符串字段 key` }
    }
    if (typeof n.type !== 'string' || !n.type) {
      return { ok: false, error: `nodes[${i}] 缺少非空字符串字段 type` }
    }
    if (n.config !== undefined && !isRecord(n.config)) {
      return { ok: false, error: `nodes[${i}].config 必须是对象` }
    }
    const node: GraphNode = {
      key: n.key,
      type: n.type,
      config: (n.config as Record<string, unknown>) ?? {},
    }
    if (typeof n.name === 'string') node.name = n.name
    nodes.push(node)
  }

  const edges: GraphEdge[] = []
  for (let i = 0; i < raw.edges.length; i++) {
    const e = raw.edges[i]
    if (!isRecord(e)) return { ok: false, error: `edges[${i}] 必须是对象` }
    if (typeof e.source_node_key !== 'string' || !e.source_node_key) {
      return { ok: false, error: `edges[${i}] 缺少非空字符串字段 source_node_key` }
    }
    if (typeof e.target_node_key !== 'string' || !e.target_node_key) {
      return { ok: false, error: `edges[${i}] 缺少非空字符串字段 target_node_key` }
    }
    const edge: GraphEdge = {
      source_node_key: e.source_node_key,
      target_node_key: e.target_node_key,
    }
    if (typeof e.condition === 'string' && e.condition) edge.condition = e.condition
    edges.push(edge)
  }

  return {
    ok: true,
    config: { start_node_key: raw.start_node_key, nodes, edges },
  }
}

/** 序列化图配置（仅 start_node_key/nodes/edges；schema 与表单字段由组装合并） */
export function serializeGraphConfig(config: GraphConfig): string {
  return JSON.stringify(
    {
      start_node_key: config.start_node_key,
      nodes: config.nodes,
      edges: config.edges,
    },
    null,
    2,
  )
}

// ---- task 型 schema 解析（data-model §5：数组 + 元素形状 + type 合法；空文本 = 空） ----

export function parseSchemaFields(text: string): SchemaParseResult {
  if (!text.trim()) return { ok: true, fields: [] }
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch (e) {
    return { ok: false, error: syntaxErrorMessage(e) }
  }
  if (!Array.isArray(raw)) return { ok: false, error: 'schema 必须是 JSON 数组' }
  const fields: SchemaField[] = []
  for (let i = 0; i < raw.length; i++) {
    const f = raw[i]
    if (!isRecord(f)) return { ok: false, error: `schema[${i}] 必须是对象` }
    if (typeof f.name !== 'string' || !f.name) {
      return { ok: false, error: `schema[${i}] 缺少非空字符串字段 name` }
    }
    if (
      f.type !== 'string' &&
      f.type !== 'number' &&
      f.type !== 'boolean'
    ) {
      return { ok: false, error: `schema[${i}].type 必须是 string/number/boolean` }
    }
    if (typeof f.required !== 'boolean') {
      return { ok: false, error: `schema[${i}] 缺少布尔字段 required` }
    }
    const field: SchemaField = { name: f.name, type: f.type, required: f.required }
    if (typeof f.description === 'string' && f.description) {
      field.description = f.description
    }
    fields.push(field)
  }
  return { ok: true, fields }
}

// ---- 提交前校验（空图 / 起始节点；后端图校验 R3/R1 的前端预检） ----

export function graphSubmitError(config: GraphConfig): string | null {
  if (config.nodes.length === 0) return '至少需要一个节点（画布为空或 nodes 为空数组）'
  if (!config.start_node_key) return '未指定起始节点'
  if (!config.nodes.some((n) => n.key === config.start_node_key)) {
    return `起始节点 ${config.start_node_key} 不在节点集合内`
  }
  return null
}

// ---- 提交组装（FR-011：config 外键字符串保形零转换；chat 型不带 schema 键） ----

export interface CreateAssembly {
  name: string
  description: string
  type: WorkflowType
  graph: GraphConfig
  /** 仅 task 型传入；undefined / 空文本 = 不携带该键 */
  inputSchema?: SchemaField[]
  outputSchema?: SchemaField[]
}

export function buildCreatePayload(a: CreateAssembly): CreateWorkflowData {
  const data: CreateWorkflowData = {
    name: a.name.trim(),
    type: a.type,
    start_node_key: a.graph.start_node_key,
    nodes: a.graph.nodes,
    edges: a.graph.edges,
  }
  if (a.description.trim()) data.description = a.description.trim()
  if (a.type === 'task') {
    if (a.inputSchema) data.input_schema = a.inputSchema
    if (a.outputSchema) data.output_schema = a.outputSchema
  }
  return data
}

// ---- 画布模型与双向转换（Vue Flow 形态；data-model §3，research #6/#7）----

export interface XYPosition {
  x: number
  y: number
}

/**
 * 会话级位置 Map（key → 坐标）：节点拖动 / 面板拖入时写入，JSON↔画布往返保留；
 * 不序列化进配置（后端契约无位置字段）；节点删除时由画布同步清理。
 */
export type NodePositions = Map<string, XYPosition>

/** 画布节点 data：config 与 GraphNode.config 同引用——检查器改 config 即改图配置单源 */
export interface CanvasNodeData {
  nodeType: string
  name?: string
  config: Record<string, unknown>
}

/** 画布节点输出形状（结构兼容 @vue-flow/core 的 Node；graph.ts 不 import vue-flow，保持纯逻辑） */
export interface CanvasNode {
  /** = GraphNode.key（往返锚点） */
  id: string
  position: XYPosition
  /** 画布展示名（name 优先，缺省类型中文名） */
  label?: string
  data: CanvasNodeData
  /** hf-wf-node-{type}（起始另加标识），由 canvasNodeClass 生成 */
  class?: string
}

/** 画布边输出形状（结构兼容 @vue-flow/core 的 Edge） */
export interface CanvasEdge {
  /** `${source}->${target}` */
  id: string
  /** = key */
  source: string
  target: string
  /** = condition */
  label?: string
}

/** 画布 → 配置转换的输入面（结构化宽松：vue-flow 的 Node<CanvasNodeData> 天然满足） */
export interface CanvasNodeInput {
  id: string
  data?: CanvasNodeData
}

/** 画布 → 配置转换的输入面（label 未知型：vue-flow Edge.label 可为 VNode，函数内收窄 string） */
export interface CanvasEdgeInput {
  source: string
  target: string
  label?: unknown
}

/** 自动网格布局：按 nodes 序自左向右换行，步进 x=260 / y=120（data-model §3） */
const GRID_COLUMNS = 4
const GRID_STEP_X = 260
const GRID_STEP_Y = 120

export function gridPosition(index: number): XYPosition {
  return {
    x: (index % GRID_COLUMNS) * GRID_STEP_X,
    y: Math.floor(index / GRID_COLUMNS) * GRID_STEP_Y,
  }
}

/** 节点 class：类型着色 + 起始标识（起始切换时画布对受影响节点重算） */
export function canvasNodeClass(nodeType: string, isStart: boolean): string {
  const base = `hf-wf-node-${nodeType}`
  return isStart ? `${base} hf-wf-node--start` : base
}

export interface CanvasGraph {
  nodes: CanvasNode[]
  edges: CanvasEdge[]
}

/** 配置 → 画布：key↔id、config 引用共享、位置优先取 Map（无则网格布局）、label = 展示名 */
export function graphToCanvas(config: GraphConfig, positions: NodePositions): CanvasGraph {
  const nodes: CanvasNode[] = config.nodes.map((node, i) => ({
    id: node.key,
    position: positions.get(node.key) ?? gridPosition(i),
    label: nodeDisplayName(node),
    data: { nodeType: node.type, name: node.name, config: node.config },
    class: canvasNodeClass(node.type, node.key === config.start_node_key),
  }))
  const edges: CanvasEdge[] = config.edges.map((edge) => ({
    id: `${edge.source_node_key}->${edge.target_node_key}`,
    source: edge.source_node_key,
    target: edge.target_node_key,
    label: edge.condition,
  }))
  return { nodes, edges }
}

/** 画布 → 配置节点：id→key 回填、config 引用回填（单源无需拷贝）、name 未填省略键 */
export function canvasToGraphNodes(nodes: readonly CanvasNodeInput[]): GraphNode[] {
  return nodes.map((n) => {
    const d = n.data! // 画布节点 data 由 graphToCanvas / 面板拖入构造，恒存在
    const node: GraphNode = { key: n.id, type: d.nodeType, config: d.config }
    if (d.name) node.name = d.name
    return node
  })
}

/** 画布 → 配置边：label↔condition（非字符串/空 label 省略键） */
export function canvasToGraphEdges(edges: readonly CanvasEdgeInput[]): GraphEdge[] {
  return edges.map((e) => {
    const edge: GraphEdge = { source_node_key: e.source, target_node_key: e.target }
    if (typeof e.label === 'string' && e.label) edge.condition = e.label
    return edge
  })
}

/** 类型中文名（画布面板五类）；未知类型回退裸 type 字符串（检查器兜底文案用） */
export function nodeTypeLabel(type: string): string {
  return NODE_TYPE_NAMES[type as PaletteNodeType] || type
}

/** 检查器选中节点的输入面（GraphNode 结构子集：检查器只读写 id + data） */
export interface InspectorNode {
  id: string
  data?: CanvasNodeData
}

/** 检查器选中连线的输入面（id 供删除时清选中；label 即 condition 编辑面） */
export interface InspectorEdge {
  id?: string
  label?: unknown
}
