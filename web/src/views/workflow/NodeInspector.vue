<template>
  <!-- 右侧检查器（FR-010）：点击节点 → 按类型分化表单；点击连线 → condition 标签编辑。
       写入模式（data-model §3 单源）：computed get/set 直改 node.data.config 的已知键，
       未知 config 键原样透传（SC-006）；config 外键（model_id/workflow_id）字符串保形（FR-011）。
       数据源（research #4/#5）：模型下拉走 AgentList 先例（providers 遍历 + chat 过滤、
       按供应商分组）；子工作流下拉前端过滤 task 型。 -->
  <aside class="node-inspector">
    <!-- 选中节点：按类型分化表单 -->
    <div v-if="node" class="node-inspector__body">
      <div class="node-inspector__header">
        <span class="node-inspector__type">{{ typeLabel }}</span>
        <span class="node-inspector__key">{{ node.id }}</span>
      </div>

      <template v-if="nodeType === 'llm'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">模型</span>
          <el-select
            v-model="modelId"
            :loading="modelLoading"
            placeholder="选择 chat 模型"
            filterable
          >
            <el-option-group v-for="g in modelGroups" :key="g.name" :label="g.name">
              <el-option v-for="m in g.models" :key="m.id" :label="m.name" :value="m.id" />
            </el-option-group>
          </el-select>
          <p v-if="!modelLoading && modelGroups.length === 0" class="node-inspector__hint">
            暂无可用模型：请先在「提供商管理」配置并启用 chat 模型
          </p>
        </div>
        <div class="node-inspector__field">
          <span class="node-inspector__label">Prompt</span>
          <el-input
            v-model="prompt"
            type="textarea"
            :rows="4"
            placeholder="支持 {{var}} 模板"
          />
        </div>
      </template>

      <template v-else-if="nodeType === 'end'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">Output</span>
          <el-input
            v-model="output"
            type="textarea"
            :rows="3"
            placeholder="{{var}} 模板，如 {{reply}}（可选）"
          />
        </div>
      </template>

      <template v-else-if="nodeType === 'condition'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">Expression</span>
          <el-input
            v-model="expression"
            type="textarea"
            :rows="2"
            placeholder="{{classify}} == 'ORDER_QUERY'"
          />
          <p class="node-inspector__hint">
            分支取值编辑各出边的条件标签：点选连线后在下方填写
          </p>
        </div>
      </template>

      <template v-else-if="nodeType === 'api'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">Method</span>
          <el-select v-model="method">
            <el-option v-for="m in API_METHODS" :key="m" :label="m" :value="m" />
          </el-select>
        </div>
        <div class="node-inspector__field">
          <span class="node-inspector__label">URL</span>
          <el-input v-model="url" placeholder="http(s)://…" />
        </div>
      </template>

      <template v-else-if="nodeType === 'workflow'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">子工作流</span>
          <el-select
            v-model="workflowId"
            :loading="workflowLoading"
            placeholder="选择任务型工作流"
            filterable
          >
            <el-option v-for="w in workflowOptions" :key="w.value" :label="w.label" :value="w.value" />
          </el-select>
          <p v-if="!workflowLoading && workflowOptions.length === 0" class="node-inspector__hint">
            暂无任务型工作流可引用：请先创建 type=task 的工作流
          </p>
        </div>
        <div class="node-inspector__field">
          <span class="node-inspector__label">Inputs</span>
          <div v-for="(row, i) in inputRows" :key="i" class="node-inspector__kv">
            <el-input v-model="row.key" placeholder="变量名" @change="syncInputsToConfig" />
            <el-input v-model="row.value" placeholder="{{var}} 模板" @change="syncInputsToConfig" />
            <el-button text type="danger" @click="removeInputRow(i)">
              <el-icon><Delete /></el-icon>
            </el-button>
          </div>
          <el-button text type="primary" @click="addInputRow">
            <el-icon><Plus /></el-icon>
            添加入参
          </el-button>
        </div>
      </template>

      <template v-else>
        <p class="node-inspector__hint">
          该节点类型（{{ typeLabel }}）无画布配置表单，请用 JSON 模式编辑
        </p>
      </template>
    </div>

    <!-- 选中连线：condition 标签编辑 -->
    <div v-else-if="edge" class="node-inspector__body">
      <div class="node-inspector__header">
        <span class="node-inspector__type">连线</span>
      </div>
      <div class="node-inspector__field">
        <span class="node-inspector__label">Condition 标签</span>
        <el-input v-model="edgeCondition" placeholder="如 ORDER_QUERY（可选）" clearable />
        <p class="node-inspector__hint">condition 节点按出边标签分流；空标签 = 默认分支</p>
      </div>
    </div>

    <!-- 未选中 -->
    <div v-else class="node-inspector__empty">
      <el-empty description="点击节点编辑配置，点击连线编辑条件标签" :image-size="48" />
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Delete, Plus } from '@element-plus/icons-vue'
import { getModelList, getProviderList, type ModelItem } from '@/api/provider'
import { getWorkflowList } from '@/api/workflow'
import { nodeTypeLabel, type InspectorEdge, type InspectorNode } from './graph'

