<template>
  <div class="timeline-wrap">
    <div class="timeline-toolbar">
      <span class="metric-chip">
        <span class="metric-label">{{ $t('logs.totalTokens') }}</span>
        <span class="metric-value">{{ totalTokens.toLocaleString() }}</span>
      </span>
      <span v-if="stream.connected.value" class="status-chip is-live">
        <span class="status-dot"></span>{{ $t('logs.live') }}
      </span>
      <span v-else class="status-chip is-muted">{{ $t('logs.reconnecting') }}</span>
    </div>

    <div ref="scrollEl" class="timeline-scroll" @scroll="onScroll">
      <div v-if="hasMore" class="load-earlier">
        <button class="ghost-button ghost-button--sm" :disabled="loadingHistory" @click="loadEarlier">
          {{ $t('logs.loadEarlier') }}
        </button>
      </div>

      <div v-if="!displayGroups.length && !loadingHistory" class="empty-state-card empty-state-card--inline">
        <div class="empty-state-card__icon">
          <el-icon :size="24"><Monitor /></el-icon>
        </div>
        <div class="empty-state-card__text">
          <h4 class="empty-state-card__title">{{ $t('logs.noLogs') }}</h4>
          <p class="empty-state-card__sub">{{ $t('logs.noLogsHint') }}</p>
        </div>
      </div>

      <div v-for="(group, gi) in displayGroups" :key="group.batch_index" :ref="el => setGroupRef(group.batch_index, el)" class="batch-group">
        <div class="batch-header" @click="toggleGroup(group, gi)">
          <el-icon class="batch-chevron" :class="{ 'is-open': !isCollapsed(group, gi) }"><ArrowDown /></el-icon>
          <span class="batch-label">{{ groupLabel(group, gi) }}</span>
          <span v-if="group.live" class="status-chip is-live"><span class="status-dot"></span>{{ $t('logs.live') }}</span>
          <span class="batch-meta cell-time">{{ $t('logs.stepCount', { count: group.steps.length }) }}</span>
          <span v-if="group.tokenSum > 0" class="batch-meta cell-time">{{ group.tokenSum.toLocaleString() }} tok</span>
          <span v-if="group.started_at" class="batch-meta cell-time">{{ formatTime(group.started_at) }}</span>
        </div>

        <div v-if="!isCollapsed(group, gi)" class="batch-steps">
          <template v-for="step in group.steps" :key="step.key">
            <!-- Chat message -->
            <div v-if="step.kind === 'message'" class="msg-row" :class="`msg-${step.role}`">
              <div class="msg-meta">
                <span class="status-chip" :class="roleChipClass(step.role)">{{ roleLabel(step.role) }}</span>
                <span v-if="step.time" class="cell-time">{{ step.time }}</span>
                <span v-if="step.total_tokens" class="cell-time">{{ step.prompt_tokens }}↑ {{ step.completion_tokens }}↓ = {{ step.total_tokens }}</span>
              </div>
              <div class="msg-bubble">
                <MarkdownView v-if="step.role === 'assistant'" :content="step.content" />
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
                <span v-if="step.time" class="cell-time">{{ step.time }}</span>
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
      <button v-if="!stickToBottom && displayGroups.length" class="scroll-latest" @click="scrollToBottom(true)">
        <el-icon :size="14"><Bottom /></el-icon>{{ $t('logs.scrollToLatest') }}
      </button>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { listLogs } from '../api'
import type { AgentLog } from '../types'
import { useSessionEventStream } from '../composables/sessionEvents'
import MarkdownView from './MarkdownView.vue'

const props = withDefaults(defineProps<{
  sessionId: number
  highlightBatch?: number | null
}>(), { highlightBatch: null })

const emit = defineEmits<{
  /** A run reached its terminal event — parent should refresh session counters. */
  (e: 'runDone'): void
  /** Stream saw a phase start/done transition — parent may refresh the status chip. */
  (e: 'runStatus'): void
}>()

const { t } = useI18n()

// ===== Steps & groups =====
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
type Step = MessageStep | ToolStep

interface BatchGroup {
  batch_index: number
  label?: string
  steps: Step[]
  tokenSum: number
  started_at?: string
  live: boolean
  firstId: number
}

let stepSeq = 0
const nextKey = () => `s${++stepSeq}`

