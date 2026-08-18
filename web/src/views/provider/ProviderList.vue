<template>
  <!-- Provider 列表页：HifyTable + HifyFormDialog + useConfirm 落地。
       当前为 mock 数据源（后端未就绪），fetchList/onSubmit 的签名对齐
       utils/request 的 getList 形态，日后换真实 API 只动这两个函数。 -->
  <div>
    <PageHeader
      title="模型提供商管理"
      description="多模型提供商统一接入，API Key 加密存储"
    >
      <template #actions>
        <el-button type="primary" @click="openCreate">新增提供商</el-button>
      </template>
    </PageHeader>

    <HifyTable ref="tableRef" :columns="columns" :api="fetchList">
      <template #type="{ row }">
        <el-tag :type="TYPE_TAG[row.type]" size="small">{{ row.type }}</el-tag>
      </template>
      <template #status="{ row }">
        <el-tag
          :type="row.status === 'enabled' ? 'success' : 'info'"
          size="small"
        >
          {{ row.status === 'enabled' ? '启用' : '禁用' }}
        </el-tag>
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click="modelsRef?.open(row)">
          模型
        </el-button>
        <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
        <el-button link type="danger" @click="remove(row)">删除</el-button>
      </template>
    </HifyTable>

    <!-- 模型子资源抽屉：open(provider) 结构化满足 ProviderRef -->
    <ProviderModelsDrawer ref="modelsRef" />

    <HifyFormDialog
      ref="dialogRef"
      v-model="dialogVisible"
      title="提供商"
      :initial-model="emptyForm"
      :rules="rules"
      @submit="onSubmit"
    >
      <template #default="{ form }">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="如 OpenAI 官方 / 本地 Ollama" />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-select v-model="form.type" style="width: 100%">
            <el-option v-for="t in TYPES" :key="t" :label="t" :value="t" />
          </el-select>
        </el-form-item>
        <el-form-item label="API Key" prop="apiKey">
          <!-- 编辑不回显明文：留空 = 保留原 Key，填写 = 轮换（对齐后端永不回明文的安全姿态） -->
          <el-input
            v-model="form.apiKey"
            type="password"
            show-password
            :placeholder="
              form.id === 0 ? '如 sk-…' : '掩码 sk-••••••••：留空保留原 Key，填写即轮换'
            "
          />
        </el-form-item>
        <el-form-item label="Base URL" prop="baseUrl">
          <el-input
            v-model="form.baseUrl"
            placeholder="如 https://api.openai.com/v1"
          />
        </el-form-item>
      </template>
    </HifyFormDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { FormRules } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import HifyTable, { type HifyTableColumn } from '@/components/HifyTable.vue'
import HifyFormDialog from '@/components/HifyFormDialog.vue'
import ProviderModelsDrawer from '@/components/ProviderModelsDrawer.vue'
import { useConfirm } from '@/composables/useConfirm'
import { notifySuccess } from '@/utils/notify'
import type { PageQuery, PageResult, ProviderType } from '@/types'

// ---- 领域类型（mock 阶段本地定义；真实 API 就绪后迁 api 层 schema） ----
type ProviderStatus = 'enabled' | 'disabled'

interface ProviderRow {
  id: number
  name: string
  type: ProviderType
  baseUrl: string
  apiKey: string
  status: ProviderStatus
  createdAt: string
}

/** 表单模型：id=0 为新增；apiKey 留空 = 保留原值 */
interface ProviderForm {
  id: number
  name: string
  type: ProviderType
  apiKey: string
  baseUrl: string
}

const TYPES: ProviderType[] = ['OpenAI', 'Claude', 'Gemini', 'Ollama']

// 类型 tag 配色与 /design 公共组件演示区一致
const TYPE_TAG: Record<ProviderType, 'primary' | 'warning' | 'success' | 'info'> = {
  OpenAI: 'primary',
  Claude: 'warning',
  Gemini: 'success',
  Ollama: 'info',
}

