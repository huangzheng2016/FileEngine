import { onUnmounted, ref, type Ref } from 'vue'
import { sessionEventsUrl } from '../api'
import type { SessionEventMap, SessionEventType } from '../types'

type Handler<T extends SessionEventType> = (payload: SessionEventMap[T]) => void

/**
 * Thin EventSource wrapper for GET /api/v1/sessions/:id/events/stream.
 *
 * - Typed per-event subscriptions via on(type, handler).
 * - EventSource auto-reconnects; `onOpen` fires on the initial connect AND
 *   after every reconnect. There is no last-event-id replay, so subscribers
 *   should refetch /logs inside onOpen to fill gaps.
 * - The stream only carries new events; history stays in GET /logs.
 *
 * Lifecycle contract: register handlers ONCE per instance (on/onOpen/onError
 * append to a list that open() re-attaches to each fresh EventSource), then
 * call open(id) as often as needed to re-point at a session. Registering the
 * same handler again stacks it — every event then fires N times.
 */
export class SessionEventStream {
  private es: EventSource | null = null
  private openCallbacks: (() => void)[] = []
  private errorCallbacks: (() => void)[] = []
  readonly connected: Ref<boolean> = ref(false)

  on<T extends SessionEventType>(type: T, handler: Handler<T>): this {
    // Registered lazily in open(); keep a list so a stream can be reopened.
    this.pending.push({ type, handler: handler as Handler<SessionEventType> })
    return this
  }

  /** Fires on initial connect and every auto-reconnect — refetch history here. */
  onOpen(callback: () => void): this {
    this.openCallbacks.push(callback)
    return this
  }

  /** Fires when the connection drops (EventSource will retry on its own). */
  onError(callback: () => void): this {
    this.errorCallbacks.push(callback)
    return this
  }

  private pending: { type: SessionEventType; handler: Handler<SessionEventType> }[] = []

  open(sessionId: number): void {
    this.close()
    const es = new EventSource(sessionEventsUrl(sessionId))
    this.es = es
    for (const { type, handler } of this.pending) {
      es.addEventListener(type, (e: MessageEvent) => {
        try {
          handler(JSON.parse(e.data))
        } catch {
          // Ignore malformed frames (e.g. heartbeat comments never reach here).
        }
      })
    }
    es.onopen = () => {
      this.connected.value = true
      for (const cb of this.openCallbacks) cb()
    }
    es.onerror = () => {
      this.connected.value = false
      for (const cb of this.errorCallbacks) cb()
    }
  }

  close(): void {
    if (this.es) {
      this.es.close()
      this.es = null
    }
    this.connected.value = false
  }
}

/** Component-scoped stream: closed automatically on unmount. */
export function useSessionEventStream(): SessionEventStream {
  const stream = new SessionEventStream()
  onUnmounted(() => stream.close())
  return stream
}
