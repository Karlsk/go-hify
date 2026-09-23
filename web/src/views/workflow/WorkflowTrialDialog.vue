<template>
  <!-- 试运行对话框（spec 013 US2）：编辑页 / 详情页双入口共用（两步式创建无入口）。
       目标恒为已落库版本（两入口 detail 均来自 GET——「跑已保存版本」由数据源
       结构性保证，非运行时快照）。入参三态（FR-004）：A chat 单文本框 /
       B task 有 schema 字段行 / C task 无 schema 单文本框直传；
       执行（FR-005）：running 短路防重复点击。 -->
  <el-dialog
    :model-value="modelValue"
    :title="workflow ? `试运行：${workflow.name}` : '试运行'"
    width="720px"
    :close-on-click-modal="false"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template v-if="workflow">
      <!-- A / C 态：单文本框（16384 上限，后端 binding 同值双拦） -->
      <el-input
        v-if="mode !== 'schema'"
        v-model="inputText"
        type="textarea"
        :rows="4"
        maxlength="16384"
        show-word-limit
        :placeholder="mode === 'chat' ? '模拟用户消息' : '输入文本（无入参 Schema，原样传入）'"
      />
      <!-- B 态：每 SchemaField 一行（string/number/boolean 分型控件；
           动态键无 v-model——NodeInspector 同式类型收窄读 + 显式写） -->
      <el-form
        v-else
        ref="formRef"
        :model="fieldValues"
        :rules="fieldRules"
        label-width="120px"
        scroll-to-error
      >
        <el-form-item
          v-for="f in schemaFields"
          :key="f.name"
          :label="f.name"
          :prop="f.name"
        >
          <el-input
            v-if="f.type === 'string'"
            :model-value="fieldText(f.name)"
            @update:model-value="setFieldValue(f.name, $event)"
          />
          <el-input-number
            v-else-if="f.type === 'number'"
            :model-value="fieldNumber(f.name)"
            style="width: 100%"
            @update:model-value="setFieldValue(f.name, $event)"
          />
          <el-switch
            v-else
            :model-value="fieldBoolean(f.name)"
            @update:model-value="setFieldValue(f.name, $event)"
          />
          <p v-if="f.description" class="trial-field-desc">{{ f.description }}</p>
        </el-form-item>
      </el-form>

      <!-- 结果区（FR-006/007）：打开期间改参再执行不重置，新结果完成时刷新。
           失败两面：运行失败（HTTP 200 + status:"failed"）alert 文案取轨迹尾部
           error_msg、轨迹表照常展示（中断点可见）；请求失败（信封 reject）
           alert 持久展示信封 message——拦截器 toast 瞬时提示照常并存 -->
      <section v-if="result" class="trial-result">
        <el-alert
          v-if="result.status === 'failed'"
          type="error"
          :title="failMessage"
          :closable="false"
          show-icon
          class="trial-alert"
        />
        <template v-else>
          <h4 class="trial-section-title">输出</h4>
          <pre class="trial-output">{{ result.output }}</pre>
        </template>
        <h4 class="trial-section-title">
          执行轨迹
          <span class="trial-total">总耗时 {{ result.duration_ms }} ms</span>
        </h4>
        <el-table :data="result.node_trace" size="small" border>
          <el-table-column prop="node_key" label="节点" min-width="120" />
          <el-table-column prop="node_type" label="类型" width="110" />
          <el-table-column label="状态" width="90" align="center">
            <template #default="{ row }">
              <el-tag
                :type="row.status === 'succeeded' ? 'success' : 'danger'"
                size="small"
              >
                {{ row.status }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="耗时" width="90" align="right">
            <template #default="{ row }">{{ row.duration_ms }} ms</template>
          </el-table-column>
          <el-table-column label="错误" min-width="160">
            <template #default="{ row }">
              <span :class="{ 'trial-error-text': !!row.error_msg }">
                {{ row.error_msg || '—' }}
              </span>
            </template>
          </el-table-column>
        </el-table>
      </section>
      <!-- 请求失败（信封 reject）：持久展示（toast 瞬时、alert 持久，不遮蔽） -->
      <el-alert
        v-else-if="errorMsg"
        type="error"
        :title="errorMsg"
        :closable="false"
        show-icon
        class="trial-result trial-alert"
      />
    </template>

    <template #footer>
      <el-button :disabled="running" @click="emit('update:modelValue', false)">
        关闭
      </el-button>
      <el-button type="primary" :loading="running" @click="run">执行</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { FormInstance, FormRules } from 'element-plus'
import {
  executeWorkflow,
  type SchemaField,
  type WorkflowDetail,
  type WorkflowRunResult,
} from '@/api/workflow'
import { notifyError } from '@/utils/notify'

/** 输入长度上限（后端 ExecuteWorkflowReq binding max=16384，前端同值双拦） */
const INPUT_MAX = 16384

const props = defineProps<{
  /** v-model：对话框开关 */
  modelValue: boolean
  /** 试运行目标（已落库版本）；null 时内容不渲染（防御 detail 未就绪时点开） */
  workflow: Pick<WorkflowDetail, 'id' | 'name' | 'type' | 'input_schema'> | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()

/** 入参三态（FR-004）：A chat / B task 有 schema / C task 无 schema */
const mode = computed<'chat' | 'schema' | 'plain'>(() => {
  if (!props.workflow) return 'plain'
  if (props.workflow.type === 'chat') return 'chat'
  return (props.workflow.input_schema?.length ?? 0) > 0 ? 'schema' : 'plain'
})

// ---- 状态（watch 打开时全量重置；打开期间不重置 result——改参再执行场景） ----

const inputText = ref('')
/** B 态字段行值（number 未填 = undefined，JSON 组装时该键自然省略） */
const fieldValues = ref<Record<string, string | number | boolean | undefined>>({})
const formRef = ref<FormInstance>()
const result = ref<WorkflowRunResult | null>(null)
/** 请求失败（信封 reject）信封 message；运行失败不写这里（走 result.status） */
const errorMsg = ref('')
const running = ref(false)

/** 运行失败文案（D7）：node_trace 执行序最后一条非空 error_msg，无则兜底 */
const failMessage = computed(() => {
  const trace = result.value?.node_trace ?? []
  for (let i = trace.length - 1; i >= 0; i--) {
    if (trace[i].error_msg) return trace[i].error_msg
  }
  return '执行失败'
})

/** B 态 schema 字段行 */
const schemaFields = computed<SchemaField[]>(() => props.workflow?.input_schema ?? [])

/** B 态必填 rules（string 非空 / number 必填；boolean 恒有值不设规则） */
const fieldRules = computed<FormRules>(() => {
  const rules: FormRules = {}
  for (const f of schemaFields.value) {
    if (!f.required || f.type === 'boolean') continue
    rules[f.name] = [
      {
        required: true,
        message: `请输入 ${f.name}`,
        trigger: f.type === 'string' ? 'blur' : 'change',
      },
    ]
  }
  return rules
})

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    inputText.value = ''
    fieldValues.value = {}
    result.value = null
    errorMsg.value = ''
    running.value = false
    void nextTick(() => formRef.value?.clearValidate())
  },
)

