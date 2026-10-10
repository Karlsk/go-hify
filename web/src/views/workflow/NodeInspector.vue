<template>
  <!-- 右侧检查器（FR-010）：点击节点 → 按类型分化表单；点击连线 → condition 标签编辑。
       写入模式（data-model §3 单源）：computed get/set 直改 node.data.config 的已知键，
       未知 config 键原样透传（SC-006）；config 外键（model_id/workflow_id）字符串保形（FR-011）。
       数据源（research #4/#5）：模型下拉走 AgentList 先例（providers 遍历 + chat 过滤、
       按供应商分组）；子工作流下拉前端过滤 task 型。 -->
  <aside class="node-inspector">
    <!-- 伪节点面板模式（FR-002，contracts §2）：伪节点上下文（sentinel）时替代节点表单——
         task 型 = 入参 Schema 行表单（spec 018 FR-005：出参收敛至 end 节点映射模式，
         本面板不再编辑 output_schema）；chat 型 = 单一 input 说明（只读） -->
    <div v-if="isStartPanel" class="node-inspector__body">
      <div class="node-inspector__header">
        <span class="node-inspector__type">开始</span>
        <p class="node-inspector__hint">工作流入参契约（子工作流节点引用本工作流的字段面）</p>
      </div>

      <template v-if="graphKind === 'chat'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">入参</span>
          <p class="node-inspector__hint">
            本工作流为 chat 型：仅暴露单一入参 {{ INPUT_REF_TEXT }}（用户消息），无入参 / 出参 schema 可配置
          </p>
        </div>
      </template>

      <template v-else>
        <div class="node-inspector__field">
          <span class="node-inspector__label">入参 Input</span>
          <SchemaFieldsEditor
            :model-value="inputSchema ?? []"
            :disabled="readonly"
            @update:model-value="(rows: SchemaField[]) => emit('update:inputSchema', rows)"
          />
          <p class="node-inspector__hint">
            出参在结束节点的「输出映射」中配置；伪节点不进图主体序列化（JSON 模式与提交载荷均不可见）
          </p>
        </div>
      </template>
    </div>

    <!-- 选中节点：按类型分化表单 -->
    <div v-else-if="node" class="node-inspector__body">
      <div class="node-inspector__header">
        <span class="node-inspector__type">{{ typeLabel }}</span>
        <!-- Key 编辑（FR-003）：非空 / ≤64 / 不与画布现存 key 冲突前端拦截，
             合法才 emit rename-node-key 由画布做结构性级联 -->
        <span class="node-inspector__label">Key</span>
        <el-input
          v-model="keyDraft"
          :disabled="readonly"
          :class="{ 'node-inspector__key-input--invalid': !!keyError }"
          @change="commitKey"
        />
        <p v-if="keyError" class="node-inspector__error">{{ keyError }}</p>
        <p v-else-if="isStartKey" class="node-inspector__hint">起始节点 · 左侧面板下拉 / 双击节点可切换</p>
      </div>

      <template v-if="nodeType === 'llm'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">模型</span>
          <el-select
            v-model="modelId"
            :disabled="readonly"
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
        <!-- FR-004：System Prompt（可选，空 = 不携带）与 Prompt（必填）双文本域 -->
        <div class="node-inspector__field">
          <span class="node-inspector__label">System Prompt</span>
          <TemplateField
            v-model="systemPrompt"
            :variables="variableGroups"
            :disabled="readonly"
            :rows="3"
            placeholder="角色设定（可选，空 = 不携带）"
          />
        </div>
        <div class="node-inspector__field">
          <span class="node-inspector__label">Prompt</span>
          <TemplateField
            v-model="prompt"
            :variables="variableGroups"
            :disabled="readonly"
            :rows="4"
            placeholder="必填：支持 {{var}} 模板"
          />
        </div>
        <!-- FR-003：输出字段声明（可选）——复用 SchemaFieldsEditor（与伪节点 schema 面板
             同组件同 props 形态）；readonly 态不渲染；未声明 = 现状行为零变化 -->
        <div v-if="!readonly" class="node-inspector__field">
          <span class="node-inspector__label">输出字段</span>
          <SchemaFieldsEditor
            :model-value="llmOutputSchema"
            @update:model-value="(rows: SchemaField[]) => (llmOutputSchema = rows)"
          />
          <p class="node-inspector__hint">
            可选：声明后运行期要求模型输出含这些字段的 JSON 对象并按此校验；未声明 = 现状行为
          </p>
        </div>
      </template>

      <template v-else-if="nodeType === 'end'">
        <!-- spec 018 FR-003/FR-004：task 型 end 双模式——映射模式（output_fields，逐字段
             引用变量构造终稿 JSON 对象）与高级模式（output 自由模板）互斥；chat 型保持
             单一 output 模板（无 output_schema，映射不适用） -->
        <template v-if="graphKind === 'task'">
          <div class="node-inspector__field">
            <el-radio-group
              :model-value="endMode"
              :disabled="readonly"
              size="small"
              @update:model-value="(v: string | number | boolean | undefined) => switchEndMode(v === 'advanced' ? 'advanced' : 'mapping')"
            >
              <el-radio-button value="mapping">输出映射</el-radio-button>
              <el-radio-button value="advanced">高级模板</el-radio-button>
            </el-radio-group>
            <p class="node-inspector__hint">
              映射：逐字段引用变量，终稿为 JSON 对象（字段类型自动推导）；
              高级模板：自由 <code>{{ INPUT_REF_TEXT }}</code> 风格模板
            </p>
          </div>

          <div v-if="endMode === 'mapping'" class="node-inspector__field">
            <span class="node-inspector__label">输出字段映射</span>
            <div v-if="mappingRows.length > 0" class="node-inspector__map-grid node-inspector__map-header">
              <span>字段名</span>
              <span>引用变量</span>
              <span>类型</span>
              <span>描述</span>
              <span><!-- 删除列占位 --></span>
            </div>
            <div
              v-for="(row, i) in mappingRows"
              :key="i"
              class="node-inspector__map-grid node-inspector__map-row"
            >
              <el-input
                :model-value="row.name"
                :disabled="readonly"
                placeholder="字段名"
                @update:model-value="(v: string) => onMapFieldInput(i, 'name', v)"
              />
              <el-select
                :model-value="row.ref"
                :disabled="readonly"
                placeholder="选择变量"
                filterable
                @update:model-value="(v: string) => onMapFieldInput(i, 'ref', v)"
              >
                <el-option-group v-for="g in variableGroups" :key="g.group" :label="g.group">
                  <el-option
                    v-for="o in g.options"
                    :key="o.insert"
                    :label="o.label"
                    :value="o.insert"
                  />
                </el-option-group>
              </el-select>
              <span class="node-inspector__map-type" :title="`推导类型（来源：${rowRefSource(row.ref)}）`">
                {{ rowType(row.ref) }}
              </span>
              <el-input
                :model-value="row.description"
                :disabled="readonly"
                placeholder="描述（可选）"
                @update:model-value="(v: string) => onMapFieldInput(i, 'description', v)"
              />
              <el-button v-if="!readonly" link type="danger" @click="removeMappingRow(i)">
                <el-icon><Delete /></el-icon>
              </el-button>
              <span v-else><!-- 只读态删除列占位 --></span>
            </div>
            <el-button v-if="!readonly" plain @click="addMappingRow">
              <el-icon><Plus /></el-icon>
              添加字段
            </el-button>
            <p v-if="mappingError" class="node-inspector__error">{{ mappingError }}</p>
            <p v-else class="node-inspector__hint">
              类型由被引变量推导；全部结束节点都产出的字段为必填——图级出参 schema 随映射自动同步
            </p>
          </div>

          <div v-else class="node-inspector__field">
            <span class="node-inspector__label">Output</span>
            <TemplateField
              v-model="output"
              :variables="variableGroups"
              :disabled="readonly"
              :rows="3"
              placeholder="{{var}} 模板，如 {{reply}}（可选）"
            />
          </div>
        </template>

        <div v-else class="node-inspector__field">
          <span class="node-inspector__label">Output</span>
          <TemplateField
            v-model="output"
            :variables="variableGroups"
            :disabled="readonly"
            :rows="3"
            placeholder="{{var}} 模板，如 {{reply}}（可选）"
          />
        </div>
      </template>

      <template v-else-if="nodeType === 'condition'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">Expression</span>
          <TemplateField
            v-model="expression"
            :variables="variableGroups"
            :disabled="readonly"
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
          <el-select v-model="method" :disabled="readonly">
            <el-option v-for="m in API_METHODS" :key="m" :label="m" :value="m" />
          </el-select>
        </div>
        <div class="node-inspector__field">
          <span class="node-inspector__label">URL</span>
          <TemplateField
            v-model="url"
            :variables="variableGroups"
            :disabled="readonly"
            :rows="2"
            placeholder="http(s)://…"
          />
        </div>
        <!-- FR-005：Headers 键值行（值域可插变量）；空键行不产出、重复键后写覆盖 -->
        <div class="node-inspector__field">
          <span class="node-inspector__label">Headers</span>
          <div
            v-for="(row, i) in headerRows"
            :key="i"
            class="node-inspector__kv node-inspector__kv--stack"
          >
            <el-input
              class="node-inspector__kv-name"
              :model-value="row.key"
              :disabled="readonly"
              placeholder="名称"
              @update:model-value="(v: string) => onHeaderFieldInput(i, 'key', v)"
            />
            <div class="node-inspector__kv-value">
              <TemplateField
                :model-value="row.value"
                :variables="variableGroups"
                :disabled="readonly"
                :rows="2"
                placeholder="值（可 {{var}}）"
                @update:model-value="(v: string) => onHeaderFieldInput(i, 'value', v)"
              />
            </div>
            <el-button
              text
              type="danger"
              :disabled="readonly"
              @click="removeHeaderRow(i)"
            >
              <el-icon><Delete /></el-icon>
            </el-button>
          </div>
          <el-button v-if="!readonly" text type="primary" @click="addHeaderRow">
            <el-icon><Plus /></el-icon>
            添加 Header
          </el-button>
        </div>
        <!-- FR-005：Auth 预设——推导自 Authorization 现值，写回同名 header 行（无独立 config 键） -->
        <div class="node-inspector__field">
          <span class="node-inspector__label">Auth</span>
          <el-select
            :model-value="authPreset"
            :disabled="readonly"
            placeholder="无鉴权"
            @update:model-value="applyAuthPreset"
          >
            <el-option label="无" value="none" />
            <el-option label="Bearer Token" value="bearer" />
            <el-option label="Basic 用户名密码" value="basic" />
          </el-select>
          <el-input
            v-if="authPreset === 'bearer'"
            :model-value="bearerToken"
            :disabled="readonly"
            placeholder="Token"
            show-password
            @update:model-value="onBearerInput"
          />
          <template v-else-if="authPreset === 'basic'">
            <el-input
              :model-value="basicUser"
              :disabled="readonly"
              placeholder="用户名"
              @update:model-value="(v: string) => onBasicInput('user', v)"
            />
            <el-input
              :model-value="basicPass"
              :disabled="readonly"
              placeholder="密码"
              show-password
              @update:model-value="(v: string) => onBasicInput('pass', v)"
            />
          </template>
          <p class="node-inspector__hint">
            写入 Headers 的 Authorization 行（Basic 在前端完成 base64）
          </p>
        </div>
        <div v-if="method === 'POST'" class="node-inspector__field">
          <span class="node-inspector__label">Body</span>
          <TemplateField
            v-model="body"
            :variables="variableGroups"
            :disabled="readonly"
            :rows="3"
            placeholder="请求体模板（可 {{var}}）"
          />
        </div>
      </template>

      <template v-else-if="nodeType === 'workflow'">
        <div class="node-inspector__field">
          <span class="node-inspector__label">子工作流</span>
          <el-select
            v-model="workflowId"
            :disabled="readonly"
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
        <!-- FR-006：入参行来自子流程 input_schema（字段名只读 + 模板值域） -->
        <div class="node-inspector__field">
          <span class="node-inspector__label">Inputs</span>
          <p v-if="subflowInputLoading" class="node-inspector__hint">入参加载中…</p>
          <template v-else-if="subflowInputFields.length > 0">
            <div
              v-for="f in subflowInputFields"
              :key="f.name"
              class="node-inspector__subflow-input"
            >
              <span class="node-inspector__subflow-name" :title="f.description || f.name">
                {{ f.name }}
              </span>
              <TemplateField
                :model-value="subflowInputValue(f.name)"
                :variables="variableGroups"
                :disabled="readonly"
                :rows="2"
                placeholder="值（可 {{var}} 模板）"
                @update:model-value="(v: string) => setSubflowInputValue(f.name, v)"
              />
            </div>
          </template>
          <el-empty
            v-else-if="workflowId"
            description="该子工作流未声明入参（input_schema 为空）"
            :image-size="48"
          />
          <p v-else class="node-inspector__hint">先选择子工作流，按其入参契约填写</p>
        </div>
      </template>

      <template v-else>
        <p class="node-inspector__hint">
          该节点类型（{{ typeLabel }}）无画布配置表单，请用 JSON 模式编辑
        </p>
      </template>

      <!-- 动作区（FR-001）：删除节点的级联由画布走既有 remove 链；entry 指定三入口——
           左侧面板「起始节点」下拉 / 双击节点 / 本按钮（2026-09-23 裁定后并存） -->
      <div v-if="!readonly" class="node-inspector__actions">
        <el-button :disabled="isStartKey" @click="onSetStartClick">
          <el-icon><Pointer /></el-icon>
          设为起始
        </el-button>
        <el-button type="danger" plain @click="emit('delete-node')">
          <el-icon><Delete /></el-icon>
          删除节点
        </el-button>
      </div>
    </div>

    <!-- 选中连线：连线信息 + condition 标签编辑 + 删除入口（FR-001） -->
    <div v-else-if="edge" class="node-inspector__body">
      <div class="node-inspector__header">
        <span class="node-inspector__type">连线</span>
        <span class="node-inspector__key">{{ edgeLabel }}</span>
      </div>
      <div class="node-inspector__field">
        <span class="node-inspector__label">Condition 标签</span>
        <el-input
          v-model="edgeCondition"
          :disabled="readonly"
          placeholder="如 ORDER_QUERY（可选）"
          clearable
        />
        <p class="node-inspector__hint">condition 节点按出边标签分流；空标签 = 默认分支</p>
      </div>
      <div v-if="!readonly" class="node-inspector__actions">
        <el-button type="danger" plain @click="emit('delete-edge', edge.id ?? '')">
          <el-icon><Delete /></el-icon>
          删除连线
        </el-button>
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
import { Delete, Pointer, Plus } from '@element-plus/icons-vue'
import { getModelList, getProviderList, type ModelItem } from '@/api/provider'
import {
  getWorkflowDetail,
  getWorkflowList,
  type EndNodeConfig,
  type LLMNodeConfig,
  type SchemaField,
} from '@/api/workflow'
import TemplateField, {
  type VariableGroups,
  type VariableOption,
} from './TemplateField.vue'
import SchemaFieldsEditor from './SchemaFieldsEditor.vue'
import {
  ancestorsOf,
  canvasToGraphNodes,
  deriveOutputSchema,
  deriveRefType,
  formatAuthorization,
  isStartContextNode,
  nodeTypeLabel,
  parseAuthorization,
  type AuthPreset,
  type GraphNode,
  type InspectorEdge,
  type InspectorGraphContext,
  type InspectorNode,
} from './graph'

