<script setup lang="ts">
import { ref, watch } from 'vue'
import Select from './Select.vue'

// A duration or size edited in human units and stored in base units, so settings
// never show "900 秒" or "67108864" and the API never sees "15 分钟".
//   <UnitInput v-model="form.run_timeout_seconds" kind="duration" name="run_timeout" label="运行超时" />
//   <UnitInput v-model="form.max_upload_bytes" kind="bytes" name="max_upload" label="单个附件上限" />
// The model is seconds (duration) or bytes (bytes). On load the largest unit that
// divides the value evenly is shown (900 -> 15 分钟). Switching the unit keeps the
// number and changes the value: a user who picks 小时 means "15 hours", not a conversion.
// An emptied field sets the model to 0; validate the range in the form rules.
const props = withDefaults(
  defineProps<{
    kind: 'duration' | 'bytes'
    // the number input's name, for useFormValidation
    name?: string
    // accessible name for both parts when there is no surrounding <label>
    label?: string
    disabled?: boolean
    invalid?: boolean
  }>(),
  { name: undefined, label: undefined },
)
const model = defineModel<number>({ required: true })

const UNITS = {
  duration: [
    { value: 1, label: '秒' },
    { value: 60, label: '分钟' },
    { value: 3600, label: '小时' },
    { value: 86400, label: '天' },
  ],
  bytes: [
    { value: 1024, label: 'KB' },
    { value: 1024 ** 2, label: 'MB' },
    { value: 1024 ** 3, label: 'GB' },
  ],
}
const units = UNITS[props.kind]

const unit = ref(units[0].value)
const amount = ref<number | ''>('')

function fit(v: number) {
  const best = [...units].reverse().find((u) => v !== 0 && v % u.value === 0) ?? units[units.length > 2 ? 1 : 0]
  unit.value = best.value
  amount.value = v === 0 ? '' : v / best.value
}

// only an outside change (load, reset) re-picks the unit; our own writes match already
fit(model.value)
watch(model, (v) => {
  if (v !== (amount.value === '' ? 0 : Math.round(amount.value * unit.value))) fit(v)
})

function onAmount(e: Event) {
  const raw = (e.target as HTMLInputElement).value
  amount.value = raw === '' ? '' : Number(raw)
  model.value = amount.value === '' || !Number.isFinite(amount.value) ? 0 : Math.round(amount.value * unit.value)
}

function onUnit(u: number) {
  unit.value = u
  if (amount.value !== '') model.value = Math.round(amount.value * u)
}
</script>

<template>
  <span class="unit-input">
    <input
      type="number"
      min="0"
      step="any"
      inputmode="decimal"
      :name="name"
      :value="amount"
      :aria-label="label"
      :aria-invalid="invalid || undefined"
      :disabled="disabled"
      @input="onAmount"
    />
    <Select
      :model-value="unit"
      :options="units"
      :label="label ? `${label}单位` : '单位'"
      :disabled="disabled"
      @update:model-value="onUnit"
    />
  </span>
</template>

<style scoped>
.unit-input {
  display: flex;
  gap: var(--space-2);
  min-width: 0;
}
.unit-input input {
  flex: 1;
  min-width: 0;
}
.unit-input :deep(.select) {
  flex: none;
  width: auto;
  min-width: 80px;
}
</style>