// ---- mock 数据源（5 条，类型分布开；1 条禁用） ----
const dataset = ref<ProviderRow[]>([
  {
    id: 1,
    name: 'OpenAI 官方',
    type: 'OpenAI',
    baseUrl: 'https://api.openai.com/v1',
    apiKey: 'sk-mock-openai-001',
    status: 'enabled',
    createdAt: '2026-06-02',
  },
  {
    id: 2,
    name: 'Claude 官方',
    type: 'Claude',
    baseUrl: 'https://api.anthropic.com',
    apiKey: 'sk-ant-mock-002',
    status: 'enabled',
    createdAt: '2026-06-15',
  },
  {
    id: 3,
    name: 'Gemini 官方',
    type: 'Gemini',
    baseUrl: 'https://generativelanguage.googleapis.com',
    apiKey: 'ai-mock-003',
    status: 'enabled',
    createdAt: '2026-07-01',
  },
  {
    id: 4,
    name: '本地 Ollama',
    type: 'Ollama',
    baseUrl: 'http://host.docker.internal:11434',
    apiKey: '',
    status: 'enabled',
    createdAt: '2026-07-20',
  },
  {
    id: 5,
    name: 'OpenAI 备用线路',
    type: 'OpenAI',
    baseUrl: 'https://api.openai.com/v1',
    apiKey: 'sk-mock-openai-005',
    status: 'disabled',
    createdAt: '2026-08-05',
  },
])

/** mock 列表接口：签名对齐 utils/request.getList，日后整体替换 */
function fetchList(q: PageQuery): Promise<PageResult<ProviderRow>> {
  const page = q.page ?? 1
  const pageSize = q.page_size ?? 20
  return new Promise((resolve) => {
    setTimeout(() => {
      const start = (page - 1) * pageSize
      resolve({
        list: dataset.value.slice(start, start + pageSize),
        total: dataset.value.length,
        page,
        pageSize,
      })
    }, 300)
  })
}

// ---- 表格 ----
const tableRef = ref<{ refresh: () => void }>()
const columns: HifyTableColumn[] = [
  { label: '名称', prop: 'name' },
  { label: '类型', slot: 'type', width: 110 },
  { label: 'Base URL', prop: 'baseUrl' },
  { label: '状态', slot: 'status', width: 90 },
  { label: '创建时间', prop: 'createdAt', width: 130 },
  { label: '操作', slot: 'actions', width: 180, align: 'right' },
]

// ---- 模型子资源抽屉 ----
const modelsRef = ref<{ open: (provider: ProviderRow) => void }>()

// ---- 新增 / 编辑弹窗 ----
const dialogRef = ref<{ open: (data?: ProviderForm) => void }>()
const dialogVisible = ref(false)
const isEdit = ref(false)

const emptyForm = (): ProviderForm => ({
  id: 0,
  name: '',
  type: 'OpenAI',
  apiKey: '',
  baseUrl: '',
})

/** 校验：API Key 仅新增必填（编辑留空 = 保留原值） */
const rules = computed<FormRules>(() => ({
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  type: [{ required: true, message: '请选择类型', trigger: 'change' }],
  baseUrl: [{ required: true, message: '请输入 Base URL', trigger: 'blur' }],
  ...(isEdit.value
    ? {}
    : {
        apiKey: [{ required: true, message: '请输入 API Key', trigger: 'blur' }],
      }),
}))

function openCreate(): void {
  isEdit.value = false
  dialogRef.value?.open()
}

function openEdit(row: ProviderRow): void {
  isEdit.value = true
  // apiKey 不回显明文：表单留空，提交时空值保留原 Key
  dialogRef.value?.open({
    id: row.id,
    name: row.name,
    type: row.type,
    apiKey: '',
    baseUrl: row.baseUrl,
  })
}

function onSubmit(form: ProviderForm, done: (ok?: boolean) => void): void {
  setTimeout(() => {
    if (form.id === 0) {
      const nextId = dataset.value.reduce((m, r) => Math.max(m, r.id), 0) + 1
      dataset.value = [
        ...dataset.value,
        {
          id: nextId,
          name: form.name,
          type: form.type,
          apiKey: form.apiKey,
          baseUrl: form.baseUrl,
          status: 'enabled', // 新增默认启用（澄清决策）
          createdAt: '2026-08-18',
        },
      ]
      notifySuccess('创建成功')
    } else {
      dataset.value = dataset.value.map((r) =>
        r.id === form.id
          ? {
              ...r,
              name: form.name,
              type: form.type,
              baseUrl: form.baseUrl,
              // 留空保留原 Key，填写即轮换
              apiKey: form.apiKey === '' ? r.apiKey : form.apiKey,
            }
          : r,
      )
      notifySuccess('更新成功')
    }
    done(true)
    tableRef.value?.refresh()
  }, 400)
}

// ---- 删除：useConfirm 一行全流程 ----
function remove(row: ProviderRow): void {
  void useConfirm({
    message: `删除提供商「${row.name}」？此操作不可恢复。`,
    api: () =>
      new Promise((resolve) => {
        setTimeout(() => {
          dataset.value = dataset.value.filter((r) => r.id !== row.id)
          resolve(true)
        }, 300)
      }),
  }).then((ok) => {
    if (ok) tableRef.value?.refresh()
  })
}
</script>
