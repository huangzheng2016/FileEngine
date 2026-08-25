<template>
  <section class="page-card">
    <header class="page-header">
      <div class="page-titles">
        <span class="page-eyebrow">FileEngine</span>
        <h2 class="page-heading">{{ $t('tasks.title') }}</h2>
        <p class="page-sub">{{ $t('tasks.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button @click="load">
          <el-icon style="margin-right: 4px"><Refresh /></el-icon>{{ $t('common.refresh') }}
        </el-button>
        <el-button type="primary" @click="openNewScanDialog">
          <el-icon style="margin-right: 4px"><Plus /></el-icon>{{ $t('tasks.newScan') }}
        </el-button>
      </div>
    </header>

    <div class="page-body">
      <el-table :data="sessions" v-loading="loading" style="width: 100%">
        <el-table-column prop="id" label="ID" width="60">
          <template #default="{ row }"><span class="cell-time">{{ row.id }}</span></template>
        </el-table-column>
        <el-table-column prop="root_path" :label="$t('tasks.rootPath')" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <code class="cell-mono">{{ row.root_path === '.' ? $t('tasks.allDirectories') : row.root_path }}</code>
          </template>
        </el-table-column>
        <el-table-column prop="protocol" :label="$t('tasks.protocol')" width="100">
          <template #default="{ row }">
            <span class="status-chip" :class="protocolChipClass(row.protocol)">{{ row.protocol.toUpperCase() }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="$t('common.status')" width="150">
          <template #default="{ row }">
            <div style="display: flex; gap: 6px; align-items: center">
              <span class="status-chip" :class="statusChipClass(row.status)">
                <span v-if="isActive(row)" class="status-dot"></span>
                {{ statusLabel(row.status) }}
              </span>
              <span v-if="row.allow_vision" class="status-chip is-accent">{{ $t('tasks.visionOn') }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="$t('tasks.progress')" min-width="300">
          <template #default="{ row }">
            <div class="progress-cell">
              <div class="progress-chips">
                <span class="metric-chip"><span class="metric-label">{{ $t('tasks.totalFiles') }}</span><span class="metric-value">{{ row.total_files }}</span></span>
                <span class="metric-chip"><span class="metric-label">{{ $t('tasks.taggedFiles') }}</span><span class="metric-value">{{ row.tagged_files }}</span></span>
                <span class="metric-chip"><span class="metric-label">{{ $t('tasks.plannedOps') }}</span><span class="metric-value">{{ row.planned_ops }}</span></span>
                <span class="metric-chip"><span class="metric-label">{{ $t('tasks.executedOps') }}</span><span class="metric-value">{{ row.executed_ops }}</span></span>
                <span class="metric-chip" v-if="row.total_size > 0"><span class="metric-label">{{ $t('common.size') }}</span><span class="metric-value">{{ formatSize(row.total_size) }}</span></span>
              </div>
              <div v-if="row.total_tokens > 0" class="progress-tokens cell-time">
                Token: {{ row.prompt_tokens.toLocaleString() }}↑ {{ row.completion_tokens.toLocaleString() }}↓ = {{ row.total_tokens.toLocaleString() }}
                <span v-if="row.total_files > 0 && row.total_tokens / row.total_files >= 1"> | {{ $t('tasks.tokensPerFile', { count: (row.total_tokens / row.total_files).toFixed(0) }) }}</span>
                <span v-if="row.total_size > 0 && row.total_tokens / (row.total_size / 1048576) >= 1"> | {{ $t('tasks.tokensPerMB', { count: (row.total_tokens / (row.total_size / 1048576)).toFixed(0) }) }}</span>
              </div>
              <el-progress v-if="row.total_files > 0"
                :percentage="Math.round((row.tagged_files / row.total_files) * 100)"
                :stroke-width="6" />
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="$t('common.actions')" width="150" fixed="right">
          <template #default="{ row }">
            <el-dropdown trigger="click" @command="(cmd: string) => handleCommand(cmd, row)">
              <el-button size="small">
                {{ $t('common.actions') }}<el-icon class="el-icon--right"><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="run"><el-icon><Monitor /></el-icon>{{ $t('tasks.viewRun') }}</el-dropdown-item>
                  <el-dropdown-item command="settings"><el-icon><Setting /></el-icon>{{ $t('tasks.settings') }}</el-dropdown-item>
                  <el-dropdown-item command="rescan" :disabled="row.status === 'scanning'"><el-icon><Refresh /></el-icon>{{ $t('tasks.rescan') }}</el-dropdown-item>
                  <el-dropdown-item command="tag" :disabled="!canTag(row)"><el-icon><PriceTag /></el-icon>{{ row.status === 'tagging' ? $t('tasks.stopTag') : $t('tasks.tag') }}</el-dropdown-item>
                  <el-dropdown-item command="plans" :disabled="row.planned_ops === 0"><el-icon><Document /></el-icon>{{ $t('tasks.plans') }}</el-dropdown-item>
                  <el-dropdown-item command="stats"><el-icon><DataAnalysis /></el-icon>{{ $t('tasks.stats') }}</el-dropdown-item>
                  <el-dropdown-item command="execute" :disabled="!canExecute(row)"><el-icon><VideoPlay /></el-icon>{{ row.status === 'executing' ? $t('tasks.stop') : $t('tasks.execute') }}</el-dropdown-item>
                  <el-dropdown-item command="delete" divided><el-icon><Delete /></el-icon>{{ $t('common.delete') }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>
        <template #empty>
          <div class="empty-state-card empty-state-card--inline">
            <div class="empty-state-card__icon">
              <el-icon :size="24"><Monitor /></el-icon>
            </div>
            <div class="empty-state-card__text">
              <h4 class="empty-state-card__title">{{ $t('tasks.noSessions') }}</h4>
              <p class="empty-state-card__sub">{{ $t('tasks.noSessionsHint') }}</p>
            </div>
          </div>
        </template>
      </el-table>
    </div>

    <!-- Plans dialog -->
    <el-dialog append-to-body v-model="plansVisible" :title="$t('tasks.executionPlans')" width="80%">
      <template #header>
        <div style="display: flex; align-items: center; gap: 10px">
          <span>{{ $t('tasks.executionPlans') }}</span>
          <el-button size="small" @click="handleExportPlans" :icon="Download">{{ $t('tasks.exportPlans') }}</el-button>
          <el-button size="small" @click="handleValidate" :loading="validating" :icon="CircleCheck">{{ validating ? $t('tasks.validating') : $t('tasks.validate') }}</el-button>
        </div>
      </template>
      <el-alert v-if="validationResult && !validationResult.ok" type="warning" :closable="false" style="margin-bottom: 10px">
        <template #title>{{ $t('tasks.validateIssues', { count: validationResult.issues.length }) }}</template>
        <div v-for="(issue, idx) in validationResult.issues" :key="idx" style="font-size: 12px; margin-top: 4px">
          <span class="status-chip" :class="issue.issue === 'conflict' ? 'is-danger' : 'is-warning'">{{ $t('tasks.issue' + (issue.issue === 'conflict' ? 'Conflict' : 'Duplicate')) }}</span>
          {{ issue.new_path }} — {{ issue.detail }}
        </div>
      </el-alert>
      <el-alert v-if="validationResult && validationResult.ok" type="success" :closable="false" style="margin-bottom: 10px">
        {{ $t('tasks.validateOk') }}
      </el-alert>
      <el-table :data="plans" style="width: 100%" max-height="500">
        <el-table-column prop="name" :label="$t('tasks.planName')" width="200" />
        <el-table-column prop="operation" :label="$t('tasks.planOp')" width="90">
          <template #default>
            <span class="status-chip is-muted">{{ $t('tasks.planned') }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="original_path" :label="$t('tasks.planFrom')" min-width="280" show-overflow-tooltip>
          <template #default="{ row }"><code class="cell-mono">{{ row.original_path }}</code></template>
        </el-table-column>
        <el-table-column prop="new_path" :label="$t('tasks.planTo')" min-width="280" show-overflow-tooltip>
          <template #default="{ row }"><code class="cell-mono">{{ row.new_path }}</code></template>
        </el-table-column>
        <el-table-column prop="file_type" :label="$t('common.type')" width="110">
          <template #default="{ row }">
            <span v-if="row.file_type === 'directory'" class="status-chip is-warning">{{ $t('files.directory') }}</span>
            <span v-else class="status-chip is-muted">{{ $t('files.file') }}</span>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <!-- Stats dialog -->
    <el-dialog append-to-body v-model="statsVisible" :title="$t('tasks.statsTitle')" width="500px">
      <div v-if="sessionStatsData && sessionStatsData.category_distribution && sessionStatsData.category_distribution.length > 0">
        <p style="margin-bottom: 8px; font-weight: 600">{{ $t('tasks.categoryDistribution') }}</p>
        <el-table :data="sessionStatsData.category_distribution" size="small" max-height="300">
          <el-table-column prop="name" :label="$t('common.name')" width="150" />
          <el-table-column prop="path" :label="$t('common.path')" min-width="150" show-overflow-tooltip />
          <el-table-column prop="file_count" :label="$t('tasks.fileCount')" width="80" align="right" />
          <el-table-column :label="$t('tasks.totalSizeLabel')" width="100" align="right">
            <template #default="{ row }">{{ formatSize(row.total_size) }}</template>
          </el-table-column>
        </el-table>
      </div>
      <div v-else class="empty-state-card empty-state-card--inline">
        <div class="empty-state-card__text">
          <h4 class="empty-state-card__title">{{ $t('tasks.noStats') }}</h4>
        </div>
      </div>
    </el-dialog>

    <!-- New scan dialog -->
    <el-dialog append-to-body v-model="showNewDialog" :title="$t('tasks.newScan')" width="460px">
      <div class="form-stack">
        <div class="form-field">
          <label class="form-field__label is-required">{{ $t('tasks.filesystem') }}</label>
          <el-select v-model="newScan.filesystemId" style="width: 100%" :placeholder="$t('tasks.selectFs')">
            <el-option v-for="fs in filesystems" :key="fs.id" :label="`[${fs.protocol.toUpperCase()}] ${fs.name}`" :value="fs.id">
              <span style="display: flex; align-items: center; gap: 6px">
                <span class="status-chip" :class="protocolChipClass(fs.protocol)" style="width: 52px; justify-content: center">{{ fs.protocol.toUpperCase() }}</span>
                {{ fs.name }}
              </span>
            </el-option>
          </el-select>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.scanPath') }}</label>
          <el-input v-model="newScan.scanPath" :placeholder="$t('tasks.scanPathHint')" />
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('models.selectModel') }}</label>
          <el-select v-model="newScan.modelProviderId" style="width: 100%" clearable :placeholder="$t('models.noModel')">
            <el-option v-for="m in modelProviders" :key="m.id" :label="`[${providerLabel(m.provider)}] ${m.name}`" :value="m.id">
              <span style="display: flex; align-items: center; gap: 6px">
                <span class="status-chip" :class="providerChipClass(m.provider)">{{ providerLabel(m.provider) }}</span>
                {{ m.name }} ({{ m.model }})
                <span v-if="m.vision_enabled" class="status-chip is-live" style="margin-left: auto">{{ $t('tasks.visionOn') }}</span>
              </span>
            </el-option>
          </el-select>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.allowVision') }}</label>
          <div><el-switch v-model="newScan.allowVision" /></div>
          <p class="form-field__hint">{{ $t('tasks.allowVisionHint') }}</p>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.excludeCategoryDirs') }}</label>
          <div><el-switch v-model="newScan.excludeCategoryDirs" /></div>
          <p class="form-field__hint">{{ $t('tasks.excludeCategoryDirsHint') }}</p>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.filterMode') }}</label>
          <el-radio-group v-model="newScan.filterMode">
            <el-radio value="blacklist">{{ $t('tasks.filterBlacklist') }}</el-radio>
            <el-radio value="whitelist">{{ $t('tasks.filterWhitelist') }}</el-radio>
          </el-radio-group>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.filterDirs') }}</label>
          <el-input v-model="newScan.filterDirs" type="textarea" :rows="4" :placeholder="$t('tasks.filterDirsPlaceholder')" />
          <p class="form-field__hint">{{ $t('tasks.filterDirsHint') }}</p>
        </div>
      </div>
      <template #footer>
        <el-button @click="showNewDialog = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" @click="handleNewScan">{{ $t('tasks.newScan') }}</el-button>
      </template>
    </el-dialog>

    <!-- Execute mode dialog -->
    <el-dialog append-to-body v-model="execDialogVisible" :title="$t('tasks.execute')" width="400px">
      <div class="form-stack">
        <p style="margin: 0">{{ $t('tasks.executeConfirm', { count: execSession?.planned_ops || 0 }) }}</p>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.execMode') }}</label>
          <el-radio-group v-model="execMode">
            <el-radio value="copy">{{ $t('tasks.copy') }} {{ $t('tasks.execCopyHint') }}</el-radio>
            <el-radio value="move">{{ $t('tasks.move') }} {{ $t('tasks.execMoveHint') }}</el-radio>
          </el-radio-group>
        </div>
      </div>
      <template #footer>
        <el-button @click="execDialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="warning" @click="confirmExecute">{{ $t('tasks.execute') }}</el-button>
      </template>
    </el-dialog>

    <!-- Session settings dialog -->
    <el-dialog append-to-body v-model="settingsVisible" :title="$t('tasks.settings')" width="440px">
      <div class="form-stack">
        <div class="form-field">
          <label class="form-field__label">{{ $t('models.selectModel') }}</label>
          <el-select v-model="sessionSettings.model_provider_id" style="width: 100%" clearable :placeholder="$t('models.noModel')">
            <el-option v-for="m in modelProviders" :key="m.id" :label="`[${providerLabel(m.provider)}] ${m.name}`" :value="m.id">
              <span style="display: flex; align-items: center; gap: 6px">
                <span class="status-chip" :class="providerChipClass(m.provider)">{{ providerLabel(m.provider) }}</span>
                {{ m.name }} ({{ m.model }})
                <span v-if="m.vision_enabled" class="status-chip is-live" style="margin-left: auto">{{ $t('tasks.visionOn') }}</span>
              </span>
            </el-option>
          </el-select>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.allowReadFile') }}</label>
          <div><el-switch v-model="sessionSettings.allow_read_file" /></div>
          <p class="form-field__hint">{{ $t('tasks.allowReadFileHint') }}</p>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.allowAutoCategory') }}</label>
          <div><el-switch v-model="sessionSettings.allow_auto_category" /></div>
          <p class="form-field__hint">{{ $t('tasks.allowAutoCategoryHint') }}</p>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.allowVision') }}</label>
          <div><el-switch v-model="sessionSettings.allow_vision" /></div>
          <p class="form-field__hint">{{ $t('tasks.allowVisionHint') }}</p>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.excludeCategoryDirs') }}</label>
          <div><el-switch v-model="sessionSettings.exclude_category_dirs" /></div>
          <p class="form-field__hint">{{ $t('tasks.excludeCategoryDirsHint') }}</p>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.filterMode') }}</label>
          <el-radio-group v-model="sessionSettings.filter_mode">
            <el-radio value="blacklist">{{ $t('tasks.filterBlacklist') }}</el-radio>
            <el-radio value="whitelist">{{ $t('tasks.filterWhitelist') }}</el-radio>
          </el-radio-group>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('tasks.filterDirs') }}</label>
          <el-input v-model="sessionSettings.filter_dirs" type="textarea" :rows="4" :placeholder="$t('tasks.filterDirsPlaceholder')" />
          <p class="form-field__hint">{{ $t('tasks.filterDirsHint') }}</p>
        </div>
      </div>
      <template #footer>
        <el-button @click="settingsVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" @click="saveSettings">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { listSessions, getSession, createSession, deleteSession, rescanSession, updateSessionConfig, startTagging, stopTagging, startExecute, stopExecute, getPlans, exportPlansCSV, validatePlans, getSessionStats, listFilesystems, listModelProviders } from '../api'
import type { ScanSession, PlanItem, Filesystem, ModelProvider, ValidationResult, SessionStats, ProgressEvent } from '../types'
import { SessionEventStream } from '../composables/sessionEvents'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Download, CircleCheck, DataAnalysis, Plus, Refresh, Monitor } from '@element-plus/icons-vue'

const { t } = useI18n()
const router = useRouter()
const sessions = ref<ScanSession[]>([])
const filesystems = ref<Filesystem[]>([])
const modelProviders = ref<ModelProvider[]>([])
const loading = ref(false)
const plansVisible = ref(false)
const plans = ref<PlanItem[]>([])
const showNewDialog = ref(false)
const newScan = ref<{ filesystemId: number; scanPath: string; modelProviderId: number | null; allowVision: boolean; excludeCategoryDirs: boolean; filterMode: string; filterDirs: string }>({ filesystemId: 0, scanPath: '', modelProviderId: null, allowVision: false, excludeCategoryDirs: false, filterMode: 'blacklist', filterDirs: '' })
const execDialogVisible = ref(false)
const execMode = ref('copy')
const execSession = ref<ScanSession | null>(null)
const settingsVisible = ref(false)
const settingsSessionId = ref(0)
const sessionSettings = ref({ allow_read_file: true, allow_auto_category: false, allow_vision: false, exclude_category_dirs: false, filter_mode: 'blacklist', filter_dirs: '', model_provider_id: 0 })
const validating = ref(false)
const validationResult = ref<ValidationResult | null>(null)
const currentPlansSessionId = ref(0)
const statsVisible = ref(false)
const sessionStatsData = ref<SessionStats | null>(null)

// ===== Event-driven live updates =====
// Agent runs (tagging / executing) stream over SSE per session; scanning has no
// run events, so a light poll runs only while at least one session is scanning.
const streams = new Map<number, SessionEventStream>()
let scanPollTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  load()
  loadFilesystems()
  loadModelProviders()
})

