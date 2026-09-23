<template>
  <!-- 拖拽编排画布（US3）：左侧五类节点面板（原生拖拽）+ Vue Flow 画布。
       状态模型（data-model §3）：挂载时从 config 初始化画布节点/边，此后画布即源，
       出口经 getGraph() 转回图配置——画布节点的 data.config 与配置节点共享引用，
       检查器（T014）改 config 即改配置单源；节点位置存 positions Map（父组件
       会话级持有，往返保留，不序列化进配置）。起始节点 = entry（start_node_key）：
       面板「起始节点」下拉直接指定（2026-09-23 裁定：取消画布伪「开始」节点；双击
       节点与检查器「设为起始」按钮为等价入口），首个拖入默认、删除迁移，
       hf-wf-node--start class 在画布上标识起始节点。
       只读态（readonly，spec 010）：面板 / 检查器不渲染、编辑交互全禁，
       平移缩放保留（FR-005）；fill = 高度铺满父容器（编辑 / 编排整页形态）。 -->
  <div class="canvas-editor" :class="{ 'canvas-editor--fill': fill, 'canvas-editor--readonly': readonly }">
    <aside v-if="!readonly" class="canvas-editor__palette">
      <div
        v-for="t in PALETTE_NODE_TYPES"
        :key="t"
        class="canvas-editor__palette-item"
        draggable="true"
        @dragstart="onDragStart($event, t)"
      >
        <span class="canvas-editor__palette-dot" :class="`hf-wf-node-dot-${t}`" />
        <span>{{ NODE_TYPE_NAMES[t] }}</span>
      </div>

      <!-- 起始节点（entry）下拉（2026-09-23 裁定：取消画布伪「开始」节点）：选项 =
           画布现存节点 key（`key（类型名）`）；首个拖入自动默认、删除起始经
           migrateStartKey 迁移、检查器改 Key 随动；双击节点与检查器「设为起始」
           按钮为等价入口——全部汇到 startKey 单源 -->
      <div class="canvas-editor__palette-field">
        <span class="canvas-editor__palette-label">起始节点</span>
        <el-select
          v-model="startKey"
          class="canvas-editor__palette-select"
          placeholder="拖入首节点自动指定"
          @change="refreshStartClasses"
        >
          <el-option v-for="n in flowNodes" :key="n.id" :label="startOptionLabel(n)" :value="n.id" />
        </el-select>
      </div>

      <!-- 伪节点面板入口（FR-002）：点击置伪节点上下文 → 检查器切 schema 面板
           （入参/出参行表单）；伪节点不进 nodes 数组、不进图主体序列化 -->
      <button type="button" class="canvas-editor__palette-schema" @click="openSchemaPanel">
        入参 / 出参
      </button>

      <p class="canvas-editor__palette-hint">拖入画布 · 下拉 / 双击节点 / 检查器按钮指定起始</p>
    </aside>

    <div class="canvas-editor__flow" @dragover.prevent @drop="onDrop">
      <VueFlow
        v-model:nodes="flowNodes"
        v-model:edges="flowEdges"
        fit-view-on-init
        :nodes-draggable="!readonly"
        :nodes-connectable="!readonly"
        :nodes-selectable="!readonly"
        :edges-selectable="!readonly"
      >
        <Background />
        <Controls />
      </VueFlow>
    </div>

    <!-- 右侧检查器（FR-010）：选中 node/edge 由下方 click hooks 维护，对象直传（引用共享）；
         伪节点上下文（sentinel）同样经 node prop 传入 → 面板模式（contracts §2）；
         schema 同源 props/emits 透传（FR-002，contracts §3）；只读态不渲染 -->
    <NodeInspector
      v-if="!readonly"
      :node="selectedNode"
      :edge="selectedEdge"
      :graph="inspectorGraph"
      :start-node-key="startKey"
      :readonly="readonly"
      :graph-kind="graphKind"
      :input-schema="inputSchema"
      :output-schema="outputSchema"
      :self-workflow-id="selfWorkflowId"
      @delete-node="onDeleteNode"
      @delete-edge="onDeleteEdge"
      @rename-node-key="onRenameNodeKey"
      @set-start="onSetStart"
      @update:input-schema="onInputSchemaChange"
      @update:output-schema="onOutputSchemaChange"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, shallowRef } from 'vue'
