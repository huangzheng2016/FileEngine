<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { app } from './app'
import { vTip } from './tooltip'

const props = withDefaults(
  defineProps<{
    storageKey: string
    label?: string
    minMaster?: number
    minDetail?: number
    defaultPercent?: number
  }>(),
  { label: '调整列表宽度', minMaster: 320, minDetail: 360, defaultPercent: undefined },
)
const key = `${app.storagePrefix}.split.${props.storageKey}`
const el = ref<HTMLElement | null>(null)
const pct = ref(42)
let preferredPct: number | null = null
let beforeDrag: number | null = null
let pointerID: number | null = null
let dragStartX = 0
let dragStartW = 0
let observer: ResizeObserver | null = null

const container = () => el.value?.parentElement ?? null
const master = () => el.value?.previousElementSibling as HTMLElement | null
const pixels = (value: string) => Number.parseFloat(value) || 0

function geometry() {
  const parent = container()
  const divider = el.value
  if (!parent || !divider) return { width: 0, available: 0 }
  const style = getComputedStyle(parent)
  const dividerStyle = getComputedStyle(divider)
  const width = Math.max(0, parent.clientWidth - pixels(style.paddingLeft) - pixels(style.paddingRight))
  const gap = pixels(style.columnGap === 'normal' ? style.gap : style.columnGap)
  const occupied = 2 * gap + divider.offsetWidth + pixels(dividerStyle.marginLeft) + pixels(dividerStyle.marginRight)
  return { width, available: Math.max(0, width - occupied) }
}

function clamp(value: number) {
  const { width, available } = geometry()
  if (!width) return value
  const minimum = Math.max(0, props.minMaster)
  const detailMinimum = Math.max(0, props.minDetail)
  if (available < minimum + detailMinimum) {
    return ((available * minimum) / Math.max(1, minimum + detailMinimum) / width) * 100
  }
  return Math.min(((available - detailMinimum) / width) * 100, Math.max((minimum / width) * 100, value))
}

function apply(value: number) {
  pct.value = clamp(value)
  container()?.style.setProperty('--split-master-width', `${pct.value}%`)
}

function measure() {
  const { width } = geometry()
  return width ? ((master()?.offsetWidth ?? 0) / width) * 100 : pct.value
}

function renderPreference() {
  if (pointerID !== null) return
  if (preferredPct !== null) apply(preferredPct)
  else if (props.defaultPercent !== undefined) apply(props.defaultPercent)
  else {
    container()?.style.removeProperty('--split-master-width')
    const measured = measure()
    pct.value = clamp(measured)
    if (pct.value !== measured) apply(measured)
  }
}

function save(value: number) {
  apply(value)
  preferredPct = pct.value
  try {
    localStorage.setItem(key, preferredPct.toFixed(1))
  } catch {}
}

function onDown(event: PointerEvent) {
  if (event.button !== 0 || pointerID !== null) return
  beforeDrag = preferredPct
  pointerID = event.pointerId
  dragStartX = event.clientX
  dragStartW = master()?.offsetWidth ?? 0
  el.value?.setPointerCapture(event.pointerId)
  event.preventDefault()
}

function onMove(event: PointerEvent) {
  const { width } = geometry()
  if (pointerID === event.pointerId && width) apply(((dragStartW + event.clientX - dragStartX) / width) * 100)
}

function releasePointer() {
  const id = pointerID
  pointerID = null
  if (id !== null && el.value?.hasPointerCapture(id)) el.value.releasePointerCapture(id)
}

function onUp(event: PointerEvent) {
  if (pointerID !== event.pointerId) return
  releasePointer()
  save(pct.value)
}

function cancelDrag() {
  if (pointerID === null) return
  releasePointer()
  preferredPct = beforeDrag
  renderPreference()
}

function onKey(event: KeyboardEvent) {
  if (pointerID !== null) return
  const step = event.shiftKey ? 10 : 2
  if (event.key === 'ArrowLeft') save(measure() - step)
  else if (event.key === 'ArrowRight') save(measure() + step)
  else return
  event.preventDefault()
}

function reset() {
  releasePointer()
  preferredPct = null
  try {
    localStorage.removeItem(key)
  } catch {}
  renderPreference()
}

onMounted(() => {
  let saved = NaN
  try {
    saved = Number.parseFloat(localStorage.getItem(key) ?? '')
  } catch {}
  if (Number.isFinite(saved) && saved >= 0 && saved <= 100) preferredPct = saved
  renderPreference()
  if (typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(renderPreference)
    const parent = container()
    if (parent) observer.observe(parent)
    if (el.value) observer.observe(el.value)
  } else window.addEventListener('resize', renderPreference)
})
watch(() => [props.minMaster, props.minDetail, props.defaultPercent], renderPreference)
onBeforeUnmount(() => {
  observer?.disconnect()
  window.removeEventListener('resize', renderPreference)
  releasePointer()
  container()?.style.removeProperty('--split-master-width')
})
</script>

<template>
  <div
    ref="el"
    v-tip="'拖动调整宽度，双击恢复默认'"
    class="split-resizer"
    role="separator"
    aria-orientation="vertical"
    :aria-label="label"
    :aria-valuenow="Math.round(pct)"
    tabindex="0"
    @pointerdown="onDown"
    @pointermove="onMove"
    @pointerup="onUp"
    @pointercancel="cancelDrag"
    @lostpointercapture="cancelDrag"
    @dblclick="reset"
    @keydown="onKey"
  />
</template>

<style scoped>
.split-resizer {
  position: relative;
  flex: 0 0 6px;
  margin: 0 calc((var(--space-3) + 6px) / -2);
  cursor: col-resize;
  touch-action: none;
  border-radius: 3px;
}
.split-resizer::after {
  content: '';
  position: absolute;
  top: 50%;
  left: 2px;
  width: 2px;
  height: 32px;
  max-height: 50%;
  transform: translateY(-50%);
  border-radius: 1px;
  background: var(--border-strong);
}
.split-resizer:hover,
.split-resizer:focus-visible {
  background: var(--accent-soft);
  outline: none;
}
@media (max-width: 960px) {
  .split-resizer {
    display: none;
  }
}
</style>
