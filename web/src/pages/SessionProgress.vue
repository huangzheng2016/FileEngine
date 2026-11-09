<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import EmptyState from '@/base/EmptyState.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import ProgressBar from '@/base/ProgressBar.vue'
import StatusPill from '@/base/StatusPill.vue'
import { fmtLabel, fmtNumber } from '@/base/format'
import { RUN_PHASE, RUN_STATUS } from '@/base/labels'
import { fmtDuration, fmtRelative, fmtTime } from '@/base/time'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useConfirm } from '@/base/useConfirm'
import { useFetch } from '@/base/useFetch'
import { useRouteQuery } from '@/base/useRouteQuery'
import { toSec } from '@/api/http'
import { revertRun } from '@/api/sessions'
import type { AgentRun } from '@/api/types'
import RunTranscript from '@/components/RunTranscript.vue'
import StreamMarkdown from '@/components/StreamMarkdown.vue'
import type { RunView, Timeline } from '@/lib/transcript'
import { loadRunTimeline } from '@/lib/useSessionStream'

const props = defineProps<{ sessionId: number; runs: AgentRun[]; runsError: string; timeline: Timeline }>()
const emit = defineEmits<{ changed: [] }>()
const confirm = useConfirm()

const { query, set } = useRouteQuery({ run: 0 })
const selectedId = computed(() => query.value.run || props.runs[0]?.id || 0)
const selected = computed(() => props.runs.find((r) => r.id === selectedId.value))

const history = useFetch(async (signal) => {
  const t = await loadRunTimeline(props.sessionId, selectedId.value, signal)
  return t.runs[selectedId.value]
})
const live = computed(() => props.timeline.runs[selectedId.value])
watch(
  [selectedId, () => !!live.value],
  ([runId, isLive]) => {
    if (runId && !isLive) void history.refresh()
    else history.cancel()
  },
  { immediate: true },
)
const view = computed<RunView | undefined>(() => live.value ?? history.data.value ?? undefined)

const moves = (r: AgentRun | undefined) => r?.phase === 'executing' || r?.phase === 'revert'
const runName = (r: AgentRun) => (r.phase === 'chat' && r.title) || fmtLabel(RUN_PHASE, r.phase)

const status = computed(() => (live.value?.done || !live.value ? selected.value?.status : 'running'))
// A run whose transcript ends in the assistant's answer already shows its summary there.
const answered = computed(() =>
  (view.value?.lanes ?? []).some((l) => l.items.some((i) => i.kind === 'message' && i.role === 'assistant' && i.text)),
)
const summary = computed(() => (answered.value ? '' : view.value?.summary || selected.value?.summary || ''))
const error = computed(() => view.value?.error || selected.value?.error || '')
const progress = computed(() => view.value?.progress)
const progressValue = computed(() => {
  const p = progress.value
  if (!p || !p.total) return null
  const done = moves(selected.value) ? p.success + p.failed + p.skipped : p.tagged
  return { value: done, max: p.total }
})
const tokens = computed(() => view.value?.tokens?.total ?? selected.value?.total_tokens ?? 0)

const duration = (r: AgentRun) => {
  const start = toSec(r.started_at)
  const end = toSec(r.ended_at)
  return start && end && end - start >= 1 ? fmtDuration(end - start) : ''
}

const reverting = ref(false)
const canRevert = computed(() => {
  const r = selected.value
  return !!r && r.phase === 'executing' && (r.status === 'done' || r.status === 'partial') && !r.reverted_by_run_id
})
function revertMessage(r: AgentRun) {
  const n = progress.value?.success ?? 0
  const count = n ? ` ${fmtNumber(n)} 项` : '文件'
  return r.exec_mode === 'copy'
    ? `删除这次执行复制出的${count}？原文件不受影响。`
    : `把这次执行移动的${count}放回原处？`
}
async function revert() {
  const r = selected.value
  if (!r || reverting.value) return
  const ok = await confirm({ title: '撤销执行', message: revertMessage(r), confirmText: '撤销' })
  if (!ok) return
  reverting.value = true
  try {
    const runId = await revertRun(r.id)
    toast.success('已开始撤销')
    emit('changed')
    if (runId) set({ run: runId })
  } catch (e) {
    toast.error('撤销失败', e)
  } finally {
    reverting.value = false
  }
}
</script>

<template>
  <div class="runs">
    <p v-if="runsError" class="text-danger text-sm">加载运行记录失败：{{ runsError }}</p>
    <EmptyState v-if="!runs.length && !runsError" icon="play" title="还没有运行过" />
    <div v-else class="runs-body">
      <div class="table-wrap">
        <table aria-label="运行记录">
          <tbody>
            <tr
              v-for="r in runs"
              :key="r.id"
              class="clickable"
              :class="{ selected: r.id === selectedId }"
              tabindex="0"
              :aria-current="r.id === selectedId || undefined"
              @click="set({ run: r.id })"
              @keydown.enter.self.prevent="set({ run: r.id })"
            >
              <td>
                <span class="run-line">
                  <span>{{ fmtLabel(RUN_PHASE, r.phase) }}</span>
                  <StatusPill :value="r.status" :map="RUN_STATUS" />
                </span>
                <span v-tip="fmtTime(toSec(r.started_at))" class="cell-sub">
                  {{ fmtRelative(toSec(r.started_at)) }}<template v-if="duration(r)"> · {{ duration(r) }}</template>
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <section v-if="selected" class="card run-card" aria-label="运行过程">
        <div class="card-head">
          <div>
            <h3 v-tip="tokens ? `用量 ${fmtNumber(tokens)} token` : ''" class="truncate">{{ runName(selected) }}</h3>
          </div>
          <StatusPill :value="status" :map="RUN_STATUS" />
          <button v-if="canRevert" type="button" class="btn-sm" :disabled="reverting" @click="revert">
            <Icon name="rotate-ccw" :size="14" />撤销
          </button>
        </div>
        <ProgressBar
          v-if="progressValue"
          :value="progressValue.value"
          :max="progressValue.max"
          :label="`${fmtNumber(progressValue.value)} / ${fmtNumber(progressValue.max)}`"
        />
        <p v-if="progress && (progress.failed || progress.skipped)" class="text-sm text-muted tabular">
          <template v-if="progress.failed">失败 {{ fmtNumber(progress.failed) }} 项</template>
          <template v-if="progress.failed && progress.skipped">，</template>
          <template v-if="progress.skipped">跳过 {{ fmtNumber(progress.skipped) }} 项</template>
        </p>
        <Alert v-if="error && status !== 'running'" tone="danger">{{ error }}</Alert>
        <StreamMarkdown v-if="summary" :source="summary" />
        <Loading v-if="!view && history.loading.value && !moves(selected)" />
        <p v-else-if="!view && history.error.value" class="text-danger">加载过程失败：{{ history.error.value }}</p>
        <RunTranscript v-else-if="view && !moves(selected)" :run="view" />
      </section>
    </div>
  </div>
</template>

<style scoped>
.runs {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}
.runs-body {
  display: grid;
  grid-template-columns: 200px minmax(0, 1fr);
  gap: var(--space-3);
  align-items: start;
}
.run-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.run-card {
  min-width: 0;
}
@media (max-width: 760px) {
  .runs-body {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
