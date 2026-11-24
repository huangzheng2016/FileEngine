<script setup lang="ts">
import { computed } from 'vue'
import CodeBlock from '@/base/CodeBlock.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { fmtMs, fmtNumber } from '@/base/format'
import {
  failedDecisions,
  fileRows,
  landing,
  readExcerpt,
  ruleRows,
  toolDecisions,
  toolLabel,
  toolProblem,
  toolSummary,
} from '@/lib/toolView'
import { baseName } from '@/lib/paths'
import type { ToolItem } from '@/lib/transcript'
import { useOpenState } from './openState'

// One step of an agent run: a single line, with the step's own view of its
// result below. A failed step opens itself so its reason is never hidden.
const props = defineProps<{ tool: ToolItem; scope?: string }>()

const state = useOpenState()
const stateKey = computed(() => `${props.scope ?? ''}t:${props.tool.key}`)
const open = computed(() => state.get(stateKey.value) ?? props.tool.status === 'failed')
const toggle = () => state.set(stateKey.value, !open.value)

const problem = computed(() => (props.tool.status === 'failed' ? toolProblem(props.tool) : null))
const summary = computed(() => toolSummary(props.tool) || problem.value?.error || '')
const args = computed(() => (props.tool.args ?? {}) as Record<string, unknown>)
const name = computed(() => props.tool.name)

const files = computed(() => (name.value === 'list_entries' ? fileRows(props.tool.output) : null))
const decisions = computed(() => (name.value === 'decide' ? toolDecisions(props.tool).slice(0, 8) : []))
const decisionTotal = computed(() => (name.value === 'decide' ? toolDecisions(props.tool).length : 0))
const rejected = computed(() => (name.value === 'decide' ? failedDecisions(props.tool).slice(0, 8) : []))
const excerpt = computed(() =>
  name.value === 'read_file' || name.value === 'run_script' ? readExcerpt(props.tool.output) : '',
)
const rules = computed(() => (name.value === 'apply_rules' ? ruleRows(props.tool.output) : []))
const script = computed(() => (name.value === 'run_script' ? String(args.value.script ?? '') : ''))
</script>

<template>
  <div class="tool" :class="tool.status">
    <button type="button" class="tool-head" :aria-expanded="open" @click="toggle">
      <Loading v-if="tool.status === 'running'" inline label="" :delay="0" />
      <Icon v-else :name="tool.status === 'ok' ? 'check-circle' : 'x-circle'" :size="14" class="tool-icon" />
      <span class="tool-name">{{ toolLabel(tool.name) }}</span>
      <span class="tool-subject truncate">{{ summary }}</span>
      <span v-if="tool.durationMs" class="tool-time tabular">{{ fmtMs(tool.durationMs) }}</span>
      <Icon name="chevron-right" :size="14" class="tool-chevron" />
    </button>
    <div v-if="open" class="tool-body">
      <template v-if="problem">
        <p class="text-danger">{{ problem.error || '未完成' }}</p>
        <p v-if="problem.hint" class="text-muted text-sm">{{ problem.hint }}</p>
      </template>

      <div v-if="files && files.rows.length" class="table-wrap">
        <table>
          <tbody>
            <tr v-for="(f, i) in files.rows" :key="i">
              <td>
                <span class="tool-file">
                  <Icon :name="f.dir ? 'folder' : 'file'" :size="14" />
                  <span class="truncate">{{ f.name }}</span>
                </span>
              </td>
              <td class="text-muted">{{ f.target ? landing(f.name, f.target) : f.review ? '待复查' : '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="files && (files.total > files.rows.length || files.more)" class="text-muted text-sm">
        共 {{ fmtNumber(files.total) }} 项{{ files.more ? '，还有更多' : '' }}
      </p>

      <ul v-if="decisions.length" class="tool-list text-sm">
        <li v-for="d in decisions" :key="d.key">
          <span class="tool-file">
            <Icon :name="d.role === 'unit' ? 'folder' : 'file'" :size="14" />
            <span class="truncate">{{ baseName(d.path) }}</span>
            <Icon name="arrow-right" :size="14" />
            <span class="truncate">{{ landing(d.path, d.target) }}</span>
          </span>
          <span v-if="d.reason" class="cell-sub">{{ d.reason }}</span>
        </li>
      </ul>
      <p v-if="decisionTotal > decisions.length" class="text-muted text-sm">共 {{ fmtNumber(decisionTotal) }} 项</p>
      <ul v-if="rejected.length" class="tool-list text-sm">
        <li v-for="f in rejected" :key="f.path">
          <span class="truncate">{{ baseName(f.path) }}</span>
          <span class="text-danger">{{ f.error }}</span>
        </li>
      </ul>

      <ul v-if="rules.length" class="tool-list text-sm">
        <li v-for="r in rules" :key="r.name">
          <span>{{ r.name }}</span>
          <span class="text-muted">匹配 {{ fmtNumber(r.matched) }} 项</span>
        </li>
      </ul>

      <pre v-if="excerpt" class="tool-excerpt">{{ excerpt }}</pre>
      <CodeBlock v-if="script" :value="script" lang="text" :max-lines="16" />
      <details class="tool-raw">
        <summary class="text-sm">原始数据</summary>
        <div class="field-label">参数</div>
        <CodeBlock :value="tool.args ?? {}" :max-lines="12" />
        <template v-if="tool.output !== undefined">
          <div class="field-label">结果</div>
          <CodeBlock :value="tool.output" :max-lines="12" />
        </template>
      </details>
    </div>
  </div>
</template>

<style scoped>
.tool {
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface-raised);
}
.tool.failed {
  border-color: color-mix(in srgb, var(--danger) 35%, transparent);
}
.tool-head {
  width: 100%;
  height: 32px;
  justify-content: flex-start;
  gap: var(--space-2);
  padding: 0 10px;
  font-weight: 400;
  background: transparent;
  border: 0;
}
.tool-head:hover {
  background: var(--surface-hover);
}
.tool-name {
  flex-shrink: 0;
  font-weight: 600;
}
.tool-subject {
  flex: 1;
  min-width: 0;
  max-width: none;
  color: var(--text-muted);
  text-align: left;
}
.tool-time {
  color: var(--text-muted);
  font-size: var(--text-sm);
}
.ok .tool-icon {
  color: var(--success);
}
.failed .tool-icon {
  color: var(--danger);
}
.tool-chevron {
  color: var(--text-muted);
  transition: transform var(--dur-fast) var(--ease);
}
.tool-head[aria-expanded='true'] .tool-chevron {
  transform: rotate(90deg);
}
.tool-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) 10px 10px;
  border-top: 1px solid var(--border);
  min-width: 0;
}
.tool-file {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  overflow-wrap: anywhere;
}
.tool-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}
.tool-list li {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.tool-excerpt {
  max-height: 200px;
  overflow: auto;
  white-space: pre-wrap;
}
.tool-raw {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
</style>
