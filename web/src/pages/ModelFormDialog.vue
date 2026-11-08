<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Combobox, { type ComboOption } from '@/base/Combobox.vue'
import Select from '@/base/Select.vue'
import Switch from '@/base/Switch.vue'
import { errorMessage } from '@/base/errors'
import { labelOptions } from '@/base/format'
import { PROVIDER } from '@/base/labels'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { createModel, testModel, updateModel, type ModelInput } from '@/api/settings'
import type { ModelProvider } from '@/api/types'
import {
  CONTEXT_PRESETS,
  DEFAULT_CONTEXT,
  EFFORT_LABEL,
  baseUrlError,
  contextError,
  effortError,
  effortOptions,
  fmtTokens,
  isContextPreset,
  parseTokens,
} from '@/lib/modelForm'

const props = defineProps<{ open: boolean; editing: ModelProvider | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const empty = () => ({
  name: '',
  provider: 'openai' as ModelProvider['provider'],
  model: '',
  base_url: '',
  api_key: '',
  temperature: 0.3,
  max_tokens: 8192,
  context: DEFAULT_CONTEXT as number | 'custom',
  context_custom: '',
  vision_enabled: false,
  thinking_enabled: false,
  effort: null as ComboOption | null,
})
const form = reactive(empty())
const saving = ref(false)
const guard = useDirtyGuard(() => form)

const contextTokens = computed(() =>
  form.context === 'custom' ? parseTokens(form.context_custom) : (form.context as number),
)
const keyRequired = computed(() => !props.editing?.api_key_set && form.provider !== 'ollama')
const effort = computed(() => (form.thinking_enabled ? (form.effort?.value ?? '') : ''))

const v = useFormValidation(() => ({
  name: form.name.trim() ? '' : '请输入名称',
  model: form.model.trim() ? '' : '请输入模型名称',
  base_url: baseUrlError(form.base_url),
  api_key: keyRequired.value && !form.api_key ? '请输入密钥' : '',
  temperature: form.temperature < 0 || form.temperature > 2 ? '温度需在 0 到 2 之间' : '',
  max_tokens: !Number.isInteger(form.max_tokens) || form.max_tokens < 1 ? '请输入单次最多输出' : '',
  context_custom: form.context === 'custom' ? contextError(contextTokens.value) : '',
  effort: effortError(effort.value),
}))

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
            name: m.name,
            provider: m.provider,
            model: m.model,
            base_url: m.base_url,
            temperature: m.temperature,
            max_tokens: m.max_tokens,
            context: isContextPreset(m.context_window) ? m.context_window : 'custom',
            context_custom: isContextPreset(m.context_window) ? '' : fmtTokens(m.context_window),
            vision_enabled: m.vision_enabled,
            thinking_enabled: m.thinking_enabled,
            effort: m.reasoning_effort ? { value: m.reasoning_effort, label: m.reasoning_effort } : null,
          }
        : empty(),
    )
    tested.value = null
    v.reset()
    guard.reset()
  },
)

const PROVIDERS = labelOptions(PROVIDER) as { value: ModelProvider['provider']; label: string }[]
const CONTEXTS = [
  ...CONTEXT_PRESETS.map((p) => ({ value: p.value as number | 'custom', label: p.label })),
  { value: 'custom' as const, label: '自定义' },
]
const loadEfforts = async (q: string) => effortOptions(q).map((o) => ({ ...o, hint: o.hint ?? EFFORT_LABEL[o.value] }))

function body(): ModelInput {
  return {
    name: form.name.trim(),
    provider: form.provider,
    model: form.model.trim(),
    base_url: form.base_url.trim(),
    api_key: form.api_key || undefined,
    temperature: form.temperature,
    max_tokens: form.max_tokens,
    context_window: contextTokens.value,
    vision_enabled: form.vision_enabled,
    thinking_enabled: form.thinking_enabled,
    reasoning_effort: form.thinking_enabled ? effort.value : undefined,
  }
}

// ---------- test ----------

interface TestResult {
  model: string
  host: string
  ok: boolean
  text: string
}
const tested = ref<TestResult | null>(null)
const testing = ref(false)

async function test() {
  if (!v.validate() || testing.value) return
  const b = body()
  testing.value = true
  const host = b.base_url ? new URL(b.base_url).host : PROVIDER[b.provider].text
  try {
    const r = await testModel({ ...b, id: props.editing?.id })
    tested.value = { model: b.model, host, ok: r.success, text: r.success ? r.reply || '—' : r.error || '—' }
  } catch (e) {
    tested.value = { model: b.model, host, ok: false, text: errorMessage(e) }
  } finally {
    testing.value = false
  }
}