onUnmounted(() => {
  for (const s of streams.values()) s.close()
  streams.clear()
  if (scanPollTimer) { clearInterval(scanPollTimer); scanPollTimer = null }
})

function isActive(s: ScanSession): boolean {
  return s.status === 'scanning' || s.status === 'tagging' || s.status === 'executing'
}

/** Open SSE streams for run-active sessions; close streams for inactive ones. */
function syncStreams() {
  const activeRunIds = new Set(
    sessions.value.filter(s => s.status === 'tagging' || s.status === 'executing').map(s => s.id)
  )
  for (const [id, stream] of streams) {
    if (!activeRunIds.has(id)) { stream.close(); streams.delete(id) }
  }
  for (const id of activeRunIds) {
    if (streams.has(id)) continue
    const stream = new SessionEventStream()
    stream
      .on('run_status', () => refreshSession(id))
      .on('run_done', () => refreshSession(id))
      .on('progress', (p: ProgressEvent) => patchProgress(id, p))
    stream.open(id)
    streams.set(id, stream)
  }
  syncScanPoll()
}

/** Scanning emits no SSE events — poll only while a scan is in flight. */
function syncScanPoll() {
  const anyScanning = sessions.value.some(s => s.status === 'scanning')
  if (anyScanning && !scanPollTimer) {
    scanPollTimer = setInterval(load, 3000)
  } else if (!anyScanning && scanPollTimer) {
    clearInterval(scanPollTimer)
    scanPollTimer = null
  }
}