const props = defineProps<{
  /** 选中节点（store 响应式对象；data.config 直改即改图配置——引用共享单源） */
  node: InspectorNode | null
  /** 选中连线（label 即 condition；空串序列化时省略键） */
  edge: InspectorEdge | null
  /** 图上下文（画布直传数组引用）：key 冲突校验 / 变量源计算 */
  graph?: InspectorGraphContext
  /** 起始节点 key（改 key 时展示是否为起始） */
  startNodeKey?: string | null
  /** 只读态：全部编辑控件禁用、删除入口不显示 */
  readonly?: boolean
  /** 宿主图类型（task 型按 input_schema 展开入参引用） */
  graphKind?: 'task' | 'chat'
  /** 宿主入参 schema（task 型 `{{input.x}}` 展开源；伪节点面板同源） */
  inputSchema?: SchemaField[]
  /** 宿主出参 schema（伪节点面板同源） */
  outputSchema?: SchemaField[]
  /** 自身工作流 id（编辑态子工作流下拉排除自身） */
  selfWorkflowId?: string | null
}>()

const emit = defineEmits<{
  /** 删除节点（FR-001）：级联由 CanvasEditor 走既有 remove 链 */
  'delete-node': []
  /** 删除连线（FR-001）：edgeId 供画布按 id 清理 */
  'delete-edge': [edgeId: string]
  /** Key 改名（FR-003）：校验通过才提交，级联应用在画布 */
  'rename-node-key': [oldKey: string, newKey: string]
  /** 设为起始（entry 三入口之一）：画布 onSetStart 改 startKey 单源（readonly 时按钮不渲染） */
  'set-start': [key: string]
  /** 伪节点面板 schema 同源上行（FR-002） */
  'update:inputSchema': [rows: SchemaField[]]
  'update:outputSchema': [rows: SchemaField[]]
}>()

