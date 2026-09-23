<template>
  <!-- 工作流编辑页（US2 整页形态，fullBleed）：工具栏（返回 + 名称 / 描述内联 +
       类型禁用单选「类型不可变，换型需删除重建」FR-010 + task 型 I/O Schema
       抽屉入口 FR-008）+ GraphModeEditor 整页编排（画布默认可切 JSON，fill
       铺满）。GET 回填（404 / 失败回列表）；脏态守卫双通道（路由离开确认 +
       浏览器刷新 / 关闭拦截，FR-011）；保存链路（schema 校验 → getGraph →
       buildUpdatePayload 不带 type → PUT，成功回详情页，FR-009）。
       I/O Schema 单源（FR-002）：抽屉表单与伪节点面板绑同一页面 ref，一处改另一处同步；
       selfWorkflowId 透传 = 子工作流下拉排除自身（FR-006）。 -->
  <div v-loading="loading" class="workflow-edit">
    <template v-if="detail">
      <header class="workflow-edit__toolbar">
        <el-button @click="goBack">
          <el-icon><ArrowLeft /></el-icon>
          返回
        </el-button>
        <el-input v-model="name" class="workflow-edit__name" placeholder="名称" />
        <el-input
          v-model="description"
          class="workflow-edit__desc"
          placeholder="描述（可选）"
        />
        <el-radio-group :model-value="type" disabled>
          <el-radio-button value="chat">对话型</el-radio-button>
          <el-radio-button value="task">任务型</el-radio-button>
        </el-radio-group>
        <el-tooltip content="类型不可变，换型需删除重建" placement="bottom">
          <el-icon class="workflow-edit__type-lock"><InfoFilled /></el-icon>
        </el-tooltip>
        <el-button v-if="type === 'task'" @click="schemaDrawer = true">
          I/O Schema
        </el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </header>

      <main class="workflow-edit__body">
        <GraphModeEditor
          v-if="graph"
          ref="graphRef"
          :initial="graph"
          default-mode="canvas"
          fill
          :input-schema="inputSchema"
          :output-schema="outputSchema"
          :graph-kind="type"
          :self-workflow-id="String(route.params.id)"
          @update:input-schema="onInputSchemaChange"
          @update:output-schema="onOutputSchemaChange"
        />
      </main>

      <!-- task 型 I/O Schema 抽屉：两个 SchemaFieldsEditor 编辑页面状态（v-model） -->
      <el-drawer v-model="schemaDrawer" title="入参 / 出参 Schema" size="720px">
        <section class="workflow-edit__schema">
          <h4 class="workflow-edit__schema-title">入参（input_schema）</h4>
          <SchemaFieldsEditor v-model="inputSchema" />
        </section>
        <section class="workflow-edit__schema">
          <h4 class="workflow-edit__schema-title">出参（output_schema）</h4>
          <SchemaFieldsEditor v-model="outputSchema" />
        </section>
      </el-drawer>
    </template>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { ArrowLeft, InfoFilled } from '@element-plus/icons-vue'
import GraphModeEditor from './GraphModeEditor.vue'
import SchemaFieldsEditor from './SchemaFieldsEditor.vue'
import {
  getWorkflowDetail,
  updateWorkflow,
  type SchemaField,
  type WorkflowDetail,
  type WorkflowType,
} from '@/api/workflow'
import { notifyError, notifySuccess } from '@/utils/notify'
import {
  buildUpdatePayload,
  detailToGraphConfig,
  schemaFieldsError,
  type GraphConfig,
} from './graph'

const route = useRoute()
const router = useRouter()

const loading = ref(true)
const detail = ref<WorkflowDetail | null>(null)
/** 图配置初始（加载后一次性转换；GraphModeEditor 挂载一次性消费） */
const graph = ref<GraphConfig | null>(null)

// ---- 表单状态（回填自 detail；编辑即产生脏态，守卫按快照比对） ----

const name = ref('')
const description = ref('')
const type = ref<WorkflowType>('chat')
const inputSchema = ref<SchemaField[]>([])
const outputSchema = ref<SchemaField[]>([])

/** 伪节点面板 schema 编辑写回（FR-002）：面板与抽屉表单绑同一 ref（单源），
 *  脏态守卫 snapshot() 读的就是这两个 ref——面板编辑自动纳入离开确认 */
function onInputSchemaChange(rows: SchemaField[]): void {
  inputSchema.value = rows
}

function onOutputSchemaChange(rows: SchemaField[]): void {
  outputSchema.value = rows
}

/** task 型 I/O Schema 抽屉开关 */
const schemaDrawer = ref(false)

/** 双模式图编排实例（getGraph / normalizedGraphText 出口，保存与脏态比对用） */
const graphRef = ref<{
  getGraph: () => GraphConfig | null
  normalizedGraphText: () => string | null
}>()

function goBack(): void {
  void router.push(`/workflows/${String(route.params.id)}`)
}

