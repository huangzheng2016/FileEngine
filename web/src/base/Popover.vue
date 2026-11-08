<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { isTopLayer, pushLayer, removeLayer } from './dialogStack'

// The one floating panel: menus, select dropdowns, and filter panels all render
// through it, so they share position, layering, and close behavior.
// It closes on Esc (topmost layer only, focus back to the anchor), on a press
// outside both panel and anchor, and follows the anchor when the page scrolls.
const props = withDefaults(
  defineProps<{
    open: boolean
    anchor: HTMLElement | null
    // 'anchor' matches the anchor's width (comboboxes); 'min-anchor' is at least that wide
    // and grows to its content (selects in toolbars); a number fixes it (menus)
    width?: number | 'anchor' | 'min-anchor'
    align?: 'start' | 'end'
    // preferred side; flips when there is no room
    side?: 'bottom' | 'top'
  }>(),
  { width: 'anchor', align: 'start', side: 'bottom' },
)
const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement | null>(null)
const style = ref<Record<string, string>>({})
const layer = Symbol('popover')
const GAP = 4
const MARGIN = 8

function place() {
  const a = props.anchor?.getBoundingClientRect()
  if (!a) return
  const fluid = props.width === 'min-anchor'
  const width = fluid
    ? Math.max(a.width, panel.value?.offsetWidth ?? 0)
    : props.width === 'anchor'
      ? a.width
      : props.width
  const height = panel.value?.offsetHeight ?? 0
  const fitsBelow = a.bottom + GAP + height <= window.innerHeight - MARGIN
  const fitsAbove = a.top - GAP - height >= MARGIN
  const below = props.side === 'bottom' ? fitsBelow || !fitsAbove : !fitsAbove && fitsBelow
  const left = props.align === 'end' ? a.right - width : a.left
  style.value = {
    ...(fluid
      ? { minWidth: `${a.width}px`, maxWidth: `${window.innerWidth - 2 * MARGIN}px` }
      : { width: `${width}px` }),
    left: `${Math.max(MARGIN, Math.min(left, window.innerWidth - width - MARGIN))}px`,
    top: `${below ? a.bottom + GAP : a.top - GAP - height}px`,
    maxHeight: `${Math.max(120, below ? window.innerHeight - a.bottom - GAP - MARGIN : a.top - GAP - MARGIN)}px`,
  }
}

function onPointerDown(e: PointerEvent) {
  const t = e.target as Node
  if (!panel.value?.contains(t) && !props.anchor?.contains(t)) emit('close')
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && isTopLayer(layer)) {
    e.preventDefault()
    e.stopPropagation()
    emit('close')
    const a = props.anchor
    const target = a?.matches('input, button') ? a : a?.querySelector<HTMLElement>('input, button')
    target?.focus()
  }
}

function onScroll(e: Event) {
  if (!panel.value?.contains(e.target as Node)) place()
}

function attach() {
  pushLayer(layer)
  document.addEventListener('pointerdown', onPointerDown, true)
  window.addEventListener('keydown', onKeydown, true)
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', place)
  place()
  // second pass once the panel has its real height, for the flip decision
  void nextTick(place)
}

function detach() {
  removeLayer(layer)
  document.removeEventListener('pointerdown', onPointerDown, true)
  window.removeEventListener('keydown', onKeydown, true)
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', place)
}

watch(
  () => props.open,
  (open, was) => {
    if (open && !was) attach()
    else if (!open && was) detach()
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (props.open) detach()
})

defineExpose({ panel, place })
</script>

<template>
  <Teleport to="body">
    <div v-if="open" ref="panel" class="popover" :style="style" @click.stop>
      <slot />
    </div>
  </Teleport>
</template>

<style scoped>
.popover {
  position: fixed;
  z-index: var(--z-menu);
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  padding: 4px;
  background: var(--surface-raised);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-float);
}
</style>
