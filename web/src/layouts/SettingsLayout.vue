<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'

const route = useRoute()
const { t } = useI18n()

const items = computed(() => [
  { name: 'settings-models', label: t('nav.models'), path: '/settings/models' },
  { name: 'settings-filesystems', label: t('nav.filesystems'), path: '/settings/filesystems' },
  { name: 'settings-config', label: t('nav.config'), path: '/settings/config' },
])

const activeName = computed(() => route.name as string)
</script>

<template>
  <div class="settings-layout">
    <nav class="settings-subnav filter-tabs">
      <router-link
        v-for="item in items"
        :key="item.name"
        :to="item.path"
        class="filter-tab"
        :class="{ 'is-active': activeName === item.name }"
      >
        {{ item.label }}
      </router-link>
    </nav>
    <router-view v-slot="{ Component }">
      <transition name="page-fade" mode="out-in">
        <component :is="Component" />
      </transition>
    </router-view>
  </div>
</template>

<style scoped>
.settings-layout {
  display: flex;
  flex-direction: column;
  gap: 16px;
  flex: 1;
  min-height: 0;
}

.settings-subnav {
  align-self: flex-start;
}

.settings-subnav .filter-tab {
  text-decoration: none;
  display: inline-flex;
  align-items: center;
}
</style>
