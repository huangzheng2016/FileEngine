<template>
  <section class="page-card">
    <header class="page-header">
      <div class="page-titles">
        <span class="page-eyebrow">FileEngine</span>
        <h2 class="page-heading">{{ $t('filesystems.title') }}</h2>
        <p class="page-sub">{{ $t('filesystems.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button type="primary" @click="openDialog()">
          <el-icon style="margin-right: 4px"><Plus /></el-icon>{{ $t('filesystems.addFs') }}
        </el-button>
      </div>
    </header>

    <div class="page-body">
      <el-table :data="filesystems" v-loading="loading" style="width: 100%">
        <el-table-column prop="name" :label="$t('common.name')" width="160">
          <template #default="{ row }"><span class="cell-phone">{{ row.name }}</span></template>
        </el-table-column>
        <el-table-column prop="protocol" :label="$t('filesystems.protocol')" width="110">
          <template #default="{ row }">
            <span class="status-chip" :class="protocolChipClass(row.protocol)">{{ row.protocol.toUpperCase() }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="base_path" :label="$t('filesystems.basePath')" min-width="220" show-overflow-tooltip>
          <template #default="{ row }"><code class="cell-mono">{{ row.base_path }}</code></template>
        </el-table-column>
        <el-table-column prop="host" :label="$t('filesystems.host')" width="160" show-overflow-tooltip>
          <template #default="{ row }"><span class="cell-time">{{ row.host || '-' }}</span></template>
        </el-table-column>
        <el-table-column prop="description" :label="$t('common.description')" min-width="200" show-overflow-tooltip />
        <el-table-column :label="$t('common.actions')" width="160" fixed="right">
          <template #default="{ row }">
            <button class="link-button" @click="openDialog(row)">{{ $t('common.edit') }}</button>
            <button class="link-button is-danger" @click="handleDelete(row.id)">{{ $t('common.delete') }}</button>
          </template>
        </el-table-column>
        <template #empty>
          <div class="empty-state-card empty-state-card--inline">
            <div class="empty-state-card__icon">
              <el-icon :size="24"><Connection /></el-icon>
            </div>
            <div class="empty-state-card__text">
              <h4 class="empty-state-card__title">{{ $t('filesystems.title') }}</h4>
              <p class="empty-state-card__sub">{{ $t('filesystems.subtitle') }}</p>
            </div>
          </div>
        </template>
      </el-table>
    </div>

    <el-dialog append-to-body v-model="dialogVisible" :title="editingId ? $t('filesystems.editFs') : $t('filesystems.addFs')" width="550px">
      <div class="form-stack">
        <div class="form-field">
          <label class="form-field__label is-required">{{ $t('common.name') }}</label>
          <el-input v-model="form.name" />
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('common.description') }}</label>
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </div>
        <div class="form-row">
          <div class="form-field">
            <label class="form-field__label">{{ $t('filesystems.protocol') }}</label>
            <el-select v-model="form.protocol">
              <el-option label="Local" value="local" />
              <el-option label="SFTP" value="sftp" />
              <el-option label="FTP" value="ftp" />
              <el-option label="SMB" value="smb" />
              <el-option label="NFS" value="nfs" />
            </el-select>
            <p class="form-field__hint">{{ protocolCapsHint }}</p>
          </div>
          <div class="form-field">
            <label class="form-field__label">{{ $t('filesystems.basePath') }}</label>
            <el-input v-model="form.base_path" placeholder="/" />
          </div>
        </div>
        <template v-if="form.protocol !== 'local'">
          <div class="form-row">
            <div class="form-field">
              <label class="form-field__label">{{ $t('filesystems.host') }}</label>
              <el-input v-model="form.host" />
            </div>
            <div class="form-field">
              <label class="form-field__label">{{ $t('filesystems.port') }}</label>
              <el-input-number v-model="form.port" :min="0" style="width: 100%" />
            </div>
          </div>
          <div class="form-row">
            <div class="form-field">
              <label class="form-field__label">{{ $t('filesystems.username') }}</label>
              <el-input v-model="form.username" />
            </div>
            <div class="form-field">
              <label class="form-field__label">{{ $t('filesystems.password') }}</label>
              <el-input v-model="form.password" type="password" show-password :placeholder="editingId ? $t('filesystems.keepPassword') : ''" />
            </div>
          </div>
          <div v-if="form.protocol === 'sftp'" class="form-field">
            <label class="form-field__label">{{ $t('filesystems.keyPath') }}</label>
            <el-input v-model="form.key_path" />
          </div>
        </template>
      </div>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="success" @click="handleTestConn" :loading="testingConn">{{ $t('filesystems.testConn') }}</el-button>
        <el-button type="primary" @click="handleSave">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { listFilesystems, createFilesystem, updateFilesystem, deleteFilesystem, testFilesystemConnection } from '../api'
import type { Filesystem } from '../types'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Connection } from '@element-plus/icons-vue'

const { t } = useI18n()
const filesystems = ref<Filesystem[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
const testingConn = ref(false)
const form = ref({
  name: '', description: '', protocol: 'local', base_path: '',
  host: '', port: 0, username: '', password: '', key_path: ''
})

onMounted(() => load())

const defaultPorts: Record<string, number> = {
  sftp: 22, ftp: 21, smb: 445, nfs: 2049, local: 0,
}

watch(() => form.value.protocol, (protocol, oldProtocol) => {
  // Only auto-set port when it's still on the old protocol's default (or 0)
  const oldDefault = defaultPorts[oldProtocol] ?? 0
  if (form.value.port === 0 || form.value.port === oldDefault) {
    form.value.port = defaultPorts[protocol] ?? 0
  }
})

function protocolChipClass(protocol: string): string {
  const map: Record<string, string> = {
    local: 'is-muted', sftp: 'is-live', ftp: 'is-accent', smb: 'is-warning', nfs: 'is-danger'
  }
  return map[protocol] || 'is-muted'
}

// Per-protocol capability hint (random access / archive support), matching
// the backend matrix documented in CLAUDE.md.
const protocolCapsHint = computed(() => {
  const keys: Record<string, string> = {
    local: 'filesystems.capsLocal', sftp: 'filesystems.capsSftp', smb: 'filesystems.capsSmb',
    nfs: 'filesystems.capsNfs', ftp: 'filesystems.capsFtp',
  }
  const key = keys[form.value.protocol]
  return key ? t(key) : ''
})

async function load() {
  loading.value = true
  try {
    const res = await listFilesystems()
    filesystems.value = res.data
  } finally {
    loading.value = false
  }
}

function openDialog(fs?: Filesystem) {
  if (fs) {
    editingId.value = fs.id
    form.value = {
      name: fs.name, description: fs.description, protocol: fs.protocol,
      base_path: fs.base_path, host: fs.host, port: fs.port,
      username: fs.username, password: '', key_path: fs.key_path
    }
  } else {
    editingId.value = null
    form.value = { name: '', description: '', protocol: 'local', base_path: '', host: '', port: 0, username: '', password: '', key_path: '' }
  }
  dialogVisible.value = true
}

async function handleSave() {
  const data = { ...form.value }
  if (editingId.value) {
    // Send "****" to preserve existing password if user didn't change it
    if (!data.password) data.password = '****'
    await updateFilesystem(editingId.value, data)
  } else {
    await createFilesystem(data)
  }
  ElMessage.success(t('common.saved'))
  dialogVisible.value = false
  load()
}

async function handleDelete(id: number) {
  await ElMessageBox.confirm(t('filesystems.deleteConfirm'), t('common.confirm'))
  await deleteFilesystem(id)
  ElMessage.success(t('common.deleted'))
  load()
}

async function handleTestConn() {
  testingConn.value = true
  try {
    const res = await testFilesystemConnection(form.value)
    if (res.data.success) ElMessage.success(t('filesystems.connOk'))
    else ElMessage.error(t('filesystems.connFail', { error: res.data.error }))
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || 'Test failed')
  } finally {
    testingConn.value = false
  }
}
</script>
