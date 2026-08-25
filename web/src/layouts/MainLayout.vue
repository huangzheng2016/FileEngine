<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElIcon } from 'element-plus'
import {
  FolderOpened, Monitor, Setting,
  Moon, Sunny, Fold, Expand, SwitchFilled, Folder,
} from '@element-plus/icons-vue'
import { setLocale } from '../i18n'

const route = useRoute()
const { t, locale } = useI18n()
const collapsed = ref(false)

const isDark = ref(document.documentElement.classList.contains('dark'))

function toggleTheme() {
  document.documentElement.classList.toggle('dark')
  isDark.value = document.documentElement.classList.contains('dark')
  localStorage.setItem('fe_theme', isDark.value ? 'dark' : 'light')
}

const navItems = computed(() => [
  { name: 'tasks', label: t('nav.tasks'), icon: Monitor, path: '/tasks' },
  { name: 'files', label: t('nav.files'), icon: FolderOpened, path: '/files' },
  { name: 'settings', label: t('nav.settings'), icon: Setting, path: '/settings' },
])

// Settings section stays active for any /settings/* child route.
const activeName = computed(() => (route.path.startsWith('/settings') ? 'settings' : (route.name as string)))
const pageTitle = computed(() => (route.meta.title ? t(route.meta.title as string) : ''))

const currentLangLabel = computed(() => (locale.value === 'zh-CN' ? '中文' : 'EN'))

function switchLang(lang: string) {
  setLocale(lang)
}
</script>

<template>
  <div class="shell-layout" :class="{ 'is-collapsed': collapsed }">
    <aside class="shell-sidebar">
      <div class="brand">
        <div class="brand-mark">
          <ElIcon :size="20"><Folder /></ElIcon>
        </div>
        <transition name="fade">
          <div v-if="!collapsed" class="brand-text">
            <span class="brand-name">FileEngine</span>
            <span class="brand-sub">{{ t('app.tagline') }}</span>
          </div>
        </transition>
      </div>

      <nav class="nav-list">
        <el-tooltip
          v-for="item in navItems"
          :key="item.name"
          :content="item.label"
          placement="right"
          :disabled="!collapsed"
        >
          <router-link
            :to="item.path"
            class="nav-item"
            :class="{ 'is-active': activeName === item.name }"
          >
            <ElIcon :size="18"><component :is="item.icon" /></ElIcon>
            <span v-if="!collapsed" class="nav-label">{{ item.label }}</span>
          </router-link>
        </el-tooltip>
      </nav>

      <el-tooltip :content="collapsed ? t('nav.expand') : t('nav.collapse')" placement="right">
        <button class="rail-toggle" :aria-label="collapsed ? t('nav.expand') : t('nav.collapse')" @click="collapsed = !collapsed">
          <ElIcon :size="16">
            <Fold v-if="!collapsed" />
            <Expand v-else />
          </ElIcon>
        </button>
      </el-tooltip>
    </aside>

    <main class="shell-main">
      <header class="shell-header">
        <div class="header-title-block">
          <span class="page-eyebrow">FileEngine</span>
          <h1 class="page-title">{{ pageTitle }}</h1>
        </div>
        <div class="header-actions">
          <el-dropdown trigger="click" @command="switchLang">
            <button class="toolbar-button toolbar-button--lang">
              <ElIcon :size="16"><SwitchFilled /></ElIcon>
              <span>{{ currentLangLabel }}</span>
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="zh-CN" :disabled="locale === 'zh-CN'">中文</el-dropdown-item>
                <el-dropdown-item command="en" :disabled="locale === 'en'">English</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <button class="toolbar-button" @click="toggleTheme">
            <ElIcon :size="18">
              <Moon v-if="!isDark" />
              <Sunny v-else />
            </ElIcon>
          </button>
        </div>
      </header>

      <router-view v-slot="{ Component }">
        <transition name="page-fade">
          <component :is="Component" />
        </transition>
      </router-view>
    </main>
  </div>
</template>

<style scoped>
.shell-layout {
  position: relative;
  z-index: 1;
  display: grid;
  grid-template-columns: 248px minmax(0, 1fr);
  gap: 24px;
  padding: 24px;
  min-height: 100vh;
  transition: grid-template-columns var(--duration-base) var(--ease-spring);
}

.shell-layout.is-collapsed {
  grid-template-columns: 72px minmax(0, 1fr);
}

