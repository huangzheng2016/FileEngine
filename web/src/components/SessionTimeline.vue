<template>
  <div class="timeline-wrap">
    <div class="timeline-toolbar">
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
      <!-- Stats sit right; run state and live indicators take priority on the left -->
      <span class="metric-chip metric-chip--right" :title="serverTokensTitle">
        <span class="metric-label">{{ $t('logs.totalTokens') }}</span>
        <span class="metric-value">{{ shownTotalTokens.toLocaleString() }}</span>
      </span>
    </div>

    <div v-if="!visibleLanes.length && !loadingHistory" class="empty-state-card empty-state-card--inline">
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
      <div v-for="lane in visibleLanes" :key="lane.workerId" class="lane">
        <div class="lane-header">
          <span class="lane-title">{{ laneTitle(lane) }}</span>
          <span v-if="lane.currentPull" class="lane-pull cell-time" :title="lane.currentPull">{{ pullLabel(lane.currentPull) }}</span>
          <span v-if="lane.failed" class="status-chip is-danger">{{ $t('logs.failed') }}</span>
          <span v-if="lane.live" class="status-chip is-live"><span class="status-dot"></span>{{ $t('logs.live') }}</span>
          <span class="lane-meta cell-time">{{ $t('logs.stepCount', { count: lane.steps.length }) }}</span>
          <span v-if="lane.tokenSum > 0" class="lane-meta cell-time">{{ lane.tokenSum.toLocaleString() }} tok</span>
          <button
            v-if="displayLanes.length > 1"
            class="ghost-button ghost-button--sm lane-solo"
            :title="soloWorker === lane.workerId ? $t('logs.allLanes') : $t('logs.soloLane')"
            @click="toggleSolo(lane.workerId)"
          >
            <el-icon :size="13"><ScaleToOriginal v-if="soloWorker === lane.workerId" /><FullScreen v-else /></el-icon>
          </button>
        </div>

        <div v-if="laneLoadingEarlier.has(lane.workerId)" class="lane-loading" role="status" :aria-label="$t('logs.laneLoading')"><span></span></div>
        <div class="lane-scroll" :ref="el => setLaneRef(lane.workerId, el)" @scroll="onLaneScroll(lane.workerId)">
          <div v-if="laneExhausted.has(lane.workerId) && lane.steps.length" class="lane-nomore">{{ $t('logs.laneNoMore') }}</div>
          <div v-if="!lane.steps.length" class="lane-idle">{{ $t('logs.workerIdle') }}</div>
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
                <span v-if="step.total_tokens" class="cell-time">{{ step.prompt_tokens }}↑<template v-if="step.cached_tokens">({{ $t('logs.cachedTokens', { count: step.cached_tokens }) }})</template> {{ step.completion_tokens }}↓ = {{ step.total_tokens }}</span>
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

        <!-- Lane-level back-to-latest: floats inside the lane, only while unstuck -->
        <transition name="fade">
          <button v-if="!isLaneSticky(lane.workerId)" class="lane-latest" @click="scrollLaneToBottom(lane.workerId)">
            <el-icon :size="13"><Bottom /></el-icon>{{ $t('logs.backToLatest') }}
            <span v-if="unreadCount(lane.workerId)" class="lane-latest__badge">{{ unreadCount(lane.workerId) }}</span>
          </button>
        </transition>
      </div>
    </div>
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
  cached_tokens?: number
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

const laneLogs = reactive(new Map<number, AgentLog[]>()) // per worker, ascending by id
const liveSteps = reactive(new Map<number, Step[]>()) // per worker lane
// Every worker_id ever seen this session — lanes stay mounted even when a
// worker goes quiet between pulls, so the grid stops flickering.
const seenWorkers = reactive(new Set<number>())
const soloWorker = ref<number | null>(null) // null = all lanes, otherwise only this one
const stepDetail = reactive<Record<string, boolean>>({})
const jsonCache = new Map<string, string>()
const loadingHistory = ref(false)
// Lane-level history pagination state (infinite scroll-up per lane).
const laneLoadingEarlier = reactive(new Set<number>())
const laneExhausted = reactive(new Set<number>()) // no earlier rows for this worker
const laneUnread = reactive(new Map<number, number>()) // appends while unstuck
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
const laneEarlierTimers = new Map<number, ReturnType<typeof setTimeout>>()

/** Client-side double-check for the lane contract (worker_id filter lands with
    the backend follow-up; harmless once the server filters too). */
const laneRow = (log: AgentLog, wid: number) => (log.worker_id || 0) === wid

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
  for (const [wid, logs] of laneLogs) {
    const l = ensure(wid)
    for (const log of logs) {
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
          cached_tokens: log.cached_tokens || undefined,
          completion_tokens: log.completion_tokens || undefined,
          total_tokens: log.total_tokens || undefined,
          time: log.created_at,
        })
      }
      l.tokenSum += log.total_tokens || 0
    }
  }
  // Workers seen before but silent right now still get a (header-only) lane.
  for (const wid of seenWorkers) ensure(wid)
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

