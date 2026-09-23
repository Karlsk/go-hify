<template>
  <!-- Agent 列表页：HifyTable + HifyFormDialog + useConfirm + 真实 API（api/agent.ts）。
       关联模型名 / 工具数是列表聚合列（后端分页窗口批量现读）；模型下拉按供应商分组，
       弹窗打开时现拉（管理页低频，保持新鲜优先于缓存）。 -->
  <div>
    <PageHeader
      title="Agent 管理"
      description="选模型、绑 MCP 工具、设系统提示词，组装可对话的 Agent"
    >
      <template #actions>
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon>
          创建 Agent
        </el-button>
      </template>
    </PageHeader>

    <HifyTable ref="tableRef" :columns="columns" :api="getAgentList">
      <template #modelName="{ row }">
        <!-- model_name 空串 = 悬空引用（模型已被删），fallback 显示裸 id -->
        <span :class="{ 'model-dangling': !row.model_name }">
          {{ row.model_name || `#${row.model_id}` }}
        </span>
      </template>
      <template #toolCount="{ row }">
        {{ row.tool_count }}
      </template>
      <template #kbCount="{ row }">
        {{ row.kb_count }}
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
      title="Agent"
      width="640px"
      :initial-model="emptyForm"
      :rules="rules"
      @submit="onSubmit"
    >
      <template #default="{ form }">
        <el-tabs>
          <el-tab-pane label="基础配置">
            <el-form-item label="名称" prop="name">
              <el-input v-model="form.name" placeholder="如 售后客服助手" />
            </el-form-item>
            <el-form-item label="描述" prop="description">
              <el-input
                v-model="form.description"
                type="textarea"
                :rows="2"
                placeholder="用途说明（可选）"
              />
            </el-form-item>
            <el-form-item label="模型" prop="modelId">
              <el-select
                v-model="form.modelId"
                :loading="modelLoading"
                placeholder="选择启用的对话模型"
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
                    :label="m.name"
                    :value="m.id"
                  />
                </el-option-group>
              </el-select>
            </el-form-item>
            <!-- 绑定工作流（spec 013 US1）：仅列 chat 型（前端过滤，research D5——
                 task 型入参是 JSON 对象、聊天纯文本语义对不上）；清空 = ''（不绑定 /
                 PUT 解绑全量语义）；无 required 规则（绑定恒可选，FR-008） -->
            <el-form-item label="绑定工作流">
              <el-select
                v-model="form.workflowId"
                clearable
                :loading="workflowLoading"
                placeholder="选择对话型工作流（可选）"
                style="width: 100%"
              >
                <el-option
                  v-for="w in workflowOptions"
                  :key="w.value"
                  :label="w.label"
                  :value="w.value"
                />
              </el-select>
              <span class="field-hint">
                绑定后该 Agent 的对话将直接由工作流处理（不绑定为普通模型对话）
              </span>
              <el-text v-if="workflowOptions.length === 0" size="small" type="info">
                暂无对话型工作流；可先到工作流管理创建
              </el-text>
            </el-form-item>
            <el-form-item label="System Prompt" prop="systemPrompt">
              <el-input
                v-model="form.systemPrompt"
                type="textarea"
                :rows="6"
                placeholder="角色指令——Agent 的灵魂（可选）"
              />
            </el-form-item>
            <el-form-item label="Temperature" prop="temperature">
              <!-- 前端收窄 0-1（步长 0.1，带 0/0.5/1 刻度）；后端契约允许 0-2 -->
              <el-slider
                v-model="form.temperature"
                :min="0"
                :max="1"
                :step="0.1"
                :marks="TEMP_MARKS"
                show-input
                :show-input-controls="false"
                style="width: 100%; margin-right: 16px"
              />
            </el-form-item>
            <el-form-item label="最大输出 Token" prop="maxOutputTokens">
              <!-- 空值 = 跟随模型默认（后端存 NULL） -->
              <el-input-number
                v-model="form.maxOutputTokens"
                :min="1"
                clearable
                placeholder="跟随模型默认"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item label="上下文轮数" prop="maxContextTurns">
              <div class="field-with-hint">
                <el-input-number
                  v-model="form.maxContextTurns"
                  :min="1"
                  :max="100"
                />
                <span class="field-hint">保留最近 N 轮对话历史</span>
              </div>
            </el-form-item>
            <!-- PUT 全量提交含 enabled：不处理的话任何编辑都会把 Agent 停用（Go bool 零值） -->
            <el-form-item v-if="isEdit" label="启用" prop="enabled">
              <el-switch v-model="form.enabled" />
            </el-form-item>
          </el-tab-pane>

          <el-tab-pane label="工具绑定">
            <!-- mcp 模块未建：无可选工具，展示占位；已保存绑定仍随 PUT 提交保留 -->
            <el-checkbox-group v-model="form.toolIds">
              <el-checkbox
                v-for="t in toolOptions"
                :key="t.value"
                :value="t.value"
                :label="t.label"
              />
            </el-checkbox-group>
            <el-text v-if="toolOptions.length === 0" size="small" type="info">
              MCP 工具接入后可在此勾选；当前仅保留已保存的绑定
            </el-text>
          </el-tab-pane>

          <el-tab-pane label="知识库绑定">
            <!-- 绑定 = RAG 检索注入范围（chat 发消息时按绑定 KB 召回注入 system prompt）；
                 multiple-limit 对齐后端 MaxKBBindings=10。已保存但被停用的 KB 不在选项里，
                 回填显示裸 id，不动则绑定随 PUT 保留（检索时 rag 侧静默剔除停用 KB） -->
            <el-select
              v-model="form.kbIds"
              multiple
              :loading="kbLoading"
              :multiple-limit="10"
              placeholder="选择知识库（可多选，最多 10 个）"
              style="width: 100%"
            >
              <el-option
                v-for="kb in kbOptions"
                :key="kb.value"
                :label="kb.label"
                :value="kb.value"
              />
            </el-select>
            <el-text v-if="kbOptions.length === 0" size="small" type="info">
              尚无启用的知识库；可先到「知识库管理」创建并上传文档
            </el-text>
            <!-- RAG 检索注入参数：与绑定范围同属 KB 检索语义，chat buildSystemPrompt 读取 -->
            <el-form-item label="RAG 检索数" prop="ragTopK">
              <div class="field-with-hint">
                <el-input-number
                  v-model="form.ragTopK"
                  :min="1"
                  :max="20"
                />
                <span class="field-hint">每次检索取回的片段数（默认 3）</span>
              </div>
            </el-form-item>
            <el-form-item label="相似度阈值" prop="ragMinSimilarity">
              <div class="field-with-hint">
                <el-input-number
                  v-model="form.ragMinSimilarity"
                  :min="0"
                  :max="1"
                  :step="0.05"
                  :precision="2"
                />
                <span class="field-hint">低于此值的片段被过滤（默认 0.75）</span>
              </div>
            </el-form-item>
          </el-tab-pane>
        </el-tabs>
      </template>
    </HifyFormDialog>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { FormRules } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import HifyTable, { type HifyTableColumn } from '@/components/HifyTable.vue'