function patchRow(updated: ScanSession) {
  const idx = sessions.value.findIndex(s => s.id === updated.id)
  if (idx >= 0) sessions.value[idx] = updated
}

function patchProgress(id: number, p: ProgressEvent) {
  const row = sessions.value.find(s => s.id === id)
  if (!row) return
  if (p.phase === 'tagging') {
    row.total_files = p.total
    row.tagged_files = p.tagged ?? row.tagged_files
    row.planned_ops = p.planned ?? row.planned_ops
  } else if (p.phase === 'executing') {
    row.total_files = p.total
    row.executed_ops = (p.success ?? 0) + (p.failed ?? 0) + (p.skipped ?? 0)
  }
}

async function refreshSession(id: number) {
  try {
    const res = await getSession(id)
    patchRow(res.data)
  } catch { /* session may be gone */ }
}

async function load() {
  loading.value = true
  try {
    const res = await listSessions()
    sessions.value = res.data
    syncStreams()
  } finally {
    loading.value = false
  }
}

async function loadFilesystems() {
  const res = await listFilesystems()
  filesystems.value = res.data
}

async function loadModelProviders() {
  const res = await listModelProviders()
  modelProviders.value = res.data
}

function providerChipClass(p: string): string {
  const map: Record<string, string> = { openai: 'is-live', claude: 'is-warning', ollama: 'is-muted' }
  return map[p] || 'is-accent'
}

