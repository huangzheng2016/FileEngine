import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { applyEvents, emptyTimeline } from '@/lib/transcript'
import { event } from './fixtures'

const api = vi.hoisted(() => ({
  listChat: vi.fn(),
  sendChat: vi.fn(),
  stopRun: vi.fn(),
}))
vi.mock('@/api/runs', () => api)
vi.mock('@/base/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import ChatPanel from '@/components/ChatPanel.vue'

let seq = 0
const ev = (kind: string, data: Record<string, unknown>) => event(kind, data, ++seq)

beforeEach(() => {
  vi.clearAllMocks()
  api.listChat.mockResolvedValue([])
  api.stopRun.mockResolvedValue({})
})

describe('ChatPanel', () => {
  it('offers suggestions on an empty conversation that fill the message box', async () => {
    const w = mount(ChatPanel, { props: { sessionId: '7', timeline: emptyTimeline(), busyReason: '' } })
    await flushPromises()
    expect(w.text()).toContain('告诉助手你想怎么整理')
    await w.find('.chat-suggest-item').trigger('click')
    expect((w.find('textarea').element as HTMLTextAreaElement).value).toBe('截图都放到 /图片/截图')
    w.unmount()
  })

  it('names what a live turn is doing and stops it instead of sending', async () => {
    const timeline = applyEvents(emptyTimeline(), [
      ev('run_status', { run_id: 5, phase: 'chat', status: 'running' }),
      ev('tool_call', { run_id: 5, call_id: 'a', name: 'list_entries' }),
    ])
    const w = mount(ChatPanel, { props: { sessionId: '7', timeline, busyReason: '正在运行，结束后可用' } })
    await flushPromises()
    expect(w.text()).toContain('正在查看文件')
    expect(w.find('button[aria-label="发送"]').exists()).toBe(false)
    await w.find('button[aria-label="停止"]').trigger('click')
    expect(api.stopRun).toHaveBeenCalledWith('5')
    w.unmount()
  })
})
