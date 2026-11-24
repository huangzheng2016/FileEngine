import { effectScope, nextTick, ref, shallowRef } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { confirmState, resolveConfirm } from '@/base/useConfirm'
import { event, run, session } from './fixtures'

const api = vi.hoisted(() => ({
  getSession: vi.fn(),
  listRuns: vi.fn(),
  getPlan: vi.fn(),
  listFilesystems: vi.fn(),
  revertRun: vi.fn(),
  listEvents: vi.fn(),
  connected: { value: false },
}))

vi.mock('@/api/sessions', () => ({ getSession: api.getSession, revertRun: api.revertRun }))
vi.mock('@/api/runs', () => ({ listRuns: api.listRuns, getPlan: api.getPlan, listEvents: api.listEvents }))
vi.mock('@/api/settings', () => ({ listFilesystems: api.listFilesystems }))
vi.mock('@/base/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/base/usePageTitle', () => ({ usePageTitle: vi.fn() }))
vi.mock('@/lib/useSessionStream', () => ({
  useSessionStream: () => ({
    timeline: shallowRef({}),
    error: shallowRef(''),
    connected: shallowRef(api.connected.value),
    reconnect: vi.fn(),
  }),
}))

import { loadRunProgress } from '@/lib/runProgress'
import { canRevert, revertMessage, useRevert } from '@/lib/useRevert'
import { useSessionDetail } from '@/lib/useSessionDetail'

beforeEach(() => {
  vi.clearAllMocks()
  api.connected.value = false
  api.getSession.mockResolvedValue(session({ planned_ops: 3, executed_ops: 0, tagged_files: 3 }))
  api.listRuns.mockResolvedValue([])
  api.getPlan.mockResolvedValue({ status: 'approved', content: '', version: 1 })
  api.listFilesystems.mockResolvedValue([])
})
afterEach(() => {
  vi.useRealTimers()
  resolveConfirm(false)
})

describe('useRevert', () => {
  it('confirms a copy revert as dangerous and says that the copies are deleted', async () => {
    api.revertRun.mockResolvedValue('9')
    const started = vi.fn()
    const { revert } = useRevert(started)
    const p = revert({ id: '4', exec_mode: 'copy' }, 12)
    expect(confirmState.danger).toBe(true)
    expect(confirmState.message).toContain('删除')
    expect(confirmState.message).toContain('12')
    resolveConfirm(true)
    await p
    expect(api.revertRun).toHaveBeenCalledWith('4')
    expect(started).toHaveBeenCalledWith('9')
  })

  it('does not call the API when the confirm is declined, and a move revert is not dangerous', async () => {
    const { revert } = useRevert()
    const p = revert({ id: '4', exec_mode: 'move' })
    expect(confirmState.danger).toBe(false)
    expect(confirmState.message).toContain('放回原处')
    resolveConfirm(false)
    await p
    expect(api.revertRun).not.toHaveBeenCalled()
  })

  it('reverts only finished executions that nothing has reverted yet', () => {
    const base = run({ phase: 'executing', status: 'done' })
    expect(canRevert(base)).toBe(true)
    expect(canRevert({ ...base, status: 'partial' })).toBe(true)
    expect(canRevert({ ...base, status: 'running' })).toBe(false)
    expect(canRevert({ ...base, reverted_by_run_id: '8' })).toBe(false)
    expect(canRevert(run({ phase: 'tagging', status: 'done' }))).toBe(false)
    expect(revertMessage('copy')).toContain('文件')
  })
})

describe('loadRunProgress', () => {
  it('reads the final counters from the newest events in one request', async () => {
    api.listEvents.mockResolvedValue({
      items: [
        event('progress', { total: 10, success: 8, failed: 1, skipped: 1, tagged: 0, planned: 0 }, 5),
        event('run_done', { status: 'partial' }, 6),
      ].map((e) => ({ ...e, run_id: '4' })),
      hasMore: false,
    })
    const p = await loadRunProgress('7', '4')
    expect(api.listEvents).toHaveBeenCalledTimes(1)
    expect(api.listEvents.mock.calls[0][1]).toMatchObject({ run_id: '4', limit: 100 })
    expect(p).toMatchObject({ success: 8, failed: 1, skipped: 1 })
  })
})

describe('useSessionDetail', () => {
  async function mount() {
    const scope = effectScope()
    const d = scope.run(() => useSessionDetail(ref('7')))!
    await vi.waitFor(() => expect(d.session.value).toBeTruthy())
    return { scope, d }
  }

  it('derives the next action and the busy reason from session and runs', async () => {
    api.listRuns.mockResolvedValue([run({ id: '2', phase: 'tagging', status: 'running' })])
    const { scope, d } = await mount()
    await vi.waitFor(() => expect(d.running.value?.id).toBe('2'))
    expect(d.busyReason.value).toBe('正在运行，结束后可用')
    expect(d.next.value.kind).toBe('none')
    scope.stop()
  })

  it('offers 执行 once planned and idle', async () => {
    const { scope, d } = await mount()
    expect(d.next.value.kind).toBe('execute')
    expect(d.stage.value).toBe('planned')
    scope.stop()
  })

  it('polls only while the stream is down', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const down = await mount()
    const before = api.getSession.mock.calls.length
    await vi.advanceTimersByTimeAsync(3500)
    expect(api.getSession.mock.calls.length).toBeGreaterThan(before)
    down.scope.stop()

    api.connected.value = true
    const up = await mount()
    const n = api.getSession.mock.calls.length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.getSession.mock.calls.length).toBe(n)
    up.scope.stop()
    await nextTick()
  })
})
