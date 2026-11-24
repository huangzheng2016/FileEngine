// One reconnect state machine for the session event stream. The transports (SSE in a browser, a Wails
// subscription on the desktop) only open one connection at a time; this loop owns everything else: resume
// from the last delivered seq, drop duplicates, back off, and never give up.
import { ApiError, notifyUnauthorized } from '@/api/http'
import type { Id, SessionEvent } from '@/api/types'
import { errorMessage } from '@/base/errors'

export interface StreamState {
  connected: boolean
  // set after the second failed attempt in a row, so a single blip stays silent; cleared on connect
  error: string
}

export interface ConnectArgs {
  sessionId: Id
  runId?: Id
  // the last delivered seq; null = start at the latest run's first event
  after: number | null
  signal: AbortSignal
  // the connection is established (headers received, subscription accepted)
  onOpen: () => void
  onEvent: (ev: SessionEvent) => void
}

// Resolves or rejects when the connection ended; an end the caller did not ask for is reconnected.
export type Connect = (args: ConnectArgs) => Promise<void>

export interface FollowOptions {
  sessionId: Id
  runId?: Id
  after?: number | null
  connect: Connect
  onEvent: (ev: SessionEvent) => void
  onState?: (s: StreamState) => void
  // injectable for tests
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>
  random?: () => number
  // false disables the online / visibility listeners (tests install their own)
  listen?: boolean
}

export interface FollowHandle {
  stop: () => void
  // retry now: cuts a backoff sleep, and drops a live connection that may be half-open
  reconnect: () => void
  lastSeq: () => number | null
}

export const BACKOFF_BASE_MS = 500
export const BACKOFF_MAX_MS = 8000

export function backoffMs(failures: number, random = Math.random): number {
  const base = Math.min(BACKOFF_MAX_MS, BACKOFF_BASE_MS * 2 ** Math.max(0, failures - 1))
  return base * (0.8 + random() * 0.4)
}

function sleepFor(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve()
    const t = setTimeout(done, ms)
    function done() {
      clearTimeout(t)
      signal.removeEventListener('abort', done)
      resolve()
    }
    signal.addEventListener('abort', done, { once: true })
  })
}

export function followEvents(opts: FollowOptions): FollowHandle {
  const sleep = opts.sleep ?? sleepFor
  const random = opts.random ?? Math.random
  const life = new AbortController()
  let last: number | null = opts.after ?? null
  let conn: AbortController | null = null
  let wake: AbortController | null = null
  let failures = 0
  let state: StreamState = { connected: false, error: '' }

  function setState(next: StreamState) {
    if (next.connected === state.connected && next.error === state.error) return
    state = next
    opts.onState?.(next)
  }

  async function loop() {
    while (!life.signal.aborted) {
      const c = (conn = new AbortController())
      const stopConn = () => c.abort()
      life.signal.addEventListener('abort', stopConn, { once: true })
      let failure: unknown
      try {
        await opts.connect({
          sessionId: opts.sessionId,
          runId: opts.runId,
          after: last,
          signal: c.signal,
          onOpen: () => {
            failures = 0
            setState({ connected: true, error: '' })
          },
          onEvent: (ev) => {
            // a replay overlapping what was delivered, or a second path to the same event
            if (last !== null && ev.seq <= last) return
            last = ev.seq
            failures = 0
            opts.onEvent(ev)
          },
        })
        failure = new Error('连接中断')
      } catch (e) {
        failure = e
      } finally {
        life.signal.removeEventListener('abort', stopConn)
      }
      if (life.signal.aborted) return
      if (c.signal.aborted) {
        // a requested reconnect: go again at once, without counting a failure
        failures = 0
        setState({ connected: false, error: '' })
        continue
      }
      if (failure instanceof ApiError && failure.status === 401) {
        notifyUnauthorized()
        setState({ connected: false, error: errorMessage(failure) })
        return
      }
      failures++
      setState({ connected: false, error: failures >= 2 ? errorMessage(failure) : '' })
      const w = (wake = new AbortController())
      const stopWake = () => w.abort()
      life.signal.addEventListener('abort', stopWake, { once: true })
      await sleep(backoffMs(failures, random), w.signal)
      life.signal.removeEventListener('abort', stopWake)
      wake = null
    }
  }

  function reconnect() {
    failures = 0
    if (wake) wake.abort()
    else conn?.abort()
  }

  const onOnline = () => reconnect()
  const onVisible = () => {
    // a tab coming back only needs to cut a backoff; a live connection has its own idle watchdog
    if (document.visibilityState === 'visible' && wake) wake.abort()
  }
  const listen = opts.listen !== false && typeof window !== 'undefined'
  if (listen) {
    window.addEventListener('online', onOnline)
    document.addEventListener('visibilitychange', onVisible)
  }

  void loop()

  return {
    stop() {
      life.abort()
      if (listen) {
        window.removeEventListener('online', onOnline)
        document.removeEventListener('visibilitychange', onVisible)
      }
    },
    reconnect,
    lastSeq: () => last,
  }
}
