<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import ActionMenu from '@/base/ActionMenu.vue'
import Icon from '@/base/Icon.vue'
import Pagination from '@/base/Pagination.vue'
import Segmented from '@/base/Segmented.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { fmtLabel, fmtNumber } from '@/base/format'
import { SESSION_STAGE } from '@/base/labels'
import { fmtRelative, fmtTime } from '@/base/time'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useConfirm } from '@/base/useConfirm'
import { useFetch } from '@/base/useFetch'
import { useList } from '@/base/useList'
import { usePolling } from '@/base/usePolling'
import { toSec } from '@/api/http'
import { deleteSession, listSessions } from '@/api/sessions'
import { listFilesystems, listModels } from '@/api/settings'
import type { ScanSession } from '@/api/types'
import { listRunning, sessionPath, stageOf, type Stage } from '@/lib/sessionView'
import ModelFormDialog from './ModelFormDialog.vue'
import SessionCreateDialog from './SessionCreateDialog.vue'
import StorageFormDialog from './StorageFormDialog.vue'

const router = useRouter()
const confirm = useConfirm()

const storages = useFetch(listFilesystems, { immediate: true })
const models = useFetch(listModels, { immediate: true })
const storageName = (id: number) =>
  storages.data.value ? (storages.data.value.find((f) => f.id === id)?.name ?? '已删除的存储') : ''
const missing = computed(() => ({
  storage: storages.data.value?.length === 0,
  model: models.data.value?.length === 0,
}))
const setup = computed(() => missing.value.storage || missing.value.model)
const adding = ref<'' | 'storage' | 'model'>('')

const stage = (s: ScanSession) => stageOf(s, listRunning(s))

// The filter groups the stages a person acts on; running stages show only under 全部.
const FILTER_STAGES: Record<string, readonly Stage[]> = {
  ready: ['ready'],
  planned: ['planned'],
  done: ['done', 'no_moves'],
}
const STATUS_OPTIONS = [
  { value: '', label: '全部' },
  ...Object.keys(FILTER_STAGES).map((k) => ({ value: k, label: fmtLabel(SESSION_STAGE, k) })),
]

const list = useList(
  async (p: { q: string; status: string; page: number; size: number }, signal) => {
    const all = await listSessions(signal)
    const q = p.q.trim().toLowerCase()
    const stages = FILTER_STAGES[p.status]
    const rows = all
      .filter((s) => !stages || stages.includes(stage(s)))
      .filter((s) => !q || `${s.scan_path} ${s.goal}`.toLowerCase().includes(q))
      .sort((a, b) => b.id - a.id)
    return { items: rows.slice((p.page - 1) * p.size, p.page * p.size), total: rows.length }
  },
  { q: '', status: '' },
  { search: 'q' },
)
const filtered = computed(() => !!(list.filters.q || list.filters.status))

const active = computed(() => list.items.value.some((s) => s.status === 'scanning' || listRunning(s)))
usePolling(
  async () => {
    await list.refresh()
    if (list.error.value) throw new Error(list.error.value)
  },
  { interval: 3000, enabled: active },
)

const creating = ref(false)
const open = (s: ScanSession) => void router.push(`/sessions/${s.id}`)

async function remove(s: ScanSession) {
  const ok = await confirm({
    message: `删除「${sessionPath(s)}」的整理任务？扫描结果和整理方案会一并删除，存储里的文件不受影响。`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteSession(s.id)
    toast.success('已删除')
    void list.refresh()
  } catch (e) {
    toast.error('删除失败', e)
  }
}

const menuFor = (s: ScanSession) => [
  { label: '删除', icon: 'trash-2' as const, danger: true, onSelect: () => void remove(s) },
]
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <input v-model="list.filters.q" type="search" class="search" placeholder="搜索目录或要求" aria-label="搜索" />
      <Segmented v-model="list.filters.status" :options="STATUS_OPTIONS" label="状态" />
      <span class="spacer" />
      <button type="button" class="primary" @click="creating = true"><Icon name="plus" />新建</button>
    </div>

    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>目录</th>
            <th class="hide-sm">存储</th>
            <th>状态</th>
            <th class="num hide-sm">文件</th>
            <th class="hide-sm">更新时间</th>
            <th class="col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="s in list.items.value"
            :key="s.id"
            class="clickable"
            tabindex="0"
            @click="open(s)"
            @keydown.enter.self.prevent="open(s)"
          >
            <td>
              <span v-tip="sessionPath(s)" class="truncate">{{ sessionPath(s) }}</span>
              <span v-if="s.goal" v-tip="s.goal" class="cell-sub truncate">{{ s.goal }}</span>
            </td>
            <td class="hide-sm">{{ storageName(s.filesystem_id) }}</td>
            <td><StatusPill :value="stage(s)" :map="SESSION_STAGE" /></td>
            <td class="num hide-sm">{{ fmtNumber(s.status === 'scanning' ? s.scanned_count : s.total_files) }}</td>
            <td v-tip="fmtTime(toSec(s.updated_at))" class="tabular hide-sm">
              {{ fmtRelative(toSec(s.updated_at)) }}
            </td>
            <td class="row-actions" @click.stop>
              <span><ActionMenu :items="menuFor(s)" /></span>
            </td>
          </tr>
          <TableState
            :colspan="6"
            :loading="list.loading.value && !list.items.value.length"
            :error="list.error.value"
            :empty="list.items.value.length === 0"
            :empty-text="filtered ? '没有符合条件的整理任务' : setup ? '开始前需要：' : '暂无整理任务'"
            @retry="list.refresh"
          >
            <button v-if="filtered" type="button" class="btn-sm" @click="list.reset">清除筛选</button>
            <ul v-else-if="setup" class="setup">
              <li>
                <Icon v-if="!missing.storage" name="check" class="text-success" />存储
                <button v-if="missing.storage" type="button" class="btn-sm" @click="adding = 'storage'">
                  新建存储
                </button>
              </li>
              <li>
                <Icon v-if="!missing.model" name="check" class="text-success" />模型
                <button v-if="missing.model" type="button" class="btn-sm" @click="adding = 'model'">新建模型</button>
              </li>
            </ul>
            <button v-else type="button" @click="creating = true">新建整理任务</button>
          </TableState>
        </tbody>
      </table>
    </div>

    <Pagination
      :total="list.total.value"
      :page="list.page.value"
      :size="list.size.value"
      @update:page="list.setPage"
      @update:size="list.setSize"
    />

    <SessionCreateDialog :open="creating" @close="creating = false" @created="open" />
    <StorageFormDialog :open="adding === 'storage'" :editing="null" @close="adding = ''" @saved="storages.refresh()" />
    <ModelFormDialog :open="adding === 'model'" :editing="null" @close="adding = ''" @saved="models.refresh()" />
  </div>
</template>

<style scoped>
.setup {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: 0;
  list-style: none;
}
.setup li {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
}
@media (max-width: 760px) {
  .hide-sm {
    display: none;
  }
}
</style>
