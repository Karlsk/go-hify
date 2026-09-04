/**
 * Chat 页面状态编排（spec §11 / §12 / §14）：会话列表、消息加载、流式发送、
 * abort 生命周期、错误与重试。页面私有态不进 Pinia（一人维护原则），ChatView 消费。
 *
 * 关键行为（钉死，勿走样）：
 * - 进入页面不自动选中会话（右侧空态引导）；
 * - 消息加载 = 正序循环翻页至 has_more=false（后端无倒序端点），上限 1000 条兜底；
 * - done / error 后静默重拉会话首页（title 回填 + updated_at 排序），不清消息区；
 * - 切换会话 / 组件卸载 / beforeunload 主动 abort（取消上游 LLM 省 token），
 *   部分正文保留、不加「连接中断」标注；被动中断（EOF 无 done / 网络错误）才标注；
 * - 错误文案直接用后端 error.message（failChat 已中文），code 只用于逻辑分支。
 */
import { computed, onUnmounted, ref } from 'vue'
import type { AgentItem } from '@/api/agent'
import { getAgentList } from '@/api/agent'
import {
  createConversation as createConversationApi,
  deleteConversation as deleteConversationApi,
  getConversationList,
  getConversationMessages,
  sendMessageStream,
  type ConversationItem,
  type DoneFrame,
  type ErrorFrame,
  type MessageItem,
  type ToolCall,
} from '@/api/chat'
import { SSEError } from '@/utils/sse'

// ---- UI 层消息行（服务端行 + 本地乐观行） ----

export type MessageState = 'loading' | 'streaming' | 'done' | 'error' | 'interrupted'

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  toolCalls: ToolCall[]
  createdAt: string
  /** assistant 行的流式状态；user / tool 服务端行恒 'done' */
  state: MessageState
  /** state='error' 时的红字文案（后端 error.message 或网络层 fallback） */
  errorText?: string
  /** error 行是否展示「重新发送」按钮（§14.2） */
  retryable?: boolean
  /** 被动流中断标注「（连接中断）」；主动 abort 不标注（§14.4） */
  passiveInterrupt?: boolean
  /** 本地乐观行标记：切换 / 刷新后丢弃，以服务端为准（§12.3） */
  local?: boolean
}

function toChatMessage(m: MessageItem): ChatMessage {
  return {
    id: m.id,
    role: m.role,
    content: m.content,
    toolCalls: m.tool_calls ?? [],
    createdAt: m.created_at,
    state: 'done',
  }
}

// ---- 布局无关的行为常量（spec §12.2：非 token，不进 tokens.css） ----

/** 会话列表页大小（左栏触底加载） */
const CONVERSATION_PAGE_LIMIT = 20
/** 消息翻页页大小（循环加载至尾页） */
const MESSAGE_PAGE_LIMIT = 100
/** 消息加载上限（超长会话截断，顶部提示「仅显示最近 N 条」） */
const MESSAGE_MAX = 1000

