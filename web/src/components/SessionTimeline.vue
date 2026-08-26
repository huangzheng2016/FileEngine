<template>
  <div class="timeline-wrap">
    <div class="timeline-toolbar">
      <span class="metric-chip" :title="serverTokensTitle">
        <span class="metric-label">{{ $t('logs.totalTokens') }}</span>
        <span class="metric-value">{{ shownTotalTokens.toLocaleString() }}</span>
      </span>
      <!-- Run mainline: latest phase transition + absolute progress counters -->
      <span v-if="runPhase" class="status-chip" :class="phaseChipClass(runPhase.status)">
        <span v-if="runPhase.status === 'started'" class="status-dot"></span>
        {{ phaseLabel(runPhase.phase) }} · {{ runStatusLabel(runPhase.status) }}
      </span>
      <span v-if="progressText" class="metric-chip">
        <span class="metric-value">{{ progressText }}</span>
      </span>
      <span v-if="stream.connected.value" class="status-chip is-live">
        <span class="status-dot"></span>{{ $t('logs.live') }}
      </span>
      <span v-else class="status-chip is-muted">{{ $t('logs.reconnecting') }}</span>
    </div>

    <div v-if="hasMore" class="load-earlier">
      <button class="ghost-button ghost-button--sm" :disabled="loadingHistory" @click="loadEarlier">
        {{ $t('logs.loadEarlier') }}
      </button>
    </div>

    <div v-if="!displayLanes.length && !loadingHistory" class="empty-state-card empty-state-card--inline">
      <div class="empty-state-card__icon">
        <el-icon :size="24"><Monitor /></el-icon>
      </div>
      <div class="empty-state-card__text">
        <h4 class="empty-state-card__title">{{ $t('logs.noLogs') }}</h4>
        <p class="empty-state-card__sub">{{ $t('logs.noLogsHint') }}</p>
      </div>
    </div>

    <!-- Worker lanes: one column per agent worker, each a continuous run log.
         Historical rows (worker_id = 0 everywhere) collapse into a single lane. -->
    <div class="lanes-grid">
      <div v-for="lane in displayLanes" :key="lane.workerId" class="lane">
        <div class="lane-header">
          <span class="lane-title">{{ laneTitle(lane) }}</span>
          <span v-if="lane.currentPull" class="lane-pull cell-time" :title="lane.currentPull">{{ pullLabel(lane.currentPull) }}</span>
          <span v-if="lane.failed" class="status-chip is-danger">{{ $t('logs.failed') }}</span>
          <span v-if="lane.live" class="status-chip is-live"><span class="status-dot"></span>{{ $t('logs.live') }}</span>
          <span class="lane-meta cell-time">{{ $t('logs.stepCount', { count: lane.steps.length }) }}</span>
          <span v-if="lane.tokenSum > 0" class="lane-meta cell-time">{{ lane.tokenSum.toLocaleString() }} tok</span>
        </div>

        <div class="lane-scroll" :ref="el => setLaneRef(lane.workerId, el)" @scroll="onLaneScroll(lane.workerId)">
          <template v-for="step in lane.steps" :key="step.key">
            <!-- Pull marker: one work unit (directory / loose-file pull / instruct) -->
            <div v-if="step.kind === 'pull'" class="pull-marker" :class="{ 'is-failed': !step.pending && (step.failed ?? 0) > 0 }">
              <el-icon v-if="step.pending" class="tool-icon is-spinning"><Loading /></el-icon>
              <el-icon v-else class="tool-icon"><Finished /></el-icon>
              <span class="pull-label" :title="step.label">{{ pullLabel(step.label) }}</span>
              <span v-if="step.pending" class="status-chip is-warning">{{ $t('logs.pullActive') }}</span>
              <span v-else class="pull-result cell-time">{{ $t('logs.pullResult', { tagged: step.tagged ?? 0, failed: step.failed ?? 0 }) }}</span>
              <span v-if="step.time" class="cell-time">{{ formatTime(step.time) }}</span>
            </div>

            <!-- Chat message -->
            <div v-else-if="step.kind === 'message'" class="msg-row" :class="`msg-${step.role}`">
              <div class="msg-meta">
                <span class="status-chip" :class="roleChipClass(step.role)">{{ roleLabel(step.role) }}</span>
                <span v-if="step.time" class="cell-time">{{ formatTime(step.time) }}</span>
                <span v-if="step.total_tokens" class="cell-time">{{ step.prompt_tokens }}↑ {{ step.completion_tokens }}↓ = {{ step.total_tokens }}</span>
              </div>
              <div class="msg-bubble">
                <MarkdownView v-if="step.role === 'assistant'" :content="step.content" />
                <template v-else-if="step.role === 'system'">
                  <div class="msg-text">{{ systemSummary(step.content) }}</div>
                  <details class="sys-details">
                    <summary>{{ $t('logs.techDetails') }}</summary>
                    <pre class="sys-pre">{{ step.content }}</pre>
                  </details>
                </template>
                <div v-else class="msg-text">{{ step.content }}</div>
              </div>
            </div>

            <!-- Tool step -->
            <div v-else class="tool-row">
              <div class="tool-head" @click="toggleStepDetail(step)">
                <el-icon class="tool-icon" :class="{ 'is-spinning': step.pending }">
                  <Loading v-if="step.pending" />
                  <Tools v-else />
                </el-icon>
                <code class="tool-name">{{ step.tool_name }}</code>
                <span v-if="step.pending" class="status-chip is-warning">{{ $t('logs.toolRunning') }}</span>
                <span v-else-if="step.ok === true" class="status-chip is-live">{{ $t('logs.toolOk') }}</span>
                <span v-else-if="step.ok === false" class="status-chip is-danger">{{ $t('logs.toolFailed') }}</span>
                <span v-if="step.time" class="cell-time">{{ formatTime(step.time) }}</span>
                <span class="tool-toggles">
                  <button v-if="step.input" class="link-button" @click.stop="toggleStepDetail(step, 'input')">
                    {{ $t('logs.input') }} {{ stepDetail[step.key + '_input'] ? '▾' : '▸' }}
                  </button>
                  <button v-if="step.output" class="link-button" @click.stop="toggleStepDetail(step, 'output')">
                    {{ $t('logs.output') }} {{ stepDetail[step.key + '_output'] ? '▾' : '▸' }}
                  </button>
                </span>
              </div>
              <pre v-if="stepDetail[step.key + '_input']" class="tool-pre">{{ prettyJSON(step.key, 'input', step.input!) }}</pre>
              <pre v-if="stepDetail[step.key + '_output']" class="tool-pre tool-pre--output">{{ prettyJSON(step.key, 'output', step.output!) }}</pre>
            </div>
          </template>
        </div>
      </div>
    </div>

    <!-- Scroll-to-latest -->
    <transition name="fade">
      <button v-if="anyUnstuck && displayLanes.length" class="scroll-latest" @click="scrollAllToBottom(true)">
        <el-icon :size="14"><Bottom /></el-icon>{{ $t('logs.scrollToLatest') }}
      </button>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { listLogs } from '../api'
