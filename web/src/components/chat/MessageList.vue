<template>
  <!-- 消息滚动区（spec §6.2 / §11 / §15，2026-09-04 布局修订：AI/我 头像 + 气泡贴合内容贴边分布）。
       页面唯一纵向滚动容器。user 气泡永远纯文本（用户输入不可信）；
       assistant 走 mdRender（marked + DOMPurify）。
       v-memo 钉住 [content, state, errorText]：流式 delta 只重渲染当前行，
       不触发全列表 Markdown 重解析。 -->
  <div ref="scrollEl" class="msg-list" @scroll="onScroll">
    <!-- 会话消息加载中（切换会话时组件保持挂载，加载完成 watch 触发滚底） -->
    <div v-if="loading" class="msg-list__status">
      <el-icon class="is-loading msg-list__spinner"><Loading /></el-icon>
    </div>

    <!-- 空态：新会话零消息（spec §7；未选会话的空态在 ChatView） -->
    <div v-else-if="messages.length === 0" class="msg-list__status">
      <el-empty description="发送第一条消息开始对话" />
    </div>

    <div v-else class="msg-list__column">
      <!-- 超长会话截断提示（spec §12.2：上限 1000 条兜底） -->
      <div v-if="truncated" class="msg-list__truncated">
        会话消息较多，仅显示最近 1000 条
      </div>

      <div
        v-for="(msg, idx) in messages"
        :key="msg.id"
        v-memo="[msg.content, msg.state, msg.errorText, msg.passiveInterrupt, msg.citations]"
        class="msg-list__row"
        :class="[
          msg.role === 'user' ? 'msg-list__row--user' : 'msg-list__row--assistant',
          idx > 0 && messages[idx - 1].role !== msg.role ? 'msg-list__row--gap' : '',
        ]"
      >
        <!-- assistant：AI 头像 + 左对齐气泡（贴合内容宽度） -->
        <template v-if="msg.role === 'assistant'">
          <span class="msg-avatar" aria-hidden="true">AI</span>
          <div class="msg-bubble msg-bubble--assistant">
            <!-- 等待首字：三点跳动 -->
            <span v-if="msg.state === 'loading'" class="msg-dots" aria-label="生成中">
              <span></span><span></span><span></span>
            </span>

            <template v-else-if="msg.state === 'error'">
              <span class="msg-bubble__error">{{ msg.errorText }}</span>
              <!-- retryable=true 才给重发按钮（§14.2）；重发 = 原 content 重新一轮（§14.3） -->
              <el-button
                v-if="msg.retryable"
                link
                type="primary"
                size="small"
                @click="emit('resend', msg)"
              >
                重新发送
              </el-button>
            </template>

            <template v-else>
              <!-- 正文（done / streaming / interrupted 共用）；流式尾部主色光标块 -->
              <!-- eslint-disable-next-line vue/no-v-html -- DOMPurify 净化后的受控 HTML（utils/markdown.ts） -->
              <span v-if="msg.content" class="md-content" v-html="mdRender(msg.content)"></span>
              <span v-if="msg.state === 'streaming'" class="stream-cursor"></span>
              <!-- 被动流中断：保留部分正文 + 弱化标注（主动 abort 不标注，§14.4） -->
              <span v-if="msg.passiveInterrupt" class="msg-bubble__interrupted">（连接中断）</span>
              <!-- RAG 引用来源 tag 行：buildSystemPrompt 检索命中的文档清单（assistant 行持久化） -->
              <div v-if="msg.citations.length > 0" class="msg-citations">
                <span v-for="c in msg.citations" :key="c.document_id" class="msg-citations__tag">
                  📄 {{ c.document_name }} · {{ c.similarity.toFixed(2) }}
                </span>
              </div>
            </template>
          </div>
        </template>

        <!-- user：右对齐深色气泡 + 我 头像（纯文本，white-space: pre-wrap） -->
        <template v-else-if="msg.role === 'user'">
          <div class="msg-bubble msg-bubble--user">{{ msg.content }}</div>
          <span class="msg-avatar" aria-hidden="true">我</span>
        </template>

        <!-- tool 历史行（后端 v1 不产生，防御性渲染：弱化等宽文本、无头像） -->
        <div v-else class="msg-bubble msg-bubble--tool">{{ msg.content }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { Loading } from '@element-plus/icons-vue'
import type { ChatMessage } from '@/composables/useChat'
import { mdRender } from '@/utils/markdown'

const props = defineProps<{
  messages: ChatMessage[]
  loading: boolean
  truncated: boolean
}>()

const emit = defineEmits<{
  resend: [row: ChatMessage]
}>()

// ---- 滚动行为（spec §12.5）：贴底跟滚 / 上滚暂停，判定阈值 80px ----

const scrollEl = ref<HTMLElement>()
/** 用户是否贴底（距底 ≤ 80px）；贴底时新内容自动滚底 */
const stick = ref(true)

function onScroll(): void {
  const el = scrollEl.value
  if (!el) return
  stick.value = el.scrollHeight - el.scrollTop - el.clientHeight <= 80
}

function scrollToBottom(): void {
  const el = scrollEl.value
  if (el) el.scrollTop = el.scrollHeight
}

// delta / 新行到达：贴底才跟滚
watch(
  () => {
    const last = props.messages[props.messages.length - 1]
    return [props.messages.length, last?.content.length ?? 0] as const
  },
  () => {
    if (stick.value) void nextTick(scrollToBottom)
  },
)

// 会话加载完成：无条件滚底（切会话即看最新，spec §12.2）
watch(
  () => props.loading,
  (loading) => {
    if (!loading) {
      stick.value = true
      void nextTick(scrollToBottom)
    }
  },
)
</script>

<style scoped>
/* 唯一纵向滚动容器；行全宽贴边分布（2026-09-04 布局修订：去居中列），
 * 首条消息顶部留白对齐参考图（space-10 = 40px） */
.msg-list {
  flex: 1;
  overflow-y: auto;
  padding: var(--hf-space-10) 0 var(--hf-space-6);
}

.msg-list__column {
  padding: 0 var(--hf-space-6);
}

.msg-list__truncated {
  margin-bottom: var(--hf-space-4);
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
  text-align: center;
}

/* 加载中 / 零消息空态：整区居中 */
.msg-list__status {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
}

.msg-list__spinner {
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xl);
}