async function close() {
  if (await guard.confirmDiscard()) emit('close')
}

async function submit() {
  if (!v.validate() || saving.value) return
  saving.value = true
  try {
    if (props.editing) await updateModel(props.editing.id, body())
    else await createModel(body())
    toast.success(props.editing ? '已保存' : '已添加')
    guard.reset()
    emit('saved')
    emit('close')
  } catch (e) {
    toast.error(props.editing ? '保存失败' : '添加失败', e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseDialog :open="open" :title="editing ? '编辑模型' : '添加模型'" width="620px" @close="close">
    <form id="model-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <div class="form-grid">
        <label class="field">
          <span class="field-label required">名称</span>
          <input
            v-model="form.name"
            name="name"
            maxlength="64"
            placeholder="主力模型"
            :aria-invalid="!!v.error('name')"
          />
          <span v-if="v.error('name')" class="field-error">{{ v.error('name') }}</span>
        </label>
        <div class="field">
          <span class="field-label">接口类型</span>
          <Select v-model="form.provider" :options="PROVIDERS" label="接口类型" />
        </div>
        <label class="field">
          <span class="field-label required">模型名称</span>
          <input
            v-model="form.model"
            name="model"
            class="mono"
            placeholder="gpt-5"
            :aria-invalid="!!v.error('model')"
          />
          <span v-if="v.error('model')" class="field-error">{{ v.error('model') }}</span>
        </label>
        <label class="field">
          <span class="field-label">接口地址</span>
          <input
            v-model="form.base_url"
            name="base_url"
            class="mono"
            placeholder="https://api.openai.com/v1"
            :aria-invalid="!!v.error('base_url')"
          />
          <span v-if="v.error('base_url')" class="field-error">{{ v.error('base_url') }}</span>
        </label>
        <label class="field span-2">
          <span class="field-label" :class="{ required: keyRequired }">密钥</span>
          <input
            v-model="form.api_key"
            name="api_key"
            type="password"
            autocomplete="new-password"
            :placeholder="editing?.api_key_set ? '已设置，输入新密钥可替换' : ''"
            :aria-invalid="!!v.error('api_key')"
          />
          <span v-if="v.error('api_key')" class="field-error">{{ v.error('api_key') }}</span>
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
            :aria-invalid="!!v.error('temperature')"
          />
          <span v-if="v.error('temperature')" class="field-error">{{ v.error('temperature') }}</span>
        </label>
        <label class="field">
          <span class="field-label">单次最多输出</span>
          <input
            v-model.number="form.max_tokens"
            name="max_tokens"
            type="number"
            min="1"
            :aria-invalid="!!v.error('max_tokens')"
          />
          <span v-if="v.error('max_tokens')" class="field-error">{{ v.error('max_tokens') }}</span>
          <span v-else class="field-hint">单位 token</span>
        </label>
        <div class="field">
          <span class="field-label">上下文长度</span>
          <Select v-model="form.context" :options="CONTEXTS" label="上下文长度" />
        </div>
        <label v-if="form.context === 'custom'" class="field">
          <span class="field-label">自定义长度</span>
          <input
            v-model="form.context_custom"
            name="context_custom"
            placeholder="300K"
            :aria-invalid="!!v.error('context_custom')"
          />
          <span v-if="v.error('context_custom')" class="field-error">{{ v.error('context_custom') }}</span>
          <span v-else class="field-hint">可写 300K、1.5M 或 token 数</span>
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
          <Combobox v-model="form.effort" :load="loadEfforts" placeholder="模型默认" :invalid="!!v.error('effort')" />
          <span v-if="v.error('effort')" class="field-error">{{ v.error('effort') }}</span>
          <span v-else class="field-hint">可直接输入其他级别</span>
        </div>
      </div>
      <Alert v-if="tested" :tone="tested.ok ? 'success' : 'danger'" :title="`${tested.model} · ${tested.host}`">
        {{ tested.ok ? `回复：${tested.text}` : `连接失败：${tested.text}` }}
      </Alert>
    </form>
    <template #footer>
      <button type="button" :disabled="testing" @click="test">{{ testing ? '测试中' : '测试连接' }}</button>
      <button type="button" @click="close">取消</button>
      <button type="submit" form="model-form" class="primary" :disabled="saving">
        {{ saving ? '保存中' : '保存' }}
      </button>
    </template>
  </BaseDialog>
</template>
