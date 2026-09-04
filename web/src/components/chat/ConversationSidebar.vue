<template>
  <!-- 会话列表左栏（spec §5，2026-09-04 布局修订：头部标题行 + 两行列表项 + 选中竖条）。
       纯展示组件：数据与操作全部经 props / emits 上收 ChatView（useChat 编排）；
       同一组件在 >992 静态栏与 ≤992 el-drawer 两处复用（spec §8）。 -->
  <aside class="conv-sidebar">
    <div class="conv-sidebar__header">
      <span class="conv-sidebar__heading">对话列表</span>
      <el-button type="primary" size="small" @click="emit('create')">
        <el-icon><Plus /></el-icon>
        新建
      </el-button>
    </div>

    <div v-if="conversations.length === 0 && !loading" class="conv-sidebar__empty">
      <el-empty description="暂无会话" :image-size="72" />
    </div>

    <!-- 列表滚动容器：触底（距底 < 40px）emit load-more -->
    <div v-else class="conv-sidebar__list" @scroll="onScroll">
      <button
        v-for="conv in conversations"
        :key="conv.id"
        type="button"
        class="conv-sidebar__item"
        :class="{ 'conv-sidebar__item--active': conv.id === activeId }"
        @click="emit('select', conv.id)"
      >
        <div class="conv-sidebar__line1">
          <span class="conv-sidebar__title">{{ conv.title || '新会话' }}</span>
          <span class="conv-sidebar__time">{{ formatRelative(conv.updated_at) }}</span>
          <!-- hover 时时间位切换为删除 icon（spec §5） -->
          <el-icon
            class="conv-sidebar__remove"
            title="删除会话"
            @click.stop="emit('remove', conv)"
          >
            <Delete />
          </el-icon>
        </div>
        <!-- 第二行预览：后端列表无 last_message 字段，暂以 agent 名充当（演进项：列表 API 补预览） -->
        <div class="conv-sidebar__line2">{{ previewOf(conv) }}</div>
      </button>
      <div v-if="loading" class="conv-sidebar__loading">
        <el-icon class="is-loading"><Loading /></el-icon>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { Delete, Loading, Plus } from '@element-plus/icons-vue'
import type { ConversationItem } from '@/api/chat'

const props = defineProps<{
  conversations: ConversationItem[]
  /** 列表请求中（首页加载 / 触底翻页共用） */
  loading: boolean
  hasMore: boolean
  activeId: string | null
  /** agent_id → 名称映射（第二行预览数据源，ChatView 的 agentNameMap） */
  agentNames?: Map<string, string>
}>()

const emit = defineEmits<{
  select: [id: string]
  create: []
  remove: [conv: ConversationItem]
  'load-more': []
}>()

/** 第二行预览：agent 名兜底 agent_id */
function previewOf(conv: ConversationItem): string {
  return props.agentNames?.get(conv.agent_id) ?? `Agent #${conv.agent_id}`
}

/** 触底加载：距底 < 40px 即触发（由父级 loadMore 幂等去重） */
function onScroll(e: Event): void {
  const el = e.target as HTMLElement
  if (el.scrollHeight - el.scrollTop - el.clientHeight < 40) emit('load-more')
}

/** 相对时间（列表项弱化展示）：刚刚 / N分钟前 / N小时前 / N天前（30 天封顶） */
function formatRelative(iso: string): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const diff = Date.now() - t
  const MIN = 60_000
  const HOUR = 60 * MIN
  const DAY = 24 * HOUR
  if (diff < MIN) return '刚刚'
  if (diff < HOUR) return `${Math.floor(diff / MIN)}分钟前`
  if (diff < DAY) return `${Math.floor(diff / HOUR)}小时前`
  const days = Math.floor(diff / DAY)
  return days > 30 ? '30天+' : `${days}天前`
}
</script>

<style scoped>
/* 260px 固定宽由外层容器给定（4px 基数布局值，非 token——spec §9 尺寸常量）；
 * width:100% 让 ≤992 drawer 场景自然撑满（280px），同一组件两处复用（spec §8） */
.conv-sidebar {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  background-color: var(--hf-bg-container);
  border-right: 1px solid var(--hf-border-2);
  overflow: hidden;
}

/* 头部：标题 + 右侧紧凑「新建」同一行 */
.conv-sidebar__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--hf-space-2);
  height: 48px;
  padding: 0 var(--hf-space-4);
  flex-shrink: 0;
}

.conv-sidebar__heading {
  color: var(--hf-text-1);
  font-size: var(--hf-font-size-md);
  font-weight: var(--hf-font-weight-semibold);
}

.conv-sidebar__empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}

.conv-sidebar__list {
  flex: 1;
  overflow-y: auto;
  padding: 0 var(--hf-space-2);
}

/* 列表项：两行（标题+时间 / agent 预览），hover bg-subtle、选中 bg-muted + 左侧主色竖条 */
.conv-sidebar__item {
  position: relative;
  display: block;
  width: 100%;
  padding: var(--hf-space-2) var(--hf-space-3);
  margin-bottom: var(--hf-space-1);
  border: none;
  border-radius: var(--hf-radius-sm);
  background: transparent;
  text-align: left;
  cursor: pointer;
  transition: background-color var(--hf-duration-fast) var(--hf-ease-in-out);
}

.conv-sidebar__item:hover {
  background-color: var(--hf-bg-subtle);
}

.conv-sidebar__item--active,
.conv-sidebar__item--active:hover {
  background-color: var(--hf-bg-muted);
}

/* 选中态：左侧 3px 主色竖线（对齐 App 侧栏菜单选中样式） */
.conv-sidebar__item--active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  width: 3px;
  height: 60%;
  border-radius: var(--hf-radius-full);
  background-color: var(--hf-primary);
  transform: translateY(-50%);
}

.conv-sidebar__line1 {
  display: flex;
  align-items: center;
  gap: var(--hf-space-2);
}

.conv-sidebar__title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--hf-text-1);
  font-size: var(--hf-font-size-sm);
  font-weight: var(--hf-font-weight-medium);
}

.conv-sidebar__time {
  flex-shrink: 0;
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
}

.conv-sidebar__line2 {
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
}

/* 删除 icon：默认隐藏，hover 项时替换相对时间位显示 */
.conv-sidebar__remove {
  display: none;
  flex-shrink: 0;
  color: var(--hf-text-3);
  cursor: pointer;
}

.conv-sidebar__item:hover .conv-sidebar__time {
  display: none;
}

.conv-sidebar__item:hover .conv-sidebar__remove {
  display: inline-flex;
}

.conv-sidebar__remove:hover {
  color: var(--hf-danger);
}

.conv-sidebar__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--hf-space-3);
  color: var(--hf-text-3);
}
</style>
