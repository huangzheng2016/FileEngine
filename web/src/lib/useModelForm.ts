import { computed, reactive, ref, watch } from 'vue'
import type { ComboOption } from '@/base/Combobox.vue'
import { errorMessage } from '@/base/errors'
import { fmtNumber } from '@/base/format'
import { toast } from '@/base/toast'
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
  fmtTokens,
  guessCaps,
  isContextPreset,
  modelOptions,
  parseTokens,
  presetFor,
  type ModelPreset,
} from '@/lib/modelForm'

const emptyForm = () => ({
  preset: 'deepseek',
  name: '',
  provider: 'openai' as ModelProvider['provider'],
  model: '',
  base_url: '',
  api_key: '',
  // 0 = not sent, the model's own default applies
  temperature: 0,
  // '' = not sent, the model's own default applies
  max_tokens: '' as number | '',
  context: DEFAULT_CONTEXT as number | 'custom',
  context_custom: '',
  vision_enabled: false,
  thinking_enabled: false,
  effort: null as ComboOption | null,
})

export interface ModelTestResult {
  key: string
  ok: boolean
  text: string
  detail: string
}

export const PRESET_OPTIONS = MODEL_PRESETS.map((p) => ({ value: p.id, label: p.title }))
export const CONTEXT_OPTIONS = [
  ...CONTEXT_PRESETS.map((p) => ({ value: p.value as number | 'custom', label: p.label })),
  { value: 'custom' as const, label: '自定义' },
]

