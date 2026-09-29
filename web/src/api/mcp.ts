/**
 * MCP 模块 API 层：类型与请求方法的唯一事实源（对齐后端 internal/mcp/api 契约）。
 *
 * 字段名直接用后端 JSON snake_case（对齐 api/provider.ts 先例）。
 * ID 序列化为字符串（防 JS 超 2^53 丢精度）——URL 路径参数原样传字符串；
 * 响应里无外键回传需求，请求体不含 id 字段。
 */
import { del, get, getList, post, put } from '@/utils/request'
import type { PageQuery } from '@/types'

// ---- 类型（对齐 mcp/api/schema.go） ----

/** 传输方式（三选一；stdio 走本地子进程、http/sse 走远程端点） */
export type McpTransport = 'stdio' | 'http' | 'sse'

/** MCP Server 基础字段（创建 / 更新 / 列表 / 详情共有；对齐后端 MCPServerSchema）。
 * env 刻意不在本层：仅详情回传（编辑动线需要），列表摘要不回。 */
export interface McpServerBase {
  id: string
  name: string
  transport: McpTransport
  /** stdio 模式的启动命令（含参数，按空白切分 argv） */
  command: string
  /** http / sse 模式的端点地址 */
  url: string
  /** false=停用（拒绝新绑定与调用；discover/run 管理面诊断不受限） */
  enabled: boolean
  created_at: string
  updated_at: string
}

/** 列表项：基础 + discover 入库的工具数（后端按当页 id 批量计数） */
export interface McpServerItem extends McpServerBase {
  tool_count: number
}

/** MCP 工具（详情内嵌；discover upsert 入库，name 在 server 内唯一） */
export interface McpTool {
  id: string
  /** 所属 server id（字符串化） */
  server_id: string
  name: string
  description: string
  /** 参数 JSON Schema 原样透传（后端不加工；空为 {}） */
  input_schema: Record<string, unknown>
}

/** 详情：基础 + env + 工具清单（DB 读，不连 server；空绑定返 {} / []） */
export interface McpServerDetail extends McpServerBase {
  /** stdio 子进程环境变量；非 stdio 恒 {} */
  env: Record<string, string>
  /** discover 入库的工具（name 升序） */
  tools: McpTool[]
}

/** discover 结果（HTTP 200 + success 字段——连通失败是业务结果不是 HTTP 错误；
 * 连通 + 发现 + upsert 一个动作完成） */
export interface DiscoverResult {
  success: boolean
  latency_ms: number
  /** 本次 upsert 后该 server 的工具总数（失败为 0） */
  tool_count: number
  /** 失败时非空（连接/握手/列工具错误摘要，后端截断） */
  error_message?: string
}

/** 创建 / 整体更新载荷（PUT 全量提交；enabled 必带——漏发会被后端置回启用） */
export interface McpServerSaveData {
  name: string
  transport: McpTransport
  command?: string
  url?: string
  /** 仅 stdio 有意义（其他 transport 存而不用） */
  env?: Record<string, string>
  enabled?: boolean
}

/** 实调工具载荷（arguments 须为 JSON 对象；缺省 = 无参） */
export interface RunToolData {
  tool_name: string
  arguments?: Record<string, unknown>
}

/** 工具调用结果（MCP 返回的 TextContent 多条换行拼接） */
export interface RunToolResult {
  content: string
}

// ---- 请求方法 ----

/** MCP Server 列表（偏移分页；HifyTable 数据源） */
export function getMcpServerList(params?: PageQuery) {
  return getList<McpServerItem>('/mcp-servers', params)
}

/** MCP Server 详情（含 env 与工具清单；编辑弹窗回填用） */
export function getMcpServer(id: string) {
  return get<McpServerDetail>(`/mcp-servers/${id}`)
}

export function createMcpServer(data: McpServerSaveData) {
  return post<McpServerBase>('/mcp-servers', data)
}

export function updateMcpServer(id: string, data: McpServerSaveData) {
  return put<McpServerBase>(`/mcp-servers/${id}`, data)
}

/** 删除 MCP Server（真删：mcp_tools 级联删；有 Agent 绑定 → 409 MCP_SERVER_IN_USE；204 无响应体） */
export function deleteMcpServer(id: string) {
  return del<void>(`/mcp-servers/${id}`)
}

/** 连通测试 + 工具发现 + 入库（一个动作；结果按 success 分支提示，耗时上限 10s 调用方管理 loading） */
export function discoverMcpServer(id: string) {
  return post<DiscoverResult>(`/mcp-servers/${id}/discover`)
}

/** 实调一个工具（管理面诊断：不检查 enabled；工具不在 discover 清单 → 404） */
export function runMcpTool(id: string, data: RunToolData) {
  return post<RunToolResult>(`/mcp-servers/${id}/run`, data)
}
