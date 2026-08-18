<template>
  <!-- 通用表单弹窗：v-model 控显隐；open(data?) 区分编辑/新增。
       组件内部持有 formData 副本，打开时重建 → 关闭即自动重置。
       默认插槽 scoped：#default="{ form }" 渲染 el-form-item。 -->
  <el-dialog
    :model-value="modelValue"
    :title="title"
    :width="width"
    @update:model-value="(v: boolean) => emit('update:modelValue', v)"
  >
    <el-form
      ref="formRef"
      :model="formData"
      :rules="rules"
      :label-width="labelWidth"
    >
      <slot :form="formData" />
    </el-form>
    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">
        确定
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts" generic="T extends object">
import { nextTick, ref, watch, type Ref } from 'vue'
import type { FormInstance, FormRules } from 'element-plus'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    title: string
    /** 空模型工厂：新增模式的重置基准（也作编辑浅拷贝的来源类型） */
    initialModel: () => T
    rules?: FormRules
    width?: string
    labelWidth?: string
  }>(),
  { width: '520px', labelWidth: '96px' },
)

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 提交：校验通过后触发；父组件处理 API，done(true) 关弹窗 / done(false) 停 loading 保持打开 */
  submit: [form: T, done: (ok?: boolean) => void]
}>()

const formRef = ref<FormInstance>()
const formData = ref(props.initialModel()) as Ref<T>
const submitting = ref(false)
let pendingData: T | null = null

/** 打开弹窗：传 data 为编辑模式（浅拷贝），不传为新增模式（工厂新建） */
function open(data?: T): void {
  pendingData = data ?? null
  emit('update:modelValue', true)
}

watch(
  () => props.modelValue,
  (visible) => {
    if (visible) {
      formData.value = pendingData ? { ...pendingData } : { ...props.initialModel() }
      pendingData = null
      void nextTick(() => formRef.value?.clearValidate())
    } else {
      // 关闭自动重置：清空校验态与提交 loading（数据副本下次打开时重建）
      submitting.value = false
      formRef.value?.clearValidate()
    }
  },
)

async function submit(): Promise<void> {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return // 校验失败：字段级错误由 el-form 展示
  }
  submitting.value = true
  emit('submit', formData.value, (ok = true) => {
    submitting.value = false
    if (ok) emit('update:modelValue', false)
  })
}

function close(): void {
  emit('update:modelValue', false)
}

defineExpose({ open })
</script>