import type { AgentLog, ProgressEvent } from '../types'
import { useSessionEventStream } from '../composables/sessionEvents'
import MarkdownView from './MarkdownView.vue'

const props = withDefaults(defineProps<{
  sessionId: number
  /** Server-side session token total (single source of truth); when provided
      the toolbar shows it instead of the sum over locally loaded pages. */
  totalTokensServer?: number | null
}>(), { totalTokensServer: null })

const emit = defineEmits<{
  /** A run reached its terminal event — parent should refresh session counters. */
  (e: 'runDone'): void
  /** Stream saw a phase start/done transition — parent may refresh the status chip. */
  (e: 'runStatus'): void
}>()

const { t } = useI18n()

// ===== Steps & lanes =====
interface MessageStep {
  kind: 'message'
  key: string
  role: string
  content: string
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
  time?: string
}
interface ToolStep {
  kind: 'tool'
  key: string
  tool_name: string
  input?: string
  output?: string
  ok?: boolean
  pending?: boolean
  time?: string
}
interface PullStep {
  kind: 'pull'
  key: string
  label: string
  count?: number
  cursor?: number
  tagged?: number
  failed?: number
  pending: boolean
  time?: string
}
type Step = MessageStep | ToolStep | PullStep

interface Lane {
  workerId: number
  steps: Step[]
  tokenSum: number
  live: boolean
  failed: boolean
  currentPull?: string
}

