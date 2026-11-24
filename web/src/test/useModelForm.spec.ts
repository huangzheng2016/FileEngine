import { beforeEach, describe, expect, it, vi } from 'vitest'
import { modelProvider } from './fixtures'

vi.mock('@/api/settings', () => ({
  createModel: vi.fn(async (b: object) => ({ id: '9', ...b })),
  updateModel: vi.fn(async (_id: string, b: object) => ({ id: '3', ...b })),
  testModel: vi.fn(async () => ({ success: true, reply: '你好', error: '', detail: '' })),
  listProviderModels: vi.fn(async () => ({ success: true, models: ['deepseek-chat', 'deepseek-flash', 'other-vl'] })),
}))

const settings = await import('@/api/settings')
const { useModelForm } = await import('@/lib/useModelForm')
const { MODEL_PRESETS, modelOptions } = await import('@/lib/modelForm')

const preset = (id: string) => MODEL_PRESETS.find((p) => p.id === id)!

beforeEach(() => vi.clearAllMocks())

describe('modelOptions', () => {
  const listed = ['deepseek-chat', 'deepseek-flash', 'other-vl']

  it('puts recommendations first, then what the provider lists', () => {
    const o = modelOptions('', preset('deepseek'), listed, true)
    expect(o.map((x) => x.value)).toEqual(['deepseek-flash', 'deepseek-chat', 'other-vl'])
    expect(o.filter((x) => x.hint === '推荐').map((x) => x.value)).toEqual(['deepseek-flash', 'deepseek-chat'])
  })

  it('drops recommendations the provider does not list, but keeps them when it cannot list', () => {
    expect(modelOptions('', preset('deepseek'), ['other-vl'], true).map((x) => x.value)).toEqual(['other-vl'])
    expect(modelOptions('', preset('deepseek'), [], false).map((x) => x.value)).toEqual([
      'deepseek-flash',
      'deepseek-chat',
      'deepseek-reasoner',
    ])
  })

  it('offers the typed name first unless it is already an option', () => {
    expect(modelOptions('brand-new', preset('deepseek'), listed, true)[0]).toEqual({
      value: 'brand-new',
      label: 'brand-new',
      hint: '使用这个名称',
    })
    expect(modelOptions('deepseek-chat', preset('deepseek'), listed, true)[0].value).toBe('deepseek-chat')
    expect(modelOptions('deepseek-chat', preset('deepseek'), listed, true)).toHaveLength(1)
  })

  it('filters case-insensitively and caps the list', () => {
    expect(modelOptions('VL', preset('deepseek'), listed, true).map((x) => x.value)).toEqual(['VL', 'other-vl'])
    const many = Array.from({ length: 500 }, (_, i) => `m-${i}`)
    expect(modelOptions('', preset('custom'), many, true)).toHaveLength(200)
  })
})

