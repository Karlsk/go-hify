/**
 * 公共类型，对齐 CLAUDE.md《统一响应信封》与《分页》。
 */

/** 后端 respond.Result 信封：{ success, data, error, meta } */
export interface Result<T = unknown> {
  success: boolean
  data: T | null
  error: ResultError | null
  meta: ResultMeta | null
}

export interface ResultError {
  /** 机器可读错误码，如 PROVIDER_NOT_FOUND（见 CLAUDE.md 错误码表） */
  code: string
  /** 人类可读消息 */
  message: string
  details?: unknown
}

/** 分页元数据（游标分页 / 偏移分页按资源取用对应字段） */
export interface ResultMeta {
  // 游标分页（conversations / messages / executions）
  limit?: number
  has_more?: boolean
  next_cursor?: string | null
  // 偏移分页（providers / agents 等极小静态表）
  page?: number
  page_size?: number
  total?: number
}

/** 列表查询基础参数 */
export interface PageQuery {
  // 游标分页
  limit?: number
  cursor?: string
  // 偏移分页
  page?: number
  page_size?: number
}

/** 偏移分页列表结果（配置表，对齐后端 meta 的 page/page_size/total） */
export interface PageResult<T> {
  list: T[]
  total: number
  page: number
  pageSize: number
}

/** 模型提供商类型（mock 阶段公共定义，provider 列表与模型抽屉共用） */
export type ProviderType = 'OpenAI' | 'Claude' | 'Gemini' | 'Ollama'
