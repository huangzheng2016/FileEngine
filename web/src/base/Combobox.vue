<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import Icon from './Icon.vue'
import Popover from './Popover.vue'
import { errorMessage, isAbort } from './errors'

export interface ComboOption {
  value: string
  label: string
  hint?: string
}

// Searchable select. Use it instead of Select when the options are many, come
// from the server, or several can be chosen. The model holds whole options, so a
// saved value shows its label without waiting for a search.
//   <Combobox v-model="owner" :load="searchUsers" placeholder="搜索用户" />
//   <Combobox v-model="tags" multiple :options="TAGS" />
const props = withDefaults(
  defineProps<{
    options?: ComboOption[]
    // server search; called with '' on open, then debounced while typing
    load?: (query: string, signal: AbortSignal) => Promise<ComboOption[]>
    multiple?: boolean
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
  }>(),
  { options: () => [], load: undefined, placeholder: '请选择' },
)
const model = defineModel<ComboOption | null | ComboOption[]>({ required: true })

const id = useId()
const anchor = ref<HTMLElement | null>(null)
const input = ref<HTMLInputElement | null>(null)
const open = ref(false)
const query = ref('')
const active = ref(0)
const remote = ref<ComboOption[]>([])
const loading = ref(false)
const loadError = ref('')

const selected = computed<ComboOption[]>(() =>
  Array.isArray(model.value) ? model.value : model.value ? [model.value] : [],
)
const isSelected = (o: ComboOption) => selected.value.some((s) => s.value === o.value)

const shown = computed(() => {
  if (props.load) return remote.value
  const q = query.value.trim().toLowerCase()
  return q ? props.options.filter((o) => o.label.toLowerCase().includes(q)) : props.options
})

let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | null = null

async function fetchOptions() {
  if (!props.load) return
  controller?.abort()
  const ctrl = new AbortController()
  controller = ctrl
  loading.value = true
  loadError.value = ''
  try {
    const result = await props.load(query.value.trim(), ctrl.signal)
    if (controller === ctrl) remote.value = result
  } catch (e) {
    if (controller === ctrl && !isAbort(e)) loadError.value = errorMessage(e)
  } finally {
    if (controller === ctrl) loading.value = false
  }
}

watch(query, () => {
  active.value = 0
  if (!open.value) return
  clearTimeout(timer)
  timer = setTimeout(fetchOptions, 250)
})

function show() {
  if (open.value || props.disabled) return
  open.value = true
  active.value = Math.max(0, shown.value.findIndex(isSelected))
  void fetchOptions()
}

const list = ref<HTMLElement | null>(null)
watch([active, open], async () => {
  if (!open.value) return
  await nextTick()
  list.value?.querySelector<HTMLElement>('.combo-option.active')?.scrollIntoView?.({ block: 'nearest' })
})

function onInput(e: Event) {
  const value = (e.target as HTMLInputElement).value
  // Typing into a closed single select starts a new search instead of editing the shown label.
  query.value = open.value || props.multiple ? value : ((e as InputEvent).data ?? '')
  show()
}

function hide() {
  open.value = false
  query.value = ''
  clearTimeout(timer)
  controller?.abort()
}

function choose(o: ComboOption) {
  if (props.multiple) {
    model.value = isSelected(o) ? selected.value.filter((s) => s.value !== o.value) : [...selected.value, o]
    query.value = ''
    input.value?.focus()
  } else {
    model.value = o
    hide()
  }
}

function remove(o: ComboOption) {
  model.value = props.multiple ? selected.value.filter((s) => s.value !== o.value) : null
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    if (!open.value) return show()
    const n = shown.value.length
    if (n) active.value = (active.value + (e.key === 'ArrowDown' ? 1 : n - 1)) % n
  } else if (e.key === 'Enter' && open.value) {
    e.preventDefault()
    const o = shown.value[active.value]
    if (o) choose(o)
  } else if (e.key === 'Backspace' && !query.value && props.multiple && selected.value.length) {
    remove(selected.value[selected.value.length - 1])
  } else if (e.key === 'Tab') {
    hide()
  }
}

// Single mode shows the chosen label in the input while closed, the search text while open.
const inputValue = computed(() => (open.value || props.multiple ? query.value : (selected.value[0]?.label ?? '')))
</script>

