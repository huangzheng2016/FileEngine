import type { SessionEvent } from '@/api/types'

export type Scheduler = (cb: () => void) => () => void

export const frameScheduler: Scheduler = (cb) => {
  const id = requestAnimationFrame(cb)
  return () => cancelAnimationFrame(id)
}

// Text arriving faster than this between two frames is flushed at once, so a burst
// (a long replay) never piles up unbounded work for one frame.
export const MAX_PENDING_CHARS = 64 * 1024

export function createEventBuffer(
  onFlush: (events: SessionEvent[]) => void,
  scheduler: Scheduler = frameScheduler,
  maxPendingChars = MAX_PENDING_CHARS,
) {
  let pending: SessionEvent[] = []
  let chars = 0
  let cancel: (() => void) | null = null

  function flush() {
    cancel?.()
    cancel = null
    if (!pending.length) return
    const batch = pending
    pending = []
    chars = 0
    onFlush(batch)
  }

  function push(ev: SessionEvent) {
    pending.push(ev)
    const delta = ev.data?.delta
    if (typeof delta === 'string') chars += delta.length
    if (chars > maxPendingChars) return flush()
    cancel ??= scheduler(() => {
      cancel = null
      flush()
    })
  }

  function dispose() {
    cancel?.()
    cancel = null
    pending = []
    chars = 0
  }

  return { push, flush, dispose }
}
