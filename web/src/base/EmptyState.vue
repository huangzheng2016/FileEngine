<script setup lang="ts">
import Icon from './Icon.vue'
import type { IconName } from './icons'

withDefaults(defineProps<{ title: string; hint?: string; icon?: IconName }>(), {
  hint: '',
  icon: 'inbox',
})
</script>

<template>
  <div class="empty">
    <Icon :name="icon" :size="28" class="empty-icon" />
    <div class="empty-title">{{ title }}</div>
    <div v-if="hint" class="empty-hint">{{ hint }}</div>
    <div v-if="$slots.default" class="empty-action">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  text-align: center;
}
.empty-icon {
  color: var(--text-muted);
  opacity: 0.6;
}
.empty-title {
  font-weight: 500;
  color: var(--text-secondary);
}
.empty-hint {
  font-size: var(--text-sm);
  color: var(--text-muted);
}
.empty-action {
  margin-top: var(--space-2);
}
</style>
