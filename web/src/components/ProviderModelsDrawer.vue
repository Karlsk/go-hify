<template>
  <!-- 提供商模型列表抽屉：子资源标准形态（不增路由），真实 API（api/provider.ts）。
       同步 = 后端自动发现（新条目 enabled=false，用启用开关放行）；启停开关走 PUT 全量回传。 -->
  <el-drawer v-model="visible" :title="title" size="520px" class="models-drawer">
    <div class="models-drawer__toolbar">
      <el-button type="primary" :loading="syncing" @click="sync">
        同步模型
      </el-button>
      <el-button @click="dialogRef?.open()">手动新增</el-button>
    </div>
    <!-- 交互提示：同步导入的模型默认停用，勾选列即启用（点击列表不改变） -->
    <p class="models-drawer__hint">勾选 = 启用；同步导入的模型默认停用</p>

    <HifyTable ref="tableRef" :columns="columns" :api="fetchModels">
      <template #capability="{ row }">
        <el-tag :type="row.capability === 'chat' ? 'primary' : 'info'" size="small">
          {{ row.capability === 'chat' ? '对话' : '嵌入' }}
        </el-tag>
      </template>
      <template #source="{ row }">
        <el-tag :type="row.source === 'manual' ? 'info' : 'success'" size="small">
          {{ row.source === 'manual' ? '手动' : '自动' }}
        </el-tag>
      </template>
      <template #context="{ row }">
        {{ row.context_window == null ? '—' : `${Math.round(row.context_window / 1024)}K` }}
      </template>
      <template #enabled="{ row }">
        <!-- 勾选 = 启用（同步导入的模型默认停用，勾选即调 PUT 启用）：
             v-model 先翻转视觉态；PUT 失败回滚（错误提示已由拦截器弹） -->
        <el-checkbox v-model="row.enabled" @change="toggleEnabled(row)" />
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
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="展示名，留空取模型 ID" />
        </el-form-item>
        <el-form-item label="能力类型" prop="capability">
          <el-select
            v-model="form.capability"
            style="width: 100%"
            @change="onCapabilityChange"
          >
            <el-option label="对话（chat）" value="chat" />
            <el-option label="嵌入（embedding）" value="embedding" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.capability === 'embedding'" label="嵌入维度" prop="embeddingDim">
          <el-input-number
            v-model="form.embeddingDim"
            :min="1"
            :max="32768"
            placeholder="如 1536"
            style="width: 100%"
          />
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
import { notifySuccess } from '@/utils/notify'
import type { PageQuery, PageResult } from '@/types'
import {
  createModel,
  deleteModel,
  getModelList,
  syncModels,
  updateModel,
  type ModelCapability,
  type ModelItem,
} from '@/api/provider'

/** 抽屉打开入参：provider 行的最小子集（ProviderList 的 ProviderItem 结构化满足） */
export interface ProviderRef {
  id: string
  name: string
}

interface ModelForm {
  modelId: string
  name: string
  capability: ModelCapability
  embeddingDim?: number
  contextK?: number
}

const visible = ref(false)
const current = ref<ProviderRef | null>(null)
const title = computed(() =>
  current.value ? `${current.value.name} · 可用模型` : '可用模型',
)

const tableRef = ref<{ refresh: () => void }>()

/** 打开抽屉：换提供商后强制刷新（表格已挂载时 onMounted 不会再跑） */
function open(provider: ProviderRef): void {
  current.value = provider
  formCapability.value = 'chat'
  visible.value = true
  void nextTick(() => tableRef.value?.refresh())
}

function fetchModels(q: PageQuery): Promise<PageResult<ModelItem>> {
  const provider = current.value
  if (!provider) {
    return Promise.resolve({ list: [], total: 0, page: 1, pageSize: 20 })
  }
  return getModelList(provider.id, q)
}

// ---- 同步：后端自动发现（只增改不删；上游不可用 503 由拦截器提示） ----

const syncing = ref(false)

async function sync(): Promise<void> {
  const provider = current.value
  if (!provider) return
  syncing.value = true
  try {
    const r = await syncModels(provider.id)
    notifySuccess(`同步完成：新增 ${r.added}，更新 ${r.updated}`)
    tableRef.value?.refresh()
  } catch {
    // 已由拦截器提示
  } finally {
    syncing.value = false
  }
}

