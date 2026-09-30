<template>
  <!-- 单张工具活动卡（spec 017 F1/F5）：折叠头部 + 展开详情；纯展示组件——
       折叠/展开是本地 ref 态、默认折叠、无 emits（contracts/tool-events.md §3）。
       args / result 走纯文本插值（{{ }} 自带 HTML 转义），不经 v-html。 -->
  <div class="tool-card">
    <!-- 折叠头部：状态图标 + 显示名 + 参数摘要 + 结果首行摘要 + 展开箭头；title = 完整调用名 -->
    <button
      type="button"
      class="tool-card__head"
      :title="activity.tool"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <!-- 状态图标：running 旋转 / done 完成 / done+is_error 红警示 / interrupted 无图标 -->
      <el-icon v-if="activity.state === 'running'" class="is-loading tool-card__icon">
        <Loading />
      </el-icon>
      <el-icon v-else-if="isErrorState" class="tool-card__icon tool-card__icon--error">
        <Warning />
      </el-icon>
      <el-icon v-else-if="activity.state === 'done'" class="tool-card__icon">
        <CircleCheck />
      </el-icon>

      <span class="tool-card__name">{{ displayName }}</span>
      <span v-if="argsSummary" class="tool-card__args">{{ argsSummary }}</span>
      <!-- done 态结果首行摘要（错误态红字）；running 无结果、interrupted 不伪造 → 不渲染 -->
      <span
        v-if="resultSummary"
        class="tool-card__summary"
        :class="{ 'tool-card__summary--error': isErrorState }"
      >
        {{ resultSummary }}
      </span>
      <!-- 中断兜底（FR-002）：无图标、弱化文本，与 assistant 行「（连接中断）」同哲学 -->
      <span v-if="activity.state === 'interrupted'" class="tool-card__interrupted">（中断）</span>

      <el-icon class="tool-card__arrow" :class="{ 'is-open': expanded }"><ArrowDown /></el-icon>
    </button>

    <!-- 展开态：args 完整 JSON（等宽 pretty）+ result 全文（等宽 pre-wrap 限高内部滚动） -->
    <div v-if="expanded" class="tool-card__body">
      <pre class="tool-card__args-json">{{ argsJson }}</pre>
      <pre v-if="activity.result" class="tool-card__result">{{ activity.result }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowDown, CircleCheck, Loading, Warning } from '@element-plus/icons-vue'
import type { ToolActivity } from '@/composables/useChat'

const props = defineProps<{
  activity: ToolActivity
}>()

/** 默认全部折叠（F1）；行级 v-memo 重渲染不重置本地态（组件实例按 :key=id 保持） */
const expanded = ref(false)

/** 显示名剥离 s<数字>__ 派生前缀（F1）；非派生名 replace 不命中即原样，不误伤 */
const displayName = computed(() => props.activity.tool.replace(/^s\d+__/, ''))

/** done + is_error 才是错误红态；历史回放不标 isError（messages 行未存）→ 一律正常 done */
const isErrorState = computed(() => props.activity.state === 'done' && props.activity.isError === true)

/** 参数键值摘要：k=v 空格拼接；对象/数组值 JSON 化；空 {} 摘要为空（头部不占位） */
const argsSummary = computed(() => {
  const entries = Object.entries(props.activity.args)
  if (entries.length === 0) return ''
  return entries
    .map(([k, v]) => `${k}=${typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v)}`)
    .join(' ')
})

/** 结果首行摘要：仅 done 态渲染（首行 + CSS 省略号截断） */
const resultSummary = computed(() => {
  if (props.activity.state !== 'done' || props.activity.result == null) return ''
  return props.activity.result.split('\n')[0] ?? ''
})

/** args 完整 JSON 等宽 pretty（纯文本插值） */
const argsJson = computed(() => JSON.stringify(props.activity.args, null, 2))
</script>

<style scoped>
/* 工具活动卡（F5 零新增 token）：bg-muted 底 + radius-sm；整体弱化辅助色 text-3，
 * 错误态 danger；间距 / 时长走既有 token */
.tool-card {
  border-radius: var(--hf-radius-sm);
  background-color: var(--hf-bg-muted);
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-sm);
}

/* 头部：整行可点切换折叠（原生 button，键盘可达）；摘要长时省略号截断 */
.tool-card__head {
  display: flex;
  align-items: center;
  gap: var(--hf-space-2);
  width: 100%;
  padding: var(--hf-space-1) var(--hf-space-2);
  border: none;
  background: none;
  font: inherit;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.tool-card__icon {
  flex-shrink: 0;
  font-size: var(--hf-font-size-sm);
}

.tool-card__icon--error {
  color: var(--hf-danger);
}

.tool-card__name {
  flex-shrink: 0;
  max-width: 40%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-card__args {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 结果首行摘要占大头（当前信息量最大）；错误态红字 */
.tool-card__summary {
  flex: 2 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-card__summary--error {
  color: var(--hf-danger);
}

.tool-card__interrupted {
  flex: 1 1 auto;
  min-width: 0;
  white-space: nowrap;
}

.tool-card__arrow {
  flex-shrink: 0;
  margin-left: auto;
  font-size: var(--hf-font-size-sm);
  transition: transform var(--hf-duration-fast) var(--hf-ease-in-out);
}

.tool-card__arrow.is-open {
  transform: rotate(180deg);
}

/* 展开态：贴头部、下方留内边距 */
.tool-card__body {
  padding: 0 var(--hf-space-2) var(--hf-space-2);
}

/* args 完整 JSON：等宽 pretty、横向滚动（长行不撑破气泡） */
.tool-card__args-json {
  margin: 0;
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-sm);
  white-space: pre;
  overflow-x: auto;
}

/* result 全文：等宽 pre-wrap、限高内部滚动（F1） */
.tool-card__result {
  max-height: 200px;
  overflow-y: auto;
  margin: var(--hf-space-2) 0 0;
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-sm);
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
