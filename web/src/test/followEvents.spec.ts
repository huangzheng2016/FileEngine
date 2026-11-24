import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, setUnauthorizedHandler } from '@/api/http'
import type { SessionEvent } from '@/api/types'
import {
  BACKOFF_BASE_MS,
  BACKOFF_MAX_MS,
  backoffMs,
  followEvents,
  type Connect,
  type ConnectArgs,
  type StreamState,
} from '@/lib/followEvents'
import { event } from './fixtures'

afterEach(() => {
  setUnauthorizedHandler(() => {})
})

const tick = () => new Promise<void>((r) => setTimeout(r, 0))
async function until(cond: () => boolean) {
  for (let i = 0; i < 200 && !cond(); i++) await tick()
  expect(cond()).toBe(true)
}

// A scripted connect: each attempt gets the next script step, which may emit events and then end or fail.
function scripted(steps: ((a: ConnectArgs) => Promise<void> | void)[]) {
  const calls: (number | null)[] = []
  const connect: Connect = async (a) => {
    calls.push(a.after)
    const step = steps[Math.min(calls.length - 1, steps.length - 1)]
    await step(a)
  }
  return { connect, calls }
}

const noJitter = () => 0.5

function run(connect: Connect, extra: Partial<Parameters<typeof followEvents>[0]> = {}) {
  const got: SessionEvent[] = []
  const states: StreamState[] = []
  const sleeps: number[] = []
  const h = followEvents({
    sessionId: '7',
    connect,
    onEvent: (e) => got.push(e),
    onState: (s) => states.push(s),
    sleep: async (ms) => {
      sleeps.push(ms)
      await tick()
    },
    random: noJitter,
    listen: false,
    ...extra,
  })
  return { h, got, states, sleeps }
}

describe('backoff', () => {
  it('doubles from 0.5 s and is capped at 8 s, with jitter inside +-20 percent', () => {
    expect([1, 2, 3, 4, 5, 6, 50].map((n) => backoffMs(n, () => 0.5))).toEqual([
      500, 1000, 2000, 4000, 8000, 8000, 8000,
    ])
    expect(backoffMs(1, () => 0)).toBe(BACKOFF_BASE_MS * 0.8)
    expect(backoffMs(99, () => 1)).toBeCloseTo(BACKOFF_MAX_MS * 1.2)
  })
})

