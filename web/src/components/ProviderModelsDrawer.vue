<template>
  <!-- 提供商模型列表抽屉：子资源标准形态（不增路由）。
       mock 数据源组件内自持；「同步模型」mock 真实语义 =
       GET /providers/{id}/models 探测（按类型预置，合并去重）。 -->
  <el-drawer v-model="visible" :title="title" size="520px" class="models-drawer">
    <div class="models-drawer__toolbar">
      <el-button type="primary" :loading="syncing" @click="sync">
        同步模型
      </el-button>
      <el-button @click="dialogRef?.open()">手动新增</el-button>
    </div>

    <HifyTable
      ref="tableRef"
      :columns="columns"
      :api="fetchModels"
      :pagination="false"
    >
      <template #kind="{ row }">
        <el-tag :type="row.kind === 'chat' ? 'primary' : 'info'" size="small">
          {{ row.kind === 'chat' ? '对话' : '嵌入' }}
        </el-tag>
      </template>
      <template #context="{ row }">
        {{ row.contextK === null ? '—' : `${row.contextK}K` }}
      </template>
      <template #actions="{ row }">
        <el-button link type="danger" @click="remove(row)">删除</el-button>
      </template>
    </HifyTable>

    <HifyFormDialog
      ref="dialogRef"
      v-model="dialogVisible"
      title="模型"
      :initial-model="emptyModel"
      :rules="rules"
      @submit="onSubmit"
    >
      <template #default="{ form }">
        <el-form-item label="模型 ID" prop="modelId">
          <el-input v-model="form.modelId" placeholder="如 gpt-4o" />
        </el-form-item>
        <el-form-item label="能力类型" prop="kind">
          <el-select v-model="form.kind" style="width: 100%">
            <el-option label="对话（chat）" value="chat" />
            <el-option label="嵌入（embedding）" value="embedding" />
          </el-select>
        </el-form-item>
        <el-form-item label="上下文长度" prop="contextK">
          <el-input-number
            v-model="form.contextK"
            :min="1"
            :max="10000"
            placeholder="K tokens，可空"
            style="width: 100%"
          />
        </el-form-item>
      </template>
    </HifyFormDialog>
  </el-drawer>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import type { FormRules } from 'element-plus'
import HifyTable, { type HifyTableColumn } from '@/components/HifyTable.vue'
import HifyFormDialog from '@/components/HifyFormDialog.vue'
import { useConfirm } from '@/composables/useConfirm'
import { notifySuccess, notifyWarning } from '@/utils/notify'
import type { PageQuery, PageResult, ProviderType } from '@/types'

/** 抽屉打开入参：provider 行的最小子集（ProviderList 的行类型结构化满足） */
export interface ProviderRef {
  id: number
  name: string
  type: ProviderType
}

type ModelKind = 'chat' | 'embedding'

interface ModelRow {
  id: number
  modelId: string
  kind: ModelKind
  contextK: number | null
}

interface ModelForm {
  modelId: string
  kind: ModelKind
  contextK?: number
}

// 按类型预置清单（mock 探测结果；Claude 无 embedding 是真实差异）
const PRESETS: Record<ProviderType, Array<Omit<ModelRow, 'id'>>> = {
  OpenAI: [
    { modelId: 'gpt-4o', kind: 'chat', contextK: 128 },
    { modelId: 'gpt-4o-mini', kind: 'chat', contextK: 128 },
    { modelId: 'text-embedding-3-small', kind: 'embedding', contextK: 8 },
  ],
  Claude: [
    { modelId: 'claude-sonnet-4-5', kind: 'chat', contextK: 200 },
    { modelId: 'claude-haiku-4-5', kind: 'chat', contextK: 200 },
  ],
  Gemini: [
    { modelId: 'gemini-2.0-flash', kind: 'chat', contextK: 1000 },
    { modelId: 'text-embedding-004', kind: 'embedding', contextK: 2 },
  ],
  Ollama: [
    { modelId: 'llama3.1', kind: 'chat', contextK: 128 },
    { modelId: 'qwen2.5', kind: 'chat', contextK: 32 },
    { modelId: 'nomic-embed-text', kind: 'embedding', contextK: 8 },
  ],
}

