<script setup lang="ts" generic="V extends string">
// 2 to 5 mutually exclusive options that should all stay visible: a status
// filter, a view switch, a time granularity. More options: use Select.
const props = defineProps<{ options: readonly { value: V; label: string }[]; label?: string }>()
const model = defineModel<V>({ required: true })

function onKeydown(e: KeyboardEvent) {
  const step =
    e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
  if (!step) return
  e.preventDefault()
  const n = props.options.length
  const i = (props.options.findIndex((o) => o.value === model.value) + step + n) % n
  model.value = props.options[i].value
  ;(e.currentTarget as HTMLElement).querySelectorAll<HTMLElement>('[role="radio"]')[i]?.focus()
}
</script>

<template>
  <div class="segmented" role="radiogroup" :aria-label="label" @keydown="onKeydown">
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="radio"
      :aria-checked="o.value === model"
      :tabindex="o.value === model ? 0 : -1"
      @click="model = o.value"
    >
      {{ o.label }}
    </button>
  </div>
</template>

<style scoped>
.segmented {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--surface-muted);
  border-radius: var(--radius-sm);
}
.segmented button {
  height: calc(var(--control-h) - 4px);
  padding: 0 10px;
  color: var(--text-secondary);
  background: transparent;
  border: 0;
  border-radius: calc(var(--radius-sm) - 1px);
}
.segmented button:hover {
  color: var(--text);
  background: transparent;
}
.segmented button[aria-checked='true'] {
  color: var(--text);
  font-weight: 600;
  background: var(--surface-raised);
  box-shadow: 0 0 0 1px var(--border);
}
</style>
