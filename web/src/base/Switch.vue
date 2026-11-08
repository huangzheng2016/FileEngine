<script setup lang="ts">
// The only on/off control. For a row toggle that saves immediately, pass :loading
// while the request runs so it cannot be double-clicked.
defineProps<{ modelValue: boolean; disabled?: boolean; loading?: boolean; label?: string }>()
defineEmits<{ 'update:modelValue': [boolean] }>()
</script>

<template>
  <button
    type="button"
    role="switch"
    class="switch"
    :class="{ on: modelValue, busy: loading }"
    :aria-checked="modelValue"
    :aria-label="label"
    :disabled="disabled || loading"
    @click.stop="$emit('update:modelValue', !modelValue)"
  >
    <span class="knob" />
  </button>
</template>

<style scoped>
.switch {
  position: relative;
  width: 32px;
  height: 18px;
  padding: 0;
  border: 0;
  border-radius: 9px;
  background: var(--border-strong);
  vertical-align: middle;
}
.switch:hover {
  background: var(--text-muted);
}
.switch.on {
  background: var(--accent);
}
.switch.busy {
  opacity: 0.6;
}
.knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #fff;
  transition: transform var(--dur-fast) var(--ease);
}
.switch.on .knob {
  transform: translateX(14px);
}
</style>
