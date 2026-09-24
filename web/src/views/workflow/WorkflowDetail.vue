<template>
  <!-- 工作流详情页（US1 只读）：基础信息 + task 型入参/出参 Schema 只读表（chat 型
       整块不渲染，FR-003）+ 图编排只读双模式（默认画布、可切 JSON——GraphModeEditor
       readonly，FR-004/005）。404 / 加载失败：拦截器已弹错，页面退回列表（Edge Case）。 -->
  <div v-loading="loading" class="workflow-detail">
    <PageHeader title="工作流详情" description="工作流基础信息与图编排（只读浏览）">
      <template #actions>
        <el-button @click="router.push('/workflows')">
          <el-icon><ArrowLeft /></el-icon>
          返回列表
        </el-button>
        <!-- 试运行（spec 013）：只读页无脏态，直接打开；trial=true 放开 draft/disabled -->
        <el-button v-if="detail" @click="trialVisible = true">试运行</el-button>
        <el-button v-if="detail" type="primary" @click="goEdit">
          <el-icon><Edit /></el-icon>
          编辑
        </el-button>
      </template>
    </PageHeader>

    <template v-if="detail">
      <el-card class="workflow-detail__card">
        <template #header>基础信息</template>
        <el-descriptions :column="2" border>
          <el-descriptions-item label="名称">{{ detail.name }}</el-descriptions-item>
          <el-descriptions-item label="类型">
            <el-tag :type="detail.type === 'task' ? 'warning' : 'primary'" size="small">
              {{ detail.type === 'task' ? '任务型' : '对话型' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="STATUS_TAG[detail.status]" size="small">
              {{ STATUS_TEXT[detail.status] }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="创建时间">
            {{ formatDateTime(detail.created_at) }}
          </el-descriptions-item>
          <el-descriptions-item label="描述" :span="2">
            {{ detail.description || '—' }}
          </el-descriptions-item>
        </el-descriptions>
      </el-card>

      <!-- task 型独有：I/O Schema 只读表（chat 型不显示，FR-003） -->
      <el-card v-if="detail.type === 'task'" class="workflow-detail__card">
        <template #header>入参 / 出参 Schema</template>
        <section class="workflow-detail__schema">
          <h4 class="workflow-detail__schema-title">入参（input_schema）</h4>
          <el-table :data="detail.input_schema ?? []" size="small" border>
            <el-table-column prop="name" label="字段名" min-width="120" />
            <el-table-column prop="type" label="类型" width="90" />
            <el-table-column label="必填" width="70" align="center">
              <template #default="{ row }">{{ row.required ? '是' : '否' }}</template>
            </el-table-column>
            <el-table-column label="描述" min-width="200">
              <template #default="{ row }">{{ row.description || '—' }}</template>
            </el-table-column>
          </el-table>
        </section>
        <section class="workflow-detail__schema">
          <h4 class="workflow-detail__schema-title">出参（output_schema）</h4>
          <el-table :data="detail.output_schema ?? []" size="small" border>
            <el-table-column prop="name" label="字段名" min-width="120" />
            <el-table-column prop="type" label="类型" width="90" />
            <el-table-column label="必填" width="70" align="center">
              <template #default="{ row }">{{ row.required ? '是' : '否' }}</template>
            </el-table-column>
            <el-table-column label="描述" min-width="200">
              <template #default="{ row }">{{ row.description || '—' }}</template>
            </el-table-column>
          </el-table>
        </section>
      </el-card>

      <el-card class="workflow-detail__card">
        <template #header>图编排</template>
        <GraphModeEditor v-if="graph" :initial="graph" default-mode="canvas" readonly />
      </el-card>

      <!-- 运行历史（spec 015）：列表区块自治（挂载拉首页 + 加载更多），行点击详情抽屉 -->
      <WorkflowRunsPanel class="workflow-detail__card" :workflow-id="detail.id" />

      <!-- 试运行对话框（spec 013）：detail 为 GET 已落库版本 -->
      <WorkflowTrialDialog v-model="trialVisible" :workflow="detail" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Edit } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import GraphModeEditor from './GraphModeEditor.vue'
import WorkflowRunsPanel from './WorkflowRunsPanel.vue'
import WorkflowTrialDialog from './WorkflowTrialDialog.vue'
import { getWorkflowDetail, type WorkflowDetail } from '@/api/workflow'
import { detailToGraphConfig, type GraphConfig } from './graph'

const route = useRoute()
const router = useRouter()

const loading = ref(true)
const detail = ref<WorkflowDetail | null>(null)
/** 图配置初始（detail 加载后一次性转换；GraphModeEditor 挂载一次性消费） */
const graph = ref<GraphConfig | null>(null)

/** 试运行对话框开关（spec 013） */
const trialVisible = ref(false)

// 状态三态映射（沿用列表页形态）
const STATUS_TEXT: Record<string, string> = {
  draft: '草稿',
  published: '已发布',
  disabled: '已停用',
}
const STATUS_TAG: Record<string, 'info' | 'success' | 'danger'> = {
  draft: 'info',
  published: 'success',
  disabled: 'danger',
}

function goEdit(): void {
  if (detail.value) void router.push(`/workflows/${detail.value.id}/edit`)
}

// 时间展示：RFC 3339 → YYYY-MM-DD HH:mm（本地时区，与列表页一致）
function formatDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number): string => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
    d.getHours(),
  )}:${pad(d.getMinutes())}`
}

onMounted(async () => {
  try {
    const d = await getWorkflowDetail(String(route.params.id))
    detail.value = d
    graph.value = detailToGraphConfig(d)
  } catch {
    void router.replace('/workflows') // 404 / 失败提示已由拦截器弹
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
.workflow-detail__card {
  margin-bottom: var(--hf-space-3);
}

.workflow-detail__schema {
  margin-bottom: var(--hf-space-3);
}

.workflow-detail__schema:last-child {
  margin-bottom: 0;
}

.workflow-detail__schema-title {
  margin: 0 0 var(--hf-space-2);
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-sm);
  font-weight: 600;
}
</style>
