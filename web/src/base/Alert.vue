<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'
import type { IconName } from './icons'

// A persistent message about the page or object the user is looking at
// ("该示例已停用", "只读模式", quota warnings). Results of actions go to toast instead.
const props = withDefaults(defineProps<{ tone?: 'info' | 'success' | 'warning' | 'danger'; title?: string }>(), {
  tone: 'info',
  title: '',
})

const ICONS: Record<string, IconName> = {
  info: 'info',
  success: 'check-circle',
  warning: 'alert-triangle',
  danger: 'x-circle',
}
const icon = computed(() => ICONS[props.tone])
</script>

<template>
  <div class="alert" :class="`alert-${tone}`" :role="tone === 'danger' || tone === 'warning' ? 'alert' : 'status'">
    <Icon :name="icon" class="alert-icon" />
    <div class="alert-body">
      <strong v-if="title" class="alert-title">{{ title }}</strong>
      <slot />
    </div>
    <div v-if="$slots.action" class="alert-action">
      <slot name="action" />
    </div>
  </div>
</template>

<style scoped>
.alert {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: 10px 12px;
  font-size: var(--text-md);
  border-radius: var(--radius-md);
  border: 1px solid transparent;
}
.alert-icon {
  margin-top: 2px;
}
.alert-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  color: var(--text);
}
.alert-title {
  font-weight: 600;
}
.alert-action {
  display: flex;
  gap: var(--space-2);
  margin: -3px 0;
}
.alert-info {
  background: var(--info-soft);
  border-color: color-mix(in srgb, var(--info) 25%, transparent);
}
.alert-info .alert-icon {
  color: var(--info);
}
.alert-success {
  background: var(--success-soft);
  border-color: color-mix(in srgb, var(--success) 25%, transparent);
}
.alert-success .alert-icon {
  color: var(--success);
}
.alert-warning {
  background: var(--warning-soft);
  border-color: color-mix(in srgb, var(--warning) 30%, transparent);
}
.alert-warning .alert-icon {
  color: var(--warning);
}
.alert-danger {
  background: var(--danger-soft);
  border-color: color-mix(in srgb, var(--danger) 25%, transparent);
}
.alert-danger .alert-icon {
  color: var(--danger);
}
</style>
