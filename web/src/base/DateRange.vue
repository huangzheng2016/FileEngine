<script setup lang="ts">
import { ref, watch } from 'vue'
import Select from './Select.vue'
import { RANGE_PRESETS } from './time'

// Time filter for toolbars. The model is the URL-friendly range string described
// in time.ts; convert it with rangeBounds() when calling the API.
const model = defineModel<string>({ required: true })

const CUSTOM = 'custom'
const MODES = [
  { value: '', label: '不限时间' },
  ...RANGE_PRESETS.map((p) => ({ value: p.value as string, label: p.label })),
  { value: CUSTOM, label: '自定义' },
]
const mode = ref('')
const from = ref('')
const to = ref('')

watch(
  model,
  (v) => {
    const m = /^(\d{4}-\d{2}-\d{2})~(\d{4}-\d{2}-\d{2})$/.exec(v)
    if (m) {
      mode.value = CUSTOM
      from.value = m[1]
      to.value = m[2]
    } else if (mode.value !== CUSTOM || v) {
      mode.value = v
    }
  },
  { immediate: true },
)

function onMode(value: string) {
  mode.value = value
  if (value !== CUSTOM) model.value = value
}

// Emit only a complete, ordered range; a half-filled custom range keeps the old filter.
function onDate() {
  if (from.value && to.value)
    model.value = from.value <= to.value ? `${from.value}~${to.value}` : `${to.value}~${from.value}`
}
</script>

<template>
  <span class="date-range">
    <Select :model-value="mode" :options="MODES" label="时间范围" @update:model-value="onMode" />
    <template v-if="mode === CUSTOM">
      <input v-model="from" type="date" aria-label="开始日期" @change="onDate" />
      <span class="text-muted">至</span>
      <input v-model="to" type="date" aria-label="结束日期" @change="onDate" />
    </template>
  </span>
</template>

<style scoped>
.date-range {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}
.date-range input {
  width: auto;
}
</style>
