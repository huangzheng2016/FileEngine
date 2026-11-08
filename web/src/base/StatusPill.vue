<script setup lang="ts">
import { computed } from 'vue'
import type { LabelMap } from './labels'

const props = defineProps<{ value: string | number | null | undefined; map: LabelMap }>()

const label = computed(() => {
  const key = props.value == null ? '' : String(props.value)
  return props.map[key] ?? { text: key || '—', tone: 'neutral' }
})
</script>

<template>
  <span class="pill" :class="`tone-${label.tone}`">{{ label.text }}</span>
</template>