import {
  MarkerType,
  VueFlow,
  useVueFlow,
  type Edge as FlowEdge,
  type Node as FlowNode,
} from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import NodeInspector from './NodeInspector.vue'
import type { SchemaField } from '@/api/workflow'
import {
  NODE_TYPE_NAMES,
  PALETTE_NODE_TYPES,
  START_NODE_ID,
  START_NODE_TYPE,
  canvasNodeClass,
  canvasToGraphEdges,
  canvasToGraphNodes,
  generateNodeKey,
  graphToCanvas,
  isStartContextNode,
  migrateStartKey,
  renameNodeKey,
  type CanvasNodeData,
  type GraphConfig,
  type InspectorEdge,
  type InspectorGraphContext,
  type InspectorNode,
  type NodePositions,
  type PaletteNodeType,
} from './graph'

const props = defineProps<{
  /** 初始图配置：挂载时转为画布状态；此后画布为源，经 getGraph() 出口转换 */
  config: GraphConfig
  /** 会话级位置 Map（父组件持有）：拖入/拖动写入、往返保留、不序列化 */
  positions: NodePositions
  /** 只读态（详情页）：藏面板/检查器、禁拖动/连线/选中删除，drop 短路；平移缩放保留（FR-005） */
  readonly?: boolean
  /** 高度铺满父容器（编辑页/编排页整页形态）；缺省 420px 保持 009 表单内嵌形态 */
  fill?: boolean
  /** 伪节点面板同源 schema（FR-002）：宿主页单源，经检查器面板读写（contracts §3） */
  inputSchema?: SchemaField[]
  outputSchema?: SchemaField[]
  /** 工作流型别：伪节点面板模式（task = 两段 schema 行表单 / chat = 只读 input 说明） */
  graphKind?: 'task' | 'chat'
  /** 自身工作流 id（编辑态）：子工作流下拉排除自身（FR-006） */
  selfWorkflowId?: string | null
}>()

const emit = defineEmits<{
  'update:inputSchema': [rows: SchemaField[]]
  'update:outputSchema': [rows: SchemaField[]]
}>()

const {
  addEdges,
  addNodes,
  onConnect,
  onEdgeClick,
  onEdgesChange,
  onNodeClick,
  onNodeDoubleClick,
  onNodeDragStop,
  onNodesChange,
  onPaneClick,
  removeEdges,
  removeNodes,
  screenToFlowCoordinate,
} = useVueFlow()

// 画布状态（Vue Flow 双向绑定；store 同步后元素为内部 GraphNode，data 引用保留）。
// shallowRef：vue-flow store 已对节点做响应式，本地深响应既多余又触发 UnwrapRef 深实例化（TS2589）
const initial = graphToCanvas(props.config, props.positions)
const flowNodes = shallowRef<FlowNode<CanvasNodeData>[]>(initial.nodes)
const flowEdges = shallowRef<FlowEdge[]>(initial.edges)

/** 起始节点 key（画布内维护；首个放入默认，面板下拉切换，删除时迁移） */
const startKey = ref(props.config.start_node_key)

/** 起始下拉选项展示：`key（类型名）`（类型名取面板命名表；未知类型兜底空串仅显 key） */
function startOptionLabel(node: FlowNode<CanvasNodeData>): string {
  const type = node.data?.nodeType as PaletteNodeType | undefined
  const typeName = type ? NODE_TYPE_NAMES[type] ?? '' : ''
  return typeName ? `${node.id}（${typeName}）` : node.id
}

// ---- 选中态（FR-010）：点节点/连线切换检查器内容；点空白画布清空 ----

const selectedNode = ref<InspectorNode | null>(null)
const selectedEdge = ref<InspectorEdge | null>(null)

/** 检查器图上下文（数组直传引用）：key 冲突校验 / 变量源计算的输入面 */
const inspectorGraph = computed<InspectorGraphContext>(() => ({
  nodes: flowNodes.value,
  edges: flowEdges.value,
}))

onNodeClick(({ node }) => {
  selectedNode.value = node
  selectedEdge.value = null
})

onEdgeClick(({ edge }) => {
  selectedEdge.value = edge
  selectedNode.value = null
})

onPaneClick(() => {
  selectedNode.value = null
  selectedEdge.value = null
})