function providerLabel(p: string): string {
  const map: Record<string, string> = { openai: 'OpenAI', claude: 'Claude', ollama: 'Ollama' }
  return map[p] || p
}

function protocolChipClass(protocol: string): string {
  const map: Record<string, string> = {
    local: 'is-muted', sftp: 'is-live', ftp: 'is-accent', smb: 'is-warning', nfs: 'is-danger'
  }
  return map[protocol] || 'is-muted'
}

function statusLabel(status: string): string {
  if (status.startsWith('error')) return t('tasks.statusError')
  return t('tasks.status_' + status) || status
}

function statusChipClass(status: string): string {
  if (status === 'done' || status === 'tagged') return 'is-live'
  if (status.startsWith('error')) return 'is-danger'
  if (status === 'scanning' || status === 'tagging' || status === 'executing') return 'is-warning'
  return 'is-muted'
}

function openNewScanDialog() {
  const lastId = Number(localStorage.getItem('fe_last_fs_id'))
  const exists = filesystems.value.find(f => f.id === lastId)
  const defaultId = exists ? lastId : (filesystems.value.length > 0 ? filesystems.value[0].id : 0)
  newScan.value = {
    filesystemId: defaultId,
    scanPath: '',
    modelProviderId: modelProviders.value.length > 0 ? modelProviders.value[0].id : null,
    allowVision: false,
    excludeCategoryDirs: false,
    filterMode: 'blacklist',
    filterDirs: '',
  }
  showNewDialog.value = true
}