const histLogs = ref<AgentLog[]>([]) // ascending by id
const liveSteps = reactive(new Map<number, Step[]>())
const liveLabels = reactive(new Map<number, string>())
const liveBatches = reactive(new Set<number>())
const collapsedMap = reactive(new Map<number, boolean>())
const stepDetail = reactive<Record<string, boolean>>({})
const jsonCache = new Map<string, string>()
const loadingHistory = ref(false)
const totalLogs = ref(0)
const loadedPages = ref(0)
const pageSize = 50
const scrollEl = ref<HTMLElement>()
const stickToBottom = ref(true)
const groupEls = new Map<number, Element>()

const stream = useSessionEventStream()
let skipFirstOpen = true
let historyReloadTimer: ReturnType<typeof setTimeout> | null = null

const hasMore = computed(() => histLogs.value.length < totalLogs.value)

const displayGroups = computed<BatchGroup[]>(() => {
  const map = new Map<number, BatchGroup>()
  const order: number[] = []
  const ensure = (bi: number, firstId: number) => {
    let g = map.get(bi)
    if (!g) {
      g = { batch_index: bi, steps: [], tokenSum: 0, live: false, firstId }
      map.set(bi, g)
      order.push(bi)
    }
    return g
  }
  for (const log of histLogs.value) {
    const g = ensure(log.batch_index, log.id)
    if (log.role === 'tool') {
      g.steps.push({
        kind: 'tool', key: `h${log.id}`, tool_name: log.tool_name,
        input: log.tool_input || undefined, output: log.tool_output || undefined,
        time: log.created_at,
      })
    } else {
      g.steps.push({
        kind: 'message', key: `h${log.id}`, role: log.role, content: log.content,
        prompt_tokens: log.prompt_tokens || undefined,
        completion_tokens: log.completion_tokens || undefined,
        total_tokens: log.total_tokens || undefined,
        time: log.created_at,
      })
    }
    g.tokenSum += log.total_tokens || 0
    if (!g.started_at && log.created_at) g.started_at = log.created_at
  }
  for (const [bi, steps] of liveSteps) {
    const g = ensure(bi, Number.MAX_SAFE_INTEGER - 1000 + order.length)
    g.steps.push(...steps)
    g.live = liveBatches.has(bi)
    g.tokenSum += steps.reduce((sum, s) => sum + (s.kind === 'message' ? (s.total_tokens || 0) : 0), 0)
    if (liveLabels.has(bi)) g.label = liveLabels.get(bi)
  }
  // Live-only groups (no history yet) sort after history groups, in arrival order.
  return order.map(bi => map.get(bi)!).sort((a, b) => a.firstId - b.firstId)
})

const totalTokens = computed(() => displayGroups.value.reduce((sum, g) => sum + g.tokenSum, 0))

// ===== Lifecycle =====
onMounted(async () => {
  if (!props.sessionId) return
  await init()
  if (props.highlightBatch != null) await revealBatch(props.highlightBatch)
})

watch(() => props.sessionId, async (id, oldId) => {
  if (!id || id === oldId) return
  await init()
})

onUnmounted(() => {
  if (historyReloadTimer) clearTimeout(historyReloadTimer)
})

async function init() {
  stream.close()
  histLogs.value = []
  liveSteps.clear()
  liveLabels.clear()
  liveBatches.clear()
  collapsedMap.clear()
  jsonCache.clear()
  Object.keys(stepDetail).forEach(k => delete stepDetail[k])
  loadedPages.value = 0
  totalLogs.value = 0
  stickToBottom.value = true
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
    scrollToBottom(false)
  } finally {
    loadingHistory.value = false
  }
}

async function loadEarlier() {
  if (!props.sessionId || loadingHistory.value || !hasMore.value) return
  loadingHistory.value = true
  const el = scrollEl.value
  const prevHeight = el?.scrollHeight || 0
  const prevTop = el?.scrollTop || 0
  try {
    const res = await listLogs({ session_id: props.sessionId, page: loadedPages.value + 1, page_size: pageSize, order: 'desc' })
    const rows = (res.data.logs || []).slice().reverse()
    histLogs.value = [...rows, ...histLogs.value]
    loadedPages.value += 1
    await nextTick()
    if (el) el.scrollTop = el.scrollHeight - prevHeight + prevTop
  } finally {
    loadingHistory.value = false
  }
}

/** Expand + scroll to a specific batch, loading older pages until it shows up. */
async function revealBatch(bi: number) {
  for (let i = 0; i < 10; i++) {
    const gi = displayGroups.value.findIndex(g => g.batch_index === bi)
    if (gi >= 0) {
      collapsedMap.set(bi, false)
      await nextTick()
      groupEls.get(bi)?.scrollIntoView({ block: 'start' })
      stickToBottom.value = false
      return
    }
    if (!hasMore.value) return
    await loadEarlier()
  }
}

