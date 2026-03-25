<template>
  <el-card>
    <template #header><span>{{ $t('config.title') }}</span></template>
    <el-tabs v-model="activeTab">
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
import { getConfig, updateConfig, getPrompt, updatePrompt } from '../api'
import type { Config } from '../types'
import { ElMessage } from 'element-plus'
import { EditorView, basicSetup } from 'codemirror'
import { markdown } from '@codemirror/lang-markdown'
import { oneDark } from '@codemirror/theme-one-dark'
import { EditorState } from '@codemirror/state'

const { t } = useI18n()
const activeTab = ref('database')
const saving = ref(false)
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
  agent: { batch_size: 10, concurrency: 1, max_file_read_size: 102400, max_retries: 3 },
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
