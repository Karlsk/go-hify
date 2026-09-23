<template>
  <!-- 双模式图编排编辑器（spec 010 收编 009 WorkflowCreate 的模式切换逻辑）：
       JSON 文本（JsonConfigEditor）与拖拽画布（CanvasEditor）共享单一图配置数据源——
       切画布 = JSON 解析（非法 notify 阻断并回退）；切回 JSON = 画布 getGraph()
       序列化（SC-002 双模式一致、未知键透传）。initial 挂载一次性消费；
       readonly / fill 透传两子组件（readonly 下模式切换保留——FR-004 详情页
       默认画布、可切 JSON）。 -->
  <div class="graph-mode-editor" :class="{ 'graph-mode-editor--fill': fill }">
    <el-radio-group v-model="mode" class="graph-mode-editor__mode" @change="onModeChange">
      <el-radio-button value="json">JSON</el-radio-button>
      <el-radio-button value="canvas">拖拽</el-radio-button>
    </el-radio-group>

    <JsonConfigEditor
      v-if="mode === 'json'"
      v-model="graphText"
      :rows="14"
      :readonly="readonly"
      class="graph-mode-editor__pane"
    />
    <div v-else class="graph-mode-editor__canvas">
      <CanvasEditor
        ref="canvasRef"
        :config="canvasConfig"
        :positions="positions"
        :readonly="readonly"
        :fill="fill"
        :input-schema="inputSchema"
        :output-schema="outputSchema"
        :graph-kind="graphKind"
        :self-workflow-id="selfWorkflowId"
        @update:input-schema="onInputSchemaChange"
        @update:output-schema="onOutputSchemaChange"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, shallowRef } from 'vue'
import JsonConfigEditor from './JsonConfigEditor.vue'
import CanvasEditor from './CanvasEditor.vue'
import { notifyError } from '@/utils/notify'
import type { SchemaField } from '@/api/workflow'
import {
  parseGraphConfig,
  serializeGraphConfig,
  type GraphConfig,
  type NodePositions,
} from './graph'

const props = withDefaults(
  defineProps<{
    /** 初始图配置：挂载时一次性消费（此后内部为源，改 prop 不生效） */
    initial: GraphConfig
    /** 初始模式（缺省 json；详情 / 编辑 / 编排页传 canvas——画布默认、可切 JSON） */
    defaultMode?: 'json' | 'canvas'
    /** 只读态（详情页）：透传两子组件；模式切换保留（FR-004） */
    readonly?: boolean
    /** 画布高度铺满父容器（编辑 / 编排整页形态）：透传 CanvasEditor */
    fill?: boolean
    /** 伪节点面板同源 schema（FR-002，contracts §5）：宿主页单源，透传画布/检查器面板 */
    inputSchema?: SchemaField[]
    outputSchema?: SchemaField[]
    /** 工作流型别：伪节点面板模式（chat = 只读 input 说明，FR-002） */
    graphKind?: 'task' | 'chat'
    /** 自身工作流 id（编辑态）：子工作流下拉排除自身（FR-006） */
    selfWorkflowId?: string | null
  }>(),
  { defaultMode: 'json' },
)

const emit = defineEmits<{
  /** 伪节点面板 schema 编辑上行（FR-002）：宿主页 ref 即单源 */
  'update:inputSchema': [rows: SchemaField[]]
  'update:outputSchema': [rows: SchemaField[]]
}>()

function onInputSchemaChange(rows: SchemaField[]): void {
  emit('update:inputSchema', rows)
}

function onOutputSchemaChange(rows: SchemaField[]): void {
  emit('update:outputSchema', rows)
}

// ---- 双模式状态（009 WorkflowCreate 语义原样收编） ----

const mode = ref<'json' | 'canvas'>(props.defaultMode)
const graphText = ref(serializeGraphConfig(props.initial))

/** 画布实例（getGraph 出口转换；v-if 挂载期间可用） */
const canvasRef = ref<{ getGraph: () => GraphConfig }>()
/** 画布初始图配置：defaultMode 为 canvas 时挂载即用 initial；此后每次切入画布重新解析赋值 */
const canvasConfig = ref<GraphConfig>(
  props.defaultMode === 'canvas'
    ? props.initial
    : { start_node_key: '', nodes: [], edges: [] },
)
/** 画布节点位置（会话级；子组件经 props 引用读写，往返保留、不序列化进配置） */
const positions = shallowRef<NodePositions>(new Map())

/**
 * 切画布 = JSON 解析进配置模型（画布按当前配置渲染）；切回 JSON = 画布编辑结果
 * 序列化回编辑器。el-radio 的 change 在 v-model 更新后同步触发、先于 v-if 重渲染
 * ——此刻画布 ref 仍可读。
 */
function onModeChange(value: string | number | boolean | undefined): void {
  if (value === 'canvas') {
    const r = parseGraphConfig(graphText.value)
    if (!r.ok) {
      mode.value = 'json' // 非法 JSON：回退本次切换，留在 JSON 模式修复
      notifyError(`JSON 非法，请先修复后再切换到拖拽模式：${r.error}`)
      return
    }
    canvasConfig.value = r.config
    return
  }
  if (value === 'json') {
    const g = canvasRef.value?.getGraph()
    if (g) graphText.value = serializeGraphConfig(g)
  }
}

// ---- 出口 ----

/** 当前图配置（提交出口）：JSON 模式解析文本（非法 notify + null）；画布模式 getGraph */
function getGraph(): GraphConfig | null {
  if (mode.value === 'json') {
    const r = parseGraphConfig(graphText.value)
    if (!r.ok) {
      notifyError(`工作流配置 JSON 非法：${r.error}`)
      return null
    }
    return r.config
  }
  const g = canvasRef.value?.getGraph()
  if (!g) {
    notifyError('画布未就绪，请稍后重试')
    return null
  }
  return g
}

/**
 * 脏态比对出口（宿主页编辑守卫用，U1 统一规范化）：JSON 模式 parse→serialize、
 * 画布模式 getGraph→serialize——两侧同走 serializeGraphConfig，格式差异（如点过
 * 「格式化」）不计 dirty；JSON 非法返回 null，宿主页视为已修改。
 */
function normalizedGraphText(): string | null {
  if (mode.value === 'json') {
    const r = parseGraphConfig(graphText.value)
    return r.ok ? serializeGraphConfig(r.config) : null
  }
  const g = canvasRef.value?.getGraph()
  return g ? serializeGraphConfig(g) : null
}

defineExpose({ getGraph, normalizedGraphText })
</script>

<style scoped>
.graph-mode-editor {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-3);
  width: 100%;
}

/* 整页形态（fill）：铺满父容器，画布区交由 flex 拉伸 */
.graph-mode-editor--fill {
  height: 100%;
  min-height: 0;
}

.graph-mode-editor__pane {
  width: 100%;
}

.graph-mode-editor__canvas {
  flex: 1;
  min-height: 0;
  display: flex; /* CanvasEditor 撑满可用高度（fill 时） */
}
</style>
