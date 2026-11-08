<script setup lang="ts" generic="K extends string">
import { useId } from 'vue'

// <Tabs v-model="tab" :items="[{ key: 'base', label: '基本信息' }, { key: 'logs', label: '日志' }]">
//   <template #base>...</template>
//   <template #logs>...</template>
// </Tabs>
// Keep the active tab in the URL: bind v-model to a useRouteQuery key.
const props = defineProps<{ items: readonly { key: K; label: string }[] }>()
const active = defineModel<K>({ required: true })
const id = useId()

function onKeydown(e: KeyboardEvent) {
  const i = props.items.findIndex((t) => t.key === active.value)
  const last = props.items.length - 1
  let next: number
  if (e.key === 'ArrowRight') next = i === last ? 0 : i + 1
  else if (e.key === 'ArrowLeft') next = i === 0 ? last : i - 1
  else if (e.key === 'Home') next = 0
  else if (e.key === 'End') next = last
  else return
  e.preventDefault()
  active.value = props.items[next].key
  const tabs = (e.currentTarget as HTMLElement).querySelectorAll<HTMLElement>('[role="tab"]')
  tabs[next]?.focus()
}
</script>

<template>
  <div class="tabs">
    <div class="tab-list" role="tablist" @keydown="onKeydown">
      <button
        v-for="t in items"
        :id="`${id}-tab-${t.key}`"
        :key="t.key"
        type="button"
        role="tab"
        class="tab"
        :class="{ active: t.key === active }"
        :aria-selected="t.key === active"
        :aria-controls="`${id}-panel`"
        :tabindex="t.key === active ? 0 : -1"
        @click="active = t.key"
      >
        {{ t.label }}
      </button>
    </div>
    <div :id="`${id}-panel`" role="tabpanel" class="tab-panel" :aria-labelledby="`${id}-tab-${active}`">
      <slot :name="active" />
    </div>
  </div>
</template>

<style scoped>
.tabs {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-height: 0;
  min-width: 0;
}
.tab-list {
  display: flex;
  gap: var(--space-1);
  border-bottom: 1px solid var(--border);
  min-width: 0;
  flex-shrink: 0;
  overflow-x: auto;
  overflow-y: hidden;
}
.tab {
  flex-shrink: 0;
  white-space: nowrap;
  height: 34px;
  margin-bottom: -1px;
  padding: 0 12px;
  color: var(--text-secondary);
  background: transparent;
  border: 0;
  border-bottom: 2px solid transparent;
  border-radius: 0;
}
.tab:hover {
  color: var(--text);
  background: transparent;
}
.tab.active {
  color: var(--text);
  font-weight: 600;
  border-bottom-color: var(--accent);
}
.tab-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-height: 0;
  min-width: 0;
}
</style>
