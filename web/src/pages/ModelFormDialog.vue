<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Combobox, { type ComboOption } from '@/base/Combobox.vue'
import Select from '@/base/Select.vue'
import Switch from '@/base/Switch.vue'
import { errorMessage } from '@/base/errors'
import { fmtLabel, labelOptions } from '@/base/format'
import { PROVIDER } from '@/base/labels'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { createModel, testModel, updateModel, type ModelInput } from '@/api/settings'
import type { ModelProvider } from '@/api/types'
import {
  CONTEXT_PRESETS,
  DEFAULT_CONTEXT,
  baseUrlError,
  contextError,
  effortError,
  effortLabel,
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
  // '' = model default (sent as 0)
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
const guard = useDirtyGuard(() => form)

const contextTokens = computed(() =>
  form.context === 'custom' ? parseTokens(form.context_custom) : (form.context as number),
)
const keyRequired = computed(() => !props.editing?.api_key_set && form.provider !== 'ollama')
const effort = computed(() => (form.thinking_enabled ? (form.effort?.value ?? '') : ''))

const connectionRules = () => ({
  model: form.model.trim() ? '' : '请输入模型名称',
  base_url: baseUrlError(form.base_url),
  api_key: keyRequired.value && !form.api_key ? '请输入密钥' : '',
})
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
const v = useFormValidation(() => ({ ...connectionRules(), ...advancedRules() }))
// 测试连接 checks only what the connection needs.
const vt = useFormValidation(connectionRules)
type ConnKey = keyof ReturnType<typeof connectionRules>
const err = (k: ConnKey | keyof ReturnType<typeof advancedRules>) =>
  v.error(k) || (k in connectionRules() ? vt.error(k as ConnKey) : '')

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
            max_tokens: m.max_tokens || '',
            context: isContextPreset(m.context_window) ? m.context_window : 'custom',
            context_custom: isContextPreset(m.context_window) ? '' : fmtTokens(m.context_window),
            vision_enabled: m.vision_enabled,
            thinking_enabled: m.thinking_enabled,
            effort: m.reasoning_effort ? { value: m.reasoning_effort, label: effortLabel(m.reasoning_effort) } : null,
          }
        : empty(),
    )
    tested.value = null
    advanced.value = false
    v.reset()
    vt.reset()
    guard.reset()
  },
)

const PROVIDERS = labelOptions(PROVIDER) as { value: ModelProvider['provider']; label: string }[]
const CONTEXTS = [
  ...CONTEXT_PRESETS.map((p) => ({ value: p.value as number | 'custom', label: p.label })),
  { value: 'custom' as const, label: '自定义' },
]
const loadEfforts = async (q: string) => effortOptions(q)
const BASE_URL_PLACEHOLDER: Record<ModelProvider['provider'], string> = {
  openai: 'https://api.openai.com/v1',
  claude: 'https://api.anthropic.com',
  ollama: 'http://localhost:11434',
}

function body(): ModelInput {
  return {
    name: form.name.trim() || form.model.trim(),
    provider: form.provider,
    model: form.model.trim(),
    base_url: form.base_url.trim(),
    api_key: form.api_key || undefined,
    temperature: form.temperature,
    max_tokens: form.max_tokens || 0,
    context_window: contextTokens.value,
    vision_enabled: form.vision_enabled,
    thinking_enabled: form.thinking_enabled,
    reasoning_effort: form.thinking_enabled ? effort.value : undefined,
  }
}

interface TestResult {
  model: string
  host: string
  ok: boolean
  text: string
}
const tested = ref<TestResult | null>(null)
const testing = ref(false)
watch(
  () => JSON.stringify({ ...body(), name: '' }),
  () => (tested.value = null),
)

async function test() {
  if (!vt.validate() || testing.value) return
  const b = body()
  testing.value = true
  const host = b.base_url ? new URL(b.base_url).host : fmtLabel(PROVIDER, b.provider)
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
  if (!v.validate()) {
    if (Object.values(advancedRules()).some(Boolean)) advanced.value = true
    return
  }
  if (saving.value) return
  saving.value = true
  try {
    if (props.editing) await updateModel(props.editing.id, body())
    else await createModel(body())
    toast.success(props.editing ? '已保存' : '已创建')
    guard.reset()
    emit('saved')
    emit('close')
  } catch (e) {
    toast.error(props.editing ? '保存失败' : '创建失败', e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseDialog :open="open" :title="editing ? '编辑模型' : '新建模型'" width="620px" @close="close">
    <form id="model-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <div class="form-grid">
        <div class="field">
          <span class="field-label">接口类型</span>
          <Select v-model="form.provider" :options="PROVIDERS" label="接口类型" />
        </div>
        <label class="field">
          <span class="field-label">接口地址</span>
          <input
            v-model="form.base_url"
            name="base_url"
            class="mono"
            :placeholder="BASE_URL_PLACEHOLDER[form.provider]"
            :aria-invalid="!!err('base_url')"
          />
          <span v-if="err('base_url')" class="field-error">{{ err('base_url') }}</span>
        </label>
        <label class="field span-2">
          <span class="field-label" :class="{ required: keyRequired }">密钥</span>
          <input
            v-model="form.api_key"
            name="api_key"
            type="password"
            autocomplete="new-password"
            :placeholder="editing?.api_key_set ? '已设置，输入新密钥可替换' : ''"
            :aria-invalid="!!err('api_key')"
          />
          <span v-if="err('api_key')" class="field-error">{{ err('api_key') }}</span>
        </label>
        <label class="field">
          <span class="field-label required">模型名称</span>
          <input v-model="form.model" name="model" class="mono" placeholder="gpt-5" :aria-invalid="!!err('model')" />
          <span v-if="err('model')" class="field-error">{{ err('model') }}</span>
        </label>
        <label class="field">
          <span class="field-label">名称</span>
          <input v-model="form.name" name="name" maxlength="64" placeholder="默认同模型名称" />
        </label>
      </div>
      <details :open="advanced" @toggle="advanced = ($event.target as HTMLDetailsElement).open">
        <summary>更多设置</summary>
        <div class="form-grid advanced">
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
          </label>
          <label class="field">
            <span class="field-label">单次最多输出</span>
            <input
              v-model.number="form.max_tokens"
              name="max_tokens"
              type="number"
              min="1"
              placeholder="模型默认"
              :aria-invalid="!!err('max_tokens')"
            />
            <span v-if="err('max_tokens')" class="field-error">{{ err('max_tokens') }}</span>
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
              :aria-invalid="!!err('context_custom')"
            />
            <span v-if="err('context_custom')" class="field-error">{{ err('context_custom') }}</span>
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
            <Combobox v-model="form.effort" :load="loadEfforts" placeholder="模型默认" :invalid="!!err('effort')" />
            <span v-if="err('effort')" class="field-error">{{ err('effort') }}</span>
          </div>
        </div>
      </details>
      <Alert v-if="tested" :tone="tested.ok ? 'success' : 'danger'" :title="`${tested.model} · ${tested.host}`">
        {{ tested.ok ? `回复：${tested.text}` : `连接失败：${tested.text}` }}
      </Alert>
    </form>
    <template #footer>
      <button type="button" :disabled="testing" @click="test">{{ testing ? '测试中' : '测试连接' }}</button>
      <span class="foot-spacer" />
      <button type="button" @click="close">取消</button>
      <button type="submit" form="model-form" class="primary" :disabled="saving">
        {{ saving ? '保存中' : '保存' }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.advanced {
  margin-top: var(--space-3);
}
.foot-spacer {
  flex: 1;
}
</style>