/** chat 型唯一入参的字面引用写法。模板文本里不能直接写 `{{input}}`——插值会在第一个
 *  `}}` 处闭合、SFC 编译报未终结字符串（vue-tsc 放过、构建红）；走常量渲染字面量。
 *  注意：placeholder 等属性值里的 `{{var}}` 是字面量（Vue 不做属性插值），不受此坑影响。 */
const INPUT_REF_TEXT = '{{input}}'

const nodeType = computed(() => props.node?.data?.nodeType ?? '')
const typeLabel = computed(() => nodeTypeLabel(nodeType.value))

/** 伪节点面板模式（FR-002）：sentinel 上下文（id + 占位 type 双条件）时渲染 schema 面板 */
const isStartPanel = computed(() => isStartContextNode(props.node))

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

/** 可选字符串型 config 字段工厂：写空串即删键（对齐后端 omitempty——空 = 不携带） */
function useOptStrConfigField(key: string) {
  return computed<string>({
    get: () => {
      const v = props.node?.data?.config?.[key]
      return typeof v === 'string' ? v : ''
    },
    set: (value: string) => {
      const cfg = props.node?.data?.config
      if (!cfg) return
      if (value) cfg[key] = value
      else delete cfg[key]
    },
  })
}

const modelId = useStrConfigField('model_id')
const prompt = useStrConfigField('prompt')
/** System Prompt（FR-004）：可选，空串删键对齐后端 omitempty */
const systemPrompt = useOptStrConfigField('system_prompt')
const output = useStrConfigField('output')
const expression = useStrConfigField('expression')
const url = useStrConfigField('url')
const method = useStrConfigField('method', 'GET')
const body = useStrConfigField('body')
const workflowId = useStrConfigField('workflow_id')