let stepSeq = 0
const nextKey = () => `s${++stepSeq}`

const histLogs = ref<AgentLog[]>([]) // ascending by id
const liveSteps = reactive(new Map<number, Step[]>()) // per worker lane
const stepDetail = reactive<Record<string, boolean>>({})
const jsonCache = new Map<string, string>()
const loadingHistory = ref(false)
const totalLogs = ref(0)
const loadedPages = ref(0)
const pageSize = 50
const laneEls = new Map<number, HTMLElement>()
const laneStick = reactive(new Map<number, boolean>())
// Run mainline state (live only — historical sessions show counters in the
// session run-card above the timeline instead).
const runPhase = ref<{ phase: string; status: string } | null>(null)
const progressInfo = ref<ProgressEvent | null>(null)

const stream = useSessionEventStream()
let skipFirstOpen = true
let historyReloadTimer: ReturnType<typeof setTimeout> | null = null

const hasMore = computed(() => histLogs.value.length < totalLogs.value)

const displayLanes = computed<Lane[]>(() => {
  const map = new Map<number, Lane>()
  const ensure = (wid: number) => {
    let l = map.get(wid)
    if (!l) {
      l = { workerId: wid, steps: [], tokenSum: 0, live: false, failed: false }
      map.set(wid, l)
    }
    return l
  }
  for (const log of histLogs.value) {
    const l = ensure(log.worker_id || 0)
    if (log.role === 'tool') {
      l.steps.push({
        kind: 'tool', key: `h${log.id}`, tool_name: log.tool_name,
        input: log.tool_input || undefined, output: log.tool_output || undefined,
        time: log.created_at,
      })
    } else {
      l.steps.push({
        kind: 'message', key: `h${log.id}`, role: log.role, content: log.content,
        prompt_tokens: log.prompt_tokens || undefined,
        completion_tokens: log.completion_tokens || undefined,
        total_tokens: log.total_tokens || undefined,
        time: log.created_at,
      })
    }
    l.tokenSum += log.total_tokens || 0
  }
  for (const [wid, steps] of liveSteps) {
    const l = ensure(wid)
    l.steps.push(...steps)
    l.live = true
    l.tokenSum += steps.reduce((sum, s) => sum + (s.kind === 'message' ? (s.total_tokens || 0) : 0), 0)
  }
  const lanes = [...map.values()].sort((a, b) => a.workerId - b.workerId)
  for (const l of lanes) {
    l.failed = l.steps.some(s =>
      (s.kind === 'message' && s.role === 'system') ||
      (s.kind === 'pull' && !s.pending && (s.failed ?? 0) > 0))
    const open = l.steps.filter(s => s.kind === 'pull' && s.pending).pop() as PullStep | undefined
    l.currentPull = open?.label
  }
  return lanes
})

const totalTokens = computed(() => displayLanes.value.reduce((sum, l) => sum + l.tokenSum, 0))

// Token口径统一：优先服务端会话累计（含未加载页），否则退回本地已加载合计。
const shownTotalTokens = computed(() => props.totalTokensServer ?? totalTokens.value)
const serverTokensTitle = computed(() =>
  props.totalTokensServer != null
    ? t('logs.loadedTokens', { count: totalTokens.value.toLocaleString() })
    : undefined)

