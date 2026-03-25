<template>
  <div style="display: flex; gap: 16px; height: calc(100vh - 100px)">
    <!-- Left: batch list -->
    <div style="width: 220px; flex-shrink: 0; display: flex; flex-direction: column; gap: 12px">
      <el-card>
        <el-select v-model="sessionId" :placeholder="$t('logs.session')" size="small" style="width: 100%" @change="onSessionChange">
          <el-option v-for="s in sessions" :key="s.id" :label="formatSessionLabel(s)" :value="s.id" />
        </el-select>
      </el-card>
      <el-card style="flex: 1; overflow: auto; display: flex; flex-direction: column">
        <template #header>
          <div style="display: flex; justify-content: space-between; align-items: center">
            <span style="font-size: 13px; font-weight: 500">{{ $t('logs.batch', { index: '' }).replace('#', '') }}</span>
            <el-button size="small" text @click="toggleOrder">
              {{ orderDesc ? $t('logs.orderDesc') : $t('logs.orderAsc') }}
              <el-icon style="margin-left: 2px"><Sort /></el-icon>
            </el-button>
          </div>
        </template>
        <div style="flex: 1; overflow: auto">
          <div
            style="padding: 6px 8px; cursor: pointer; border-radius: 4px; margin-bottom: 2px; font-size: 13px"
            :style="{ background: selectedBatch === null ? '#ecf5ff' : '', color: selectedBatch === null ? '#409eff' : '' }"
            @click="selectBatch(null)">
            {{ $t('logs.allBatches') }}
          </div>
          <div v-for="b in displayBatches" :key="b"
            style="padding: 6px 8px; cursor: pointer; border-radius: 4px; margin-bottom: 2px; font-size: 13px"
            :style="{ background: selectedBatch === b ? '#ecf5ff' : '', color: selectedBatch === b ? '#409eff' : '' }"
            @click="selectBatch(b)">
            {{ $t('logs.batch', { index: b }) }}
          </div>
        </div>
        <el-pagination v-if="batchTotal > batchPageSize" size="small" layout="prev, next"
          :total="batchTotal" :page-size="batchPageSize"
          v-model:current-page="batchPage" @current-change="loadBatches"
          style="margin-top: 8px; justify-content: center" />
      </el-card>
    </div>

    <!-- Right: log content -->
    <div style="flex: 1; display: flex; flex-direction: column; gap: 12px; overflow: hidden">
      <el-card>
        <div style="display: flex; gap: 12px; align-items: center">
          <el-tag v-if="totalTokens > 0">{{ $t('logs.totalTokens', { count: totalTokens }) }}</el-tag>
          <el-tag v-if="isLive" type="success" size="small" effect="dark" style="animation: pulse 1.5s infinite">LIVE</el-tag>
        </div>
      </el-card>
      <el-card style="flex: 1; overflow: hidden">
        <div ref="logContainer" style="height: 100%; overflow: auto; padding: 8px">
          <div v-for="log in logs" :key="log.id" style="margin-bottom: 10px; border-left: 3px solid; padding-left: 10px"
            :style="{ borderColor: roleColor(log.role) }">
            <div style="display: flex; gap: 6px; align-items: center; margin-bottom: 3px">
              <el-tag :type="roleTagType(log.role)" size="small">{{ log.role }}</el-tag>
              <el-tag v-if="log.tool_name" size="small" type="warning">{{ log.tool_name }}</el-tag>
              <span style="font-size: 12px; color: #999">{{ $t('logs.batch', { index: log.batch_index }) }} | {{ formatTime(log.created_at) }}</span>
              <span v-if="log.tokens_used" style="font-size: 12px; color: #999">{{ $t('logs.tokens', { count: log.tokens_used }) }}</span>
            </div>
            <!-- Content with truncation -->
            <div v-if="log.content">
              <div style="white-space: pre-wrap; font-size: 13px; line-height: 1.5; overflow: hidden"
                :style="{ maxHeight: expanded[log.id + '_content'] ? 'none' : '100px' }">{{ log.content }}</div>
              <el-button v-if="log.content.length > 300" size="small" text type="primary" @click="toggleExpand(log.id, 'content')">
                {{ expanded[log.id + '_content'] ? $t('logs.collapse') : $t('logs.expand') }}
              </el-button>
            </div>
            <!-- Tool I/O -->
            <template v-if="log.tool_input || log.tool_output">
              <div style="margin-top: 4px; display: flex; gap: 6px">
                <el-button v-if="log.tool_input" size="small" text type="info" @click="toggleExpand(log.id, 'input')">{{ $t('logs.input') }} {{ expanded[log.id + '_input'] ? '▾' : '▸' }}</el-button>
                <el-button v-if="log.tool_output" size="small" text type="info" @click="toggleExpand(log.id, 'output')">{{ $t('logs.output') }} {{ expanded[log.id + '_output'] ? '▾' : '▸' }}</el-button>
              </div>
              <pre v-if="expanded[log.id + '_input']" style="font-size: 12px; background: #f5f5f5; padding: 8px; border-radius: 4px; overflow: auto; max-height: 250px; margin-top: 4px">{{ cachedJSON(log.id, 'input', log.tool_input) }}</pre>
              <pre v-if="expanded[log.id + '_output']" style="font-size: 12px; background: #f0f9eb; padding: 8px; border-radius: 4px; overflow: auto; max-height: 250px; margin-top: 4px">{{ cachedJSON(log.id, 'output', log.tool_output) }}</pre>
            </template>
          </div>
        </div>
      </el-card>
      <el-pagination v-if="!isLive && total > pageSize" style="justify-content: center"
        layout="total, prev, pager, next" :total="total" :page-size="pageSize"
        v-model:current-page="page" @current-change="loadLogs" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { listSessions, listLogs, listBatches } from '../api'