function formatSize(bytes: number): string {
  if (!bytes) return '0'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0; let size = bytes
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return size.toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
}

function canTag(s: ScanSession) {
  return s.status === 'scanned' || s.status === 'tagged' || s.status === 'tagging' || s.status.startsWith('error')
}

function canExecute(s: ScanSession) {
  return s.status === 'tagged' || s.status === 'executing' || s.status.startsWith('error') || s.planned_ops > 0
}

async function handleTag(s: ScanSession) {
  if (s.status === 'tagging') {
    await stopTagging(s.id)
    ElMessage.info(t('tasks.stopping'))
  } else {
    await startTagging(s.id)
    ElMessage.success(t('tasks.taggingStarted'))
  }
  setTimeout(load, 1000)
}

async function handleExecute(s: ScanSession) {
  if (s.status === 'executing') {
    await stopExecute(s.id)
    ElMessage.info(t('tasks.stopping'))
    setTimeout(load, 1000)
  } else {
    execSession.value = s
    execMode.value = 'copy'
    execDialogVisible.value = true
  }
}

async function confirmExecute() {
  if (!execSession.value) return
  execDialogVisible.value = false
  await startExecute(execSession.value.id, execMode.value)
  ElMessage.success(t('tasks.executionStarted'))
  setTimeout(load, 1000)
}