/* 消息行：头像 + 气泡（AI 头像在左 / 我 头像在右），顶部对齐 */
.msg-list__row {
  display: flex;
  align-items: flex-start;
  gap: var(--hf-space-2);
  margin-bottom: var(--hf-space-6);
}

.msg-list__row--assistant {
  justify-content: flex-start;
}

.msg-list__row--user {
  justify-content: flex-end;
}

/* user / assistant 交替时上方加分组呼吸感（spec §6.2） */
.msg-list__row--gap {
  margin-top: var(--hf-space-2);
}

/* 圆形头像：品牌渐变底 + 白字（App logo 同款组合，配色不动） */
.msg-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: var(--hf-radius-full);
  background: var(--hf-gradient-brand);
  color: var(--hf-text-inverse);
  font-size: var(--hf-font-size-xs);
  font-weight: var(--hf-font-weight-semibold);
  flex-shrink: 0;
  user-select: none;
}

/* ---- 气泡（贴合内容宽度，max-width 封顶阅读线） ---- */

.msg-bubble {
  border-radius: var(--hf-radius-md);
  padding: 10px var(--hf-space-4);
  font-size: var(--hf-font-size-base);
  line-height: var(--hf-leading-relaxed);
  word-break: break-word;
}

/* user：主色深底 + 白字（4.67:1 AA）；纯文本换行保留 */
.msg-bubble--user {
  max-width: min(640px, 75%);
  background-color: var(--hf-primary-500);
  color: var(--hf-text-inverse);
  white-space: pre-wrap;
}

/* assistant：浅色底（配色不动，保持 bg-subtle） */
.msg-bubble--assistant {
  max-width: min(800px, 85%);
  background-color: var(--hf-bg-subtle);
  color: var(--hf-text-1);
}

/* tool 历史行（防御）：弱化等宽 */
.msg-bubble--tool {
  max-width: min(800px, 85%);
  background-color: var(--hf-bg-subtle);
  color: var(--hf-text-3);
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-sm);
  white-space: pre-wrap;
}

/* 错误态：红字提示（文案来自后端 error.message） */
.msg-bubble__error {
  color: var(--hf-danger);
}

/* 被动流中断标注：弱化小字（内容可用，非红字） */
.msg-bubble__interrupted {
  margin-left: var(--hf-space-1);
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
}

