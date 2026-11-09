<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Combobox, { type ComboOption } from '@/base/Combobox.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import Select from '@/base/Select.vue'
import Steps from '@/base/Steps.vue'
import Switch from '@/base/Switch.vue'
import { errorMessage } from '@/base/errors'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { useWizard } from '@/base/useWizard'
import { createModel, listProviderModels, testModel, updateModel, type ModelInput } from '@/api/settings'
import type { ModelProvider } from '@/api/types'
import {
  CONTEXT_PRESETS,
  DEFAULT_CONTEXT,
  MODEL_PRESETS,
  baseUrlError,
  contextError,
  effortError,
  effortLabel,
  effortOptions,
  fmtTokens,
  guessCaps,
  isContextPreset,
  maxTokensDefault,
  parseTokens,
  presetFor,
  type ModelPreset,
} from '@/lib/modelForm'

// New model: provider → key → model, with the model list fetched from the
// provider and a test before saving. Editing: the same fields on one page.
const props = defineProps<{ open: boolean; editing: ModelProvider | null }>()
const emit = defineEmits<{ close: []; saved: [ModelProvider] }>()

const empty = () => ({
  preset: 'deepseek',
  name: '',
  provider: 'openai' as ModelProvider['provider'],
  model: '',
  base_url: '',
  api_key: '',
  temperature: 0.3,
  // '' = not sent (provider default)
  max_tokens: '' as number | '',
  context: DEFAULT_CONTEXT as number | 'custom',
  context_custom: '',
  vision_enabled: false,
  thinking_enabled: false,
  effort: null as ComboOption | null,
})
const form = reactive(empty())
const saving = ref(false)
const advanced = ref(false)
const nameTouched = ref(false)
const guard = useDirtyGuard(() => form)

const isCustom = computed(() => form.preset === 'custom')
const preset = computed<ModelPreset>(() => MODEL_PRESETS.find((p) => p.id === form.preset) ?? MODEL_PRESETS[0])
const contextTokens = computed(() =>
  form.context === 'custom' ? parseTokens(form.context_custom) : (form.context as number),
)
const keySaved = computed(() => !!props.editing?.api_key_set && form.provider === props.editing.provider)
const keyRequired = computed(() => preset.value.keyRequired && !keySaved.value)
const effort = computed(() => (form.thinking_enabled ? (form.effort?.value ?? '') : ''))
const endpointChanged = computed(
  () =>
    !!props.editing && (props.editing.provider !== form.provider || props.editing.base_url !== form.base_url.trim()),
)

const keyRules = () => ({
  base_url: form.preset === 'custom' && !form.base_url.trim() ? '请输入接口地址' : baseUrlError(form.base_url),
  api_key:
    keyRequired.value && !form.api_key.trim()
      ? '请输入 API Key'
      : props.editing?.api_key_set && endpointChanged.value && !form.api_key.trim()
        ? '接口地址已变化，请重新输入 API Key'
        : '',
})
const modelRules = () => ({ model: form.model.trim() ? '' : '请选择或输入模型' })
const advancedRules = () => ({
  temperature:
    typeof form.temperature !== 'number' || form.temperature < 0 || form.temperature > 2 ? '温度需在 0 到 2 之间' : '',
  max_tokens:
    form.max_tokens !== '' && (!Number.isInteger(form.max_tokens) || form.max_tokens < 1)
      ? '单次最多输出需为正整数'
      : '',
  context_custom: form.context === 'custom' ? contextError(contextTokens.value) : '',
  effort: effortError(effort.value),
})
const vk = useFormValidation(keyRules)
const vm = useFormValidation(() => ({ ...modelRules(), ...advancedRules() }))
type Key =
  keyof ReturnType<typeof keyRules> | keyof ReturnType<typeof modelRules> | keyof ReturnType<typeof advancedRules>
const err = (k: Key) => (k in keyRules() ? vk.error(k as keyof ReturnType<typeof keyRules>) : vm.error(k as never))

// ---- provider catalog ----

