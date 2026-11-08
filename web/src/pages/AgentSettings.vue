<script setup lang="ts">
// Agent settings: one document saved to the server config; every field is read per run.
import { computed, reactive, ref } from 'vue'
import CodeEditor from '@/base/CodeEditor.vue'
import EmptyState from '@/base/EmptyState.vue'
import Loading from '@/base/Loading.vue'
import Select from '@/base/Select.vue'
import UnitInput from '@/base/UnitInput.vue'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useConfirm } from '@/base/useConfirm'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFetch } from '@/base/useFetch'
import { useFormValidation } from '@/base/useFormValidation'
import { importCategories } from '@/api/files'
import { getConfig, getPrompt, listFilesystems, saveConfig, type AppConfig } from '@/api/settings'
import { categoryPresets } from '@/data/categoryPresets'

const confirm = useConfirm()

const form = reactive({
  concurrency: 6,
  max_step: 150,
  max_retries: 3,
  agent_timeout: 0,
  max_file_read_size: 0,
  category_policy: '',
  prompt: '',
})
const snapshot = ref<AppConfig | null>(null)
const defaultPrompt = ref('')
const guard = useDirtyGuard(() => form, { route: true })

const v = useFormValidation(() => ({
  concurrency: form.concurrency < 1 || form.concurrency > 32 ? '并行数需在 1 到 32 之间' : '',
  max_file_read_size: form.max_file_read_size < 1000 ? '每次读取字数至少 1000' : '',
  max_step: form.max_step < 1 ? '每次最多步数至少为 1' : '',
  max_retries: form.max_retries < 0 || form.max_retries > 10 ? '失败重试需在 0 到 10 次之间' : '',
}))

const load = useFetch(
  async (signal) => {
    const [cfg, prompt] = await Promise.all([getConfig(signal), getPrompt(signal)])
    apply(cfg, prompt.default_prompt)
  },
  { immediate: true },
)

function apply(cfg: AppConfig, def = defaultPrompt.value) {
  snapshot.value = cfg
  defaultPrompt.value = def
  const a = cfg.agent
  Object.assign(form, {
    concurrency: a.concurrency,
    max_step: a.max_step,
    max_retries: a.max_retries,
    agent_timeout: a.agent_timeout > 0 ? a.agent_timeout : 0,
    max_file_read_size: a.max_file_read_size,
    category_policy: a.category_policy ?? '',
    prompt: a.system_prompt || def,
  })
  v.reset()
  guard.reset()
}

const saving = ref(false)
async function save() {
  if (!snapshot.value || !v.validate() || saving.value) return
  saving.value = true
  try {
    const cfg = snapshot.value
    const saved = await saveConfig({
      ...cfg,
      agent: {
        ...cfg.agent,
        concurrency: form.concurrency,
        max_step: form.max_step,
        max_retries: form.max_retries,
        agent_timeout: form.agent_timeout,
        max_file_read_size: form.max_file_read_size,
        category_policy: form.category_policy.trim(),
        system_prompt: form.prompt.trim() === defaultPrompt.value.trim() ? '' : form.prompt,
      },
    })
    apply(saved)
    toast.success('已保存')
  } catch (e) {
    toast.error('保存失败', e)
  } finally {
    saving.value = false
  }
}

async function revert() {
  if (snapshot.value && (await guard.confirmDiscard())) apply(snapshot.value)
}

const customPrompt = computed(() => form.prompt.trim() !== defaultPrompt.value.trim())
function resetPrompt() {
  form.prompt = defaultPrompt.value
}

const PRESETS = categoryPresets.map((p) => ({ value: p.id, label: p.label }))
const fillFrom = ref('')
async function fillPolicy(id: string) {
  const p = categoryPresets.find((x) => x.id === id)
  if (!p) return
  if (
    form.category_policy.trim() &&
    !(await confirm({ message: `用「${p.label}」替换当前的分类规范？`, confirmText: '替换' }))
  )
    return
  form.category_policy = p.policy
  fillFrom.value = id
}

const storages = useFetch(listFilesystems, { immediate: true })
const storageOptions = computed(() => (storages.data.value ?? []).map((f) => ({ value: f.id, label: f.name })))
const imp = reactive({ preset: '', storage: 0, running: false })
const importBlocked = computed(() => (!imp.preset ? '先选择模板' : !imp.storage ? '先选择存储' : ''))
async function importPreset() {
  const p = categoryPresets.find((x) => x.id === imp.preset)
  if (!p || !imp.storage) return
  imp.running = true
  try {
    const r = await importCategories(imp.storage, p.categories)
    toast.success(r.skipped ? `已导入 ${r.created} 个分类，跳过 ${r.skipped} 个已有的` : `已导入 ${r.created} 个分类`)
  } catch (e) {
    toast.error('导入失败', e)
  } finally {
    imp.running = false
  }
}

