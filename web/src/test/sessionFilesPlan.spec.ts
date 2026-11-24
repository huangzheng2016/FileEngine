import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'
import { ApiError } from '@/api/http'
import { confirmState, resolveConfirm } from '@/base/useConfirm'
import { fileEntry, session } from './fixtures'

const api = vi.hoisted(() => ({
  listFiles: vi.fn(),
  getFileTree: vi.fn(),
  listCategories: vi.fn(),
  resetPlans: vi.fn(),
  getPlan: vi.fn(),
  savePlan: vi.fn(),
  listRules: vi.fn(),
}))
vi.mock('@/api/files', () => ({
  listFiles: api.listFiles,
  getFileTree: api.getFileTree,
  listCategories: api.listCategories,
  resetPlans: api.resetPlans,
}))
vi.mock('@/api/runs', () => ({
  getPlan: api.getPlan,
  savePlan: api.savePlan,
  draftPlan: vi.fn(),
  approvePlan: vi.fn(),
}))
vi.mock('@/api/rules', () => ({
  listRules: api.listRules,
  deleteRule: vi.fn(),
  updateRule: vi.fn(),
  previewRules: vi.fn(),
  applyRulesSummary: vi.fn(),
  createRule: vi.fn(),
}))
vi.mock('@/components/StreamMarkdown.vue', () => ({
  default: defineComponent({
    props: { source: { type: String, default: '' } },
    render() {
      return h('div', this.source)
    },
  }),
}))

const Stub = defineComponent({ render: () => h('div') })

async function mountAt(component: unknown, path: string, props: Record<string, unknown>) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: Stub }] })
  await router.push(path)
  await router.isReady()
  const w = mount(component as never, { props, global: { plugins: [router] }, attachTo: document.body } as never)
  await flushPromises()
  return { w, router }
}

const button = (text: string) =>
  [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim() === text) as HTMLButtonElement

beforeEach(() => {
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({ matches: false, addEventListener() {}, removeEventListener() {} })),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.clearAllMocks()
  document.body.innerHTML = ''
})

describe('SessionFiles', () => {
  it('lists the scan root with an empty parent_path and keeps the folder in the URL', async () => {
    api.listFiles.mockResolvedValue({
      items: [fileEntry({ id: '5', name: '照片', original_path: '照片', file_type: 'directory' })],
      total: 1,
    })
    api.getFileTree.mockResolvedValue([])
    api.listCategories.mockResolvedValue([])
    const { default: SessionFiles } = await import('@/pages/SessionFiles.vue')
    const { w, router } = await mountAt(SessionFiles, '/', { session: session({ id: '7' }) })
    expect(api.listFiles.mock.calls[0][0]).toMatchObject({ session_id: '7', parent_path: '', search: '', page: 1 })
    await w.find('tbody tr.clickable').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.dir).toBe('照片')
    expect(api.listFiles.mock.calls.at(-1)?.[0]).toMatchObject({ parent_path: '照片' })
    w.unmount()
  })

  it('confirms before sending decided files back to the assistant', async () => {
    api.listFiles.mockResolvedValue({
      items: [fileEntry({ id: '5', name: 'a.jpg', original_path: 'a.jpg', role: 'file', new_path: '/照片/a.jpg' })],
      total: 1,
    })
    api.getFileTree.mockResolvedValue([])
    api.listCategories.mockResolvedValue([])
    api.resetPlans.mockResolvedValue(1)
    const { default: SessionFiles } = await import('@/pages/SessionFiles.vue')
    const { w } = await mountAt(SessionFiles, '/', { session: session({ id: '7' }) })
    await w.find('tbody input[type="checkbox"]').setValue(true)
    button('重新整理').click()
    await flushPromises()
    expect(api.resetPlans).not.toHaveBeenCalled()
    expect(confirmState.open).toBe(true)
    expect(confirmState.message).toContain('现有的去向会被清除')
    resolveConfirm(true)
    await flushPromises()
    expect(api.resetPlans).toHaveBeenCalledWith('7', ['5'])
    w.unmount()
  })
})

describe('SessionPlan', () => {
  const plan = (version: number, content: string) => ({
    session_id: '7',
    version,
    status: 'draft',
    content,
    updated_at: 1_793_000_000,
    approved_at: 0,
  })

  it('keeps the typed text on a 409 and saves it on the newest version only when asked', async () => {
    api.getPlan.mockResolvedValueOnce(plan(2, '旧方案')).mockResolvedValue(plan(3, '别处改过的方案'))
    api.listRules.mockResolvedValue([])
    api.savePlan
      .mockRejectedValueOnce(new ApiError(409, 40900, '方案已被更新'))
      .mockResolvedValueOnce(plan(4, '我的方案'))
    const { default: SessionPlanPage } = await import('@/pages/SessionPlan.vue')
    const { w } = await mountAt(SessionPlanPage, '/', { sessionId: '7', rev: 0, busyReason: '' })
    button('编辑').click()
    await flushPromises()
    const area = document.body.querySelector('textarea[name="plan"]') as HTMLTextAreaElement
    area.value = '我的方案'
    area.dispatchEvent(new Event('input'))
    button('保存').click()
    await flushPromises()
    expect(api.savePlan).toHaveBeenLastCalledWith('7', '我的方案', 2)
    expect(document.body.textContent).toContain('方案已被更新')
    button('继续编辑').click()
    await flushPromises()
    expect((document.body.querySelector('textarea[name="plan"]') as HTMLTextAreaElement).value).toBe('我的方案')
    expect(document.body.textContent).toContain('方案已更新到版本 3')
    button('查看差异').click()
    await flushPromises()
    button('用我的修改覆盖').click()
    await flushPromises()
    expect(api.savePlan).toHaveBeenLastCalledWith('7', '我的方案', 3)
    expect(document.body.textContent).toContain('我的方案')
    w.unmount()
  })
})
