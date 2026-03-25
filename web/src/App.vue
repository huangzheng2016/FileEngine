<template>
  <el-container style="height: 100vh">
    <el-header style="background: #545c64; display: flex; align-items: center; padding: 0 20px">
      <h2 style="color: #fff; margin: 0; margin-right: 40px">FileEngine</h2>
      <el-menu mode="horizontal" :router="true" :default-active="route.path"
        background-color="#545c64" text-color="#fff" active-text-color="#ffd04b"
        style="border-bottom: none; flex: 1">
        <el-menu-item index="/files">
          <el-icon><FolderOpened /></el-icon>
          <span>{{ $t('nav.files') }}</span>
        </el-menu-item>
        <el-menu-item index="/filesystems">
          <el-icon><Connection /></el-icon>
          <span>{{ $t('nav.filesystems') }}</span>
        </el-menu-item>
        <el-menu-item index="/models">
          <el-icon><Cpu /></el-icon>
          <span>{{ $t('nav.models') }}</span>
        </el-menu-item>
        <el-menu-item index="/tasks">
          <el-icon><Monitor /></el-icon>
          <span>{{ $t('nav.tasks') }}</span>
        </el-menu-item>
        <el-menu-item index="/logs">
          <el-icon><Document /></el-icon>
          <span>{{ $t('nav.logs') }}</span>
        </el-menu-item>
        <el-menu-item index="/config">
          <el-icon><Setting /></el-icon>
          <span>{{ $t('nav.config') }}</span>
        </el-menu-item>
      </el-menu>
      <el-dropdown @command="switchLang" trigger="click">
        <span style="color: #fff; cursor: pointer; display: flex; align-items: center; gap: 4px; font-size: 14px">
          <el-icon><SwitchFilled /></el-icon>
          {{ currentLangLabel }}
        </span>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="zh-CN" :disabled="locale === 'zh-CN'">中文</el-dropdown-item>
            <el-dropdown-item command="en" :disabled="locale === 'en'">English</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </el-header>
    <el-main style="padding: 20px; overflow: auto">
      <router-view />
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { setLocale } from './i18n'
import { SwitchFilled } from '@element-plus/icons-vue'

const route = useRoute()
const { locale } = useI18n()

const currentLangLabel = computed(() => locale.value === 'zh-CN' ? '中文' : 'EN')

function switchLang(lang: string) {
  setLocale(lang)
  // Reload to re-init Element Plus locale
  window.location.reload()
}
</script>

<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; }
.el-header { height: 60px !important; }
</style>