import type { ScanSession, AgentLog } from '../types'

const sessions = ref<ScanSession[]>([])
const sessionId = ref<number>(0)
const logs = ref<AgentLog[]>([])
const batches = ref<number[]>([])
const batchTotal = ref(0)
const batchPage = ref(1)
const batchPageSize = 50
const selectedBatch = ref<number | null>(null)
const total = ref(0)
const page = ref(1)
const pageSize = 50
const logContainer = ref<HTMLElement>()
const isLive = ref(false)
const orderDesc = ref(localStorage.getItem('fe_log_order') !== 'asc')
const expanded = reactive<Record<string, boolean>>({})
const jsonCache = new Map<string, string>()
let eventSource: EventSource | null = null
let pollTimer: ReturnType<typeof setInterval> | null = null
const MAX_LIVE_LOGS = 200

const totalTokens = computed(() => logs.value.reduce((sum, l) => sum + (l.tokens_used || 0), 0))
const displayBatches = computed(() => orderDesc.value ? [...batches.value].reverse() : batches.value)

function cachedJSON(id: number, type: string, raw: string): string {
  const key = `${id}_${type}`
  if (jsonCache.has(key)) return jsonCache.get(key)!
  let result: string
  try { result = JSON.stringify(JSON.parse(raw), null, 2) } catch { result = raw }
  jsonCache.set(key, result)
  return result
}

function toggleExpand(id: number, type: string) {
  const key = `${id}_${type}`
  expanded[key] = !expanded[key]
}

onMounted(async () => {
  const res = await listSessions()
  sessions.value = res.data
  if (sessions.value.length > 0) {
    sessionId.value = sessions.value[0].id
    onSessionChange()
  }
})

onUnmounted(() => {
  stopLive()
  if (pollTimer) clearInterval(pollTimer)
})

async function onSessionChange() {
  stopLive()
  selectedBatch.value = null
  page.value = 1
  batchPage.value = 1
  Object.keys(expanded).forEach(k => delete expanded[k])
  jsonCache.clear()
  await Promise.all([loadLogs(), loadBatches()])
  const session = sessions.value.find(s => s.id === sessionId.value)
  if (session && session.status === 'tagging') startLive()
}

async function loadBatches() {
  if (!sessionId.value) return
  const res = await listBatches(sessionId.value, batchPage.value, batchPageSize)
  batches.value = res.data.batches || []
  batchTotal.value = res.data.total
}

async function loadLogs() {
  if (!sessionId.value) return
  const params: Record<string, any> = {
    session_id: sessionId.value,
    page: page.value,
    page_size: pageSize,
    order: orderDesc.value ? 'desc' : 'asc',
  }
  if (selectedBatch.value !== null) params.batch = selectedBatch.value
  const res = await listLogs(params)
  logs.value = res.data.logs || []
  total.value = res.data.total
}

function selectBatch(b: number | null) {
  selectedBatch.value = b
  page.value = 1
  loadLogs()
}

function toggleOrder() {
  orderDesc.value = !orderDesc.value
  localStorage.setItem('fe_log_order', orderDesc.value ? 'desc' : 'asc')
  page.value = 1
  loadLogs()
}

function startLive() {
  if (!sessionId.value) return
  isLive.value = true
  eventSource = new EventSource(`/api/v1/logs/stream?session_id=${sessionId.value}`)
  eventSource.onmessage = (e) => {
    const log: AgentLog = JSON.parse(e.data)
    if (selectedBatch.value !== null && log.batch_index !== selectedBatch.value) return
    if (orderDesc.value) {
      logs.value.unshift(log)
      if (logs.value.length > MAX_LIVE_LOGS) logs.value.length = MAX_LIVE_LOGS
    } else {
      logs.value.push(log)
      if (logs.value.length > MAX_LIVE_LOGS) logs.value.splice(0, logs.value.length - MAX_LIVE_LOGS)
      nextTick(() => { if (logContainer.value) logContainer.value.scrollTop = logContainer.value.scrollHeight })
    }
    if (!batches.value.includes(log.batch_index)) {
      batches.value.push(log.batch_index)
      batches.value.sort((a, b) => a - b)
      batchTotal.value++
    }
  }
  eventSource.onerror = () => { stopLive(); startPolling() }
}

function stopLive() {
  isLive.value = false
  if (eventSource) { eventSource.close(); eventSource = null }
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}

function startPolling() {
  if (pollTimer) return
  pollTimer = setInterval(async () => {
    const sessRes = await listSessions()
    sessions.value = sessRes.data
    const current = sessions.value.find(s => s.id === sessionId.value)
    if (current && current.status === 'tagging') {
      loadLogs()
      loadBatches()
    } else {
      if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
      loadLogs()
      loadBatches()
    }
  }, 3000)
}

function formatSessionLabel(s: ScanSession): string {
  const date = new Date(s.created_at).toLocaleDateString()
  const status = s.status === 'tagging' ? ' [tagging]' : ''
  return `${s.scan_path || '/'} · ${s.total_files} files · ${date}${status}`
}

function roleColor(role: string) {
  const map: Record<string, string> = { system: '#909399', user: '#409eff', assistant: '#67c23a', tool: '#e6a23c' }
  return map[role] || '#909399'
}

function roleTagType(role: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  const map: Record<string, '' | 'success' | 'warning' | 'info' | 'danger'> = { system: 'info', user: '', assistant: 'success', tool: 'warning' }
  return map[role] || 'info'
}

function formatTime(t: string) {
  if (!t) return ''
  return new Date(t).toLocaleTimeString()
}
</script>

<style scoped>
@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}
</style>