// ---- B 态字段行读写 ----

function fieldText(name: string): string {
  const v = fieldValues.value[name]
  return typeof v === 'string' ? v : ''
}

function fieldNumber(name: string): number | undefined {
  const v = fieldValues.value[name]
  return typeof v === 'number' ? v : undefined
}

function fieldBoolean(name: string): boolean {
  return fieldValues.value[name] === true
}

function setFieldValue(name: string, value: string | number | boolean | undefined): void {
  fieldValues.value = { ...fieldValues.value, [name]: value }
}

// ---- 执行（FR-005：校验全不发请求；running 短路防重复点击） ----

async function run(): Promise<void> {
  if (running.value || !props.workflow) return
  let input: string
  if (mode.value === 'schema') {
    const form = formRef.value
    if (!form) return
    try {
      await form.validate()
    } catch {
      return // scroll-to-error 已定位首个缺失行
    }
    input = JSON.stringify(
      Object.fromEntries(schemaFields.value.map((f) => [f.name, fieldValues.value[f.name]])),
    )
  } else {
    const text = inputText.value.trim()
    if (!text) {
      notifyError('请输入消息内容')
      return
    }
    input = text
  }
  if (input.length > INPUT_MAX) {
    notifyError('输入超出 16384 字符上限')
    return
  }
  running.value = true
  try {
    result.value = await executeWorkflow(props.workflow.id, input)
    errorMsg.value = ''
  } catch (e) {
    // 请求级失败：写持久 alert（拦截器 toast 照常弹，并存不遮蔽）
    result.value = null
    errorMsg.value = e instanceof Error ? e.message : '执行失败'
  } finally {
    running.value = false
  }
}
</script>

<style scoped>
/* 结果区与入参区拉开距离 */
.trial-result {
  margin-top: var(--hf-space-3);
}

.trial-alert {
  margin-bottom: var(--hf-space-3);
}

/* 轨迹表失败行错误红字 */
.trial-error-text {
  color: var(--hf-danger);
}

/* 总耗时附注（执行轨迹标题行内） */
.trial-total {
  margin-left: var(--hf-space-2);
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
  font-weight: 400;
}

.trial-section-title {
  margin: 0 0 var(--hf-space-2);
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-sm);
  font-weight: 600;
}

/* output 多行保留（pre-wrap）+ 限高滚动 */
.trial-output {
  margin: 0 0 var(--hf-space-3);
  padding: var(--hf-space-2);
  max-height: 240px;
  overflow-y: auto;
  white-space: pre-wrap;
  word-break: break-word;
  border: 1px solid var(--hf-border-1);
  border-radius: var(--hf-radius-md);
  background: var(--hf-bg-container);
  color: var(--hf-text-1);
  font-size: var(--hf-font-size-sm);
}

/* B 态字段 description 行内提示 */
.trial-field-desc {
  margin: var(--hf-space-1) 0 0;
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
}
</style>
