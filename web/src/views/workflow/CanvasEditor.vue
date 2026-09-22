<template>
  <!-- 拖拽编排画布（US3）：左侧五类节点面板（原生拖拽）+ Vue Flow 画布。
       状态模型（data-model §3）：挂载时从 config 初始化画布节点/边，此后画布即源，
       出口经 getGraph() 转回图配置——画布节点的 data.config 与配置节点共享引用，
       检查器（T014）改 config 即改配置单源；节点位置存 positions Map（父组件
       会话级持有，往返保留，不序列化进配置）。起始节点：首个放入默认起始，
       双击节点切换（hf-wf-node--start class 标识）。
       只读态（readonly，spec 010）：面板 / 检查器不渲染、编辑交互全禁，
       平移缩放保留（FR-005）；fill = 高度铺满父容器（编辑 / 编排整页形态）。 -->
  <div class="canvas-editor" :class="{ 'canvas-editor--fill': fill }">
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
      <p class="canvas-editor__palette-hint">拖入画布 · 双击节点设为起始</p>
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

    <!-- 右侧检查器（FR-010）：选中 node/edge 由下方 click hooks 维护，对象直传（引用共享）；只读态不渲染 -->
    <NodeInspector v-if="!readonly" :node="selectedNode" :edge="selectedEdge" />
  </div>
</template>

<script setup lang="ts">
import { ref, shallowRef } from 'vue'
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
import {
  NODE_TYPE_NAMES,
  PALETTE_NODE_TYPES,
  canvasNodeClass,
  canvasToGraphEdges,
  canvasToGraphNodes,
  generateNodeKey,
  graphToCanvas,
  migrateStartKey,
  type CanvasNodeData,
  type GraphConfig,
  type InspectorEdge,
  type InspectorNode,
  type NodePositions,
  type PaletteNodeType,
} from './graph'

const props = defineProps<{
  /** 初始图配置：挂载时转为画布状态；此后画布为源，经 getGraph() 出口转换 */
  config: GraphConfig
  /** 会话级位置 Map（父组件持有）：拖入/拖动写入、往返保留、不序列化 */
  positions: NodePositions
  /** 只读态（详情页）：藏面板/检查器、禁拖动/连线/选中删除，drop 与双击起始短路；平移缩放保留（FR-005） */
  readonly?: boolean
  /** 高度铺满父容器（编辑页/编排页整页形态）；缺省 420px 保持 009 表单内嵌形态 */
  fill?: boolean
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
  screenToFlowCoordinate,
} = useVueFlow()

// 画布状态（Vue Flow 双向绑定；store 同步后元素为内部 GraphNode，data 引用保留）。
// shallowRef：vue-flow store 已对节点做响应式，本地深响应既多余又触发 UnwrapRef 深实例化（TS2589）
const initial = graphToCanvas(props.config, props.positions)
const flowNodes = shallowRef<FlowNode<CanvasNodeData>[]>(initial.nodes)
const flowEdges = shallowRef<FlowEdge[]>(initial.edges)

/** 起始节点 key（画布内维护；首个放入默认，双击切换，删除时迁移） */
const startKey = ref(props.config.start_node_key)

// ---- 选中态（FR-010）：点节点/连线切换检查器内容；点空白画布清空 ----

const selectedNode = ref<InspectorNode | null>(null)
const selectedEdge = ref<InspectorEdge | null>(null)

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
    startKey.value = migrateStartKey(startKey.value, id, survivors) // 删起始 → 剩余首个
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

// ---- 起始切换：双击节点 ----

onNodeDoubleClick(({ node }) => {
  if (props.readonly) return
  startKey.value = node.id
  refreshStartClasses()
})

/** 重刷全部节点的类型/起始 class（起始变化后调用） */
function refreshStartClasses(): void {
  flowNodes.value.forEach((n) => {
    n.class = canvasNodeClass(n.data?.nodeType ?? '', n.id === startKey.value)
  })
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

/* 起始节点标识：主色强调边框 + 聚焦环 */
.canvas-editor__flow :deep(.vue-flow__node.hf-wf-node--start .vue-flow__node-default) {
  border-color: var(--hf-primary-600);
  box-shadow: var(--hf-shadow-focus);
}
</style>
