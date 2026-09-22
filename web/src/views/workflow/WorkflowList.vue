<template>
  <!-- 工作流列表页：HifyTable + useConfirm 删除 + 发布/停用生命周期操作。
       类型/状态三态映射与按钮矩阵（删除恒显 + 发布非 published + 停用 published）
       按 data-model §4；spec 010 操作列加 查看 / 编辑 入口（FR-001/002，四链接
       平铺不收纳）；错误提示一律由 request.ts 拦截器承担，页面只管成功后的刷新。 -->
  <div>
    <PageHeader
      title="工作流管理"
      description="创建、发布与管理工作流——JSON 配置或可视化拖拽编排"
    >
      <template #actions>
        <el-button type="primary" @click="router.push('/workflows/create')">
          <el-icon><Plus /></el-icon>
          新建工作流
        </el-button>
      </template>
    </PageHeader>

    <HifyTable ref="tableRef" :columns="columns" :api="getWorkflowList">
      <template #type="{ row }">
        <el-tag :type="row.type === 'task' ? 'warning' : 'primary'" size="small">
          {{ row.type === 'task' ? '任务型' : '对话型' }}
        </el-tag>
      </template>
      <template #status="{ row }">
        <el-tag :type="STATUS_TAG[row.status]" size="small">
          {{ STATUS_TEXT[row.status] }}
        </el-tag>
      </template>
      <template #createdAt="{ row }">
        {{ formatDateTime(row.created_at) }}
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click="router.push(`/workflows/${row.id}`)">
          查看
        </el-button>
        <el-button
          link
          type="primary"
          @click="router.push(`/workflows/${row.id}/edit`)"
        >
          编辑
        </el-button>
        <el-button
          v-if="row.status !== 'published'"
          link
          type="primary"
          @click="publish(row)"
        >
          发布
        </el-button>
        <el-button
          v-else
          link
          type="warning"
          @click="disable(row)"
        >
          停用
        </el-button>
        <el-button link type="danger" @click="remove(row)">删除</el-button>
      </template>
    </HifyTable>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import HifyTable, { type HifyTableColumn } from '@/components/HifyTable.vue'
import { BREAKPOINTS } from '@/composables/useBreakpoint'
import { useConfirm } from '@/composables/useConfirm'
import { notifySuccess } from '@/utils/notify'
import {
  deleteWorkflow,
  disableWorkflow,
  getWorkflowList,
  publishWorkflow,
  type WorkflowItem,
} from '@/api/workflow'

const router = useRouter()
const tableRef = ref<{ refresh: () => void }>()

// 状态三态映射（FR-003：后端返回 draft/published/disabled 小写）
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

const columns: HifyTableColumn[] = [
  { label: '名称', prop: 'name' },
  { label: '类型', slot: 'type', width: 90 },
  { label: '状态', slot: 'status', width: 90 },
  // 次要列：窄屏（≤992）隐藏，保留 名称 / 类型 / 状态 / 操作 关键信息
  { label: '创建时间', slot: 'createdAt', width: 150, hideBelow: BREAKPOINTS.md },
  { label: '操作', slot: 'actions', width: 220, align: 'right' },
]

// ---- 删除（FR-004）：useConfirm 一行全流程；409 WORKFLOW_IN_USE 由拦截器弹 ----

function remove(row: WorkflowItem): void {
  void useConfirm({
    message: `删除工作流「${row.name}」？将永久删除其配置与节点；被 Agent 绑定时无法删除（可先解绑或改为停用）。`,
    api: () => deleteWorkflow(row.id),
    successText: '删除成功',
  }).then((ok) => {
    if (ok) tableRef.value?.refresh()
  })
}

// ---- 发布 / 停用（FR-014）：确认框 → 动作端点（幂等）→ 刷新表格 ----

async function publish(row: WorkflowItem): Promise<void> {
  const ok = await ElMessageBox.confirm(
    `发布工作流「${row.name}」？发布后绑定它的 Agent 会话即可正常执行。`,
    '发布确认',
    { confirmButtonText: '发布', cancelButtonText: '取消' },
  ).then(
    () => true,
    () => false, // 取消静默
  )
  if (!ok) return
  try {
    await publishWorkflow(row.id)
  } catch {
    return // 失败提示已由拦截器弹
  }
  notifySuccess('已发布')
  tableRef.value?.refresh()
}

async function disable(row: WorkflowItem): Promise<void> {
  const ok = await ElMessageBox.confirm(
    `停用工作流「${row.name}」？绑定该工作流的 Agent 会话将不可用。`,
    '停用确认',
    { type: 'warning', confirmButtonText: '停用', cancelButtonText: '取消' },
  ).then(
    () => true,
    () => false, // 取消静默
  )
  if (!ok) return
  try {
    await disableWorkflow(row.id)
  } catch {
    return // 失败提示已由拦截器弹
  }
  notifySuccess('已停用')
  tableRef.value?.refresh()
}

// ---- 时间展示：RFC 3339 → YYYY-MM-DD HH:mm（本地时区） ----

function formatDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number): string => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
    d.getHours(),
  )}:${pad(d.getMinutes())}`
}
</script>