const catalog = reactive({ models: [] as string[], loading: false, loaded: false, error: '', detail: '' })
const query = ref('')
let catalogCtl: AbortController | null = null

async function loadCatalog(): Promise<boolean> {
  catalogCtl?.abort()
  const ctl = (catalogCtl = new AbortController())
  Object.assign(catalog, { loading: true, error: '', detail: '' })
  try {
    const r = await listProviderModels(
      {
        provider: form.provider,
        base_url: form.base_url.trim(),
        api_key: form.api_key.trim() || undefined,
        id: props.editing?.id,
      },
      ctl.signal,
    )
    if (ctl.signal.aborted) return false
    Object.assign(catalog, { models: r.models, loaded: r.success, error: r.error ?? '', detail: r.detail ?? '' })
    return r.success
  } catch (e) {
    if (!ctl.signal.aborted) Object.assign(catalog, { models: [], loaded: false, error: errorMessage(e), detail: '' })
    return false
  } finally {
    if (catalogCtl === ctl) catalog.loading = false
  }
}

// Recommended models first, then what the provider lists.
const choices = computed(() => {
  const listed = catalog.models
  const recommended = preset.value.models.filter((m) => !catalog.loaded || listed.includes(m))
  const rest = listed.filter((m) => !recommended.includes(m))
  const q = query.value.trim().toLowerCase()
  const all = [
    ...recommended.map((m) => ({ id: m, recommended: true })),
    ...rest.map((m) => ({ id: m, recommended: false })),
  ]
  const shown = q ? all.filter((m) => m.id.toLowerCase().includes(q)) : all
  return shown.slice(0, 200)
})
const typedChoice = computed(() => {
  const q = query.value.trim()
  return q && !choices.value.some((c) => c.id === q) ? q : ''
})

function chooseModel(id: string) {
  form.model = id
  const caps = guessCaps(id, preset.value)
  form.context = isContextPreset(caps.context) ? caps.context : 'custom'
  form.context_custom = isContextPreset(caps.context) ? '' : fmtTokens(caps.context)
  form.vision_enabled = caps.vision
  form.thinking_enabled = caps.thinking
  if (!nameTouched.value) form.name = id
}

// ---- wizard (new model only) ----

const skipCatalog = ref(false)
const wizard = useWizard([
  { key: 'provider', label: '服务商' },
  {
    key: 'key',
    label: '连接',
    validate: async () => {
      if (!vk.validate()) return false
      if (skipCatalog.value) return true
      return loadCatalog()
    },
  },
  { key: 'model', label: '模型' },
] as const)
const step = computed(() => wizard.current.value.key)

function applyPreset(p: ModelPreset) {
  form.preset = p.id
  form.provider = p.provider
  form.base_url = p.baseUrl
  form.temperature = p.temperature
  form.model = ''
  Object.assign(catalog, { models: [], loaded: false, error: '', detail: '' })
}

function pickPreset(p: ModelPreset) {
  if (form.preset !== p.id || wizard.reached.value === 0) applyPreset(p)
  skipCatalog.value = false
  void wizard.next()
}

async function typeModelByHand() {
  skipCatalog.value = true
  await wizard.next()
  skipCatalog.value = false
}

// Entering the model step preselects the first recommendation.
watch(step, (s) => {
  if (s !== 'model') return
  query.value = ''
  tested.value = null
  if (!form.model) {
    const first = choices.value.find((c) => c.recommended) ?? choices.value[0]
    if (first) chooseModel(first.id)
  }
})

// ---- open / reset ----