/** llm 输出字段声明（FR-003）：读写 config.output_schema——读时非数组回退 []；
 *  写时原样写回（含未填名的编辑中空行——受控回路：行编辑器靠回读渲染新行），
 *  仅空数组删键（omitempty）。空行不在此过滤：组装层 toPayloadNode 进载荷前清理
 *  （契约「空数组 / 全空行 → 删键」落点，与伪节点面板 rows 原样上抛同形态） */
const llmOutputSchema = computed<SchemaField[]>({
  get: () => {
    const cfg = props.node?.data?.config as Partial<LLMNodeConfig> | undefined
    return Array.isArray(cfg?.output_schema) ? cfg.output_schema : []
  },
  set: (rows) => {
    const cfg = props.node?.data?.config
    if (!cfg) return
    if (rows.length > 0) cfg.output_schema = rows
    else delete cfg.output_schema
  },
})

/** 连线 condition：label ↔ condition（空串在 canvasToGraphEdges 序列化时省略） */
const edgeCondition = computed<string>({
  get: () => (typeof props.edge?.label === 'string' ? props.edge.label : ''),
  set: (value: string) => {
    if (props.edge) props.edge.label = value
  },
})

// ---- spec 018：end 节点映射模式（task 型，FR-003 / FR-004） ----

/** 映射行编辑面（description 承载于图级 output_schema 行——后端 OutputField 契约无此键） */
interface MappingRow {
  name: string
  ref: string
  description: string
}

const mappingRows = ref<MappingRow[]>([])
const endMode = ref<'mapping' | 'advanced'>('mapping')
const mappingError = ref('')

/** 图级 GraphNode 视图（deriveOutputSchema 输入；config 引用共享单源） */
const graphNodes = computed<GraphNode[]>(() => canvasToGraphNodes(props.graph?.nodes ?? []))

/** 子工作流 output_schema 查表（deriveOutputSchema 第三参；cacheVersion 驱动响应） */
const subflowSchemaMap = computed<Record<string, SchemaField[]>>(() => {
  cacheVersion.value
  const m: Record<string, SchemaField[]> = {}
  subflowSchemaCache.forEach((v, id) => {
    m[id] = v.output
  })
  return m
})

/** 行类型推导列（与 deriveOutputSchema 同源规则） */
function rowType(ref: string): string {
  return deriveRefType(ref, graphNodes.value, props.inputSchema, subflowSchemaMap.value)
}

/** 行类型来源说明（悬浮提示）：input / 上游 llm / 子工作流 / 裸引用 */
function rowRefSource(ref: string): string {
  const inner = ref.replace(/^\{\{/, '').replace(/\}\}$/, '').trim()
  const dot = inner.indexOf('.')
  const base = dot < 0 ? inner : inner.slice(0, dot)
  if (dot < 0) return '裸引用，恒为 string'
  if (base === 'input') return 'input_schema'
  const node = graphNodes.value.find((n) => n.key === base)
  if (!node) return '未知来源，兜底 string'
  if (node.type === 'llm') return 'llm 输出字段声明'
  if (node.type === 'workflow') return '子工作流出参'
  return '无类型声明，恒为 string'
}

