<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import EmptyState from '@/base/EmptyState.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { fmtBytes, fmtNumber } from '@/base/format'
import { toast } from '@/base/toast'
import { useConfirm } from '@/base/useConfirm'
import { useFetch } from '@/base/useFetch'
import { useRouteQuery } from '@/base/useRouteQuery'
import { updateFileTarget } from '@/api/files'
import { revertRun } from '@/api/sessions'
import type { AgentRun, PreviewItem, ScanSession } from '@/api/types'
import RunActivity from '@/components/RunActivity.vue'
import StreamMarkdown from '@/components/StreamMarkdown.vue'
import { executable, flowRun, runningRun, stageOf } from '@/lib/sessionView'
import type { Timeline } from '@/lib/transcript'
import { loadRunTimeline } from '@/lib/useSessionStream'
import PreviewPane from './PreviewPane.vue'
import SessionReview from './SessionReview.vue'
import TargetDialog, { type TargetEntry } from './TargetDialog.vue'

// The task at its current step: what is happening now, or what is waiting for
// the person, with the before/after comparison once there is a result.
const props = defineProps<{ session: ScanSession; runs: AgentRun[]; timeline: Timeline; hasDraft: boolean }>()
const emit = defineEmits<{ changed: []; tag: []; goal: []; tab: [key: string] }>()
const confirm = useConfirm()

const running = computed(() => flowRun(runningRun(props.runs)))
const stage = computed(() => stageOf(props.session, running.value))
const liveRun = computed(() => (running.value ? props.timeline.runs[running.value.id] : undefined))

const pending = computed(() => executable(props.session))
const waiting = computed(() => props.session.planned_ops - props.session.executed_ops)
const undecided = computed(() => Math.max(0, props.session.total_files - props.session.tagged_files))

// The latest execution that still has an effect, with its final counters.
const lastExec = computed(() => {
  const r = props.runs.find((x) => x.phase === 'executing' || x.phase === 'revert')
  if (!r || r.phase !== 'executing' || r.status === 'running') return undefined
  return r.status === 'done' || r.status === 'partial' ? r : undefined
})
const execView = useFetch(async (signal) => {
  const r = lastExec.value
  if (!r) return undefined
  const t = await loadRunTimeline(props.session.id, r.id, signal)
  return t.runs[r.id]
})
watch(
  () => lastExec.value?.id,
  (runId) => {
    if (runId) void execView.refresh()
  },
  { immediate: true },
)
const execCounts = computed(
  () => props.timeline.runs[lastExec.value?.id ?? 0]?.progress ?? execView.data.value?.progress,
)
const execVerb = computed(() => (lastExec.value?.exec_mode === 'move' ? '移动' : '复制'))
const unfinished = computed(() => !!execCounts.value && execCounts.value.failed + execCounts.value.skipped > 0)
const execTitle = computed(() => {
  const c = execCounts.value
  if (unfinished.value && c) return `有 ${fmtNumber(c.failed + c.skipped)} 项没有成功`
  return stage.value === 'done' ? '整理完成' : '已执行一部分'
})
const problemsOpen = ref(false)
const lastRevert = computed(() => {
  const r = props.runs.find((x) => x.phase === 'executing' || x.phase === 'revert')
  return r?.phase === 'revert' && r.status === 'done' ? r : undefined
})

const reverting = ref(false)
async function revert() {
  const r = lastExec.value
  if (!r || reverting.value) return
  const n = execCounts.value?.success
  const count = n ? ` ${fmtNumber(n)} 项` : '文件'
  const ok = await confirm({
    title: '撤销执行',
    message: r.exec_mode === 'move' ? `把这次移动的${count}放回原处？` : `删除这次复制出的${count}？原文件不受影响。`,
    confirmText: '撤销',
  })
  if (!ok) return
  reverting.value = true
  try {
    await revertRun(r.id)
    toast.success('已开始撤销')
    emit('changed')
  } catch (e) {
    toast.error('撤销失败', e)
  } finally {
    reverting.value = false
  }
}

const { query, set } = useRouteQuery({ bp: '', ap: '' })
const rev = ref(0)
watch(
  () => [props.session.planned_ops, props.session.review_count, props.session.executed_ops],
  () => rev.value++,
)
function changed() {
  rev.value++
  emit('changed')
}

