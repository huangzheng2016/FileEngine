<script setup lang="ts">
import { computed, ref } from 'vue'
import CodeBlock from '@/base/CodeBlock.vue'
import Icon from '@/base/Icon.vue'
import { fmtMs } from '@/base/format'
import { fileRows, readExcerpt, toolLabel, toolSubject } from '@/lib/toolView'
import type { ToolItem } from '@/lib/transcript'

// One agent tool call: a single folded line; a failure opens itself.
const props = defineProps<{ tool: ToolItem }>()

const toggled = ref<boolean | null>(null)
const open = computed(() => toggled.value ?? props.tool.status === 'failed')
const subject = computed(() => toolSubject(props.tool))
const args = computed(() => (props.tool.args ?? {}) as Record<string, unknown>)
const files = computed(() =>
  props.tool.name === 'list_files' || props.tool.name === 'query_files' ? fileRows(props.tool.output) : null,
)
const excerpt = computed(() => (props.tool.name === 'read_file' ? readExcerpt(props.tool.output) : ''))
const script = computed(() =>
  props.tool.name === 'run_script' ? String(args.value.script ?? args.value.source ?? '') : '',
)
</script>

<template>
  <div class="tool" :class="tool.status">
    <button type="button" class="tool-head" :aria-expanded="open" @click="toggled = !open">
      <span v-if="tool.status === 'running'" class="tool-spinner" aria-label="运行中" />
      <Icon v-else :name="tool.status === 'ok' ? 'check-circle' : 'x-circle'" :size="14" class="tool-icon" />
      <span class="tool-name">{{ toolLabel(tool.name) }}</span>
      <span class="tool-subject truncate">{{ subject }}</span>
      <span v-if="tool.durationMs" class="tool-time tabular">{{ fmtMs(tool.durationMs) }}</span>
      <Icon name="chevron-right" :size="14" class="tool-chevron" />
    </button>
    <div v-if="open" class="tool-body">
      <p v-if="tool.status === 'failed'" class="text-danger">{{ tool.error || '未完成' }}</p>
      <p v-if="tool.name === 'set_target' && args.new_path" class="tool-move mono">
        <span>{{ args.path }}</span>
        <Icon name="arrow-right" :size="14" />
        <span>{{ args.new_path }}</span>
      </p>
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
              <td class="text-muted">{{ f.target || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="files && files.total > files.rows.length" class="text-muted text-sm">共 {{ files.total }} 项</p>
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
.tool-spinner {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  border: 2px solid var(--accent-soft);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
.tool-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) 10px 10px;
  border-top: 1px solid var(--border);
  min-width: 0;
}
.tool-move,
.tool-file {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  overflow-wrap: anywhere;
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
