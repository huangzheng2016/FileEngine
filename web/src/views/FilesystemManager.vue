<template>
  <el-card>
    <template #header>
      <div style="display: flex; justify-content: space-between; align-items: center">
        <span>{{ $t('filesystems.title') }}</span>
        <el-button type="primary" @click="openDialog()">{{ $t('filesystems.addFs') }}</el-button>
      </div>
    </template>
    <el-table :data="filesystems" style="width: 100%">
      <el-table-column prop="name" :label="$t('common.name')" width="160" />
      <el-table-column prop="protocol" :label="$t('filesystems.protocol')" width="100">
        <template #default="{ row }">
          <el-tag :type="protocolTagType(row.protocol)" size="small" effect="dark">{{ row.protocol.toUpperCase() }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="base_path" :label="$t('filesystems.basePath')" width="250" show-overflow-tooltip />
      <el-table-column prop="host" :label="$t('filesystems.host')" width="160" />
      <el-table-column prop="description" :label="$t('common.description')" min-width="250" show-overflow-tooltip />
      <el-table-column :label="$t('common.actions')" width="160" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openDialog(row)">{{ $t('common.edit') }}</el-button>
          <el-button size="small" type="danger" @click="handleDelete(row.id)">{{ $t('common.delete') }}</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editingId ? $t('filesystems.editFs') : $t('filesystems.addFs')" width="550px">
      <el-form :model="form" label-width="120px">
        <el-form-item :label="$t('common.name')">
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item :label="$t('common.description')">
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item :label="$t('filesystems.protocol')">
          <el-select v-model="form.protocol">
            <el-option label="Local" value="local" />
            <el-option label="SFTP" value="sftp" />
            <el-option label="FTP" value="ftp" />
            <el-option label="SMB" value="smb" />
            <el-option label="NFS" value="nfs" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('filesystems.basePath')">
          <el-input v-model="form.base_path" placeholder="/" />
        </el-form-item>
        <template v-if="form.protocol !== 'local'">
          <el-form-item :label="$t('filesystems.host')">
            <el-input v-model="form.host" />
          </el-form-item>
          <el-form-item :label="$t('filesystems.port')">
            <el-input-number v-model="form.port" :min="0" />
          </el-form-item>
          <el-form-item :label="$t('filesystems.username')">
            <el-input v-model="form.username" />
          </el-form-item>
          <el-form-item :label="$t('filesystems.password')">
            <el-input v-model="form.password" type="password" show-password :placeholder="editingId ? $t('filesystems.keepPassword') : ''" />
          </el-form-item>
          <el-form-item v-if="form.protocol === 'sftp'" :label="$t('filesystems.keyPath')">
            <el-input v-model="form.key_path" />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="success" @click="handleTestConn" :loading="testingConn">{{ $t('filesystems.testConn') }}</el-button>
        <el-button type="primary" @click="handleSave">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { listFilesystems, createFilesystem, updateFilesystem, deleteFilesystem, testFilesystemConnection } from '../api'
import type { Filesystem } from '../types'
import { ElMessage, ElMessageBox } from 'element-plus'

const { t } = useI18n()
const filesystems = ref<Filesystem[]>([])
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

function protocolTagType(protocol: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  const map: Record<string, '' | 'success' | 'warning' | 'info' | 'danger'> = {
    local: 'info', sftp: 'success', ftp: '', smb: 'warning', nfs: 'danger'
  }
  return map[protocol] || 'info'
}

async function load() {
  const res = await listFilesystems()
  filesystems.value = res.data
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