/** 反解析重建（FR-003）：初始模式 = 有 output_fields → 映射 / 仅 output → 高级 /
 *  全新 → 映射；行集回填 config.output_fields（description 自当前 output_schema 承继） */
function rebuildEndForm(): void {
  mappingError.value = ''
  if (nodeType.value !== 'end') return
  const cfg = props.node?.data?.config as Partial<EndNodeConfig> | undefined
  const fields = Array.isArray(cfg?.output_fields) ? cfg.output_fields : []
  if (fields.length > 0) endMode.value = 'mapping'
  else if (typeof cfg?.output === 'string' && cfg.output) endMode.value = 'advanced'
  else endMode.value = 'mapping'
  const descOf = new Map(
    (props.outputSchema ?? []).map((f) => [f.name, f.description ?? ''] as const),
  )
  mappingRows.value = fields.map((r) => ({
    name: typeof r?.name === 'string' ? r.name : '',
    ref: typeof r?.ref === 'string' ? r.ref : '',
    description: descOf.get(typeof r?.name === 'string' ? r.name : '') ?? '',
  }))
}

/** 行编辑 → config.output_fields + 图级 output_schema 并集同步（FR-004）。
 *  冲突拦截（同名不同型跨 end）：不写 config、红字提示——冲突行永不进保存载荷，
 *  修正后写入。空行不产出（编辑中空行留草稿，受控回路同 headerRows）。 */
function syncMappingToConfig(): void {
  const cfg = props.node?.data?.config
  if (!cfg) return
  const hadFields = Array.isArray(cfg.output_fields)
  const rows = mappingRows.value.filter((r) => r.name.trim())
  const wouldBe = { ...cfg }
  if (rows.length > 0) {
    wouldBe.output_fields = rows.map((r) => ({ name: r.name.trim(), ref: r.ref }))
  } else {
    delete wouldBe.output_fields
  }
  const wouldNodes = graphNodes.value.map((n) =>
    n.key === props.node?.id ? { ...n, config: wouldBe } : n,
  )
  const derived = deriveOutputSchema(
    wouldNodes,
    props.inputSchema,
    subflowSchemaMap.value,
    props.outputSchema,
  )
  if (derived.conflict) {
    mappingError.value = `${derived.conflict}（修正后才会写入配置）`
    return
  }
  mappingError.value = ''
  if (rows.length > 0) cfg.output_fields = wouldBe.output_fields
  else delete cfg.output_fields
  if (rows.length > 0 || hadFields) {
    // description 落图级 schema 行（OutputField 契约无此键）；承继行保留原描述
    const descOf = new Map(
      mappingRows.value
        .filter((r) => r.name.trim() && r.description)
        .map((r) => [r.name.trim(), r.description] as const),
    )
    emit(
      'update:outputSchema',
      derived.schema.map((f) => ({ ...f, description: descOf.get(f.name) ?? f.description })),
    )
  }
}

function onMapFieldInput(index: number, field: keyof MappingRow, value: string): void {
  const row = mappingRows.value[index]
  if (!row) return
  row[field] = value
  syncMappingToConfig()
}

function addMappingRow(): void {
  mappingRows.value = [...mappingRows.value, { name: '', ref: '', description: '' }]
}

function removeMappingRow(index: number): void {
  mappingRows.value = mappingRows.value.filter((_, i) => i !== index)
  syncMappingToConfig()
}

/** 模式切换（互斥）：映射 ← → 高级模板，切换即清另一形态 config 键（后端并存拒）；
 *  切到高级保留当前 outputSchema 不动（手写 schema / 历史值不误清，JSON 模式可改） */
function switchEndMode(next: 'mapping' | 'advanced'): void {
  if (next === endMode.value) return
  endMode.value = next
  const cfg = props.node?.data?.config
  if (!cfg || props.readonly) return
  if (next === 'mapping') {
    delete cfg.output
    syncMappingToConfig()
  } else {
    delete cfg.output_fields
    mappingError.value = ''
  }
}

/** 连线信息展示：source → target（FR-001 删除前的身份确认） */
const edgeLabel = computed(
  () => `${props.edge?.source ?? ''} → ${props.edge?.target ?? ''}`,
)

// ---- Key 编辑（FR-003）：draft + 校验拦截，合法才 emit 由画布级联 ----

const keyDraft = ref('')

/** 选中节点切换时同步 draft（含改名成功后节点对象换新） */
watch(
  () => props.node?.id,
  (id) => {
    keyDraft.value = id ?? ''
  },
  { immediate: true },
)

/** 起始节点标识（改 key 迁移起始指向前的提示） */
const isStartKey = computed(
  () => !!props.node && props.node.id === props.startNodeKey,
)

/** 「设为起始」按钮（三入口之一）：emit 由画布 onSetStart 改 startKey 单源 */
function onSetStartClick(): void {
  if (props.node && !isStartKey.value) emit('set-start', props.node.id)
}

/** Key 校验（对齐 JSON 模式既有校验）：非空 / ≤64 / 不与画布现存 key 冲突 */
const keyError = computed(() => {
  const v = keyDraft.value.trim()
  if (!v) return 'Key 不能为空'
  if (v.length > 64) return 'Key 不能超过 64 个字符'
  const currentId = props.node?.id
  if (props.graph?.nodes.some((n) => n.id === v && n.id !== currentId)) {
    return `Key 已存在（${v}）`
  }
  return ''
})

