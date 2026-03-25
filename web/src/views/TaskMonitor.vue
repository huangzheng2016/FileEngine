<template>
  <div>
    <el-card style="margin-bottom: 16px">
      <template #header>
        <div style="display: flex; justify-content: space-between; align-items: center">
          <span>{{ $t('tasks.title') }}</span>
          <el-button type="primary" @click="openNewScanDialog">{{ $t('tasks.newScan') }}</el-button>
        </div>
      </template>
      <el-table :data="sessions" style="width: 100%">
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="root_path" :label="$t('tasks.rootPath')" min-width="200" show-overflow-tooltip>
          <template #default="{ row }">{{ row.root_path === '.' ? $t('tasks.allDirectories') : row.root_path }}</template>
        </el-table-column>
        <el-table-column prop="protocol" :label="$t('tasks.protocol')" width="100">
          <template #default="{ row }">
            <el-tag :type="protocolTagType(row.protocol)" size="small" effect="dark">{{ row.protocol.toUpperCase() }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="$t('common.status')" width="140">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="$t('tasks.progress')" min-width="280">
          <template #default="{ row }">
            <div style="font-size: 12px; color: #666">
              {{ $t('tasks.totalFiles') }}: {{ row.total_files }} ({{ formatSize(row.total_size) }}) | {{ $t('tasks.taggedFiles') }}: {{ row.tagged_files }} | {{ $t('tasks.plannedOps') }}: {{ row.planned_ops }} | {{ $t('tasks.executedOps') }}: {{ row.executed_ops }}
            </div>
            <div v-if="row.total_tokens > 0" style="font-size: 12px; color: #999; margin-top: 2px">
              Token: {{ row.prompt_tokens.toLocaleString() }}↑ {{ row.completion_tokens.toLocaleString() }}↓ = {{ row.total_tokens.toLocaleString() }}
              <span v-if="row.total_files > 0"> | {{ (row.total_tokens / row.total_files).toFixed(0) }}/{{ $t('files.file') }}</span>
              <span v-if="row.total_size > 0"> | {{ (row.total_tokens / (row.total_size / 1048576)).toFixed(0) }}/MB</span>
            </div>
            <el-progress v-if="row.total_files > 0"
              :percentage="Math.round((row.tagged_files / row.total_files) * 100)"
              :stroke-width="6" style="margin-top: 4px" />
          </template>
        </el-table-column>
        <el-table-column :label="$t('common.actions')" width="160" fixed="right">
          <template #default="{ row }">
            <el-dropdown trigger="click" @command="(cmd: string) => handleCommand(cmd, row)">
              <el-button size="small">
                {{ $t('common.actions') }}<el-icon class="el-icon--right"><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="settings"><el-icon><Setting /></el-icon>{{ $t('tasks.settings') }}</el-dropdown-item>
                  <el-dropdown-item command="rescan" :disabled="row.status === 'scanning'"><el-icon><Refresh /></el-icon>{{ $t('tasks.rescan') }}</el-dropdown-item>
                  <el-dropdown-item command="tag" :disabled="!canTag(row)"><el-icon><PriceTag /></el-icon>{{ row.status === 'tagging' ? $t('tasks.stopTag') : $t('tasks.tag') }}</el-dropdown-item>
                  <el-dropdown-item command="plans" :disabled="row.planned_ops === 0"><el-icon><Document /></el-icon>{{ $t('tasks.plans') }}</el-dropdown-item>
                  <el-dropdown-item command="execute" :disabled="!canExecute(row)"><el-icon><VideoPlay /></el-icon>{{ row.status === 'executing' ? $t('tasks.stop') : $t('tasks.execute') }}</el-dropdown-item>
                  <el-dropdown-item command="delete" divided><el-icon><Delete /></el-icon>{{ $t('common.delete') }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- Plans dialog -->
    <el-dialog v-model="plansVisible" :title="$t('tasks.executionPlans')" width="80%">
      <el-table :data="plans" style="width: 100%" max-height="500">
        <el-table-column prop="name" :label="$t('tasks.planName')" width="200" />
        <el-table-column prop="operation" :label="$t('tasks.planOp')" width="80">
          <template #default>
            <el-tag type="info" size="small">{{ $t('tasks.planned') }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="original_path" :label="$t('tasks.planFrom')" min-width="300" show-overflow-tooltip />
        <el-table-column prop="new_path" :label="$t('tasks.planTo')" min-width="300" show-overflow-tooltip />
        <el-table-column prop="file_type" :label="$t('common.type')" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.file_type === 'directory'" type="warning" size="small" effect="plain">{{ $t('files.directory') }}</el-tag>
            <el-tag v-else size="small" effect="plain">{{ $t('files.file') }}</el-tag>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <!-- New scan dialog -->
    <el-dialog v-model="showNewDialog" :title="$t('tasks.newScan')" width="450px">
      <el-form label-width="120px">
        <el-form-item :label="$t('tasks.filesystem')">
          <el-select v-model="newScan.filesystemId" style="width: 100%" :placeholder="$t('tasks.selectFs')">
            <el-option v-for="fs in filesystems" :key="fs.id" :label="`[${fs.protocol.toUpperCase()}] ${fs.name}`" :value="fs.id">
              <span style="display: flex; align-items: center; gap: 6px">
                <el-tag :type="protocolTagType(fs.protocol)" size="small" effect="dark" style="width: 48px; text-align: center">{{ fs.protocol.toUpperCase() }}</el-tag>
                {{ fs.name }}
              </span>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('tasks.scanPath')">
          <el-input v-model="newScan.scanPath" :placeholder="$t('tasks.scanPathHint')" />
        </el-form-item>
        <el-form-item :label="$t('models.selectModel')">
          <el-select v-model="newScan.modelProviderId" style="width: 100%" clearable :placeholder="$t('models.noModel')">
            <el-option v-for="m in modelProviders" :key="m.id" :label="`[${providerLabel(m.provider)}] ${m.name}`" :value="m.id">
              <span style="display: flex; align-items: center; gap: 6px">
                <el-tag :type="providerTagType(m.provider)" size="small" effect="dark">{{ providerLabel(m.provider) }}</el-tag>
                {{ m.name }} ({{ m.model }})
              </span>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('tasks.excludeCategoryDirs')">
          <el-switch v-model="newScan.excludeCategoryDirs" />
          <p style="font-size: 12px; color: #909399; margin: 4px 0 0">{{ $t('tasks.excludeCategoryDirsHint') }}</p>
        </el-form-item>
        <el-form-item :label="$t('tasks.filterMode')">
          <el-radio-group v-model="newScan.filterMode">
            <el-radio value="blacklist">{{ $t('tasks.filterBlacklist') }}</el-radio>
            <el-radio value="whitelist">{{ $t('tasks.filterWhitelist') }}</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('tasks.filterDirs')">
          <el-input v-model="newScan.filterDirs" type="textarea" :rows="4" :placeholder="$t('tasks.filterDirsPlaceholder')" />
          <p style="font-size: 12px; color: #909399; margin: 4px 0 0">{{ $t('tasks.filterDirsHint') }}</p>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showNewDialog = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" @click="handleNewScan">{{ $t('tasks.newScan') }}</el-button>
      </template>
    </el-dialog>

    <!-- Execute mode dialog -->
    <el-dialog v-model="execDialogVisible" :title="$t('tasks.execute')" width="400px">
      <p style="margin-bottom: 16px">{{ $t('tasks.executeConfirm', { count: execSession?.planned_ops || 0 }) }}</p>
      <el-form label-width="100px">
        <el-form-item :label="$t('tasks.execMode')">
          <el-radio-group v-model="execMode">
            <el-radio value="copy">{{ $t('tasks.copy') }} {{ $t('tasks.execCopyHint') }}</el-radio>
            <el-radio value="move">{{ $t('tasks.move') }} {{ $t('tasks.execMoveHint') }}</el-radio>
          </el-radio-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="execDialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="warning" @click="confirmExecute">{{ $t('tasks.execute') }}</el-button>
      </template>
    </el-dialog>

    <!-- Session settings dialog -->
    <el-dialog v-model="settingsVisible" :title="$t('tasks.settings')" width="420px">
      <el-form label-position="top">
        <el-form-item :label="$t('models.selectModel')">
          <el-select v-model="sessionSettings.model_provider_id" style="width: 100%" clearable :placeholder="$t('models.noModel')">
            <el-option v-for="m in modelProviders" :key="m.id" :label="`[${providerLabel(m.provider)}] ${m.name}`" :value="m.id">
              <span style="display: flex; align-items: center; gap: 6px">
                <el-tag :type="providerTagType(m.provider)" size="small" effect="dark">{{ providerLabel(m.provider) }}</el-tag>
                {{ m.name }} ({{ m.model }})
              </span>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('tasks.allowReadFile')">
          <el-switch v-model="sessionSettings.allow_read_file" />
          <p style="font-size: 12px; color: #909399; margin: 4px 0 0">{{ $t('tasks.allowReadFileHint') }}</p>
        </el-form-item>
        <el-form-item :label="$t('tasks.allowAutoCategory')">
          <el-switch v-model="sessionSettings.allow_auto_category" />
          <p style="font-size: 12px; color: #909399; margin: 4px 0 0">{{ $t('tasks.allowAutoCategoryHint') }}</p>
        </el-form-item>
        <el-form-item :label="$t('tasks.excludeCategoryDirs')">
          <el-switch v-model="sessionSettings.exclude_category_dirs" />
          <p style="font-size: 12px; color: #909399; margin: 4px 0 0">{{ $t('tasks.excludeCategoryDirsHint') }}</p>
        </el-form-item>
        <el-form-item :label="$t('tasks.filterMode')">
          <el-radio-group v-model="sessionSettings.filter_mode">
            <el-radio value="blacklist">{{ $t('tasks.filterBlacklist') }}</el-radio>
            <el-radio value="whitelist">{{ $t('tasks.filterWhitelist') }}</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('tasks.filterDirs')">
          <el-input v-model="sessionSettings.filter_dirs" type="textarea" :rows="4" :placeholder="$t('tasks.filterDirsPlaceholder')" />
          <p style="font-size: 12px; color: #909399; margin: 4px 0 0">{{ $t('tasks.filterDirsHint') }}</p>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="settingsVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" @click="saveSettings">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { listSessions, createSession, deleteSession, rescanSession, updateSessionConfig, startTagging, stopTagging, startExecute, stopExecute, getPlans, listFilesystems, listModelProviders } from '../api'
import type { ScanSession, PlanItem, Filesystem, ModelProvider } from '../types'
import { ElMessage, ElMessageBox } from 'element-plus'

const { t } = useI18n()
const sessions = ref<ScanSession[]>([])
const filesystems = ref<Filesystem[]>([])
const modelProviders = ref<ModelProvider[]>([])
const plansVisible = ref(false)
const plans = ref<PlanItem[]>([])
const showNewDialog = ref(false)
const newScan = ref({ filesystemId: 0, scanPath: '', modelProviderId: 0, excludeCategoryDirs: false, filterMode: 'blacklist', filterDirs: '' })
const execDialogVisible = ref(false)
const execMode = ref('copy')
const execSession = ref<ScanSession | null>(null)
const settingsVisible = ref(false)
const settingsSessionId = ref(0)
const sessionSettings = ref({ allow_read_file: true, allow_auto_category: false, exclude_category_dirs: false, filter_mode: 'blacklist', filter_dirs: '', model_provider_id: 0 })

let timer: ReturnType<typeof setInterval>

onMounted(() => {
  load()
  loadFilesystems()
  loadModelProviders()
  timer = setInterval(load, 5000)
})

onUnmounted(() => clearInterval(timer))

async function load() {
  const res = await listSessions()
  sessions.value = res.data
}

async function loadFilesystems() {
  const res = await listFilesystems()
  filesystems.value = res.data
}

async function loadModelProviders() {
  const res = await listModelProviders()
  modelProviders.value = res.data
}

function providerTagType(p: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  const map: Record<string, '' | 'success' | 'warning' | 'info' | 'danger'> = {
    openai: 'success', claude: 'warning', ollama: 'info'
  }
  return map[p] || 'info'
}

function providerLabel(p: string): string {
  const map: Record<string, string> = { openai: 'OpenAI', claude: 'Claude', ollama: 'Ollama' }
  return map[p] || p
}

function protocolTagType(protocol: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  const map: Record<string, '' | 'success' | 'warning' | 'info' | 'danger'> = {
    local: 'info', sftp: 'success', ftp: '', smb: 'warning', nfs: 'danger'
  }
  return map[protocol] || 'info'
}

function statusLabel(status: string): string {
  if (status.startsWith('error')) return t('tasks.statusError')
  return t('tasks.status_' + status) || status
}

function openNewScanDialog() {
  const lastId = Number(localStorage.getItem('fe_last_fs_id'))
  const exists = filesystems.value.find(f => f.id === lastId)
  const defaultId = exists ? lastId : (filesystems.value.length > 0 ? filesystems.value[0].id : 0)
  newScan.value = {
    filesystemId: defaultId,
    scanPath: '',
    modelProviderId: modelProviders.value.length > 0 ? modelProviders.value[0].id : 0,
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

function statusType(status: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  if (status === 'done') return 'success'
  if (status.startsWith('error')) return 'danger'
  if (status === 'scanning' || status === 'tagging' || status === 'executing') return 'warning'
  return 'info'
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
  plansVisible.value = true
}

async function handleDelete(s: ScanSession) {
  await ElMessageBox.confirm(t('tasks.deleteConfirm'), t('common.confirm'))
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
    case 'rescan': handleRescan(row); break
    case 'tag': handleTag(row); break
    case 'plans': showPlans(row); break
    case 'execute': handleExecute(row); break
    case 'settings': openSettings(row); break
    case 'delete': handleDelete(row); break
  }
}

function openSettings(s: ScanSession) {
  settingsSessionId.value = s.id
  sessionSettings.value = {
    allow_read_file: s.allow_read_file,
    allow_auto_category: s.allow_auto_category,
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
  await createSession({ filesystem_id: newScan.value.filesystemId, scan_path: newScan.value.scanPath, model_provider_id: newScan.value.modelProviderId, exclude_category_dirs: newScan.value.excludeCategoryDirs, filter_mode: newScan.value.filterMode, filter_dirs: newScan.value.filterDirs })
  ElMessage.success(t('tasks.scanStarted'))
  showNewDialog.value = false
  load()
}
</script>
