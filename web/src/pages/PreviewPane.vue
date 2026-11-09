<script setup lang="ts">
import { computed, watch } from 'vue'
import Icon from '@/base/Icon.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { fmtBytes, fmtNumber, fmtRatio } from '@/base/format'
import { ENTRY_FLAG, PREVIEW_STATUS } from '@/base/labels'
import { vTip } from '@/base/tooltip'
import { useFetch } from '@/base/useFetch'
import { PREVIEW_LIMIT, previewTree } from '@/api/files'
import type { PreviewItem } from '@/api/types'
import { crumbs, resetReason, sortItems } from '@/lib/paths'

const props = defineProps<{ sessionId: number; side: 'before' | 'after'; path: string; rev: number; busy?: boolean }>()
const emit = defineEmits<{ open: [path: string]; reset: [item: PreviewItem] }>()

const list = useFetch((signal) => previewTree(props.sessionId, props.side, props.path, signal))
watch(
  () => [props.path, props.rev],
  () => void list.refresh(),
  { immediate: true },
)
const items = computed(() => sortItems(list.data.value?.items ?? []))
const total = computed(() => list.data.value?.total ?? 0)
const trail = computed(() => crumbs(props.path, props.side === 'before' ? '整理前' : '整理后', props.side === 'after'))

const countText = (n?: number) => (n == null ? '—' : `${fmtNumber(n)} 个`)
const resetBlocked = (it: PreviewItem) => resetReason(it) || (props.busy ? '正在重置' : '')

function open(it: PreviewItem) {
  if (it.is_dir) emit('open', it.path)
}
</script>

<template>
  <section class="pane" :aria-label="side === 'before' ? '整理前' : '整理后'">
    <nav class="crumbs text-sm" aria-label="位置">
      <template v-for="(c, i) in trail" :key="c.path">
        <Icon v-if="i" name="chevron-right" :size="12" class="text-muted" />
        <button
          type="button"
          class="ghost btn-sm"
          :aria-current="i === trail.length - 1 || undefined"
          @click="emit('open', c.path)"
        >
          {{ c.label }}
        </button>
      </template>
    </nav>
    <div class="table-wrap">
      <table>
        <tbody>
          <tr
            v-for="it in items"
            :key="it.path"
            :class="{ clickable: it.is_dir }"
            :tabindex="it.is_dir ? 0 : undefined"
            @click="open(it)"
            @keydown.enter.self.prevent="open(it)"
          >
            <td>
              <span class="name">
                <Icon :name="it.is_dir ? 'folder' : 'file'" :size="14" />
                <span class="truncate">{{ it.name }}</span>
              </span>
              <span v-if="side === 'before' && it.status === 'moved' && it.new_path" class="cell-sub">
                → {{ it.new_path }}
              </span>
              <span v-else-if="side === 'before' && it.status === 'partial' && it.moved_count" class="cell-sub">
                其中 {{ fmtNumber(it.moved_count) }} 项将移动
              </span>
              <span v-else-if="side === 'after' && it.original_path" class="cell-sub">来自 {{ it.original_path }}</span>
              <span v-if="it.reason && it.role !== 'container'" class="cell-sub">{{ it.reason }}</span>
            </td>
            <td>
              <span class="flags">
                <StatusPill v-if="side === 'before' && it.status" :value="it.status" :map="PREVIEW_STATUS" />
                <StatusPill v-if="it.needs_review" value="needs_review" :map="ENTRY_FLAG" />
                <StatusPill v-if="it.sensitive" value="sensitive" :map="ENTRY_FLAG" />
                <span v-if="it.confidence" v-tip="'助手的把握'" class="text-muted text-sm tabular">
                  {{ fmtRatio(it.confidence) }}
                </span>
              </span>
            </td>
            <td class="num">
              {{ it.is_dir ? countText(it.file_count) : fmtBytes(it.size) }}
            </td>
            <td v-if="side === 'after'" class="row-actions">
              <span>
                <button
                  v-tip="resetBlocked(it)"
                  type="button"
                  class="ghost btn-sm"
                  :disabled="!!resetBlocked(it)"
                  @click.stop="emit('reset', it)"
                >
                  重置
                </button>
              </span>
            </td>
          </tr>
          <TableState
            :colspan="side === 'after' ? 4 : 3"
            :loading="list.loading.value && !items.length"
            :error="list.error.value"
            :empty="items.length === 0"
            :empty-text="side === 'after' && !path ? '还没有要移动的文件' : '这个目录是空的'"
            @retry="list.refresh"
          />
        </tbody>
      </table>
    </div>
    <p v-if="total > items.length" class="text-muted text-sm tabular">
      共 {{ fmtNumber(total) }} 项，只列出前 {{ fmtNumber(PREVIEW_LIMIT) }} 项
    </p>
  </section>
</template>

<style scoped>
.pane {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}
.crumbs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px;
}
.name {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}
.flags {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}
</style>
