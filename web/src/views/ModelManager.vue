<template>
  <section class="page-card">
    <header class="page-header">
      <div class="page-titles">
        <span class="page-eyebrow">FileEngine</span>
        <h2 class="page-heading">{{ $t('models.title') }}</h2>
        <p class="page-sub">{{ $t('models.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button type="primary" @click="openDialog()">
          <el-icon style="margin-right: 4px"><Plus /></el-icon>{{ $t('models.addModel') }}
        </el-button>
      </div>
    </header>

    <div class="page-body">
      <el-table :data="providers" v-loading="loading" style="width: 100%">
        <el-table-column prop="name" :label="$t('common.name')" width="150">
          <template #default="{ row }"><span class="cell-phone">{{ row.name }}</span></template>
        </el-table-column>
        <el-table-column prop="provider" :label="$t('models.provider')" width="110">
          <template #default="{ row }">
            <span class="status-chip" :class="providerChipClass(row.provider)">{{ providerLabel(row.provider) }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="model" :label="$t('models.modelName')" min-width="200" show-overflow-tooltip>
          <template #default="{ row }"><code class="cell-mono">{{ row.model }}</code></template>
        </el-table-column>
        <el-table-column :label="$t('models.contextWindow')" width="120" align="center">
          <template #default="{ row }">
            <span class="metric-chip"><span class="metric-value">{{ formatWindow(row.context_window) }}</span></span>
          </template>
        </el-table-column>
        <el-table-column :label="$t('models.visionEnabled')" width="90" align="center">
          <template #default="{ row }">
            <span v-if="row.vision_enabled" class="status-chip is-live"><span class="status-dot"></span>{{ $t('common.yes') }}</span>
            <span v-else class="status-chip is-muted">{{ $t('common.no') }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="base_url" :label="$t('models.baseUrl')" min-width="200" show-overflow-tooltip>
          <template #default="{ row }"><span class="cell-time">{{ row.base_url || '-' }}</span></template>
        </el-table-column>
        <el-table-column :label="$t('common.actions')" width="280" fixed="right">
          <template #default="{ row }">
            <el-button size="small" @click="handleTestExisting(row)" :loading="testingId === row.id">{{ $t('models.testConn') }}</el-button>
            <el-button size="small" @click="handleDuplicate(row)"><el-icon><CopyDocument /></el-icon></el-button>
            <el-button size="small" @click="openDialog(row)">{{ $t('common.edit') }}</el-button>
            <el-button size="small" type="danger" plain @click="handleDelete(row.id)">{{ $t('common.delete') }}</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div class="empty-state-card empty-state-card--inline">
            <div class="empty-state-card__icon">
              <el-icon :size="24"><Cpu /></el-icon>
            </div>
            <div class="empty-state-card__text">
              <h4 class="empty-state-card__title">{{ $t('models.title') }}</h4>
              <p class="empty-state-card__sub">{{ $t('models.subtitle') }}</p>
            </div>
          </div>
        </template>
      </el-table>
    </div>

    <el-dialog append-to-body v-model="dialogVisible" :title="editingId ? $t('models.editModel') : $t('models.addModel')" width="560px">
      <div class="form-stack">
        <div class="form-row">
          <div class="form-field">
            <label class="form-field__label is-required">{{ $t('common.name') }}</label>
            <el-input v-model="form.name" />
          </div>
          <div class="form-field">
            <label class="form-field__label">{{ $t('models.provider') }}</label>
            <el-select v-model="form.provider">
              <el-option label="OpenAI" value="openai" />
              <el-option label="Claude" value="claude" />
              <el-option label="Ollama" value="ollama" />
            </el-select>
          </div>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('models.apiKey') }}</label>
          <el-input v-model="form.api_key" type="password" show-password :placeholder="editingId ? $t('models.keepApiKey') : ''" />
        </div>
        <div class="form-field">
          <label class="form-field__label is-required">{{ $t('models.modelName') }}</label>
          <el-input v-model="form.model" placeholder="gpt-4o / claude-sonnet-4-20250514" />
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('models.baseUrl') }}</label>
          <el-input v-model="form.base_url" placeholder="https://api.openai.com/v1" />
        </div>
        <div class="form-row">
          <div class="form-field">
            <label class="form-field__label">{{ $t('models.temperature') }}</label>
            <el-input-number v-model="form.temperature" :min="0" :max="2" :step="0.1" style="width: 100%" />
          </div>
          <div class="form-field">
            <label class="form-field__label">{{ $t('models.maxTokens') }}</label>
            <el-input-number v-model="form.max_tokens" :min="0" :step="256" style="width: 100%" />
          </div>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('models.contextWindow') }}</label>
          <el-select v-model="contextTier">
            <el-option v-for="tier in contextTiers" :key="tier" :label="formatWindow(tier)" :value="tier" />
            <el-option :label="$t('models.contextCustom')" :value="CUSTOM_TIER" />
          </el-select>
          <el-input-number
            v-if="contextTier === CUSTOM_TIER"
            v-model="form.context_window"
            :min="1000"
            :step="10000"
            style="width: 100%" />
          <p class="form-field__hint">{{ $t('models.contextWindowHint') }}</p>
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('models.visionEnabled') }}</label>
          <div>
            <el-switch v-model="form.vision_enabled" />
          </div>
          <p class="form-field__hint">{{ $t('models.visionEnabledHint') }}</p>
        </div>
      </div>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="success" @click="handleTest" :loading="testing">{{ $t('models.testConn') }}</el-button>
        <el-button type="primary" @click="handleSave">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { listModelProviders, createModelProvider, updateModelProvider, deleteModelProvider, testModelProviderConnection } from '../api'
