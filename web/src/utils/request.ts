import axios, { type AxiosResponse } from 'axios'
import { ElMessage } from 'element-plus'
import type { Result } from '@/types'

const request = axios.create({
  baseURL: '/api/v1',
  timeout: 30_000,
})

// 请求拦截器：鉴权走 HttpOnly cookie（无需手动加头），此处保留扩展位
request.interceptors.request.use((config) => config)

// 响应拦截器：拆 Result 信封，success=false 统一抛出
request.interceptors.response.use(
  (response: AxiosResponse<Result<unknown>>) => {
    const result = response.data
    // 非信封结构（如文件流）直接放行
    if (result === null || typeof result !== 'object' || !('success' in result)) {
      return response
    }
    if (!result.success) {
      const message = result.error?.message ?? '请求失败'
      ElMessage.error(message)
      return Promise.reject(
        new RequestError(result.error?.code ?? 'UNKNOWN', message),
      )
    }
    return response
  },
  (error) => {
    // HTTP 层错误（4xx / 5xx）
    const status: number | undefined = error.response?.status
    const message: string =
      error.response?.data?.error?.message ?? error.message ?? '网络错误'
    if (status === 401) {
      // 未登录 / session 失效 → 跳登录（auth 模块落地后接 router.push('/login')）
    }
    ElMessage.error(message)
    return Promise.reject(error)
  },
)

/** 业务错误（携带 error.code，供调用方按 CLAUDE.md 错误码分支） */
export class RequestError extends Error {
  constructor(
    public code: string,
    message: string,
  ) {
    super(message)
    this.name = 'RequestError'
  }
}

export default request
