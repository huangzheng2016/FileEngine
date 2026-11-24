// The two ways to hold one event-stream connection: SSE over fetch in a browser, a Wails subscription in the
// desktop window. Both deliver the same SessionEvent; lib/followEvents.ts owns reconnecting.
import { createSseParser } from '@/base/sse'
import type { ConnectArgs } from '@/lib/followEvents'
import { transport } from './env'
import { ApiError, buildQuery } from './http'
import type { Id } from './types'

// The server pings every 20 s; a connection silent for three pings is dead (a sleeping laptop leaves
// half-open sockets that never error).
export const IDLE_MS = 60_000

export function connectEvents(args: ConnectArgs): Promise<void> {
  return transport() === 'desktop' ? connectDesktop(args) : connectWeb(args)
}

export function parseEventData(text: string): Record<string, unknown> {
  try {
    const v: unknown = JSON.parse(text)
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
  } catch {
    return {}
  }
}

function runIdOf(data: Record<string, unknown>): Id {
  const v = data.run_id
  return typeof v === 'number' || typeof v === 'string' ? String(v) : '0'
}

export async function connectWeb(args: ConnectArgs, idleMs = IDLE_MS): Promise<void> {
  const query = buildQuery({ session_id: args.sessionId, run_id: args.runId, after: args.after })
  const res = await fetch(`/api/event/stream?${new URLSearchParams(query)}`, {
    credentials: 'same-origin',
    headers: { Accept: 'text/event-stream' },
    signal: args.signal,
  })
  if (!res.ok || !res.body) {
    const env = (await res.json().catch(() => ({}))) as { code?: number; msg?: string }
    throw new ApiError(res.status, env.code ?? 0, env.msg || `请求失败（${res.status}）`)
  }
  args.onOpen()
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
  const feed = createSseParser()
  let idle: ReturnType<typeof setTimeout> | undefined
  let arm = () => {}
  const dead = new Promise<never>((_, reject) => {
    arm = () => {
      clearTimeout(idle)
      idle = setTimeout(() => reject(new Error('连接中断')), idleMs)
    }
  })
  arm()
  try {
    for (;;) {
      const { value, done } = await Promise.race([reader.read(), dead])
      if (done) return
      arm()
      for (const e of feed(value)) {
        const seq = Number(e.id)
        if (!Number.isFinite(seq) || seq <= 0) continue
        const data = parseEventData(e.data)
        args.onEvent({ seq, run_id: runIdOf(data), kind: e.event, data, created_at: 0 })
      }
    }
  } finally {
    clearTimeout(idle)
    void reader.cancel().catch(() => {})
  }
}

interface WirePayload {
  seq: number
  run_id: string
  kind: string
  data: string
  created_at: number
}

interface EndPayload {
  subscription_id: string
  error?: string
}

export async function connectDesktop(args: ConnectArgs): Promise<void> {
  const [{ Events }, { Subscribe, Unsubscribe }] = await Promise.all([
    import('@wailsio/runtime'),
    import('@bindings/FileEngine/cmd/fileengine-desktop/service'),
  ])
  // the topic is per session, so listen first: nothing the backend emits can be missed
  const topic = `session://events/${args.sessionId}`
  return new Promise<void>((resolve, reject) => {
    let subscription = ''
    const ends: EndPayload[] = []
    const offs: (() => void)[] = []
    let settled = false

    function finish(err?: unknown) {
      if (settled) return
      settled = true
      for (const off of offs) off()
      args.signal.removeEventListener('abort', onAbort)
      if (subscription) void Promise.resolve(Unsubscribe(subscription)).catch(() => {})
      if (err) reject(err instanceof Error ? err : new Error(String(err)))
      else resolve()
    }
    function onAbort() {
      finish()
    }
    function checkEnds() {
      const mine = ends.find((p) => p.subscription_id === subscription)
      if (mine) finish(mine.error ? new Error(mine.error) : undefined)
    }

    offs.push(
      Events.On(topic, (m) => {
        const p = m.data as WirePayload
        const data = parseEventData(p.data)
        args.onEvent({ seq: p.seq, run_id: p.run_id, kind: p.kind, data, created_at: p.created_at })
      }),
      Events.On(`${topic}/end`, (m) => {
        ends.push(m.data as EndPayload)
        if (subscription) checkEnds()
      }),
    )
    args.signal.addEventListener('abort', onAbort, { once: true })
    if (args.signal.aborted) return finish()

    Subscribe({ session_id: args.sessionId, run_id: args.runId, after: args.after }).then((res) => {
      subscription = res.subscription_id
      if (settled) {
        void Promise.resolve(Unsubscribe(subscription)).catch(() => {})
        return
      }
      args.onOpen()
      checkEnds()
    }, finish)
  })
}
