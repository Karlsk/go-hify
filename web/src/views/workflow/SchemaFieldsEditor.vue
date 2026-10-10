<template>
  <!-- Schema 字段行编辑器（spec 010 US3，FR-007 Schema 表单化；spec 018 加默认值列）：
       v-model 双向 SchemaField[]，每行五控件（字段名 / 类型下拉三值 / 必填开关 /
       默认值〔仅 optional 行，按类型分型控件；boolean 三态下拉〕/ 描述）+ 行删除，
       底部「添加字段」追加空行。required 置位即清默认（后端 required+default 保存期
       拒，编辑期先行）；类型切换即清默认（值型随 type）。非法历史数据（回填 type 不在
       三值内）由 el-select 原值显示语义兜底（显示原字符串不炸，重新选择即修正——
       Edge Case，research #8）。组件本体不做提交校验：校验由宿主页提交前调 graph.ts
       schemaFieldsError（错误文案含行号，SC-004）。行编辑一律 emit 新数组（不可变更新）。 -->
  <div class="schema-fields-editor">
    <div v-if="modelValue.length > 0" class="schema-fields-editor__header">
      <span>字段名</span>
      <span>类型</span>
      <span class="schema-fields-editor__center">必填</span>
      <span>默认值</span>
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
        @update:model-value="(v: string) => setType(index, v as SchemaField['type'])"
      >
        <el-option label="string" value="string" />
        <el-option label="number" value="number" />
        <el-option label="boolean" value="boolean" />
      </el-select>
      <div class="schema-fields-editor__center">
        <el-switch
          :model-value="field.required"
          :disabled="disabled"
          @update:model-value="(v: string | number | boolean) => setRequired(index, Boolean(v))"
        />
      </div>
      <template v-if="!field.required">
        <el-input
          v-if="field.type === 'string'"
          :model-value="typeof field.default === 'string' ? field.default : ''"
          :disabled="disabled"
          placeholder="空 = 无默认"
          @update:model-value="(v: string) => updateField(index, { default: v || undefined })"
        />
        <el-input-number
          v-else-if="field.type === 'number'"
          :model-value="typeof field.default === 'number' ? field.default : undefined"
          :disabled="disabled"
          style="width: 100%"
          @update:model-value="(v: number | undefined) => updateField(index, { default: v })"
        />
        <el-select
          v-else-if="field.type === 'boolean'"
          :model-value="typeof field.default === 'boolean' ? String(field.default) : ''"
          :disabled="disabled"
          placeholder="无"
          @update:model-value="(v: string) => updateField(index, { default: v === '' ? undefined : v === 'true' })"
        >
          <el-option label="无" value="" />
          <el-option label="true" value="true" />
          <el-option label="false" value="false" />
        </el-select>
        <span v-else class="schema-fields-editor__center">—</span>
      </template>
      <span v-else class="schema-fields-editor__center">—</span>
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

/** 类型切换即清默认（spec 018：值型随 type，残留异型默认会被保存期校验拒） */
function setType(index: number, type: SchemaField['type']): void {
  updateField(index, { type, default: undefined })
}

/** required 置位即清默认（spec 018 FR-006：required 禁配 default，编辑期先行） */
function setRequired(index: number, required: boolean): void {
  updateField(index, required ? { required, default: undefined } : { required })
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
  grid-template-columns: minmax(110px, 1.2fr) 108px 64px minmax(120px, 1fr) minmax(140px, 1.8fr) 40px;
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