// ---- mock 模型库：种子数据（OpenAI 2 条 / Ollama 1 条，其余空 → 演示空态 + 同步） ----
const db = ref<Record<number, ModelRow[]>>({
  1: [
    { id: 1, modelId: 'gpt-4o', kind: 'chat', contextK: 128 },
    { id: 2, modelId: 'text-embedding-3-small', kind: 'embedding', contextK: 8 },
  ],
  4: [{ id: 3, modelId: 'llama3.1', kind: 'chat', contextK: 128 }],
})
let nextId = 100

const visible = ref(false)
const current = ref<ProviderRef | null>(null)
const title = computed(() => (current.value ? `${current.value.name} · 模型` : '模型'))

const tableRef = ref<{ refresh: () => void }>()

/** 打开抽屉：换提供商后强制刷新（表格已挂载时 onMounted 不会再跑） */
function open(provider: ProviderRef): void {
  current.value = provider
  visible.value = true
  void nextTick(() => tableRef.value?.refresh())
}

function fetchModels(q: PageQuery): Promise<PageResult<ModelRow>> {
  const list = db.value[current.value?.id ?? -1] ?? []
  return new Promise((resolve) => {
    setTimeout(() => {
      resolve({ list, total: list.length, page: q.page ?? 1, pageSize: q.page_size ?? 20 })
    }, 300)
  })
}

// ---- 同步：按类型预置合并去重 ----
const syncing = ref(false)

function sync(): void {
  // 闭包外捕获：setTimeout 内 current.value 的 null 收窄会失效
  const provider = current.value
  if (!provider) return
  syncing.value = true
  setTimeout(() => {
    const providerId = provider.id
    const existing = db.value[providerId] ?? []
    const added = PRESETS[provider.type].filter(
      (p) => !existing.some((e) => e.modelId === p.modelId),
    )
    db.value = {
      ...db.value,
      [providerId]: [...existing, ...added.map((p) => ({ ...p, id: nextId++ }))],
    }
    syncing.value = false
    notifySuccess(`同步完成，新增 ${added.length} 个模型`)
    tableRef.value?.refresh()
  }, 500)
}

// ---- 手动新增 ----
const dialogRef = ref<{ open: () => void }>()
const dialogVisible = ref(false)

const emptyModel = (): ModelForm => ({ modelId: '', kind: 'chat', contextK: undefined })

const rules: FormRules = {
  modelId: [{ required: true, message: '请输入模型 ID', trigger: 'blur' }],
  kind: [{ required: true, message: '请选择能力类型', trigger: 'change' }],
}

function onSubmit(form: ModelForm, done: (ok?: boolean) => void): void {
  if (!current.value) return
  const providerId = current.value.id
  const existing = db.value[providerId] ?? []
  // 同提供商下模型 ID 唯一（对齐后端 (provider_id, model_id) 唯一约束语义）
  if (existing.some((m) => m.modelId === form.modelId)) {
    notifyWarning('模型已存在')
    done(false)
    return
  }
  setTimeout(() => {
    db.value = {
      ...db.value,
      [providerId]: [
        ...existing,
        {
          id: nextId++,
          modelId: form.modelId,
          kind: form.kind,
          contextK: form.contextK ?? null,
        },
      ],
    }
    notifySuccess('创建成功')
    done(true)
    tableRef.value?.refresh()
  }, 400)
}

// ---- 删除 ----
function remove(row: ModelRow): void {
  if (!current.value) return
  const providerId = current.value.id
  void useConfirm({
    message: `删除模型「${row.modelId}」？此操作不可恢复。`,
    api: () =>
      new Promise((resolve) => {
        setTimeout(() => {
          db.value = {
            ...db.value,
            [providerId]: (db.value[providerId] ?? []).filter((m) => m.id !== row.id),
          }
          resolve(true)
        }, 300)
      }),
  }).then((ok) => {
    if (ok) tableRef.value?.refresh()
  })
}

const columns: HifyTableColumn[] = [
  { label: '模型 ID', prop: 'modelId' },
  { label: '能力', slot: 'kind', width: 80 },
  { label: '上下文', slot: 'context', width: 90 },
  { label: '操作', slot: 'actions', width: 70, align: 'right' },
]

defineExpose({ open })
</script>

<style scoped>
.models-drawer__toolbar {
  display: flex;
  justify-content: flex-end;
  gap: var(--hf-space-2);
  margin-bottom: var(--hf-space-4);
}

/* 抽屉白底上不再叠卡片阴影，避免双层悬浮感 */
.models-drawer :deep(.el-card.is-always-shadow) {
  box-shadow: none;
}
</style>
