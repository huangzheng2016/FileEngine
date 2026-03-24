<template>
  <el-card>
    <template #header>
      <div style="display: flex; gap: 12px; align-items: center; flex-wrap: wrap">
        <span style="font-weight: bold">{{ $t('logs.title') }}</span>
        <el-select v-model="sessionId" :placeholder="$t('logs.session')" size="small" style="width: 240px" @change="onSessionChange">
          <el-option v-for="s in sessions" :key="s.id"
            :label="formatSessionLabel(s)"
            :value="s.id" />
        </el-select>
        <el-tag v-if="totalTokens > 0">{{ $t('logs.totalTokens', { count: totalTokens }) }}</el-tag>
        <el-tag v-if="isLive" type="success" size="small" effect="dark" style="animation: pulse 1.5s infinite">LIVE</el-tag>
      </div>
    </template>

    <div ref="logContainer" style="height: calc(100vh - 220px); overflow: auto; padding: 8px">
      <div v-for="log in logs" :key="log.id" style="margin-bottom: 12px; border-left: 3px solid; padding-left: 12px"
        :style="{ borderColor: roleColor(log.role) }">
        <div style="display: flex; gap: 8px; align-items: center; margin-bottom: 4px">
          <el-tag :type="roleTagType(log.role)" size="small">{{ log.role }}</el-tag>
          <el-tag v-if="log.tool_name" size="small" type="warning">{{ log.tool_name }}</el-tag>
          <span style="font-size: 12px; color: #999">{{ $t('logs.batch', { index: log.batch_index }) }} | {{ formatTime(log.created_at) }}</span>
          <span v-if="log.tokens_used" style="font-size: 12px; color: #999">{{ $t('logs.tokens', { count: log.tokens_used }) }}</span>
        </div>

        <div v-if="log.content" style="white-space: pre-wrap; font-size: 13px; line-height: 1.5; max-height: 300px; overflow: auto">{{ log.content }}</div>

        <div v-if="log.tool_input" style="margin-top: 4px">
          <el-collapse>
            <el-collapse-item :title="$t('logs.input')">
              <pre style="font-size: 12px; background: #f5f5f5; padding: 8px; border-radius: 4px; overflow: auto">{{ formatJSON(log.tool_input) }}</pre>
            </el-collapse-item>
          </el-collapse>
        </div>

        <div v-if="log.tool_output" style="margin-top: 4px">
          <el-collapse>
            <el-collapse-item :title="$t('logs.output')">
              <pre style="font-size: 12px; background: #f0f9eb; padding: 8px; border-radius: 4px; overflow: auto; max-height: 300px">{{ formatJSON(log.tool_output) }}</pre>
            </el-collapse-item>
          </el-collapse>
        </div>
      </div>
    </div>
    <el-pagination v-if="!isLive && total > pageSize" style="margin-top: 12px; justify-content: center"
      layout="total, prev, pager, next" :total="total" :page-size="pageSize"
      v-model:current-page="page" @current-change="loadLogs" />
  </el-card>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, nextTick, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { listSessions, listLogs } from '../api'
import type { ScanSession, AgentLog } from '../types'

useI18n()

const sessions = ref<ScanSession[]>([])
const sessionId = ref<number>(0)
const logs = ref<AgentLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 100
const logContainer = ref<HTMLElement>()
const isLive = ref(false)
let eventSource: EventSource | null = null
let pollTimer: ReturnType<typeof setInterval> | null = null

const totalTokens = computed(() => logs.value.reduce((sum, l) => sum + (l.tokens_used || 0), 0))

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

function onSessionChange() {
  stopLive()
  page.value = 1
  loadLogs()

  // Auto-detect if session is actively tagging → start live
  const session = sessions.value.find(s => s.id === sessionId.value)
  if (session && session.status === 'tagging') {
    startLive()
  }
}

async function loadLogs() {
  if (!sessionId.value) return
  const params: Record<string, any> = { session_id: sessionId.value, page: page.value, page_size: pageSize }
  const res = await listLogs(params)
  logs.value = res.data.logs || []
  total.value = res.data.total
}

function startLive() {
  if (!sessionId.value) return
  isLive.value = true
  eventSource = new EventSource(`/api/v1/logs/stream?session_id=${sessionId.value}`)
  eventSource.onmessage = (e) => {
    const log: AgentLog = JSON.parse(e.data)
    logs.value.push(log)
    nextTick(() => {
      if (logContainer.value) logContainer.value.scrollTop = logContainer.value.scrollHeight
    })
  }
  eventSource.onerror = () => {
    // SSE disconnected, fall back to polling
    stopLive()
    startPolling()
  }
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
    } else {
      // Tagging done, stop polling, do final load
      if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
      loadLogs()
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

function formatJSON(s: string) {
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}
</script>

<style scoped>
@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}
</style>
