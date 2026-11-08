<script setup lang="ts">
// Preview tab: the tree as it is and as it will be, side by side. Anything that looks wrong
// is sent back; low-confidence decisions wait for confirmation.
import { computed, ref, watch } from 'vue'
import Pagination from '@/base/Pagination.vue'
import Segmented from '@/base/Segmented.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { fmtNumber, fmtRatio } from '@/base/format'
import { ENTRY_FLAG } from '@/base/labels'
import { toast } from '@/base/toast'
import { useFetch } from '@/base/useFetch'
import { useRouteQuery } from '@/base/useRouteQuery'
import { useSelection } from '@/base/useSelection'
import { listFiles, resetPlans } from '@/api/files'
import { confirmReview } from '@/api/sessions'
import type { PreviewItem } from '@/api/types'
import PreviewPane from './PreviewPane.vue'

const props = defineProps<{ sessionId: number }>()
const emit = defineEmits<{ changed: [] }>()

const { query, set } = useRouteQuery({ bp: '', ap: '', view: 'tree', rp: 1 })
const rev = ref(0)

const VIEWS = [
  { value: 'tree', label: '全部' },
  { value: 'review', label: '待确认' },
]

async function reset(it: PreviewItem) {
  if (!it.file_id) return
  try {
    await resetPlans(props.sessionId, [it.file_id])
    toast.success('已退回，下次整理会重新决定')
    rev.value++
    emit('changed')
  } catch (e) {
    toast.error('退回失败', e)
  }
}

// ---------- review queue ----------

const SIZE = 50
const review = useFetch((signal) =>
  listFiles({ session_id: props.sessionId, needs_review: true, page: query.value.rp, size: SIZE }, signal),
)
watch(
  () => [query.value.view, query.value.rp, rev.value],
  () => {
    if (query.value.view === 'review') void review.refresh()
  },
  { immediate: true },
)
const rows = computed(() => review.data.value?.items ?? [])
const selection = useSelection(computed(() => rows.value.map((r) => ({ id: String(r.id) }))))
const batching = ref(false)

async function batch(kind: 'confirm' | 'reset') {
  const ids = selection.ids.value.map(Number)
  batching.value = true
  try {
    if (kind === 'confirm') await confirmReview(props.sessionId, ids)
    else await resetPlans(props.sessionId, ids)
    toast.success(kind === 'confirm' ? `已确认 ${ids.length} 项` : `已退回 ${ids.length} 项`)
    selection.clear()
    rev.value++
    emit('changed')
  } catch (e) {
    toast.error(kind === 'confirm' ? '确认失败' : '退回失败', e)
  } finally {
    batching.value = false
  }
}
</script>

<template>
  <div class="page">
    <div v-if="selection.count.value" class="toolbar batch">
      <span>已选 {{ selection.count.value }} 项</span>
      <span class="spacer" />
      <button type="button" class="btn-sm" :disabled="batching" @click="batch('confirm')">确认</button>
      <button type="button" class="btn-sm" :disabled="batching" @click="batch('reset')">退回</button>
      <button type="button" class="ghost btn-sm" @click="selection.clear">取消选择</button>
    </div>
    <div v-else class="toolbar">
      <Segmented
        :model-value="query.view"
        :options="VIEWS"
        label="显示"
        @update:model-value="set({ view: $event, rp: 1 })"
      />
    </div>

    <div v-if="query.view === 'tree'" class="sides">
      <PreviewPane :session-id="sessionId" side="before" :path="query.bp" :rev="rev" @open="set({ bp: $event })" />
      <PreviewPane
        :session-id="sessionId"
        side="after"
        :path="query.ap"
        :rev="rev"
        @open="set({ ap: $event })"
        @reset="reset"
      />
    </div>

    <template v-else>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th class="col-check">
                <input
                  type="checkbox"
                  aria-label="全选本页"
                  :checked="selection.all.value"
                  :indeterminate="selection.some.value"
                  :disabled="rows.length === 0"
                  @change="selection.toggleAll"
                />
              </th>
              <th>文件</th>
              <th>目标</th>
              <th class="num">把握</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="f in rows" :key="f.id">
              <td class="col-check">
                <input
                  type="checkbox"
                  :aria-label="`选择 ${f.name}`"
                  :checked="selection.has(String(f.id))"
                  @change="selection.toggle(String(f.id))"
                />
              </td>
              <td>
                <span class="truncate">{{ f.original_path }}</span>
                <span v-if="f.reason" class="cell-sub">{{ f.reason }}</span>
              </td>
              <td>
                <span class="truncate">{{ f.new_path || '—' }}</span>
                <StatusPill v-if="f.sensitive" value="sensitive" :map="ENTRY_FLAG" />
              </td>
              <td class="num">{{ f.confidence ? fmtRatio(f.confidence) : '—' }}</td>
            </tr>
            <TableState
              :colspan="4"
              :loading="review.loading.value && !rows.length"
              :error="review.error.value"
              :empty="rows.length === 0"
              empty-text="没有待确认的决定"
              @retry="review.refresh"
            />
          </tbody>
        </table>
      </div>
      <Pagination
        v-if="(review.data.value?.total ?? 0) > SIZE"
        :total="review.data.value?.total ?? 0"
        :page="query.rp"
        :size="SIZE"
        @update:page="set({ rp: $event })"
      />
      <p v-if="review.data.value?.total" class="text-muted text-sm tabular">
        待确认的 {{ fmtNumber(review.data.value.total) }} 项在确认前不会执行
      </p>
    </template>
  </div>
</template>

<style scoped>
.sides {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
  align-items: start;
}
@media (max-width: 960px) {
  .sides {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