/* RAG 引用来源 tag 行：气泡下方紧凑排列（📄 文档名 · 相似度） */
.msg-citations {
  display: flex;
  flex-wrap: wrap;
  gap: var(--hf-space-2);
  margin-top: var(--hf-space-2);
}

.msg-citations__tag {
  display: inline-flex;
  align-items: center;
  gap: var(--hf-space-1);
  padding: 2px var(--hf-space-2);
  border-radius: var(--hf-radius-sm);
  background-color: var(--hf-bg-muted);
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-xs);
  white-space: nowrap;
}

/* ---- 等待首字：三点跳动（时长由 token 算术，reduced-motion 由 main.css 全局降级） ---- */

.msg-dots {
  display: inline-flex;
  gap: var(--hf-space-1);
  align-items: center;
}

.msg-dots span {
  width: 6px;
  height: 6px;
  border-radius: var(--hf-radius-full);
  background-color: var(--hf-text-3);
  animation: msg-dot-bounce calc(var(--hf-duration-slow) * 2) var(--hf-ease-in-out) infinite;
}

.msg-dots span:nth-child(2) {
  animation-delay: calc(var(--hf-duration-slow) * 2 / 3);
}

.msg-dots span:nth-child(3) {
  animation-delay: calc(var(--hf-duration-slow) * 4 / 3);
}

@keyframes msg-dot-bounce {
  0%,
  60%,
  100% {
    transform: translateY(0);
    opacity: 0.5;
  }
  30% {
    transform: translateY(-4px);
    opacity: 1;
  }
}

/* ---- 流式光标块 ▊：主色、闪烁 ---- */

.stream-cursor {
  display: inline-block;
  width: 0.5em;
  height: 1em;
  margin-left: 2px;
  border-radius: 1px;
  background-color: var(--hf-primary);
  vertical-align: -0.15em;
  animation: stream-cursor-blink calc(var(--hf-duration-slow) * 3) steps(2, start) infinite;
}

@keyframes stream-cursor-blink {
  to {
    visibility: hidden;
  }
}

/* ---- Markdown 内容（spec §15.3）：v-html 内容非 scoped，走 :deep；零新增 token ---- */

.md-content :deep(code) {
  font-family: var(--hf-font-mono);
  font-size: 95%;
  background-color: var(--hf-bg-muted);
  padding: 2px var(--hf-space-1);
  border-radius: var(--hf-radius-xs);
}

/* 代码块：等宽 + muted 底 + 横向滚动 */
.md-content :deep(pre) {
  margin: 0 0 var(--hf-space-3);
  padding: var(--hf-space-3);
  background-color: var(--hf-bg-muted);
  border-radius: var(--hf-radius-sm);
  overflow-x: auto;
}

.md-content :deep(pre code) {
  display: block;
  padding: 0;
  background-color: transparent;
  border-radius: 0;
  font-size: var(--hf-font-size-sm);
}

/* 间距归一（「其他样式保持」：继承气泡排版，不上新色） */
.md-content :deep(p),
.md-content :deep(ul),
.md-content :deep(ol),
.md-content :deep(table),
.md-content :deep(blockquote) {
  margin: 0 0 var(--hf-space-3);
}

.md-content :deep(:last-child) {
  margin-bottom: 0;
}

.md-content :deep(ul),
.md-content :deep(ol) {
  padding-left: var(--hf-space-5);
}

.md-content :deep(h1),
.md-content :deep(h2),
.md-content :deep(h3),
.md-content :deep(h4) {
  margin: var(--hf-space-3) 0 var(--hf-space-2);
  font-weight: var(--hf-font-weight-semibold);
  color: var(--hf-text-1);
}

.md-content :deep(h1) {
  font-size: var(--hf-font-size-lg);
}

.md-content :deep(h2) {
  font-size: var(--hf-font-size-md);
}

.md-content :deep(h3),
.md-content :deep(h4) {
  font-size: var(--hf-font-size-base);
}

.md-content :deep(blockquote) {
  padding-left: var(--hf-space-3);
  border-left: 2px solid var(--hf-border-3);
  color: var(--hf-text-2);
}

.md-content :deep(table) {
  border-collapse: collapse;
}

.md-content :deep(th),
.md-content :deep(td) {
  padding: var(--hf-space-1) var(--hf-space-2);
  border: 1px solid var(--hf-border-2);
}

.md-content :deep(a) {
  color: var(--hf-text-link);
}
</style>
