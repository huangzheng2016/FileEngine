<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'
import MainLayout from './layouts/MainLayout.vue'

const { locale } = useI18n()

// Element Plus locale follows the i18n locale reactively — no page reload needed.
const elLocale = computed(() => (locale.value === 'zh-CN' ? zhCn : en))

onMounted(() => {
  const saved = localStorage.getItem('fe_theme')
  if (saved === 'dark') {
    document.documentElement.classList.add('dark')
  }
})
</script>

<template>
  <el-config-provider :locale="elLocale">
    <div class="ambient-shell">
      <div class="ambient"></div>
      <div class="grain"></div>
      <MainLayout />
    </div>
  </el-config-provider>
</template>

<style scoped>
.ambient-shell {
  position: relative;
  min-height: 100vh;
  isolation: isolate;
}

.ambient {
  position: fixed;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  background:
    radial-gradient(circle at 15% 12%, var(--ambient-blue), transparent 42%),
    radial-gradient(circle at 85% 8%, var(--ambient-orange), transparent 36%),
    linear-gradient(160deg, var(--ambient-base-start) 0%, var(--ambient-base-end) 60%, var(--app-bg) 100%);
  transition: background 0.4s var(--ease-spring);
}

.grain {
  position: fixed;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  opacity: 0.022;
  background-image:
    url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='180' height='180'><filter id='n'><feTurbulence type='fractalNoise' baseFrequency='0.9' numOctaves='2' seed='3'/><feColorMatrix values='0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1 0'/></filter><rect width='100%' height='100%' filter='url(%23n)'/></svg>");
  background-size: 180px 180px;
  mix-blend-mode: soft-light;
}

:root.dark .grain {
  opacity: 0.035;
}
</style>
