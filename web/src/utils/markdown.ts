/**
 * Markdown → 安全 HTML 渲染（assistant 消息气泡专用，spec §15）。
 *
 * - marked 官方声明不做输出净化：LLM 输出可能夹带工具结果 / 网页里的注入载荷，
 *   marked 之后必须过 DOMPurify 才能进 v-html（CLAUDE.md《安全》XSS 净化硬性要求）；
 * - user 气泡永远不走本函数（纯文本 pre-wrap——用户输入不可信，渲染成 HTML = 存储型 XSS 入口）；
 * - 流式场景每个 delta 后对全文重渲染（单条消息量级小，不做增量解析）。
 */
import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.setOptions({
  gfm: true, // 表格 / 删除线 / 任务列表
  breaks: true, // 单换行即换行（聊天语义）
})

// 链接统一新窗口打开并断开 opener（净化后属性补写）
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  }
})

/** Markdown 渲染为净化后的 HTML 字符串（供 v-html；仅用于 assistant 内容） */
export function mdRender(content: string): string {
  const html = marked.parse(content, { async: false })
  return DOMPurify.sanitize(html)
}