// Solo view: null shows every lane; a worker id shows only that lane, which
// then stretches to full grid width via auto-fit.
const visibleLanes = computed<Lane[]>(() =>
  soloWorker.value == null
    ? displayLanes.value
    : displayLanes.value.filter(l => l.workerId === soloWorker.value))

// Token口径统一：优先服务端会话累计（含未加载页），否则退回本地已加载合计。
const shownTotalTokens = computed(() => props.totalTokensServer ?? totalTokens.value)
const serverTokensTitle = computed(() =>
  props.totalTokensServer != null
    ? t('logs.loadedTokens', { count: totalTokens.value.toLocaleString() })
    : undefined)

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
      prompt_tokens: e.prompt_tokens, cached_tokens: e.cached_tokens, completion_tokens: e.completion_tokens,
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
  for (const timer of laneEarlierTimers.values()) clearTimeout(timer)
  laneEarlierTimers.clear()
})

async function init() {
  // Drop any pending history refill from the previous session — it would
  // fire with the NEW sessionId and wipe its live steps (review P2-2).
  if (historyReloadTimer) { clearTimeout(historyReloadTimer); historyReloadTimer = null }
  for (const timer of laneEarlierTimers.values()) clearTimeout(timer)
  laneEarlierTimers.clear()
  stream.close()
  laneLogs.clear()
  liveSteps.clear()
  seenWorkers.clear()
  soloWorker.value = null
  laneStick.clear()
  laneLoadingEarlier.clear()
  laneExhausted.clear()
  laneUnread.clear()
  jsonCache.clear()
  Object.keys(stepDetail).forEach(k => delete stepDetail[k])
  runPhase.value = null
  progressInfo.value = null
  skipFirstOpen = true
  await loadLatest()
  openStream()
}

// Initial load: one global newest page discovers recently active workers,
// then each discovered worker gets its own newest-page baseline (parallel).
// Workers silent in the newest global page appear via live events later —
// same initial visibility as the old global paging, but lanes paginate
// independently from here on.
async function loadLatest() {
  if (!props.sessionId) return
  loadingHistory.value = true
  try {
    const res = await listLogs({ session_id: props.sessionId, page: 1, page_size: pageSize, order: 'desc' })
    const wids = [...new Set((res.data.logs || []).map(log => log.worker_id || 0))]
    for (const wid of wids) seenWorkers.add(wid)
    await Promise.all(wids.map(wid => loadLaneLatest(wid)))
    await nextTick()
    for (const l of visibleLanes.value) scrollLaneToBottom(l.workerId)
  } finally {
    loadingHistory.value = false
  }
}

/** Newest page of one worker's own history; short page means history exhausted. */
async function loadLaneLatest(wid: number) {
  const res = await listLogs({ session_id: props.sessionId, worker_id: wid, page: 1, page_size: pageSize, order: 'desc' })
  const rows = (res.data.logs || []).filter(log => laneRow(log, wid)).slice().reverse()
  laneLogs.set(wid, rows)
  if (rows.length) seenWorkers.add(wid)
  if (rows.length < pageSize) laneExhausted.add(wid)
  else laneExhausted.delete(wid)
}

// Lane-level infinite scroll-up: fetch rows older than this lane's oldest
// loaded id (before_id contract) and prepend, keeping the viewport anchored
// on the previously topmost step. Lanes paginate fully independently.
async function loadLaneEarlier(wid: number) {
  if (!props.sessionId || laneLoadingEarlier.has(wid) || laneExhausted.has(wid)) return
  const current = laneLogs.get(wid) || []
  const oldestId = current[0]?.id
  laneLoadingEarlier.add(wid)
  const el = laneEls.get(wid)
  const prev = el ? { h: el.scrollHeight, t: el.scrollTop } : null
  try {
    const res = await listLogs({
      session_id: props.sessionId, worker_id: wid,
      ...(oldestId != null ? { before_id: oldestId } : {}),
      page: 1, page_size: pageSize, order: 'desc',
    })
    const raw = res.data.logs || []
    const fresh = raw
      .filter(log => laneRow(log, wid) && (oldestId == null || log.id < oldestId))
      .slice().reverse()
    if (fresh.length > 0) {
      laneLogs.set(wid, [...fresh, ...current])
    }
    if (raw.length < pageSize || fresh.length === 0) laneExhausted.add(wid)
    await nextTick()
    // Anchor on the previously topmost visible step — viewport must not jump.
    if (el && prev && fresh.length > 0) el.scrollTop = el.scrollHeight - prev.h + prev.t
  } finally {
    laneLoadingEarlier.delete(wid)
  }
}

function scheduleLaneEarlier(wid: number) {
  if (laneEarlierTimers.has(wid) || laneLoadingEarlier.has(wid) || laneExhausted.has(wid)) return
  laneEarlierTimers.set(wid, setTimeout(() => {
    laneEarlierTimers.delete(wid)
    const el = laneEls.get(wid)
    if (el && el.scrollTop < 20) loadLaneEarlier(wid)
  }, 200))
}

function openStream() {
  if (!props.sessionId) return
  stream.open(props.sessionId)
}

