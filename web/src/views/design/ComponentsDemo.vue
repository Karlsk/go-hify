<template>
  <!-- 公共组件演示：mock api 驱动 HifyTable / HifyFormDialog / useConfirm 全链路。
       从 DesignTokens.vue 拆出（主文件已近 800 行上限）。 -->
  <section class="cd">
    <h2 class="cd__title">公共组件（HifyTable / HifyFormDialog / useConfirm）</h2>
    <p class="cd__subtitle">
      mock 数据源：表格分页 / 空态 / 行内删除确认 / 表单弹窗新增编辑，全部可点
    </p>

    <div class="cd__toolbar">
      <el-switch
        v-model="emptyMode"
        active-text="展示空态"
        @change="tableRef?.refresh()"
      />
      <el-button type="primary" @click="dialogRef?.open()">新增提供商</el-button>
    </div>

    <HifyTable
      ref="tableRef"
      :columns="columns"
      :api="fetchList"
      :page-size="5"
    >
      <template #type="{ row }">
        <el-tag :type="tagType(row.type)" size="small">{{ row.type }}</el-tag>
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click="dialogRef?.open(row)">
          编辑
        </el-button>
        <el-button link type="danger" @click="remove(row)">删除</el-button>
      </template>
    </HifyTable>

    <HifyFormDialog
      ref="dialogRef"
      v-model="dialogVisible"
      title="提供商"
      :initial-model="emptyRow"
      :rules="rules"
      @submit="onSubmit"
    >
      <template #default="{ form }">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="如 OpenAI / Claude" />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-select v-model="form.type" style="width: 100%">
            <el-option
              v-for="t in TYPES"
              :key="t"
              :label="t"
              :value="t"
            />
          </el-select>
        </el-form-item>
      </template>
    </HifyFormDialog>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { FormRules } from 'element-plus'
import HifyTable, { type HifyTableColumn } from '@/components/HifyTable.vue'
import HifyFormDialog from '@/components/HifyFormDialog.vue'
import { useConfirm } from '@/composables/useConfirm'
import { notifySuccess } from '@/utils/notify'
import type { PageQuery, PageResult } from '@/types'

interface DemoRow {
  id: number
  name: string
  type: string
  createdAt: string
}

const TYPES = ['OpenAI', 'Claude', 'Gemini', 'Ollama'] as const

const tagType = (type: string) =>
  ({
    OpenAI: 'primary',
    Claude: 'warning',
    Gemini: 'success',
    Ollama: 'info',
  } as const)[type as (typeof TYPES)[number]] ?? 'info'

// ---- mock 数据源（本地数组 + 延迟，演示 loading / 分页） ----
const seed: DemoRow[] = Array.from({ length: 12 }, (_, i) => ({
  id: i + 1,
  name: `提供商 ${String(i + 1).padStart(2, '0')}`,
  type: TYPES[i % TYPES.length],
  createdAt: `2026-08-${String((i % 27) + 1).padStart(2, '0')}`,
}))
const dataset = ref<DemoRow[]>([...seed])
const emptyMode = ref(false)

function fetchList(q: PageQuery): Promise<PageResult<DemoRow>> {
  const page = q.page ?? 1
  const pageSize = q.page_size ?? 20
  return new Promise((resolve) => {
    setTimeout(() => {
      if (emptyMode.value) {
        resolve({ list: [], total: 0, page, pageSize })
        return
      }
      const start = (page - 1) * pageSize
      resolve({
        list: dataset.value.slice(start, start + pageSize),
        total: dataset.value.length,
        page,
        pageSize,
      })
    }, 300)
  })
}

// ---- 表格列与刷新（结构型：只依赖 defineExpose 的面） ----
const tableRef = ref<{ refresh: () => void }>()
const columns: HifyTableColumn[] = [
  { label: '名称', prop: 'name' },
  { label: '类型', slot: 'type', width: 120 },
  { label: '创建时间', prop: 'createdAt', width: 140 },
  { label: '操作', slot: 'actions', width: 140, align: 'right' },
]

// ---- 删除确认：一行全流程 ----
function remove(row: DemoRow): void {
  void useConfirm({
    message: `删除提供商「${row.name}」？此操作不可恢复。`,
    api: () =>
      new Promise((resolve) => {
        setTimeout(() => {
          dataset.value = dataset.value.filter((r) => r.id !== row.id)
          resolve(true)
        }, 300)
      }),
  }).then((ok) => {
    if (ok) tableRef.value?.refresh()
  })
}

// ---- 表单弹窗：新增 / 编辑 ----
const dialogRef = ref<{ open: (data?: DemoRow) => void }>()
const dialogVisible = ref(false)

const emptyRow = (): DemoRow => ({
  id: 0,
  name: '',
  type: 'OpenAI',
  createdAt: '',
})

const rules: FormRules = {
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  type: [{ required: true, message: '请选择类型', trigger: 'change' }],
}

function onSubmit(form: DemoRow, done: (ok?: boolean) => void): void {
  setTimeout(() => {
    if (form.id === 0) {
      const nextId = dataset.value.reduce((m, r) => Math.max(m, r.id), 0) + 1
      dataset.value = [
        ...dataset.value,
        { ...form, id: nextId, createdAt: '2026-08-17' },
      ]
      notifySuccess('创建成功')
    } else {
      dataset.value = dataset.value.map((r) =>
        r.id === form.id ? { ...r, name: form.name, type: form.type } : r,
      )
      notifySuccess('更新成功')
    }
    done(true)
    tableRef.value?.refresh()
  }, 400)
}
</script>

<style scoped>
.cd {
  margin-top: var(--hf-space-10);
}

.cd__title {
  margin: 0;
  font-size: var(--hf-font-size-md);
  font-weight: var(--hf-font-weight-semibold);
  color: var(--hf-text-1);
}

.cd__subtitle {
  margin: var(--hf-space-1) 0 var(--hf-space-4);
  font-size: var(--hf-font-size-sm);
  color: var(--hf-text-2);
}

.cd__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--hf-space-4);
}
</style>