const props = defineProps<{
  /** 选中节点（store 响应式对象；data.config 直改即改图配置——引用共享单源） */
  node: InspectorNode | null
  /** 选中连线（label 即 condition；空串序列化时省略键） */
  edge: InspectorEdge | null
}>()

const nodeType = computed(() => props.node?.data?.nodeType ?? '')
const typeLabel = computed(() => nodeTypeLabel(nodeType.value))

// ---- config 字段读写：computed get/set 只动已知键，未知键不动（透传） ----

/** 字符串型 config 字段工厂：读时非字符串/缺省回退 fallback，写时直改 config */
function useStrConfigField(key: string, fallback = '') {
  return computed<string>({
    get: () => {
      const v = props.node?.data?.config?.[key]
      return typeof v === 'string' ? v : fallback
    },
    set: (value: string) => {
      const cfg = props.node?.data?.config
      if (cfg) cfg[key] = value
    },
  })
}

const modelId = useStrConfigField('model_id')
const prompt = useStrConfigField('prompt')
const output = useStrConfigField('output')
const expression = useStrConfigField('expression')
const url = useStrConfigField('url')
const method = useStrConfigField('method', 'GET')
const workflowId = useStrConfigField('workflow_id')

/** 连线 condition：label ↔ condition（空串在 canvasToGraphEdges 序列化时省略） */
const edgeCondition = computed<string>({
  get: () => (typeof props.edge?.label === 'string' ? props.edge.label : ''),
  set: (value: string) => {
    if (props.edge) props.edge.label = value
  },
})

/** API 节点 method 合法值（后端 ApiCallConfig：GET/POST/PUT/DELETE/PATCH 大写） */
const API_METHODS = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH'] as const

// ---- 模型下拉数据源（AgentList 先例：启用 provider → 启用 chat 模型，按供应商分组） ----

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
    // 拦截器已提示；空分组即反馈
  } finally {
    modelLoading.value = false
  }
}

// ---- 子工作流下拉数据源（前端过滤 task 型：嵌套目标仅 task，后端 R11） ----

const workflowOptions = ref<Array<{ value: string; label: string }>>([])
const workflowLoading = ref(false)

async function loadWorkflowOptions(): Promise<void> {
  workflowLoading.value = true
  try {
    const page = await getWorkflowList({ page: 1, page_size: 100 })
    workflowOptions.value = page.list
      .filter((w) => w.type === 'task')
      .map((w) => ({ value: w.id, label: w.name }))
  } catch {
    // 拦截器已提示；空选项即反馈
  } finally {
    workflowLoading.value = false
  }
}

onMounted(() => {
  void loadModelOptions()
  void loadWorkflowOptions()
})

// ---- workflow 节点 inputs 动态键值编辑（config.inputs: map[string]string） ----

interface InputRow {
  key: string
  value: string
}

const inputRows = ref<InputRow[]>([])

/** 选中切换时从 config.inputs 重建行；非 workflow 节点 / 非对象值 → 空行 */
watch(
  () => props.node,
  () => rebuildInputRows(),
  { immediate: true },
)

function rebuildInputRows(): void {
  const inputs = props.node?.data?.config?.inputs
  if (nodeType.value !== 'workflow' || typeof inputs !== 'object' || inputs === null) {
    inputRows.value = []
    return
  }
  inputRows.value = Object.entries(inputs).map(([key, value]) => ({
    key,
    value: typeof value === 'string' ? value : String(value),
  }))
}

/** 行编辑回写 config.inputs：空 key 行忽略；全部清空删键（omitempty 语义） */
function syncInputsToConfig(): void {
  const cfg = props.node?.data?.config
  if (!cfg) return
  const entries = inputRows.value.filter((r) => r.key)
  if (entries.length === 0) delete cfg.inputs
  else cfg.inputs = Object.fromEntries(entries.map((r) => [r.key, r.value]))
}

function addInputRow(): void {
  inputRows.value.push({ key: '', value: '' })
}

function removeInputRow(index: number): void {
  inputRows.value.splice(index, 1)
  syncInputsToConfig()
}
</script>

<style scoped>
.node-inspector {
  display: flex;
  flex-direction: column;
  width: 250px;
  flex-shrink: 0;
  padding: var(--hf-space-3);
  border: 1px solid var(--hf-border-1);
  border-radius: var(--hf-radius-md);
  background: var(--hf-bg-container);
  overflow-y: auto;
}

.node-inspector__header {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-1);
  padding-bottom: var(--hf-space-2);
  margin-bottom: var(--hf-space-3);
  border-bottom: 1px solid var(--hf-border-2);
}

.node-inspector__type {
  font-size: var(--hf-font-size-sm);
  font-weight: var(--hf-font-weight-semibold);
  color: var(--hf-text-1);
}

.node-inspector__key {
  font-size: var(--hf-font-size-xs);
  color: var(--hf-text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node-inspector__field {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-1);
  margin-bottom: var(--hf-space-3);
}

.node-inspector__label {
  font-size: var(--hf-font-size-xs);
  color: var(--hf-text-2);
}

.node-inspector__hint {
  margin: 0;
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
  line-height: var(--hf-leading-normal);
}

/* inputs 键值行：key + value + 删除 */
.node-inspector__kv {
  display: flex;
  align-items: center;
  gap: var(--hf-space-1);
}

.node-inspector__empty {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: center;
}
</style>