onMounted(async () => {
  try {
    const d = await getWorkflowDetail(String(route.params.id))
    detail.value = d
    graph.value = detailToGraphConfig(d)
    name.value = d.name
    description.value = d.description
    type.value = d.type
    inputSchema.value = d.input_schema ?? []
    outputSchema.value = d.output_schema ?? []
  } catch {
    loading.value = false
    void router.replace('/workflows') // 404 / 失败提示已由拦截器弹
    return // 失败已跳转，不记基准快照
  }
  loading.value = false
  await nextTick() // 等 GraphModeEditor 挂载（v-if 数据就绪），快照才能取到图文本
  baseline = snapshot()
})

// ---- 脏态守卫（FR-011 双通道） ----

/**
 * 基准快照（加载完成 + 图编排挂载后记录一次）。dirty 判定改为守卫触发时点现场
 * 序列化比对而非 watch(dirty) computed：检查器直改共享 config 引用不触发响应式，
 * stale dirty 会漏拦浏览器刷新——现场比对无漏拦窗口（偏离 tasks.md 字面，行为等价）。
 */
let baseline = ''

/** 保存成功标记：置位后守卫放行（跳详情页不弹离开确认） */
const saved = ref(false)

/** 当前全量表单快照（graphText 走 normalizedGraphText 规范化——U1：点「格式化」等纯格式差异不计 dirty；JSON 非法得 null = 视为已修改） */
function snapshot(): string {
  return JSON.stringify({
    name: name.value,
    description: description.value,
    inputSchema: inputSchema.value,
    outputSchema: outputSchema.value,
    graphText: graphRef.value?.normalizedGraphText() ?? null,
  })
}

function isDirty(): boolean {
  return !saved.value && detail.value !== null && snapshot() !== baseline
}

/** 通道一：路由跳转守卫（站内离开确认） */
onBeforeRouteLeave(async () => {
  if (!isDirty()) return true
  try {
    await ElMessageBox.confirm('未保存的修改将丢失，确认离开？', '离开确认', {
      type: 'warning',
      confirmButtonText: '离开',
      cancelButtonText: '留下',
    })
    return true
  } catch {
    return false // 取消：留在编辑页
  }
})

/** 通道二：浏览器刷新 / 关闭（常驻注册，事件时点判定 dirty） */
function onBeforeUnload(e: BeforeUnloadEvent): void {
  if (!isDirty()) return
  e.preventDefault()
  e.returnValue = '' // Chrome 需要 returnValue 才弹原生确认
}

onMounted(() => {
  window.addEventListener('beforeunload', onBeforeUnload)
})

onUnmounted(() => {
  window.removeEventListener('beforeunload', onBeforeUnload)
})

// ---- 保存链路（FR-009） ----

const saving = ref(false)

async function save(): Promise<void> {
  if (saving.value || !detail.value) return
  // task 型：Schema 字段行先本地校验（SC-004 前端拦截率——错误文案含行号，不发请求）
  if (type.value === 'task') {
    const err =
      schemaFieldsError(inputSchema.value) ?? schemaFieldsError(outputSchema.value)
    if (err) {
      notifyError(err)
      return
    }
  }
  const g = graphRef.value?.getGraph()
  if (!g) return // JSON 非法 / 画布未就绪——提示已由组件 notify
  // PUT 不带 type（硬红线组装层 + 消费层双闸；UpdateWorkflowData 类型层另有一道）
  const payload = buildUpdatePayload({
    name: name.value,
    description: description.value,
    type: type.value,
    graph: g,
    inputSchema: inputSchema.value,
    outputSchema: outputSchema.value,
  })
  saving.value = true
  try {
    await updateWorkflow(detail.value.id, payload)
    saved.value = true // 守卫放行
    notifySuccess('保存成功')
    void router.push(`/workflows/${detail.value.id}`)
  } catch {
    // 失败留页、内容不丢（409 名称冲突 / 400 图规则——提示已由拦截器弹）
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
/* 满血整页形态（fullBleed）：root 撑满 App.vue 去 padding 的 el-main */
.workflow-edit {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  background-color: var(--hf-bg-container);
}

.workflow-edit__toolbar {
  display: flex;
  align-items: center;
  gap: var(--hf-space-3);
  height: 56px;
  padding: 0 var(--hf-space-5);
  border-bottom: 1px solid var(--hf-border-1);
  flex-shrink: 0;
}

.workflow-edit__name {
  width: 220px;
}

.workflow-edit__desc {
  flex: 1;
  min-width: 160px;
}

.workflow-edit__type-lock {
  color: var(--hf-text-3);
}

.workflow-edit__body {
  flex: 1;
  min-height: 0;
  padding: var(--hf-space-3);
}

.workflow-edit__schema {
  margin-bottom: var(--hf-space-5);
}

.workflow-edit__schema-title {
  margin: 0 0 var(--hf-space-2);
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-sm);
  font-weight: 600;
}
</style>
