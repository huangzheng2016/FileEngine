// Server-Sent Events over fetch (not EventSource): it sends cookies and custom
// headers, reports HTTP errors, and can be aborted. Reconnects resume with
// Last-Event-ID, so the server replays only what was missed.

export interface SseEvent {
  id: string
  event: string
  data: string
}

// Incremental parser: feed it chunks as they arrive, get complete events back.
export function createSseParser() {
  let buffer = ''
  let event = { id: '', event: 'message', data: [] as string[] }

  return function feed(chunk: string): SseEvent[] {
    buffer += chunk
    const out: SseEvent[] = []
    let nl: number
    while ((nl = buffer.search(/\r\n|\r|\n/)) >= 0) {
      const line = buffer.slice(0, nl)
      buffer = buffer.slice(nl + (buffer.startsWith('\r\n', nl) ? 2 : 1))
      if (line === '') {
        if (event.data.length) out.push({ id: event.id, event: event.event, data: event.data.join('\n') })
        event = { id: event.id, event: 'message', data: [] }
        continue
      }
      if (line.startsWith(':')) continue
      const colon = line.indexOf(':')
      const field = colon < 0 ? line : line.slice(0, colon)
      const value = colon < 0 ? '' : line.slice(colon + 1).replace(/^ /, '')
      if (field === 'data') event.data.push(value)
      else if (field === 'event') event.event = value
      else if (field === 'id') event.id = value
    }
    return out
  }
}

export interface StreamOptions {
  onEvent: (e: SseEvent) => void
  // stop reading when this returns true for an event (e.g. 'done')
  isFinal?: (e: SseEvent) => boolean
  // called before each reconnect; return false to stop (e.g. 401 that refresh cannot fix)
  onError?: (err: unknown, attempt: number) => boolean | Promise<boolean>
  signal?: AbortSignal
  maxRetries?: number
}

export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly body: { code?: number; msg?: string },
    readonly refreshNeeded: boolean,
  ) {
    super(body.msg || `HTTP ${status}`)
  }
}

// Reads the stream until a final event, abort, or too many failed reconnects.
export async function readEventStream(url: string, opts: StreamOptions): Promise<void> {
  let lastId = ''
  for (let attempt = 0; ; attempt++) {
    try {
      const res = await fetch(url, {
        credentials: 'same-origin',
        headers: { Accept: 'text/event-stream', ...(lastId ? { 'Last-Event-ID': lastId } : {}) },
        signal: opts.signal,
      })
      if (!res.ok || !res.body) {
        const body = await res.json().catch(() => ({}))
        throw new HttpError(res.status, body, res.headers.get('x-token-refresh-needed') === 'true')
      }
      const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
      const feed = createSseParser()
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        for (const e of feed(value)) {
          if (e.id) lastId = e.id
          attempt = 0
          if (e.event === 'ping') continue
          opts.onEvent(e)
          if (opts.isFinal?.(e)) {
            await reader.cancel()
            return
          }
        }
      }
      throw new Error('连接中断')
    } catch (err) {
      if (opts.signal?.aborted) return
      if (attempt >= (opts.maxRetries ?? 5)) throw err
      if (opts.onError && !(await opts.onError(err, attempt))) throw err
      // 0.5s, 1s, 2s, 4s ... with jitter, so many clients do not reconnect in lockstep
      await sleep(Math.min(8000, 500 * 2 ** attempt) * (0.8 + Math.random() * 0.4), opts.signal)
    }
  }
}

function sleep(ms: number, signal?: AbortSignal) {
  return new Promise<void>((resolve) => {
    const t = setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => (clearTimeout(t), resolve()), { once: true })
  })
}
