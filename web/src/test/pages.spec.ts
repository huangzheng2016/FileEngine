import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'
import type { ModelProvider, ScanSession } from '@/api/types'

vi.mock('@/api/sessions', () => ({
  listSessions: vi.fn(async () => [
    {
      id: 7,
      filesystem_id: 1,
      scan_path: '照片',
      goal: '照片按年份整理',
      status: 'scanned',
      total_files: 1200,
      scanned_count: 1200,
      tagged_files: 300,
      planned_ops: 300,
      executed_ops: 0,
      review_count: 0,
      total_size: 1024,
      created_at: '2026-11-01T00:00:00Z',
      updated_at: '2026-11-01T00:00:00Z',
    } as Partial<ScanSession>,
  ]),
  deleteSession: vi.fn(),
}))

vi.mock('@/api/settings', () => ({
  listFilesystems: vi.fn(async () => [{ id: 1, name: '家里的 NAS', protocol: 'smb' }]),
  listModels: vi.fn(async () => []),
  createModel: vi.fn(async () => ({})),
  updateModel: vi.fn(async () => ({})),
  testModel: vi.fn(async () => ({ success: true, reply: '你好' })),
}))

const Stub = defineComponent({ render: () => h('div') })

async function withRouter(component: unknown, path: string, props: Record<string, unknown> = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions', component: Stub },
      { path: '/sessions/:id', component: Stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  const w = mount(component as never, { props, global: { plugins: [router] }, attachTo: document.body } as never)
  await flushPromises()
  return { w, router }
}

beforeEach(() => {
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({ matches: false, addEventListener() {}, removeEventListener() {} })),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('SessionList', () => {
  it('lists sessions with their storage and stage and opens the task on click', async () => {
    const { default: SessionList } = await import('@/pages/SessionList.vue')
    const { w, router } = await withRouter(SessionList, '/sessions')
    expect(w.text()).toContain('/照片')
    expect(w.text()).toContain('家里的 NAS')
    expect(w.text()).toContain('照片按年份整理')
    expect(w.text()).toContain('待执行')
    await w.find('tbody tr.clickable').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/sessions/7')
    w.unmount()
  })
})

describe('ModelFormDialog', () => {
  it('validates the custom reasoning level and shows the test reply with the inputs it ran with', async () => {
    const { default: ModelFormDialog } = await import('@/pages/ModelFormDialog.vue')
    const editing = {
      id: 3,
      name: '主力',
      provider: 'openai',
      model: 'gpt-5',
      base_url: 'https://api.example.com/v1',
      temperature: 0.3,
      max_tokens: 4096,
      context_window: 300_000,
      vision_enabled: false,
      thinking_enabled: true,
      reasoning_effort: 'ultra',
      api_key_set: true,
    } as ModelProvider
    const { w } = await withRouter(ModelFormDialog, '/sessions', { open: false, editing })
    await w.setProps({ open: true } as never)
    await flushPromises()
    const body = document.body
    expect((body.querySelector('input[name="context_custom"]') as HTMLInputElement).value).toBe('300K')
    expect((body.querySelector('input[name="api_key"]') as HTMLInputElement).placeholder).toContain('已设置')
    const testBtn = [...body.querySelectorAll('button')].find((b) => b.textContent?.includes('测试连接'))!
    testBtn.click()
    await flushPromises()
    expect(body.textContent).toContain('gpt-5 · api.example.com')
    expect(body.textContent).toContain('回复：你好')
    w.unmount()
  })
})