import type { ModelProvider } from '../types'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Cpu, CopyDocument } from '@element-plus/icons-vue'

const { t } = useI18n()
const providers = ref<ModelProvider[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
const testing = ref(false)
const testingId = ref<number | null>(null)

const contextTiers = [200_000, 256_000, 500_000, 900_000, 1_000_000]
const CUSTOM_TIER = -1

const emptyForm = () => ({
  name: '', provider: 'openai', api_key: '', model: '',
  base_url: '', temperature: 0.1, max_tokens: 4096,
  context_window: 200_000, vision_enabled: false,
})
const form = ref(emptyForm())

// Tier dropdown value: the tier itself when it matches, otherwise the custom sentinel.
const contextTier = computed({
  get: () => (contextTiers.includes(form.value.context_window) ? form.value.context_window : CUSTOM_TIER),
  set: (v: number) => {
    if (v !== CUSTOM_TIER) form.value.context_window = v
    else if (contextTiers.includes(form.value.context_window)) form.value.context_window = 300_000
  },
})

function formatWindow(n: number): string {
  if (!n) return '-'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M`
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(n)
}

onMounted(() => load())

async function load() {
  loading.value = true
  try {
    const res = await listModelProviders()
    providers.value = res.data
  } finally {
    loading.value = false
  }
}

function providerChipClass(p: string): string {
  const map: Record<string, string> = { openai: 'is-live', claude: 'is-warning', ollama: 'is-muted' }
  return map[p] || 'is-accent'
}

function providerLabel(p: string): string {
  const map: Record<string, string> = { openai: 'OpenAI', claude: 'Claude', ollama: 'Ollama' }
  return map[p] || p
}

function fillForm(m: ModelProvider, nameSuffix: string) {
  return {
    name: m.name + nameSuffix, provider: m.provider, api_key: '',
    model: m.model, base_url: m.base_url,
    temperature: m.temperature, max_tokens: m.max_tokens,
    context_window: m.context_window || 200_000, vision_enabled: m.vision_enabled,
  }
}

function openDialog(m?: ModelProvider) {
  if (m) {
    editingId.value = m.id
    form.value = fillForm(m, '')
  } else {
    editingId.value = null
    form.value = emptyForm()
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
    if (res.data.success) ElMessage.success(t('models.connOk') + (res.data.reply ? ': ' + res.data.reply : ''))
    else ElMessage.error(t('models.connFail', { error: res.data.error }))
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || 'Test failed')
  } finally {
    testing.value = false
  }
}

async function handleTestExisting(m: ModelProvider) {
  testingId.value = m.id
  try {
    const data = { name: m.name, provider: m.provider, api_key: '****', model: m.model, base_url: m.base_url, temperature: m.temperature, max_tokens: m.max_tokens }
    const res = await testModelProviderConnection(data)
    if (res.data.success) ElMessage.success(t('models.connOk') + (res.data.reply ? ': ' + res.data.reply : ''))
    else ElMessage.error(t('models.connFail', { error: res.data.error }))
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || 'Test failed')
  } finally {
    testingId.value = null
  }
}

function handleDuplicate(m: ModelProvider) {
  editingId.value = null
  form.value = fillForm(m, ' (copy)')
  dialogVisible.value = true
}
</script>