const editing = ref<TargetEntry | null>(null)
const keeping = ref(false)
function edit(it: PreviewItem) {
  if (it.file_id) editing.value = { id: it.file_id, original_path: it.original_path ?? '', new_path: it.path }
}
async function keep(it: PreviewItem) {
  if (!it.file_id || keeping.value) return
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
    <section v-if="stage === 'scanning'" class="card" aria-label="扫描">
      <div class="card-head">
        <div>
          <h3 class="with-icon"><Loading inline label="" :delay="0" />正在扫描文件</h3>
          <p class="card-desc tabular">
            已找到 {{ fmtNumber(session.scanned_count) }} 个，只读取文件信息，不会改动文件
          </p>
        </div>
      </div>
      <p v-if="session.current_path" class="text-sm text-muted truncate">{{ session.current_path }}</p>
    </section>

    <Alert v-else-if="stage === 'scan_failed'" tone="danger" title="扫描失败">{{ session.error_message || '—' }}</Alert>
    <Alert v-else-if="stage === 'scan_interrupted'" tone="warning" title="扫描中断">
      扫描没有完成，重新扫描即可继续
    </Alert>

    <template v-else-if="liveRun">
      <RunActivity :run="liveRun" :mode="running?.exec_mode" />
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
        <p v-else class="text-muted">没有写要求，AI 会按文件类型分类</p>
        <button type="button" class="ghost btn-sm" @click="emit('goal')"><Icon name="pencil" :size="14" />修改</button>
      </div>
      <ol class="next-steps">
        <li><strong>AI 整理</strong>：给每个文件找个新位置，这一步不动文件</li>
        <li><strong>确认结果</strong>：对比整理前后，调整 AI 拿不准的</li>
        <li><strong>执行</strong>：按结果复制或移动文件，之后可以撤销</li>
      </ol>
      <p v-if="hasDraft" class="text-sm">
        AI 已写好整理方案，开始整理时会按方案来。
        <button type="button" class="ghost btn-sm" @click="emit('tab', 'plan')">查看方案</button>
      </p>
    </section>

    <EmptyState
      v-else-if="stage === 'no_moves'"
      icon="check-circle"
      title="没有需要移动的文件"
      hint="AI 认为现在的位置都合适。可以修改整理要求后再整理一次。"
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
          <p class="card-desc tabular">
            <template v-if="execCounts">
              已{{ execVerb }} {{ fmtNumber(execCounts.success) }} 项<template v-if="execCounts.failed"
                >，失败 {{ fmtNumber(execCounts.failed) }} 项</template
              ><template v-if="execCounts.skipped">，跳过 {{ fmtNumber(execCounts.skipped) }} 项</template>。
            </template>
            {{ lastExec.exec_mode === 'move' ? '撤销会把文件放回原处。' : '原文件都还在原处。' }}
          </p>
        </div>
        <button type="button" class="btn-sm" :disabled="reverting" @click="revert">
          <Icon name="rotate-ccw" :size="14" />{{ reverting ? '撤销中' : '撤销' }}
        </button>
      </div>
      <template v-if="unfinished && lastExec.summary">
        <button
          type="button"
          class="ghost btn-sm fold-head"
          :aria-expanded="problemsOpen"
          @click="problemsOpen = !problemsOpen"
        >
          <Icon name="chevron-right" :size="14" class="chev" />查看没成功的文件
        </button>
        <StreamMarkdown v-if="problemsOpen" :source="lastExec.summary" />
      </template>
      <p v-else-if="stage !== 'done' && pending" class="text-sm text-muted tabular">
        还有 {{ fmtNumber(pending) }} 项没执行，去存储里看过效果后，可以继续执行其余的
      </p>
    </section>
    <p v-else-if="!running && lastRevert" class="text-sm text-muted with-icon">
      <Icon name="rotate-ccw" :size="14" />上次执行已撤销
    </p>

    <template v-if="!running && hasResult && stage !== 'scanning'">
      <section v-if="stage === 'planned' && !lastExec" class="summary" aria-label="整理结果">
        <h3 class="tabular">AI 建议移动 {{ fmtNumber(waiting) }} 项</h3>
        <p class="text-muted text-sm">
          下面是整理前后的对比，有不合适的可以改位置或保持原位。确认后点「执行」才会真正改动文件。
        </p>
      </section>
      <p v-if="undecided && stage === 'planned'" class="text-sm text-muted tabular">
        还有 {{ fmtNumber(undecided) }} 项 AI 没有决定
        <button type="button" class="ghost btn-sm" @click="emit('tag')">继续整理</button>
      </p>

      <SessionReview v-if="session.review_count" :session-id="session.id" :rev="rev" @changed="changed" />

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
.next-steps {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding-left: var(--space-5);
  color: var(--text-secondary);
}
.summary {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.fold-head {
  align-self: flex-start;
  gap: 4px;
  color: var(--text-muted);
  font-weight: 400;
}
.chev {
  transition: transform var(--dur-fast) var(--ease);
}
.fold-head[aria-expanded='true'] .chev {
  transform: rotate(90deg);
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