/* ===== Sidebar ===== */
.shell-sidebar {
  position: sticky;
  top: 24px;
  align-self: start;
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: 22px 16px;
  height: calc(100vh - 48px);
  border-radius: var(--radius-2xl);
  background: var(--aside-bg);
  border: 1px solid var(--border-soft);
  box-shadow: var(--shadow-soft), var(--inner-highlight);
  backdrop-filter: blur(18px);
  overflow: hidden;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 4px 6px 14px;
  border-bottom: 1px solid var(--border-soft);
}

.brand-mark {
  width: 40px;
  height: 40px;
  display: grid;
  place-items: center;
  border-radius: var(--radius-md);
  background: linear-gradient(135deg, var(--accent-soft), rgba(255, 255, 255, 0.18));
  border: 1px solid var(--floating-border);
  color: var(--accent);
  flex-shrink: 0;
}

.brand-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  overflow: hidden;
}

.brand-name {
  font-weight: 800;
  font-size: 15px;
  letter-spacing: -0.01em;
  color: var(--text-primary);
}

.brand-sub {
  font-size: 11px;
  color: var(--text-muted);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  white-space: nowrap;
}

.nav-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  font-size: 14px;
  font-weight: 500;
  color: var(--text-secondary);
  text-decoration: none;
  border: 1px solid transparent;
  border-left: 3px solid transparent;
  transition: all var(--duration-fast) var(--ease-spring);
}

.nav-item:hover {
  background: var(--accent-soft);
  color: var(--accent);
}

.nav-item.is-active {
  background: linear-gradient(135deg, var(--accent-soft), rgba(255, 255, 255, 0.06));
  color: var(--accent);
  border-color: var(--floating-border);
  border-left-color: var(--accent);
  font-weight: 600;
}

.nav-label {
  white-space: nowrap;
}

.rail-toggle {
  align-self: stretch;
  display: grid;
  place-items: center;
  height: 36px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-soft);
  background: var(--input-bg);
  color: var(--text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-spring);
}

.rail-toggle:hover {
  background: var(--input-bg-hover);
  color: var(--accent);
  border-color: var(--floating-border);
}

/* ===== Main ===== */
.shell-main {
  display: flex;
  flex-direction: column;
  gap: 20px;
  min-width: 0;
  /* Fill-viewport pages (layouts/fill-and-scroll.md) need this floor.
     48px = shell-layout top+bottom padding (24px * 2); keep in sync. */
  min-height: calc(100vh - 48px);
}

.shell-header {
  position: sticky;
  top: 24px;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 24px;
  border-radius: var(--radius-xl);
  background: var(--header-bg);
  border: 1px solid var(--border-soft);
  box-shadow: var(--shadow-soft), var(--inner-highlight);
  backdrop-filter: blur(18px);
}

.header-title-block {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.page-eyebrow {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.page-title {
  margin: 0;
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--text-primary);
}

.header-actions {
  display: flex;
  gap: 8px;
}

.toolbar-button {
  width: 40px;
  height: 40px;
  display: grid;
  place-items: center;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-soft);
  background: var(--input-bg);
  color: var(--text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-spring);
}

.toolbar-button--lang {
  width: auto;
  padding: 0 12px;
  display: inline-flex;
  gap: 6px;
  align-items: center;
  font-family: var(--font-display);
  font-size: 13px;
  font-weight: 600;
}

.toolbar-button:hover {
  background: var(--input-bg-hover);
  color: var(--accent);
  border-color: var(--floating-border);
  transform: translateY(-1px);
}

/* ===== Transitions ===== */
.fade-enter-active,
.fade-leave-active {
  transition: opacity var(--duration-fast) var(--ease-spring);
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

.page-fade-enter-active,
.page-fade-leave-active {
  transition: opacity var(--duration-fast) var(--ease-spring),
              transform var(--duration-base) var(--ease-spring);
}

/* The leaving view disappears at once (no stacking, no stuck transition);
   only the entering view fades in. */
.page-fade-leave-active {
  display: none;
}

.page-fade-enter-from {
  opacity: 0;
  transform: translateY(8px);
}

.page-fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

/* ===== Responsive ===== */
@media (max-width: 960px) {
  .shell-layout {
    grid-template-columns: 72px minmax(0, 1fr);
    padding: 16px;
    gap: 16px;
  }
  .nav-label,
  .brand-text {
    display: none;
  }
}

@media (max-width: 640px) {
  .shell-layout {
    grid-template-columns: 1fr;
    padding: 14px;
  }
  .shell-sidebar {
    position: relative;
    top: 0;
    height: auto;
  }
}
</style>
