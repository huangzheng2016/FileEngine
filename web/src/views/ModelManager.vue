<template>
  <el-card>
    <template #header>
      <div style="display: flex; justify-content: space-between; align-items: center">
        <span>{{ $t('models.title') }}</span>
        <el-button type="primary" @click="openDialog()">{{ $t('models.addModel') }}</el-button>
      </div>
    </template>
    <el-table :data="providers" style="width: 100%">
      <el-table-column prop="name" :label="$t('common.name')" width="160" />
      <el-table-column prop="provider" :label="$t('models.provider')" width="100">
        <template #default="{ row }">
          <el-tag :type="providerTagType(row.provider)" size="small" effect="dark">{{ providerLabel(row.provider) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="model" :label="$t('models.modelName')" width="250" show-overflow-tooltip />
      <el-table-column prop="base_url" :label="$t('models.baseUrl')" min-width="250" show-overflow-tooltip />
      <el-table-column :label="$t('common.actions')" width="160" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openDialog(row)">{{ $t('common.edit') }}</el-button>
          <el-button size="small" type="danger" @click="handleDelete(row.id)">{{ $t('common.delete') }}</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editingId ? $t('models.editModel') : $t('models.addModel')" width="550px">
      <el-form :model="form" label-width="120px">
        <el-form-item :label="$t('common.name')">
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item :label="$t('models.provider')">
          <el-select v-model="form.provider">
            <el-option label="OpenAI" value="openai" />
            <el-option label="Claude" value="claude" />
            <el-option label="Ollama" value="ollama" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('models.apiKey')">
          <el-input v-model="form.api_key" type="password" show-password :placeholder="editingId ? $t('models.keepApiKey') : ''" />
        </el-form-item>
        <el-form-item :label="$t('models.modelName')">
          <el-input v-model="form.model" placeholder="gpt-4o / claude-sonnet-4-20250514" />
        </el-form-item>
        <el-form-item :label="$t('models.baseUrl')">
          <el-input v-model="form.base_url" placeholder="https://api.openai.com/v1" />
        </el-form-item>
        <el-form-item :label="$t('models.temperature')">
          <el-input-number v-model="form.temperature" :min="0" :max="2" :step="0.1" />
        </el-form-item>
        <el-form-item :label="$t('models.maxTokens')">
          <el-input-number v-model="form.max_tokens" :min="0" :step="256" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="success" @click="handleTest" :loading="testing">{{ $t('models.testConn') }}</el-button>
        <el-button type="primary" @click="handleSave">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { listModelProviders, createModelProvider, updateModelProvider, deleteModelProvider, testModelProviderConnection } from '../api'
import type { ModelProvider } from '../types'
import { ElMessage, ElMessageBox } from 'element-plus'

const { t } = useI18n()
const providers = ref<ModelProvider[]>([])
const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
const testing = ref(false)
const form = ref({
  name: '', provider: 'openai', api_key: '', model: '',
  base_url: '', temperature: 0.1, max_tokens: 4096,
})

onMounted(() => load())

async function load() {
  const res = await listModelProviders()
  providers.value = res.data
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

function openDialog(m?: ModelProvider) {
  if (m) {
    editingId.value = m.id
    form.value = {
      name: m.name, provider: m.provider, api_key: '',
      model: m.model, base_url: m.base_url,
      temperature: m.temperature, max_tokens: m.max_tokens,
    }
  } else {
    editingId.value = null
    form.value = { name: '', provider: 'openai', api_key: '', model: '', base_url: '', temperature: 0.1, max_tokens: 4096 }
  }
  dialogVisible.value = true
}

async function handleSave() {
  const data = { ...form.value }
  if (editingId.value) {
    if (!data.api_key) data.api_key = '****'
    await updateModelProvider(editingId.value, data)
  } else {
    await createModelProvider(data)
  }
  ElMessage.success(t('common.saved'))
  dialogVisible.value = false
  load()
}

async function handleDelete(id: number) {
  await ElMessageBox.confirm(t('models.deleteConfirm'), t('common.confirm'))
  await deleteModelProvider(id)
  ElMessage.success(t('common.deleted'))
  load()
}

async function handleTest() {
  testing.value = true
  try {
    const res = await testModelProviderConnection(form.value)
    if (res.data.success) ElMessage.success(t('models.connOk'))
    else ElMessage.error(t('models.connFail', { error: res.data.error }))
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || 'Test failed')
  } finally {
    testing.value = false
  }
}
</script>
