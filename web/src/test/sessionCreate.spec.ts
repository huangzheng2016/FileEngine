import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'
import { filesystem, modelProvider, session } from './fixtures'

const calls: string[] = []
const bodies: Record<string, unknown[]> = {}
const record = (name: string, ...args: unknown[]) => {
  calls.push(name)
  ;(bodies[name] ??= []).push(...args)
}

vi.mock('@/api/sessions', () => ({
  createSession: vi.fn(async (b: object) => {
    record('createSession', b)
    return session({ id: '42' })
  }),
}))
vi.mock('@/api/settings', () => ({
  listFilesystems: vi.fn(async () => [filesystem({ id: '1', name: '家里的 NAS' })]),
  listModels: vi.fn(async () => [modelProvider({ id: '3', name: '主力' })]),
  testFilesystem: vi.fn(async () => ({ success: true, dirs: [] })),
}))

const { createTask } = await import('@/lib/sessionCreate')
const { TASK_PRESETS } = await import('@/data/presets')
const api = await import('@/api/sessions')

const input = { filesystem_id: '1', scan_path: '', model_provider_id: '3', goal: '' } as never

beforeEach(() => {
  calls.length = 0
  for (const k of Object.keys(bodies)) delete bodies[k]
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({ matches: false, addEventListener() {}, removeEventListener() {} })),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('createTask', () => {
  const preset = TASK_PRESETS.find((p) => p.rules.length && p.categories.length)!

  it('sends the template and auto_tag in one request', async () => {
    const out = await createTask(input, preset, true)
    expect(out.id).toBe('42')
    expect(calls).toEqual(['createSession'])
    const body = bodies.createSession[0] as { auto_tag: boolean; rules: { position: number }[]; categories: unknown[] }
    expect(body.auto_tag).toBe(true)
    expect(body.rules.map((r) => r.position)).toEqual(preset.rules.map((_, i) => i))
    expect(body.categories).toHaveLength(preset.categories.length)
    expect(body.categories[0]).toMatchObject({ structure: '', description: expect.any(String) })
  })

  it('maps the switch to auto_tag and omits the template without a preset', async () => {
    await createTask(input, undefined, false)
    const body = bodies.createSession[0] as Record<string, unknown>
    expect(body.auto_tag).toBe(false)
    expect(body).not.toHaveProperty('rules')
    expect(body).not.toHaveProperty('categories')
  })

  it('lets a failed create reach the caller', async () => {
    vi.mocked(api.createSession).mockRejectedValueOnce(new Error('boom'))
    await expect(createTask(input, preset, true)).rejects.toThrow('boom')
  })
})

describe('SessionCreateDialog', () => {
  it('sends scope, agent switches and auto_tag in one request', async () => {
    const { default: Dialog } = await import('@/pages/SessionCreateDialog.vue')
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:p(.*)', component: defineComponent({ render: () => h('div') }) }],
    })
    await router.push('/sessions')
    await router.isReady()
    const w = mount(
      Dialog as never,
      {
        props: { open: false },
        global: { plugins: [router] },
        attachTo: document.body,
      } as never,
    )
    await w.setProps({ open: true } as never)
    await flushPromises()
    // The only storage and model are preselected.
    const form = document.body.querySelector('form#session-form') as HTMLFormElement
    form.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(calls).toEqual(['createSession'])
    expect(bodies.createSession[0]).toMatchObject({
      filesystem_id: '1',
      model_provider_id: '3',
      scope_exts: '',
      allow_read_file: true,
      allow_auto_category: true,
      allow_script: true,
      auto_tag: true,
    })
    w.unmount()
  })
})