<template>
  <div ref="anchor" class="combo" :class="{ open, disabled, invalid }" @click="disabled || (show(), input?.focus())">
    <span v-for="o in multiple ? selected : []" :key="o.value" class="combo-chip">
      {{ o.label }}
      <button
        type="button"
        class="combo-x"
        :aria-label="`移除 ${o.label}`"
        :disabled="disabled"
        @click.stop="remove(o)"
      >
        <Icon name="x" :size="12" />
      </button>
    </span>
    <input
      ref="input"
      role="combobox"
      autocomplete="off"
      :value="inputValue"
      :placeholder="multiple && selected.length ? '' : selected.length ? selected[0].label : placeholder"
      :disabled="disabled"
      :aria-expanded="open"
      :aria-controls="`${id}-list`"
      :aria-activedescendant="open && shown[active] ? `${id}-${active}` : undefined"
      :aria-invalid="invalid || undefined"
      @input="onInput"
      @keydown="onKeydown"
    />
    <button
      v-if="!multiple && selected.length && !disabled"
      type="button"
      class="combo-x"
      aria-label="清除"
      @click.stop="remove(selected[0])"
    >
      <Icon name="x" :size="14" />
    </button>
    <Icon v-else name="chevron-down" :size="14" class="combo-caret" />
  </div>
  <Popover :open="open" :anchor="anchor" @close="hide">
    <ul :id="`${id}-list`" ref="list" role="listbox" :aria-multiselectable="multiple || undefined" class="combo-list">
      <li
        v-for="(o, i) in shown"
        :id="`${id}-${i}`"
        :key="o.value"
        role="option"
        class="combo-option"
        :class="{ active: i === active }"
        :aria-selected="isSelected(o)"
        @pointerdown.prevent
        @pointermove="active = i"
        @click="choose(o)"
      >
        <span class="combo-label">{{ o.label }}</span>
        <span v-if="o.hint" class="combo-hint">{{ o.hint }}</span>
        <Icon v-if="isSelected(o)" name="check" :size="14" />
      </li>
      <li v-if="loading && shown.length === 0" class="combo-note">搜索中</li>
      <li v-else-if="loadError" class="combo-note text-danger">加载失败：{{ loadError }}</li>
      <li v-else-if="shown.length === 0" class="combo-note">无匹配项</li>
    </ul>
  </Popover>
</template>

<style scoped>
.combo {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  width: 100%;
  min-height: var(--control-h);
  padding: 2px 8px 2px 4px;
  background: var(--surface-raised);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  cursor: text;
  transition:
    border-color var(--dur-fast) var(--ease),
    box-shadow var(--dur-fast) var(--ease);
}
.combo:hover:not(.disabled) {
  border-color: var(--text-muted);
}
.combo.open,
.combo:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.combo.invalid {
  border-color: var(--danger);
}
.combo.disabled {
  background: var(--surface-muted);
  cursor: not-allowed;
}
.combo input {
  flex: 1;
  min-width: 60px;
  height: calc(var(--control-h) - 6px);
  padding: 0 6px;
  border: 0;
  background: transparent;
  box-shadow: none;
}
.combo input:focus {
  box-shadow: none;
}
.combo-chip {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  height: 22px;
  padding: 0 2px 0 8px;
  font-size: var(--text-sm);
  background: var(--surface-muted);
  border-radius: var(--radius-sm);
}
.combo-x {
  width: 20px;
  height: 20px;
  padding: 0;
  color: var(--text-muted);
  background: transparent;
  border: 0;
}
.combo-x:hover {
  color: var(--text);
  background: var(--surface-hover);
}
.combo-caret {
  color: var(--text-muted);
}
.combo-list {
  margin: 0;
  padding: 0;
  list-style: none;
}
.combo-option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 30px;
  padding: 0 8px;
  font-size: var(--text-md);
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.combo-option.active {
  background: var(--surface-hover);
}
.combo-option[aria-selected='true'] {
  color: var(--accent-text);
  font-weight: 500;
}
.combo-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.combo-hint {
  font-size: var(--text-sm);
  color: var(--text-muted);
}
.combo-note {
  padding: 6px 8px;
  font-size: var(--text-md);
  color: var(--text-muted);
}
</style>
