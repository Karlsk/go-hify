/**
 * Provider 模块 API 层：类型与请求方法的唯一事实源（对齐后端 internal/provider/api 契约）。
 *
 * 字段名直接用后端 JSON snake_case（对齐 types/index.ts 的 ResultMeta / PageQuery 先例）。
 * ID 序列化为字符串（防 JS 超 2^53 丢精度）——URL 路径参数原样传字符串；
 * 但请求 body 的 provider_id 是数值（Go uint64 无 `,string` tag，传字符串会 400），
 * 调用处用 Number() 转换。
 */
import { del, get, getList, post, put } from '@/utils/request'
import type { PageQuery } from '@/types'

// ---- 类型（对齐 provider/api/schema.go） ----

/** 提供商类型（kind 枚举，4 类；创建后不可改）。openai_compatible 含官方 OpenAI（base_url 空 = 默认官方端点） */
export type ProviderKind =
  | 'openai_compatible'
  | 'claude'
  | 'gemini'
  | 'ollama'

/** 健康状态（provider_health.status，DEGRADED 状态机见 db_model.md §2.3） */
export type HealthStatus = 'up' | 'degraded' | 'down' | 'unknown'

/** 模型能力类型 */
export type ModelCapability = 'chat' | 'embedding'

/** 模型来源（manual 手动录入 / discovered 自动发现，后者 sync 只刷新 name） */
export type ModelSource = 'manual' | 'discovered'

/** 提供商健康（无行为 null——从未探测） */
export interface ProviderHealth {
  provider_id: string
  status: HealthStatus
  last_check_at: string | null
  last_success_at: string | null
  fail_count: number
  latency_ms: number | null
  error_message: string
  updated_at: string
}

/** 提供商列表项：本体 + 当页批量现读的聚合列（后端 withAggregates） */
export interface ProviderItem {
  id: string
  name: string
  kind: ProviderKind
  base_url: string
  has_api_key: boolean
  enabled: boolean
  created_at: string
  updated_at: string
  health: ProviderHealth | null
  /** 该提供商下 enabled=true 的模型数 */
  enabled_model_count: number
}

/** 连通性测试结果（HTTP 200 + success 字段——探测失败是业务结果不是 HTTP 错误） */
export interface ConnectionTestResult {
  success: boolean
  latency_ms: number
  model_count: number
  error_message?: string
}

/** 模型列表项 */
export interface ModelItem {
  id: string
  provider_id: string
  name: string
  model_id: string
  capability: ModelCapability
  context_window: number | null
  max_output_tokens: number | null
  input_price: string | null
  output_price: string | null
  embedding_dim: number | null
  enabled: boolean
  source: ModelSource
  extra_params: Record<string, unknown> | null
}

/** 创建提供商（api_key 仅 openai/claude/gemini 必填；openai_compatible 必填 base_url） */
export interface CreateProviderData {
  name: string
  kind: ProviderKind
  base_url?: string
  api_key?: string
}

/** 整体更新提供商（PUT 全量提交；kind 不可改；api_key 空串/省略 = 保留原值） */
export interface UpdateProviderData {
  name: string
  base_url?: string
  api_key?: string
  enabled: boolean
}

/** 创建模型（source=manual；embedding 必填 embedding_dim，chat 不得携带——后端 validateModel） */
export interface CreateModelData {
  provider_id: number
  name: string
  model_id: string
  capability: ModelCapability
  context_window?: number
  embedding_dim?: number
}

/** 整体更新模型（PUT 全量提交：可选列须整行回传，否则会被清空——启停开关切换用） */
export interface UpdateModelData {
  provider_id: number
  name: string
  model_id: string
  capability: ModelCapability
  context_window: number | null
  max_output_tokens: number | null
  input_price: string | null
  output_price: string | null
  embedding_dim: number | null
  enabled: boolean
}

/** 模型同步结果（只增改不删；新条目 enabled=false 待启用） */
export interface ModelSyncResult {
  added: number
  updated: number
}

// ---- provider 请求方法 ----

/** 提供商列表（偏移分页；HifyTable 数据源） */
export function getProviderList(params?: PageQuery) {
  return getList<ProviderItem>('/providers', params)
}

export function createProvider(data: CreateProviderData) {
  return post<ProviderItem>('/providers', data)
}

export function updateProvider(id: string, data: UpdateProviderData) {
  return put<ProviderItem>(`/providers/${id}`, data)
}

/** 删除提供商（204 无响应体；级联删 models 与 health，被引用时 409） */
export function deleteProvider(id: string) {
  return del<void>(`/providers/${id}`)
}

/** 手动连通性探测（耗时上限 10s，调用方管理 loading；结果按 success 分支提示） */
export function testConnection(id: string) {
  return post<ConnectionTestResult>(`/providers/${id}/test-connection`)
}

// ---- model 请求方法 ----

/** 某提供商下模型列表（偏移分页） */
export function getModelList(providerId: string, params?: PageQuery) {
  return getList<ModelItem>(`/providers/${providerId}/models`, params)
}

/** 模型自动发现同步（上游不可用 503；手动同步响亮失败） */
export function syncModels(providerId: string) {
  return post<ModelSyncResult>(`/providers/${providerId}/models/sync`)
}

export function createModel(data: CreateModelData) {
  return post<ModelItem>('/models', data)
}

export function updateModel(id: string, data: UpdateModelData) {
  return put<ModelItem>(`/models/${id}`, data)
}

export function deleteModel(id: string) {
  return del<void>(`/models/${id}`)
}
