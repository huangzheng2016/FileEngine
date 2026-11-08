<script setup lang="ts">
import { computed } from 'vue'
import EmptyState from './EmptyState.vue'
import StatusPill from './StatusPill.vue'
import { diffValues } from './diff'
import type { LabelMap } from './labels'

// What changed between two versions, one row per field. Field names come from `labels`
// (dot paths -> 中文名), so the user never reads raw keys; an unmapped path falls back to the
// key itself, which is the cue to add it. Raw JSON of both versions belongs in a CodeBlock tab.
//   <DiffView :before="v1.config" :after="v2.config" :labels="{ 'retry.max': '最大重试次数' }" />
//   <DiffView ... before-label="服务端" after-label="草稿" />   (save conflicts)
const props = withDefaults(
  defineProps<{
    before: unknown
    after: unknown
    labels?: Record<string, string>
    // format a value for a path (units, enums); default handles empty, booleans and JSON
    format?: (value: unknown, path: string) => string
    beforeLabel?: string
    afterLabel?: string
  }>(),
  { labels: () => ({}), format: undefined, beforeLabel: '修改前', afterLabel: '修改后' },
)

const KINDS = {
  added: { text: '新增', tone: 'success' },
  removed: { text: '删除', tone: 'danger' },
  changed: { text: '修改', tone: 'warning' },
} satisfies LabelMap

function show(v: unknown): string {
  if (v === undefined || v === null || v === '') return '—'
  if (typeof v === 'boolean') return v ? '是' : '否'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

const rows = computed(() =>
  diffValues(props.before, props.after).map((c) => {
    const path = c.path.join('.')
    const fmt = (v: unknown) => (v === undefined ? '—' : props.format ? props.format(v, path) : show(v))
    return {
      key: path,
      name: props.labels[path] ?? (path || '整体'),
      kind: c.kind,
      before: fmt(c.before),
      after: fmt(c.after),
    }
  }),
)
</script>

<template>
  <EmptyState v-if="rows.length === 0" icon="check-circle" title="没有变化" />
  <div v-else class="table-wrap">
    <table>
      <thead>
        <tr>
          <th>字段</th>
          <th>变更</th>
          <th>{{ beforeLabel }}</th>
          <th>{{ afterLabel }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.key">
          <td>{{ r.name }}</td>
          <td><StatusPill :value="r.kind" :map="KINDS" /></td>
          <td class="wrap diff-before">{{ r.before }}</td>
          <td class="wrap">{{ r.after }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.diff-before {
  color: var(--text-muted);
}
</style>