const saveBlocked = computed(() => (!guard.dirty.value ? '没有修改' : ''))
</script>

<template>
  <div class="page">
    <Loading v-if="!snapshot && load.loading.value" />
    <EmptyState
      v-else-if="!snapshot && load.error.value"
      icon="alert-triangle"
      title="加载失败"
      :hint="load.error.value"
    >
      <button type="button" @click="load.refresh">重试</button>
    </EmptyState>
    <form v-else-if="snapshot" class="page" novalidate v-on="v.form" @submit.prevent="save">
      <section class="card">
        <div class="card-head"><h3>运行</h3></div>
        <div class="form-grid">
          <label class="field">
            <span class="field-label">并行数</span>
            <input
              v-model.number="form.concurrency"
              type="number"
              min="1"
              max="32"
              name="concurrency"
              :aria-invalid="!!v.error('concurrency')"
            />
            <span v-if="v.error('concurrency')" class="field-error">{{ v.error('concurrency') }}</span>
          </label>
          <label class="field">
            <span class="field-label">每次最多步数</span>
            <input
              v-model.number="form.max_step"
              type="number"
              min="1"
              name="max_step"
              :aria-invalid="!!v.error('max_step')"
            />
            <span v-if="v.error('max_step')" class="field-error">{{ v.error('max_step') }}</span>
          </label>
          <label class="field">
            <span class="field-label">失败重试</span>
            <input
              v-model.number="form.max_retries"
              type="number"
              min="0"
              max="10"
              name="max_retries"
              :aria-invalid="!!v.error('max_retries')"
            />
            <span v-if="v.error('max_retries')" class="field-error">{{ v.error('max_retries') }}</span>
          </label>
          <div class="field">
            <span class="field-label">单次运行时限</span>
            <UnitInput v-model="form.agent_timeout" kind="duration" name="agent_timeout" label="单次运行时限" />
            <span class="field-hint">留空表示不限</span>
          </div>
          <label class="field">
            <span class="field-label">每次读取字数</span>
            <input
              v-model.number="form.max_file_read_size"
              type="number"
              min="1000"
              name="max_file_read_size"
              :aria-invalid="!!v.error('max_file_read_size')"
            />
            <span v-if="v.error('max_file_read_size')" class="field-error">{{ v.error('max_file_read_size') }}</span>
          </label>
        </div>
      </section>

      <section class="card">
        <div class="card-head">
          <h3>分类规范</h3>
          <Select
            :model-value="fillFrom"
            :options="PRESETS"
            label="用模板填写"
            placeholder="用模板填写"
            size="sm"
            @update:model-value="fillPolicy"
          />
        </div>
        <textarea v-model="form.category_policy" name="category_policy" rows="8" aria-label="分类规范" />
        <span class="field-hint">留空保存会恢复默认规范</span>
        <div class="toolbar">
          <span class="text-sm">把模板里的分类导入到</span>
          <Select v-model="imp.preset" :options="PRESETS" label="模板" placeholder="选择模板" size="sm" />
          <Select v-model="imp.storage" :options="storageOptions" label="存储" placeholder="选择存储" size="sm" />
          <button
            v-tip="importBlocked"
            type="button"
            class="btn-sm"
            :disabled="imp.running || !!importBlocked"
            @click="importPreset"
          >
            {{ imp.running ? '导入中' : '导入' }}
          </button>
        </div>
      </section>

      <section class="card">
        <div class="card-head">
          <h3>提示词</h3>
          <button v-if="customPrompt" type="button" class="btn-sm" @click="resetPrompt">恢复默认</button>
        </div>
        <CodeEditor v-model="form.prompt" :rows="24" name="prompt" />
      </section>

      <div class="save-bar">
        <span v-if="guard.dirty.value" class="text-sm">有未保存的修改</span>
        <span class="spacer" />
        <button type="button" :disabled="!guard.dirty.value || saving" @click="revert">撤销修改</button>
        <button v-tip="saveBlocked" type="submit" class="primary" :disabled="saving || !!saveBlocked">
          {{ saving ? '保存中' : '保存' }}
        </button>
      </div>
    </form>
  </div>
</template>