import HifyFormDialog from '@/components/HifyFormDialog.vue'
import { BREAKPOINTS } from '@/composables/useBreakpoint'
import { useConfirm } from '@/composables/useConfirm'
import { notifySuccess } from '@/utils/notify'
import {
  createAgent,
  deleteAgent,
  getAgent,
  getAgentList,
  updateAgent,
  type AgentDetail,
  type AgentItem,
} from '@/api/agent'
import { getModelList, getProviderList, type ModelItem } from '@/api/provider'
import { getKnowledgeBaseList } from '@/api/rag'
import { getWorkflowList } from '@/api/workflow'

// ---- 表格 ----

const tableRef = ref<{ refresh: () => void }>()
const columns: HifyTableColumn[] = [
  { label: '名称', prop: 'name' },
  { label: '关联模型', slot: 'modelName' },
  { label: '工具数', slot: 'toolCount', width: 80 },
  { label: '知识库数', slot: 'kbCount', width: 90 },
  // 次要列：窄屏（≤992）隐藏，保留 名称 / 模型 / 工具数 / 知识库数 / 状态 / 操作 关键信息
  { label: 'Temperature', prop: 'temperature', width: 110, hideBelow: BREAKPOINTS.md },
  { label: '状态', slot: 'enabled', width: 80 },
  { label: '创建时间', slot: 'createdAt', width: 150, hideBelow: BREAKPOINTS.md },
  { label: '操作', slot: 'actions', width: 130, align: 'right' },
]

// ---- 模型下拉数据源（弹窗打开时现拉：启用提供商 → 启用 chat 模型，按供应商分组） ----

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
            models: mp.list.filter((m) => m.capability === 'chat' && m.enabled),
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

// ---- 工具绑定（mcp 模块未建：占位空列表，绑定明细走详情 tool_ids 保留） ----

const toolOptions: Array<{ value: string; label: string }> = []

// ---- 知识库下拉数据源（弹窗打开时现拉：只列启用的 KB，与模型下拉同策略） ----

const kbOptions = ref<Array<{ value: string; label: string }>>([])
const kbLoading = ref(false)

async function loadKbOptions(): Promise<void> {
  kbLoading.value = true
  try {
    const page = await getKnowledgeBaseList({ page: 1, page_size: 100 })
    kbOptions.value = page.list
      .filter((kb) => kb.enabled)
      .map((kb) => ({ value: kb.id, label: kb.name }))
  } catch {
    // 拦截器已提示；空选项即反馈
  } finally {
    kbLoading.value = false
  }
}

/** Temperature 刻度（el-slider marks）：0 / 0.5 / 1 三档参照 */
const TEMP_MARKS: Record<number, string> = { 0: '0', 0.5: '0.5', 1: '1' }

// ---- 工作流下拉数据源（spec 013：弹窗打开时现拉，只列 chat 型——前端过滤 D5） ----

const workflowOptions = ref<Array<{ value: string; label: string }>>([])
const workflowLoading = ref(false)