/** 提交改名（change 事件）：校验不过回退 draft 原值；同值幂等不 emit */
function commitKey(): void {
  const newKey = keyDraft.value.trim()
  const oldKey = props.node?.id
  if (!oldKey) return
  if (keyError.value || newKey === oldKey) {
    keyDraft.value = oldKey
    return
  }
  emit('rename-node-key', oldKey, newKey)
}

// ---- 变量源计算（FR-007，data-model §2）：input ∪ upstream ∪ subflow-output ----

/** 子流程 schema 缓存条目（决策 4 出参展开 + 决策 8 入参渲染共用一次详情拉取） */
interface SubflowSchemas {
  input: SchemaField[]
  output: SchemaField[]
}

/** 子流程 schema 会话级缓存（contracts §6：Map<id, SubflowSchemas>，不进 store） */
const subflowSchemaCache = new Map<string, SubflowSchemas>()
/** 拉取中的子流程 id（不可变替换 Set 触发响应） */
const subflowLoadingIds = ref<ReadonlySet<string>>(new Set())
/** 缓存写入版本号（缓存非响应式，bump 驱动 variableGroups 重算） */
const cacheVersion = ref(0)

/** 拉取子流程 schema 进会话缓存（幂等：已缓存/拉取中跳过） */
function ensureSubflowSchema(id: string): void {
  if (!id || subflowSchemaCache.has(id)) return
  if (subflowLoadingIds.value.has(id)) return
  subflowLoadingIds.value = new Set([...subflowLoadingIds.value, id])
  void getWorkflowDetail(id)
    .then((d) => {
      subflowSchemaCache.set(id, {
        input: d.input_schema ?? [],
        output: d.output_schema ?? [],
      })
    })
    .catch(() => {
      // 拦截器已提示；失败不缓存（下次选中再试），出参分组暂不展开
    })
    .finally(() => {
      const next = new Set(subflowLoadingIds.value)
      next.delete(id)
      subflowLoadingIds.value = next
      cacheVersion.value++
    })
}

/** 选中节点的祖先节点（ancestorsOf 反向 BFS，不含自身） */
const ancestorNodes = computed(() => {
  const nodes = props.graph?.nodes ?? []
  const selectedKey = props.node?.id ?? ''
  if (!selectedKey) return []
  const anc = ancestorsOf(nodes, props.graph?.edges ?? [], selectedKey)
  return nodes.filter((n) => anc.has(n.id))
})

/** 祖先中的 workflow 节点所绑子流程 id（subflow-output 展开源） */
const ancestorWorkflowIds = computed(() =>
  ancestorNodes.value
    .map((n) => (n.data?.nodeType === 'workflow' ? n.data.config.workflow_id : undefined))
    .filter((v): v is string => typeof v === 'string' && !!v),
)

// 祖先子流程变更即预取 output_schema（会话缓存命中零请求）
watch(
  ancestorWorkflowIds,
  (ids) => ids.forEach(ensureSubflowSchema),
  { immediate: true },
)

const variableGroups = computed<VariableGroups>(() => {
  cacheVersion.value // 依赖缓存写入
  const groups: VariableGroups = []

  // 入参：{{input}} 恒在；task 型按宿主 input_schema 逐字段展开
  const inputOptions: VariableOption[] = [
    { insert: '{{input}}', label: 'input', group: 'input' },
  ]
  if (props.graphKind === 'task') {
    for (const f of props.inputSchema ?? []) {
      const name = f.name.trim()
      if (!name) continue
      inputOptions.push({
        insert: `{{input.${name}}}`,
        label: `input.${name}`,
        group: 'input',
      })
    }
  }
  groups.push({ group: '入参', options: inputOptions })

  // 上游节点：祖先 key 逐个一行；声明了输出字段的 llm 祖先在整体条目后展开字段条目
  //（FR-004：{{key.field}} 一级下钻可插、label 标注类型与必填；整体条目保留，
  //  未声明 llm 与非 llm 祖先条目形态零变化）
  const upstreamOptions: VariableOption[] = []
  for (const n of ancestorNodes.value) {
    upstreamOptions.push({ insert: `{{${n.id}}}`, label: n.id, group: 'upstream' })
    if (n.data?.nodeType !== 'llm') continue
    const declared = (n.data.config as Partial<LLMNodeConfig>).output_schema
    if (!Array.isArray(declared)) continue
    for (const f of declared) {
      const name = f.name.trim()
      if (!name) continue
      upstreamOptions.push({
        insert: `{{${n.id}.${name}}}`,
        label: `${n.id}.${name}（${f.type}·${f.required ? '必填' : '可选'}）`,
        group: 'upstream',
      })
    }
  }
  if (upstreamOptions.length > 0) {
    groups.push({ group: '上游节点', options: upstreamOptions })
  }

  // 子流程出参：祖先 workflow 节点 × 其子流程 output_schema 字段
  const subflowOptions: VariableOption[] = []
  for (const n of ancestorNodes.value) {
    if (n.data?.nodeType !== 'workflow') continue
    const wfId = n.data.config.workflow_id
    if (typeof wfId !== 'string' || !wfId) continue
    for (const f of subflowSchemaCache.get(wfId)?.output ?? []) {
      const name = f.name.trim()
      if (!name) continue
      subflowOptions.push({
        insert: `{{${n.id}.${name}}}`,
        label: `${n.id}.${name}`,
        group: 'subflow-output',
      })
    }
  }
  const subflowLoading = ancestorWorkflowIds.value.some(
    (id) => !subflowSchemaCache.has(id) && subflowLoadingIds.value.has(id),
  )
  if (subflowOptions.length > 0 || subflowLoading) {
    groups.push({ group: '子流程出参', options: subflowOptions, loading: subflowLoading })
  }

  return groups
})

