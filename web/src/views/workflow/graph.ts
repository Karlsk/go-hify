/**
 * 工作流图配置的纯逻辑收敛（无 Vue 依赖，本篇全部业务规则的家）：
 * 类型与节点类型常量、预填示例、key 生成、起始节点迁移、
 * JSON 文本 ↔ GraphConfig 解析/序列化（结构校验）、提交组装；
 * spec 010 增量：详情转换（detailToGraphConfig）、Schema 行表单校验
 * （schemaFieldsError）、更新组装（buildUpdatePayload，PUT 不带 type）。
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
  UpdateWorkflowData,
  WorkflowDetail,
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

/** 预填示例深拷贝（创建第二步初始图）：serialize→parse 往返产生全新对象树——
 *  画布 / 检查器编辑的是 config 引用，直传模块级常量会被污染（research #7） */
export function prefillGraphCopy(): GraphConfig {
  const r = parseGraphConfig(serializeGraphConfig(PREFILL_GRAPH))
  if (!r.ok) throw new Error(`PREFILL_GRAPH 非法：${r.error}`) // 模块内常量恒合法，防御性兜底
  return r.config
}

// ---- 节点 key 生成：`${type}_${n}`，n 从 1 递增至画布内唯一 ----

export function generateNodeKey(type: string, existingKeys: readonly string[]): string {
  const keys = new Set(existingKeys)
  for (let n = 1; ; n++) {
    const key = `${type}_${n}`
    if (!keys.has(key)) return key
  }
}

// ---- 起始节点迁移（spec Edge Cases：删起始 → 剩余首个无入边者；清空 → ''） ----

/** 纯函数：仅起始被删时迁移（start 未删原样返回；剩余空集 → ''）——新起始 = 剩余中
 *  首个无入边节点（图入口）；全有入边（成环）回退剩余首个；入参 = 删除后幸存 key 序列与现存边 */
export function migrateStartKey(
  startKey: string,
  removedKey: string,
  remainingKeys: readonly string[],
  remainingEdges: readonly CanvasEdgeInput[],
): string {
  if (startKey !== removedKey) return startKey
  const survivors = new Set(remainingKeys)
  remainingEdges.forEach((e) => survivors.delete(e.target)) // 有入边者排除（悬挂边 target 不在幸存集，无影响）
  return remainingKeys.find((k) => survivors.has(k)) ?? remainingKeys[0] ?? ''
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
    if (a.inputSchema?.length) data.input_schema = a.inputSchema
    if (a.outputSchema?.length) data.output_schema = a.outputSchema
  }
  return data
}

// ---- 详情 → 图配置（spec 010：GET 回填唯一入口；data-model §2/§4） ----

/**
 * WorkflowDetail → GraphConfig：name 空串省略键（不变量 4：与后端零值 "" 序列化
 * 等价）、condition null 省略键（不变量 2）、config 整对象引用直传（不变量 1：
 * 未知键透传的根基）；schema 不进 GraphConfig（表单 / 详情表单独持有）。
 */
export function detailToGraphConfig(d: WorkflowDetail): GraphConfig {
  return {
    start_node_key: d.start_node_key,
    nodes: d.nodes.map((n) => {
      const node: GraphNode = { key: n.key, type: n.type, config: n.config }
      if (n.name) node.name = n.name
      return node
    }),
    edges: d.edges.map((e) => {
      const edge: GraphEdge = {
        source_node_key: e.source_node_key,
        target_node_key: e.target_node_key,
      }
      if (e.condition) edge.condition = e.condition
      return edge
    }),
  }
}

// ---- Schema 行表单校验（spec 010：对齐后端 ValidateSchemaFields 三规则，SC-004） ----

const SCHEMA_FIELD_TYPES: readonly string[] = ['string', 'number', 'boolean']

/** 行表单校验：name trim 非空、不重名、type 限三值；错误文案带行号（1 起）。
 *  type 运行时可能越界（非法历史数据回填——TS 类型不设防，axios 不校验）。 */
export function schemaFieldsError(fields: SchemaField[]): string | null {
  const seen = new Set<string>()
  for (let i = 0; i < fields.length; i++) {
    const f = fields[i]
    const name = f.name.trim()
    if (!name) return `第 ${i + 1} 行字段：name 不能为空`
    if (seen.has(name)) return `第 ${i + 1} 行字段：name 重复（${name}）`
    seen.add(name)
    if (!SCHEMA_FIELD_TYPES.includes(f.type)) {
      return `第 ${i + 1} 行字段：type 必须是 string/number/boolean（当前值：${String(f.type)}）`
    }
  }
  return null
}