const anyUnstuck = computed(() => displayLanes.value.some(l => !isLaneSticky(l.workerId)))

const progressText = computed(() => {
  const p = progressInfo.value
  if (!p) return ''
  if (p.phase === 'tagging') {
    return t('logs.progressTagging', { tagged: p.tagged ?? 0, total: p.total, planned: p.planned ?? 0 })
  }
  return t('logs.progressExecuting', { success: p.success ?? 0, failed: p.failed ?? 0, skipped: p.skipped ?? 0, total: p.total })
})

// ===== Lifecycle =====
// Handlers are registered ONCE per stream instance — open(id) only re-points
// the EventSource URL. Re-registering on every init() stacked 7 callbacks per
// session switch and rendered every live event N times (review P1-1).
stream
  .onOpen(() => {
    // First open follows the initial history load; later (re)opens mean we
    // may have missed events — no replay exists, so refetch history.
    if (skipFirstOpen) { skipFirstOpen = false; return }
    scheduleHistoryReload()
  })
  .on('pull_begin', (e) => {
    pushLive(e.worker_id, {
      kind: 'pull', key: nextKey(), label: e.label, count: e.count, cursor: e.cursor,
      pending: true, time: new Date().toISOString(),
    })
  })
  .on('pull_done', (e) => {
    const steps = liveSteps.get(e.worker_id)
    const marker = steps?.filter(s => s.kind === 'pull' && s.pending).pop() as PullStep | undefined
    if (marker) {
      marker.tagged = e.tagged
      marker.failed = e.failed
      marker.pending = false
    } else {
      pushLive(e.worker_id, {
        kind: 'pull', key: nextKey(), label: e.label || '', tagged: e.tagged, failed: e.failed,
        pending: false, time: new Date().toISOString(),
      })
    }
    afterAppend(e.worker_id)
  })
  .on('message', (e) => {
    pushLive(e.worker_id, {
      kind: 'message', key: nextKey(), role: e.role, content: e.content,
      prompt_tokens: e.prompt_tokens, completion_tokens: e.completion_tokens,
      total_tokens: e.total_tokens, time: new Date().toISOString(),
    })
  })
  .on('tool_call', (e) => {
    pushLive(e.worker_id, {
      kind: 'tool', key: nextKey(), tool_name: e.tool_name, input: e.input, pending: true,
      time: new Date().toISOString(),
    })
  })
  .on('tool_result', (e) => {
    const steps = liveSteps.get(e.worker_id)
    const pending = steps?.filter(s => s.kind === 'tool' && s.pending && s.tool_name === e.tool_name).pop() as ToolStep | undefined
    if (pending) {
      pending.output = e.output
      pending.ok = e.ok
      pending.pending = false
    } else {
      pushLive(e.worker_id, {
        kind: 'tool', key: nextKey(), tool_name: e.tool_name, output: e.output, ok: e.ok,
        time: new Date().toISOString(),
      })
    }
    afterAppend(e.worker_id)
  })
  .on('run_status', (e) => {
    runPhase.value = { phase: e.phase, status: e.status }
    emit('runStatus')
  })
  .on('progress', (p) => { progressInfo.value = p })
  .on('run_done', () => {
    // Terminal event: persisted rows (with token counts) are now complete.
    emit('runDone')
    scheduleHistoryReload()
  })

onMounted(async () => {
  if (!props.sessionId) return
  await init()
})

watch(() => props.sessionId, async (id, oldId) => {
  if (!id || id === oldId) return
  await init()
})

onUnmounted(() => {
  if (historyReloadTimer) clearTimeout(historyReloadTimer)
})