// State and actions of the model dialog. New model: provider → connection → model, with the model list fetched
// from the provider and a test before saving. Editing: the same fields on one page.
export function useModelForm(editing: () => ModelProvider | null) {
  const form = reactive(emptyForm())
  const saving = ref(false)
  const advanced = ref(false)
  const nameTouched = ref(false)
  const skipCatalog = ref(false)

  const isCustom = computed(() => form.preset === 'custom')
  const preset = computed<ModelPreset>(() => MODEL_PRESETS.find((p) => p.id === form.preset) ?? MODEL_PRESETS[0])
  const contextTokens = computed(() =>
    form.context === 'custom' ? parseTokens(form.context_custom) : (form.context as number),
  )
  const keySaved = computed(() => {
    const e = editing()
    return !!e?.api_key_set && form.provider === e.provider
  })
  const keyRequired = computed(() => preset.value.keyRequired && !keySaved.value)
  const effort = computed(() => (form.thinking_enabled ? (form.effort?.value ?? '') : ''))
  const endpointChanged = computed(() => {
    const e = editing()
    return !!e && (e.provider !== form.provider || e.base_url !== form.base_url.trim())
  })

  // ---- validation ----

  const keyRules = () => ({
    base_url: form.preset === 'custom' && !form.base_url.trim() ? '请输入接口地址' : baseUrlError(form.base_url),
    api_key:
      keyRequired.value && !form.api_key.trim()
        ? '请输入 API Key'
        : editing()?.api_key_set && endpointChanged.value && !form.api_key.trim()
          ? '接口地址已变化，请重新输入 API Key'
          : '',
  })
  const modelRules = () => ({ model: form.model.trim() ? '' : '请选择或输入模型' })
  const advancedRules = () => ({
    temperature:
      typeof form.temperature !== 'number' || form.temperature < 0 || form.temperature > 2
        ? '温度需在 0 到 2 之间'
        : '',
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
  let catalogCtl: AbortController | null = null
  const resetCatalog = () =>
    Object.assign(catalog, { models: [], loaded: false, loading: false, error: '', detail: '' })

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
          id: editing()?.id,
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

  const loadModelOptions = async (query: string) => modelOptions(query, preset.value, catalog.models, catalog.loaded)

  function chooseModel(id: string) {
    form.model = id
    const caps = guessCaps(id, preset.value)
    form.context = isContextPreset(caps.context) ? caps.context : 'custom'
    form.context_custom = isContextPreset(caps.context) ? '' : fmtTokens(caps.context)
    form.vision_enabled = caps.vision
    form.thinking_enabled = caps.thinking
    if (!nameTouched.value) form.name = id
  }

  // The Combobox model: choosing runs the same side effects as every other way of picking a model.
  const picked = computed<ComboOption | null>({
    get: () => (form.model ? { value: form.model, label: form.model } : null),
    set: (o) => {
      if (o) chooseModel(o.value)
      else form.model = ''
    },
  })

  // ---- test ----

  const tested = ref<ModelTestResult | null>(null)
  const testing = ref(false)

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

  // A test result belongs to the settings it ran with; the name does not matter.
  const bodyKey = () => JSON.stringify({ ...body(), name: '' })
  const testedNow = computed(() => (tested.value?.key === bodyKey() ? tested.value : null))

  async function test(): Promise<boolean> {
    if (testing.value) return false
    testing.value = true
    const key = bodyKey()
    try {
      const r = await testModel({ ...body(), id: editing()?.id })
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

  // ---- wizard (new model only) ----

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
    form.model = ''
    Object.assign(catalog, { models: [], loaded: false, error: '', detail: '' })
  }

  function selectPreset(id: string) {
    const p = MODEL_PRESETS.find((x) => x.id === id)
    if (p && p.id !== form.preset) applyPreset(p)
  }

  // Editing: switching the provider fills its address and keeps the other settings.
  function changePreset(id: string) {
    const p = MODEL_PRESETS.find((x) => x.id === id)
    if (!p) return
    form.preset = p.id
    form.provider = p.provider
    form.base_url = p.baseUrl
    Object.assign(catalog, { models: [], loaded: false, error: '', detail: '' })
  }

  async function typeModelByHand() {
    skipCatalog.value = true
    await wizard.next()
    skipCatalog.value = false
  }

  // Entering the model step preselects the first recommendation.
  watch(step, (s) => {
    if (s !== 'model') return
    tested.value = null
    if (!form.model) {
      const opts = modelOptions('', preset.value, catalog.models, catalog.loaded)
      const first = opts.find((o) => o.hint === '推荐') ?? opts[0]
      if (first) chooseModel(first.value)
    }
  })

  // ---- open / save ----

  function init() {
    const m = editing()
    Object.assign(
      form,
      m
        ? {
            ...emptyForm(),
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
        : emptyForm(),
    )
    if (!m) applyPreset(MODEL_PRESETS[0])
    nameTouched.value = !!m
    tested.value = null
    advanced.value = false
    resetCatalog()
    wizard.reset()
    vk.reset()
    vm.reset()
  }

  function abort() {
    catalogCtl?.abort()
  }

  function validateAll() {
    const ok = vk.validate() && vm.validate()
    if (!ok && Object.values(advancedRules()).some(Boolean)) advanced.value = true
    return ok
  }

  // New models are tested first; a failed test can still be saved on purpose.
  async function submit(): Promise<ModelProvider | null> {
    if (!validateAll() || saving.value) return null
    const e = editing()
    if (!e && !testedNow.value && !(await test())) return null
    saving.value = true
    try {
      const saved = e ? await updateModel(e.id, body()) : await createModel(body())
      toast.success(e ? '已保存' : '已添加模型')
      return saved
    } catch (x) {
      toast.error(e ? '保存失败' : '添加失败', x)
      return null
    } finally {
      saving.value = false
    }
  }

  // What the model is told to do, in one line, so nobody has to open 高级设置 to know.
  const summary = computed(() =>
    [
      `上下文 ${fmtTokens(contextTokens.value)}`,
      form.vision_enabled ? '能读图' : '不读图',
      form.thinking_enabled ? `思考${effort.value ? `（${effortLabel(effort.value)}）` : ''}` : '不思考',
      form.temperature ? `温度 ${form.temperature}` : '温度用模型默认值',
      form.max_tokens ? `单次最多输出 ${fmtNumber(form.max_tokens)}` : '单次输出用模型默认值',
    ].join(' · '),
  )

  const saveLabel = computed(() => {
    if (saving.value) return '保存中'
    if (testing.value) return '测试中'
    if (editing()) return '保存'
    return testedNow.value && !testedNow.value.ok ? '仍然保存' : '测试并保存'
  })

  return {
    form,
    saving,
    advanced,
    nameTouched,
    isCustom,
    preset,
    contextTokens,
    keySaved,
    keyRequired,
    effort,
    endpointChanged,
    err,
    vk,
    vm,
    catalog,
    loadCatalog,
    loadModelOptions,
    chooseModel,
    picked,
    tested,
    testing,
    testedNow,
    body,
    bodyKey,
    test,
    runTest,
    wizard,
    step,
    selectPreset,
    changePreset,
    typeModelByHand,
    init,
    abort,
    submit,
    summary,
    saveLabel,
  }
}

export type ModelFormApi = ReturnType<typeof useModelForm>
