<script setup lang="ts">
import { ref } from 'vue'
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
import { deleteMemory, updateMemory } from '@/api/memory'
import type { MemoryItem, MemoryScope, Id } from '@/api/types'
import MemoryFormDialog from './MemoryFormDialog.vue'

defineProps<{
  items: MemoryItem[]
  loading: boolean
  error: string
  scope: MemoryScope
  scopeId: Id
  filtered: boolean
  emptyText: string
}>()
const emit = defineEmits<{ changed: []; retry: []; clear: [] }>()
const confirm = useConfirm()

const formOpen = ref(false)
const editing = ref<MemoryItem | null>(null)
function openForm(m: MemoryItem | null) {
  editing.value = m
  formOpen.value = true
}
defineExpose({ create: () => openForm(null) })

async function togglePin(m: MemoryItem) {
  try {
    await updateMemory(m.id, { pinned: !m.pinned })
    toast.success(m.pinned ? '已取消置顶' : '已置顶')
    emit('changed')
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
    emit('changed')
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
        <col />
      </colgroup>
      <thead>
        <tr>
          <th>内容</th>
          <th>类型</th>
          <th class="num">用过</th>
          <th>更新时间</th>
          <th class="col-actions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="m in items"
          :key="m.id"
          class="clickable"
          tabindex="0"
          @click="openForm(m)"
          @keydown.enter.self.prevent="openForm(m)"
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
          <td v-tip="fmtTime(m.updated_at)">{{ fmtRelative(m.updated_at) }}</td>
          <td class="row-actions">
            <ActionMenu :items="menuFor(m)" />
          </td>
        </tr>
        <TableState
          :colspan="5"
          :loading="loading"
          :error="error"
          :empty="items.length === 0"
          :empty-text="filtered ? '没有符合条件的记忆' : emptyText"
          @retry="emit('retry')"
        >
          <button v-if="filtered" type="button" class="btn-sm" @click="emit('clear')">清除筛选</button>
        </TableState>
      </tbody>
    </table>
  </div>
  <MemoryFormDialog
    :open="formOpen"
    :editing="editing"
    :scope="scope"
    :scope-id="scopeId"
    @close="formOpen = false"
    @saved="emit('changed')"
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
