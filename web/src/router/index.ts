import { createRouter, createWebHashHistory } from 'vue-router'

const routes = [
  { path: '/', redirect: '/files' },
  { path: '/files', name: 'files', component: () => import('../views/FileBrowser.vue'), meta: { title: 'nav.files' } },
  { path: '/filesystems', name: 'filesystems', component: () => import('../views/FilesystemManager.vue'), meta: { title: 'nav.filesystems' } },
  { path: '/models', name: 'models', component: () => import('../views/ModelManager.vue'), meta: { title: 'nav.models' } },
  { path: '/config', name: 'config', component: () => import('../views/ConfigManager.vue'), meta: { title: 'nav.config' } },
  { path: '/tasks', name: 'tasks', component: () => import('../views/TaskMonitor.vue'), meta: { title: 'nav.tasks' } },
  { path: '/logs', name: 'logs', component: () => import('../views/AgentLogs.vue'), meta: { title: 'nav.logs' } },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

export default router
