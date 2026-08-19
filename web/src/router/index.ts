import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    /** 页面标题：面包屑当前项 + document.title 的唯一来源 */
    title: string
    /** 无 chrome 布局（登录 / 注册）：App.vue 不渲染侧边栏 + 顶栏，且免登录 */
    bare?: boolean
    /** 免登录但保留 chrome（/design 专用：不调 API 的设计走查页） */
    public?: boolean
  }
}

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/provider' },
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/auth/LoginView.vue'),
    meta: { title: '登录', bare: true },
  },
  {
    path: '/register',
    name: 'register',
    component: () => import('@/views/auth/RegisterView.vue'),
    meta: { title: '注册', bare: true },
  },
  {
    path: '/provider',
    name: 'provider',
    component: () => import('@/views/provider/ProviderList.vue'),
    meta: { title: '提供商管理' },
  },
  {
    path: '/agent',
    name: 'agent',
    component: () => import('@/views/agent/AgentList.vue'),
    meta: { title: 'Agent 管理' },
  },
  {
    path: '/chat',
    name: 'chat',
    component: () => import('@/views/chat/ChatView.vue'),
    meta: { title: '对话' },
  },
  {
    // 设计系统 token 预览与验收页（不进侧边栏菜单，直接访问 URL）
    path: '/design',
    name: 'design',
    component: () => import('@/views/design/DesignTokens.vue'),
    meta: { title: '设计系统', public: true },
  },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

/** 登录后回跳目标：仅接受站内路径（/ 开头），防 open-redirect */
function redirectTarget(redirect: unknown): string {
  return typeof redirect === 'string' && redirect.startsWith('/') ? redirect : '/'
}

// 登录守卫：bare = 登录/注册页（已登录者直接进系统）；public = 免登录保留 chrome（/design）；
// 其余一律要求登录，未登录带 redirect 跳登录页。
// 动态 import stores/auth：router 顶层保持零业务依赖（request → router 静态边因此无环）。
router.beforeEach(async (to) => {
  if (to.meta.public && !to.meta.bare) return true
  const { useAuthStore } = await import('@/stores/auth')
  const auth = useAuthStore()
  await auth.ensureReady()
  if (to.meta.bare) {
    return auth.isLoggedIn ? redirectTarget(to.query.redirect) : true
  }
  if (!auth.isLoggedIn) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  return true
})

router.afterEach((to) => {
  document.title = `${to.meta.title} · Hify`
})

export default router