async function init() {
  // Drop any pending history refill from the previous session — it would
  // fire with the NEW sessionId and wipe its live steps (review P2-2).
  if (historyReloadTimer) { clearTimeout(historyReloadTimer); historyReloadTimer = null }
  stream.close()
  histLogs.value = []
  liveSteps.clear()
  laneStick.clear()
  jsonCache.clear()
  Object.keys(stepDetail).forEach(k => delete stepDetail[k])
  loadedPages.value = 0
  totalLogs.value = 0
  runPhase.value = null
  progressInfo.value = null
  skipFirstOpen = true
  await loadLatest()
  openStream()
}

async function loadLatest() {
  if (!props.sessionId) return
  loadingHistory.value = true
  try {
    const res = await listLogs({ session_id: props.sessionId, page: 1, page_size: pageSize, order: 'desc' })
    const rows = (res.data.logs || []).slice().reverse() // chronological
    histLogs.value = rows
    totalLogs.value = res.data.total
    loadedPages.value = 1
    await nextTick()
    scrollAllToBottom(false)
  } finally {
    loadingHistory.value = false
  }
}

async function loadEarlier() {
  if (!props.sessionId || loadingHistory.value || !hasMore.value) return
  loadingHistory.value = true
  const prev = new Map<number, { h: number; t: number }>()
  for (const [id, el] of laneEls) prev.set(id, { h: el.scrollHeight, t: el.scrollTop })
  try {
    const res = await listLogs({ session_id: props.sessionId, page: loadedPages.value + 1, page_size: pageSize, order: 'desc' })
    const rows = (res.data.logs || []).slice().reverse()
    histLogs.value = [...rows, ...histLogs.value]
    loadedPages.value += 1
    await nextTick()
    for (const [id, el] of laneEls) {
      const p = prev.get(id)
      if (p) el.scrollTop = el.scrollHeight - p.h + p.t
    }
  } finally {
    loadingHistory.value = false
  }
}

function openStream() {
  if (!props.sessionId) return
  stream.open(props.sessionId)
}

function pushLive(workerId: number, step: Step) {
  if (!liveSteps.has(workerId)) liveSteps.set(workerId, [])
  liveSteps.get(workerId)!.push(step)
  afterAppend(workerId)
}

function afterAppend(workerId: number) {
  nextTick(() => { if (isLaneSticky(workerId)) scrollLaneToBottom(workerId, false) })
}

/** Refetch the newest history page and drop live steps now covered by it. */
function scheduleHistoryReload() {
  if (historyReloadTimer) clearTimeout(historyReloadTimer)
  historyReloadTimer = setTimeout(async () => {
    historyReloadTimer = null
    const liveTotal = [...liveSteps.values()].reduce((n, arr) => n + arr.length, 0)
    const size = Math.max(pageSize, Math.ceil((liveTotal + 10) / pageSize) * pageSize)
    const res = await listLogs({ session_id: props.sessionId, page: 1, page_size: size, order: 'desc' }).catch(() => null)
    if (!res) return // keep live steps; a later reconnect will retry the refill
    histLogs.value = (res.data.logs || []).slice().reverse()
    totalLogs.value = res.data.total
    loadedPages.value = 1
    liveSteps.clear()
    jsonCache.clear()
    await nextTick()
    for (const l of displayLanes.value) {
      if (isLaneSticky(l.workerId)) scrollLaneToBottom(l.workerId, false)
    }
  }, 600)
}

// ===== Lane scrolling =====
function setLaneRef(workerId: number, el: any) {
  if (el) laneEls.set(workerId, el as HTMLElement)
  else laneEls.delete(workerId)
}

function isLaneSticky(workerId: number): boolean {
  return laneStick.get(workerId) ?? true
}

function onLaneScroll(workerId: number) {
  const el = laneEls.get(workerId)
  if (!el) return
  laneStick.set(workerId, el.scrollHeight - el.scrollTop - el.clientHeight < 120)
}

function scrollLaneToBottom(workerId: number, smooth: boolean) {
  const el = laneEls.get(workerId)
  if (!el) return
  el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
  laneStick.set(workerId, true)
}

