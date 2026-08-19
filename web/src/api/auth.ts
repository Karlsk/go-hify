/**
 * Auth 模块 API 层：类型与请求方法（对齐后端 internal/auth/api 契约）。
 *
 * 会话走 HttpOnly cookie（hify_session，request.ts 已配 withCredentials），
 * token 不经前端代码；login 响应体即用户信息（后端只回 UserSchema，token 在 cookie 里）。
 */
import { get, post } from '@/utils/request'

/** 登录用户（login / register / me 的响应体） */
export interface AuthUser {
  id: string
  username: string
  created_at: string
}

export interface LoginData {
  username: string
  password: string
}

export interface RegisterData {
  username: string
  password: string
}

/** 登录（200，成功即 Set-Cookie；凭据错误 401 由拦截器弹） */
export function login(data: LoginData) {
  // skipAuthHandler：登录页自身的 401 是业务结果（凭据错误），不触发「清身份 + 跳登录」
  return post<AuthUser>('/auth/login', data, { skipAuthHandler: true })
}

/** 注册（201；用户名已存在 409 由拦截器弹） */
export function register(data: RegisterData) {
  return post<AuthUser>('/auth/register', data)
}

/** 注销（204 清 cookie；幂等，调用方失败也不阻断本地清理） */
export function logout() {
  return post<null>('/auth/logout')
}

/** 当前登录用户（401 = 未登录 / session 失效；引导调用：静默且不跳转） */
export function fetchMe() {
  return get<AuthUser>('/auth/me', { skipAuthHandler: true, skipErrorToast: true })
}
