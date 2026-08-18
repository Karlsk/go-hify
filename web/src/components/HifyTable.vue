<template>
  <!-- 列表页表格卡片：表格出血到边（body padding 0），分页条右对齐。
       只支持偏移分页（配置表定位）；游标列表留给聊天 UI 手写。 -->
  <el-card class="hify-table">
    <el-table v-loading="loading" :data="rows">
      <el-table-column
        v-for="col in visibleColumns"
        :key="col.prop ?? col.slot ?? col.label"
        :label="col.label"
        :prop="col.prop"
        :width="col.width"
        :align="col.align"
      >
        <template v-if="col.slot" #default="scope">
          <slot :name="col.slot" v-bind="scope" />
        </template>
      </el-table-column>
      <template #empty>
        <slot name="empty">
          <el-empty description="暂无数据" :image-size="72" />
        </slot>
      </template>
    </el-table>
    <div v-if="pagination" class="hify-table__footer">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="size"
        :total="total"
        :page-sizes="[10, 20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        background
        @current-change="load"
        @size-change="onSizeChange"
      />
    </div>
  </el-card>
</template>

<script lang="ts">
/** 列配置：slot 指定后该列单元格走同名具名插槽（带行作用域） */
export interface HifyTableColumn {
  label: string
  prop?: string
  width?: string | number
  align?: 'left' | 'center' | 'right'
  slot?: string
  /** 视口 ≤ 此值（px）时隐藏该列——次要信息列专用；取 BREAKPOINTS 常量，勿写裸值 */
  hideBelow?: number
}
</script>

<script setup lang="ts" generic="T extends Record<string, any>">
import { computed, onMounted, ref, shallowRef } from 'vue'
import type { PageQuery, PageResult } from '@/types'
import { useBreakpoint } from '@/composables/useBreakpoint'

// 具名插槽带行作用域（row 为泛型 T）；empty 可覆写空态
defineSlots<
  Record<string, (scope: { row: T; $index: number }) => unknown> & {
    empty?: () => unknown
  }
>()

const props = withDefaults(
  defineProps<{
    columns: HifyTableColumn[]
    /** 数据源：偏移分页接口，可用 utils/request 的 getList */
    api: (params: PageQuery) => Promise<PageResult<T>>
    /** 是否显示分页条 */
    pagination?: boolean
    pageSize?: number
  }>(),
  { pagination: true, pageSize: 20 },
)

// 窄屏隐藏次要列（hideBelow 标记）；固定宽容器内的表（如抽屉）不标记即不参与
const { width } = useBreakpoint()
const visibleColumns = computed(() =>
  props.columns.filter((col) => !col.hideBelow || width.value > col.hideBelow),
)

// shallowRef：整表替换，避免 deep unwrap 破坏泛型行类型
const rows = shallowRef<T[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const size = ref(props.pageSize)

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await props.api({ page: page.value, page_size: size.value })
    rows.value = result.list
    total.value = result.total
  } catch {
    // 错误提示已由 request.ts 拦截器统一弹；表格回到空态
    rows.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function onSizeChange(): void {
  page.value = 1
  void load()
}

/** 供父组件刷新（如新增/删除后） */
function refresh(): void {
  void load()
}

onMounted(load)

defineExpose({ refresh })
</script>

<style scoped>
/* 内容卡片 20px 内边距规则对表格卡片例外：表格出血到边，
 * 分页条自带 padding（视觉决策见 docs/design/design-system.md《整体布局》） */
.hify-table :deep(.el-card__body) {
  padding: 0;
}

.hify-table__footer {
  display: flex;
  justify-content: flex-end;
  padding: var(--hf-space-3) var(--hf-space-5);
}
</style>
