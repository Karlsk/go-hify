import axios, { type AxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'
import type { PageQuery, PageResult, Result } from '@/types'

// CLAUDE.md《统一响应信封》：{ success, data, error:{code,message,details}, meta }

// 内部标记（auth 引导类调用专用）：随 config 传入，拦截器按标记降级处理
declare module 'axios' {
  export interface AxiosRequestConfig {
    /** 401 不触发「清身份 + 跳登录」（fetchMe 引导 / login 自身的 401 是业务结果） */
    skipAuthHandler?: boolean
    /** 失败不弹 ElMessage（冷启动身份引导静默——未登录是正常态不是错误） */
    skipErrorToast?: boolean
  }
}

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
// 注意：get/post/put/del 解包 Result.data 后分页 meta 不再可见；
// 需 meta 的列表端点用 getList 取 { list, meta }（HifyTable 的数据源）。
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
    const config = error.config
    const message: string =
      error.response?.data?.error?.message ?? error.message ?? '网络错误'
    if (error.response?.status === 401 && !config?.skipAuthHandler) {
      // session 失效 → 清身份 + 跳登录（带 redirect 回跳）。
      // 动态 import：避免 request → stores/router 静态环（stores/auth → api/auth → 本文件）。
      void import('@/stores/auth').then(({ useAuthStore }) => useAuthStore().clear())
      void import('@/router').then(({ default: router }) => {
        if (router.currentRoute.value.name !== 'login') {
          void router.push({
            name: 'login',
            query: { redirect: router.currentRoute.value.fullPath },
          })
        }
      })
    }
    if (!config?.skipErrorToast) {
      ElMessage.error(message)
    }
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

/** 偏移分页列表端点：解包 data + meta，返回 PageResult（HifyTable 数据源） */
export async function getList<T>(
  url: string,
  params?: PageQuery,
  config?: AxiosRequestConfig,
): Promise<PageResult<T>> {
  const res = await request.get<Result<T[]>>(url, { ...config, params })
  const list = res.data.data ?? []
  const meta = res.data.meta
  return {
    list,
    total: meta?.total ?? list.length,
    page: meta?.page ?? params?.page ?? 1,
    pageSize: meta?.page_size ?? params?.page_size ?? 20,
  }
}

export default request
