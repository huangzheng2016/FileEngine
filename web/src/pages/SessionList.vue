<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import ActionMenu from '@/base/ActionMenu.vue'
import Icon from '@/base/Icon.vue'
import Pagination from '@/base/Pagination.vue'
import Segmented from '@/base/Segmented.vue'
import SplitResizer from '@/base/SplitResizer.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { fmtBytes, fmtNumber, labelOptions } from '@/base/format'
import { SESSION_STATUS } from '@/base/labels'
import { fmtRelative, fmtTime } from '@/base/time'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useConfirm } from '@/base/useConfirm'
import { useFetch } from '@/base/useFetch'
import { useList } from '@/base/useList'
import { usePolling } from '@/base/usePolling'
import { useRouteQuery } from '@/base/useRouteQuery'
import { toSec } from '@/api/http'
import { deleteSession, listSessions } from '@/api/sessions'
import { listFilesystems } from '@/api/settings'
import type { ScanSession } from '@/api/types'
import { sessionPath } from '@/lib/sessionView'
import SessionCreateDialog from './SessionCreateDialog.vue'

const router = useRouter()
const confirm = useConfirm()

const storages = useFetch(listFilesystems, { immediate: true })
const storageName = (id: number) => storages.data.value?.find((f) => f.id === id)?.name ?? '已删除的存储'

const list = useList(
  async (p: { q: string; status: string; page: number; size: number }, signal) => {
    const all = await listSessions(signal)
    const q = p.q.trim().toLowerCase()
    const rows = all
      .filter((s) => !p.status || s.status === p.status)
      .filter((s) => !q || `${s.scan_path} ${s.goal} ${storageName(s.filesystem_id)}`.toLowerCase().includes(q))
      .sort((a, b) => b.id - a.id)
    return { items: rows.slice((p.page - 1) * p.size, p.page * p.size), total: rows.length }
  },
  { q: '', status: '' },
  { search: 'q' },
)
const { query, set } = useRouteQuery({ id: 0 })
const STATUS_OPTIONS = [{ value: '', label: '全部' }, ...labelOptions(SESSION_STATUS)]
const filtered = computed(() => !!(list.filters.q || list.filters.status))

const scanning = computed(() => list.items.value.some((s) => s.status === 'scanning'))
usePolling(
  async () => {
    await list.refresh()
    if (list.error.value) throw new Error(list.error.value)
  },
  { interval: 3000, enabled: scanning },
)

const selected = computed(() => list.items.value.find((s) => s.id === query.value.id))

const creating = ref(false)
function onCreated(s: ScanSession) {
  void router.push(`/sessions/${s.id}`)
}

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
    if (s.id === query.value.id) set({ id: 0 })
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
  <div class="split" :class="{ 'has-detail': !!selected }">
    <section class="split-master">
      <div class="toolbar">
        <input v-model="list.filters.q" type="search" class="search" placeholder="搜索目录或目标" aria-label="搜索" />
        <Segmented v-model="list.filters.status" :options="STATUS_OPTIONS" label="状态" />
        <span class="spacer" />
        <button type="button" class="btn-icon" aria-label="刷新" :disabled="list.loading.value" @click="list.refresh">
          <Icon name="refresh-cw" />
        </button>
        <button type="button" class="primary" @click="creating = true"><Icon name="plus" />新建</button>
      </div>

      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>目录</th>
              <th>扫描</th>
              <th class="num">文件</th>
              <th class="num">更新时间</th>
              <th class="col-actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="s in list.items.value"
              :key="s.id"
              class="clickable"
              :class="{ selected: s.id === query.id }"
              tabindex="0"
              @click="set({ id: s.id })"
              @keydown.enter.self.prevent="set({ id: s.id })"
            >
              <td>
                <span class="truncate">{{ sessionPath(s) }}</span>
                <span class="cell-sub">{{ storageName(s.filesystem_id) }}</span>
              </td>
              <td><StatusPill :value="s.status" :map="SESSION_STATUS" /></td>
              <td class="num">{{ fmtNumber(s.status === 'scanning' ? s.scanned_count : s.total_files) }}</td>
              <td v-tip="fmtTime(toSec(s.updated_at))" class="num">{{ fmtRelative(toSec(s.updated_at)) }}</td>
              <td class="row-actions">
                <span>
                  <RouterLink :to="`/sessions/${s.id}`" class="btn ghost btn-sm" @click.stop>打开</RouterLink>
                  <ActionMenu :items="menuFor(s)" />
                </span>
              </td>
            </tr>
            <TableState
              :colspan="5"
              :loading="list.loading.value && !list.items.value.length"
              :error="list.error.value"
              :empty="list.items.value.length === 0"
              :empty-text="filtered ? '没有符合条件的整理任务' : '暂无整理任务'"
              @retry="list.refresh"
            >
              <button v-if="filtered" type="button" class="btn-sm" @click="list.reset">清除筛选</button>
              <button v-else type="button" class="btn-sm" @click="creating = true">新建整理任务</button>
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
    </section>

    <SplitResizer storage-key="sessions" />

    <section class="split-detail">
      <div v-if="!selected" class="split-empty">选择左侧一个任务查看详情</div>
      <template v-else>
        <div class="detail-head">
          <button type="button" class="ghost btn-icon split-back" aria-label="返回列表" @click="set({ id: 0 })">
            <Icon name="arrow-left" />
          </button>
          <h2>{{ sessionPath(selected) }}</h2>
          <RouterLink :to="`/sessions/${selected.id}`" class="btn primary">打开</RouterLink>
        </div>
        <dl class="kv">
          <dt>存储</dt>
          <dd>{{ storageName(selected.filesystem_id) }}</dd>
          <dt>扫描</dt>
          <dd><StatusPill :value="selected.status" :map="SESSION_STATUS" /></dd>
          <dt>整理目标</dt>
          <dd>{{ selected.goal || '—' }}</dd>
          <dt>文件</dt>
          <dd class="tabular">{{ fmtNumber(selected.total_files) }} 个 · {{ fmtBytes(selected.total_size) }}</dd>
          <dt>计划移动</dt>
          <dd class="tabular">{{ fmtNumber(selected.planned_ops) }}</dd>
          <dt>已执行</dt>
          <dd class="tabular">{{ fmtNumber(selected.executed_ops) }}</dd>
          <dt>创建时间</dt>
          <dd class="tabular">{{ fmtTime(toSec(selected.created_at)) }}</dd>
        </dl>
      </template>
    </section>

    <SessionCreateDialog :open="creating" @close="creating = false" @created="onCreated" />
  </div>
</template>
