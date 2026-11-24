<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import EmptyState from '@/base/EmptyState.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { fmtBytes, fmtLabel, fmtNumber } from '@/base/format'
import { EXEC_MODE } from '@/base/labels'
import { toast } from '@/base/toast'
import { useFetch } from '@/base/useFetch'
import { useRouteQuery } from '@/base/useRouteQuery'
import { updateFileTarget } from '@/api/files'
import { hasId, type PreviewItem, type Run, type Session } from '@/api/types'
import RunActivity from '@/components/RunActivity.vue'
import StreamMarkdown from '@/components/StreamMarkdown.vue'
import { loadRunProgress } from '@/lib/runProgress'
import { executable, flowRun, runningRun, stageOf } from '@/lib/sessionView'
import type { Timeline } from '@/lib/transcript'
import { useRevert } from '@/lib/useRevert'
import PreviewPane from './PreviewPane.vue'
import SessionReview from './SessionReview.vue'
import TargetDialog, { type TargetEntry } from './TargetDialog.vue'

// The task at its current step: what is happening now, or what is waiting for
// the person, with the before/after comparison once there is a result.
const props = defineProps<{ session: Session; runs: Run[]; timeline: Timeline; hasDraft: boolean }>()
const emit = defineEmits<{ changed: []; tag: []; goal: []; tab: [key: string] }>()

const running = computed(() => flowRun(runningRun(props.runs)))
const stage = computed(() => stageOf(props.session, running.value))
const liveRun = computed(() => (running.value ? props.timeline.runs[running.value.id] : undefined))

const pending = computed(() => executable(props.session))
const waiting = computed(() => props.session.planned_ops - props.session.executed_ops)
const undecided = computed(() => Math.max(0, props.session.total_files - props.session.tagged_files))
// A scanned task carries a message only when folders were skipped.
const scanWarning = computed(() => (props.session.status === 'scanned' ? props.session.error_message : ''))

// The latest execution that still has an effect, with its final counters.
const lastExec = computed(() => {
  const r = props.runs.find((x) => x.phase === 'executing' || x.phase === 'revert')
  if (!r || r.phase !== 'executing' || r.status === 'running') return undefined
  return r.status === 'done' || r.status === 'partial' ? r : undefined
})
const finalCounts = useFetch((signal) => {
  const r = lastExec.value
  return r ? loadRunProgress(props.session.id, r.id, signal) : Promise.resolve(undefined)
})
watch(
  () => lastExec.value?.id,
  (runId) => {
    if (runId) void finalCounts.refresh()
  },
  { immediate: true },
)
const execCounts = computed(() => props.timeline.runs[lastExec.value?.id ?? '']?.progress ?? finalCounts.data.value)
const unfinished = computed(() =>
  execCounts.value ? execCounts.value.failed + execCounts.value.skipped > 0 : lastExec.value?.status === 'partial',
)
const execTitle = computed(() => {
  const c = execCounts.value
  if (unfinished.value && c) return `有 ${fmtNumber(c.failed + c.skipped)} 项没有成功`
  if (unfinished.value) return '有部分没有成功'
  return stage.value === 'done' ? '整理完成' : '已执行一部分'
})
const lastRevert = computed(() => {
  const r = props.runs.find((x) => x.phase === 'executing' || x.phase === 'revert')
  return r?.phase === 'revert' && r.status === 'done' ? r : undefined
})

const { reverting, revert } = useRevert(() => emit('changed'))

const { query, set } = useRouteQuery({ bp: '', ap: '', focus: '' })
const rev = ref(0)
watch(
  () => [props.session.planned_ops, props.session.review_count, props.session.executed_ops],
  () => rev.value++,
)
function changed() {
  rev.value++
  emit('changed')
}

const review = ref<{ $el: HTMLElement } | null>(null)
watch(
  () => [query.value.focus, review.value] as const,
  async ([focus, target]) => {
    if (focus !== 'review' || !target) return
    await nextTick()
    target.$el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    set({ focus: '' })
  },
  { immediate: true, flush: 'post' },
)

const editing = ref<TargetEntry | null>(null)
const keeping = ref(false)
function edit(it: PreviewItem) {
  if (hasId(it.file_id)) editing.value = { id: it.file_id, original_path: it.original_path ?? '', new_path: it.path }
}
async function keep(it: PreviewItem) {
  if (!hasId(it.file_id) || keeping.value) return
  keeping.value = true
  try {
    await updateFileTarget(it.file_id, '')
    toast.success('已设为保持原位')
    changed()
  } catch (e) {
    toast.error('保存失败', e)
  } finally {
    keeping.value = false
  }
}

const hasResult = computed(() => props.session.planned_ops > 0)
</script>