// ---- 伪节点面板（FR-002）：sentinel 上下文不进 nodes 数组、不进图主体序列化 ----

/** 打开伪节点面板：置伪节点上下文（sentinel InspectorNode），检查器切 schema 面板模式
 *  （2026-09-23 裁定：画布伪「开始」节点取消，面板入口移到左侧面板按钮） */
function openSchemaPanel(): void {
  selectedNode.value = {
    id: START_NODE_ID,
    data: { nodeType: START_NODE_TYPE, config: {} },
  }
  selectedEdge.value = null
}

// ---- schema 同源透传（FR-002，contracts §3）：检查器面板编辑 → 宿主单源 ----

function onInputSchemaChange(rows: SchemaField[]): void {
  emit('update:inputSchema', rows)
}

function onOutputSchemaChange(rows: SchemaField[]): void {
  emit('update:outputSchema', rows)
}

// ---- 左侧面板拖拽（官方 DnD 模式：dragstart 携带类型 → drop 换算落点） ----

const NODE_TYPE_MIME = 'application/hify-workflow-node-type'

/** 类型守卫：drop 取回的字符串收窄为面板节点类型（Set.has 不收窄，索引 NODE_TYPE_NAMES 需要） */
function isPaletteNodeType(v: string): v is PaletteNodeType {
  return (PALETTE_NODE_TYPES as readonly string[]).includes(v)
}

function onDragStart(event: DragEvent, type: PaletteNodeType): void {
  if (!event.dataTransfer) return
  event.dataTransfer.setData(NODE_TYPE_MIME, type)
  event.dataTransfer.effectAllowed = 'move'
}

function onDrop(event: DragEvent): void {
  if (props.readonly) return
  const type = event.dataTransfer?.getData(NODE_TYPE_MIME) ?? ''
  if (!isPaletteNodeType(type)) return
  const position = screenToFlowCoordinate({ x: event.clientX, y: event.clientY })
  const key = generateNodeKey(type, flowNodes.value.map((n) => n.id))
  const isStart = !startKey.value // 空起始时首个放入节点默认起始
  if (isStart) startKey.value = key
  props.positions.set(key, position)
  addNodes([
    {
      id: key,
      position,
      label: NODE_TYPE_NAMES[type],
      data: { nodeType: type, config: {} },
      class: canvasNodeClass(type, isStart),
    },
  ])
}

// ---- 连线：connect 事件 → addEdges（重复 source→target 静默忽略） ----

onConnect((connection) => {
  if (props.readonly) return
  const duplicated = flowEdges.value.some(
    (e) => e.source === connection.source && e.target === connection.target,
  )
  if (duplicated) return
  addEdges([
    {
      id: `${connection.source}->${connection.target}`,
      source: connection.source,
      target: connection.target,
      markerEnd: MarkerType.ArrowClosed,
    },
  ])
})

// ---- 删除（v-model 自动应用）：起始迁移 + 位置清理 + 悬挂边兜底清理 ----

onNodesChange((changes) => {
  const removedIds = new Set(
    changes.filter((c) => c.type === 'remove').map((c) => c.id),
  )
  if (removedIds.size === 0) return
  removedIds.forEach((id) => props.positions.delete(id)) // 位置 Map 同步清理
  const survivors = flowNodes.value.filter((n) => !removedIds.has(n.id)).map((n) => n.id)
  removedIds.forEach((id) => {
    startKey.value = migrateStartKey(startKey.value, id, survivors, flowEdges.value) // 删起始 → 剩余首个无入边者
  })
  // 兜底：清理指向已删节点的悬挂边（Vue Flow 自动移除与否两态均幂等）
  flowEdges.value = flowEdges.value.filter(
    (e) => !removedIds.has(e.source) && !removedIds.has(e.target),
  )
  if (selectedNode.value && removedIds.has(selectedNode.value.id)) selectedNode.value = null
  refreshStartClasses()
})

// ---- 删除连线（v-model 自动应用）：清空指向已删边的选中态 ----

onEdgesChange((changes) => {
  const removedIds = new Set(changes.filter((c) => c.type === 'remove').map((c) => c.id))
  if (removedIds.size === 0) return
  if (selectedEdge.value?.id && removedIds.has(selectedEdge.value.id)) {
    selectedEdge.value = null
  }
})

