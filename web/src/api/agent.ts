/**
 * Agent 模块 API 层：类型与请求方法的唯一事实源（对齐后端 internal/agent/api 契约）。
 *
 * 字段名直接用后端 JSON snake_case（对齐 api/provider.ts 先例）。
 * ID 序列化为字符串（防 JS 超 2^53 丢精度）——URL 路径参数原样传字符串；
 * 但请求 body 的 model_id / tool_ids 是数值（Go uint64 无 `,string` tag），
 * 提交处用 Number() 转换（踩坑 #8）。
 */
import { del, get, getList, post, put } from '@/utils/request'
import type { PageQuery } from '@/types'

// ---- 类型（对齐 agent/api/schema.go） ----

/** Agent 基础字段（创建 / 更新 / 详情共有；对齐后端 AgentSchema） */
export interface AgentBase {
  id: string
  name: string
  description: string
  model_id: string
  fallback_model_id: string | null
  system_prompt: string
  temperature: number
  max_output_tokens: number | null
  /** 多轮对话携带的最大历史轮数（缺省 10） */
  max_context_turns: number
  /** false=停用（保留配置，新会话被拒） */
  enabled: boolean
  /** RAG 检索注入取回片段数（1-20，默认 3） */
  rag_top_k: number
  /** RAG 检索注入相似度过滤阈值（0-1，默认 0.75） */
  rag_min_similarity: number
  created_at: string
  updated_at: string
}

/** 列表项：基础 + 当页批量现读的聚合列（后端 List withAggregates） */
export interface AgentItem extends AgentBase {
  /** 关联模型展示名；空串 = 悬空引用（模型已被删），展示时 fallback model_id */
  model_name: string
  /** 绑定 MCP 工具数 */
  tool_count: number
  /** 绑定知识库数（RAG 检索注入范围） */
  kb_count: number
}

/** 详情：基础 + 绑定工具 / 知识库 id（空绑定 []） */
export interface AgentDetail extends AgentBase {
  tool_ids: string[]
  knowledge_base_ids: string[]
}

/** 创建 / 整体更新载荷（PUT 全量提交；enabled 必带——漏发会被后端置回启用） */
export interface AgentSaveData {
  name: string
  description?: string
  model_id: number
  system_prompt?: string
  temperature?: number
  max_output_tokens?: number
  max_context_turns?: number
  enabled?: boolean
  tool_ids?: number[]
  knowledge_base_ids?: number[]
  rag_top_k?: number
  rag_min_similarity?: number
}

// ---- 请求方法 ----

/** Agent 列表（偏移分页；HifyTable 数据源） */
export function getAgentList(params?: PageQuery) {
  return getList<AgentItem>('/agents', params)
}

/** Agent 详情（含 tool_ids；编辑弹窗回填用） */
export function getAgent(id: string) {
  return get<AgentDetail>(`/agents/${id}`)
}

export function createAgent(data: AgentSaveData) {
  return post<AgentBase>('/agents', data)
}

export function updateAgent(id: string, data: AgentSaveData) {
  return put<AgentBase>(`/agents/${id}`, data)
}

/** 删除 Agent（软删除：历史会话保留、新会话被拒；204 无响应体） */
export function deleteAgent(id: string) {
  return del<void>(`/agents/${id}`)
}
