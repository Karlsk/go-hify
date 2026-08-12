/**
 * SSE 流式请求（对话核心链路）。
 * CLAUDE.md：用 fetch + ReadableStream，不用 EventSource（要带 body + 鉴权头）。
 * 此处为通用流式骨架：逐行解析 `event:` / `data:` 并回调；
 * chat 事件协议（meta / delta / tool_call / tool_result / done / error）的业务处理在 ChatView 落地。
 */

export interface SSEOptions {
  url: string
  body: unknown
  signal?: AbortSignal
  /** 收到一条 SSE 事件（event + data 原文）时回调 */
  onEvent: (event: string, data: string) => void
  onError?: (error: unknown) => void
}

/** 发起 SSE 流式请求，按 SSE 帧协议逐行解析。 */
export async function startSSE(options: SSEOptions): Promise<void> {
  const { url, body, signal, onEvent, onError } = options

  const response = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
    },
    credentials: 'include', // 携带 session cookie
    body: JSON.stringify(body),
    signal,
  })

  if (!response.ok || !response.body) {
    throw new Error(`SSE 连接失败：HTTP ${response.status}`)
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8')
  let buffer = ''
  let event = 'message'

  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })

      const lines = buffer.split('\n')
      buffer = lines.pop() ?? ''

      for (const line of lines) {
        if (line.startsWith(':')) continue // 心跳注释（`: ping`）
        if (line.startsWith('event:')) {
          event = line.slice(6).trim()
        } else if (line.startsWith('data:')) {
          onEvent(event, line.slice(5).trim())
          event = 'message' // 触发后重置
        }
      }
    }
  } catch (err) {
    onError?.(err)
    throw err
  }
}
