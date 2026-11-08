<script setup lang="ts">
import Icon from './Icon.vue'

// Progress of a useWizard flow. Visited steps are clickable; later ones are not.
defineProps<{ steps: readonly { key: string; label: string }[]; index: number; reached: number; locked?: boolean }>()
defineEmits<{ go: [number] }>()
</script>

<template>
  <ol class="steps" aria-label="步骤">
    <li
      v-for="(s, i) in steps"
      :key="s.key"
      class="step"
      :class="{ done: i < index, current: i === index }"
      :aria-current="i === index ? 'step' : undefined"
    >
      <button type="button" class="step-btn" :disabled="locked || i > reached || i === index" @click="$emit('go', i)">
        <span class="step-dot">
          <Icon v-if="i < index" name="check" :size="12" />
          <template v-else>{{ i + 1 }}</template>
        </span>
        <span class="step-label">{{ s.label }}</span>
      </button>
    </li>
  </ol>
</template>

<style scoped>
.steps {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.step {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
}
.step:not(:last-child)::after {
  content: '';
  flex: 1;
  height: 1px;
  min-width: 16px;
  background: var(--border-strong);
}
.step.done:not(:last-child)::after {
  background: var(--accent);
}
.step-btn {
  height: auto;
  gap: var(--space-2);
  padding: 0;
  color: var(--text-muted);
  background: transparent;
  border: 0;
}
.step-btn:disabled {
  opacity: 1;
}
.step-btn:not(:disabled):hover .step-label {
  color: var(--text);
}
.step-dot {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  font-size: var(--text-sm);
  font-weight: 600;
  border: 1px solid var(--border-strong);
  border-radius: 50%;
}
.step.current .step-dot,
.step.done .step-dot {
  color: var(--on-accent);
  background: var(--accent);
  border-color: var(--accent);
}
.step.current .step-label {
  color: var(--text);
  font-weight: 600;
}
.step-label {
  white-space: nowrap;
}
</style>
