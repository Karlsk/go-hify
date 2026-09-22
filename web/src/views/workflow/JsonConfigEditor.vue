<template>
  <!-- JSON 配置编辑器：textarea（等宽字体）+ 下方「格式化」按钮（用户原始需求：
       编辑器下方放格式化按钮，点击美化缩进）。校验态经 defineExpose 暴露，
       供父组件在提交与切换模式前判定；错误提示走 notifyError（本地校验场景，
       请求类错误才归拦截器）。 -->
  <div class="json-config-editor">
    <el-input
      :model-value="modelValue"
      type="textarea"
      :rows="rows"
      :placeholder="placeholder"
      class="json-config-editor__textarea"
      spellcheck="false"
      @update:model-value="onInput"
    />
    <div class="json-config-editor__footer">
      <el-button size="small" @click="format">格式化</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { notifyError } from '@/utils/notify'

const props = withDefaults(
  defineProps<{
    /** 编辑器文本（图配置或 schema 数组的 JSON 序列化形态） */
    modelValue: string
    rows?: number
    placeholder?: string
  }>(),
  { rows: 12 },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function onInput(value: string): void {
  emit('update:modelValue', value)
}

/** JSON 语法校验（含空文本检查）；SyntaxError.message 自带位置信息 */
function validate(): { ok: boolean; error: string } {
  if (!props.modelValue.trim()) return { ok: false, error: 'JSON 内容为空' }
  try {
    JSON.parse(props.modelValue)
    return { ok: true, error: '' }
  } catch (e) {
    return { ok: false, error: e instanceof SyntaxError ? e.message : String(e) }
  }
}

/** 格式化：合法 → 美化缩进替换原文；非法 → 提示错误（含位置）且原文不变 */
function format(): void {
  const v = validate()
  if (!v.ok) {
    notifyError(`JSON 非法：${v.error}`)
    return
  }
  const pretty = JSON.stringify(JSON.parse(props.modelValue), null, 2)
  emit('update:modelValue', pretty)
}

defineExpose({ validate })
</script>

<style scoped>
.json-config-editor__textarea :deep(textarea) {
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-sm);
  line-height: var(--hf-leading-normal);
}

.json-config-editor__footer {
  display: flex;
  justify-content: flex-end;
  margin-top: var(--hf-space-2);
}
</style>