/** 每 useChat() 实例持有一份状态（当前仅 ChatView 一个实例，页面私有） */
export function useChat() {
  // ---- 会话列表（左栏） ----
  const conversations = ref<ConversationItem[]>([])
  const convNextCursor = ref<string | null>(null)
  const convHasMore = ref(false)
  const convLoading = ref(false)

  // ---- 当前会话与消息 ----
  const activeId = ref<string | null>(null)
  const messages = ref<ChatMessage[]>([])
  const messagesLoading = ref(false)
  /** 消息超 1000 条被截断（顶部提示） */
  const truncated = ref(false)

  // ---- 流式状态机（§11.2） ----
  const streaming = ref(false)
  let abortController: AbortController | null = null

  // ---- agent 选项（新建 dialog 数据源 + 顶栏 tag 名映射） ----
  const agentOptions = ref<AgentItem[]>([])
  const agentNameMap = computed(
    () => new Map(agentOptions.value.map((a) => [a.id, a.name])),
  )

  const activeConversation = computed(
    () => conversations.value.find((c) => c.id === activeId.value) ?? null,
  )

  // ---- 加载流（§12.2） ----

  /** 进入页面：会话首页 + agent 列表并行；不自动选中（空态引导） */
  function init(): void {
    void loadAgents()
    void loadConversations()
  }

  async function loadAgents(): Promise<void> {
    try {
      // agent 极小静态表，一页 100 足够
      const r = await getAgentList({ page: 1, page_size: 100 })
      agentOptions.value = r.list
    } catch {
      // 拦截器已提示；agent 名缺省时顶栏回退显示 agent_id
    }
  }

  /** 会话首页（进入页面 / 发送结束后静默重拉——title 回填与排序靠它） */
  async function loadConversations(): Promise<void> {
    convLoading.value = true
    try {
      const r = await getConversationList({ limit: CONVERSATION_PAGE_LIMIT })
      conversations.value = r.list
      convHasMore.value = r.hasMore
      convNextCursor.value = r.nextCursor
    } catch {
      // 拦截器已提示
    } finally {
      convLoading.value = false
    }
  }

  /** 左栏触底加载下一页（游标 = 上页 next_cursor） */
  async function loadMoreConversations(): Promise<void> {
    if (convLoading.value || !convHasMore.value || !convNextCursor.value) return
    convLoading.value = true
    try {
      const r = await getConversationList({
        limit: CONVERSATION_PAGE_LIMIT,
        cursor: convNextCursor.value,
      })
      conversations.value.push(...r.list)
      convHasMore.value = r.hasMore
      convNextCursor.value = r.nextCursor
    } catch {
      // 拦截器已提示
    } finally {
      convLoading.value = false
    }
  }

  /** 点击会话：streaming 中先中止旧流（§11.2）→ 切换 → 加载消息 */
  async function selectConversation(id: string): Promise<void> {
    if (id === activeId.value) return
    abort()
    activeId.value = id
    messages.value = []
    truncated.value = false
    await loadMessages(id)
  }

  /**
   * 消息加载（受后端约束：历史只有正序 after_id 游标，无倒序端点）——
   * 循环翻页至 has_more=false 全量渲染，上限 1000 条保尾部（最新）。
   */
  async function loadMessages(convId: string): Promise<void> {
    messagesLoading.value = true
    try {
      const rows: ChatMessage[] = []
      let afterId: string | undefined
      for (;;) {
        const r = await getConversationMessages(convId, {
          after_id: afterId,
          limit: MESSAGE_PAGE_LIMIT,
        })
        rows.push(...r.list.map(toChatMessage))
        if (!r.hasMore || r.nextCursor == null) break
        afterId = r.nextCursor
        if (rows.length >= MESSAGE_MAX) {
          truncated.value = true
          break
        }
      }
      if (activeId.value !== convId) return // 加载期间已切走，丢弃
      messages.value = rows.slice(-MESSAGE_MAX)
    } catch {
      // 拦截器已提示；保留已切换的空态
    } finally {
      messagesLoading.value = false
    }
  }

  // ---- 发送编排（§11.1 八步时间线） ----

  /** 中止进行中的流（幂等）：切换会话 / 组件卸载 / beforeunload 调用 */
  function abort(): void {
    abortController?.abort()
  }

  /** 主动 abort 的收尾：assistant 行脱离动画态，部分正文保留、不标注（§14.4） */
  function settleAborted(row: ChatMessage): void {
    if (row.state === 'loading' || row.state === 'streaming') {
      row.state = 'interrupted'
    }
  }

  async function send(content: string): Promise<void> {
    const convId = activeId.value
    const text = content.trim()
    if (!convId || !text || streaming.value) return

    // ①②③ 乐观双气泡：user 行（本地）+ assistant 占位行（三点动画）
    const stamp = Date.now()
    messages.value.push(
      {
        id: `local:user:${stamp}`,
        role: 'user',
        content: text,
        toolCalls: [],
        createdAt: new Date().toISOString(),
        state: 'done',
        local: true,
      },
      {
        id: `local:assistant:${stamp}`,
        role: 'assistant',
        content: '',
        toolCalls: [],
        createdAt: new Date().toISOString(),
        state: 'loading',
        local: true,
      },
    )
    // 持有响应式代理再改字段：直接改原始对象不触发渲染
    const assistant = messages.value[messages.value.length - 1]

    streaming.value = true
    abortController = new AbortController()
    const signal = abortController.signal

    try {
      await sendMessageStream(
        convId,
        text,
        {
          // ⑥ 首个 delta 到达即动画让位于正文，逐字追加 + 光标
          onDelta: (chunk) => {
            if (assistant.state === 'loading') assistant.state = 'streaming'
            assistant.content += chunk
          },
          // ⑦ done：message_id 落行、光标移除；静默重拉会话首页（title 回填）
          onDone: (frame: DoneFrame) => {
            assistant.id = frame.message_id
            assistant.state = 'done'
            void loadConversations()
          },
          // ⑧b 流内 error 事件（流已 200 开始，状态码不可改）
          onError: (frame: ErrorFrame) => {
            assistant.state = 'error'
            assistant.errorText = frame.message
            assistant.retryable = frame.retryable
            onSendFailed(frame.code)
            void loadConversations()
          },
        },
        signal,
      )
      // 流 EOF 且未收到 done → 被动中断：保留部分正文 + 弱化标注（§14.4）
      if (assistant.state === 'loading' || assistant.state === 'streaming') {
        assistant.state = 'interrupted'
        assistant.passiveInterrupt = true
      }
    } catch (err) {
      if (signal.aborted) {
        settleAborted(assistant) // 主动 abort：内容保留、不标注
      } else if (err instanceof SSEError) {
        // ⑧a 请求失败（emit 前，HTTP 非 2xx）：文案取信封 error.message
        assistant.state = 'error'
        assistant.errorText = err.message
        // 重发按钮语义（§14.2/§14.5）：503 可重试；429 限流可重试、预算耗尽不可
        assistant.retryable =
          err.status === 503 || (err.status === 429 && err.code === 'RATE_LIMITED')
        onSendFailed(err.code)
        void loadConversations()
      } else {
        // 网络层失败（连接拒绝 / DNS / 中途断开）
        assistant.state = 'error'
        assistant.errorText = err instanceof Error && err.message ? err.message : '连接失败，请检查网络'
        assistant.retryable = true
      }
    } finally {
      streaming.value = false
      abortController = null
    }
  }

  /** 发送失败后的联动：会话可能已被他处删除 → 重拉列表（§14.5） */
  function onSendFailed(code?: string): void {
    if (code === 'CONVERSATION_NOT_FOUND') void loadConversations()
  }

  /** 「重新发送」（§14.3）：原 content 重新 POST 一轮——新 user + 新 assistant 行，
   * 失败轮留痕可查（后端 v1 无 regenerate 端点，不做替换式重生成）。 */
  async function resend(errorRowId: string): Promise<void> {
    const idx = messages.value.findIndex((m) => m.id === errorRowId)
    if (idx < 0) return
    for (let i = idx - 1; i >= 0; i--) {
      const row = messages.value[i]
      if (row.role === 'user') {
        await send(row.content)
        return
      }
    }
  }

  // ---- 会话操作（§12.4） ----

  /** 新建会话（dialog 选 agent 后调用）：unshift 列表顶部并选中 */
  async function createConversation(agentId: number): Promise<ConversationItem> {
    const conv = await createConversationApi(agentId) // 失败向 dialog 冒泡（done(false)）
    conversations.value.unshift(conv)
    await selectConversation(conv.id)
    return conv
  }

  /** 删除完成后的本地收尾（API 调用由 useConfirm 完成）：移除列表行；
   * 删的是当前会话则中止流 + 右侧回空态。 */
  function onConversationDeleted(id: string): void {
    conversations.value = conversations.value.filter((c) => c.id !== id)
    if (id === activeId.value) {
      abort()
      activeId.value = null
      messages.value = []
    }
  }

  /** 兼容入口：删除确认 + API + 收尾一步走（ChatView 用） */
  async function removeConversation(id: string): Promise<void> {
    await deleteConversationApi(id)
    onConversationDeleted(id)
  }

  // ---- abort 生命周期（§11.2）：组件卸载 + beforeunload ----
  function handleBeforeUnload(): void {
    abort()
  }
  window.addEventListener('beforeunload', handleBeforeUnload)
  onUnmounted(() => {
    abort()
    window.removeEventListener('beforeunload', handleBeforeUnload)
  })

  return {
    // 状态
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
    // 加载流
    init,
    selectConversation,
    loadMoreConversations,
    loadConversations,
    // 发送
    send,
    resend,
    abort,
    // 会话操作
    createConversation,
    removeConversation,
    onConversationDeleted,
  }
}