// ---- 编辑提交组装（spec 010：PUT 整图替换；data-model 不变量 5） ----

export interface UpdateAssembly {
  name: string
  description: string
  /** 仅作 task/chat 分支判定，不组装进 payload（后端 Type *string 携带即拒，同值也拒） */
  type: WorkflowType
  graph: GraphConfig
  /** 仅 task 型传入 */
  inputSchema?: SchemaField[]
  outputSchema?: SchemaField[]
}

/** PUT 组装：请求体不出现 type 键（组装层闸，与 UpdateWorkflowData 类型层双闸）；
 *  task 型带 schema（空数组 = 清空）、chat 型不带；config 引用直传（外键字符串
 *  保形零转换）。description 恒携带（可清空——区别于创建侧的空省略）。 */
export function buildUpdatePayload(a: UpdateAssembly): UpdateWorkflowData {
  const data: UpdateWorkflowData = {
    name: a.name.trim(),
    description: a.description.trim(),
    start_node_key: a.graph.start_node_key,
    nodes: a.graph.nodes,
    edges: a.graph.edges,
  }
  if (a.type === 'task') {
    data.input_schema = a.inputSchema ?? []
    data.output_schema = a.outputSchema ?? []
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

/** 检查器选中连线的输入面（id 供删除时清选中；label 即 condition 编辑面；
 *  source/target 供连线信息展示（spec 012 FR-001 删除入口）——FlowEdge 结构满足） */
export interface InspectorEdge {
  id?: string
  source?: string
  target?: string
  label?: unknown
}

// ---- 伪「开始」节点标识（FR-002，data-model §1）：仅渲染层与点击判定用 ----

/** 伪「开始」节点 id 常量：永不进 nodes 数组 / 不参与 onNodesChange / 不可序列化 */
export const START_NODE_ID = '__start__'

/** 伪节点 data.nodeType 占位（后端节点类型全集无 start，与真实节点类型天然互斥） */
export const START_NODE_TYPE = 'start'

/**
 * 伪节点上下文判定（id + 占位 type 双条件）：真实节点即使 key 被改成 `__start__`
 * （type ≠ start）也不会被误判成伪节点、误切检查器面板模式。
 */
export function isStartContextNode(node: InspectorNode | null | undefined): boolean {
  return node?.id === START_NODE_ID && node?.data?.nodeType === START_NODE_TYPE
}

// ---- spec 012：key 改名级联 / 祖先计算（纯函数；getGraph・serialize 零触碰） ----

/** renameNodeKey 输入的连线面（CanvasEdgeInput + 可选 id——改名后按 source/target 重生成） */
export type RenameEdge = CanvasEdgeInput & { id?: string }

/** renameNodeKey 的画布状态面（五级联点聚合，contracts §5；泛型保形具体节点/边类型） */
export interface CanvasRenameState<N extends CanvasNodeInput, E extends RenameEdge> {
  nodes: N[]
  edges: E[]
  startKey: string
  positions: NodePositions
  selectedKey: string | null
}

/**
 * key 改名结构性级联（FR-003）：不可变更新，一次返回新画布状态——
 * nodes 条目替换（key=id，config 等其余字段引用保留）、edges 端点与 id 替换、
 * startKey 相等替换、positions 键迁移（坐标保留）、selected 指向新 key。
 * 不改写模板文本 `{{old_key}}`（spec Assumptions：R10 保存期后端兜底）；
 * 幂等（newKey==oldKey 原样返回）。校验（非空/≤64/不冲突）在 Inspector 编辑点拦截。
 */
export function renameNodeKey<N extends CanvasNodeInput, E extends RenameEdge>(
  state: CanvasRenameState<N, E>,
  oldKey: string,
  newKey: string,
): CanvasRenameState<N, E> {
  if (oldKey === newKey) return state
  const nodes = state.nodes.map((n) => (n.id === oldKey ? { ...n, id: newKey } : n))
  const edges = state.edges.map((e) => {
    const source = e.source === oldKey ? newKey : e.source
    const target = e.target === oldKey ? newKey : e.target
    if (source === e.source && target === e.target) return e
    return { ...e, source, target, id: `${source}->${target}` }
  })
  const positions = new Map<string, XYPosition>()
  state.positions.forEach((pos, key) => positions.set(key === oldKey ? newKey : key, pos))
  return {
    nodes,
    edges,
    startKey: state.startKey === oldKey ? newKey : state.startKey,
    positions,
    selectedKey: state.selectedKey === oldKey ? newKey : state.selectedKey,
  }
}

/**
 * 沿 edges 反向 BFS 求祖先节点 key 集合（不含自身；FR-007 变量源——非祖先引用
 * 必被后端 R10 拒，提前收窄）。环安全（已访问即跳过）；指向未知节点的边忽略。
 */
export function ancestorsOf(
  nodes: readonly CanvasNodeInput[],
  edges: readonly CanvasEdgeInput[],
  nodeKey: string,
): Set<string> {
  const known = new Set(nodes.map((n) => n.id))
  if (!known.has(nodeKey)) return new Set()
  const incoming = new Map<string, string[]>()
  for (const e of edges) {
    if (!known.has(e.source) || !known.has(e.target)) continue
    const list = incoming.get(e.target)
    if (list) list.push(e.source)
    else incoming.set(e.target, [e.source])
  }
  const result = new Set<string>()
  const queue = [nodeKey]
  while (queue.length > 0) {
    const cur = queue.shift()!
    for (const src of incoming.get(cur) ?? []) {
      if (src === nodeKey || result.has(src)) continue
      result.add(src)
      queue.push(src)
    }
  }
  return result
}

/** 检查器图上下文（变量源计算 / key 冲突校验的最小输入面；画布直传数组引用） */
export interface InspectorGraphContext {
  nodes: readonly CanvasNodeInput[]
  edges: readonly CanvasEdgeInput[]
}

// ---- spec 012：Authorization 预设编解码（FR-005，research 决策 6；纯函数） ----

/** Auth 预设三态：无 / Bearer Token / Basic 用户名密码（无独立 config 键，归 headers 管） */
export type AuthPreset = 'none' | 'bearer' | 'basic'

/** Authorization 现值的推导结果（预设 + 凭据草稿；非 Bearer/Basic 前缀 → none，值留 KV 行） */
export interface AuthDraft {
  preset: AuthPreset
  token: string
  user: string
  pass: string
}

/** Basic 凭据编码（UTF-8 安全：unicode → latin1 展开后 btoa，形态同 research 决策 6） */
export function encodeBasicAuth(user: string, pass: string): string {
  return btoa(unescape(encodeURIComponent(`${user}:${pass}`)))
}

/** Basic 凭据解码（encodeBasicAuth 逆变换；按首个冒号分隔账密；非法 base64 回空账密不抛错） */
export function decodeBasicAuth(b64: string): { user: string; pass: string } {
  try {
    const raw = decodeURIComponent(escape(atob(b64)))
    const i = raw.indexOf(':')
    return i < 0
      ? { user: raw, pass: '' }
      : { user: raw.slice(0, i), pass: raw.slice(i + 1) }
  } catch {
    return { user: '', pass: '' }
  }
}

/** Authorization 现值 → 预设与凭据草稿（scheme 大小写不敏感；裸 Bearer（空 token）也算 bearer） */
export function parseAuthorization(raw: string): AuthDraft {
  const v = raw.trim()
  const sp = v.indexOf(' ')
  const scheme = sp < 0 ? v : v.slice(0, sp)
  const rest = sp < 0 ? '' : v.slice(sp + 1).trim()
  if (/^bearer$/i.test(scheme)) {
    return { preset: 'bearer', token: rest, user: '', pass: '' }
  }
  if (/^basic$/i.test(scheme)) {
    const { user, pass } = decodeBasicAuth(rest)
    return { preset: 'basic', token: '', user, pass }
  }
  return { preset: 'none', token: '', user: '', pass: '' }
}

/** 预设 + 凭据草稿 → Authorization 行值（none 由调用方删行，此处回空串） */
export function formatAuthorization(draft: {
  preset: AuthPreset
  token?: string
  user?: string
  pass?: string
}): string {
  if (draft.preset === 'bearer') {
    const token = draft.token ?? ''
    return token ? `Bearer ${token}` : 'Bearer'
  }
  if (draft.preset === 'basic') {
    return `Basic ${encodeBasicAuth(draft.user ?? '', draft.pass ?? '')}`
  }
  return ''
}