/** API 节点 method 合法值（后端 ApiCallConfig：GET/POST/PUT/DELETE/PATCH 大写） */
const API_METHODS = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH'] as const

// ---- API headers KV + Auth 预设（FR-005，research 决策 6）：config.headers 是唯一事实源 ----

/** Headers 键值行（UI 编辑面；空键行不产出 config 条目，重复键后写覆盖） */
interface HeaderRow {
  key: string
  value: string
}

const headerRows = ref<HeaderRow[]>([])

/** Auth 区草稿（推导缓存，非独立状态源——写入一律经 applyAuthToHeaders 回 config.headers） */
const authPreset = ref<AuthPreset>('none')
const bearerToken = ref('')
const basicUser = ref('')
const basicPass = ref('')

/** Authorization 键归 Auth 区管（HTTP 头名大小写不敏感） */
const AUTH_HEADER_KEY = 'Authorization'

function isAuthHeader(key: string): boolean {
  return key.trim().toLowerCase() === 'authorization'
}

/** 选中切换：从 config.headers 重建行并推导 Auth 区（非 api 节点 / 非对象 → 空行） */
function rebuildApiForm(): void {
  const headers = props.node?.data?.config?.headers
  if (nodeType.value !== 'api' || typeof headers !== 'object' || headers === null) {
    headerRows.value = []
  } else {
    headerRows.value = Object.entries(headers).map(([key, value]) => ({
      key,
      value: typeof value === 'string' ? value : String(value),
    }))
  }
  syncAuthFromRaw()
}

/** 行编辑回写 config.headers：空键行不产出；重复键后写覆盖；全空删键（omitempty 语义）。
 *  fromAuth = Auth 区写入路径——跳过反向推导防环（决策 6：用户手输 Authorization 行才随动）。 */
function syncHeadersToConfig(fromAuth = false): void {
  const cfg = props.node?.data?.config
  if (!cfg) return
  const entries = headerRows.value.filter((r) => r.key.trim())
  if (entries.length === 0) delete cfg.headers
  else {
    cfg.headers = Object.fromEntries(entries.map((r) => [r.key.trim(), r.value]))
  }
  if (!fromAuth) syncAuthFromRaw()
}

/** Authorization 现值 → Auth 区草稿（只读推导不回写；非 Bearer/Basic 前缀 → none，值留 KV 行） */
function syncAuthFromRaw(): void {
  const row = headerRows.value.find((r) => isAuthHeader(r.key))
  const draft = parseAuthorization(row?.value ?? '')
  authPreset.value = draft.preset
  bearerToken.value = draft.token
  basicUser.value = draft.user
  basicPass.value = draft.pass
}

/** Auth 区写入：先移除旧 Authorization 行再注入新行（切换预设零残留）；none = 只删不注入 */
function applyAuthToHeaders(): void {
  const rows = headerRows.value.filter((r) => !isAuthHeader(r.key))
  if (authPreset.value !== 'none') {
    rows.push({
      key: AUTH_HEADER_KEY,
      value: formatAuthorization({
        preset: authPreset.value,
        token: bearerToken.value,
        user: basicUser.value,
        pass: basicPass.value,
      }),
    })
  }
  headerRows.value = rows
  syncHeadersToConfig(true)
}

/** 预设下拉（仅用户交互触发——单向绑定，程序推导不回灌事件） */
function applyAuthPreset(preset: AuthPreset): void {
  authPreset.value = preset
  applyAuthToHeaders()
}

function onBearerInput(value: string): void {
  bearerToken.value = value
  applyAuthToHeaders()
}

function onBasicInput(field: 'user' | 'pass', value: string): void {
  if (field === 'user') basicUser.value = value
  else basicPass.value = value
  applyAuthToHeaders()
}

/** 行内编辑（key / 值即时回写）：值域是 TemplateField，逐键同步 */
function onHeaderFieldInput(index: number, field: 'key' | 'value', value: string): void {
  const row = headerRows.value[index]
  if (!row) return
  row[field] = value
  syncHeadersToConfig()
}

function addHeaderRow(): void {
  headerRows.value.push({ key: '', value: '' })
}

function removeHeaderRow(index: number): void {
  headerRows.value.splice(index, 1)
  syncHeadersToConfig()
}

// 选中节点切换即重建 headers 行与 Auth 区（config.headers 引用共享，行编辑不换节点对象）
// + end 映射模式行集（spec 018）
watch(
  () => props.node,
  () => {
    rebuildApiForm()
    rebuildEndForm()
  },
  { immediate: true },
)

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
      // 前端过滤 task 型（嵌套目标仅 task）+ 排除工作流自身（FR-006 编辑态防自引用）
      .filter((w) => w.type === 'task' && (!props.selfWorkflowId || w.id !== props.selfWorkflowId))
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

// ---- workflow 节点入参渲染（FR-006，research 决策 8）：行集来自子流程 input_schema ----

/** 选中 workflow 节点所绑子流程的入参 schema（缓存命中即时；未拉到回空集） */
const subflowInputFields = computed<SchemaField[]>(() => {
  cacheVersion.value // 依赖缓存写入
  if (nodeType.value !== 'workflow') return []
  const wfId = String(workflowId.value ?? '')
  if (!wfId) return []
  return subflowSchemaCache.get(wfId)?.input ?? []
})

