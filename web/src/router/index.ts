import { createRouter, createWebHashHistory } from 'vue-router'

const routes = [
  { path: '/', redirect: '/files' },
  { path: '/files', name: 'files', component: () => import('../views/FileBrowser.vue') },
  { path: '/filesystems', name: 'filesystems', component: () => import('../views/FilesystemManager.vue') },
  { path: '/models', name: 'models', component: () => import('../views/ModelManager.vue') },
  { path: '/config', name: 'config', component: () => import('../views/ConfigManager.vue') },
  { path: '/tasks', name: 'tasks', component: () => import('../views/TaskMonitor.vue') },
  { path: '/logs', name: 'logs', component: () => import('../views/AgentLogs.vue') },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

export default router
