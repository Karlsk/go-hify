import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import DefaultLayout from '@/layouts/DefaultLayout.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: DefaultLayout,
    redirect: '/chat',
    children: [
      {
        path: 'provider',
        name: 'provider',
        component: () => import('@/views/provider/ProviderList.vue'),
        meta: { title: '模型提供商' },
      },
      {
        path: 'agent',
        name: 'agent',
        component: () => import('@/views/agent/AgentList.vue'),
        meta: { title: 'Agent' },
      },
      {
        path: 'chat',
        name: 'chat',
        component: () => import('@/views/chat/ChatView.vue'),
        meta: { title: '对话' },
      },
    ],
  },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

export default router
