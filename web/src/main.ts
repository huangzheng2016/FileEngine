import { createApp } from 'vue'
import MainLayout from '@/base/MainLayout.vue'
import { setUnauthorizedHandler } from '@/api/http'
import { resetSignIn, router } from '@/router'
import { installTooltips } from '@/base/tooltip'
import '@/styles/tokens.css'
import '@/styles/base.css'

const app = createApp(MainLayout).use(router)
installTooltips()

setUnauthorizedHandler(() => {
  resetSignIn()
  const current = router.currentRoute.value
  if (!current.meta.public) void router.replace({ path: '/login', query: { next: current.fullPath } })
})

// Wait for the first navigation so the shell never flashes before a bare route.
void router.isReady().then(() => app.mount('#app'))
