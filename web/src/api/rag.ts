/**
 * RAG 模块 API 层：类型与请求方法的唯一事实源（对齐后端 internal/rag/api 契约）。
 *
 * 字段名直接用后端 JSON snake_case（对齐 api/provider.ts 先例）。
 * ID 序列化为字符串（防 JS 超 2^53 丢精度）——URL 路径参数原样传字符串；
 * 但请求 body 的外键是数值（Go uint64 无 `,string` tag，传字符串会 400），
 * 调用处用 Number() 转换（agent.ts 踩坑 #8）。
 */
import { del, get, getCursorList, getList, post, put } from '@/utils/request'
import type { PageQuery } from '@/types'

// ---- 类型（对齐 rag/api/schema.go） ----

/** 文档状态枚举（DB CHECK 约束：pending/processing/ready/failed） */
export type DocumentStatus = 'pending' | 'processing' | 'ready' | 'failed'

/** 知识库列表项（聚合列 + 基础字段） */
export interface KnowledgeBaseItem {
  id: string
  name: string
  description: string
  embedding_model_id: string
  embedding_model_name: string
  enabled: boolean
  document_count: number
  created_at: string
  updated_at: string
}

/** 知识库详情（含 document_count，用于详情页标题） */
export interface KnowledgeBaseDetail extends KnowledgeBaseItem {}

/** 创建知识库请求 */
export interface CreateKnowledgeBaseData {
  name: string
  description?: string
  embedding_model_id: number
}

/** 更新知识库请求（PUT 全量；enabled 必带——漏发会被后端置回启用） */
export interface UpdateKnowledgeBaseData {
  name: string
  description?: string
  enabled: boolean
}

/** 文档列表项 */
export interface DocumentItem {
  id: string
  knowledge_base_id: string
  name: string
  file_type: string
  file_size: number
  status: DocumentStatus
  chunk_count: number
  error_message: string
  created_at: string
  updated_at: string
}

/** 文档详情（含 content 原文） */
export interface DocumentDetail extends DocumentItem {
  content: string
}

/** 上传文档响应（202 Accepted，status=pending） */
export interface UploadDocumentResult {
  id: string
  name: string
  file_type: string
  file_size: number
  status: DocumentStatus
}

// ---- knowledge_bases 请求方法 ----

/** 知识库列表（偏移分页；HifyTable 数据源） */
export function getKnowledgeBaseList(params?: PageQuery & { name?: string }) {
  return getList<KnowledgeBaseItem>('/knowledge-bases', params)
}

/** 知识库详情（含 document_count） */
export function getKnowledgeBase(id: string) {
  return get<KnowledgeBaseDetail>(`/knowledge-bases/${id}`)
}

export function createKnowledgeBase(data: CreateKnowledgeBaseData) {
  return post<KnowledgeBaseItem>('/knowledge-bases', data)
}

export function updateKnowledgeBase(id: string, data: UpdateKnowledgeBaseData) {
  return put<KnowledgeBaseItem>(`/knowledge-bases/${id}`, data)
}

/** 删除知识库（硬删；有文档时 409） */
export function deleteKnowledgeBase(id: string) {
  return del<void>(`/knowledge-bases/${id}`)
}

// ---- documents 请求方法 ----

/** 文档列表（游标分页；轮询数据源） */
export function getDocumentList(kbId: string, params?: { limit?: number; cursor?: string }) {
  return getCursorList<DocumentItem>(`/knowledge-bases/${kbId}/documents`, params)
}

/** 文档详情（含 content 原文） */
export function getDocument(id: string) {
  return get<DocumentDetail>(`/documents/${id}`)
}

/** 上传文档（multipart/form-data；返回 202 Accepted） */
export function uploadDocument(kbId: string, file: File, name?: string) {
  const formData = new FormData()
  formData.append('file', file)
  if (name) {
    formData.append('name', name)
  }
  return post<UploadDocumentResult>(`/knowledge-bases/${kbId}/documents`, formData)
}

/** 删除文档（软删 + 同事务硬删 chunks） */
export function deleteDocument(id: string) {
  return del<void>(`/documents/${id}`)
}

/** 重建索引（202：事务删 chunks + 置 pending 重跑管线） */
export function reindexDocument(id: string) {
  return post<DocumentItem>(`/documents/${id}/reindex`)
}
