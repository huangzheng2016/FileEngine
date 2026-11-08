<script setup lang="ts" generic="T">
import EmptyState from './EmptyState.vue'
import type { Tone } from './labels'
import { fmtRelative, fmtTime } from './time'
import { vTip } from './tooltip'

// What happened to one object, newest first: its audit trail, a ticket's events,
// a job's attempts. Each entry is one line (what, who, when) with optional detail
// below from the default slot. Use a table instead when people compare entries
// across columns or filter them; use this when they read a story.
//   <Timeline :items="entries" empty-text="暂无记录">
//     <template #default="{ item }"><DiffView v-if="item.diff" ... /></template>
//   </Timeline>
export interface TimelineItem<D = unknown> {
  key: string
  // unix seconds
  time: number
  title: string
  // who did it; empty renders nothing
  actor?: string
  tone?: Tone
  data?: D
}

defineProps<{ items: TimelineItem<T>[]; emptyText?: string }>()
defineSlots<{ default?: (props: { item: TimelineItem<T> }) => unknown }>()
</script>

<template>
  <EmptyState v-if="items.length === 0" icon="history" :title="emptyText ?? '暂无记录'" />
  <ol v-else class="timeline">
    <li v-for="item in items" :key="item.key" class="timeline-item" :class="`tone-${item.tone ?? 'neutral'}`">
      <span class="timeline-dot" aria-hidden="true" />
      <div class="timeline-head">
        <span class="timeline-title">{{ item.title }}</span>
        <span v-if="item.actor" class="text-muted">{{ item.actor }}</span>
        <span v-tip="fmtTime(item.time)" class="timeline-time text-muted tabular">{{ fmtRelative(item.time) }}</span>
      </div>
      <div v-if="$slots.default" class="timeline-body">
        <slot :item="item" />
      </div>
    </li>
  </ol>
</template>

<style scoped>
.timeline {
  margin: 0;
  padding: 0;
  list-style: none;
}
.timeline-item {
  position: relative;
  padding: 0 0 var(--space-4) var(--space-5);
}
/* the line runs from each dot down to the next one */
.timeline-item:not(:last-child)::before {
  content: '';
  position: absolute;
  top: 14px;
  bottom: 0;
  left: 4px;
  width: 1px;
  background: var(--border-strong);
}
.timeline-dot {
  position: absolute;
  top: 6px;
  left: 0;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--text-muted);
}
.tone-accent .timeline-dot {
  background: var(--accent);
}
.tone-success .timeline-dot {
  background: var(--success);
}
.tone-warning .timeline-dot {
  background: var(--warning);
}
.tone-danger .timeline-dot {
  background: var(--danger);
}
.tone-info .timeline-dot {
  background: var(--info);
}
.timeline-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-2);
  font-size: var(--text-md);
}
.timeline-title {
  font-weight: 500;
}
.timeline-time {
  margin-left: auto;
  font-size: var(--text-sm);
}
.timeline-body {
  margin-top: var(--space-2);
  min-width: 0;
}
</style>