function openStream() {
  if (!props.sessionId) return
  stream
    .onOpen(() => {
      // First open follows the initial history load; later (re)opens mean we
      // may have missed events — no replay exists, so refetch history.
      if (skipFirstOpen) { skipFirstOpen = false; return }
      scheduleHistoryReload()
    })
    .on('batch_start', (e) => {
      liveLabels.set(e.batch_index, e.label)
      liveBatches.add(e.batch_index)
      if (!liveSteps.has(e.batch_index)) liveSteps.set(e.batch_index, [])
      collapsedMap.set(e.batch_index, false)
      afterAppend()
    })
    .on('message', (e) => {
      pushLive(e.batch_index, {
        kind: 'message', key: nextKey(), role: e.role, content: e.content,
        prompt_tokens: e.prompt_tokens, completion_tokens: e.completion_tokens,
        total_tokens: e.total_tokens, time: new Date().toISOString(),
      })
    })
    .on('tool_call', (e) => {
      pushLive(e.batch_index, {
        kind: 'tool', key: nextKey(), tool_name: e.tool_name, input: e.input, pending: true,
        time: new Date().toISOString(),
      })
    })
    .on('tool_result', (e) => {
      const steps = liveSteps.get(e.batch_index)
      const pending = steps?.filter(s => s.kind === 'tool' && s.pending && s.tool_name === e.tool_name).pop() as ToolStep | undefined
      if (pending) {
        pending.output = e.output
        pending.ok = e.ok
        pending.pending = false
      } else {
        pushLive(e.batch_index, {
          kind: 'tool', key: nextKey(), tool_name: e.tool_name, output: e.output, ok: e.ok,
          time: new Date().toISOString(),
        })
      }
      afterAppend()
    })
    .on('run_status', () => emit('runStatus'))
    .on('run_done', () => {
      // Terminal event: persisted rows (with token counts) are now complete.
      emit('runDone')
      scheduleHistoryReload()
    })
  stream.open(props.sessionId)
}

function pushLive(bi: number, step: Step) {
  if (!liveSteps.has(bi)) liveSteps.set(bi, [])
  liveSteps.get(bi)!.push(step)
  liveBatches.add(bi)
  afterAppend()
}

function afterAppend() {
  nextTick(() => { if (stickToBottom.value) scrollToBottom(false) })
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
    liveBatches.clear()
    jsonCache.clear()
    await nextTick()
    if (stickToBottom.value) scrollToBottom(false)
  }, 600)
}

// ===== View helpers =====
function isCollapsed(group: BatchGroup, gi: number): boolean {
  const explicit = collapsedMap.get(group.batch_index)
  if (explicit !== undefined) return explicit
  // Default: only the latest group is expanded.
  return gi < displayGroups.value.length - 1
}

function toggleGroup(group: BatchGroup, gi: number) {
  collapsedMap.set(group.batch_index, !isCollapsed(group, gi))
}

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

function groupLabel(group: BatchGroup, gi: number): string {
  if (group.label) return group.label
  if (group.batch_index < 0) {
    const manualBefore = displayGroups.value.slice(0, gi).filter(g => g.batch_index < 0).length
    return t('logs.manualBatch', { index: manualBefore + 1 })
  }
  return t('logs.batch', { index: group.batch_index })
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
  return new Date(tstr).toLocaleTimeString()
}

function setGroupRef(bi: number, el: any) {
  if (el) groupEls.set(bi, el as Element)
  else groupEls.delete(bi)
}

function onScroll() {
  const el = scrollEl.value
  if (!el) return
  stickToBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight < 120
}

function scrollToBottom(smooth: boolean) {
  const el = scrollEl.value
  if (!el) return
  el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
  stickToBottom.value = true
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
}

.timeline-scroll {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-right: 4px;
}

.load-earlier {
  display: flex;
  justify-content: center;
  flex-shrink: 0;
}

/* ===== Batch group ===== */
.batch-group {
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-elevated);
  overflow: hidden;
  flex-shrink: 0;
}

.batch-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  cursor: pointer;
  user-select: none;
  transition: background-color var(--duration-fast) var(--ease-spring);
}

.batch-header:hover {
  background: var(--accent-soft);
}

.batch-chevron {
  color: var(--text-muted);
  transition: transform var(--duration-fast) var(--ease-spring);
  transform: rotate(-90deg);
}

.batch-chevron.is-open {
  transform: rotate(0deg);
}

.batch-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
}

.batch-meta {
  flex-shrink: 0;
  font-size: 11.5px;
}

.batch-steps {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 4px 14px 14px 38px;
}

/* ===== Chat bubbles ===== */
.msg-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 88%;
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
