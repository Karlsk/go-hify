/**
 * Workflow 模块 API 层：类型与请求方法的唯一事实源（对齐后端 internal/workflow/api 契约）。
 *
 * 字段名直接用后端 JSON snake_case（对齐 api/agent.ts 先例）。
 * ID 序列化为字符串（防 JS 超 2^53 丢精度）——URL 路径参数原样传字符串；
 * 节点 config 内的外键（model_id / workflow_id）同样保持字符串形态（后端 NodeConfig
 * 带 `,string` tag，数值形 400——schema.go 与手测文档一致，FR-011 2026-09-22 修正）。
 *
 * spec 010 起接详情与更新：GET /workflows/{id}（详情渲染 / 编辑回填）、
 * PUT /workflows/{id}（整图替换；请求体不带 type——后端 Type *string 携带即拒，
 * 同值也拒）。仍未接 execute（执行入口与历史）。
 */
import { del, get, getList, post, put } from '@/utils/request'
import type { PageQuery } from '@/types'

// ---- 类型（对齐 workflow/api/schema.go） ----

/** 工作流状态（db_model 决策 #5：三态小写；只能经 publish/disable 动作改变） */
export type WorkflowStatus = 'draft' | 'published' | 'disabled'

/** 工作流分型（spec 08：chat = 对话管道终答 / task = 可组合任务函数；创建后不可改） */
export type WorkflowType = 'chat' | 'task'

/**
 * 列表行（后端 WorkflowSummarySchema；updated_at / input_schema / output_schema
 * 也在响应里，本篇不消费、省略声明）。
 */
export interface WorkflowItem {
  id: string
  name: string
  description: string | null
  type: WorkflowType
  status: WorkflowStatus
  created_at: string
}

/** task 型结构化 I/O 契约的字段（spec 08 §4.5 简化形态） */
export interface SchemaField {
  name: string
  /** string / number / boolean */
  type: 'string' | 'number' | 'boolean'
  required: boolean
  description?: string
}

/**
 * 创建载荷的节点元素。type 为后端 NodeType 全集（7 类）字符串——前端画布面板
 * 只提供 5 类（llm/end/condition/api/workflow），手写 JSON 可携带其余（透传）。
 * config 外键字符串保形（见文件头）。
 */
export interface WorkflowNodeData {
  key: string
  type: string
  name?: string
  config: Record<string, unknown>
}

/** 创建载荷的连线元素；condition 缺省 = 无条件直走 */
export interface WorkflowEdgeData {
  source_node_key: string
  target_node_key: string
  condition?: string
}

/** 创建请求体（POST /workflows；schema 仅 task 型携带，chat 型整体不带——强不变量） */
export interface CreateWorkflowData {
  name: string
  description?: string
  type: WorkflowType
  start_node_key: string
  nodes: WorkflowNodeData[]
  edges: WorkflowEdgeData[]
  input_schema?: SchemaField[]
  output_schema?: SchemaField[]
}

// ---- 类型（spec 010：详情 / 更新，contracts §2/§3/§6 逐字对齐） ----

/** 详情响应的节点（后端 NodeSchema：config 原样透传——库里 JSON 原文，未知键含在内） */
export interface WorkflowDetailNode {
  key: string
  /** 后端 7 类全集字符串（画布面板只提供 5 类，tool / knowledge_retrieval 照渲染） */
  type: string
  /** 后端恒序列化；空串合法（detailToGraphConfig 转换时省略键） */
  name: string
  config: Record<string, unknown>
}

/** 详情响应的连线（后端 EdgeSchema：condition null = 无条件直走） */
export interface WorkflowDetailEdge {
  source_node_key: string
  target_node_key: string
  condition: string | null
}

/** 详情响应（后端 WorkflowDetailSchema：摘要 + 图组装）；GET / PUT 均返回此形 */
export interface WorkflowDetail {
  id: string
  name: string
  /** 后端 string 恒序列化（空串 ≠ null） */
  description: string
  type: WorkflowType
  status: WorkflowStatus
  /** null = 未声明（chat 型恒 null） */
  input_schema: SchemaField[] | null
  output_schema: SchemaField[] | null
  created_at: string
  updated_at: string
  start_node_key: string
  nodes: WorkflowDetailNode[]
  edges: WorkflowDetailEdge[]
}

/**
 * 更新请求体（PUT /workflows/:id，整图替换）——**不含 type 键**：后端
 * Type *string 携带即拒（同值也拒），本类型无 type 字段是第一重闸
 * （组装层闸见 graph.ts buildUpdatePayload）。仅 task 型携带 schema。
 */
export interface UpdateWorkflowData {
  name: string
  description: string
  start_node_key: string
  nodes: WorkflowNodeData[]
  edges: WorkflowEdgeData[]
  input_schema?: SchemaField[]
  output_schema?: SchemaField[]
}

// ---- 请求方法 ----

/** 工作流列表（偏移分页；HifyTable 数据源 / 子工作流下拉前端过滤 task 型） */
export function getWorkflowList(params?: PageQuery) {
  return getList<WorkflowItem>('/workflows', params)
}

export function createWorkflow(data: CreateWorkflowData) {
  return post<WorkflowItem>('/workflows', data)
}

/** 删除工作流（204 无响应体；被 Agent 绑定时 409 WORKFLOW_IN_USE） */
export function deleteWorkflow(id: string) {
  return del<void>(`/workflows/${id}`)
}

/** 发布：draft/disabled → published（幂等） */
export function publishWorkflow(id: string) {
  return post<WorkflowItem>(`/workflows/${id}/publish`)
}

/** 停用：published → disabled（幂等；绑定 agent 的会话将不可用） */
export function disableWorkflow(id: string) {
  return post<WorkflowItem>(`/workflows/${id}/disable`)
}

/** 详情（GET /workflows/{id}）：详情页渲染 / 编辑页回填（spec 010） */
export function getWorkflowDetail(id: string) {
  return get<WorkflowDetail>(`/workflows/${id}`)
}

/** 更新（PUT /workflows/{id}）：整图替换、后保存者覆盖；编辑不降级 status（spec 010） */
export function updateWorkflow(id: string, data: UpdateWorkflowData) {
  return put<WorkflowDetail>(`/workflows/${id}`, data)
}
