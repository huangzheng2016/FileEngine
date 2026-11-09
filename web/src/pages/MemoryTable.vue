<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import Icon from '@/base/Icon.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { fmtLabel, fmtNumber } from '@/base/format'
import { MEMORY_KIND, MEMORY_SOURCE } from '@/base/labels'
import { fmtRelative, fmtTime } from '@/base/time'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useConfirm } from '@/base/useConfirm'
import { useFetch } from '@/base/useFetch'
import { toSec } from '@/api/http'
import { deleteMemory, listMemory, updateMemory } from '@/api/memory'
import type { MemoryItem, MemoryScope } from '@/api/types'
import MemoryFormDialog from './MemoryFormDialog.vue'

const props = withDefaults(
  defineProps<{
    scope: MemoryScope
    scopeId: number
    rev?: number
    q?: string
    emptyText?: string
    editable?: boolean
  }>(),
  { rev: 0, q: '', emptyText: '暂无记忆', editable: true },
)
const emit = defineEmits<{ loaded: [count: number] }>()
const confirm = useConfirm()

const list = useFetch((signal) => listMemory(props.scope, props.scopeId, signal))
watch(
  () => [props.scope, props.scopeId, props.rev],
  async () => {
    await list.refresh()
    emit('loaded', list.data.value?.length ?? 0)
  },
  { immediate: true },
)

const rows = computed(() => {
  const q = props.q.trim().toLowerCase()
  return (list.data.value ?? [])
    .filter((m) => !q || `${m.content} ${m.path}`.toLowerCase().includes(q))
    .sort((a, b) => Number(b.pinned) - Number(a.pinned) || b.hits - a.hits)
})

const formOpen = ref(false)
const editing = ref<MemoryItem | null>(null)
function openForm(m: MemoryItem | null) {
  editing.value = m
  formOpen.value = true
}
defineExpose({ create: () => openForm(null), refresh: list.refresh })

async function togglePin(m: MemoryItem) {
  try {
    await updateMemory(m.id, { pinned: !m.pinned })
    toast.success(m.pinned ? '已取消置顶' : '已置顶')
    void list.refresh()
  } catch (e) {
    toast.error('保存失败', e)
  }
}

async function remove(m: MemoryItem) {
  if (!(await confirm({ message: `删除这条记忆？「${m.content.slice(0, 40)}」`, confirmText: '删除', danger: true })))
    return
  try {
    await deleteMemory(m.id)
    toast.success('已删除')
    void list.refresh()
  } catch (e) {
    toast.error('删除失败', e)
  }
}

// Agent-written items carry Markdown code spans; the cell is plain text.
const plain = (text: string) => text.replace(/`/g, '')
// Manual and correction sources repeat what the row already shows.
const sourceOf = (m: MemoryItem) =>
  m.source === 'agent' || m.source === 'reflection' ? fmtLabel(MEMORY_SOURCE, m.source) : ''
const subOf = (m: MemoryItem) => [sourceOf(m), m.path].filter(Boolean).join(' · ')

const menuFor = (m: MemoryItem) => [
  { label: m.pinned ? '取消置顶' : '置顶', icon: 'pin' as const, onSelect: () => void togglePin(m) },
  { label: '删除', icon: 'trash-2' as const, danger: true, onSelect: () => void remove(m) },
]
</script>

<template>
  <div class="table-wrap">
    <table>
      <colgroup>
        <col />
        <col class="w-kind" />
        <col class="w-hits" />
        <col class="w-time" />
        <col v-if="editable" />
      </colgroup>
      <thead>
        <tr>
          <th>内容</th>
          <th>类型</th>
          <th class="num">用过</th>
          <th>更新时间</th>
          <th v-if="editable" class="col-actions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="m in rows"
          :key="m.id"
          :class="{ clickable: editable }"
          :tabindex="editable ? 0 : undefined"
          @click="editable && openForm(m)"
          @keydown.enter.self.prevent="editable && openForm(m)"
        >
          <td class="wrap">
            <span class="content">
              <Icon v-if="m.pinned" v-tip="'置顶'" name="pin" :size="14" />
              {{ plain(m.content) }}
            </span>
            <span v-if="subOf(m)" class="cell-sub">{{ subOf(m) }}</span>
          </td>
          <td><StatusPill :value="m.kind" :map="MEMORY_KIND" /></td>
          <td class="num">{{ m.hits ? `${fmtNumber(m.hits)} 次` : '—' }}</td>
          <td v-tip="fmtTime(toSec(m.updated_at))">{{ fmtRelative(toSec(m.updated_at)) }}</td>
          <td v-if="editable" class="row-actions">
            <ActionMenu :items="menuFor(m)" />
          </td>
        </tr>
        <TableState
          :colspan="editable ? 5 : 4"
          :loading="list.loading.value && !list.data.value"
          :error="list.error.value"
          :empty="rows.length === 0"
          :empty-text="q ? '没有符合条件的记忆' : emptyText"
          @retry="list.refresh"
        />
      </tbody>
    </table>
  </div>
  <MemoryFormDialog
    :open="formOpen"
    :editing="editing"
    :scope="scope"
    :scope-id="scopeId"
    @close="formOpen = false"
    @saved="list.refresh"
  />
</template>

<style scoped>
.content {
  display: inline-flex;
  align-items: flex-start;
  gap: var(--space-1);
}
.w-kind {
  width: 88px;
}
.w-hits {
  width: 72px;
}
.w-time {
  width: 112px;
}
</style>
