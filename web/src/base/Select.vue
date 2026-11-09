<script setup lang="ts" generic="V extends string | number">
import { computed, nextTick, ref, useId, watch } from 'vue'
import Icon from './Icon.vue'
import Popover from './Popover.vue'
import { vTip } from './tooltip'

export interface SelectOption<T extends string | number = string> {
  value: T
  label: string
  hint?: string
  // a disabled option must say why
  disabledReason?: string
}

// One value from a short fixed list (status, type, page size). The list looks and behaves
// like Combobox and the menus: same panel, hover, keyboard, light/dark. Use Segmented when
// 2 to 5 options should stay visible, Combobox when the list is long, remote, or multiple.
//   <Select v-model="list.filters.kind" :options="[{ value: '', label: '全部类型' }, ...labelOptions(KIND)]" label="类型" />
// Keyboard on the closed button: ↑ ↓ Enter Space open, a letter jumps to the next match.
// Open: ↑ ↓ Home End move, Enter Space choose, Esc / Tab close.
// Hover follows pointer movement only, so a list opening under a resting mouse keeps the
// keyboard highlight.
const props = withDefaults(
  defineProps<{
    options: readonly SelectOption<V>[]
    // accessible name when there is no visible <label> around it (toolbars)
    label?: string
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
    size?: 'md' | 'sm'
  }>(),
  { label: undefined, placeholder: '请选择', size: 'md' },
)
const model = defineModel<V>({ required: true })

const id = useId()
const trigger = ref<HTMLButtonElement | null>(null)
const list = ref<HTMLElement | null>(null)
const open = ref(false)
const active = ref(-1)

const current = computed(() => props.options.find((o) => o.value === model.value))
// An unknown value stays visible instead of looking unset.
// A value with no matching option shows the placeholder, never the raw value.
const shownLabel = computed(() => current.value?.label ?? '')

const enabled = (i: number) => !!props.options[i] && !props.options[i].disabledReason

function step(from: number, dir: 1 | -1) {
  const n = props.options.length
  for (let k = 1; k <= n; k++) {
    const i = (from + dir * k + n * k) % n
    if (enabled(i)) return i
  }
  return from
}

function show() {
  if (props.disabled || open.value) return
  const i = props.options.findIndex((o) => o.value === model.value)
  active.value = enabled(i) ? i : step(-1, 1)
  open.value = true
}

function hide(refocus = true) {
  open.value = false
  if (refocus) trigger.value?.focus()
}

function choose(i: number) {
  if (!enabled(i)) return
  model.value = props.options[i].value
  hide()
}

let typed = ''
let typedAt = 0
function typeahead(key: string) {
  const now = Date.now()
  typed = now - typedAt > 600 ? key : typed + key
  typedAt = now
  const n = props.options.length
  const start = open.value ? active.value : props.options.findIndex((o) => o.value === model.value)
  // a new letter moves past the current match; a longer prefix may stay on it
  const offset = typed.length === 1 ? 1 : 0
  for (let k = 0; k < n; k++) {
    const i = (start + offset + k + n) % n
    if (enabled(i) && props.options[i].label.toLowerCase().startsWith(typed.toLowerCase())) {
      if (open.value) active.value = i
      else model.value = props.options[i].value
      return
    }
  }
}

function onKeydown(e: KeyboardEvent) {
  const k = e.key
  if (!open.value) {
    if (k === 'ArrowDown' || k === 'ArrowUp' || k === 'Enter' || k === ' ') {
      e.preventDefault()
      show()
    } else if (k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      typeahead(k)
    }
    return
  }
  if (k === 'ArrowDown' || k === 'ArrowUp') {
    e.preventDefault()
    active.value = step(active.value, k === 'ArrowDown' ? 1 : -1)
  } else if (k === 'Home' || k === 'End') {
    e.preventDefault()
    active.value = step(k === 'Home' ? -1 : props.options.length, k === 'Home' ? 1 : -1)
  } else if (k === 'Enter' || k === ' ') {
    e.preventDefault()
    choose(active.value)
  } else if (k === 'Tab') {
    hide(false)
  } else if (k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
    typeahead(k)
  }
}