// ---- 拖动结束：位置写入会话 Map（往返保留；不序列化进配置） ----

onNodeDragStop(({ node }) => {
  props.positions.set(node.id, node.position)
})

// ---- 起始切换：重刷节点的起始 class（下拉选择 / 删除迁移 / 改名后调用） ----

/** 重刷全部节点的类型/起始 class（起始变化后调用） */
function refreshStartClasses(): void {
  flowNodes.value.forEach((n) => {
    n.class = canvasNodeClass(n.data?.nodeType ?? '', n.id === startKey.value)
  })
}

/** 设为起始（下拉 / 双击 / 检查器按钮三入口共用）：改 startKey 单源 + 重刷起始 class */
function onSetStart(key: string): void {
  if (props.readonly || !key || key === startKey.value) return
  startKey.value = key
  refreshStartClasses()
}

/** 双击节点 = 设为起始（三入口之一；readonly 守卫在 onSetStart 内） */
onNodeDoubleClick(({ node }) => {
  onSetStart(node.id)
})

// ---- 检查器删除 / 改名事件接线（FR-001/FR-003） ----

/** 删除节点：removeNodes 触发与 Backspace 相同的 remove changes，
 *  悬挂边/positions/migrateStartKey/选中清空走既有 onNodesChange 级联链（零新逻辑） */
function onDeleteNode(): void {
  const id = selectedNode.value?.id
  if (props.readonly || !id || isStartContextNode(selectedNode.value)) return // 伪节点无实体可删
  removeNodes([id])
}

/** 删除连线：removeEdges 触发 edgesChange remove，走既有 onEdgesChange 选中清理 */
function onDeleteEdge(edgeId: string): void {
  if (props.readonly || !edgeId) return
  removeEdges([edgeId])
}

/** Key 改名（FR-003）：应用 renameNodeKey 返回的新状态（五级联点）——
 *  nodes/edges 换新数组、positions 共享 Map 键迁移、startKey/selected 随动 */
function onRenameNodeKey(oldKey: string, newKey: string): void {
  if (props.readonly || !oldKey || !newKey || oldKey === newKey) return
  const r = renameNodeKey(
    {
      nodes: flowNodes.value,
      edges: flowEdges.value,
      startKey: startKey.value,
      positions: props.positions,
      selectedKey: selectedNode.value?.id ?? null,
    },
    oldKey,
    newKey,
  )
  flowNodes.value = r.nodes
  flowEdges.value = r.edges
  startKey.value = r.startKey
  // 共享位置 Map 同步键迁移（函数返回的是副本，会话 Map 才是持久单源）
  const pos = props.positions.get(oldKey)
  props.positions.delete(oldKey)
  if (pos) props.positions.set(newKey, pos)
  selectedNode.value = r.nodes.find((n) => n.id === r.selectedKey) ?? null
  refreshStartClasses()
}

// ---- 出口：画布状态 → 图配置（config 引用共享；父组件用于序列化/提交） ----

function getGraph(): GraphConfig {
  return {
    start_node_key: startKey.value,
    nodes: canvasToGraphNodes(flowNodes.value),
    edges: canvasToGraphEdges(flowEdges.value),
  }
}

defineExpose({ getGraph })
</script>

<style scoped>
.canvas-editor {
  display: flex;
  gap: var(--hf-space-3);
  width: 100%;
}

/* 左侧节点面板 */
.canvas-editor__palette {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-2);
  width: 140px;
  flex-shrink: 0;
}

.canvas-editor__palette-item {
  display: flex;
  align-items: center;
  gap: var(--hf-space-2);
  padding: var(--hf-space-2) var(--hf-space-3);
  border: 1px solid var(--hf-border-1);
  border-radius: var(--hf-radius-sm);
  background: var(--hf-bg-container);
  font-size: var(--hf-font-size-sm);
  cursor: grab;
  user-select: none;
}

.canvas-editor__palette-item:active {
  cursor: grabbing;
}

.canvas-editor__palette-dot {
  width: 8px;
  height: 8px;
  border-radius: var(--hf-radius-full);
  flex-shrink: 0;
}

/* 起始节点（entry）下拉：entry 唯一指定入口（2026-09-23 裁定，替代已取消的
   画布伪「开始」节点连线）；选项 = 画布现存节点 key */
