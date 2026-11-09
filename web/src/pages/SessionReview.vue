<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import Icon from '@/base/Icon.vue'
import Pagination from '@/base/Pagination.vue'
import TableState from '@/base/TableState.vue'
import { fmtNumber } from '@/base/format'
import { toast } from '@/base/toast'
import { useFetch } from '@/base/useFetch'
import { useRouteQuery } from '@/base/useRouteQuery'
import { listFiles, updateFileTarget } from '@/api/files'
import { confirmReview } from '@/api/sessions'
import type { FileEntry } from '@/api/types'
import { parentOf } from '@/lib/paths'
import { landing, shownPath } from '@/lib/toolView'
import TargetDialog, { type TargetEntry } from './TargetDialog.vue'

// The decisions the AI was unsure about. They are skipped by 执行 until confirmed
// here, changed, or kept in place.
const props = defineProps<{ sessionId: number; rev: number }>()
const emit = defineEmits<{ changed: [] }>()

const SIZE = 20
const { query, set } = useRouteQuery({ rp: 1 })
const list = useFetch((signal) =>
  listFiles({ session_id: props.sessionId, needs_review: true, page: query.value.rp, size: SIZE }, signal),
)
watch(
  () => [props.sessionId, props.rev, query.value.rp],
  () => void list.refresh(),
  { immediate: true },
)
const rows = computed(() => list.data.value?.items ?? [])
const total = computed(() => list.data.value?.total ?? 0)

const busy = ref(false)
async function run(action: () => Promise<unknown>, done: string, failed: string) {
  if (busy.value) return
  busy.value = true
  try {
    await action()
    toast.success(done)
    emit('changed')
  } catch (e) {
    toast.error(failed, e)
  } finally {
    busy.value = false
  }
}

const confirmOne = (f: FileEntry) => run(() => confirmReview(props.sessionId, [f.id]), '已确认', '确认失败')
const confirmPage = () =>
  run(
    () =>
      confirmReview(
        props.sessionId,
        rows.value.map((f) => f.id),
      ),
    `已确认 ${fmtNumber(rows.value.length)} 项`,
    '确认失败',
  )
const keep = (f: FileEntry) => run(() => updateFileTarget(f.id, ''), '已设为保持原位', '保存失败')

const editing = ref<TargetEntry | null>(null)
const menu = (f: FileEntry) => [
  { label: '改位置', icon: 'pencil' as const, onSelect: () => (editing.value = f) },
  { label: '保持原位', icon: 'x' as const, onSelect: () => void keep(f) },
]
</script>

<template>
  <section class="card" aria-label="需要你确认">
    <div class="card-head">
      <div>
        <h3>需要你确认 {{ fmtNumber(total) }} 项</h3>
        <p class="card-desc">AI 对这些文件拿不准，确认之前执行会跳过它们</p>
      </div>
      <button v-if="rows.length" type="button" class="btn-sm" :disabled="busy" @click="confirmPage">
        <Icon name="check" :size="14" />{{ total > rows.length ? `确认这 ${fmtNumber(rows.length)} 项` : '全部确认' }}
      </button>
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>文件</th>
            <th class="col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="f in rows" :key="f.id">
            <td class="wrap">
              <span class="name">
                <Icon :name="f.file_type === 'directory' ? 'folder' : 'file'" :size="14" class="text-muted" />
                <span class="truncate">{{ f.name }}</span>
              </span>
              <span class="cell-sub">
                <span class="route">
                  <span class="truncate">{{ shownPath(parentOf(f.original_path)) }}</span>
                  <Icon name="arrow-right" :size="12" />
                  <span class="truncate target">{{ landing(f.original_path, f.new_path) }}</span>
                </span>
              </span>
              <span v-if="f.reason" class="cell-sub">{{ f.reason }}</span>
            </td>
            <td class="row-actions">
              <span>
                <button type="button" class="ghost btn-sm" :disabled="busy" @click="confirmOne(f)">确认</button>
                <ActionMenu :items="menu(f)" />
              </span>
            </td>
          </tr>
          <TableState
            :colspan="2"
            :loading="list.loading.value && !rows.length"
            :error="list.error.value"
            :empty="rows.length === 0"
            empty-text="没有需要确认的文件"
            @retry="list.refresh"
          />
        </tbody>
      </table>
    </div>
    <Pagination v-if="total > SIZE" :total="total" :page="query.rp" :size="SIZE" @update:page="set({ rp: $event })" />
    <TargetDialog :entry="editing" @close="editing = null" @saved="emit('changed')" />
  </section>
</template>

<style scoped>
.route {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
.target {
  color: var(--text);
}
.name {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}
</style>
