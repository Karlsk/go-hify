/**
 * Auth 会话 store（Pinia）：登录用户的唯一事实源。
 *
 * 身份引导：路由守卫首次导航调 ensureReady（fetchMe，401 静默归 null——
 * 未登录是正常态不是错误，不该弹 toast）。
 * 失效路径：request.ts 拦截器 401 时调 clear（带 skipAuthHandler 标记的调用除外）。
 */
import { defineStore } from 'pinia'
import {
  fetchMe,
  login as loginApi,
  logout as logoutApi,
  type AuthUser,
  type LoginData,
} from '@/api/auth'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    user: null as AuthUser | null,
    /** 引导完成标记：ensureReady 只发一次 fetchMe */
    ready: false,
  }),
  getters: {
    isLoggedIn: (state) => state.user !== null,
    username: (state) => state.user?.username ?? '',
    avatarLetter: (state) =>
      state.user ? state.user.username.charAt(0).toUpperCase() : '',
  },
  actions: {
    /** 一次性身份引导：401（未登录 / session 失效）静默归 null */
    async ensureReady() {
      if (this.ready) return
      this.ready = true
      try {
        this.user = await fetchMe()
      } catch {
        this.user = null
      }
    },
    /** 登录并写入身份（cookie 已由后端 Set-Cookie 写入） */
    async login(data: LoginData) {
      this.user = await loginApi(data)
    },
    /** 只清本地身份（cookie 是后端的事，不在此处理） */
    clear() {
      this.user = null
    },
    /** 注销：best-effort 调后端清 session；失败也清本地（Redis TTL 7 天自然过期兜底） */
    async logout() {
      try {
        await logoutApi()
      } finally {
        this.clear()
      }
    },
  },
})
