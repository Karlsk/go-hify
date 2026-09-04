<template>
  <!-- Provider 列表页：HifyTable + HifyFormDialog + useConfirm + 真实 API（api/provider.ts）。
       健康状态 / 模型数是列表聚合列（后端分页窗口批量现读）；测试按钮探测上限 10s。 -->
  <div>
    <PageHeader
      title="模型提供商管理"
      description="多模型提供商统一接入，API Key 加密存储"
    >
      <template #actions>
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon>
          新增提供商
        </el-button>
      </template>
    </PageHeader>

    <HifyTable ref="tableRef" :columns="columns" :api="getProviderList">
      <template #kind="{ row }">
        <el-tag :type="KIND_TAG[row.kind]" size="small">
          {{ KIND_LABEL[row.kind] }}
        </el-tag>
      </template>
      <template #health="{ row }">
        <el-tag :type="HEALTH_TAG[row.health?.status ?? 'unknown']" size="small">
          {{ HEALTH_LABEL[row.health?.status ?? 'unknown'] }}
        </el-tag>
        <span v-if="row.health?.latency_ms != null" class="health-latency">
          {{ row.health.latency_ms }}ms
        </span>
      </template>
      <template #models="{ row }">
        <!-- 点击模型数展开该提供商的模型抽屉（唯一入口） -->
        <el-button link type="primary" @click="modelsRef?.open(row)">
          {{ row.enabled_model_count }}
        </el-button>
      </template>
      <template #status="{ row }">
        <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
          {{ row.enabled ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #createdAt="{ row }">
        {{ formatDateTime(row.created_at) }}
      </template>
      <template #actions="{ row }">
        <el-button
          link
          type="primary"
          :loading="testingId === row.id"
          @click="testConn(row)"
        >
          测试
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
        <el-form-item label="类型" prop="kind">
          <!-- kind 创建后不可改（后端 UpdateProviderReq 无此字段）：编辑时禁用 -->
          <el-select
            v-model="form.kind"
            :disabled="form.id !== ''"
            style="width: 100%"
            @change="onKindChange"
          >
            <el-option
              v-for="k in KIND_OPTIONS"
              :key="k.value"
              :label="k.label"
              :value="k.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="API Key" prop="apiKey">
          <!-- 编辑不回显明文：留空 = 保留原 Key，填写 = 轮换（对齐后端永不回明文的安全姿态） -->
          <el-input
            v-model="form.apiKey"
            type="password"
            show-password
            :placeholder="
              form.id === ''
                ? '如 sk-…'
                : form.hasApiKey
                  ? '已设置：留空保留原 Key，填写即轮换'
                  : '未设置'
            "
          />
        </el-form-item>
        <el-form-item label="Base URL" prop="baseUrl">
          <el-input
            v-model="form.baseUrl"
            placeholder="可选；空 = 默认官方端点，兼容网关填完整前缀（含 /v1）"
          />
        </el-form-item>
        <!-- PUT 全量提交含 enabled：不处理的话任何编辑都会把提供商停用（Go bool 零值） -->
        <el-form-item v-if="isEdit" label="启用" prop="enabled">
          <el-switch v-model="form.enabled" />
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
import { BREAKPOINTS } from '@/composables/useBreakpoint'
import { useConfirm } from '@/composables/useConfirm'
import { notifyError, notifySuccess } from '@/utils/notify'
import {
  createProvider,
  deleteProvider,
  getProviderList,
  testConnection,
  updateProvider,
  type ProviderItem,
  type ProviderKind,
} from '@/api/provider'

// ---- kind 展示与校验配置（对齐后端 api 层枚举） ----

const KIND_OPTIONS: Array<{ value: ProviderKind; label: string }> = [
  { value: 'openai_compatible', label: 'OpenAI 兼容（官方 / 网关）' },
  { value: 'claude', label: 'Claude' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'ollama', label: 'Ollama' },
]

// EP 仅 5 种 tag 色
const KIND_TAG: Record<ProviderKind, 'primary' | 'warning' | 'success' | 'info'> = {
  openai_compatible: 'primary',
  claude: 'warning',
  gemini: 'success',
  ollama: 'info',
}

const KIND_LABEL: Record<ProviderKind, string> = {
  openai_compatible: 'OpenAI 兼容',
  claude: 'Claude',
  gemini: 'Gemini',
  ollama: 'Ollama',
}

/** api_key 必填的 kind（对齐后端 kindNeedsAPIKey：ollama 无鉴权；无鉴权本地网关可填占位串） */
const KIND_NEEDS_KEY: Record<ProviderKind, boolean> = {
  openai_compatible: true,
  claude: true,
  gemini: true,
  ollama: false,
}

// ---- 健康状态展示（null 归 unknown——从未探测） ----

type HealthTagType = 'success' | 'warning' | 'danger' | 'info'
const HEALTH_TAG: Record<string, HealthTagType> = {
  up: 'success',
  degraded: 'warning',
  down: 'danger',
  unknown: 'info',
}
const HEALTH_LABEL: Record<string, string> = {
  up: '正常',
  degraded: '降级',
  down: '故障',
  unknown: '未探测',
}

// ---- 表格 ----

const tableRef = ref<{ refresh: () => void }>()
const columns: HifyTableColumn[] = [
  { label: '名称', prop: 'name' },
  { label: '类型', slot: 'kind', width: 100 },
  { label: '健康状态', slot: 'health', width: 140 },
  { label: '模型数', slot: 'models', width: 80 },
  // 次要列：窄屏（≤992）隐藏，保留 名称 / 类型 / 健康 / 模型数 / 状态 / 操作 关键信息
  { label: 'Base URL', prop: 'base_url', hideBelow: BREAKPOINTS.md },
  { label: '状态', slot: 'status', width: 80 },
  { label: '创建时间', slot: 'createdAt', width: 150, hideBelow: BREAKPOINTS.md },
  { label: '操作', slot: 'actions', width: 170, align: 'right' },
]

// ---- 模型子资源抽屉 ----

const modelsRef = ref<{ open: (provider: ProviderItem) => void }>()

// ---- 连通性测试（探测上限 10s：per-row loading，结束刷新健康列） ----

const testingId = ref<string | null>(null)

async function testConn(row: ProviderItem): Promise<void> {
  testingId.value = row.id
  try {
    const r = await testConnection(row.id)
    // 探测失败是业务结果（HTTP 200 + success:false），显式提示；HTTP 层失败由拦截器弹
    if (r.success) {
      notifySuccess(`连接成功 · ${r.latency_ms}ms · ${r.model_count} 个模型`)
    } else {
      notifyError(`连接失败：${r.error_message || '未知原因'}`)
    }
  } catch {
    // 已由拦截器提示
  } finally {
    testingId.value = null
    tableRef.value?.refresh() // 探测已写 provider_health，刷新健康列
  }
}

// ---- 新增 / 编辑弹窗 ----

/** 表单模型：id='' 为新增；apiKey 留空 = 保留原值 */
interface ProviderForm {
  id: string
  name: string
  kind: ProviderKind
  apiKey: string
  baseUrl: string
  hasApiKey: boolean
  enabled: boolean
}

const dialogRef = ref<{ open: (data?: ProviderForm) => void }>()
const dialogVisible = ref(false)
const isEdit = ref(false)
/** rules 需感知弹窗内的 kind（api_key 必填按 kind 分）：选择变化时同步镜像 */
const formKind = ref<ProviderKind>('openai_compatible')

const emptyForm = (): ProviderForm => ({
  id: '',
  name: '',
  kind: 'openai_compatible',
  apiKey: '',
  baseUrl: '',
  hasApiKey: false,
  enabled: true,
})

function onKindChange(kind: ProviderKind): void {
  formKind.value = kind
}

/** base_url 非空时校验前缀（对齐后端 validateProvider） */
function validateBaseUrl(
  _rule: unknown,
  value: string,
  callback: (error?: Error) => void,
): void {
  if (value && !/^https?:\/\//.test(value)) {
    callback(new Error('必须以 http:// 或 https:// 开头'))
    return
  }
  callback()
}

const rules = computed<FormRules>(() => ({
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  kind: [{ required: true, message: '请选择类型', trigger: 'change' }],
  apiKey:
    !isEdit.value && KIND_NEEDS_KEY[formKind.value]
      ? [{ required: true, message: '该类型必须提供 API Key', trigger: 'blur' }]
      : [],
  baseUrl: [
    // 可选：空 = kind 默认端点（openai_compatible 默认官方 api.openai.com/v1）
    { validator: validateBaseUrl, trigger: 'blur' },
  ],
}))

function openCreate(): void {
  isEdit.value = false
  formKind.value = 'openai_compatible'
  dialogRef.value?.open()
}

function openEdit(row: ProviderItem): void {
  isEdit.value = true
  formKind.value = row.kind
  // apiKey 不回显明文：表单留空，提交时空值保留原 Key
  dialogRef.value?.open({
    id: row.id,
    name: row.name,
    kind: row.kind,
    apiKey: '',
    baseUrl: row.base_url,
    hasApiKey: row.has_api_key,
    enabled: row.enabled,
  })
}

function onSubmit(
  form: ProviderForm,
  done: (ok?: boolean) => void,
): void {
  if (form.id === '') {
    void createProvider({
      name: form.name,
      kind: form.kind,
      base_url: form.baseUrl,
      api_key: form.apiKey,
    })
      .then(() => {
        notifySuccess('创建成功')
        done(true)
        tableRef.value?.refresh()
      })
      .catch(() => done(false)) // 失败提示已由拦截器弹；保持弹窗打开
  } else {
    // PUT 全量提交：kind 不发（不可改）；api_key 空串 = 保留原值
    void updateProvider(form.id, {
      name: form.name,
      base_url: form.baseUrl,
      api_key: form.apiKey,
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

function remove(row: ProviderItem): void {
  void useConfirm({
    message: `删除提供商「${row.name}」？模型与健康记录将一并删除，此操作不可恢复。`,
    api: () => deleteProvider(row.id),
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
/* 健康列的延迟小字（弱化，不与 tag 抢视觉） */
.health-latency {
  margin-left: var(--hf-space-2);
  color: var(--hf-text-3);
  font-size: 12px;
}
</style>
