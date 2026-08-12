import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/provider' },
  {
    path: '/provider',
    name: 'provider',
    component: () => import('@/views/provider/ProviderList.vue'),
  },
  {
    path: '/agent',
    name: 'agent',
    component: () => import('@/views/agent/AgentList.vue'),
  },
  {
    path: '/chat',
    name: 'chat',
    component: () => import('@/views/chat/ChatView.vue'),
  },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

export default router