function scrollAllToBottom(smooth: boolean) {
  for (const l of displayLanes.value) scrollLaneToBottom(l.workerId, smooth)
}

// ===== View helpers =====
function toggleStepDetail(step: ToolStep, part?: 'input' | 'output') {
  const target = part ?? (step.input ? 'input' : 'output')
  stepDetail[step.key + '_' + target] = !stepDetail[step.key + '_' + target]
}

function prettyJSON(key: string, part: string, raw: string): string {
  const cacheKey = `${key}_${part}`
  if (jsonCache.has(cacheKey)) return jsonCache.get(cacheKey)!
  let result: string
  try { result = JSON.stringify(JSON.parse(raw), null, 2) } catch { result = raw }
  jsonCache.set(cacheKey, result)
  return result
}

function laneTitle(lane: Lane): string {
  if (displayLanes.value.length === 1) return t('logs.singleLane')
  return t('logs.worker', { n: lane.workerId + 1 })
}

/** Backend labels loose-file pulls in English ("10 files") — localize. */
function pullLabel(label: string): string {
  const m = /^(\d+) files$/.exec(label)
  if (m) return t('logs.filesCount', { count: Number(m[1]) })
  if (label === 'instruct') return t('logs.phaseInstruct')
  return label
}

function phaseLabel(phase: string): string {
  const map: Record<string, string> = {
    tagging: t('logs.phaseTagging'),
    instruct: t('logs.phaseInstruct'),
    executing: t('logs.phaseExecuting'),
  }
  return map[phase] || phase
}

function runStatusLabel(status: string): string {
  const map: Record<string, string> = {
    started: t('logs.runStarted'),
    done: t('logs.runDone'),
    error: t('logs.runError'),
    stopped: t('logs.runStopped'),
  }
  return map[status] || status
}

function phaseChipClass(status: string): string {
  const map: Record<string, string> = { started: 'is-live', done: 'is-muted', error: 'is-danger', stopped: 'is-warning' }
  return map[status] || 'is-muted'
}

/** First-line Chinese conclusion for a persisted system error dump; the raw
    dump stays available behind 技术详情. */
function systemSummary(content: string): string {
  const tool = /failed to invoke tool\[name:([^\s\]]+)/.exec(content)
  const errLine = content.split('\n')
    .map(l => l.trim())
    .find(l => l.startsWith('err='))
  const reason = errLine ? errLine.slice(4) : content.split('\n')[0]
  const short = reason.length > 200 ? reason.slice(0, 200) + '…' : reason
  if (tool) return t('logs.toolRunError', { tool: tool[1], reason: short })
  return short
}

function roleLabel(role: string): string {
  const map: Record<string, string> = { user: t('logs.roleUser'), assistant: t('logs.roleAssistant'), system: t('logs.roleSystem') }
  return map[role] || role
}

function roleChipClass(role: string): string {
  const map: Record<string, string> = { user: 'is-accent', assistant: 'is-live', system: 'is-muted' }
  return map[role] || 'is-muted'
}

function formatTime(tstr: string) {
  if (!tstr) return ''
  // Unified site-wide: local 24h with date (sv-SE renders ISO-style).
  return new Date(tstr).toLocaleString('sv-SE')
}
</script>

<style scoped>
.timeline-wrap {
  flex: 1 1 0;
  min-height: 0;
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.timeline-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.load-earlier {
  display: flex;
  justify-content: center;
  flex-shrink: 0;
}

/* ===== Worker lanes ===== */
.lanes-grid {
  flex: 1 1 0;
  min-height: 0;
  display: grid;
  /* Default ≈3 parallel lanes on wide screens; the worker count adapts —
     fewer workers stretch, more wrap onto the next row. */
  grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
  gap: 12px;
  padding-right: 4px;
  overflow-y: auto;
}

.lane {
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-elevated);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}

.lane-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--border-soft);
  flex-shrink: 0;
}

.lane-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  flex-shrink: 0;
}