/** 入参行区域加载中（子流程 schema 拉取中） */
const subflowInputLoading = computed(() => {
  const wfId = nodeType.value === 'workflow' ? String(workflowId.value ?? '') : ''
  return !!wfId && !subflowSchemaCache.has(wfId) && subflowLoadingIds.value.has(wfId)
})

/** 待收窄的子流程 id：同节点切换子流程后，等 schema 到位按字段名保留同名旧值 */
const pendingRestrictWfId = ref('')

/** 切换子流程的重建（决策 8）：只保留新 schema 同名字段的旧值，其余丢弃 */
function restrictInputsToSchema(fields: SchemaField[]): void {
  const cfg = props.node?.data?.config
  if (!cfg) return
  const inputs = cfg.inputs
  if (typeof inputs !== 'object' || inputs === null) return
  const names = new Set(fields.map((f) => f.name.trim()).filter(Boolean))
  const kept = Object.fromEntries(
    Object.entries(inputs).filter(([k]) => names.has(k)),
  )
  if (Object.keys(kept).length === 0) delete cfg.inputs
  else cfg.inputs = kept
}

/** schema 到位后收窄（缓存命中立即、拉取完成经 cacheVersion 触发） */
function maybeRestrictInputs(): void {
  const wfId = pendingRestrictWfId.value
  if (!wfId) return
  const schemas = subflowSchemaCache.get(wfId)
  if (!schemas) return // 仍在拉取，cacheVersion 再触发
  pendingRestrictWfId.value = ''
  if (nodeType.value !== 'workflow' || String(workflowId.value ?? '') !== wfId) return
  restrictInputsToSchema(schemas.input)
}

/** 子流程选择态（节点 id + workflow_id）：只在「同一节点内改 workflow_id」时算切换子流程——
 *  仅切换选中节点不动既有 inputs（未知键透传） */
const subflowSelection = computed(() =>
  props.node
    ? `${props.node.id}\x00${nodeType.value === 'workflow' ? String(workflowId.value ?? '') : ''}`
    : '',
)

let lastSubflowSelection: { nodeId: string; wfId: string } | null = null

watch(
  subflowSelection,
  () => {
    const nodeId = props.node?.id ?? ''
    const wfId = nodeType.value === 'workflow' ? String(workflowId.value ?? '') : ''
    const prev = lastSubflowSelection
    lastSubflowSelection = { nodeId, wfId }
    if (!wfId) return
    ensureSubflowSchema(wfId)
    if (prev && prev.nodeId === nodeId && prev.wfId !== wfId) {
      pendingRestrictWfId.value = wfId
      maybeRestrictInputs()
    }
  },
  { immediate: true },
)

watch(cacheVersion, () => maybeRestrictInputs())

/** 入参值读（config.inputs[field]；非字符串回空） */
function subflowInputValue(name: string): string {
  const inputs = props.node?.data?.config?.inputs
  if (typeof inputs !== 'object' || inputs === null) return ''
  const v = (inputs as Record<string, unknown>)[name]
  return typeof v === 'string' ? v : ''
}

/** 入参值写（config.inputs[field]）：空值删键、全空删 inputs 键（omitempty 语义） */
function setSubflowInputValue(name: string, value: string): void {
  const cfg = props.node?.data?.config
  if (!cfg) return
  const current = cfg.inputs
  const inputs: Record<string, unknown> =
    typeof current === 'object' && current !== null
      ? { ...(current as Record<string, unknown>) }
      : {}
  if (value) inputs[name] = value
  else delete inputs[name]
  if (Object.keys(inputs).length === 0) delete cfg.inputs
  else cfg.inputs = inputs
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

/* Key 校验错误提示与非法态输入框 */
.node-inspector__error {
  margin: 0;
  color: var(--hf-danger);
  font-size: var(--hf-font-size-xs);
  line-height: var(--hf-leading-normal);
}

.node-inspector__key-input--invalid :deep(.el-input__wrapper) {
  box-shadow: 0 0 0 1px var(--hf-danger) inset;
}

/* 动作区（节点 / 连线共用）：贴面板底部，按钮横排（删除节点 / 删除连线） */
.node-inspector__actions {
  margin-top: auto;
  display: flex;
  gap: var(--hf-space-2);
}

/* 键值行基础（headers 用）：key + value + 删除 */
.node-inspector__kv {
  display: flex;
  align-items: center;
  gap: var(--hf-space-1);
}

/* headers 键值行：值域是 TemplateField（下拉 + 文本域），顶部对齐、名称列定宽 */
.node-inspector__kv--stack {
  align-items: flex-start;
}

.node-inspector__kv-name {
  flex: 0 0 84px;
}

.node-inspector__kv-value {
  flex: 1;
  min-width: 0;
}

/* 子工作流入参行（FR-006）：schema 字段名只读标签 + 模板值域 */
.node-inspector__subflow-input {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-1);
}

.node-inspector__subflow-name {
  color: var(--hf-text-1);
  font-size: var(--hf-font-size-xs);
  word-break: break-all;
}

/* end 映射行（spec 018）：字段名 / 引用变量 / 类型（只读）/ 描述 / 删除 */
.node-inspector__map-grid {
  display: grid;
  grid-template-columns: minmax(64px, 1fr) minmax(80px, 1.2fr) 56px minmax(60px, 1fr) 28px;
  gap: var(--hf-space-1);
  align-items: center;
}

.node-inspector__map-header {
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-xs);
}

.node-inspector__map-row {
  margin-bottom: var(--hf-space-1);
}

.node-inspector__map-type {
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
  text-align: center;
  word-break: break-all;
}

.node-inspector__empty {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: center;
}
</style>
