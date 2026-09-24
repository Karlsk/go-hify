<template>
  <!-- 运行历史区块（spec 015）：按工作流回看每次调用的记录——挂载拉首页 +
       加载更多（keyset 游标）+ 行点击开运行详情抽屉（US2）+ 刷新按钮（US3：
       试运行对话框零变化约束下的联动方案，Panel 自治重拉、不监听 TrialDialog）。 -->
  <el-card class="workflow-runs-panel">
    <template #header>
      <div class="workflow-runs-panel__header">
        <span>运行历史</span>
        <el-button size="small" :loading="loading" @click="refresh">刷新</el-button>
      </div>
      <p class="workflow-runs-panel__hint">
        运行记录受保留期约束，较早的记录可能已被定期清理；列表为空或缺少旧记录属正常现象。
      </p>
    </template>

    <!-- 空态（首次加载完成后仍无记录；保留期清理后的正常态，FR-004） -->
    <el-empty
      v-if="loaded && !loading && items.length === 0"
      description="暂无运行记录"
      :image-size="64"
    />

    <template v-else>
      <el-table
        v-loading="loading"
        :data="items"
        size="small"
        border
        class="workflow-runs-panel__list"
        @row-click="openDetail"
      >
        <el-table-column label="状态" width="80" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 'succeeded' ? 'success' : 'danger'" size="small">
              {{ row.status === 'succeeded' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="触发来源" width="100" align="center">
          <template #default="{ row }">{{ TRIGGER_TEXT[row.trigger_source] ?? row.trigger_source }}</template>
        </el-table-column>
        <el-table-column label="试运行" width="70" align="center">
          <template #default="{ row }">
            <el-tag v-if="row.is_trial" size="small" type="warning">试运行</el-tag>
            <template v-else>—</template>
          </template>
        </el-table-column>
        <el-table-column label="耗时" width="100" align="right">
          <template #default="{ row }">{{ formatDuration(row.duration_ms) }}</template>
        </el-table-column>
        <el-table-column label="失败节点" min-width="110">
          <template #default="{ row }">{{ row.error_node || '—' }}</template>
        </el-table-column>
        <el-table-column label="错误摘要" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">{{ row.error_msg || '—' }}</template>
        </el-table-column>
        <el-table-column label="调用时间" width="150">
          <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
        </el-table-column>
        <el-table-column label="运行编号" width="110">
          <template #default="{ row }">
            <span class="workflow-runs-panel__run-id">#{{ row.id }}</span>
          </template>
        </el-table-column>
      </el-table>

      <div v-if="hasMore" class="workflow-runs-panel__more">
        <el-button :loading="loading" size="small" @click="loadMore">加载更多</el-button>
      </div>
    </template>

    <!-- 运行详情抽屉（US2，契约 §2）：按需拉全量详情（列表摘要不含大文本）；
         错误态在抽屉内展示不弹全局 toast；404 = 记录刚被保留期清理 → 抽屉内空态、
         列表保持（FR-004）。 -->
    <el-drawer v-model="drawerVisible" :title="`运行 #${currentRunId}`" size="720px">
      <div v-loading="detailLoading" class="workflow-runs-panel__detail">
        <el-empty v-if="runNotFound" description="该运行记录已不存在" :image-size="64" />

        <div v-else-if="detailError" class="workflow-runs-panel__detail-error">
          <el-alert title="运行详情加载失败" type="error" :closable="false" show-icon />
          <el-button size="small" @click="loadDetail">重试</el-button>
        </div>

        <template v-else-if="runDetail">
          <el-descriptions :column="2" size="small" border>
            <el-descriptions-item label="状态">
              <el-tag :type="runDetail.status === 'succeeded' ? 'success' : 'danger'" size="small">
                {{ runDetail.status === 'succeeded' ? '成功' : '失败' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="触发来源">
              {{ TRIGGER_TEXT[runDetail.trigger_source] ?? runDetail.trigger_source }}
            </el-descriptions-item>
            <el-descriptions-item label="试运行">
              {{ runDetail.is_trial ? '是' : '否' }}
            </el-descriptions-item>
            <el-descriptions-item label="总耗时">
              {{ formatDuration(runDetail.duration_ms) }}
            </el-descriptions-item>
            <el-descriptions-item label="开始时间">
              {{ formatDateTime(runDetail.started_at) }}
            </el-descriptions-item>
            <el-descriptions-item label="记录时间">
              {{ formatDateTime(runDetail.created_at) }}
            </el-descriptions-item>
            <el-descriptions-item v-if="runDetail.parent_run_id" label="父运行编号">
              <span class="workflow-runs-panel__run-id">#{{ runDetail.parent_run_id }}</span>
            </el-descriptions-item>
            <el-descriptions-item
              v-if="runDetail.trigger_source === 'chat' && runDetail.conversation_id"
              label="关联会话 / 消息"
            >
              <span class="workflow-runs-panel__run-id">
                #{{ runDetail.conversation_id }} / #{{ runDetail.message_id ?? '—' }}
              </span>
            </el-descriptions-item>
          </el-descriptions>

          <!-- 失败面（仅 failed）：错误文案 + 失败节点（危险色） -->
          <template v-if="runDetail.status === 'failed'">
            <el-alert
              type="error"
              :title="runDetail.error_msg || '运行失败'"
              :closable="false"
              show-icon
            />
            <p v-if="runDetail.error_node" class="workflow-runs-panel__error-node">
              失败节点：{{ runDetail.error_node }}
            </p>
          </template>

          <!-- 输入 / 输出：pre-wrap 限高滚动；截断保真文本如实展示不加工（US2-3） -->
          <div class="workflow-runs-panel__io">
            <h4>输入</h4>
            <pre class="workflow-runs-panel__io-text">{{ runDetail.input || '—' }}</pre>
            <h4>输出</h4>
            <pre class="workflow-runs-panel__io-text">{{ runDetail.output || '—' }}</pre>
          </div>

          <!-- 节点轨迹（执行序 seq 升序；失败行 = node_key 命中 run 级 error_node） -->
          <h4 class="workflow-runs-panel__track-title">节点轨迹</h4>
          <el-table
            :data="runDetail.nodes"
            size="small"
            border
            :row-class-name="nodeRowClass"
            empty-text="该运行无节点轨迹记录"
          >
            <el-table-column label="序号" prop="seq" width="60" align="center" />
            <el-table-column label="节点 key" prop="node_key" min-width="110" show-overflow-tooltip />
            <el-table-column label="类型" prop="node_type" min-width="130" show-overflow-tooltip />
            <el-table-column label="状态" width="70" align="center">
              <template #default="{ row }">
                <el-tag :type="row.status === 'succeeded' ? 'success' : 'danger'" size="small">
                  {{ row.status === 'succeeded' ? '成功' : '失败' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="耗时" width="95" align="right">
              <template #default="{ row }">{{ formatDuration(row.duration_ms) }}</template>
            </el-table-column>
            <el-table-column label="输入" min-width="120" show-overflow-tooltip>
              <template #default="{ row }">{{ row.input || '—' }}</template>
            </el-table-column>
            <el-table-column label="输出" min-width="120" show-overflow-tooltip>
              <template #default="{ row }">{{ row.output || '—' }}</template>
            </el-table-column>
          </el-table>
        </template>
      </div>
    </el-drawer>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  getWorkflowRun,
  listWorkflowRuns,
  type NodeRunTrack,
  type RunDetail,
  type RunSummary,
} from '@/api/workflow'

const props = defineProps<{ workflowId: string }>()

/** 列表数据（首页 + 追加；keyset 游标，最新在前） */
const items = ref<RunSummary[]>([])
const hasMore = ref(false)
const nextCursor = ref<string | null>(null)
/** 请求中（首页 / 加载更多共用，防重复点击——spec 013 试运行同款纪律） */
const loading = ref(false)
/** 首次加载完成（区分「加载中」与「确认空态」） */
const loaded = ref(false)

/** 详情抽屉（US2）：点行按需拉全量详情——列表摘要不含大文本（FR-002） */
const drawerVisible = ref(false)
const currentRunId = ref('')
const runDetail = ref<RunDetail | null>(null)
const detailLoading = ref(false)
/** 拉取失败（非 404）：抽屉内错误态 + 重试，不弹全局 toast（契约交互细则） */
const detailError = ref(false)
/** 404 RUN_NOT_FOUND：记录刚被保留期清理 → 抽屉内空态、列表保持（FR-004） */
const runNotFound = ref(false)

// 触发来源中文映射（console 试运行 / chat 对话触发 / workflow 子工作流嵌套）
const TRIGGER_TEXT: Record<string, string> = {
  console: '控制台试运行',
  chat: '对话触发',
  workflow: '子工作流',
}

async function loadFirstPage(): Promise<void> {
  if (loading.value) return // 防重（挂载与手动刷新并发时只跑一次）
  loading.value = true
  try {
    const res = await listWorkflowRuns(props.workflowId)
    items.value = res.list
    hasMore.value = res.hasMore
    nextCursor.value = res.nextCursor
  } catch {
    // 拦截器已 toast；无数据可展示，保持空列表
  } finally {
    loading.value = false
    loaded.value = true
  }
}

// 刷新（US3）：自治重拉首页——试运行完成后点此即见新记录；items/游标整体重置回首页态
function refresh(): void {
  void loadFirstPage()
}

async function loadMore(): Promise<void> {
  if (loading.value || !hasMore.value || !nextCursor.value) return
  loading.value = true
  try {
    const res = await listWorkflowRuns(props.workflowId, { cursor: nextCursor.value })
    items.value = [...items.value, ...res.list]
    hasMore.value = res.hasMore
    nextCursor.value = res.nextCursor
  } catch {
    // 拦截器已 toast；保留已加载页，游标不前移（重试点加载更多续传）
  } finally {
    loading.value = false
  }
}

// 耗时：ms 千分位（「1,234 ms」契约形态）
function formatDuration(ms: number): string {
  return `${ms.toLocaleString('en-US')} ms`
}

// 时间展示：RFC 3339 → YYYY-MM-DD HH:mm（本地时区，详情页同款）
function formatDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number): string => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
    d.getHours(),
  )}:${pad(d.getMinutes())}`
}

function openDetail(row: RunSummary): void {
  currentRunId.value = row.id
  drawerVisible.value = true
  void loadDetail()
}

async function loadDetail(): Promise<void> {
  if (detailLoading.value || !currentRunId.value) return
  detailLoading.value = true
  detailError.value = false
  runNotFound.value = false
  try {
    // skipErrorToast：错误态在抽屉内展示，不弹全局 toast（auth.ts 同款静默先例）
    runDetail.value = await getWorkflowRun(props.workflowId, currentRunId.value, {
      skipErrorToast: true,
    })
  } catch (e: unknown) {
    // 404 走 axios HTTP 错误分支（skipErrorToast 生效）；信封 code 在 response.data 上，
    // 鸭子类型读取（request.ts 拦截器 reject 的是 axios error，非 RequestError）
    const code = (e as { response?: { data?: { error?: { code?: string } } } })?.response?.data
      ?.error?.code
    if (code === 'RUN_NOT_FOUND') {
      runNotFound.value = true
    } else {
      detailError.value = true
    }
  } finally {
    detailLoading.value = false
  }
}

// 轨迹行高亮（契约 §2）：node_key 命中 run 级 error_node 且该节点 failed → 危险色背景
function nodeRowClass({ row }: { row: NodeRunTrack }): string {
  if (runDetail.value && row.node_key === runDetail.value.error_node && row.status === 'failed') {
    return 'workflow-runs-panel__fail-row'
  }
  return ''
}

onMounted(() => {
  void loadFirstPage()
})
</script>

<style scoped>
.workflow-runs-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}

.workflow-runs-panel__hint {
  margin: var(--hf-space-1) 0 0;
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-sm);
  font-weight: 400;
}

.workflow-runs-panel__run-id {
  font-family: var(--hf-font-mono);
}

.workflow-runs-panel__more {
  margin-top: var(--hf-space-2);
  text-align: center;
}

/* 列表行可点击（开详情抽屉） */
.workflow-runs-panel__list :deep(.el-table__row) {
  cursor: pointer;
}

/* 详情抽屉错误态 */
.workflow-runs-panel__detail-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--hf-space-2);
}

.workflow-runs-panel__error-node {
  margin: var(--hf-space-1) 0 0;
  color: var(--el-color-danger);
  font-size: var(--hf-font-size-sm);
}

/* 输入 / 输出与轨迹标题 */
.workflow-runs-panel__io h4,
.workflow-runs-panel__track-title {
  margin: var(--hf-space-2) 0 var(--hf-space-1);
  font-size: var(--hf-font-size-sm);
  font-weight: 600;
}

/* 大文本：pre-wrap 限高滚动，等宽字体 */
.workflow-runs-panel__io-text {
  max-height: 160px;
  margin: 0;
  padding: var(--hf-space-2);
  overflow: auto;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
  background: var(--el-fill-color-light);
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-sm);
  white-space: pre-wrap;
  word-break: break-all;
}

/* 失败节点行高亮：EP 行背景变量覆盖为 danger 浅底 */
.workflow-runs-panel__detail :deep(.workflow-runs-panel__fail-row) {
  --el-table-tr-bg-color: var(--el-color-danger-light-9);
}
</style>