function pushLive(workerId: number, step: Step) {
  if (!liveSteps.has(workerId)) liveSteps.set(workerId, [])
  liveSteps.get(workerId)!.push(step)
  seenWorkers.add(workerId)
  afterAppend(workerId)
}

function afterAppend(workerId: number) {
  nextTick(() => {
    if (isLaneSticky(workerId)) scrollLaneToBottom(workerId)
    else laneUnread.set(workerId, (laneUnread.get(workerId) ?? 0) + 1)
  })
}

/** Per-worker refill: fetch each lane's own newest page (sized to cover its
    live steps, now persisted) and drop those live steps. Unstuck lanes keep
    their scrollTop — a reload must not yank the reader back to the bottom. */
function scheduleHistoryReload() {
  if (historyReloadTimer) clearTimeout(historyReloadTimer)
  historyReloadTimer = setTimeout(async () => {
    historyReloadTimer = null
    const wids = [...new Set([...laneLogs.keys(), ...liveSteps.keys()])]
    if (!wids.length) return
    const prevTops = new Map<number, number>()
    for (const [id, el] of laneEls) prevTops.set(id, el.scrollTop)
    await Promise.all(wids.map(async wid => {
      const liveCount = liveSteps.get(wid)?.length ?? 0
      const size = Math.max(pageSize, Math.ceil((liveCount + 10) / pageSize) * pageSize)
      const res = await listLogs({ session_id: props.sessionId, worker_id: wid, page: 1, page_size: size, order: 'desc' }).catch(() => null)
      if (!res) return // keep lane as-is; a later reconnect will retry the refill
      const rows = (res.data.logs || []).filter(log => laneRow(log, wid)).slice().reverse()
      laneLogs.set(wid, rows)
      seenWorkers.add(wid)
      liveSteps.delete(wid)
      if (rows.length < pageSize) laneExhausted.add(wid)
      else laneExhausted.delete(wid)
    }))
    jsonCache.clear()
    await nextTick()
    for (const l of displayLanes.value) {
      const el = laneEls.get(l.workerId)
      if (!el) continue
      if (isLaneSticky(l.workerId)) scrollLaneToBottom(l.workerId)
      else {
        const t = prevTops.get(l.workerId)
        if (t != null) el.scrollTop = t
      }
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
  const sticky = el.scrollHeight - el.scrollTop - el.clientHeight < 120
  laneStick.set(workerId, sticky)
  if (sticky) laneUnread.set(workerId, 0)
  if (el.scrollTop < 20) scheduleLaneEarlier(workerId)
}

function scrollLaneToBottom(workerId: number) {
  const el = laneEls.get(workerId)
  if (!el) return
  // Always instant: a smooth animation is interrupted by the next live append
  // (afterAppend re-pins to the growing scrollHeight), lands short of the
  // bottom, and the mid-animation scroll events clear laneStick — the lane
  // stops following and the back-to-latest pill stays lit for good.
  el.scrollTo({ top: el.scrollHeight, behavior: 'auto' })
  laneStick.set(workerId, true)
  laneUnread.set(workerId, 0)
}

function unreadCount(workerId: number): number {
  return laneUnread.get(workerId) ?? 0
}

function toggleSolo(workerId: number) {
  soloWorker.value = soloWorker.value === workerId ? null : workerId
  // Lanes remounted by the filter start at scrollTop 0; re-pin the ones that
  // were following so they land back at the bottom.
  nextTick(() => {
    for (const l of visibleLanes.value) {
      if (isLaneSticky(l.workerId)) scrollLaneToBottom(l.workerId)
    }
  })
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

.metric-chip--right {
  margin-left: auto;
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
  position: relative;
}

/* Slim indeterminate bar pinned to the lane top while earlier rows load. */
.lane-loading {
  height: 2px;
  flex-shrink: 0;
  overflow: hidden;
  background: transparent;
}

.lane-loading span {
  display: block;
  height: 100%;
  width: 40%;
  background: var(--accent);
  animation: lane-loading-slide 1s ease-in-out infinite;
}

@keyframes lane-loading-slide {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(250%); }
}

.lane-nomore,
.lane-idle {
  text-align: center;
  font-size: 11.5px;
  color: var(--text-muted);
  padding: 4px 0 2px;
  flex-shrink: 0;
}

.lane-idle {
  padding: 18px 0 8px;
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

/* Icon-only ghost button — ghost-button--sm is pill-sized for text. */
.lane-solo {
  min-height: 24px;
  padding: 0 7px;
  gap: 0;
  flex-shrink: 0;
  color: var(--text-muted);
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

/* ===== Lane-level back-to-latest pill (floats inside the lane) ===== */
.lane-latest {
  position: absolute;
  left: 50%;
  bottom: 12px;
  transform: translateX(-50%);
  z-index: 2;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 12px;
  min-height: 28px;
  font-family: var(--font-display);
  font-size: 12px;
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

.lane-latest:hover {
  transform: translateX(-50%) translateY(-1px);
  background: var(--accent-soft);
}

.lane-latest__badge {
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: var(--radius-pill);
  background: var(--accent);
  color: var(--surface-strong);
  font-size: 10.5px;
  font-weight: 700;
  display: inline-flex;
  align-items: center;
  justify-content: center;
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
