<script setup lang="ts">
import { computed } from 'vue'
import EmptyState from '@/base/EmptyState.vue'
import type { RunView, UnitItem } from '@/lib/transcript'
import LaneView from './LaneView.vue'

const props = defineProps<{ run: RunView }>()

const lanes = computed(() => props.run.lanes)
const multi = computed(() => lanes.value.length > 1)
const live = computed(() => !props.run.done)

function current(items: readonly { kind: string }[]): UnitItem | undefined {
  for (let i = items.length - 1; i >= 0; i--) if (items[i].kind === 'unit') return items[i] as UnitItem
  return undefined
}
</script>

<template>
  <EmptyState v-if="!lanes.length && !live" title="这次运行没有留下过程记录" icon="inbox" />
  <div v-else-if="!multi" class="lanes">
    <LaneView :items="lanes[0]?.items ?? []" :prompts="run.phase === 'chat'" :live="live" />
  </div>
  <div v-else class="lanes multi">
    <section v-for="l in lanes" :key="l.workerId" class="lane-col" :aria-label="`工作线程 ${l.workerId}`">
      <header class="lane-head text-sm">
        <span class="lane-title">线程 {{ l.workerId }}</span>
        <span class="truncate text-muted">{{ current(l.items)?.label ?? '' }}</span>
      </header>
      <LaneView :items="l.items" :live="live" />
    </section>
  </div>
</template>

<style scoped>
.lanes {
  display: flex;
  height: min(70vh, 720px);
  min-height: 320px;
  min-width: 0;
}
.lanes.multi {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: minmax(300px, 1fr);
  gap: var(--space-3);
  overflow-x: auto;
}
.lane-col {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  min-height: 0;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface);
}
.lane-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}
.lane-title {
  flex-shrink: 0;
  font-weight: 600;
}
</style>
