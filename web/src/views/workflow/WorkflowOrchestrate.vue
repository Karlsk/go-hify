<template>
  <!-- 工作流创建第二步（spec 010 两步式，整页编排 fullBleed）：初始图 = 草稿图 ??
       PREFILL 深拷贝（预填智能客服分类示例图，Clarifications）；画布默认可切
       JSON（GraphModeEditor fill）。工具栏：上一步（离开即回写草稿 + 回第一步，
       不弹确认）+ 名称 / 类型只读展示（修改走上一步）+ 保存并创建（一次 POST
       带 type——与 PUT 双轨隔离，成功清草稿回列表，FR-012/015）。直访 / 刷新：
       草稿内存态归零 → 挂载即回第一步（FR-013）。
       I/O Schema 单源（FR-002）：画布「入参 / 出参」面板直连第一步 store 字段（编辑即写回），
       与图的离开时回写两条通道并行；创建无自身 id，不传 selfWorkflowId。 -->
  <div v-if="store.hasForm" class="workflow-orchestrate">
    <header class="workflow-orchestrate__toolbar">
      <el-button @click="router.push('/workflows/create')">
        <el-icon><ArrowLeft /></el-icon>
        上一步
      </el-button>
      <div class="workflow-orchestrate__meta">
        <span class="workflow-orchestrate__name">{{ store.name }}</span>
        <el-tag :type="store.type === 'task' ? 'warning' : 'primary'" size="small">
          {{ store.type === 'task' ? '任务型' : '对话型' }}
        </el-tag>
      </div>
      <div class="workflow-orchestrate__spacer" />
      <el-button type="primary" :loading="saving" @click="save">
        保存并创建
      </el-button>
    </header>

    <main class="workflow-orchestrate__body">
      <GraphModeEditor
        ref="graphRef"
        :initial="initialGraph"
        default-mode="canvas"
        fill
        :input-schema="store.inputSchema"
        :output-schema="store.outputSchema"
        :graph-kind="store.type"
        @update:input-schema="onInputSchemaChange"
        @update:output-schema="onOutputSchemaChange"
      />
    </main>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import { ArrowLeft } from '@element-plus/icons-vue'
import GraphModeEditor from './GraphModeEditor.vue'
import { createWorkflow, type SchemaField } from '@/api/workflow'
import { notifyError, notifySuccess } from '@/utils/notify'
import {
  buildCreatePayload,
  graphSubmitError,
  prefillGraphCopy,
  schemaFieldsError,
  type GraphConfig,
} from './graph'
import { useWorkflowCreateDraftStore } from '@/stores/workflowCreateDraft'

const router = useRouter()
const store = useWorkflowCreateDraftStore()

// 直访 / 刷新：草稿内存态归零（hasForm false）→ 回第一步（FR-013；replace 触发
// 的离开不经图回写——守卫对无草稿态直接放行）
if (!store.hasForm) {
  void router.replace('/workflows/create')
}

/** 初始图：第二步此前编辑过的草稿图 ?? 预填示例图深拷贝（防画布编辑污染常量） */
const initialGraph: GraphConfig = store.graph ?? prefillGraphCopy()

/** 图编排实例（getGraph 出口） */
const graphRef = ref<{ getGraph: () => GraphConfig | null }>()

/** 画布「入参 / 出参」面板 schema 编辑写回草稿 store（FR-002）：第一步表单与面板
 *  共用同一单源（编辑即写回，区别于图的离开时回写——上一步 / 误点不丢面板编辑） */
function onInputSchemaChange(rows: SchemaField[]): void {
  store.saveInputSchema(rows)
}

function onOutputSchemaChange(rows: SchemaField[]): void {
  store.saveOutputSchema(rows)
}

const saving = ref(false)

/**
 * 离开第二步：图编排回写草稿（上一步 / 侧边栏误点不丢图）。JSON 非法时
 * getGraph() 返回 null（组件已 notify）——跳过回写、保留上一份合法图。
 */
onBeforeRouteLeave(() => {
  if (!store.hasForm) return true // 直访回退 / 创建成功后：无草稿可回写
  const g = graphRef.value?.getGraph()
  if (g) store.saveGraph(g)
  return true
})

// ---- 保存并创建（FR-015：一次 POST；成功清草稿回列表，失败留页内容不丢） ----

async function save(): Promise<void> {
  if (saving.value) return
  // task 型：Schema 字段行先本地校验（SC-004 前端拦截——画布「入参 / 出参」面板是第二编辑入口，
  // 与第一步 / 编辑页同一条校验，错误文案含行号，不发请求）
  if (store.type === 'task') {
    const err =
      schemaFieldsError(store.inputSchema) ?? schemaFieldsError(store.outputSchema)
    if (err) {
      notifyError(err)
      return
    }
  }
  const g = graphRef.value?.getGraph()
  if (!g) return // JSON 非法 / 画布未就绪——提示已由组件 notify

  // 图规则前端预检（009 既有：空图 / 起始节点——后端图校验 R3/R1 的预检）
  const graphError = graphSubmitError(g)
  if (graphError) {
    notifyError(graphError)
    return
  }

  saving.value = true
  try {
    await createWorkflow(
      // POST 带 type（后端创建必填）——与编辑侧 PUT 不带 type 双轨隔离
      buildCreatePayload({
        name: store.name,
        description: store.description,
        type: store.type,
        graph: g,
        inputSchema: store.type === 'task' ? store.inputSchema : undefined,
        outputSchema: store.type === 'task' ? store.outputSchema : undefined,
      }),
    )
  } catch {
    return // 失败留第二步、内容不丢；提示已由拦截器弹
  } finally {
    saving.value = false
  }
  notifySuccess('创建成功')
  store.clear() // 守卫对清零后的草稿直接放行（不再回写图）
  void router.push('/workflows')
}
</script>

<style scoped>
/* 满血整页形态（fullBleed）：root 撑满 App.vue 去 padding 的 el-main */
.workflow-orchestrate {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  background-color: var(--hf-bg-container);
}

.workflow-orchestrate__toolbar {
  display: flex;
  align-items: center;
  gap: var(--hf-space-3);
  height: 56px;
  padding: 0 var(--hf-space-5);
  border-bottom: 1px solid var(--hf-border-1);
  flex-shrink: 0;
}

.workflow-orchestrate__meta {
  display: flex;
  align-items: center;
  gap: var(--hf-space-2);
  min-width: 0;
}

.workflow-orchestrate__name {
  overflow: hidden;
  color: var(--hf-text-1);
  font-size: var(--hf-font-size-md);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workflow-orchestrate__spacer {
  flex: 1;
}

.workflow-orchestrate__body {
  flex: 1;
  min-height: 0;
  padding: var(--hf-space-3);
}
</style>