async function showPlans(s: ScanSession) {
  const res = await getPlans(s.id)
  plans.value = res.data
  currentPlansSessionId.value = s.id
  validationResult.value = null
  plansVisible.value = true
}

async function handleExportPlans() {
  try {
    const res = await exportPlansCSV(currentPlansSessionId.value)
    const url = URL.createObjectURL(new Blob([res.data]))
    const a = document.createElement('a')
    a.href = url
    a.download = `plans_session_${currentPlansSessionId.value}.csv`
    a.click()
    URL.revokeObjectURL(url)
    ElMessage.success(t('tasks.exportPlansSuccess'))
  } catch { ElMessage.error(t('common.error')) }
}

async function handleValidate() {
  validating.value = true
  try {
    const res = await validatePlans(currentPlansSessionId.value)
    validationResult.value = res.data
    if (res.data.ok) ElMessage.success(t('tasks.validateOk'))
    else ElMessage.warning(t('tasks.validateIssues', { count: res.data.issues.length }))
  } catch { ElMessage.error(t('common.error')) }
  finally { validating.value = false }
}

async function showStats(s: ScanSession) {
  try {
    const res = await getSessionStats(s.id)
    sessionStatsData.value = res.data
    statsVisible.value = true
  } catch { ElMessage.error(t('common.error')) }
}

