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
  type Citation,
  type ConversationItem,
  type DoneFrame,
  type ErrorFrame,
  type MessageItem,
  type ToolCall,
  type ToolCallFrame,
  type ToolResultFrame,
} from '@/api/chat'
import { SSEError } from '@/utils/sse'

// ---- UI 层消息行（服务端行 + 本地乐观行） ----

export type MessageState = 'loading' | 'streaming' | 'done' | 'error' | 'interrupted'

/** 工具活动卡（spec 017 F2）：挂在 assistant 行上的单次工具调用展示单元；
 *  id = 流式帧配对（tool_call ↔ tool_result）与历史行配对的共同锚点；
 *  result / isError 仅 tool_result 到达后落（running / interrupted 态不伪造结果） */
export interface ToolActivity {
  id: string
  /** 完整调用名（含 s<数字>__ 派生前缀，如 s1__weather）——展示层剥离前缀、完整名进 title */
  tool: string
  args: Record<string, unknown>
  result?: string
  isError?: boolean
  state: 'running' | 'done' | 'interrupted'
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  toolCalls: ToolCall[]
  /** 本行工具卡清单（按到达序，spec 017）：未绑工具对话恒 []——
   *  任何变化一律整数组替换（v-memo 依赖按数组引用判定的前提） */
  toolActivities: ToolActivity[]
  /** assistant 行的 RAG 引用来源（buildSystemPrompt 检索命中）；user / tool 行恒 [] */
  citations: Citation[]
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
    // 历史行构造点：分组（loadMessages）按 id 配对回填，归并前恒 []
    toolActivities: [],
    citations: m.citations ?? [],
    createdAt: m.created_at,
    state: 'done',
  }
}

/**
 * 历史回放分组（spec 017 FR-005）：assistant 中间行（判定仅「content 空 + tool_calls
 * 非空」——正文非空的行按普通消息行渲染防误吞）与其后 tool 行按 id 配对，归并进其后
 * 首个 assistant 终答行的 toolActivities，与流式路径同一形态；历史卡直接落 done、
 * 不重演 running、不标 isError（messages 行未存 is_error，后端 schema 冻结）。
 *
 * 纯函数式重排：产出行数 ≤ 原始行数（中间行 / 已配对 tool 行不产出），
 * 归并行与合成气泡一律新对象，不改入参行。配对锚点 = 后端 tool 行
 * `tool_calls[0].id`（tools.go：调用引用存 ToolCalls[0]，无独立列）。
 */
function groupToolRows(rows: ChatMessage[]): ChatMessage[] {
  const out: ChatMessage[] = []
  let pending: ToolActivity[] = []
  // 合成气泡 id / createdAt 取残留卡最后来源行（DB id 稳定，刷新后 :key 不漂移）
  let lastSource: ChatMessage | null = null

  /** 残留卡独立成尾部气泡（仅卡片无正文）：错误中断轮 / 窗口末尾无终答行 */
  const flush = (): void => {
    if (pending.length === 0 || !lastSource) return
    out.push({
      id: `tools:${lastSource.id}`,
      role: 'assistant',
      content: '',
      toolCalls: [],
      toolActivities: pending,
      citations: [],
      createdAt: lastSource.createdAt,
      state: 'done',
    })
    pending = []
  }

  for (const row of rows) {
    if (row.role === 'assistant' && row.content === '' && row.toolCalls.length > 0) {
      // 中间行：每条 tool_call 一张卡（id / 工具名 / args；空参 jsonb 为 null 时兜底 {}）
      for (const tc of row.toolCalls) {
        pending = [
          ...pending,
          { id: tc.id, tool: tc.tool, args: tc.args ?? {}, state: 'done' },
        ]
      }
      lastSource = row
      continue
    }
    if (row.role === 'tool') {
      const callId = row.toolCalls[0]?.id
      const idx = pending.findIndex((a) => a.id === callId)
      if (idx >= 0) {
        // tool 行供 result（按 id 配对）
        pending = pending.map((a, i) => (i === idx ? { ...a, result: row.content } : a))
      } else {
        out.push(row) // 孤儿 tool 行（窗口截断 / 数据异常）：防御性原样输出，MessageList 兜底
      }
      lastSource = row
      continue
    }
    if (row.role === 'assistant' && pending.length > 0) {
      // 终答行：残留卡归并进气泡顶部（新对象，不改入参行）
      out.push({ ...row, toolActivities: [...row.toolActivities, ...pending] })
      pending = []
      continue
    }
    if (row.role === 'user') {
      flush() // 轮次边界：中断轮残留卡先独立成气泡，再渲染新 user 行
    }
    out.push(row)
  }
  flush() // 窗口末尾残留（最后一轮即错误中断轮）
  return out
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
      // 截断先于分组（spec 017 FR-005）：MESSAGE_MAX 按原始行数保尾部最新——
      // 先分组后截断会把工具行顶出尾窗（历史失真）；分组归并只减不增，产出行数 ≤ 截断数
      messages.value = groupToolRows(rows.slice(-MESSAGE_MAX))
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

  /** 流收尾统一兜底（spec 017 FR-002）：残留 running 卡置 interrupted、停动画——
   *  无 result 不伪造结果（与 assistant 行「（连接中断）」标注同哲学）；
   *  在 send() 的 finally 单点调用即覆盖全部收尾路径
   *  （done / 流内 error 事件 / EOF 被动中断 / 网络层失败 / 主动 abort）。 */
  function settleToolActivities(row: ChatMessage): void {
    if (!row.toolActivities.some((a) => a.state === 'running')) return
    row.toolActivities = row.toolActivities.map((a): ToolActivity =>
      a.state === 'running' ? { ...a, state: 'interrupted' } : a,
    )
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
        toolActivities: [],
        citations: [],
        createdAt: new Date().toISOString(),
        state: 'done',
        local: true,
      },
      {
        id: `local:assistant:${stamp}`,
        role: 'assistant',
        content: '',
        toolCalls: [],
        toolActivities: [],
        citations: [],
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
          // RAG 引用来源（首个 delta 前到达；空引用不发此帧）
          onCitations: (cites) => {
            assistant.citations = cites
          },
          // 工具卡（spec 017 F2）：call 建 running 卡 / result 按 id 置结果转 done；
          // 恒整数组替换（v-memo 依赖按引用判定），与正文 += 直改路径不混用
          onToolCall: (frame: ToolCallFrame) => {
            assistant.toolActivities = [
              ...assistant.toolActivities,
              { id: frame.id, tool: frame.tool, args: frame.args, state: 'running' },
            ]
          },
          onToolResult: (frame: ToolResultFrame) => {
            const idx = assistant.toolActivities.findIndex((a) => a.id === frame.id)
            if (idx < 0) return // 孤儿结果帧（后端保证 call 先于 result，异常序防御）：忽略不建卡
            assistant.toolActivities = assistant.toolActivities.map((a, i): ToolActivity =>
              i === idx
                ? { ...a, result: frame.result.content, isError: frame.result.is_error, state: 'done' }
                : a,
            )
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
      // 流收尾兜底（FR-002）：done 后异常残留 / 中断路径的 running 卡统一标「（中断）」
      settleToolActivities(assistant)
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
