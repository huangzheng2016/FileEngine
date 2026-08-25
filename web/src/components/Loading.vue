<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'

const props = withDefaults(defineProps<{
  visible?: boolean
  delay?: number
  inline?: boolean
  label?: string
}>(), { delay: 200, inline: false, label: '' })

const showSpinner = ref(false)
let timer: number | undefined

function clearTimer() {
  if (timer !== undefined) { window.clearTimeout(timer); timer = undefined }
}

watch(() => props.visible, (v) => {
  clearTimer()
  if (v) {
    timer = window.setTimeout(() => { showSpinner.value = true; timer = undefined }, props.delay)
  } else {
    showSpinner.value = false
  }
}, { immediate: true })

onUnmounted(() => clearTimer())

const wrapClass = computed(() => props.inline ? 'loading-inline' : 'loading-overlay')
</script>

<template>
  <teleport to="body" :disabled="inline">
    <transition name="loading-fade">
      <div v-if="showSpinner" :class="wrapClass" role="status" aria-live="polite">
        <div class="loading-spinner" aria-hidden="true"></div>
        <span v-if="label" class="loading-label">{{ label }}</span>
      </div>
    </transition>
  </teleport>
</template>

<style scoped>
.loading-overlay {
  position: fixed;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  z-index: 3000;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 20px 26px;
  background: var(--surface-strong);
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-soft), var(--inner-highlight);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  pointer-events: none;
}

.loading-inline {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-muted);
  font-size: 12px;
}

.loading-spinner {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  border: 2.5px solid var(--accent-soft);
  border-top-color: var(--accent);
  animation: loading-spin 0.8s linear infinite;
}

.loading-inline .loading-spinner {
  width: 14px;
  height: 14px;
  border-width: 2px;
}

.loading-label {
  font-size: 12px;
  color: var(--text-muted);
  font-family: var(--font-display);
}

@keyframes loading-spin { to { transform: rotate(360deg); } }

.loading-fade-enter-active,
.loading-fade-leave-active { transition: opacity 0.15s var(--ease-spring); }
.loading-fade-enter-from,
.loading-fade-leave-to { opacity: 0; }
</style>
