<template>
  <!-- 对话页容器（spec §4，2026-09-04 布局修订：面板头只留标题）：
       左栏会话列表 260px + 右侧聊天面板（顶栏 / 消息区 / 输入区）。
       全高布局依赖路由 meta.fullBleed（App.vue 的 el-main 去 padding + 禁滚动）。
       编排全部走 useChat（页面私有态，不进 Pinia）；组件只做展示与事件上收。 -->
  <div class="chat">
    <!-- 左栏：>992 静态栏（260px 布局常量）；≤992 隐藏、收进 drawer -->
    <div v-if="!isCompact" class="chat__sidebar">
      <ConversationSidebar
        :conversations="conversations"
        :loading="convLoading"
        :has-more="convHasMore"
        :active-id="activeId"
        :agent-names="agentNameMap"
        @select="onSelect"
        @create="openCreate"
        @remove="onRemove"
        @load-more="loadMoreConversations"
      />
    </div>

    <!-- 右侧面板：纵向三段（顶栏 / 消息区 flex-1 / 输入区沉底） -->
    <section class="chat__panel">
      <header class="chat__header">
        <!-- ≤992：会话列表收进 drawer 的入口（spec §8） -->
        <el-icon
          v-if="isCompact"
          class="chat__icon-btn"
          role="button"
          aria-label="会话列表"
          @click="drawerVisible = true"
        >
          <Menu />
        </el-icon>
        <span class="chat__title">{{ headerTitle }}</span>
      </header>

      <!-- 空态①：未选会话（spec §7）；选中后的加载中 / 零消息空态在 MessageList 内 -->
      <div v-if="!activeId" class="chat__empty">
        <el-empty description="从左侧选择会话，或新建一个开始对话">
          <el-button type="primary" @click="openCreate">新建会话</el-button>
        </el-empty>
      </div>
      <MessageList
        v-else
        :messages="messages"
        :loading="messagesLoading"
        :truncated="truncated"
        @resend="onResend"
      />

      <!-- 未选会话也禁发送（空态①引导先建/选会话）；流式中禁发送但输入可编辑 -->
      <ChatInput :disabled="streaming || !activeId" @send="onSend" />
    </section>

    <!-- ≤992：drawer 内嵌同一 ConversationSidebar 组件（复用非复制，spec §8） -->
    <el-drawer
      v-model="drawerVisible"
      direction="ltr"
      size="280px"
      :with-header="false"
      class="chat-drawer"
    >
      <ConversationSidebar
        :conversations="conversations"
        :loading="convLoading"
        :has-more="convHasMore"
        :active-id="activeId"
        :agent-names="agentNameMap"
        @select="onSelect"
        @create="openCreate"
        @remove="onRemove"
        @load-more="loadMoreConversations"
      />
    </el-drawer>

    <!-- 新建会话：dialog 选 agent（会话创建即绑定，中途不换——spec §12.4） -->
    <HifyFormDialog
      ref="dialogRef"
      v-model="dialogVisible"
      title="新建会话"
      :initial-model="emptyForm"
      :rules="rules"
      @submit="onCreateSubmit"
    >
      <template #default="{ form }">
        <el-form-item label="Agent" prop="agentId">
          <el-select
            v-model="form.agentId"
            placeholder="选择对话使用的 Agent"
            style="width: 100%"
          >
            <el-option
              v-for="a in agentOptions"
              :key="a.id"
              :label="a.name"
              :value="a.id"
            />
          </el-select>
        </el-form-item>
      </template>
    </HifyFormDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Menu } from '@element-plus/icons-vue'
import type { FormRules } from 'element-plus'
import ChatInput from '@/components/chat/ChatInput.vue'
import ConversationSidebar from '@/components/chat/ConversationSidebar.vue'
import MessageList from '@/components/chat/MessageList.vue'
import HifyFormDialog from '@/components/HifyFormDialog.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { useConfirm } from '@/composables/useConfirm'
import { useChat, type ChatMessage } from '@/composables/useChat'
import type { ConversationItem } from '@/api/chat'
import { notifySuccess } from '@/utils/notify'

