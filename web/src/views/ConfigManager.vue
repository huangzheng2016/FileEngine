<template>
  <el-card>
    <template #header><span>{{ $t('config.title') }}</span></template>
    <el-tabs v-model="activeTab">
      <el-tab-pane :label="$t('config.tabs.model')" name="model">
        <el-form :model="config.model" label-width="140px" style="max-width: 600px">
          <el-form-item :label="$t('config.model.provider')">
            <el-select v-model="config.model.provider">
              <el-option label="OpenAI" value="openai" />
              <el-option label="Claude" value="claude" />
              <el-option label="Ollama" value="ollama" />
            </el-select>
          </el-form-item>
          <el-form-item :label="$t('config.model.apiKey')">
            <el-input v-model="config.model.api_key" type="password" show-password />
          </el-form-item>
          <el-form-item :label="$t('config.model.modelName')">
            <el-input v-model="config.model.model" placeholder="gpt-4o" />
          </el-form-item>
          <el-form-item :label="$t('config.model.baseUrl')">
            <el-input v-model="config.model.base_url" placeholder="https://api.openai.com/v1" />
          </el-form-item>
          <el-form-item :label="$t('config.model.temperature')">
            <el-input-number v-model="config.model.temperature" :min="0" :max="2" :step="0.1" />
          </el-form-item>
          <el-form-item :label="$t('config.model.maxTokens')">
            <el-input-number v-model="config.model.max_tokens" :min="0" :step="256" />
          </el-form-item>
          <el-form-item>
            <el-button type="success" @click="handleTestModel" :loading="testingModel">{{ $t('config.model.testModel') }}</el-button>
            <el-button type="primary" @click="handleSave" :loading="saving">{{ $t('config.saveConfig') }}</el-button>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <el-tab-pane :label="$t('config.tabs.database')" name="database">
        <el-form :model="config.database" label-width="140px" style="max-width: 600px">
          <el-form-item :label="$t('config.database.driver')">
            <el-select v-model="config.database.driver">
              <el-option label="SQLite" value="sqlite" />
              <el-option label="MySQL" value="mysql" />
            </el-select>
          </el-form-item>
          <el-form-item :label="$t('config.database.dsn')">
            <el-input v-model="config.database.dsn" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" @click="handleSave" :loading="saving">{{ $t('config.saveConfig') }}</el-button>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <el-tab-pane :label="$t('config.tabs.agent')" name="agent">
        <el-form :model="config.agent" label-width="160px" style="max-width: 600px">
          <el-form-item :label="$t('config.agent.batchSize')">
            <el-input-number v-model="config.agent.batch_size" :min="1" :max="50" />
          </el-form-item>
          <el-form-item :label="$t('config.agent.concurrency')">
            <el-input-number v-model="config.agent.concurrency" :min="1" :max="20" />
          </el-form-item>
          <el-form-item :label="$t('config.agent.maxFileReadSize')">
            <el-input-number v-model="config.agent.max_file_read_size" :min="1024" :step="1024" />
          </el-form-item>
          <el-form-item :label="$t('config.agent.maxRetries')">
            <el-input-number v-model="config.agent.max_retries" :min="0" :max="10" />
          </el-form-item>
          <el-form-item :label="$t('config.agent.allowAutoCategory')">
            <el-switch v-model="config.agent.allow_auto_category" />
            <span style="margin-left: 8px; font-size: 12px; color: #909399">{{ $t('config.agent.allowAutoCategoryHint') }}</span>
          </el-form-item>
          <el-form-item>
            <el-button type="primary" @click="handleSave" :loading="saving">{{ $t('config.saveConfig') }}</el-button>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <el-tab-pane :label="$t('config.tabs.prompt')" name="prompt">
        <div style="max-width: 900px">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px">
            <span style="font-size: 14px; color: #606266">
              <el-tag v-if="promptCustom" type="warning" size="small">{{ $t('config.prompt.customLabel') }}</el-tag>
              <el-tag v-else type="info" size="small">{{ $t('config.prompt.defaultLabel') }}</el-tag>
            </span>
            <el-button v-if="promptCustom" text type="primary" @click="handleResetPrompt">
              {{ $t('config.prompt.resetDefault') }}
            </el-button>
          </div>
          <div ref="editorContainer" style="border: 1px solid #dcdfe6; border-radius: 4px; overflow: hidden; min-height: 400px"></div>
          <div style="margin-top: 12px; text-align: right">
            <el-button type="primary" @click="handleSavePrompt" :loading="savingPrompt">
              {{ $t('config.prompt.savePrompt') }}
            </el-button>
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>
  </el-card>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { getConfig, updateConfig, testModel, getPrompt, updatePrompt } from '../api'
import type { Config } from '../types'
import { ElMessage } from 'element-plus'
import { EditorView, basicSetup } from 'codemirror'
import { markdown } from '@codemirror/lang-markdown'
import { oneDark } from '@codemirror/theme-one-dark'
import { EditorState } from '@codemirror/state'

const { t } = useI18n()
const activeTab = ref('model')
const saving = ref(false)
const testingModel = ref(false)
const savingPrompt = ref(false)
const promptText = ref('')
const promptDefault = ref('')
const promptCustom = ref(false)
const editorContainer = ref<HTMLElement>()
let editorView: EditorView | null = null

function initEditor(content: string) {
  if (editorView) editorView.destroy()
  if (!editorContainer.value) return
  editorView = new EditorView({
    state: EditorState.create({
      doc: content,
      extensions: [
        basicSetup,
        markdown(),
        oneDark,
        EditorView.lineWrapping,
        EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            promptText.value = update.state.doc.toString()
          }
        }),
      ],
    }),
    parent: editorContainer.value,
  })
}

const config = reactive<Config>({
  server: { port: 8080, host: '0.0.0.0' },
  database: { driver: 'sqlite', dsn: 'fileengine.db' },
  model: { provider: 'openai', api_key: '', model: 'gpt-4o', base_url: '', temperature: 0.1, max_tokens: 4096 },
  agent: { batch_size: 10, concurrency: 1, max_file_read_size: 102400, max_retries: 3, allow_auto_category: false, allow_read_file: true },
})

onMounted(async () => {
  const res = await getConfig()
  Object.assign(config, res.data)
  try {
    const pRes = await getPrompt()
    promptText.value = pRes.data.prompt
    promptDefault.value = pRes.data.default_prompt
    promptCustom.value = pRes.data.is_custom
    await nextTick()
    initEditor(promptText.value)
  } catch { /* ignore */ }
})

async function handleSave() {
  saving.value = true
  try {
    await updateConfig(config)
    ElMessage.success(t('config.configSaved'))
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || t('config.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function handleTestModel() {
  testingModel.value = true
  try {
    const res = await testModel(config.model)
    if (res.data.success) ElMessage.success(t('config.model.testOk'))
    else ElMessage.error(t('config.model.testFail', { error: res.data.error }))
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || t('config.saveFailed'))
  } finally {
    testingModel.value = false
  }
}

async function handleSavePrompt() {
  savingPrompt.value = true
  try {
    const res = await updatePrompt(promptText.value)
    promptCustom.value = res.data.is_custom
    ElMessage.success(t('config.prompt.promptSaved'))
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || t('config.saveFailed'))
  } finally {
    savingPrompt.value = false
  }
}

function handleResetPrompt() {
  promptText.value = promptDefault.value
  promptCustom.value = false
  initEditor(promptDefault.value)
}
</script>
