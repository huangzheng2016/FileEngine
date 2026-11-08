<script setup lang="ts">
import CodeEditor from './CodeEditor.vue'
import { jsonObjectError } from './highlight'

// JSON input. Validation timing belongs to the form: use jsonObjectError in the
// useFormValidation rules and pass the shown error as `invalid`.
withDefaults(defineProps<{ rows?: number; invalid?: boolean; name?: string }>(), {
  rows: 6,
  invalid: false,
  name: undefined,
})
const model = defineModel<string>({ required: true })

function format() {
  if (jsonObjectError(model.value) || !model.value.trim()) return
  model.value = JSON.stringify(JSON.parse(model.value), null, 2)
}
</script>

<template>
  <div class="json-field">
    <CodeEditor v-model="model" lang="json" :rows="rows" :invalid="invalid" :name="name" placeholder="{ }" />
    <button type="button" class="ghost btn-sm" :disabled="!model.trim()" @click="format">格式化</button>
  </div>
</template>

<style scoped>
.json-field {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
}
.json-field > :first-child {
  align-self: stretch;
}
</style>