const { isCompact } = useBreakpoint()

const {
  conversations,
  convLoading,
  convHasMore,
  activeId,
  activeConversation,
  messages,
  messagesLoading,
  truncated,
  streaming,
  agentOptions,
  agentNameMap,
  init,
  selectConversation,
  loadMoreConversations,
  send,
  resend,
  createConversation,
  removeConversation,
} = useChat()

onMounted(init)

// ---- 面板顶栏（布局修订后仅标题；agent 名在左栏列表项第二行，删除走左栏 hover） ----

const headerTitle = computed(() => activeConversation.value?.title || '新会话')

// ---- 会话选择 / 删除 ----

const drawerVisible = ref(false)

function onSelect(id: string): void {
  drawerVisible.value = false // drawer 内选择后收起
  void selectConversation(id)
}

function onRemove(conv: ConversationItem): void {
  void useConfirm({
    message: `删除会话「${conv.title || '新会话'}」？全部消息将一并删除，此操作不可恢复。`,
    api: () => removeConversation(conv.id),
  })
}

// ---- 发送 / 重发 ----

function onSend(content: string): void {
  void send(content)
}

function onResend(row: ChatMessage): void {
  void resend(row.id)
}

// ---- 新建会话 dialog ----

interface CreateForm {
  agentId: string
}

const dialogRef = ref<{ open: (data?: CreateForm) => void }>()
const dialogVisible = ref(false)

const emptyForm = (): CreateForm => ({ agentId: '' })

const rules: FormRules = {
  agentId: [{ required: true, message: '请选择 Agent', trigger: 'change' }],
}

function openCreate(): void {
  dialogRef.value?.open()
}

function onCreateSubmit(form: CreateForm, done: (ok?: boolean) => void): void {
  // body FK 数值（Go uint64 无 ,string tag，传字符串会 400——踩坑 #8）
  void createConversation(Number(form.agentId))
    .then(() => {
      notifySuccess('会话已创建')
      done(true)
      drawerVisible.value = false
    })
    .catch(() => done(false)) // 失败提示已由拦截器弹；保持弹窗打开
}
</script>

<style scoped>
/* 两栏容器：撑满 fullBleed 的 el-main（App.vue 条件类） */
.chat {
  display: flex;
  height: 100%;
  background-color: var(--hf-bg-container);
  overflow: hidden;
}

/* 左栏 260px 布局常量（4px 基数，非 token——spec §9） */
.chat__sidebar {
  width: 260px;
  flex-shrink: 0;
}

.chat__panel {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

/* 面板顶栏 56px（布局修订对齐参考图）：仅标题 */
.chat__header {
  display: flex;
  align-items: center;
  gap: var(--hf-space-2);
  height: 56px;
  padding: 0 var(--hf-space-6);
  border-bottom: 1px solid var(--hf-border-2);
  flex-shrink: 0;
}

.chat__title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--hf-text-1);
  font-size: var(--hf-font-size-md);
  font-weight: var(--hf-font-weight-semibold);
}

.chat__icon-btn {
  flex-shrink: 0;
  padding: var(--hf-space-1);
  border-radius: var(--hf-radius-sm);
  color: var(--hf-text-3);
  cursor: pointer;
  transition: color var(--hf-duration-fast) var(--hf-ease-in-out);
}

.chat__icon-btn:hover {
  color: var(--hf-text-1);
}

/* 未选会话空态：整区居中（消息区 flex-1 的位置） */
.chat__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1;
}
</style>

<style>
/* drawer 内嵌侧栏：去 EP 默认 body padding 让侧栏撑满（非 scoped——drawer 挂在 body 下） */
.chat-drawer .el-drawer__body {
  padding: 0;
}
</style>
