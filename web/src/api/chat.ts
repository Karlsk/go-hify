/**
 * Chat 模块 API 层：类型与请求方法的唯一事实源（对齐后端 internal/chat/api 契约）。
 *
 * 两个钉死事实（spec §12–§13）：
 * - 消息历史翻页回传参数名是 after_id（值 = 上页 meta.next_cursor，handler 已把
 *   next_after_id 转成字符串游标）；conversations 列表翻页回传 cursor——两者 wire 上
 *   都返回 next_cursor，仅查询参数名不同。
 * - SSE 帧 retryable 无 omitempty：所有帧（含 delta）都携带该字段，勿当 done/error 专属。
 *
 * SSE 走 startSSE（fetch，不经 axios、无 baseURL）：url 写全 /api/v1 前缀。
 */
import { del, getCursorList, post } from '@/utils/request'
import { startSSE } from '@/utils/sse'

// ---- 类型（对齐 chat/api/schema.go + 手测帧样例） ----

/** 会话行（conversations 表；title 由首轮用户消息回填） */
export interface ConversationItem {
  id: string
  agent_id: string
  title: string
  created_at: string
  updated_at: string
}

/** assistant 中间行的工具调用请求（tool_calls jsonb；v1 后端不触发，仅为历史行完整性） */
export interface ToolCall {
  id: string
  tool: string
  args: Record<string, unknown>
}

/** 消息行（append-only，id 单调递增即消息顺序） */
export interface MessageItem {
  id: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  tool_calls: ToolCall[]
  created_at: string
}

/** token 用量（done 帧 / 一次输出模式返回体） */
export interface Usage {
  input: number
  output: number
}

/** 一次输出模式（stream:false）的返回体；v1 页面纯流式不用，API 完整性保留 */
export interface AssistantReply {
  message_id: string
  content: string
  usage: Usage
  finish_reason: string
}

/** 发消息请求体（stream 缺省 true，此处显式传值） */
export interface SendMessageData {
  content: string
  stream: boolean
}

// ---- SSE `data:` 帧联合（帧内 type 判别，无 event: 命名行） ----

export interface DeltaFrame {
  type: 'delta'
  content: string
  retryable: boolean
}

export interface DoneFrame {
  type: 'done'
  message_id: string
  usage: Usage
  finish_reason: string
  retryable: boolean
}

export interface ErrorFrame {
  type: 'error'
  code: string
  message: string
  retryable: boolean
}

export interface ToolCallFrame {
  type: 'tool_call'
  id: string
  tool: string
  args: Record<string, unknown>
  retryable: boolean
}

export interface ToolResultFrame {
  type: 'tool_result'
  id: string
  tool: string
  result: unknown
  retryable: boolean
}

export type StreamFrame =
  | DeltaFrame
  | DoneFrame
  | ErrorFrame
  | ToolCallFrame
  | ToolResultFrame

// ---- 请求方法 ----

/** 建会话即绑 Agent（中途不换）；body FK 数值（先例 provider / agent） */
export function createConversation(agentId: number) {
  return post<ConversationItem>('/conversations', { agent_id: agentId })
}

/** 会话列表（keyset 游标，最新在前；触底加载回传 cursor） */
export function getConversationList(params?: { limit?: number; cursor?: string }) {
  return getCursorList<ConversationItem>('/conversations', params)
}

/** 历史消息（正序 after_id 游标；回传值 = 上页 next_cursor） */
export function getConversationMessages(
  id: string,
  params?: { after_id?: string; limit?: number },
) {
  return getCursorList<MessageItem>(`/conversations/${id}/messages`, params)
}

/** 删除会话（204 无响应体；messages 级联删） */
export function deleteConversation(id: string) {
  return del<void>(`/conversations/${id}`)
}

/** 发消息——一次输出模式（v1 页面不用，API 完整性保留） */
export function sendMessageOnce(id: string, data: SendMessageData) {
  return post<AssistantReply>(`/conversations/${id}/messages`, data)
}

// ---- 发消息——流式模式（spec §11.3 / §13.3） ----

export interface StreamCallbacks {
  onDelta: (content: string) => void
  onDone: (frame: DoneFrame) => void
  onError: (frame: ErrorFrame) => void
}

/** 流式发消息：startSSE 薄封装，onEvent 内 JSON.parse 后按 type 分发。
 * resolve = 流 EOF（是否正常收尾由调用方结合 onDone 判定）；HTTP 失败 / 网络错误 reject。
 * tool_call / tool_result：后端 v1 预留不触发，忽略（渲染形态留后续）。 */
export async function sendMessageStream(
  id: string,
  content: string,
  callbacks: StreamCallbacks,
  signal?: AbortSignal,
): Promise<void> {
  await startSSE({
    url: `/api/v1/conversations/${id}/messages`,
    body: { content, stream: true } satisfies SendMessageData,
    signal,
    onEvent: (_event, data) => {
      let frame: StreamFrame
      try {
        frame = JSON.parse(data) as StreamFrame
      } catch {
        return // 协议外帧忽略，不炸流
      }
      switch (frame.type) {
        case 'delta':
          callbacks.onDelta(frame.content)
          break
        case 'done':
          callbacks.onDone(frame)
          break
        case 'error':
          callbacks.onError(frame)
          break
      }
    },
  })
}
