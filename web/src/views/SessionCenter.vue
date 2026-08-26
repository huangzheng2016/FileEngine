<template>
  <div class="center-row">
    <!-- Left: session list -->
    <section class="page-card list-pane">
      <el-button type="primary" style="width: 100%" @click="openNewScanDialog">
        <el-icon style="margin-right: 4px"><Plus /></el-icon>{{ $t('tasks.newScan') }}
      </el-button>
      <div class="session-list">
        <div v-if="!sessions.length && !loading" class="empty-state-card empty-state-card--inline">
          <div class="empty-state-card__text">
            <h4 class="empty-state-card__title">{{ $t('tasks.noSessions') }}</h4>
            <p class="empty-state-card__sub">{{ $t('tasks.noSessionsHint') }}</p>
          </div>
        </div>
        <div
          v-for="s in sessions"
          :key="s.id"
          class="session-item"
          :class="{ 'is-active': s.id === selectedId }"
          @click="selectSession(s.id)"
        >
          <div class="session-item__top">
            <code class="session-item__path">{{ s.root_path === '.' ? $t('tasks.allDirectories') : s.root_path }}</code>
            <span class="status-chip" :class="statusChipClass(s.status)">
              <span v-if="isActive(s)" class="status-dot"></span>
              {{ statusLabel(s.status) }}
            </span>
          </div>
          <div class="session-item__meta cell-time">
            #{{ s.id }} · {{ $t('tasks.taggedFiles') }} {{ s.tagged_files }}/{{ s.total_files }}
            <template v-if="s.planned_ops > 0"> · {{ $t('tasks.plannedOps') }} {{ s.planned_ops }}</template>
            <template v-if="s.executed_ops > 0"> · {{ $t('tasks.executedOps') }} {{ s.executed_ops }}</template>
          </div>
        </div>
      </div>
    </section>

    <!-- Right: selected session -->
    <section v-if="current" class="page-card detail-pane">
      <!-- Run status card -->
      <div class="run-card">
        <div class="run-card__head">
          <code class="cell-mono run-card__path">#{{ current.id }} {{ current.root_path === '.' ? $t('tasks.allDirectories') : current.root_path }}</code>
          <span class="status-chip" :class="protocolChipClass(current.protocol)">{{ current.protocol.toUpperCase() }}</span>
          <span v-if="current.allow_vision" class="status-chip is-accent">{{ $t('tasks.visionOn') }}</span>
          <span class="status-chip" :class="statusChipClass(current.status)">
            <span v-if="isActive(current)" class="status-dot"></span>
            {{ statusLabel(current.status) }}
          </span>
          <div class="run-card__actions">
            <el-button v-if="canTag(current)" size="small" :type="current.status === 'tagging' ? 'warning' : 'primary'" @click="handleTag(current)">
              {{ current.status === 'tagging' ? $t('tasks.stopTag') : $t('tasks.tag') }}
            </el-button>
            <el-button v-if="canExecute(current)" size="small" :type="current.status === 'executing' ? 'warning' : 'primary'" plain @click="handleExecute(current)">
              {{ current.status === 'executing' ? $t('tasks.stop') : $t('tasks.execute') }}
            </el-button>
            <el-dropdown trigger="click" @command="(cmd: string) => handleCommand(cmd, current!)">
              <el-button size="small">
                {{ $t('common.actions') }}<el-icon class="el-icon--right"><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="settings"><el-icon><Setting /></el-icon>{{ $t('tasks.settings') }}</el-dropdown-item>
                  <el-dropdown-item command="rescan" :disabled="current.status === 'scanning'"><el-icon><Refresh /></el-icon>{{ $t('tasks.rescan') }}</el-dropdown-item>
                  <el-dropdown-item command="plans" :disabled="current.planned_ops === 0"><el-icon><Document /></el-icon>{{ $t('tasks.plans') }}</el-dropdown-item>
                  <el-dropdown-item command="stats"><el-icon><DataAnalysis /></el-icon>{{ $t('tasks.stats') }}</el-dropdown-item>
                  <el-dropdown-item command="delete" divided><el-icon><Delete /></el-icon>{{ $t('common.delete') }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </div>
        <div class="run-card__meta">
          <span class="metric-chip"><span class="metric-label">{{ $t('tasks.totalFiles') }}</span><span class="metric-value">{{ current.total_files }}</span></span>
          <span class="metric-chip"><span class="metric-label">{{ $t('tasks.taggedFiles') }}</span><span class="metric-value">{{ current.tagged_files }}/{{ current.total_files }}</span></span>
          <span class="metric-chip"><span class="metric-label">{{ $t('tasks.plannedOps') }}</span><span class="metric-value">{{ current.planned_ops }}</span></span>
          <span class="metric-chip"><span class="metric-label">{{ $t('tasks.executedOps') }}</span><span class="metric-value">{{ current.executed_ops }}</span></span>
          <span v-if="current.total_size > 0" class="metric-chip"><span class="metric-label">{{ $t('common.size') }}</span><span class="metric-value">{{ formatSize(current.total_size) }}</span></span>
          <span v-if="current.total_tokens > 0" class="metric-chip"><span class="metric-label">Token</span><span class="metric-value">{{ current.total_tokens.toLocaleString() }}</span></span>
          <el-progress v-if="current.total_files > 0" class="run-card__progress"
            :percentage="Math.round((current.tagged_files / current.total_files) * 100)"
            :stroke-width="6" />

          <!-- Next-step guide (only when nothing is running): inline in the meta row -->
          <div v-if="guide" class="run-card__guide" :class="`is-${guide.tone}`">
            <el-icon :size="14"><Guide /></el-icon>
            <span class="run-card__guide-text">{{ guide.text }}</span>
            <router-link v-if="guide.reviewTo" :to="guide.reviewTo">
              <el-button size="small">{{ $t('tasks.goReview') }}</el-button>
            </router-link>
            <el-button v-if="guide.action" size="small" type="primary" @click="guide.action">{{ guide.actionLabel }}</el-button>
          </div>
        </div>
      </div>

      <!-- SSE worker-lane timeline -->
      <SessionTimeline
        :session-id="current.id"
        :total-tokens-server="current.total_tokens"
        @run-done="refreshSession(current.id)"
        @run-status="refreshSession(current.id)"
      />
    </section>

    <!-- Empty detail -->
    <section v-else class="page-card detail-pane detail-pane--empty">
      <div class="empty-state-card">
        <div class="empty-state-card__icon">
          <el-icon :size="28"><Monitor /></el-icon>
        </div>
        <div class="empty-state-card__text">
          <h4 class="empty-state-card__title">{{ $t('tasks.selectHint') }}</h4>
        </div>
        <el-button type="primary" @click="openNewScanDialog">{{ $t('tasks.newScan') }}</el-button>
      </div>
    </section>

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
    <el-dialog append-to-body v-model="showNewDialog" :title="$t('tasks.newScan')" width="480px">
      <div class="form-stack">
        <section class="scan-group">
          <div class="scan-group__title">{{ $t('tasks.scanGroupSource') }}</div>
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
        </section>

        <section class="scan-group">
          <div class="scan-group__title">{{ $t('tasks.scanGroupModel') }}</div>
          <div class="form-field">
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
        </section>

        <el-collapse class="scan-advanced">
          <el-collapse-item name="advanced">
            <template #title>
              <span class="scan-group__title scan-group__title--muted">{{ $t('tasks.scanGroupAdvanced') }}</span>
            </template>
            <div class="form-field">
              <label class="form-field__label">{{ $t('tasks.allowVision') }}</label>
              <div><el-switch v-model="newScan.allowVision" :disabled="!newScanVisionCapable" /></div>
              <p class="form-field__hint">{{ newScanVisionCapable ? $t('tasks.allowVisionHint') : $t('tasks.visionNotCapable') }}</p>
            </div>
            <div class="form-field">
              <label class="form-field__label">{{ $t('tasks.allowScript') }}</label>
              <div><el-switch v-model="newScan.allowScript" /></div>
              <p class="form-field__hint">{{ $t('tasks.allowScriptHint') }}</p>
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
              <div class="filter-presets">
                <button v-for="preset in filterPresets" :key="preset" type="button" class="link-button" @click="addFilterPreset(preset)">{{ preset }}</button>
              </div>
              <el-input v-model="newScan.filterDirs" type="textarea" :rows="4" :placeholder="$t('tasks.filterDirsPlaceholder')" />
              <p class="form-field__hint">{{ $t('tasks.filterDirsHint') }}</p>
            </div>
          </el-collapse-item>
        </el-collapse>
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
          <label class="form-field__label">{{ $t('tasks.allowScript') }}</label>
          <div><el-switch v-model="sessionSettings.allow_script" /></div>
          <p class="form-field__hint">{{ $t('tasks.allowScriptHint') }}</p>
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
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { listSessions, getSession, createSession, deleteSession, rescanSession, updateSessionConfig, startTagging, stopTagging, startExecute, stopExecute, getPlans, exportPlansCSV, validatePlans, getSessionStats, listFilesystems, listModelProviders } from '../api'
import type { ScanSession, PlanItem, Filesystem, ModelProvider, ValidationResult, SessionStats, ProgressEvent } from '../types'
import { SessionEventStream } from '../composables/sessionEvents'
import SessionTimeline from '../components/SessionTimeline.vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Download, CircleCheck, DataAnalysis, Plus, Refresh, Monitor, Guide } from '@element-plus/icons-vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const sessions = ref<ScanSession[]>([])
const filesystems = ref<Filesystem[]>([])
const modelProviders = ref<ModelProvider[]>([])
const loading = ref(false)
const selectedId = ref<number>(0)

const plansVisible = ref(false)
const plans = ref<PlanItem[]>([])
const showNewDialog = ref(false)
const newScan = ref<{ filesystemId: number; scanPath: string; modelProviderId: number | null; allowVision: boolean; allowScript: boolean; excludeCategoryDirs: boolean; filterMode: string; filterDirs: string }>({ filesystemId: 0, scanPath: '', modelProviderId: null, allowVision: false, allowScript: true, excludeCategoryDirs: false, filterMode: 'blacklist', filterDirs: '' })

// Vision linkage: the switch only makes sense when the chosen model accepts
// image input; otherwise it is disabled and forced off.
const newScanVisionCapable = computed(() => {
  const m = modelProviders.value.find(m => m.id === newScan.value.modelProviderId)
  return !!m?.vision_enabled
})
watch(() => newScan.value.modelProviderId, () => {
  if (!newScanVisionCapable.value) newScan.value.allowVision = false
})

// One-click directory-filter presets (common generated/junk dirs).
const filterPresets = ['.git', 'node_modules', '@eaDir']
function addFilterPreset(name: string) {
  const lines = newScan.value.filterDirs.split('\n').map(s => s.trim()).filter(Boolean)
  if (!lines.includes(name)) lines.push(name)
  newScan.value.filterDirs = lines.join('\n')
}
const execDialogVisible = ref(false)
const execMode = ref('copy')
const execSession = ref<ScanSession | null>(null)
const settingsVisible = ref(false)
const sessionSettings = ref({ allow_read_file: true, allow_auto_category: false, allow_vision: false, allow_script: true, exclude_category_dirs: false, filter_mode: 'blacklist', filter_dirs: '', model_provider_id: 0 })
const validating = ref(false)
const validationResult = ref<ValidationResult | null>(null)
const statsVisible = ref(false)
const sessionStatsData = ref<SessionStats | null>(null)

// ===== Event-driven live updates =====
// Agent runs (tagging / executing) stream over SSE per session; scanning has no
// run events, so a light poll runs only while at least one session is scanning.
const streams = new Map<number, SessionEventStream>()
let scanPollTimer: ReturnType<typeof setInterval> | null = null

const current = computed(() => sessions.value.find(s => s.id === selectedId.value) || null)

onMounted(async () => {
  await Promise.all([load(), loadFilesystems(), loadModelProviders()])
  // Deep links: ?session=N (legacy /logs redirects here), ?new=1 opens the scan dialog.
  const qSession = Number(route.query.session)
  if (qSession && sessions.value.find(s => s.id === qSession)) {
    selectedId.value = qSession
  } else if (sessions.value.length > 0) {
    selectedId.value = sessions.value[0].id
  }
  if (route.query.new === '1') {
    openNewScanDialog()
    router.replace({ query: { ...route.query, new: undefined } })
  }
})

// Deep links re-applied while already mounted (e.g. /logs?session=N
// redirecting onto an active /tasks view — review P2-3).
watch(() => route.query.session, (qSession) => {
  const sessId = Number(qSession)
  if (sessId && sessId !== selectedId.value && sessions.value.find(s => s.id === sessId)) {
    selectedId.value = sessId
  }
})

onUnmounted(() => {
  for (const s of streams.values()) s.close()
  streams.clear()
  if (scanPollTimer) { clearInterval(scanPollTimer); scanPollTimer = null }
})

function selectSession(id: number) {
  selectedId.value = id
  router.replace({ query: { session: String(id) } })
}

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
    // A run reaching its terminal state lands here via run_done — reconcile
    // streams so the finished session's EventSource is closed (review P2-1).
    syncStreams()
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

// ===== Next-step guide =====
interface Guide {
  tone: 'accent' | 'success' | 'danger'
  text: string
  actionLabel?: string
  action?: () => void
  reviewTo?: any
}

const guide = computed<Guide | null>(() => {
  const s = current.value
  if (!s || isActive(s) || s.status === 'scanning') return null
  if (s.status.startsWith('error')) {
    return {
      tone: 'danger',
      text: t('tasks.guideError', { status: s.status }),
      actionLabel: s.planned_ops > 0 ? t('tasks.execute') : t('tasks.tag'),
      action: () => (s.planned_ops > 0 ? handleExecute(s) : handleTag(s)),
    }
  }
  if (s.status === 'scanned') {
    return {
      tone: 'accent',
      text: t('tasks.guideScanned', { count: s.total_files }),
      actionLabel: t('tasks.goTag'),
      action: () => handleTag(s),
    }
  }
  if (s.status === 'tagged' && s.planned_ops > 0) {
    return {
      tone: 'success',
      text: t('tasks.guideTagged', { count: s.planned_ops }),
      actionLabel: t('tasks.goExecute'),
      action: () => handleExecute(s),
      reviewTo: { name: 'files', query: { fs: String(s.filesystem_id), session: String(s.id) } },
    }
  }
  if (s.status === 'done') {
    return {
      tone: 'success',
      text: t('tasks.guideDone', { count: s.executed_ops }),
      reviewTo: { name: 'files', query: { fs: String(s.filesystem_id), session: String(s.id) } },
    }
  }
  return null
})

// ===== Labels =====
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
  if (status === 'scanning' || status === 'tagging' || status === 'executing' || status === 'tagged_partial') return 'is-warning'
  return 'is-muted'
}