describe('useModelForm', () => {
  function fresh(editing: ReturnType<typeof modelProvider> | null = null) {
    const m = useModelForm(() => editing)
    m.init()
    return m
  }

  it('starts on the first preset with its address and temperature', () => {
    const m = fresh()
    expect(m.form).toMatchObject({ preset: 'deepseek', base_url: 'https://api.deepseek.com/v1', temperature: 0.3 })
  })

  it('applies a preset only when it changes and forgets the model chosen for the old one', () => {
    const m = fresh()
    m.chooseModel('deepseek-chat')
    m.selectPreset('deepseek')
    expect(m.form.model).toBe('deepseek-chat')
    m.selectPreset('claude')
    expect(m.form).toMatchObject({ preset: 'claude', provider: 'claude', model: '', temperature: 0 })
  })

  it('guesses abilities and the name when a model is picked, until the name is edited', () => {
    const m = fresh()
    m.picked.value = { value: 'qwen-vl-max', label: 'qwen-vl-max' }
    expect(m.form).toMatchObject({ model: 'qwen-vl-max', name: 'qwen-vl-max', vision_enabled: true, context: 128_000 })
    m.nameTouched.value = true
    m.form.name = '读图'
    m.picked.value = { value: 'deepseek-reasoner', label: 'deepseek-reasoner' }
    expect(m.form).toMatchObject({ name: '读图', thinking_enabled: true, vision_enabled: false })
  })

  it('walks provider → connection → model, fetching the list and preselecting a recommendation', async () => {
    const m = fresh()
    expect(await m.wizard.next()).toBe(true)
    expect(m.step.value).toBe('key')
    expect(await m.wizard.next()).toBe(false)
    expect(m.err('api_key')).toBe('请输入 API Key')
    m.form.api_key = ' sk-test '
    expect(await m.wizard.next()).toBe(true)
    expect(settings.listProviderModels).toHaveBeenLastCalledWith(
      { provider: 'openai', base_url: 'https://api.deepseek.com/v1', api_key: 'sk-test', id: undefined },
      expect.anything(),
    )
    await Promise.resolve()
    expect(m.step.value).toBe('model')
    expect(m.form.model).toBe('deepseek-flash')
    expect(m.form.context).toBe(1_000_000)
  })

  it('stays on the connection step when the list fails, and lets the name be typed by hand', async () => {
    vi.mocked(settings.listProviderModels).mockResolvedValueOnce({
      success: false,
      models: [],
      error: '密钥无效',
      detail: '401',
    } as never)
    const m = fresh()
    await m.wizard.next()
    m.form.api_key = 'sk'
    expect(await m.wizard.next()).toBe(false)
    expect(m.catalog).toMatchObject({ error: '密钥无效', detail: '401', loaded: false })
    await m.typeModelByHand()
    expect(m.step.value).toBe('model')
    expect((await m.loadModelOptions('')).map((o) => o.value)).toContain('deepseek-flash')
  })

  it('sends an empty max_tokens as 0 and describes it as the model default', () => {
    const m = fresh()
    m.chooseModel('deepseek-chat')
    expect(m.body().max_tokens).toBe(0)
    expect(m.summary.value).toContain('单次输出用模型默认值')
    expect(m.summary.value).not.toMatch(/8192|64000/)
    m.form.max_tokens = 12345
    expect(m.body().max_tokens).toBe(12345)
    expect(m.summary.value).toContain('单次最多输出 12,345')
  })

  it('invalidates a test result as soon as a tested setting changes, but not for a rename', async () => {
    const m = fresh()
    m.form.api_key = 'sk'
    m.chooseModel('deepseek-chat')
    expect(await m.test()).toBe(true)
    expect(m.testedNow.value?.ok).toBe(true)
    m.form.name = 'another name'
    expect(m.testedNow.value).not.toBeNull()
    m.form.temperature = 0.7
    expect(m.testedNow.value).toBeNull()
  })

  it('tests a new model before saving and saves a failed test only when asked again', async () => {
    vi.mocked(settings.testModel).mockResolvedValueOnce({ success: false, reply: '', error: '余额不足', detail: '' })
    const m = fresh()
    m.form.api_key = 'sk'
    m.chooseModel('deepseek-chat')
    expect(await m.submit()).toBeNull()
    expect(settings.createModel).not.toHaveBeenCalled()
    expect(m.saveLabel.value).toBe('仍然保存')
    expect(await m.submit()).toMatchObject({ id: '9', model: 'deepseek-chat', max_tokens: 0 })
    expect(settings.testModel).toHaveBeenCalledTimes(1)
  })

  it('does not save without a model and opens the advanced section for its errors', async () => {
    const m = fresh()
    m.form.api_key = 'sk'
    expect(await m.submit()).toBeNull()
    expect(m.err('model')).toBe('请选择或输入模型')
    m.chooseModel('deepseek-chat')
    m.form.temperature = 3
    expect(await m.submit()).toBeNull()
    expect(m.advanced.value).toBe(true)
    expect(settings.createModel).not.toHaveBeenCalled()
  })

  it('edits without a test and keeps the stored key unless the endpoint changes', async () => {
    const stored = modelProvider({
      id: '3',
      provider: 'openai',
      base_url: 'https://api.deepseek.com/v1',
      model: 'deepseek-chat',
      name: '主力',
      api_key_set: true,
    })
    const m = fresh(stored)
    expect(m.keySaved.value).toBe(true)
    expect(m.endpointChanged.value).toBe(false)
    expect(m.body().api_key).toBeUndefined()
    expect(await m.submit()).toMatchObject({ id: '3', name: '主力' })
    expect(settings.testModel).not.toHaveBeenCalled()

    m.form.base_url = 'https://example.com/v1'
    expect(m.endpointChanged.value).toBe(true)
    expect(await m.submit()).toBeNull()
    expect(m.err('api_key')).toBe('接口地址已变化，请重新输入 API Key')
    m.form.api_key = 'sk-new'
    expect(await m.submit()).not.toBeNull()
    expect(settings.updateModel).toHaveBeenLastCalledWith('3', expect.objectContaining({ api_key: 'sk-new' }))
  })

  it('puts a custom effort in the body only while thinking is on', () => {
    const m = fresh()
    m.chooseModel('deepseek-chat')
    m.form.effort = { value: 'ultra', label: 'ultra' }
    expect(m.body().reasoning_effort).toBeUndefined()
    m.form.thinking_enabled = true
    expect(m.body().reasoning_effort).toBe('ultra')
  })
})
