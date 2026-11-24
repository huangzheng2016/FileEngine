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
  startTagging: vi.fn(async (id: string) => {
    record('startTagging', id)
    return '1'
  }),
}))
vi.mock('@/api/rules', () => ({
  createRule: vi.fn(async (_id: string, r: object) => {
    record('createRule', r)
    return {}
  }),
}))
vi.mock('@/api/files', () => ({
  importCategories: vi.fn(async (_fs: string, items: unknown[]) => {
    record('importCategories', items)
    return { created: items.length, skipped: 0 }
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

  it('creates without auto tagging, adds the template, then starts tagging', async () => {
    const out = await createTask(input, preset, true)
    expect(out.failed).toBeUndefined()
    expect((bodies.createSession[0] as { auto_tag: boolean }).auto_tag).toBe(false)
    expect(calls).toEqual([
      'createSession',
      ...preset.rules.map(() => 'createRule'),
      'importCategories',
      'startTagging',
    ])
    expect(bodies.startTagging).toEqual(['42'])
  })

  it('does not start tagging when the user chose not to', async () => {
    await createTask(input, preset, false)
    expect(calls).not.toContain('startTagging')
  })

  it('does not start tagging when the template failed', async () => {
    const rules = await import('@/api/rules')
    vi.mocked(rules.createRule).mockRejectedValueOnce(new Error('boom'))
    const out = await createTask(input, preset, true)
    expect(out.failed?.step).toBe('template')
    expect(calls).toEqual(['createSession'])
  })

  it('reports a start failure with the session kept', async () => {
    vi.mocked(api.startTagging).mockRejectedValueOnce(new Error('boom'))
    const out = await createTask(input, undefined, true)
    expect(out.failed?.step).toBe('start')
    expect(out.session.id).toBe('42')
  })
})

describe('SessionCreateDialog', () => {
  it('sends scope and agent switches, then starts tagging', async () => {
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
    expect(calls).toEqual(['createSession', 'startTagging'])
    expect(bodies.createSession[0]).toMatchObject({
      filesystem_id: '1',
      model_provider_id: '3',
      scope_exts: '',
      allow_read_file: true,
      allow_auto_category: true,
      allow_script: true,
      auto_tag: false,
    })
    w.unmount()
  })
})