function formatSize(bytes: number): string {
  if (!bytes) return '0'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0; let size = bytes
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return size.toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
}

// ===== Actions =====
function canTag(s: ScanSession) {
  return s.status === 'scanned' || s.status === 'tagged' || s.status === 'tagged_partial' || s.status === 'tagging' || s.status.startsWith('error')
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
  validationResult.value = null
  plansVisible.value = true
}

async function handleExportPlans() {
  if (!current.value) return
  try {
    const res = await exportPlansCSV(current.value.id)
    const url = URL.createObjectURL(new Blob([res.data]))
    const a = document.createElement('a')
    a.href = url
    a.download = `plans_session_${current.value.id}.csv`
    a.click()
    URL.revokeObjectURL(url)
    ElMessage.success(t('tasks.exportPlansSuccess'))
  } catch { ElMessage.error(t('common.error')) }
}

async function handleValidate() {
  if (!current.value) return
  validating.value = true
  try {
    const res = await validatePlans(current.value.id)
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
  if (selectedId.value === s.id) selectedId.value = 0
  await load()
  if (!selectedId.value && sessions.value.length > 0) selectSession(sessions.value[0].id)
}

async function handleRescan(s: ScanSession) {
  await ElMessageBox.confirm(t('tasks.rescanConfirm'), t('common.confirm'))
  await rescanSession(s.id)
  ElMessage.success(t('tasks.rescanStarted'))
  setTimeout(load, 1000)
}

function handleCommand(cmd: string, row: ScanSession) {
  switch (cmd) {
    case 'rescan': handleRescan(row); break
    case 'plans': showPlans(row); break
    case 'settings': openSettings(row); break
    case 'stats': showStats(row); break
    case 'delete': handleDelete(row); break
  }
}

function openSettings(s: ScanSession) {
  sessionSettings.value = {
    allow_read_file: s.allow_read_file,
    allow_auto_category: s.allow_auto_category,
    allow_vision: s.allow_vision,
    allow_script: s.allow_script,
    exclude_category_dirs: s.exclude_category_dirs,
    filter_mode: s.filter_mode || 'blacklist',
    filter_dirs: s.filter_dirs || '',
    model_provider_id: s.model_provider_id || 0,
  }
  settingsVisible.value = true
}

async function saveSettings() {
  if (!current.value) return
  await updateSessionConfig(current.value.id, sessionSettings.value)
  ElMessage.success(t('tasks.settingsSaved'))
  settingsVisible.value = false
  load()
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
    allowScript: true,
    excludeCategoryDirs: false,
    filterMode: 'blacklist',
    filterDirs: '',
  }
  showNewDialog.value = true
}

