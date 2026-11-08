// PROJECT FILE: the only template file a project edits. Everything else in this
// directory is copied verbatim and updated by re-copying from the skill.
import { computed } from 'vue'
import type { IconName } from './icons'

export const app = {
  name: 'FileEngine',
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
      { to: '/settings/agent', label: '智能体配置', icon: 'sliders' },
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

export function useShellUser() {
  return {
    name: computed(() => ''),
    canSee: (item: NavItem) => !!item,
    logout: async () => {},
  }
}