async function handleDelete(s: ScanSession) {
  await ElMessageBox.confirm(t('tasks.deleteConfirm'), t('common.confirm'))
  const stream = streams.get(s.id)
  if (stream) { stream.close(); streams.delete(s.id) }
  await deleteSession(s.id)
  ElMessage.success(t('common.deleted'))
  load()
}

async function handleRescan(s: ScanSession) {
  await ElMessageBox.confirm(t('tasks.rescanConfirm'), t('common.confirm'))
  await rescanSession(s.id)
  ElMessage.success(t('tasks.rescanStarted'))
  setTimeout(load, 1000)
}

function handleCommand(cmd: string, row: ScanSession) {
  switch (cmd) {
    case 'run': router.push({ name: 'logs', query: { session: String(row.id) } }); break
    case 'rescan': handleRescan(row); break
    case 'tag': handleTag(row); break
    case 'plans': showPlans(row); break
    case 'execute': handleExecute(row); break
    case 'settings': openSettings(row); break
    case 'stats': showStats(row); break
    case 'delete': handleDelete(row); break
  }
}

function openSettings(s: ScanSession) {
  settingsSessionId.value = s.id
  sessionSettings.value = {
    allow_read_file: s.allow_read_file,
    allow_auto_category: s.allow_auto_category,
    allow_vision: s.allow_vision,
    exclude_category_dirs: s.exclude_category_dirs,
    filter_mode: s.filter_mode || 'blacklist',
    filter_dirs: s.filter_dirs || '',
    model_provider_id: s.model_provider_id || 0,
  }
  settingsVisible.value = true
}

async function saveSettings() {
  await updateSessionConfig(settingsSessionId.value, sessionSettings.value)
  ElMessage.success(t('tasks.settingsSaved'))
  settingsVisible.value = false
  load()
}

async function handleNewScan() {
  if (!newScan.value.filesystemId) { ElMessage.warning(t('tasks.selectFsRequired')); return }
  const res = await createSession({ filesystem_id: newScan.value.filesystemId, scan_path: newScan.value.scanPath, model_provider_id: newScan.value.modelProviderId, exclude_category_dirs: newScan.value.excludeCategoryDirs, filter_mode: newScan.value.filterMode, filter_dirs: newScan.value.filterDirs })
  // CreateSessionRequest has no allow_vision field (backend defaults it off);
  // patch it on only when the user explicitly opted in.
  if (newScan.value.allowVision) {
    await updateSessionConfig(res.data.id, { allow_vision: true })
  }
  ElMessage.success(t('tasks.scanStarted'))
  showNewDialog.value = false
  load()
}
</script>

<style scoped>
.progress-cell {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.progress-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.progress-tokens {
  font-size: 12px;
}
</style>