watch([active, open], async () => {
  if (!open.value) return
  await nextTick()
  list.value
    ?.querySelector<HTMLElement>(`#${CSS.escape(`${id}-${active.value}`)}`)
    ?.scrollIntoView?.({ block: 'nearest' })
})
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="select"
    :class="[`select-${size}`, { open, invalid }]"
    role="combobox"
    aria-haspopup="listbox"
    :aria-label="label"
    :aria-expanded="open"
    :aria-controls="`${id}-list`"
    :aria-activedescendant="open && active >= 0 ? `${id}-${active}` : undefined"
    :aria-invalid="invalid || undefined"
    :disabled="disabled"
    @click="open ? hide() : show()"
    @keydown="onKeydown"
  >
    <span class="select-value" :class="{ 'select-placeholder': !shownLabel }">{{ shownLabel || placeholder }}</span>
    <Icon name="chevron-down" :size="14" class="select-caret" />
  </button>
  <Popover :open="open" :anchor="trigger" width="min-anchor" @close="hide(false)">
    <ul :id="`${id}-list`" ref="list" role="listbox" class="select-list" :aria-label="label">
      <li
        v-for="(o, i) in options"
        :id="`${id}-${i}`"
        :key="String(o.value)"
        v-tip="o.disabledReason"
        role="option"
        class="select-option"
        :class="{ active: i === active }"
        :aria-selected="o.value === model"
        :aria-disabled="!!o.disabledReason || undefined"
        @pointerdown.prevent
        @pointermove="enabled(i) && (active = i)"
        @click="choose(i)"
      >
        <span class="select-label">{{ o.label }}</span>
        <span v-if="o.hint" class="select-hint">{{ o.hint }}</span>
        <Icon v-if="o.value === model" name="check" :size="14" />
      </li>
    </ul>
  </Popover>
</template>

<style scoped>
.select {
  justify-content: space-between;
  gap: var(--space-2);
  min-width: 0;
  padding: 0 8px 0 10px;
  font-weight: 400;
  text-align: left;
  border-color: var(--border-strong);
  transition:
    border-color var(--dur-fast) var(--ease),
    box-shadow var(--dur-fast) var(--ease);
}
.select:hover:not(:disabled, .open) {
  background: var(--surface-raised);
  border-color: var(--text-muted);
}
.select:focus-visible,
.select.open {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.select.invalid {
  border-color: var(--danger);
}
.select:disabled {
  opacity: 1;
  color: var(--text-muted);
  background: var(--surface-muted);
}
.select-sm {
  height: var(--control-h-sm);
  padding: 0 6px 0 8px;
  font-size: var(--text-sm);
}
/* No width of its own: a form field's column stretches it, a toolbar row sizes it
   to its label. */
.select-value {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.select-placeholder {
  color: var(--text-muted);
}
.select-caret {
  flex-shrink: 0;
  color: var(--text-muted);
  transition: transform var(--dur-fast) var(--ease);
}
.select.open .select-caret {
  transform: rotate(180deg);
}
.select-list {
  margin: 0;
  padding: 0;
  list-style: none;
}
.select-option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 30px;
  padding: 0 8px;
  font-size: var(--text-md);
  white-space: nowrap;
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.select-option.active {
  background: var(--surface-hover);
}
.select-option[aria-selected='true'] {
  color: var(--accent-text);
  font-weight: 500;
}
.select-option[aria-disabled='true'] {
  color: var(--text-muted);
  cursor: not-allowed;
}
.select-label {
  flex: 1;
}
.select-hint {
  font-size: var(--text-sm);
  color: var(--text-muted);
}
</style>
