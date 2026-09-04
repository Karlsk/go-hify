/**
 * SSE 流式请求（对话核心链路）。
 * CLAUDE.md：用 fetch + ReadableStream，不用 EventSource（要带 body + 鉴权头）。
 * 此处为通用流式骨架：逐行解析 `event:` / `data:` 并回调；
 * chat 事件协议（delta / tool_call / tool_result / done / error）的业务处理在消费方落地。
 *
 * 不经 axios（无 baseURL / 拦截器）：
 * - url 由调用方写全 `/api/v1/...`（dev 走 Vite /api 代理、prod 走 nginx 同路径）；
 * - emit 前的失败（HTTP 非 2xx，走标准信封）此处读 error.message 再抛；
 * - 401 是 axios 拦截器覆盖不到的盲区，此处自行清身份 + 跳登录（同款动态 import 模式）。
 */

export interface SSEOptions {
  url: string
  body: unknown
  signal?: AbortSignal
  /** 收到一条 SSE 事件（event + data 原文）时回调 */
  onEvent: (event: string, data: string) => void
  onError?: (error: unknown) => void
}

/** SSE 请求失败（HTTP 非 2xx）：携带状态码与信封 error.code，供调用方分支（retryable / 列表重拉） */
export class SSEError extends Error {
  constructor(
    message: string,
    public status: number,
    public code?: string,
  ) {
    super(message)
    this.name = 'SSEError'
  }
}

/** HTTP 非 2xx：解析 respond 信封取 error.message（后端已给中文文案），失败回退状态码文案 */
async function httpError(response: Response): Promise<SSEError> {
  const fallback = new SSEError(`SSE 连接失败：HTTP ${response.status}`, response.status)
  try {
    const payload = (await response.json()) as {
      error?: { code?: string; message?: string } | null
    }
    const err = payload?.error
    return new SSEError(err?.message || fallback.message, response.status, err?.code)
  } catch {
    return fallback
  }
}

/** 401：session 失效 → 清身份 + 跳登录（带 redirect 回跳）。
 * 动态 import：避免 utils → stores/router 静态环（与 request.ts 拦截器同款模式）。 */
function handleUnauthorized(): void {
  void import('@/stores/auth').then(({ useAuthStore }) => useAuthStore().clear())
  void import('@/router').then(({ default: router }) => {
    if (router.currentRoute.value.name !== 'login') {
      void router.push({
        name: 'login',
        query: { redirect: router.currentRoute.value.fullPath },
      })
    }
  })
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
    if (response.status === 401) handleUnauthorized()
    throw await httpError(response)
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
