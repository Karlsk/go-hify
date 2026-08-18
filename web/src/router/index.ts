import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    /** 页面标题：面包屑当前项 + document.title 的唯一来源 */
    title: string
  }
}

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/provider' },
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
    meta: { title: '设计系统' },
  },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

router.afterEach((to) => {
  document.title = `${to.meta.title} · Hify`
})

export default router
