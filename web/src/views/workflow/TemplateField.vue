<template>
  <!-- 模板字段通用组件（FR-007，contracts §1）：textarea 主体 + 变量引用下拉。
       哑组件——变量条目由外部渲染好传入（计算在 Inspector 层，research 决策 4/5），
       无图依赖、无 api 依赖；手写零干预（不清洗/不校验/不格式化既有文本）；
       选中条目按 textarea selectionStart 光标位置插入 option.insert 完整 {{}} 文本。 -->
  <div class="template-field">
    <el-select
      v-model="picker"
      :disabled="disabled"
      placeholder="插入变量引用"
      size="small"
      class="template-field__picker"
      filterable
      @change="onPick"
    >
      <el-option-group v-for="g in variables" :key="g.group" :label="g.group">
        <el-option v-if="g.loading" :label="'加载中…'" :value="''" disabled />
        <el-option
          v-for="o in g.options"
          :key="o.insert"
          :label="o.label"
          :value="o.insert"
        />
      </el-option-group>
    </el-select>
    <el-input
      ref="inputRef"
      :model-value="modelValue"
      type="textarea"
      :rows="rows"
      :disabled="disabled"
      :placeholder="placeholder"
      @update:model-value="onInput"
    />
  </div>
</template>

<script lang="ts">
/** 变量引用条目（data-model §2）：TemplateField 只渲染不计算 */
export interface VariableOption {
  /** 插入文本，含完整 {{}} 包裹，如 "{{input}}"、"{{classify}}"、"{{fetch.result}}" */
  insert: string
  /** 展示名（下拉行主文案），如 "input.q"、classify、fetch.result */
  label: string
  /** 来源分组：入参 / 上游节点 / 子流程出参 */
  group: 'input' | 'upstream' | 'subflow-output'
}

/** 分组下拉数据源（loading：子流程出参拉取中的分组态） */
export interface VariableGroup {
  group: string
  options: VariableOption[]
  loading?: boolean
}

export type VariableGroups = VariableGroup[]
</script>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import type { InputInstance } from 'element-plus'

const props = withDefaults(
  defineProps<{
    /** 模板文本（含 {{}} 引用与手写混排） */
    modelValue: string
    /** 分组下拉数据源（外部渲染好传入） */
    variables: VariableGroups
    /** readonly 态禁用全部交互 */
    disabled?: boolean
    placeholder?: string
    /** textarea 行数，默认 3 */
    rows?: number
  }>(),
  { rows: 3 },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const inputRef = ref<InputInstance | null>(null)
/** 下拉选中值：插入后即清空（下拉是插入触发器，不是字段值的一部分） */
const picker = ref('')

function onInput(value: string): void {
  emit('update:modelValue', value)
}

/** 选中变量 → 按光标位置插入完整 {{}} 文本，恢复焦点与光标到插入点之后 */
function onPick(insert: string): void {
  picker.value = ''
  if (!insert || props.disabled) return
  const ta = inputRef.value?.textarea as HTMLTextAreaElement | undefined
  const cur = props.modelValue
  const start = ta?.selectionStart ?? cur.length
  const end = ta?.selectionEnd ?? cur.length
  emit('update:modelValue', cur.slice(0, start) + insert + cur.slice(end))
  void nextTick(() => {
    if (!ta) return
    ta.focus()
    const caret = start + insert.length
    ta.setSelectionRange(caret, caret)
  })
}
</script>

<style scoped>
.template-field {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-1);
  width: 100%;
}

.template-field__picker {
  align-self: flex-start;
  max-width: 100%;
}
</style>