watch(
  () => props.open,
  (open) => {
    if (!open) return
    const m = props.editing
    Object.assign(
      form,
      m
        ? {
            ...empty(),
            preset: presetFor(m.provider, m.base_url).id,
            name: m.name,
            provider: m.provider,
            model: m.model,
            base_url: m.base_url,
            temperature: m.temperature,
            max_tokens: m.max_tokens || '',
            context: isContextPreset(m.context_window) ? m.context_window : 'custom',
            context_custom: isContextPreset(m.context_window) ? '' : fmtTokens(m.context_window),
            vision_enabled: m.vision_enabled,
            thinking_enabled: m.thinking_enabled,
            effort: m.reasoning_effort ? { value: m.reasoning_effort, label: effortLabel(m.reasoning_effort) } : null,
          }
        : empty(),
    )
    if (!m) applyPreset(MODEL_PRESETS[0])
    nameTouched.value = !!m
    tested.value = null
    advanced.value = false
    query.value = ''
    Object.assign(catalog, { models: [], loaded: false, loading: false, error: '', detail: '' })
    wizard.reset()
    vk.reset()
    vm.reset()
    guard.reset()
  },
)

// Editing: switching the provider fills its address.
function changePreset(id: string) {
  const p = MODEL_PRESETS.find((x) => x.id === id)
  if (!p) return
  form.preset = p.id
  form.provider = p.provider
  form.base_url = p.baseUrl
  Object.assign(catalog, { models: [], loaded: false, error: '', detail: '' })
}

const PRESET_OPTIONS = MODEL_PRESETS.map((p) => ({ value: p.id, label: p.title }))
const CONTEXTS = [
  ...CONTEXT_PRESETS.map((p) => ({ value: p.value as number | 'custom', label: p.label })),
  { value: 'custom' as const, label: '自定义' },
]
const loadEfforts = async (q: string) => effortOptions(q)

function body(): ModelInput {
  return {
    name: form.name.trim() || form.model.trim(),
    provider: form.provider,
    model: form.model.trim(),
    base_url: form.base_url.trim(),
    api_key: form.api_key.trim() || undefined,
    temperature: form.temperature,
    max_tokens: form.max_tokens || 0,
    context_window: contextTokens.value,
    vision_enabled: form.vision_enabled,
    thinking_enabled: form.thinking_enabled,
    reasoning_effort: form.thinking_enabled ? effort.value : undefined,
  }
}

// What the model is told to do, in one line, so nobody has to open 高级 to know.
const summary = computed(() =>
  [
    `上下文 ${fmtTokens(contextTokens.value)}`,
    form.vision_enabled ? '能读图' : '不读图',
    form.thinking_enabled ? `思考${effort.value ? `（${effortLabel(effort.value)}）` : ''}` : '不思考',
    form.temperature ? `温度 ${form.temperature}` : '温度用模型默认',
    form.max_tokens
      ? `单次最多输出 ${form.max_tokens}`
      : `单次输出${form.provider === 'claude' ? '最多 8192' : '不限制'}`,
  ].join(' · '),
)

// ---- test and save ----

interface TestResult {
  key: string
  ok: boolean
  text: string
  detail: string
}
const tested = ref<TestResult | null>(null)
const testing = ref(false)
const bodyKey = () => JSON.stringify({ ...body(), name: '' })
const testedNow = computed(() => (tested.value?.key === bodyKey() ? tested.value : null))

async function test(): Promise<boolean> {
  if (testing.value) return false
  testing.value = true
  const key = bodyKey()
  try {
    const r = await testModel({ ...body(), id: props.editing?.id })
    tested.value = {
      key,
      ok: r.success,
      text: r.success ? r.reply || '—' : r.error || '—',
      detail: r.success ? '' : (r.detail ?? ''),
    }
    return r.success
  } catch (e) {
    tested.value = { key, ok: false, text: errorMessage(e), detail: '' }
    return false
  } finally {
    testing.value = false
  }
}

async function runTest() {
  if (!vk.validate() || !vm.validate()) return
  await test()
}

async function close() {
  catalogCtl?.abort()
  if (await guard.confirmDiscard()) emit('close')
}

function validateAll() {
  const ok = vk.validate() && vm.validate()
  if (!ok && Object.values(advancedRules()).some(Boolean)) advanced.value = true
  return ok
}

