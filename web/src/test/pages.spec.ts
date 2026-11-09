import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'
import type { Filesystem, ModelProvider, ScanSession } from '@/api/types'

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
  listProviderModels: vi.fn(async () => ({ success: true, models: ['deepseek-chat', 'deepseek-flash', 'other-vl'] })),
  createFilesystem: vi.fn(async (b: object) => ({ id: 9, ...b })),
  updateFilesystem: vi.fn(async () => ({})),
  testFilesystem: vi.fn(async (b: { base_path: string }) =>
    b.base_path === ''
      ? { success: true, dirs: [{ name: 'photos', path: 'photos' }] }
      : b.base_path === 'photos'
        ? { success: true, dirs: [{ name: '2024', path: 'photos/2024' }] }
        : { success: true, dirs: [] },
  ),
}))

const settings = await import('@/api/settings')

// an exact label wins over a longer one that contains it (the steps bar repeats labels)
const buttonByText = (text: string) => {
  const all = [...document.body.querySelectorAll('button')]
  return (all.find((b) => b.textContent?.trim() === text) ??
    all.find((b) => b.textContent?.trim().includes(text))) as HTMLButtonElement
}

async function typeInto(name: string, value: string) {
  const el = document.body.querySelector(`[name="${name}"]`) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input'))
  await flushPromises()
}

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
  it('edits on one page, lists provider models and shows the test reply', async () => {
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
    expect((body.querySelector('input[name="api_key"]') as HTMLInputElement).placeholder).toContain('已保存')
    expect(body.textContent).toContain('思考（ultra）')
    buttonByText('获取列表').click()
    await flushPromises()
    expect(settings.listProviderModels).toHaveBeenLastCalledWith(
      { provider: 'openai', base_url: 'https://api.example.com/v1', api_key: undefined, id: 3 },
      expect.anything(),
    )
    expect(body.textContent).toContain('deepseek-chat')
    buttonByText('测试连接').click()
    await flushPromises()
    expect(body.textContent).toContain('gpt-5 可以用')
    expect(body.textContent).toContain('回复：你好')
    w.unmount()
  })

  it('walks provider → key → model, preselects a recommendation and tests before saving', async () => {
    const { default: ModelFormDialog } = await import('@/pages/ModelFormDialog.vue')
    const { w } = await withRouter(ModelFormDialog, '/sessions', { open: false, editing: null })
    await w.setProps({ open: true } as never)
    await flushPromises()
    buttonByText('DeepSeek').click()
    await flushPromises()
    expect((document.body.querySelector('input[name="base_url"]') as HTMLInputElement).value).toBe(
      'https://api.deepseek.com/v1',
    )
    buttonByText('下一步').click()
    await flushPromises()
    expect(document.body.textContent).toContain('请输入 API Key')
    await typeInto('api_key', 'sk-test')
    buttonByText('下一步').click()
    await flushPromises()
    // the preset's first recommendation that the provider also lists
    expect(document.body.textContent).toContain('已选 deepseek-flash')
    expect(document.body.textContent).toContain('上下文 1M')
    buttonByText('测试并保存').click()
    await flushPromises()
    expect(settings.testModel).toHaveBeenCalled()
    expect(settings.createModel).toHaveBeenLastCalledWith(
      expect.objectContaining({
        name: 'deepseek-flash',
        model: 'deepseek-flash',
        api_key: 'sk-test',
        temperature: 0.3,
      }),
    )
    w.unmount()
  })
})

describe('StorageFormDialog', () => {
  it('walks type → connection → folder → name and saves what was chosen', async () => {
    const { default: StorageFormDialog } = await import('@/pages/StorageFormDialog.vue')
    const { w } = await withRouter(StorageFormDialog, '/sessions', { open: false, editing: null })
    await w.setProps({ open: true } as never)
    await flushPromises()
    buttonByText('NAS 共享').click()
    await flushPromises()
    expect((document.body.querySelector('input[name="port"]') as HTMLInputElement).value).toBe('445')
    await typeInto('host', '192.168.1.10')
    await typeInto('username', 'me')
    await typeInto('password', 'pw')
    buttonByText('连接').click()
    await flushPromises()
    // the share list comes first; open one
    buttonByText('photos').click()
    await flushPromises()
    expect(settings.testFilesystem).toHaveBeenLastCalledWith(
      expect.objectContaining({ protocol: 'smb', host: '192.168.1.10', base_path: 'photos', password: 'pw' }),
      expect.anything(),
    )
    buttonByText('使用这个目录').click()
    await flushPromises()
    expect((document.body.querySelector('input[name="name"]') as HTMLInputElement).value).toBe('photos（192.168.1.10）')
    buttonByText('保存').click()
    await flushPromises()
    expect(settings.createFilesystem).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: 'photos（192.168.1.10）', protocol: 'smb', base_path: 'photos', port: 445 }),
    )
    expect(w.emitted('saved')).toBeTruthy()
    w.unmount()
  })

  it('switches SFTP between password and pasted key and never sends both', async () => {
    const { default: StorageFormDialog } = await import('@/pages/StorageFormDialog.vue')
    const editing = {
      id: 4,
      name: 'srv',
      description: '',
      protocol: 'sftp',
      base_path: '/data',
      host: 'h',
      port: 22,
      username: 'u',
      password_set: true,
      private_key_set: false,
      key_passphrase_set: false,
      host_key: 'SHA256:x',
    } as Filesystem
    const { w } = await withRouter(StorageFormDialog, '/sessions', { open: false, editing })
    await w.setProps({ open: true } as never)
    await flushPromises()
    expect(document.body.querySelector('textarea[name="private_key"]')).toBeNull()
    ;(document.body.querySelectorAll('[role="radio"]')[1] as HTMLButtonElement).click()
    await flushPromises()
    expect(document.body.querySelector('input[name="password"]')).toBeNull()
    buttonByText('保存').click()
    await flushPromises()
    expect(document.body.textContent).toContain('请粘贴私钥')
    const key = document.body.querySelector('textarea[name="private_key"]') as HTMLTextAreaElement
    key.value = '-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----'
    key.dispatchEvent(new Event('input'))
    await flushPromises()
    buttonByText('保存').click()
    await flushPromises()
    expect(settings.updateFilesystem).toHaveBeenLastCalledWith(
      4,
      expect.objectContaining({ password: '', private_key: expect.stringContaining('BEGIN'), key_passphrase: '' }),
    )
    w.unmount()
  })
})