<template>
  <div class="page">
    <Alert v-if="scanWarning" tone="warning" title="部分文件夹没有扫描">{{ scanWarning }}</Alert>

    <section v-if="stage === 'scanning'" class="card" aria-label="扫描">
      <div class="card-head">
        <div>
          <h3 class="with-icon"><Loading inline label="" :delay="0" />正在扫描文件</h3>
          <p class="card-desc tabular">已找到 {{ fmtNumber(session.scanned_count) }} 个</p>
        </div>
      </div>
      <p v-if="session.current_path" class="text-sm text-muted truncate">{{ session.current_path }}</p>
    </section>

    <Alert v-else-if="stage === 'scan_failed'" tone="danger" title="扫描失败">{{ session.error_message || '—' }}</Alert>
    <Alert v-else-if="stage === 'scan_interrupted'" tone="warning" title="扫描中断">
      {{ session.error_message || '扫描没有完成' }}
    </Alert>

    <template v-else-if="liveRun">
      <RunActivity :run="liveRun" :mode="running?.exec_mode || undefined" />
    </template>
    <Loading v-else-if="running" />

    <section v-else-if="stage === 'ready'" class="card" aria-label="准备整理">
      <div class="card-head">
        <div>
          <h3>扫描完成，共 {{ fmtNumber(session.total_files) }} 项</h3>
          <p class="card-desc tabular">{{ fmtBytes(session.total_size) }}</p>
        </div>
      </div>
      <div class="goal">
        <span class="field-label">整理要求</span>
        <p v-if="session.goal" class="goal-text">{{ session.goal }}</p>
        <p v-else class="goal-text text-muted">还没有整理要求</p>
        <button type="button" class="ghost btn-sm" @click="emit('goal')">
          <Icon name="pencil" :size="14" />{{ session.goal ? '修改' : '填写' }}
        </button>
      </div>
      <p v-if="hasDraft" class="text-sm">
        已写好整理方案。
        <button type="button" class="ghost btn-sm" @click="emit('tab', 'plan')">查看方案</button>
      </p>
    </section>

    <EmptyState
      v-else-if="stage === 'no_moves'"
      icon="check-circle"
      title="没有需要移动的文件"
      hint="现在的位置都合适，可以修改整理要求后再整理一次。"
    >
      <button type="button" @click="emit('goal')">修改整理要求</button>
    </EmptyState>

    <section v-if="!running && lastExec" class="card" aria-label="执行结果">
      <div class="card-head">
        <div>
          <h3 class="with-icon">
            <Icon
              :name="unfinished ? 'alert-triangle' : 'check-circle'"
              :class="unfinished ? 'text-danger' : 'text-success'"
            />
            {{ execTitle }}
          </h3>
          <p v-if="execCounts" class="card-desc tabular">
            已{{ fmtLabel(EXEC_MODE, lastExec.exec_mode) }} {{ fmtNumber(execCounts.success) }} 项<template
              v-if="execCounts.failed"
              >，失败 {{ fmtNumber(execCounts.failed) }} 项</template
            ><template v-if="execCounts.skipped">，跳过 {{ fmtNumber(execCounts.skipped) }} 项</template>
          </p>
        </div>
        <button type="button" class="btn-sm" :disabled="reverting" @click="revert(lastExec, execCounts?.success)">
          <Icon name="rotate-ccw" :size="14" />{{ reverting ? '撤销中' : '撤销' }}
        </button>
      </div>
      <StreamMarkdown v-if="unfinished && lastExec.summary" :source="lastExec.summary" />
      <p v-else-if="stage !== 'done' && pending" class="text-sm text-muted tabular">
        还有 {{ fmtNumber(pending) }} 项没执行
      </p>
    </section>
    <p v-else-if="!running && lastRevert" class="text-sm text-muted with-icon">
      <Icon name="rotate-ccw" :size="14" />上次执行已撤销
    </p>

    <template v-if="!running && hasResult && stage !== 'scanning'">
      <h3 v-if="stage === 'planned' && !lastExec" class="tabular">助手建议移动 {{ fmtNumber(waiting) }} 项</h3>
      <p v-if="undecided && stage === 'planned'" class="text-sm text-muted tabular">
        还有 {{ fmtNumber(undecided) }} 项助手没有决定
        <button type="button" class="ghost btn-sm" @click="emit('tag')">继续整理</button>
      </p>

      <SessionReview v-if="session.review_count" ref="review" :session-id="session.id" :rev="rev" @changed="changed" />

      <section class="card" aria-label="整理前后对比">
        <div class="card-head">
          <div><h3>整理前后对比</h3></div>
        </div>
        <div class="sides">
          <PreviewPane
            :session-id="session.id"
            side="before"
            :path="query.bp"
            :rev="rev"
            :settled="stage === 'done'"
            @open="set({ bp: $event })"
          />
          <PreviewPane
            :session-id="session.id"
            side="after"
            :path="query.ap"
            :rev="rev"
            :busy="keeping"
            @open="set({ ap: $event })"
            @edit="edit"
            @keep="keep"
          />
        </div>
      </section>
    </template>

    <TargetDialog :entry="editing" @close="editing = null" @saved="changed" />
  </div>
</template>

<style scoped>
.with-icon {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.goal {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.goal-text {
  flex: 1;
  min-width: 0;
}
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