.lane-pull {
  font-size: 11.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
}

.lane-meta {
  flex-shrink: 0;
  font-size: 11.5px;
  margin-left: auto;
}

.lane-meta + .lane-meta {
  margin-left: 0;
}

.lane-scroll {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 14px 14px;
}

/* ===== Pull markers ===== */
.pull-marker {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  border: 1px dashed var(--border-soft);
  border-radius: var(--radius-sm);
  background: var(--surface-soft);
  font-size: 12px;
}

.pull-marker.is-failed {
  border-color: var(--danger);
  background: var(--danger-soft, rgba(220, 38, 38, 0.08));
}

.pull-label {
  font-family: var(--font-mono);
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
}

.pull-result {
  flex-shrink: 0;
  font-size: 11.5px;
}

.pull-marker .cell-time {
  font-size: 11px;
  flex-shrink: 0;
}

/* ===== Chat bubbles ===== */
.msg-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 92%;
}

.msg-user {
  align-self: flex-end;
  align-items: flex-end;
}

.msg-assistant,
.msg-system {
  align-self: flex-start;
  align-items: flex-start;
}

.msg-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}

.msg-meta .cell-time {
  font-size: 11px;
}

.msg-bubble {
  padding: 10px 14px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border-soft);
  min-width: 0;
}

.msg-user .msg-bubble {
  background: var(--accent-soft);
  border-color: var(--floating-border);
  border-bottom-right-radius: var(--radius-xs);
}

.msg-assistant .msg-bubble {
  background: var(--surface-strong);
  border-bottom-left-radius: var(--radius-xs);
}

.msg-system .msg-bubble {
  background: var(--surface-muted);
}

.msg-text {
  font-size: 13.5px;
  line-height: 1.6;
  color: var(--text-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

/* ===== Tool steps ===== */
.tool-row {
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-sm);
  background: var(--surface-soft);
  overflow: hidden;
}

.tool-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 12px;
  cursor: pointer;
  user-select: none;
}

.tool-head:hover {
  background: var(--accent-soft);
}

.tool-icon {
  color: var(--text-muted);
  flex-shrink: 0;
}

.tool-icon.is-spinning {
  animation: tool-spin 1.2s linear infinite;
  color: var(--warning);
}

@keyframes tool-spin {
  to { transform: rotate(360deg); }
}

.tool-name {
  font-family: var(--font-mono);
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-primary);
}

.tool-head .cell-time {
  font-size: 11px;
}

.tool-toggles {
  margin-left: auto;
  display: flex;
  gap: 2px;
}

.tool-pre {
  margin: 0;
  padding: 10px 12px;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.55;
  color: var(--text-secondary);
  background: var(--surface-strong);
  border-top: 1px solid var(--border-soft);
  overflow-x: auto;
  max-height: 260px;
  overflow-y: auto;
  white-space: pre-wrap;
  word-break: break-all;
}

.sys-details {
  margin-top: 6px;
}

.sys-details summary {
  font-size: 12px;
  color: var(--text-muted);
  cursor: pointer;
  user-select: none;
}

.sys-pre {
  margin: 6px 0 0;
  padding: 8px 10px;
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--text-secondary);
  background: var(--surface-strong);
  border-radius: var(--radius-xs);
  max-height: 200px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
}

/* ===== Floating scroll button ===== */
.scroll-latest {
  position: absolute;
  right: 24px;
  bottom: 16px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 14px;
  min-height: 34px;
  font-family: var(--font-display);
  font-size: 12.5px;
  font-weight: 600;
  color: var(--accent);
  background: var(--surface-strong);
  border: 1px solid var(--floating-border);
  border-radius: var(--radius-pill);
  box-shadow: var(--shadow-floating);
  backdrop-filter: blur(14px);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-spring);
}

.scroll-latest:hover {
  transform: translateY(-1px);
  background: var(--accent-soft);
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity var(--duration-fast) var(--ease-spring);
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
