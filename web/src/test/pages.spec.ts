import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'
import { filesystem, modelProvider, session } from './fixtures'

vi.mock('@/api/sessions', () => ({
  listSessions: vi.fn(async () => [
    session({
      id: '7',
      filesystem_id: '1',
      scan_path: '照片',
      goal: '照片按年份整理',
      total_files: 1200,
      scanned_count: 1200,
      tagged_files: 300,
      planned_ops: 300,
      total_size: 1024,
    }),
  ]),
  deleteSession: vi.fn(),
}))

vi.mock('@/api/settings', () => ({
  listFilesystems: vi.fn(async () => [filesystem({ id: '1', name: '家里的 NAS', protocol: 'smb' })]),
  listModels: vi.fn(async () => []),
  createModel: vi.fn(async () => ({})),
  updateModel: vi.fn(async () => ({})),
  testModel: vi.fn(async () => ({ success: true, reply: '你好' })),
  listProviderModels: vi.fn(async () => ({ success: true, models: ['deepseek-chat', 'deepseek-flash', 'other-vl'] })),
  createFilesystem: vi.fn(async (b: object) => ({ id: '9', ...b })),
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

// a ChoiceGrid card: pick it by its title
async function choose(title: string) {
  const card = [...document.body.querySelectorAll('label.choice-card')].find((l) => l.textContent?.includes(title))
  ;(card?.querySelector('input') as HTMLInputElement).click()
  await flushPromises()
}

const rowByText = (text: string) =>
  [...document.body.querySelectorAll('tr.clickable')].find((r) => r.textContent?.includes(text)) as HTMLElement

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

describe('SessionOverview', () => {
  it('shows the running scan phase with its progress and the files found so far', async () => {
    const { default: SessionOverview } = await import('@/pages/SessionOverview.vue')
    const { emptyTimeline } = await import('@/lib/transcript')
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
    })
    await router.push('/sessions/7')
    const s = session({
      status: 'scanning',
      scanned_count: 3_700_000,
      scan_phase: 'duplicates',
      phase_done: 2 * 2 ** 30,
      phase_total: 8 * 2 ** 30,
    })
    const w = mount(SessionOverview, {
      props: { session: s, runs: [], timeline: emptyTimeline(), hasDraft: false },
      global: { plugins: [router] },
    })
    await flushPromises()
    expect(w.text()).toContain('查找重复文件')
    expect(w.text()).toContain('已找到 3,700,000 个')
    expect(w.text()).toContain('2.0 GB / 最多 8.0 GB')
    expect(w.find('[role="progressbar"]').exists()).toBe(true)

    await w.setProps({ session: { ...s, scan_phase: 'walk', phase_done: 0, phase_total: 0 } })
    expect(w.text()).toContain('遍历文件夹')
    expect(w.find('[role="progressbar"]').exists()).toBe(false)
    w.unmount()
  })
})

describe('ModelFormDialog', () => {
  it('edits on one page, lists provider models and shows the test reply', async () => {
    const { default: ModelFormDialog } = await import('@/pages/ModelFormDialog.vue')
    const editing = modelProvider({
      id: '3',
      name: '主力',
      model: 'gpt-5',
      base_url: 'https://api.example.com/v1',
      temperature: 0.3,
      context_window: 300_000,
      thinking_enabled: true,
      reasoning_effort: 'ultra',
    })
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
      { provider: 'openai', base_url: 'https://api.example.com/v1', api_key: undefined, id: '3' },
      expect.anything(),
    )
    ;(body.querySelector('input[role="combobox"]') as HTMLInputElement).click()
    await flushPromises()
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
    await choose('DeepSeek')
    buttonByText('下一步').click()
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
    expect((document.body.querySelector('input[role="combobox"]') as HTMLInputElement).value).toBe('deepseek-flash')
    expect(document.body.textContent).toContain('上下文 1M')
    buttonByText('测试并保存').click()
    await flushPromises()
    expect(settings.testModel).toHaveBeenCalled()
    expect(settings.createModel).toHaveBeenLastCalledWith(
      expect.objectContaining({
        name: 'deepseek-flash',
        model: 'deepseek-flash',
        api_key: 'sk-test',
        temperature: 0,
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
    await choose('NAS 共享')
    buttonByText('下一步').click()
    await flushPromises()
    expect((document.body.querySelector('input[name="port"]') as HTMLInputElement).value).toBe('445')
    await typeInto('host', '192.168.1.10')
    await typeInto('username', 'me')
    await typeInto('password', 'pw')
    buttonByText('连接').click()
    await flushPromises()
    // the share list comes first; open one
    rowByText('photos').click()
    await flushPromises()
    expect(settings.testFilesystem).toHaveBeenLastCalledWith(
      expect.objectContaining({ protocol: 'smb', host: '192.168.1.10', base_path: 'photos', password: 'pw' }),
      expect.anything(),
    )
    buttonByText('使用这个文件夹').click()
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
    const editing = filesystem({
      id: '4',
      name: 'srv',
      protocol: 'sftp',
      base_path: '/data',
      host: 'h',
      port: 22,
      username: 'u',
      password_set: true,
      host_key: 'SHA256:x',
    })
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
      '4',
      expect.objectContaining({ password: '', private_key: expect.stringContaining('BEGIN'), key_passphrase: '' }),
    )
    w.unmount()
  })
})
