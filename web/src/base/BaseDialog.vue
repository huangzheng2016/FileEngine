<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import Icon from './Icon.vue'
import { isTopLayer, lockScroll, pushLayer, removeLayer, unlockScroll } from './dialogStack'

const props = withDefaults(defineProps<{ open: boolean; title: string; width?: string }>(), { width: '480px' })
const emit = defineEmits<{ close: [] }>()

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

const layer = Symbol('dialog')
const titleId = useId()
const panel = ref<HTMLElement | null>(null)
const body = ref<HTMLElement | null>(null)
let returnFocus: HTMLElement | null = null
let pressedOnMask = false
const depth = ref(0)

function onKeydown(e: KeyboardEvent) {
  if (!isTopLayer(layer)) return
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('close')
  } else if (e.key === 'Tab' && panel.value) {
    const items = [...panel.value.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent)
    if (items.length === 0) return e.preventDefault()
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
}

// Close only when the press started on the mask too, so drag-selecting text
// inside the dialog and releasing outside does not discard the form.
function onMaskDown(e: PointerEvent) {
  pressedOnMask = e.target === e.currentTarget
}
function onMaskClick(e: MouseEvent) {
  if (pressedOnMask && e.target === e.currentTarget) emit('close')
}

function activate() {
  returnFocus = document.activeElement as HTMLElement | null
  depth.value = pushLayer(layer)
  lockScroll()
  window.addEventListener('keydown', onKeydown)
  void nextTick(() => {
    const target =
      panel.value?.querySelector<HTMLElement>('[autofocus]') ??
      body.value?.querySelector<HTMLElement>(FOCUSABLE) ??
      panel.value
    target?.focus()
  })
}

function deactivate() {
  removeLayer(layer)
  unlockScroll()
  window.removeEventListener('keydown', onKeydown)
  returnFocus?.focus()
  returnFocus = null
}

watch(
  () => props.open,
  (open, was) => {
    if (open && !was) activate()
    else if (!open && was) deactivate()
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (props.open) deactivate()
})
</script>

<template>
  <Teleport to="body">
    <Transition name="dialog">
      <div
        v-if="open"
        class="dialog-mask"
        :style="{ zIndex: `calc(var(--z-dialog) + ${depth})` }"
        @pointerdown="onMaskDown"
        @click="onMaskClick"
      >
        <div
          ref="panel"
          class="dialog"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="titleId"
          :style="{ width: `min(${width}, 100%)` }"
          tabindex="-1"
        >
          <header class="dialog-head">
            <h2 :id="titleId">{{ title }}</h2>
            <button type="button" class="ghost btn-icon btn-sm" aria-label="关闭" @click="emit('close')">
              <Icon name="x" />
            </button>
          </header>
          <div ref="body" class="dialog-body">
            <slot />
          </div>
          <footer v-if="$slots.footer" class="dialog-foot">
            <slot name="footer" />
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.dialog-mask {
  position: fixed;
  inset: 0;
  z-index: var(--z-dialog);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: var(--mask);
}

.dialog {
  display: flex;
  flex-direction: column;
  max-height: calc(100dvh - 32px);
  background: var(--surface-raised);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-dialog);
  outline: none;
}

.dialog-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-3) var(--space-3) var(--space-5);
}
.dialog-head h2 {
  flex: 1;
  font-size: var(--text-lg);
}

.dialog-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-1) var(--space-5) var(--space-5);
}

.dialog-foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-5);
  border-top: 1px solid var(--border);
}

.dialog-enter-active,
.dialog-leave-active {
  transition: opacity var(--dur-fast) var(--ease);
}
.dialog-enter-active .dialog,
.dialog-leave-active .dialog {
  transition: transform var(--dur) var(--ease);
}
.dialog-enter-from,
.dialog-leave-to {
  opacity: 0;
}
.dialog-enter-from .dialog,
.dialog-leave-to .dialog {
  transform: translateY(6px) scale(0.98);
}
</style>
