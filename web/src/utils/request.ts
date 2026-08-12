import axios, { type AxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'
import type { Result } from '@/types'

// CLAUDE.md《统一响应信封》：{ success, data, error:{code,message,details}, meta }

const request = axios.create({
  baseURL: '/api/v1',
  timeout: 30_000,
  withCredentials: true, // 携带 HttpOnly session cookie
})

// 请求拦截器：session 走 HttpOnly cookie，无需手动加头；保留扩展位
request.interceptors.request.use((config) => config)

// 响应拦截器：只做"信封校验 + 失败统一报错"，自动解包 data 交给下面的 helper。
//   success=false → ElMessage.error(error.message) + reject(RequestError)
//   success=true  / 非信封结构 → 原样放行
// 注意：helper 解包 Result.data 后分页 meta 不再可见；需 meta 的列表端点将来用独立 getList 取 { data, meta }。
request.interceptors.response.use(
  (response) => {
    const result = response.data as Result<unknown>
    if (
      result &&
      typeof result === 'object' &&
      'success' in result &&
      !result.success
    ) {
      const code = result.error?.code ?? 'UNKNOWN'
      const message = result.error?.message ?? '请求失败'
      ElMessage.error(message)
      return Promise.reject(new RequestError(code, message))
    }
    return response
  },
  (error) => {
    // HTTP 层错误（4xx / 5xx），axios 已抛出；error.response.data 为 Result 信封
    const message: string =
      error.response?.data?.error?.message ?? error.message ?? '网络错误'
    if (error.response?.status === 401) {
      // 未登录 / session 失效 → 跳登录（auth 模块落地后接 router.push('/login')）
    }
    ElMessage.error(message)
    return Promise.reject(error)
  },
)

/** 业务错误（携带 error.code，供调用方按 CLAUDE.md 错误码分支处理） */
export class RequestError extends Error {
  constructor(
    public code: string,
    message: string,
  ) {
    super(message)
    this.name = 'RequestError'
  }
}

// ---- 四个语义化 helper：返回值已自动解包为 data ----

export function get<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  return request.get<Result<T>>(url, config).then((res) => res.data.data as T)
}

export function post<T>(
  url: string,
  data?: unknown,
  config?: AxiosRequestConfig,
): Promise<T> {
  return request
    .post<Result<T>>(url, data, config)
    .then((res) => res.data.data as T)
}

export function put<T>(
  url: string,
  data?: unknown,
  config?: AxiosRequestConfig,
): Promise<T> {
  return request
    .put<Result<T>>(url, data, config)
    .then((res) => res.data.data as T)
}

export function del<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  return request.delete<Result<T>>(url, config).then((res) => res.data.data as T)
}

export default request
