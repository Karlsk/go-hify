<template>
  <!-- Schema 字段行编辑器（spec 010 US3，FR-007 Schema 表单化）：v-model 双向
       SchemaField[]，每行四控件（字段名 / 类型下拉三值 / 必填开关 / 描述）+ 行删除，
       底部「添加字段」追加空行。非法历史数据（回填 type 不在三值内）由 el-select
       原值显示语义兜底（显示原字符串不炸，重新选择即修正——Edge Case，research #8）。
       组件本体不做提交校验：校验由宿主页提交前调 graph.ts schemaFieldsError
       （错误文案含行号，SC-004）。行编辑一律 emit 新数组（不可变更新）。 -->
  <div class="schema-fields-editor">
    <div v-if="modelValue.length > 0" class="schema-fields-editor__header">
      <span>字段名</span>
      <span>类型</span>
      <span class="schema-fields-editor__center">必填</span>
      <span>描述</span>
      <span><!-- 删除列占位 --></span>
    </div>

    <div
      v-for="(field, index) in modelValue"
      :key="index"
      class="schema-fields-editor__row"
    >
      <el-input
        :model-value="field.name"
        :disabled="disabled"
        placeholder="字段名"
        @update:model-value="(v: string) => updateField(index, { name: v })"
      />
      <el-select
        :model-value="field.type"
        :disabled="disabled"
        placeholder="类型"
        @update:model-value="(v: string) => updateField(index, { type: v as SchemaField['type'] })"
      >
        <el-option label="string" value="string" />
        <el-option label="number" value="number" />
        <el-option label="boolean" value="boolean" />
      </el-select>
      <div class="schema-fields-editor__center">
        <el-switch
          :model-value="field.required"
          :disabled="disabled"
          @update:model-value="(v: string | number | boolean) => updateField(index, { required: Boolean(v) })"
        />
      </div>
      <el-input
        :model-value="field.description ?? ''"
        :disabled="disabled"
        placeholder="描述（可选）"
        @update:model-value="(v: string) => updateField(index, { description: v })"
      />
      <el-button
        v-if="!disabled"
        link
        type="danger"
        @click="removeField(index)"
      >
        <el-icon><Delete /></el-icon>
      </el-button>
      <span v-else><!-- 只读态删除列占位 --></span>
    </div>

    <div v-if="modelValue.length === 0" class="schema-fields-editor__empty">
      暂无字段，点击下方「添加字段」新增
    </div>

    <el-button v-if="!disabled" plain :disabled="disabled" @click="addField">
      <el-icon><Plus /></el-icon>
      添加字段
    </el-button>
  </div>
</template>

<script setup lang="ts">
import { Delete, Plus } from '@element-plus/icons-vue'
import type { SchemaField } from '@/api/workflow'

const props = defineProps<{
  /** 字段行数组（v-model；行编辑 emit 新数组，不改传入引用） */
  modelValue: SchemaField[]
  /** 禁用态：全部控件只读、删除与添加隐藏 */
  disabled?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: SchemaField[]]
}>()

// ---- 行编辑（不可变更新：map 出新数组，仅目标行替换为新对象） ----

function updateField(index: number, patch: Partial<SchemaField>): void {
  emit(
    'update:modelValue',
    props.modelValue.map((f, i) => (i === index ? { ...f, ...patch } : f)),
  )
}

function addField(): void {
  emit('update:modelValue', [
    ...props.modelValue,
    { name: '', type: 'string', required: false, description: '' },
  ])
}

function removeField(index: number): void {
  emit(
    'update:modelValue',
    props.modelValue.filter((_, i) => i !== index),
  )
}
</script>

<style scoped>
.schema-fields-editor {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-2);
  width: 100%;
}

.schema-fields-editor__header,
.schema-fields-editor__row {
  display: grid;
  grid-template-columns: minmax(120px, 1.2fr) 128px 72px minmax(160px, 2fr) 40px;
  gap: var(--hf-space-2);
  align-items: center;
}

.schema-fields-editor__header {
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-xs);
}

.schema-fields-editor__center {
  text-align: center;
}

.schema-fields-editor__empty {
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-sm);
}
</style>
