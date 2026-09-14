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
        <el-button @click="openRetrieve">
          <el-icon><Search /></el-icon>
          测试检索
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
        <el-table-column label="名称" min-width="200">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row as DocumentItem)">
              {{ row.name }}
            </el-button>
          </template>
        </el-table-column>
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
        <el-table-column label="启用" width="80">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
              {{ row.enabled ? '启用' : '停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="150">
          <template #default="{ row }">
            {{ formatDateTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-dropdown
              trigger="click"
              popper-class="doc-actions-popper"
              @command="(cmd: string) => onAction(cmd, row as DocumentItem)"
            >
              <el-button link type="primary">
                操作<el-icon class="el-icon--right"><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item
                    command="reindex"
                    :disabled="row.status === 'pending' || row.status === 'processing' || !row.enabled"
                  >
                    重建索引
                  </el-dropdown-item>
                  <el-dropdown-item
                    v-if="row.enabled"
                    command="disable"
                    :disabled="row.status === 'pending' || row.status === 'processing'"
                  >
                    停用
                  </el-dropdown-item>
                  <el-dropdown-item v-else command="enable">启用</el-dropdown-item>
                  <el-dropdown-item command="delete" divided class="doc-actions__delete">
                    删除
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
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

    <!-- 文档详情弹窗：查看原文 + 分块信息 -->
    <el-dialog v-model="detailVisible" title="文档详情" width="720px" top="5vh">
      <div v-loading="detailLoading">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="文件名">{{ detailDoc?.name }}</el-descriptions-item>
          <el-descriptions-item label="类型">
            <el-tag size="small">{{ detailDoc?.file_type?.toUpperCase() }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="大小">{{ formatFileSize(detailDoc?.file_size ?? 0) }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="STATUS_TAG[detailDoc?.status ?? '']" size="small">
              {{ STATUS_LABEL[detailDoc?.status ?? ''] }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="启用状态">
            <el-tag :type="detailDoc?.enabled ? 'success' : 'info'" size="small">
              {{ detailDoc?.enabled ? '已启用' : '已停用（向量已删，内容保留）' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="分块数">{{ detailDoc?.chunk_count }}</el-descriptions-item>
          <el-descriptions-item label="创建时间">{{ formatDateTime(detailDoc?.created_at ?? '') }}</el-descriptions-item>
          <el-descriptions-item label="错误信息" :span="2">
            <span
              v-if="detailDoc?.status === 'failed' && detailDoc?.error_message"
              class="error-text"
            >
              {{ detailDoc.error_message }}
            </span>
            <span v-else>—</span>
          </el-descriptions-item>
        </el-descriptions>
        <div class="detail-content">
          <h4>文档原文</h4>
          <el-input
            :model-value="detailDoc?.content"
            type="textarea"
            :rows="12"
            readonly
            placeholder="暂无内容"
          />
        </div>
      </div>
      <template #footer>
        <el-button @click="detailVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- 检索测试弹窗 -->
    <el-dialog v-model="retrieveVisible" title="检索测试" width="720px" top="5vh">
      <el-form :model="retrieveForm" label-width="80px">
        <el-form-item label="查询内容">
          <el-input
            v-model="retrieveForm.query"
            type="textarea"
            :rows="3"
            placeholder="输入检索内容，如：产品功能介绍"
          />
        </el-form-item>
        <el-form-item label="返回条数">
          <el-input-number v-model="retrieveForm.topK" :min="1" :max="20" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="retrieveVisible = false">取消</el-button>
        <el-button type="primary" :loading="retrieveLoading" @click="doRetrieve">
          检索
        </el-button>
      </template>
      <!-- 检索结果 -->
      <div v-if="retrieveResults.length > 0" class="retrieve-results">
        <h4>检索结果（{{ retrieveResults.length }} 条）</h4>
        <div v-for="(item, idx) in retrieveResults" :key="idx" class="retrieve-item">
          <div class="retrieve-item__header">
            <span class="retrieve-item__doc">{{ item.document_name || '未知文档' }}</span>
            <span class="retrieve-item__idx">分块 #{{ item.chunk_index }}</span>
            <span class="retrieve-item__similarity">相似度: {{ (item.similarity * 100).toFixed(1) }}%</span>
          </div>
          <div class="retrieve-item__content">{{ item.content }}</div>
        </div>
      </div>
      <el-empty v-else-if="retrieveSearched && !retrieveLoading" description="未找到匹配内容" />
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowDown, ArrowLeft, Search, Upload } from '@element-plus/icons-vue'
import type { UploadFile } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import { useConfirm } from '@/composables/useConfirm'
import { notifyError, notifySuccess } from '@/utils/notify'
import {
  deleteDocument,
  disableDocument,
  enableDocument,
  getDocument,
  getDocumentList,
  getKnowledgeBase,
  reindexDocument,
  retrieveChunks,
  uploadDocument,
  type DocumentDetail,
  type DocumentItem,
  type RetrievedChunk,
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

// ---- 停用 / 启用（深度停用：删向量保内容；启用自动重建索引） ----

function disableDoc(row: DocumentItem): void {
  void useConfirm({
    message: `停用文档「${row.name}」？将删除其全部向量分块（检索不再命中），重新启用时自动重建索引。`,
    api: () => disableDocument(row.id),
  }).then((ok) => {
    if (ok) void loadInitial()
  })
}

function enableDoc(row: DocumentItem): void {
  void useConfirm({
    message: `启用文档「${row.name}」？将自动重建索引（消耗嵌入费用）。`,
    api: () => enableDocument(row.id),
  }).then((ok) => {
    if (ok) {
      notifySuccess('已启用，正在重建索引')
      void loadInitial()
    }
  })
}

// ---- 删除 ----

function remove(row: DocumentItem): void {
  void useConfirm({
    message: `删除文档「${row.name}」？文档与其全部分块将被永久删除，此操作不可恢复（停用可保留内容）。`,
    api: () => deleteDocument(row.id),
  }).then((ok) => {
    if (ok) void loadInitial()
  })
}

/** 操作下拉分发：command → 行操作（禁用条件同原按钮组，见模板） */
function onAction(cmd: string, row: DocumentItem): void {
  switch (cmd) {
    case 'reindex':
      void reindex(row)
      break
    case 'disable':
      disableDoc(row)
      break
    case 'enable':
      enableDoc(row)
      break
    case 'delete':
      remove(row)
      break
  }
}

// ---- 文档详情弹窗 ----

const detailVisible = ref(false)
const detailLoading = ref(false)
const detailDoc = ref<DocumentDetail | null>(null)

async function openDetail(row: DocumentItem): Promise<void> {
  detailVisible.value = true
  detailLoading.value = true
  try {
    detailDoc.value = await getDocument(row.id)
  } catch {
    // 拦截器已提示
    detailDoc.value = null
  } finally {
    detailLoading.value = false
  }
}

// ---- 检索测试弹窗 ----

const retrieveVisible = ref(false)
const retrieveLoading = ref(false)
const retrieveSearched = ref(false)
const retrieveResults = ref<RetrievedChunk[]>([])
const retrieveForm = ref({ query: '', topK: 5 })

function openRetrieve(): void {
  retrieveVisible.value = true
  retrieveForm.value = { query: '', topK: 5 }
  retrieveResults.value = []
  retrieveSearched.value = false
}

async function doRetrieve(): Promise<void> {
  if (!retrieveForm.value.query.trim()) {
    notifyError('请输入检索内容')
    return
  }
  retrieveLoading.value = true
  retrieveSearched.value = true
  try {
    retrieveResults.value = await retrieveChunks(kbId.value, {
      query: retrieveForm.value.query,
      top_k: retrieveForm.value.topK,
    })
  } catch {
    // 拦截器已提示
    retrieveResults.value = []
  } finally {
    retrieveLoading.value = false
  }
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

/* 文档详情弹窗 */
.detail-content {
  margin-top: var(--hf-space-4);
}

.detail-content h4 {
  margin: 0 0 var(--hf-space-2);
  font-size: var(--hf-font-size-base);
  font-weight: var(--hf-font-weight-semibold);
  color: var(--hf-text-1);
}

/* 检索测试弹窗 */
.retrieve-results {
  margin-top: var(--hf-space-4);
  border-top: 1px solid var(--hf-border-2);
  padding-top: var(--hf-space-4);
}

.retrieve-results h4 {
  margin: 0 0 var(--hf-space-3);
  font-size: var(--hf-font-size-base);
  font-weight: var(--hf-font-weight-semibold);
  color: var(--hf-text-1);
}

.retrieve-item {
  padding: var(--hf-space-3);
  margin-bottom: var(--hf-space-2);
  background: var(--hf-bg-page);
  border-radius: var(--hf-radius-sm);
  border: 1px solid var(--hf-border-2);
}

.retrieve-item__header {
  display: flex;
  align-items: center;
  gap: var(--hf-space-2);
  margin-bottom: var(--hf-space-2);
  font-size: 12px;
  color: var(--hf-text-2);
}

.retrieve-item__doc {
  font-weight: var(--hf-font-weight-semibold);
  color: var(--hf-text-1);
}

.retrieve-item__similarity {
  margin-left: auto;
  color: var(--hf-primary);
  font-weight: var(--hf-font-weight-medium);
}

.retrieve-item__content {
  font-size: var(--hf-font-size-sm);
  line-height: var(--hf-leading-normal);
  color: var(--hf-text-2);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 120px;
  overflow-y: auto;
}
</style>

<style>
/* 操作下拉菜单 teleport 到 body，scoped 选择器够不着——经 popper-class 命中；删除项标红对齐原 danger 按钮 */
.doc-actions-popper .doc-actions__delete {
  color: var(--hf-danger);
}
</style>
