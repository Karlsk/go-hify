<template>
  <!-- 底部输入区（spec §6.3，2026-09-04 布局修订：通栏 + 顶部分隔线 + 外置「发送」按钮）。
       Enter 发送 / Shift+Enter 换行；流式中发送按钮禁用但输入框保持可编辑（可预打下一条）。
       v1 无「停止生成」按钮（中止走切换会话 / 离开页面，spec §11.2）。 -->
  <div class="chat-input">
    <div class="chat-input__row">
      <div class="chat-input__box">
        <el-input
          v-model="draft"
          type="textarea"
          :autosize="{ minRows: 1, maxRows: 8 }"
          :placeholder="placeholder"
          resize="none"
          class="chat-input__textarea"
          @input="onInput"
          @keydown.enter.exact.prevent="onEnter"
        />
      </div>
      <button
        type="button"
        class="chat-input__send"
        :disabled="!canSend"
        :title="disabled ? '生成中…' : '发送（Enter）'"
        @click="onSend"
      >
        发送
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { notifyWarning } from '@/utils/notify'

const props = withDefaults(
  defineProps<{
    /** 流式进行中：仅拦发送动作，输入框保持可编辑 */
    disabled?: boolean
    placeholder?: string
  }>(),
  { disabled: false, placeholder: '输入消息，Enter 发送，Shift+Enter 换行' },
)

const emit = defineEmits<{
  send: [content: string]
}>()

/** 输入上限（对齐后端 SendMessageReq binding max=32000，超限截断 + 提示） */
const MAX_CONTENT = 32_000

const draft = ref('')

const canSend = computed(() => !props.disabled && draft.value.trim() !== '')

/** 超限截断（watch 侧触发提示，避免输入法组合中打断） */
function onInput(): void {
  if (draft.value.length > MAX_CONTENT) {
    draft.value = draft.value.slice(0, MAX_CONTENT)
    notifyWarning(`内容超出 ${MAX_CONTENT} 字符上限，已截断`)
  }
}

/** el-input 的 keydown 回调签名是 Event | KeyboardEvent（EP 类型宽化），此处运行时恒为键盘事件 */
function onEnter(e: Event | KeyboardEvent): void {
  const ke = e as KeyboardEvent
  // 输入法组合确认的 Enter 不算发送
  if (ke.isComposing || ke.keyCode === 229) return
  onSend()
}

function onSend(): void {
  const text = draft.value.trim()
  if (!text || props.disabled) return
  emit('send', text)
  draft.value = '' // ① 发送即清空（乐观 UI，spec §11.1）
}
</script>

<style scoped>
/* 通栏（去 800px cap）：与消息区同宽，顶部分隔线区隔（2026-09-04 布局修订） */
.chat-input {
  flex-shrink: 0;
  padding: var(--hf-space-3) var(--hf-space-6) var(--hf-space-4);
  border-top: 1px solid var(--hf-border-2);
  background-color: var(--hf-bg-container);
}

.chat-input__row {
  display: flex;
  align-items: flex-end;
  gap: var(--hf-space-3);
}

.chat-input__box {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: flex-end;
  border: 1px solid var(--hf-border-1);
  border-radius: var(--hf-radius-lg); /* 12px 大圆角（参考图气质） */
  background-color: var(--hf-bg-container);
  transition: border-color var(--hf-duration-fast) var(--hf-ease-in-out);
}

.chat-input__box:focus-within {
  border-color: var(--hf-primary);
}

.chat-input__textarea {
  flex: 1;
}

.chat-input__textarea :deep(.el-textarea__inner) {
  border: none;
  box-shadow: none;
  padding: 10px var(--hf-space-4);
  line-height: var(--hf-leading-normal);
}

/* 发送按钮：外置输入框右侧，圆角矩形 + 文字（2026-09-04 布局修订） */
.chat-input__send {
  flex-shrink: 0;
  height: 36px;
  padding: 0 var(--hf-space-4);
  border: none;
  border-radius: var(--hf-radius-sm);
  background-color: var(--hf-primary-500);
  color: var(--hf-text-inverse);
  font-size: var(--hf-font-size-sm);
  cursor: pointer;
  transition:
    background-color var(--hf-duration-fast) var(--hf-ease-in-out),
    opacity var(--hf-duration-fast) var(--hf-ease-in-out);
}

.chat-input__send:hover:not(:disabled) {
  background-color: var(--hf-primary-600);
}

.chat-input__send:disabled {
  background-color: var(--hf-bg-muted);
  color: var(--hf-text-4);
  cursor: not-allowed;
}
</style>
