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
        <el-table-column prop="root_path" :label="$t('tasks.rootPath')" min-width="200" show-overflow-tooltip />
        <el-table-column prop="protocol" :label="$t('tasks.protocol')" width="100" />
        <el-table-column :label="$t('common.status')" width="140">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status)" size="small">{{ row.status }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="$t('tasks.progress')" min-width="200">
          <template #default="{ row }">
            <div style="font-size: 12px; color: #666">
              {{ $t('tasks.totalFiles') }}: {{ row.total_files }} | {{ $t('tasks.taggedFiles') }}: {{ row.tagged_files }} | {{ $t('tasks.plannedOps') }}: {{ row.planned_ops }} | {{ $t('tasks.executedOps') }}: {{ row.executed_ops }}
            </div>
            <el-progress v-if="row.total_files > 0"
              :percentage="Math.round((row.tagged_files / row.total_files) * 100)"
              :stroke-width="6" style="margin-top: 4px" />
          </template>
        </el-table-column>
        <el-table-column :label="$t('common.actions')" width="420" fixed="right">
          <template #default="{ row }">
            <el-button-group size="small">
              <el-button @click="handleRescan(row)" :disabled="row.status === 'scanning'">{{ $t('tasks.rescan') }}</el-button>
              <el-button @click="handleTag(row)" :disabled="!canTag(row)">
                {{ row.status === 'tagging' ? $t('tasks.stopTag') : $t('tasks.tag') }}
              </el-button>
              <el-button @click="showPlans(row)" :disabled="row.planned_ops === 0">{{ $t('tasks.plans') }}</el-button>
              <el-button @click="handleExecute(row)" :disabled="!canExecute(row)" type="warning">
                {{ row.status === 'executing' ? $t('tasks.stop') : $t('tasks.execute') }}
              </el-button>
              <el-button @click="handleDelete(row)" type="danger">{{ $t('common.delete') }}</el-button>
            </el-button-group>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- Plans dialog -->
    <el-dialog v-model="plansVisible" :title="$t('tasks.executionPlans')" width="80%">
      <el-table :data="plans" style="width: 100%" max-height="500">
        <el-table-column prop="name" :label="$t('tasks.planName')" width="200" />
        <el-table-column prop="operation" :label="$t('tasks.planOp')" width="80">
          <template #default="{ row }">
            <el-tag :type="row.operation === 'move' ? 'warning' : 'info'" size="small">
              {{ row.operation === 'move' ? $t('tasks.move') : $t('tasks.copy') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="original_path" :label="$t('tasks.planFrom')" min-width="300" show-overflow-tooltip />
        <el-table-column prop="new_path" :label="$t('tasks.planTo')" min-width="300" show-overflow-tooltip />
        <el-table-column prop="file_type" :label="$t('common.type')" width="100" />
      </el-table>
    </el-dialog>

    <!-- New scan dialog -->
    <el-dialog v-model="showNewDialog" :title="$t('tasks.newScan')" width="450px">
      <el-form label-width="120px">
        <el-form-item :label="$t('tasks.filesystem')">
          <el-select v-model="newScan.filesystemId" style="width: 100%" :placeholder="$t('tasks.selectFs')">
            <el-option v-for="fs in filesystems" :key="fs.id" :label="`${fs.name} (${fs.protocol}://${fs.base_path})`" :value="fs.id" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('tasks.scanPath')">
          <el-input v-model="newScan.scanPath" :placeholder="$t('tasks.scanPathHint')" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showNewDialog = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" @click="handleNewScan">{{ $t('tasks.newScan') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { listSessions, createSession, deleteSession, rescanSession, startTagging, stopTagging, startExecute, stopExecute, getPlans, listFilesystems } from '../api'
import type { ScanSession, PlanItem, Filesystem } from '../types'
import { ElMessage, ElMessageBox } from 'element-plus'

const { t } = useI18n()
const sessions = ref<ScanSession[]>([])
const filesystems = ref<Filesystem[]>([])
const plansVisible = ref(false)
const plans = ref<PlanItem[]>([])
const showNewDialog = ref(false)
const newScan = ref({ filesystemId: 0, scanPath: '' })

let timer: ReturnType<typeof setInterval>

onMounted(() => {
  load()
  loadFilesystems()
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

function openNewScanDialog() {
  const lastId = Number(localStorage.getItem('fe_last_fs_id'))
  const exists = filesystems.value.find(f => f.id === lastId)
  const defaultId = exists ? lastId : (filesystems.value.length > 0 ? filesystems.value[0].id : 0)
  newScan.value = {
    filesystemId: defaultId,
    scanPath: '',
  }
  showNewDialog.value = true
}

function statusType(status: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  if (status === 'done') return 'success'
  if (status.startsWith('error')) return 'danger'
  if (status === 'scanning' || status === 'tagging' || status === 'executing') return 'warning'
  return 'info'
}

function canTag(s: ScanSession) {
  return s.status === 'scanned' || s.status === 'tagged' || s.status === 'tagging'
}

function canExecute(s: ScanSession) {
  return s.status === 'tagged' || s.status === 'executing' || s.planned_ops > 0
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
  } else {
    await ElMessageBox.confirm(t('tasks.executeConfirm', { count: s.planned_ops }), t('common.confirm'))
    await startExecute(s.id)
    ElMessage.success(t('tasks.executionStarted'))
  }
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

async function handleNewScan() {
  if (!newScan.value.filesystemId) { ElMessage.warning(t('tasks.selectFsRequired')); return }
  await createSession({ filesystem_id: newScan.value.filesystemId, scan_path: newScan.value.scanPath })
  ElMessage.success(t('tasks.scanStarted'))
  showNewDialog.value = false
  load()
}
</script>