.canvas-editor__palette-field {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-1);
  margin-top: var(--hf-space-2);
}

.canvas-editor__palette-label {
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-xs);
}

.canvas-editor__palette-select {
  width: 100%;
}

/* 伪节点面板入口（FR-002）：检查器 schema 面板的打开按钮 */
.canvas-editor__palette-schema {
  margin-top: var(--hf-space-2);
  padding: var(--hf-space-2) var(--hf-space-3);
  border: 1px solid var(--hf-border-1);
  border-radius: var(--hf-radius-sm);
  background: var(--hf-bg-container);
  color: var(--hf-text-1);
  font-size: var(--hf-font-size-sm);
  cursor: pointer;
}

.canvas-editor__palette-schema:hover {
  border-color: var(--hf-primary-300);
}

.canvas-editor__palette-hint {
  margin: var(--hf-space-2) 0 0;
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
  line-height: var(--hf-leading-normal);
}

/* 画布容器（Vue Flow 需要显式尺寸） */
.canvas-editor__flow {
  flex: 1;
  min-width: 0;
  height: 420px;
  border: 1px solid var(--hf-border-1);
  border-radius: var(--hf-radius-md);
  overflow: hidden;
}

/* 整页形态（fill）：高度铺满父容器，画布交由 flex 拉伸；缺省 420px 保持 009 表单内嵌 */
.canvas-editor--fill {
  height: 100%;
  min-height: 0;
}

.canvas-editor--fill .canvas-editor__flow {
  height: auto;
}

/* 面板节点色点：与画布节点类型色一致 */
.hf-wf-node-dot-llm {
  background: var(--hf-primary-500);
}

.hf-wf-node-dot-end {
  background: var(--hf-success);
}

.hf-wf-node-dot-condition {
  background: var(--hf-warning);
}

.hf-wf-node-dot-api {
  background: var(--hf-accent-500);
}

.hf-wf-node-dot-workflow {
  background: var(--hf-info);
}

/* 画布节点按类型差异化着色（默认节点主题的边框/底色覆盖） */
.canvas-editor__flow :deep(.vue-flow__node.hf-wf-node-llm .vue-flow__node-default) {
  border-color: var(--hf-primary-300);
  background: var(--hf-primary-50);
}

.canvas-editor__flow :deep(.vue-flow__node.hf-wf-node-end .vue-flow__node-default) {
  border-color: var(--hf-success-border);
  background: var(--hf-success-soft);
}

.canvas-editor__flow :deep(.vue-flow__node.hf-wf-node-condition .vue-flow__node-default) {
  border-color: var(--hf-warning-border);
  background: var(--hf-warning-soft);
}

.canvas-editor__flow :deep(.vue-flow__node.hf-wf-node-api .vue-flow__node-default) {
  border-color: var(--hf-accent-200);
  background: var(--hf-accent-50);
}

.canvas-editor__flow :deep(.vue-flow__node.hf-wf-node-workflow .vue-flow__node-default) {
  border-color: var(--hf-info-border);
  background: var(--hf-info-soft);
}

/* 起始节点标识：主色强调边框 + 聚焦环（entry 在画布上的唯一视觉指示） */
.canvas-editor__flow :deep(.vue-flow__node.hf-wf-node--start .vue-flow__node-default) {
  border-color: var(--hf-primary-600);
  box-shadow: var(--hf-shadow-focus);
}

/* readonly 详情态起始角标：左面板 / 检查器不渲染，起始仅剩边框可见——加「起始」文字
   角标补足辨识（挂在节点 wrapper 上随节点走；不改 label，防泄入 getGraph 序列化） */
.canvas-editor--readonly
  .canvas-editor__flow
  :deep(.vue-flow__node.hf-wf-node--start::after) {
  content: '起始';
  position: absolute;
  top: calc(var(--hf-space-1) * -1);
  left: calc(var(--hf-space-1) * -1);
  padding: 0 var(--hf-space-1);
  border-radius: var(--hf-radius-sm);
  background: var(--hf-primary-600);
  color: var(--hf-bg-container);
  font-size: var(--hf-font-size-xs);
  line-height: var(--hf-leading-normal);
  pointer-events: none;
  z-index: 1;
}
</style>
