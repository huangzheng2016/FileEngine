import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import { authed } from '@/api/http'

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    // render without the shell (login, error pages)
    bare?: boolean
    public?: boolean
    // detail pages: path of the list they belong to, shown as a link before the title
    parent?: string
  }
}

const routes: RouteRecordRaw[] = [
  { path: '/login', component: () => import('@/pages/Login.vue'), meta: { bare: true, public: true, title: '登录' } },
  { path: '/', redirect: '/sessions' },
  { path: '/sessions', component: () => import('@/pages/SessionList.vue') },
  {
    path: '/sessions/:id',
    component: () => import('@/pages/SessionDetail.vue'),
    meta: { parent: '/sessions', title: '整理任务' },
  },
  { path: '/memory', component: () => import('@/pages/MemoryList.vue') },
  { path: '/settings/models', component: () => import('@/pages/ModelList.vue') },
  { path: '/settings/storage', component: () => import('@/pages/StorageList.vue') },
  { path: '/settings/agent', component: () => import('@/pages/AgentSettings.vue') },
  { path: '/:rest(.*)*', component: () => import('@/pages/NotFound.vue'), meta: { title: '页面不存在' } },
]

export const router = createRouter({ history: createWebHashHistory(), routes })

let signedIn: Promise<boolean> | null = null

export function resetSignIn() {
  signedIn = null
}

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  signedIn ??= authed()
  if (await signedIn) return true
  signedIn = null
  return { path: '/login', query: { next: to.fullPath } }
})
