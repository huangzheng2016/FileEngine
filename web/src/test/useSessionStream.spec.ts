import { effectScope, nextTick, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { FollowHandle, StreamState } from '@/lib/followEvents'
import type { SessionEvent } from '@/api/types'
import { useSessionStream } from '@/lib/useSessionStream'
import { event } from './fixtures'

// A manual frame scheduler: events are applied when the test flushes a frame.
function harness(sessionId = '7') {
  const frames: (() => void)[] = []
  const scheduler = (cb: () => void) => {
    frames.push(cb)
    return () => {
      const i = frames.indexOf(cb)
      if (i >= 0) frames.splice(i, 1)
    }
  }
  const handles: {
    id: string
    push: (e: SessionEvent) => void
    state: (s: StreamState) => void
    stop: ReturnType<typeof vi.fn>
    reconnect: ReturnType<typeof vi.fn>
  }[] = []
  const follow = (id: string, onEvent: (e: SessionEvent) => void, o: { onState?: (s: StreamState) => void } = {}) => {
    const h = { id, push: onEvent, state: o.onState ?? (() => {}), stop: vi.fn(), reconnect: vi.fn() }
    handles.push(h)
    return h as unknown as FollowHandle
  }
  const id = ref(sessionId)
  const hooks = { onRun: vi.fn(), onPlan: vi.fn(), onSession: vi.fn() }
  const scope = effectScope()
  const stream = scope.run(() => useSessionStream(id, hooks, { scheduler, follow }))!
  const frame = () => frames.splice(0).forEach((cb) => cb())
  return { id, hooks, scope, stream, handles, frame }
}

const run = (seq: number, kind = 'run_status') => event(kind, { run_id: 1, phase: 'tagging', status: 'running' }, seq)

describe('useSessionStream', () => {
  it('fires each hook once per flushed batch, not once per replayed event', () => {
    const t = harness()
    for (let i = 1; i <= 200; i++) t.handles[0].push(run(i))
    t.handles[0].push(event('plan_updated', {}, 201))
    t.handles[0].push(event('plan_updated', {}, 202))
    t.handles[0].push(event('session_updated', {}, 203))
    t.frame()
    expect(t.hooks.onRun).toHaveBeenCalledTimes(1)
    expect(t.hooks.onPlan).toHaveBeenCalledTimes(1)
    expect(t.hooks.onSession).toHaveBeenCalledTimes(1)
    expect(t.stream.timeline.value.seq).toBe(203)
  })

  it('does not fire hooks for events the timeline already has', () => {
    const t = harness()
    t.handles[0].push(run(1))
    t.frame()
    t.handles[0].push(run(1))
    t.handles[0].push(run(1, 'run_done'))
    t.frame()
    expect(t.hooks.onRun).toHaveBeenCalledTimes(1)
  })

  it('keeps the timeline across a reconnect and only resets for another session', async () => {
    const t = harness()
    t.handles[0].push(run(5))
    t.frame()
    t.stream.reconnect()
    expect(t.handles[0].reconnect).toHaveBeenCalledTimes(1)
    expect(t.handles).toHaveLength(1)
    expect(t.stream.timeline.value.seq).toBe(5)

    t.id.value = '8'
    await nextTick()
    expect(t.handles[0].stop).toHaveBeenCalled()
    expect(t.handles).toHaveLength(2)
    expect(t.handles[1].id).toBe('8')
    expect(t.stream.timeline.value.seq).toBe(0)
  })

  it('exposes the connection state of the follower', () => {
    const t = harness()
    t.handles[0].state({ connected: true, error: '' })
    expect(t.stream.connected.value).toBe(true)
    t.handles[0].state({ connected: false, error: '网络连接失败' })
    expect(t.stream.connected.value).toBe(false)
    expect(t.stream.error.value).toBe('网络连接失败')
  })

  it('stops following when its scope ends', () => {
    const t = harness()
    t.scope.stop()
    expect(t.handles[0].stop).toHaveBeenCalled()
  })
})
