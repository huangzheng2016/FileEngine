<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import Popover from './Popover.vue'

// Extra detail on hover: a metric's definition, a user's profile, a truncated
// value's full text with context. One-line hints use v-tip instead. Opens after
// a short hover, at once on keyboard focus, on tap for touch; stays open while
// the pointer is over the card. Never put the only copy of important
// information here: it must also be reachable without hovering.
withDefaults(defineProps<{ width?: number; side?: 'top' | 'bottom' }>(), { width: 280, side: 'top' })

const OPEN_DELAY = 300
const CLOSE_DELAY = 200
const anchor = ref<HTMLElement | null>(null)
const open = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let lastPointer = 'mouse'

function later(next: boolean, ms: number) {
  clearTimeout(timer)
  timer = setTimeout(() => (open.value = next), ms)
}

function onPointerEnter(e: PointerEvent) {
  lastPointer = e.pointerType
  if (e.pointerType === 'mouse') later(true, OPEN_DELAY)
}

// mouse users already opened it by hovering; a click would only close it again
function onClick() {
  if (lastPointer !== 'mouse') open.value = !open.value
}
function onPointerLeave(e: PointerEvent) {
  if (e.pointerType === 'mouse') later(false, CLOSE_DELAY)
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <span
    ref="anchor"
    class="hover-anchor"
    @pointerenter="onPointerEnter"
    @pointerleave="onPointerLeave"
    @focusin="later(true, 0)"
    @focusout="later(false, CLOSE_DELAY)"
    @pointerdown="lastPointer = $event.pointerType"
    @click="onClick"
  >
    <slot />
  </span>
  <Popover :open="open" :anchor="anchor" :width="width" :side="side" @close="open = false">
    <div class="hover-card" role="tooltip" @pointerenter="later(true, 0)" @pointerleave="onPointerLeave">
      <slot name="content" />
    </div>
  </Popover>
</template>

<style scoped>
.hover-anchor {
  display: inline-flex;
  align-items: center;
}
.hover-card {
  padding: 6px 8px;
  font-size: var(--text-md);
  line-height: 1.6;
  color: var(--text-secondary);
}
</style>
