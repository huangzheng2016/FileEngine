// PROJECT FILE: the only template file a project edits. Everything else in this
// directory is copied verbatim and updated by re-copying from the skill.
import { computed, ref } from 'vue'
import { http } from '@/api/http'
import { resetSignIn, router } from '@/router'
import type { IconName } from './icons'
import { toast } from './toast'

export const app = {
  name: 'FileEngine',
  logo: '/favicon.svg',
  // prefix for every localStorage key the shell and templates write
  storagePrefix: 'fe',
}

export interface NavItem {
  to: string
  label: string
  icon: IconName
}

export interface NavGroup {
  key: string
  label: string
  icon: IconName
  children: NavItem[]
}

export type NavEntry = NavItem | NavGroup

// Page titles come from here (or route meta.title). Keep labels short; they are also the page title.
export const nav: NavEntry[] = [
  { to: '/sessions', label: '整理任务', icon: 'folder' },
  { to: '/memory', label: '记忆', icon: 'bookmark' },
  {
    key: 'settings',
    label: '设置',
    icon: 'settings',
    children: [
      { to: '/settings/models', label: '模型', icon: 'box' },
      { to: '/settings/storage', label: '存储', icon: 'database' },
      { to: '/settings/agent', label: '助手', icon: 'sliders' },
    ],
  },
]

export interface NavBadge {
  count: number
  tip: string
}

// Nothing in FileEngine waits on the user in a way a nav count would help with.
export async function loadNavBadges(signal?: AbortSignal): Promise<Record<string, NavBadge>> {
  signal?.throwIfAborted()
  return {}
}

// The desktop window is signed in by its start URL and has no token to sign back in with.
const desktop = ref<boolean | null>(null)

function probeDesktop() {
  if (desktop.value !== null) return
  desktop.value = false
  http
    .get<{ desktop?: boolean }>('/auth/me', { skipAuthRedirect: true })
    .then((r) => (desktop.value = r.data.desktop === true))
    .catch(() => {})
}

export function useShellUser() {
  probeDesktop()
  return {
    name: computed(() => (desktop.value ? '' : '已登录')),
    canSee: (item: NavItem) => !!item,
    logout: async () => {
      try {
        await http.post('/auth/logout', undefined, { skipAuthRedirect: true })
      } catch (e) {
        toast.error('退出失败', e)
        return
      }
      resetSignIn()
      await router.replace('/login')
    },
  }
}
