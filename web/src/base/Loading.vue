<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'

// Shows after `delay` ms so fast requests never flash a spinner.
// inline: next to a toolbar button. Default: centered in its container.
const props = withDefaults(defineProps<{ visible?: boolean; inline?: boolean; label?: string; delay?: number }>(), {
  visible: true,
  inline: false,
  label: '加载中',
  delay: 200,
})

const shown = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

watch(
  () => props.visible,
  (v) => {
    clearTimeout(timer)
    if (v) timer = setTimeout(() => (shown.value = true), props.delay)
    else shown.value = false
  },
  { immediate: true },
)

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <span v-if="shown" :class="inline ? 'loading-inline' : 'loading-block'" role="status">
    <span class="spinner" aria-hidden="true" />
    <span v-if="label">{{ label }}</span>
  </span>
</template>

<style scoped>
.loading-inline,
.loading-block {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-sm);
  color: var(--text-muted);
}

.loading-block {
  display: flex;
  justify-content: center;
  padding: var(--space-6);
}

.spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--accent-soft);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