async function loadWorkflowOptions(): Promise<void> {
  workflowLoading.value = true
  try {
    const page = await getWorkflowList({ page: 1, page_size: 100 })
    workflowOptions.value = page.list
      .filter((w) => w.type === 'chat')
      .map((w) => ({ value: w.id, label: w.name }))
  } catch {
    // 拦截器已提示；空选项即反馈
  } finally {
    workflowLoading.value = false
  }
}

// ---- 新增 / 编辑弹窗 ----

/** 表单模型：id='' 为新增；maxOutputTokens null = 跟随模型默认 */
interface AgentForm {
  id: string
  name: string
  description: string
  modelId: string
  /** 绑定工作流 id（'' = 不绑定；提交时转数值，spec 013） */
  workflowId: string
  systemPrompt: string
  temperature: number
  maxOutputTokens: number | null
  maxContextTurns: number
  enabled: boolean
  toolIds: string[]
  kbIds: string[]
  ragTopK: number
  ragMinSimilarity: number
}

const dialogRef = ref<{ open: (data?: AgentForm) => void }>()
const dialogVisible = ref(false)
const isEdit = ref(false)

const emptyForm = (): AgentForm => ({
  id: '',
  name: '',
  description: '',
  modelId: '',
  workflowId: '',
  systemPrompt: '',
  temperature: 0.7,
  maxOutputTokens: null,
  maxContextTurns: 10,
  enabled: true,
  toolIds: [],
  kbIds: [],
  ragTopK: 3,
  ragMinSimilarity: 0.75,
})

const rules: FormRules = {
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  modelId: [{ required: true, message: '请选择模型', trigger: 'change' }],
  maxContextTurns: [{ required: true, message: '请输入上下文轮数', trigger: 'blur' }],
}

function openCreate(): void {
  isEdit.value = false
  // 先拉模型 / 知识库 / 工作流选项再开弹窗（量小可接受；保持选项新鲜）
  void Promise.all([loadModelOptions(), loadKbOptions(), loadWorkflowOptions()]).then(() =>
    dialogRef.value?.open(),
  )
}

/** 编辑：列表行无 tool_ids / knowledge_base_ids，先取详情再回填 */
function openEdit(row: AgentItem): void {
  isEdit.value = true
  void Promise.all([
    loadModelOptions(),
    loadKbOptions(),
    loadWorkflowOptions(),
    getAgent(row.id),
  ])
    .then(([, , , detail]) => {
      dialogRef.value?.open(toForm(detail))
    })
    .catch(() => {
      // 拦截器已提示；弹窗不打开
    })
}

function toForm(d: AgentDetail): AgentForm {
  return {
    id: d.id,
    name: d.name,
    description: d.description,
    modelId: d.model_id,
    workflowId: d.workflow_id ?? '',
    systemPrompt: d.system_prompt,
    temperature: d.temperature,
    maxOutputTokens: d.max_output_tokens,
    maxContextTurns: d.max_context_turns,
    enabled: d.enabled,
    toolIds: d.tool_ids,
    kbIds: d.knowledge_base_ids,
    ragTopK: d.rag_top_k,
    ragMinSimilarity: d.rag_min_similarity,
  }
}

function onSubmit(form: AgentForm, done: (ok?: boolean) => void): void {
  // 请求体 id 类字段转数值（后端 Go uint64；字符串会 400——踩坑 #8）
  const payload = {
    name: form.name,
    description: form.description,
    model_id: Number(form.modelId),
    // '' 省键 = 创建不绑定 / PUT 全量解绑；数值 = 绑定（后端 *uint64，踩坑 #8 同型）
    workflow_id: form.workflowId === '' ? undefined : Number(form.workflowId),
    system_prompt: form.systemPrompt,
    temperature: form.temperature,
    max_output_tokens: form.maxOutputTokens ?? undefined,
    max_context_turns: form.maxContextTurns,
    tool_ids: form.toolIds.map(Number),
    knowledge_base_ids: form.kbIds.map(Number),
    rag_top_k: form.ragTopK,
    rag_min_similarity: form.ragMinSimilarity,
  }
  if (form.id === '') {
    void createAgent(payload)
      .then(() => {
        notifySuccess('创建成功')
        done(true)
        tableRef.value?.refresh()
      })
      .catch(() => done(false)) // 失败提示已由拦截器弹；保持弹窗打开
  } else {
    // PUT 全量提交：必须带 enabled（漏发会被后端置回启用）
    void updateAgent(form.id, { ...payload, enabled: form.enabled })
      .then(() => {
        notifySuccess('更新成功')
        done(true)
        tableRef.value?.refresh()
      })
      .catch(() => done(false))
  }
}

// ---- 删除：useConfirm 一行全流程 ----

function remove(row: AgentItem): void {
  void useConfirm({
    message: `删除 Agent「${row.name}」？将永久删除其配置与工具绑定；有历史会话时无法删除（可先删会话或改为停用）。`,
    api: () => deleteAgent(row.id),
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

/* 数字输入 + 行内灰字提示（上下文轮数：保留最近 N 轮对话历史） */
.field-with-hint {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.field-hint {
  color: var(--hf-text-3);
  font-size: 12px;
}
</style>