async function handleNewScan() {
  if (!newScan.value.filesystemId) { ElMessage.warning(t('tasks.selectFsRequired')); return }
  const res = await createSession({ filesystem_id: newScan.value.filesystemId, scan_path: newScan.value.scanPath, model_provider_id: newScan.value.modelProviderId, exclude_category_dirs: newScan.value.excludeCategoryDirs, filter_mode: newScan.value.filterMode, filter_dirs: newScan.value.filterDirs, allow_vision: newScan.value.allowVision && newScanVisionCapable.value })
  // allow_script is the mirror image: the backend defaults it ON, so patch
  // only when the user explicitly opted out.
  if (!newScan.value.allowScript) {
    try {
      await updateSessionConfig(res.data.id, { allow_script: false })
    } catch (e: any) {
      ElMessage.warning(t('tasks.allowScriptPatchFailed', { error: e.response?.data?.error || e.message }))
    }
  }
  ElMessage.success(t('tasks.scanStarted'))
  showNewDialog.value = false
  await load()
  selectSession(res.data.id)
}
</script>

<style scoped>
.center-row {
  display: flex;
  gap: 16px;
  flex: 1;
  min-height: 0;
  align-items: stretch;
}

/* ===== Left: session list ===== */
.list-pane {
  width: 300px;
  flex-shrink: 0;
  padding: 18px;
  gap: 14px;
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.session-list {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.session-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-soft);
  background: var(--surface-elevated);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-spring);
}

