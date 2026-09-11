<template>
  <!-- 文档管理页：KB 名称 + 返回按钮 + 上传 + 文档列表 + 轮询。
       不用 HifyTable（游标分页 + 轮询逻辑不同于偏移分页）；自定义 el-table + 手写分页。 -->
  <div>
    <PageHeader :title="kbName || '文档管理'" :description="kbDescription">
      <template #actions>
        <el-button @click="goBack">
          <el-icon><ArrowLeft /></el-icon>
          返回知识库列表
        </el-button>
        <el-upload
          ref="uploadRef"
          :auto-upload="false"
          :show-file-list="false"
          :accept="ACCEPT_EXTS"
          :before-upload="beforeUpload"
          :on-change="onFileChange"
        >
          <template #trigger>
            <el-button type="primary">
              <el-icon><Upload /></el-icon>
              上传文档
            </el-button>
          </template>
        </el-upload>
      </template>
    </PageHeader>

    <el-card class="doc-table">
      <el-table v-loading="loading" :data="rows">
        <el-table-column label="名称" prop="name" min-width="200" />
        <el-table-column label="类型" width="80">
          <template #default="{ row }">
            <el-tag size="small">{{ row.file_type.toUpperCase() }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="大小" width="90">
          <template #default="{ row }">
            {{ formatFileSize(row.file_size) }}
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="STATUS_TAG[row.status]" size="small">
              {{ STATUS_LABEL[row.status] }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="分块数" width="80" prop="chunk_count" />
        <el-table-column label="错误信息" min-width="160" hide-below="992">
          <template #default="{ row }">
            <span v-if="row.status === 'failed' && row.error_message" class="error-text">
              {{ row.error_message }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="150">
          <template #default="{ row }">
            {{ formatDateTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" align="right">
          <template #default="{ row }">
            <el-button
              link
              type="primary"
              :disabled="row.status === 'pending' || row.status === 'processing'"
              @click="reindex(row as DocumentItem)"
            >
              重建索引
            </el-button>
            <el-button link type="danger" @click="remove(row as DocumentItem)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无文档，点击上方「上传文档」添加" :image-size="72" />
        </template>
      </el-table>
      <div v-if="hasMore" class="doc-table__footer">
        <el-button @click="loadMore" :loading="loadingMore">加载更多</el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Upload } from '@element-plus/icons-vue'
import type { UploadFile } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import { BREAKPOINTS } from '@/composables/useBreakpoint'
import { useConfirm } from '@/composables/useConfirm'
import { notifyError, notifySuccess } from '@/utils/notify'
import {
  deleteDocument,
  getDocumentList,
  getKnowledgeBase,
  reindexDocument,
  uploadDocument,
  type DocumentItem,
} from '@/api/rag'

const route = useRoute()
const router = useRouter()
const kbId = computed(() => route.params.kbId as string)

// ---- KB 元信息（标题 + 描述） ----

const kbName = ref('')
const kbDescription = ref('')

async function loadKBInfo(): Promise<void> {
  try {
    const kb = await getKnowledgeBase(kbId.value)
    kbName.value = kb.name
    kbDescription.value = kb.description
  } catch {
    // 拦截器已提示
  }
}

// ---- 返回按钮 ----

function goBack(): void {
  void router.push({ name: 'rag-kbs' })
}

// ---- 上传 ----

const ACCEPT_EXTS = '.txt,.md'
const uploadRef = ref()

function beforeUpload(file: File): boolean {
  // 扩展名校验（白名单对齐后端 AllowedUploadExts）
  const ext = '.' + file.name.split('.').pop()?.toLowerCase()
  if (!ACCEPT_EXTS.includes(ext)) {
    notifyError('仅支持 .txt 和 .md 文件')
    return false
  }
  // 大小校验（前端提示，后端也有兜底）
  const MAX_SIZE = 2 * 1024 * 1024 // 2MB，与后端 RAG_MAX_UPLOAD_BYTES 默认值对齐
  if (file.size > MAX_SIZE) {
    notifyError('文件大小不能超过 2MB')
    return false
  }
  return true
}

async function onFileChange(uploadFile: UploadFile): Promise<void> {
  if (!uploadFile.raw) return
  if (!beforeUpload(uploadFile.raw)) return
  try {
    await uploadDocument(kbId.value, uploadFile.raw, uploadFile.name)
    notifySuccess('上传成功，正在入库处理中')
    // 刷新列表（新文档 pending 状态），触发轮询
    await loadInitial()
  } catch {
    // 拦截器已提示
  }
}

// ---- 文档列表（游标分页 + 轮询） ----

const rows = ref<DocumentItem[]>([])
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
let cursor = ''

async function loadInitial(): Promise<void> {
  loading.value = true
  try {
    const res = await getDocumentList(kbId.value, { limit: 20 })
    rows.value = res.list
    hasMore.value = res.hasMore
    cursor = res.nextCursor ?? ''
  } catch {
    rows.value = []
    hasMore.value = false
  } finally {
    loading.value = false
  }
}

async function loadMore(): Promise<void> {
  loadingMore.value = true
  try {
    const res = await getDocumentList(kbId.value, { limit: 20, cursor })
    rows.value = [...rows.value, ...res.list]
    hasMore.value = res.hasMore
    cursor = res.nextCursor ?? ''
  } catch {
    // 拦截器已提示
  } finally {
    loadingMore.value = false
  }
}

// ---- 轮询（仅当存在 processing/pending 文档时轮询，每 3s 刷新） ----

let pollTimer: ReturnType<typeof setInterval> | null = null

const hasProcessing = computed(() =>
  rows.value.some((d) => d.status === 'pending' || d.status === 'processing'),
)

function startPoll(): void {
  if (pollTimer) return
  pollTimer = setInterval(() => {
    void loadInitial()
  }, 3000)
}

function stopPoll(): void {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

watch(hasProcessing, (processing) => {
  if (processing) {
    startPoll()
  } else {
    stopPoll()
  }
})

onMounted(() => {
  void loadKBInfo()
  void loadInitial()
})

onUnmounted(() => {
  stopPoll()
})

// ---- 重建索引 ----

async function reindex(row: DocumentItem): Promise<void> {
  try {
    await reindexDocument(row.id)
    notifySuccess('已重新提交入库')
    await loadInitial()
  } catch {
    // 拦截器已提示
  }
}

// ---- 删除 ----

function remove(row: DocumentItem): void {
  void useConfirm({
    message: `删除文档「${row.name}」？将同时删除其所有分块，此操作不可恢复。`,
    api: () => deleteDocument(row.id),
  }).then((ok) => {
    if (ok) void loadInitial()
  })
}

// ---- 格式化工具 ----

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function formatDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number): string => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
    d.getHours(),
  )}:${pad(d.getMinutes())}`
}

// ---- 状态展示 ----

type TagType = 'success' | 'warning' | 'danger' | 'info'
const STATUS_TAG: Record<string, TagType> = {
  pending: 'info',
  processing: 'warning',
  ready: 'success',
  failed: 'danger',
}
const STATUS_LABEL: Record<string, string> = {
  pending: '等待处理',
  processing: '处理中',
  ready: '已完成',
  failed: '处理失败',
}
</script>

<style scoped>
.doc-table :deep(.el-card__body) {
  padding: 0;
}

.doc-table__footer {
  display: flex;
  justify-content: center;
  padding: var(--hf-space-3) var(--hf-space-5);
}

.error-text {
  color: var(--hf-danger);
  font-size: 12px;
}
</style>
