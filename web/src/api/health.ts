import { get } from '@/utils/request'

/**
 * 探活：GET /health。
 * /health 在 /api/v1 之外、不走鉴权（CLAUDE.md《路径与版本》），
 * 故覆盖 request 实例的 baseURL（'/api/v1'）直接打 /health。
 * 当前后端用 respond.OK 返回 data 为字符串（"Hify is running"）；
 * 待 db/redis 连通性探测落地（503）后响应结构可能调整，届时同步更新返回类型。
 */
export function getHealth(): Promise<string> {
  return get<string>('/health', { baseURL: '' })
}