.session-item:hover {
  border-color: var(--floating-border);
  transform: translateY(-1px);
}

.session-item.is-active {
  background: var(--accent-soft);
  border-color: var(--floating-border);
}

.session-item__top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.session-item__path {
  font-family: var(--font-mono);
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.session-item__meta {
  font-size: 11.5px;
}

/* ===== Right: detail ===== */
.detail-pane {
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  gap: 14px;
}

.detail-pane--empty {
  justify-content: center;
}

.run-card {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 18px;
  border-radius: var(--radius-md);
  background: var(--surface-elevated);
  border: 1px solid var(--border-soft);
  box-shadow: var(--inner-highlight);
}

.run-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.run-card__path {
  font-size: 13px;
  font-weight: 600;
}

.run-card__actions {
  margin-left: auto;
  display: flex;
  gap: 8px;
  align-items: center;
}

.run-card__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
}

.run-card__progress {
  flex: 1 1 120px;
  min-width: 120px;
  max-width: 320px;
  margin: 0;
}

/* ===== Inline next-step guide (merged into the meta row) ===== */
.run-card__guide {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-left: auto;
  font-size: 13px;
  font-weight: 500;
}

.run-card__guide.is-accent {
  color: var(--accent);
}

.run-card__guide.is-success {
  color: var(--success);
}

.run-card__guide.is-danger {
  color: var(--danger);
}

.run-card__guide-text {
  min-width: 0;
  color: var(--text-primary);
}
</style>
