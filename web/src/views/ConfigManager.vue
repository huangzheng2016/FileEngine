<template>
  <section class="page-card">
    <header class="page-header">
      <div class="page-titles">
        <span class="page-eyebrow">FileEngine</span>
        <h2 class="page-heading">{{ $t('config.title') }}</h2>
        <p class="page-sub">{{ $t('config.subtitle') }}</p>
      </div>
    </header>

    <div class="page-body">
      <el-tabs v-model="activeTab">
        <el-tab-pane :label="$t('config.tabs.database')" name="database">
          <div class="form-stack" style="max-width: 560px">
            <el-alert type="warning" :title="$t('config.database.dangerWarning')" :closable="false" show-icon />
            <div class="form-field">
              <label class="form-field__label">{{ $t('config.database.driver') }}</label>
              <el-select v-model="config.database.driver">
                <el-option label="SQLite" value="sqlite" />
                <el-option label="MySQL" value="mysql" />
              </el-select>
            </div>
            <div class="form-field">
              <label class="form-field__label">{{ $t('config.database.dsn') }}</label>
              <el-input v-model="config.database.dsn" />
            </div>
            <div>
              <el-button type="primary" @click="handleSave" :loading="saving">{{ $t('config.saveConfig') }}</el-button>
            </div>
          </div>
        </el-tab-pane>

        <el-tab-pane :label="$t('config.tabs.agent')" name="agent">
          <div class="form-stack" style="max-width: 560px">
            <div class="form-row">
              <div class="form-field">
                <label class="form-field__label">{{ $t('config.agent.batchSize') }}</label>
                <el-input-number v-model="config.agent.batch_size" :min="1" :max="500" style="width: 100%" />
                <p class="form-field__hint">{{ $t('config.agent.batchSizeHint') }}</p>
              </div>
              <div class="form-field">
                <label class="form-field__label">{{ $t('config.agent.concurrency') }}</label>
                <el-input-number v-model="config.agent.concurrency" :min="1" :max="20" style="width: 100%" />
                <p class="form-field__hint">{{ $t('config.agent.concurrencyHint') }}</p>
              </div>
            </div>
            <div class="form-row">
              <div class="form-field">
                <label class="form-field__label">{{ $t('config.agent.maxFileReadSize') }}</label>
                <el-input-number v-model="config.agent.max_file_read_size" :min="1024" :step="1024" style="width: 100%" />
                <p class="form-field__hint">{{ $t('config.agent.maxFileReadSizeHint') }}</p>
              </div>
              <div class="form-field">
                <label class="form-field__label">{{ $t('config.agent.maxRetries') }}</label>
                <el-input-number v-model="config.agent.max_retries" :min="0" :max="10" style="width: 100%" />
                <p class="form-field__hint">{{ $t('config.agent.maxRetriesHint') }}</p>
              </div>
            </div>
            <div>
              <el-button type="primary" @click="handleSave" :loading="saving">{{ $t('config.saveConfig') }}</el-button>
            </div>
          </div>
        </el-tab-pane>

        <el-tab-pane :label="$t('config.tabs.prompt')" name="prompt">
          <div class="form-stack" style="max-width: 900px">
            <div style="display: flex; justify-content: space-between; align-items: center">
              <span v-if="promptCustom" class="status-chip is-warning">{{ $t('config.prompt.customLabel') }}</span>
              <span v-else class="status-chip is-muted">{{ $t('config.prompt.defaultLabel') }}</span>
              <button v-if="promptCustom" class="link-button" @click="handleResetPrompt">
                {{ $t('config.prompt.resetDefault') }}
              </button>
            </div>
            <div ref="editorContainer" class="prompt-editor"></div>
            <div style="text-align: right">
              <el-button type="primary" @click="handleSavePrompt" :loading="savingPrompt">
                {{ $t('config.prompt.savePrompt') }}
              </el-button>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>
  </section>
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
  agent: { batch_size: 50, concurrency: 1, max_file_read_size: 102400, max_retries: 3 },
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

<style scoped>
.prompt-editor {
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  overflow: hidden;
  min-height: 400px;
  background: var(--surface-soft);
}
</style>
