<script setup lang="ts" generic="V extends string">
import { useId } from 'vue'

// One of a few options that need a title and a line of description each (a provider, a connection type).
// Choosing only selects; the surrounding flow moves on with its own primary button.
defineProps<{
  options: readonly { value: V; title: string; desc?: string }[]
  label: string
}>()
const model = defineModel<V>({ required: true })
const name = useId()
</script>

<template>
  <div class="choice-grid" role="radiogroup" :aria-label="label">
    <label v-for="o in options" :key="o.value" class="card choice-card">
      <input v-model="model" type="radio" :name="name" :value="o.value" />
      <span class="choice-text">
        <strong>{{ o.title }}</strong>
        <span v-if="o.desc" class="card-desc">{{ o.desc }}</span>
      </span>
    </label>
  </div>
</template>

<style scoped>
.choice-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: var(--space-2);
}
.choice-card {
  flex-direction: row;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-3);
  cursor: pointer;
}
.choice-card:hover {
  background: var(--surface-hover);
}
.choice-card:has(input:checked) {
  background: var(--accent-soft);
  border-color: var(--accent);
}
.choice-card input {
  flex: none;
  margin-top: 3px;
}
.choice-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
</style>
