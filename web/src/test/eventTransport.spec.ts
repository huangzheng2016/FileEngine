import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/http'
import type { ConnectArgs } from '@/lib/followEvents'
import type { SessionEvent } from '@/api/types'

const wails = vi.hoisted(() => {
  const handlers = new Map<string, ((m: { data: unknown }) => void)[]>()
  return {
    handlers,
    Events: {
      On: vi.fn((topic: string, cb: (m: { data: unknown }) => void) => {
        handlers.set(topic, [...(handlers.get(topic) ?? []), cb])
        return () =>
          handlers.set(
            topic,
            (handlers.get(topic) ?? []).filter((x) => x !== cb),
          )
      }),
    },
    emit(topic: string, data: unknown) {
      for (const cb of handlers.get(topic) ?? []) cb({ data })
    },
  }
})
const service = vi.hoisted(() => ({ Subscribe: vi.fn(), Unsubscribe: vi.fn() }))
vi.mock('@wailsio/runtime', () => ({ Events: wails.Events }))
vi.mock('@bindings/FileEngine/cmd/fileengine-desktop/service', () => service)

import { connectDesktop, connectWeb, connectEvents, parseEventData } from '@/api/eventTransport'

function args(o: Partial<ConnectArgs> = {}) {
  const ctrl = new AbortController()
  const events: SessionEvent[] = []
  const a: ConnectArgs = {
    sessionId: '7',
    after: null,
    signal: ctrl.signal,
    onOpen: vi.fn(),
    onEvent: (e) => events.push(e),
    ...o,
  }
  return { a, ctrl, events }
}

const enc = new TextEncoder()
function sseResponse(chunks: string[], hold = false) {
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      for (const chunk of chunks) c.enqueue(enc.encode(chunk))
      if (!hold) c.close()
    },
  })
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  vi.clearAllMocks()
  wails.handlers.clear()
  delete window.__FILEENGINE_TRANSPORT__
})

describe('web event stream', () => {
  it('reads frames into events, taking the run id from the payload, and ignores pings', async () => {
    const fetchMock = vi.fn(async () =>
      sseResponse([
        ': ping\n\n',
        'id: 3\nevent: text\ndata: {"run_id":12,"delta":"你"}\n\n',
        'id: 4\nevent: ru',
        'n_done\ndata: {"run_id":12}\n\n',
      ]),
    )
    vi.stubGlobal('fetch', fetchMock)
    const { a, events } = args()
    await connectWeb(a)
    expect(a.onOpen).toHaveBeenCalledTimes(1)
    expect(events).toEqual([
      { seq: 3, run_id: '12', kind: 'text', data: { run_id: 12, delta: '你' }, created_at: 0 },
      { seq: 4, run_id: '12', kind: 'run_done', data: { run_id: 12 }, created_at: 0 },
    ])
  })

  it('asks for the latest run without `after`, and resumes with it once there is a seq', async () => {
    const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () => sseResponse([]))
    vi.stubGlobal('fetch', fetchMock)
    await connectWeb(args().a)
    await connectWeb(args({ after: 41, runId: '5' }).a)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/event/stream?session_id=7')
    expect(new URLSearchParams(fetchMock.mock.calls[1][0].split('?')[1]).toString()).toBe(
      'session_id=7&run_id=5&after=41',
    )
  })

  it('throws the envelope of a failed connect as an ApiError', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ code: 404, msg: '任务不存在' }), { status: 404 })),
    )
    const { a } = args()
    await expect(connectWeb(a)).rejects.toMatchObject({ status: 404, message: '任务不存在' })
    await expect(connectWeb(a)).rejects.toBeInstanceOf(ApiError)
    expect(a.onOpen).not.toHaveBeenCalled()
  })

  it('treats a connection that goes silent as dead', async () => {
    vi.useFakeTimers()
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => sseResponse(['id: 1\nevent: text\ndata: {}\n\n'], true)),
    )
    const { a, events } = args()
    const p = connectWeb(a, 1000).catch((e: unknown) => e)
    await vi.advanceTimersByTimeAsync(500)
    expect(events).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1200)
    expect(await p).toMatchObject({ message: '连接中断' })
  })

  it('parses payloads that are not objects as empty data', () => {
    expect(parseEventData('not json')).toEqual({})
    expect(parseEventData('[1]')).toEqual({})
    expect(parseEventData('')).toEqual({})
  })
})

