<script setup lang="ts">
import Icon from '@/base/Icon.vue'
import { vTip } from '@/base/tooltip'

// A path typed by hand or picked with 浏览. The caller owns the picker.
defineProps<{
  name: string
  placeholder?: string
  invalid?: boolean
  // why 浏览 is unavailable; empty when it is
  browseBlocked?: string
}>()
defineEmits<{ browse: [] }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div class="path-row">
    <input
      v-model="model"
      :name="name"
      class="mono"
      autocomplete="off"
      autocapitalize="off"
      spellcheck="false"
      :placeholder="placeholder"
      :aria-invalid="invalid || undefined"
    />
    <button v-tip="browseBlocked || ''" type="button" :disabled="!!browseBlocked" @click="$emit('browse')">
      <Icon name="folder" :size="14" />浏览
    </button>
  </div>
</template>

<style scoped>
.path-row {
  display: flex;
  gap: var(--space-2);
}
.path-row input {
  flex: 1;
  min-width: 0;
}
</style>
