import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'
import type { FileEntry, SessionEvent } from '@/api/types'
import { FLOW_STEPS, flowStep } from '@/lib/sessionView'
import { landing, runActivity, toolDecisions } from '@/lib/toolView'
import { applyEvents, emptyTimeline, type ToolItem } from '@/lib/transcript'

let seq = 0
const ev = (type: string, data: Record<string, unknown>): SessionEvent => ({ seq: ++seq, type, data })

describe('run activity', () => {
  it('lists what a decision call wrote, minus rejected items, newest result first', () => {
    seq = 0
    const t = applyEvents(emptyTimeline(), [
      ev('run_status', { run_id: 1, phase: 'tagging', status: 'running' }),
      ev('pull_begin', { run_id: 1, worker_id: 0, unit_id: 'u1', label: '下载', count: 3 }),
      ev('pull_begin', { run_id: 1, worker_id: 1, unit_id: 'u2', label: '我的项目', count: 1 }),
      ev('tool_call', {
        run_id: 1,
        worker_id: 0,
        call_id: 'c1',
        name: 'set_targets',
        args: {
          items: [
            { path: '下载/a.jpg', new_path: '/照片/a.jpg', reason: '照片', confidence: 0.9 },
            { path: '下载/b.txt', new_path: '/文档/b.txt', reason: '拿不准', confidence: 0.4 },
            { path: '下载/c.bin', new_path: '/坏/c.bin', reason: '' },
          ],
        },
      }),
      ev('tool_result', {
        run_id: 1,
        worker_id: 0,
        call_id: 'c1',
        name: 'set_targets',
        ok: false,
        output: { success: false, ok: 2, failed: [{ path: '下载/c.bin', error: '不在分类下' }] },
      }),
      ev('tool_call', {
        run_id: 1,
        worker_id: 1,
        call_id: 'c2',
        name: 'decide_dirs',
        args: {
          items: [
            { path: '我的项目', role: 'unit', new_path: '/项目/我的项目', reason: '代码项目' },
            { path: '杂物', role: 'container', reason: '逐项' },
          ],
        },
      }),
      ev('tool_result', {
        run_id: 1,
        worker_id: 1,
        call_id: 'c2',
        name: 'decide_dirs',
        ok: true,
        output: { success: true },
      }),
      ev('pull_done', { run_id: 1, worker_id: 1, unit_id: 'u2', tagged: 1, failed: 0 }),
    ])
    const a = runActivity(t.runs[1])
    expect(a.current.map((u) => u.label)).toEqual(['下载'])
    expect(a.decisions.map((d) => [d.path, d.target, d.review])).toEqual([
      ['我的项目', '/项目/我的项目', false],
      ['下载/a.jpg', '/照片/a.jpg', false],
      ['下载/b.txt', '/文档/b.txt', true],
    ])
  })

  it('ignores calls that failed as a whole or are still running', () => {
    const base = { kind: 'tool', id: 'x', name: 'set_targets', args: { items: [{ path: 'a', new_path: '/b/a' }] } }
    expect(toolDecisions({ ...base, status: 'running' } as ToolItem)).toEqual([])
    expect(toolDecisions({ ...base, status: 'failed', error: '未完成' } as ToolItem)).toEqual([])
    expect(toolDecisions({ ...base, status: 'ok', output: { error: '会话不存在' } } as ToolItem)).toEqual([])
  })

  it('shows where an entry lands as its folder unless the name changes', () => {
    expect(landing('下载/a.jpg', '/照片/2023/a.jpg')).toBe('照片/2023')
    expect(landing('下载/a.jpg', '/照片/a (1).jpg')).toBe('照片/a (1).jpg')
    expect(landing('a.jpg', '/a.jpg')).toBe('根文件夹')
    expect(landing('a.jpg', '')).toBe('保持原位')
  })

  it('maps every stage onto the four flow steps', () => {
    expect(flowStep('scanning')).toBe(0)
    expect(flowStep('ready')).toBe(1)
    expect(flowStep('review')).toBe(1)
    expect(flowStep('planned')).toBe(2)
    expect(flowStep('executing')).toBe(3)
    expect(flowStep('done')).toBe(FLOW_STEPS.length)
  })
})

const files: Partial<FileEntry>[] = [
  {
    id: 4,
    name: '未命名.bin',
    original_path: '桌面杂物/未命名.bin',
    new_path: '/未分类/未命名.bin',
    reason: '无法从名字判断用途',
    file_type: 'file',
  },
]
const confirmReview = vi.fn(async () => {})
const updateFileTarget = vi.fn(async () => ({}))
vi.mock('@/api/files', () => ({
  listFiles: vi.fn(async () => ({ items: files, total: files.length })),
  updateFileTarget: (...a: unknown[]) => updateFileTarget(...(a as [])),
}))
vi.mock('@/api/sessions', () => ({ confirmReview: (...a: unknown[]) => confirmReview(...(a as [])) }))

afterEach(() => {
  document.body.innerHTML = ''
})

describe('SessionReview', () => {
  it('shows each unsure decision in words and confirms it with one click', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: defineComponent({ render: () => h('div') }) }],
    })
    await router.push('/')
    const { default: SessionReview } = await import('@/pages/SessionReview.vue')
    const w = mount(SessionReview, { props: { sessionId: 1, rev: 0 }, global: { plugins: [router] } })
    await flushPromises()
    expect(w.text()).toContain('需要你确认 1 项')
    expect(w.text()).toContain('桌面杂物')
    expect(w.text()).toContain('未分类')
    expect(w.text()).toContain('无法从名字判断用途')
    await w
      .findAll('button')
      .find((b) => b.text() === '确认')!
      .trigger('click')
    await flushPromises()
    expect(confirmReview).toHaveBeenCalledWith(1, [4])
    expect(w.emitted('changed')).toHaveLength(1)
    w.unmount()
  })
})