// New models are tested first; a failed test can still be saved on purpose.
async function submit() {
  if (!validateAll() || saving.value) return
  if (!props.editing && !testedNow.value && !(await test())) return
  saving.value = true
  try {
    const saved = props.editing ? await updateModel(props.editing.id, body()) : await createModel(body())
    toast.success(props.editing ? '已保存' : '已添加模型')
    guard.reset()
    emit('saved', saved)
    emit('close')
  } catch (e) {
    toast.error(props.editing ? '保存失败' : '添加失败', e)
  } finally {
    saving.value = false
  }
}

function onSubmit() {
  if (props.editing || step.value === 'model') void submit()
  else if (step.value === 'key') void wizard.next()
}

const showKey = computed(() => !!props.editing || step.value === 'key')
const showModel = computed(() => !!props.editing || step.value === 'model')
const saveLabel = computed(() => {
  if (saving.value) return '保存中'
  if (testing.value) return '测试中'
  if (props.editing) return '保存'
  return testedNow.value && !testedNow.value.ok ? '仍然保存' : '测试并保存'
})
</script>

<template>
  <BaseDialog :open="open" :title="editing ? '编辑模型' : '添加模型'" width="640px" @close="close">
    <form id="model-form" class="form" novalidate @submit.prevent="onSubmit">
      <Steps
        v-if="!editing"
        :steps="wizard.steps"
        :index="wizard.index.value"
        :reached="wizard.reached.value"
        @go="wizard.goTo"
      />

      <!-- 1. provider -->
      <div v-if="!editing && step === 'provider'" class="choices">
        <button
          v-for="p in MODEL_PRESETS"
          :key="p.id"
          type="button"
          class="choice"
          :class="{ chosen: form.preset === p.id && wizard.reached.value > 0 }"
          @click="pickPreset(p)"
        >
          <span class="choice-title">{{ p.title }}</span>
          <span class="choice-desc">{{ p.desc }}</span>
        </button>
      </div>

      <!-- 2. connection -->
      <div v-if="showKey" class="form-grid" v-on="vk.form">
        <div v-if="editing" class="field">
          <span class="field-label">服务商</span>
          <Select
            :model-value="form.preset"
            :options="PRESET_OPTIONS"
            label="服务商"
            @update:model-value="changePreset"
          />
        </div>
        <label class="field" :class="{ 'span-2': !editing }">
          <span class="field-label" :class="{ required: isCustom }">接口地址</span>
          <input
            v-model="form.base_url"
            name="base_url"
            class="mono"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            :placeholder="form.preset === 'custom' ? 'https://example.com/v1' : preset.baseUrl"
            :aria-invalid="!!err('base_url')"
          />
          <span v-if="err('base_url')" class="field-error">{{ err('base_url') }}</span>
          <span v-else-if="!editing && form.preset === 'custom'" class="field-hint">
            服务商文档里的 OpenAI 兼容地址，通常以 /v1 结尾
          </span>
          <span v-else-if="!editing && form.preset === 'ollama'" class="field-hint">
            Ollama 在另一台电脑上时，把 127.0.0.1 换成那台电脑的 IP
          </span>
        </label>
        <label class="field span-2">
          <span class="field-label" :class="{ required: keyRequired }">API Key</span>
          <input
            v-model="form.api_key"
            name="api_key"
            type="password"
            autocomplete="new-password"
            :placeholder="keySaved ? '已保存，输入新的可替换' : preset.keyRequired ? '粘贴 API Key' : '不需要就留空'"
            :aria-invalid="!!err('api_key')"
          />
          <span v-if="err('api_key')" class="field-error">{{ err('api_key') }}</span>
          <span v-else-if="preset.keyUrl && !keySaved" class="field-hint">
            还没有？<a :href="preset.keyUrl" target="_blank" rel="noopener noreferrer"
              >去 {{ preset.title }} 后台创建</a
            >
          </span>
        </label>
      </div>
      <Alert v-if="step === 'key' && catalog.error && !catalog.loading" tone="danger" title="没能取到模型列表">
        {{ catalog.error }}
        <p v-if="catalog.detail" class="detail text-sm text-muted">{{ catalog.detail }}</p>
        <p class="detail">
          有些服务商不提供模型列表。确认密钥没问题的话，可以
          <button type="button" class="btn-sm" @click="typeModelByHand">手动填写模型名</button>
        </p>
      </Alert>

      <!-- 3. model -->
      <div v-if="showModel" class="form-grid" v-on="vm.form">
        <div class="field span-2">
          <span class="field-label required">模型</span>
          <div class="model-search">
            <input
              v-model="query"
              name="model_query"
              class="mono"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              :placeholder="catalog.models.length ? '搜索，或直接输入模型名称' : '输入模型名称'"
              @keydown.enter.prevent="typedChoice && chooseModel(typedChoice)"
            />
            <button v-if="editing" type="button" :disabled="catalog.loading" @click="loadCatalog">
              <Icon name="refresh-cw" :size="14" />获取列表
            </button>
          </div>
          <div class="model-list" role="listbox" aria-label="模型">
            <Loading v-if="catalog.loading" />
            <template v-else>
              <button
                v-if="typedChoice"
                type="button"
                role="option"
                :aria-selected="form.model === typedChoice"
                class="model-row"
                @click="chooseModel(typedChoice)"
              >
                <span class="mono truncate row-name">{{ typedChoice }}</span>
                <span class="text-muted text-sm">使用这个名称</span>
              </button>
              <button
                v-for="c in choices"
                :key="c.id"
                type="button"
                role="option"
                :aria-selected="form.model === c.id"
                class="model-row"
                :class="{ selected: form.model === c.id }"
                @click="chooseModel(c.id)"
              >
                <Icon v-if="form.model === c.id" name="check" :size="14" class="text-success" />
                <span class="mono truncate row-name">{{ c.id }}</span>
                <span v-if="c.recommended" class="pill">推荐</span>
              </button>
              <p v-if="!choices.length && !typedChoice" class="model-empty text-sm text-muted">
                {{ editing ? '点「获取列表」看服务商提供的模型，或直接输入名称' : '输入模型名称后按回车' }}
              </p>
            </template>
          </div>
          <span v-if="err('model')" class="field-error">{{ err('model') }}</span>
          <span v-else-if="form.model" class="field-hint">
            已选 <span class="mono">{{ form.model }}</span>
          </span>
          <span v-if="editing && catalog.error && !catalog.loading" class="field-error">{{ catalog.error }}</span>
        </div>
        <label class="field span-2">
          <span class="field-label">名称</span>
          <input
            v-model="form.name"
            name="name"
            maxlength="64"
            placeholder="默认同模型名称"
            @input="nameTouched = true"
          />
        </label>
        <p class="span-2 text-sm text-muted summary">{{ summary }}</p>
      </div>

      <details v-if="showModel" :open="advanced" @toggle="advanced = ($event.target as HTMLDetailsElement).open">
        <summary>高级设置</summary>
        <div class="form-grid advanced" v-on="vm.form">
          <div class="field">
            <span class="field-label">上下文长度</span>
            <Select v-model="form.context" :options="CONTEXTS" label="上下文长度" />
            <span class="field-hint">按模型名称自动估计，以服务商文档为准</span>
          </div>
          <label v-if="form.context === 'custom'" class="field">
            <span class="field-label">自定义长度</span>
            <input
              v-model="form.context_custom"
              name="context_custom"
              placeholder="300K"
              :aria-invalid="!!err('context_custom')"
            />
            <span v-if="err('context_custom')" class="field-error">{{ err('context_custom') }}</span>
            <span v-else class="field-hint">可写 300K、1.5M 或 token 数</span>
          </label>
          <label class="field">
            <span class="field-label">温度</span>
            <input
              v-model.number="form.temperature"
              name="temperature"
              type="number"
              min="0"
              max="2"
              step="0.1"
              :aria-invalid="!!err('temperature')"
            />
            <span v-if="err('temperature')" class="field-error">{{ err('temperature') }}</span>
            <span v-else class="field-hint">整理文件建议 0.3；0 表示不发送，用模型默认</span>
          </label>
          <label class="field">
            <span class="field-label">单次最多输出</span>
            <input
              v-model.number="form.max_tokens"
              name="max_tokens"
              type="number"
              min="1"
              placeholder="留空"
              :aria-invalid="!!err('max_tokens')"
            />
            <span v-if="err('max_tokens')" class="field-error">{{ err('max_tokens') }}</span>
            <span v-else class="field-hint">留空：{{ maxTokensDefault(form.provider) }}</span>
          </label>
          <div class="field">
            <span class="field-label">能读图</span>
            <Switch v-model="form.vision_enabled" label="能读图" />
          </div>
          <div class="field">
            <span class="field-label">开启思考</span>
            <Switch v-model="form.thinking_enabled" label="开启思考" />
          </div>
          <div v-if="form.thinking_enabled" class="field">
            <span class="field-label">思考强度</span>
            <Combobox v-model="form.effort" :load="loadEfforts" placeholder="模型默认" :invalid="!!err('effort')" />
            <span v-if="err('effort')" class="field-error">{{ err('effort') }}</span>
          </div>
        </div>
      </details>

      <Alert
        v-if="showModel && testedNow"
        :tone="testedNow.ok ? 'success' : 'danger'"
        :title="testedNow.ok ? `${form.model} 可以用` : `${form.model} 没有通过测试`"
      >
        {{ testedNow.ok ? `回复：${testedNow.text}` : testedNow.text }}
        <p v-if="testedNow.detail" class="detail text-sm text-muted">{{ testedNow.detail }}</p>
      </Alert>
    </form>

    <template #footer>
      <button v-if="!editing && !wizard.isFirst.value" type="button" class="ghost" @click="wizard.back">
        <Icon name="arrow-left" :size="14" />上一步
      </button>
      <button v-if="editing" type="button" :disabled="testing" @click="runTest">
        {{ testing ? '测试中' : '测试连接' }}
      </button>
      <span class="foot-spacer" />
      <button type="button" @click="close">取消</button>
      <button
        v-if="!editing && step === 'key'"
        type="submit"
        form="model-form"
        class="primary"
        :disabled="wizard.busy.value"
      >
        {{ wizard.busy.value ? '正在获取模型' : '下一步' }}
      </button>
      <button
        v-else-if="editing || step === 'model'"
        type="submit"
        form="model-form"
        class="primary"
        :disabled="saving || testing"
      >
        {{ saveLabel }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.choices {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: var(--space-2);
}
.choice {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-1);
  height: auto;
  padding: var(--space-3);
  text-align: left;
  white-space: normal;
  border-radius: var(--radius-md);
}
.choice.chosen {
  border-color: var(--accent);
}
.choice-title {
  font-weight: 600;
  color: var(--text);
}
.choice-desc {
  line-height: 1.5;
  font-size: var(--text-sm);
  color: var(--text-muted);
}
.model-search {
  display: flex;
  gap: var(--space-2);
}
.model-search input {
  flex: 1;
  min-width: 0;
}
.model-list {
  display: flex;
  flex-direction: column;
  max-height: 220px;
  min-height: 72px;
  overflow-y: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}
.model-row {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-2);
  width: 100%;
  height: auto;
  min-height: 34px;
  padding: var(--space-1) var(--space-3);
  text-align: left;
  background: transparent;
  border: 0;
  border-bottom: 1px solid var(--border);
  border-radius: 0;
}
.model-row:hover {
  background: var(--surface-hover);
}
.model-row.selected {
  background: var(--accent-soft);
}
.row-name {
  max-width: none;
  flex: 1;
  min-width: 0;
}
.model-empty {
  margin: auto;
  padding: var(--space-3);
  text-align: center;
}
.summary {
  margin: 0;
}
.detail {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: var(--space-1) 0 0;
  word-break: break-all;
}
.advanced {
  margin-top: var(--space-3);
}
.foot-spacer {
  flex: 1;
}
</style>
