import { createRouter, createWebHashHistory } from 'vue-router'

const routes = [
  { path: '/', redirect: '/tasks' },
  { path: '/tasks', name: 'tasks', component: () => import('../views/SessionCenter.vue'), meta: { title: 'nav.tasks' } },
  // Deep-link compat: /logs?session=X lands on the session center.
  { path: '/logs', redirect: (to: any) => ({ name: 'tasks', query: to.query }) },
  { path: '/files', name: 'files', component: () => import('../views/FileBrowser.vue'), meta: { title: 'nav.files' } },
  {
    path: '/settings',
    component: () => import('../layouts/SettingsLayout.vue'),
    children: [
      { path: '', redirect: '/settings/models' },
      { path: 'models', name: 'settings-models', component: () => import('../views/ModelManager.vue'), meta: { title: 'nav.models' } },
      { path: 'filesystems', name: 'settings-filesystems', component: () => import('../views/FilesystemManager.vue'), meta: { title: 'nav.filesystems' } },
      { path: 'config', name: 'settings-config', component: () => import('../views/ConfigManager.vue'), meta: { title: 'nav.config' } },
    ],
  },
  // Pre-IA paths keep working as redirects.
  { path: '/models', redirect: '/settings/models' },
  { path: '/filesystems', redirect: '/settings/filesystems' },
  { path: '/config', redirect: '/settings/config' },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

export default router
