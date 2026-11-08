<script setup lang="ts">
import { computed } from 'vue'
import { fmtRatio } from './format'

// Progress of a task or usage of a quota. tone defaults to accent; pass 'auto' to
// turn warning above 80% and danger above 95% (quotas, disk, rate limits).
const props = withDefaults(
  defineProps<{ value: number; max?: number; tone?: 'accent' | 'success' | 'auto'; label?: string }>(),
  { max: 100, tone: 'accent', label: '' },
)

const ratio = computed(() => (props.max > 0 ? Math.min(1, Math.max(0, props.value / props.max)) : 0))
const color = computed(() => {
  if (props.tone !== 'auto') return `var(--${props.tone})`
  return ratio.value > 0.95 ? 'var(--danger)' : ratio.value > 0.8 ? 'var(--warning)' : 'var(--accent)'
})
</script>

<template>
  <div class="progress">
    <div
      class="progress-track"
      role="progressbar"
      :aria-valuenow="value"
      aria-valuemin="0"
      :aria-valuemax="max"
      :aria-label="label || undefined"
    >
      <div class="progress-fill" :style="{ width: `${ratio * 100}%`, background: color }" />
    </div>
    <span class="progress-text tabular"
      ><slot>{{ fmtRatio(ratio, 0) }}</slot></span
    >
  </div>
</template>

<style scoped>
.progress {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 120px;
}
.progress-track {
  flex: 1;
  height: 6px;
  overflow: hidden;
  background: var(--surface-muted);
  border-radius: 3px;
}
.progress-fill {
  height: 100%;
  border-radius: 3px;
  transition: width var(--dur) var(--ease);
}
.progress-text {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  white-space: nowrap;
}
</style>
