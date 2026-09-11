<template>
  <!-- 知识库列表页：HifyTable + HifyFormDialog + useConfirm + 真实 API（api/rag.ts）。
       嵌入模型选择器按供应商分组（AgentList 同款）；名称列点击跳文档管理页。 -->
  <div>
    <PageHeader
      title="知识库管理"
      description="管理知识库与文档，构建 Agent 的知识上下文"
    >
      <template #actions>
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon>
          新增知识库
        </el-button>
      </template>
    </PageHeader>

    <HifyTable ref="tableRef" :columns="columns" :api="getKnowledgeBaseList">
      <template #name="{ row }">
        <el-button link type="primary" @click="goDocuments(row)">
          {{ row.name }}
        </el-button>
      </template>
      <template #embeddingModel="{ row }">
        <!-- embedding_model_name 空串 = 悬空引用（模型已被删），fallback 显示 id -->
        <span :class="{ 'model-dangling': !row.embedding_model_name }">
          {{ row.embedding_model_name || `#${row.embedding_model_id}` }}
        </span>
      </template>
      <template #enabled="{ row }">
        <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
          {{ row.enabled ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #createdAt="{ row }">
        {{ formatDateTime(row.created_at) }}
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
        <el-button link type="danger" @click="remove(row)">删除</el-button>
      </template>
    </HifyTable>

    <HifyFormDialog
      ref="dialogRef"
      v-model="dialogVisible"
      title="知识库"
      width="640px"
      :initial-model="emptyForm"
      :rules="rules"
      @submit="onSubmit"
    >
      <template #default="{ form }">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="如 产品文档库 / 技术 Wiki" />
        </el-form-item>
        <el-form-item label="描述" prop="description">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="3"
            placeholder="用途说明（可选）"
          />
        </el-form-item>
        <el-form-item label="嵌入模型" prop="embeddingModelId">
          <!-- 创建时必选；编辑时禁用（维度冻结，换模型=重建 chunks） -->
          <el-select
            v-model="form.embeddingModelId"
            :disabled="isEdit"
            :loading="modelLoading"
            :placeholder="isEdit ? '创建后不可更改' : '选择启用的嵌入模型'"
            style="width: 100%"
          >
            <el-option-group
              v-for="group in modelGroups"
              :key="group.name"
              :label="group.name"
            >
              <el-option
                v-for="m in group.models"
                :key="m.id"
                :label="`${m.name}（${m.embedding_dim}d）`"
                :value="m.id"
              />
            </el-option-group>
          </el-select>
          <el-text v-if="isEdit" size="small" type="info">
            嵌入模型在创建时确定，后续不可更改；如需更换请新建知识库
          </el-text>
        </el-form-item>
        <!-- PUT 全量提交含 enabled：不处理的话任何编辑都会把 KB 停用（Go bool 零值） -->
        <el-form-item v-if="isEdit" label="启用" prop="enabled">
          <el-switch v-model="form.enabled" />
        </el-form-item>
        <!-- 切分策略配置（高级选项，默认折叠） -->
        <el-divider content-position="left">切分策略（高级）</el-divider>
        <el-form-item label="策略类型">
          <el-select v-model="form.chunkStrategy.type" :disabled="isEdit" style="width: 100%">
            <el-option label="固定长度（段落→句子→硬截三级降级）" value="fixed_length" />
          </el-select>
          <el-text v-if="isEdit" size="small" type="info">
            策略类型在创建时确定，后续不可更改
          </el-text>
        </el-form-item>
        <el-form-item label="块大小（rune）">
          <el-input-number
            v-model="form.chunkStrategy.chunk_size"
            :disabled="isEdit"
            :min="100"
            :max="2000"
            :step="50"
          />
          <el-text size="small" type="info">目标块大小，0=使用全局默认（500）</el-text>
        </el-form-item>
        <el-form-item label="重叠大小（rune）">
          <el-input-number
            v-model="form.chunkStrategy.chunk_overlap"
            :disabled="isEdit"
            :min="0"
            :max="form.chunkStrategy.chunk_size > 0 ? form.chunkStrategy.chunk_size - 1 : 499"
            :step="10"
          />
          <el-text size="small" type="info">块间重叠，0=使用全局默认（80）</el-text>
        </el-form-item>
        <el-form-item label="段落分隔符">
          <el-select v-model="form.chunkStrategy.separator" :disabled="isEdit" style="width: 100%">
            <el-option label="双换行（\\n\\n）——默认" value="\n\n" />
            <el-option label="单换行（\\n）" value="\n" />
            <el-option label="空格" value=" " />
            <el-option label="无（纯硬截）" value="" />
          </el-select>
          <el-text size="small" type="info">影响段落切分的首优先级</el-text>
        </el-form-item>
      </template>
    </HifyFormDialog>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import type { FormRules } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import HifyTable, { type HifyTableColumn } from '@/components/HifyTable.vue'
import HifyFormDialog from '@/components/HifyFormDialog.vue'
import { BREAKPOINTS } from '@/composables/useBreakpoint'
import { useConfirm } from '@/composables/useConfirm'
import { notifySuccess } from '@/utils/notify'
import {
  createKnowledgeBase,
  deleteKnowledgeBase,
  getKnowledgeBaseList,
  updateKnowledgeBase,
  type KnowledgeBaseItem,
} from '@/api/rag'
import { getModelList, getProviderList, type ModelItem } from '@/api/provider'

const router = useRouter()

// ---- 表格 ----

const tableRef = ref<{ refresh: () => void }>()
const columns: HifyTableColumn[] = [
  { label: '名称', slot: 'name' },
  { label: '描述', prop: 'description', hideBelow: BREAKPOINTS.md },
  { label: '嵌入模型', slot: 'embeddingModel', width: 160 },
  { label: '文档数', prop: 'document_count', width: 80 },
  { label: '状态', slot: 'enabled', width: 80 },
  { label: '创建时间', slot: 'createdAt', width: 150, hideBelow: BREAKPOINTS.md },
  { label: '操作', slot: 'actions', width: 130, align: 'right' },
]

// ---- 名称列点击跳文档管理页 ----

function goDocuments(row: KnowledgeBaseItem): void {
  void router.push({ name: 'rag-documents', params: { kbId: row.id } })
}

// ---- 嵌入模型下拉数据源（弹窗打开时现拉：启用提供商 → 启用 embedding 模型，按供应商分组） ----

const modelGroups = ref<Array<{ name: string; models: ModelItem[] }>>([])
const modelLoading = ref(false)

async function loadModelOptions(): Promise<void> {
  modelLoading.value = true
  try {
    const page = await getProviderList({ page: 1, page_size: 100 })
    const groups = await Promise.all(
      page.list
        .filter((p) => p.enabled)
        .map(async (p) => {
          const mp = await getModelList(p.id, { page: 1, page_size: 100 })
          return {
            name: p.name,
            models: mp.list.filter((m) => m.capability === 'embedding' && m.enabled),
          }
        }),
    )
    modelGroups.value = groups.filter((g) => g.models.length > 0)
  } catch {
    // 拦截器已提示；空分组下拉即反馈
  } finally {
    modelLoading.value = false
  }
}

// ---- 新增 / 编辑弹窗 ----

/** 表单模型：id='' 为新增 */
interface KBForm {
  id: string
  name: string
  description: string
  embeddingModelId: string
  enabled: boolean
  chunkStrategy: {
    type: 'fixed_length'
    chunk_size: number
    chunk_overlap: number
    separator: string
  }
}

const dialogRef = ref<{ open: (data?: KBForm) => void }>()
const dialogVisible = ref(false)
const isEdit = ref(false)

const emptyForm = (): KBForm => ({
  id: '',
  name: '',
  description: '',
  embeddingModelId: '',
  enabled: true,
  chunkStrategy: {
    type: 'fixed_length',
    chunk_size: 0, // 0 = 使用全局默认
    chunk_overlap: 0,
    separator: '',
  },
})

const rules: FormRules = {
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  embeddingModelId: [{ required: true, message: '请选择嵌入模型', trigger: 'change' }],
}

function openCreate(): void {
  isEdit.value = false
  // 先拉模型选项再开弹窗（量小可接受；保持选项新鲜）
  void loadModelOptions().then(() => dialogRef.value?.open())
}

function openEdit(row: KnowledgeBaseItem): void {
  isEdit.value = true
  // 编辑时也拉模型（展示当前选中的嵌入模型名，即使模型已停用）
  void loadModelOptions().then(() => {
    dialogRef.value?.open({
      id: row.id,
      name: row.name,
      description: row.description,
      embeddingModelId: row.embedding_model_id,
      enabled: row.enabled,
      chunkStrategy: {
        type: row.chunk_strategy?.type || 'fixed_length',
        chunk_size: row.chunk_strategy?.chunk_size || 0,
        chunk_overlap: row.chunk_strategy?.chunk_overlap || 0,
        separator: row.chunk_strategy?.separator || '',
      },
    })
  })
}

function onSubmit(form: KBForm, done: (ok?: boolean) => void): void {
  // 请求体 id 类字段转数值（后端 Go uint64；字符串会 400——agent.ts 踩坑 #8）
  if (form.id === '') {
    // 创建时传切分策略（0 值字段后端降级读全局默认）
    const chunkStrategy = {
      type: form.chunkStrategy.type,
      chunk_size: form.chunkStrategy.chunk_size,
      chunk_overlap: form.chunkStrategy.chunk_overlap,
      separator: form.chunkStrategy.separator,
    }
    void createKnowledgeBase({
      name: form.name,
      description: form.description,
      embedding_model_id: Number(form.embeddingModelId),
      chunk_strategy: chunkStrategy,
    })
      .then(() => {
        notifySuccess('创建成功')
        done(true)
        tableRef.value?.refresh()
      })
      .catch(() => done(false))
  } else {
    // PUT 全量提交：embedding_model_id 不发（不可改）；必须带 enabled（漏发会被后端置回启用）
    void updateKnowledgeBase(form.id, {
      name: form.name,
      description: form.description,
      enabled: form.enabled,
    })
      .then(() => {
        notifySuccess('更新成功')
        done(true)
        tableRef.value?.refresh()
      })
      .catch(() => done(false))
  }
}

// ---- 删除：useConfirm 一行全流程 ----

function remove(row: KnowledgeBaseItem): void {
  void useConfirm({
    message: `删除知识库「${row.name}」？此操作不可恢复，知识库下的文档将一并删除。`,
    api: () => deleteKnowledgeBase(row.id),
  }).then((ok) => {
    if (ok) tableRef.value?.refresh()
  })
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

<style scoped>
/* 悬空引用（模型已被删）弱化展示裸 id */
.model-dangling {
  color: var(--hf-text-3);
  font-size: 12px;
}
</style>