describe('desktop event stream', () => {
  it('listens on the topic before subscribing, then delivers parsed events', async () => {
    service.Subscribe.mockImplementation(async () => {
      expect(wails.handlers.get('session://events/7')).toHaveLength(1)
      return { subscription_id: 'sub-1', topic: 'session://events/7' }
    })
    const { a, ctrl, events } = args({ after: 10 })
    const p = connectDesktop(a)
    await vi.waitFor(() => expect(a.onOpen).toHaveBeenCalled())
    expect(service.Subscribe).toHaveBeenCalledWith({ session_id: '7', run_id: undefined, after: 10 })
    wails.emit('session://events/7', { seq: 11, run_id: '3', kind: 'text', data: '{"delta":"a"}', created_at: 99 })
    expect(events).toEqual([{ seq: 11, run_id: '3', kind: 'text', data: { delta: 'a' }, created_at: 99 }])
    ctrl.abort()
    await p
    expect(service.Unsubscribe).toHaveBeenCalledWith('sub-1')
    expect(wails.handlers.get('session://events/7')).toEqual([])
  })

  it('ends with the error of the end topic, ignoring another subscription', async () => {
    service.Subscribe.mockResolvedValue({ subscription_id: 'sub-2', topic: 't' })
    const { a } = args()
    const p = connectDesktop(a).catch((e: unknown) => e)
    await vi.waitFor(() => expect(a.onOpen).toHaveBeenCalled())
    wails.emit('session://events/7/end', { subscription_id: 'sub-9', error: '别的订阅' })
    wails.emit('session://events/7/end', { subscription_id: 'sub-2', error: '数据库已关闭' })
    expect(await p).toMatchObject({ message: '数据库已关闭' })
  })

  it('keeps an end that arrives before Subscribe resolves', async () => {
    let resolve!: (v: { subscription_id: string; topic: string }) => void
    service.Subscribe.mockReturnValue(new Promise((r) => (resolve = r)))
    const { a } = args()
    const p = connectDesktop(a).catch((e: unknown) => e)
    await vi.waitFor(() => expect(service.Subscribe).toHaveBeenCalled())
    wails.emit('session://events/7/end', { subscription_id: 'sub-3', error: '提前结束' })
    resolve({ subscription_id: 'sub-3', topic: 't' })
    expect(await p).toMatchObject({ message: '提前结束' })
  })

  it('unsubscribes when aborted while Subscribe is still pending', async () => {
    let resolve!: (v: { subscription_id: string; topic: string }) => void
    service.Subscribe.mockReturnValue(new Promise((r) => (resolve = r)))
    const { a, ctrl } = args()
    const p = connectDesktop(a)
    await vi.waitFor(() => expect(service.Subscribe).toHaveBeenCalled())
    ctrl.abort()
    await p
    resolve({ subscription_id: 'sub-4', topic: 't' })
    await vi.waitFor(() => expect(service.Unsubscribe).toHaveBeenCalledWith('sub-4'))
    expect(a.onOpen).not.toHaveBeenCalled()
  })

  it('picks the transport by environment', async () => {
    window.__FILEENGINE_TRANSPORT__ = 'desktop'
    service.Subscribe.mockResolvedValue({ subscription_id: 'sub-5', topic: 't' })
    const { a, ctrl } = args()
    const p = connectEvents(a)
    await vi.waitFor(() => expect(service.Subscribe).toHaveBeenCalled())
    ctrl.abort()
    await p
  })
})

beforeEach(() => {
  wails.handlers.clear()
})