describe('followEvents', () => {
  it('resumes from the last delivered seq after the connection drops', async () => {
    const { connect, calls } = scripted([
      (a) => {
        a.onOpen()
        a.onEvent(event('text', {}, 1))
        a.onEvent(event('text', {}, 2))
        throw new Error('reset')
      },
      (a) => {
        a.onOpen()
        a.onEvent(event('text', {}, 3))
        return new Promise<void>((_, rej) => a.signal.addEventListener('abort', () => rej(new Error('closed'))))
      },
    ])
    const { h, got } = run(connect)
    await until(() => got.length === 3)
    expect(calls).toEqual([null, 2])
    expect(got.map((e) => e.seq)).toEqual([1, 2, 3])
    h.stop()
  })

  it('drops events at or below what was delivered, even when the replay overlaps', async () => {
    const { connect } = scripted([
      (a) => {
        a.onEvent(event('text', {}, 5))
        throw new Error('x')
      },
      (a) => {
        // a server that replays more than asked for
        for (const s of [4, 5, 5, 6]) a.onEvent(event('text', {}, s))
        return new Promise<void>(() => {})
      },
    ])
    const { h, got } = run(connect)
    await until(() => got.length === 2)
    expect(got.map((e) => e.seq)).toEqual([5, 6])
    expect(h.lastSeq()).toBe(6)
    h.stop()
  })

  it('starts from the given seq when asked to', async () => {
    const { connect, calls } = scripted([() => new Promise<void>(() => {})])
    const { h } = run(connect, { after: 40 })
    await until(() => calls.length === 1)
    expect(calls).toEqual([40])
    h.stop()
  })

  it('never gives up: it keeps retrying with a capped backoff', async () => {
    const { connect, calls } = scripted([
      () => {
        throw new Error('down')
      },
    ])
    const { h, sleeps } = run(connect)
    await until(() => calls.length >= 40)
    h.stop()
    expect(Math.max(...sleeps)).toBe(BACKOFF_MAX_MS)
    expect(sleeps.slice(0, 5)).toEqual([500, 1000, 2000, 4000, 8000])
  })

  it('resets the backoff once a connection delivers something', async () => {
    const { connect, calls } = scripted([
      () => {
        throw new Error('down')
      },
      () => {
        throw new Error('down')
      },
      (a) => {
        a.onOpen()
        a.onEvent(event('text', {}, 1))
        throw new Error('dropped after success')
      },
      () => {
        throw new Error('down again')
      },
    ])
    const { h, sleeps } = run(connect)
    await until(() => calls.length >= 5)
    h.stop()
    // two failures (500, 1000), then success resets, so the next sleeps start at 500 again
    expect(sleeps.slice(0, 4)).toEqual([500, 1000, 500, 1000])
  })

  it('reports an error only after the second failure in a row and clears it on connect', async () => {
    const { connect } = scripted([
      () => {
        throw new ApiError(500, 500, '服务暂时不可用')
      },
      () => {
        throw new ApiError(500, 500, '服务暂时不可用')
      },
      (a) => {
        a.onOpen()
        return new Promise<void>(() => {})
      },
    ])
    const { h, states } = run(connect)
    await until(() => states.at(-1)?.connected === true)
    h.stop()
    // the first failure is silent (no state change from the initial one), the second shows the error
    expect(states).toEqual([
      { connected: false, error: '服务暂时不可用' },
      { connected: true, error: '' },
    ])
  })

  it('stops and signs out on 401', async () => {
    const out = vi.fn()
    setUnauthorizedHandler(out)
    const { connect, calls } = scripted([
      () => {
        throw new ApiError(401, 401, '请先登录')
      },
    ])
    const { h, states } = run(connect)
    await until(() => states.length > 0)
    await tick()
    expect(out).toHaveBeenCalledTimes(1)
    expect(calls).toHaveLength(1)
    h.stop()
  })

  it('reconnect() cuts a backoff sleep and drops a live connection, resuming from the last seq', async () => {
    let connects = 0
    const calls: (number | null)[] = []
    const connect: Connect = (a) => {
      calls.push(a.after)
      connects++
      a.onOpen()
      if (connects === 1) a.onEvent(event('text', {}, 9))
      return new Promise<void>((_, rej) => a.signal.addEventListener('abort', () => rej(new Error('aborted'))))
    }
    const { h, sleeps } = run(connect)
    await until(() => connects === 1)
    h.reconnect()
    await until(() => connects === 2)
    expect(calls).toEqual([null, 9])
    expect(sleeps).toEqual([])
    h.stop()
  })

  it('stop() ends the loop and aborts the open connection', async () => {
    let aborted = false
    const connect: Connect = (a) =>
      new Promise<void>((resolve) =>
        a.signal.addEventListener('abort', () => {
          aborted = true
          resolve()
        }),
      )
    const { h } = run(connect)
    await tick()
    h.stop()
    await until(() => aborted)
  })

  it('reconnects when the browser comes back online', async () => {
    let connects = 0
    const connect: Connect = (a) => {
      connects++
      a.onOpen()
      return new Promise<void>((_, rej) => a.signal.addEventListener('abort', () => rej(new Error('aborted'))))
    }
    const { h } = run(connect, { listen: true })
    await until(() => connects === 1)
    window.dispatchEvent(new Event('online'))
    await until(() => connects === 2)
    h.stop()
    window.dispatchEvent(new Event('online'))
    await tick()
    expect(connects).toBe(2)
  })

  it('cuts a backoff sleep when the tab becomes visible', async () => {
    let connects = 0
    let release: () => void = () => {}
    const connect: Connect = async () => {
      connects++
      throw new Error('down')
    }
    const { h } = run(connect, {
      listen: true,
      sleep: (_ms, signal) =>
        new Promise<void>((resolve) => {
          release = resolve
          signal.addEventListener('abort', () => resolve())
        }),
    })
    await until(() => connects === 1)
    document.dispatchEvent(new Event('visibilitychange'))
    await until(() => connects === 2)
    release()
    h.stop()
  })
})