// ---- 启停开关：PUT 全量回传（整行可选列不回传会被清空）；失败回滚视觉态 ----

async function toggleEnabled(row: ModelItem): Promise<void> {
  try {
    await updateModel(row.id, {
      provider_id: Number(row.provider_id), // body 的 provider_id 是数值（Go uint64 无 `,string` tag）
      name: row.name,
      model_id: row.model_id,
      capability: row.capability,
      context_window: row.context_window,
      max_output_tokens: row.max_output_tokens,
      input_price: row.input_price,
      output_price: row.output_price,
      embedding_dim: row.embedding_dim,
      enabled: row.enabled,
    })
  } catch {
    row.enabled = !row.enabled
  }
}

// ---- 手动新增 ----

const dialogRef = ref<{ open: () => void }>()
const dialogVisible = ref(false)
/** rules 需感知弹窗内的 capability（embedding 必填维度）：选择变化时同步镜像 */
const formCapability = ref<ModelCapability>('chat')

const emptyModel = (): ModelForm => ({
  modelId: '',
  name: '',
  capability: 'chat',
  embeddingDim: undefined,
  contextK: undefined,
})

function onCapabilityChange(capability: ModelCapability): void {
  formCapability.value = capability
}

const rules = computed<FormRules>(() => ({
  modelId: [{ required: true, message: '请输入模型 ID', trigger: 'blur' }],
  capability: [{ required: true, message: '请选择能力类型', trigger: 'change' }],
  // 后端 validateModel：embedding 必填维度，chat 不得携带
  embeddingDim:
    formCapability.value === 'embedding'
      ? [{ required: true, message: '嵌入模型必须提供维度', trigger: 'blur' }]
      : [],
}))

function onSubmit(form: ModelForm, done: (ok?: boolean) => void): void {
  const provider = current.value
  if (!provider) {
    done(false)
    return
  }
  void createModel({
    provider_id: Number(provider.id),
    name: form.name.trim() || form.modelId.trim(), // 后端 name 必填：留空取模型 ID
    model_id: form.modelId.trim(),
    capability: form.capability,
    context_window: form.contextK != null ? form.contextK * 1024 : undefined,
    // chat 不携带 embedding_dim（undefined 键不会序列化进 JSON body）
    embedding_dim:
      form.capability === 'embedding' && form.embeddingDim != null
        ? form.embeddingDim
        : undefined,
  })
    .then(() => {
      notifySuccess('创建成功')
      done(true)
      tableRef.value?.refresh()
    })
    .catch(() => done(false)) // 409 模型已存在等已由拦截器弹；保持弹窗打开
}

// ---- 删除：useConfirm 一行全流程（被 agents / 知识库引用时 409 由拦截器提示） ----

function remove(row: ModelItem): void {
  void useConfirm({
    message: `删除模型「${row.model_id}」？此操作不可恢复。`,
    api: () => deleteModel(row.id),
  }).then((ok) => {
    if (ok) tableRef.value?.refresh()
  })
}

const columns: HifyTableColumn[] = [
  { label: '模型 ID', prop: 'model_id' },
  { label: '能力', slot: 'capability', width: 70 },
  { label: '来源', slot: 'source', width: 70 },
  { label: '上下文', slot: 'context', width: 80 },
  { label: '启用', slot: 'enabled', width: 70 },
  { label: '操作', slot: 'actions', width: 60, align: 'right' },
]

defineExpose({ open })
</script>

<style scoped>
.models-drawer__toolbar {
  display: flex;
  justify-content: flex-end;
  gap: var(--hf-space-2);
}

.models-drawer__hint {
  margin: var(--hf-space-2) 0 var(--hf-space-4);
  font-size: var(--hf-font-size-sm);
  color: var(--hf-text-3);
}

/* 抽屉白底上不再叠卡片阴影，避免双层悬浮感 */
.models-drawer :deep(.el-card.is-always-shadow) {
  box-shadow: none;
}
</style>
