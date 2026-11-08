import { onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { followSession, loadRunEvents } from '@/api/runs'
import type { SessionEvent } from '@/api/types'
import { errorMessage } from '@/base/errors'
import { createEventBuffer } from './eventBuffer'
import { applyEvents, emptyTimeline, type Timeline } from './transcript'

export interface StreamHooks {
  // a run started, ended or reported progress: refetch runs / session counters
  onRun?: (ev: SessionEvent) => void
  onPlan?: () => void
  onMemory?: () => void
}

const RUN_EVENTS = new Set(['run_status', 'run_done'])

// Follows a session's live event stream (replaying the current run first) and folds
// it into a timeline. Reconnects resume from the last seen event.
export function useSessionStream(sessionId: Ref<number>, hooks: StreamHooks = {}) {
  const timeline = shallowRef<Timeline>(emptyTimeline())
  const error = shallowRef('')
  let ctrl: AbortController | null = null

  const buffer = createEventBuffer((batch) => {
    const before = timeline.value.seq
    timeline.value = applyEvents(timeline.value, batch)
    for (const ev of batch) {
      if (ev.seq && ev.seq <= before) continue
      if (RUN_EVENTS.has(ev.type)) hooks.onRun?.(ev)
      else if (ev.type === 'plan_updated') hooks.onPlan?.()
      else if (ev.type === 'memory_updated') hooks.onMemory?.()
    }
  })

  function connect(id: number) {
    ctrl?.abort()
    buffer.dispose()
    timeline.value = emptyTimeline()
    error.value = ''
    if (!id) return
    const c = new AbortController()
    ctrl = c
    followSession(id, buffer.push, c.signal).catch((e) => {
      if (ctrl === c) error.value = errorMessage(e)
    })
  }

  watch(sessionId, connect, { immediate: true })
  onScopeDispose(() => {
    ctrl?.abort()
    buffer.dispose()
  })

  return { timeline, error, reconnect: () => connect(sessionId.value) }
}

export async function loadRunTimeline(sessionId: number, runId: number, signal?: AbortSignal) {
  return applyEvents(emptyTimeline(), await loadRunEvents(sessionId, runId, signal))
}
