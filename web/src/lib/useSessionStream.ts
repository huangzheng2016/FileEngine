import { onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { followSession, loadRunEvents } from '@/api/runs'
import type { Id, SessionEvent } from '@/api/types'
import { createEventBuffer, type Scheduler } from './eventBuffer'
import type { FollowHandle, StreamState } from './followEvents'
import { applyEvents, emptyTimeline, type Timeline } from './transcript'

export interface StreamHooks {
  // a run started, ended or reported progress: refetch runs / session counters
  onRun?: () => void
  onPlan?: () => void
  // the session's stored counters changed
  onSession?: () => void
}

const RUN_EVENTS = new Set(['run_status', 'run_done'])

export interface SessionStreamOptions {
  scheduler?: Scheduler
  // test seam: replaces the transport-bound follower
  follow?: typeof followSession
}

// Follows a session's live event stream (replaying the current run first) and folds it into a timeline.
// The follower reconnects by itself and resumes from the last delivered seq, so a reconnect never resets the
// timeline; only a different session does. Each hook fires at most once per flushed batch, so replaying a
// long history refetches once, not once per event.
export function useSessionStream(sessionId: Ref<Id>, hooks: StreamHooks = {}, options: SessionStreamOptions = {}) {
  const timeline = shallowRef<Timeline>(emptyTimeline())
  const connected = shallowRef(false)
  const error = shallowRef('')
  let handle: FollowHandle | null = null

  const buffer = createEventBuffer((batch) => {
    const before = timeline.value.seq
    timeline.value = applyEvents(timeline.value, batch)
    let run = false
    let plan = false
    let session = false
    for (const ev of batch) {
      if (ev.seq && ev.seq <= before) continue
      if (RUN_EVENTS.has(ev.kind)) run = true
      else if (ev.kind === 'plan_updated') plan = true
      else if (ev.kind === 'session_updated') session = true
    }
    if (run) hooks.onRun?.()
    if (plan) hooks.onPlan?.()
    if (session) hooks.onSession?.()
  }, options.scheduler)

  function onState(s: StreamState) {
    connected.value = s.connected
    error.value = s.error
  }

  function connect(id: Id) {
    handle?.stop()
    handle = null
    buffer.dispose()
    timeline.value = emptyTimeline()
    connected.value = false
    error.value = ''
    if (!id) return
    handle = (options.follow ?? followSession)(id, (ev: SessionEvent) => buffer.push(ev), { onState })
  }

  watch(sessionId, connect, { immediate: true })
  onScopeDispose(() => {
    handle?.stop()
    buffer.dispose()
  })

  return { timeline, error, connected, reconnect: () => handle?.reconnect() }
}

export async function loadRunTimeline(sessionId: Id, runId: Id, signal?: AbortSignal) {
  return applyEvents(emptyTimeline(), await loadRunEvents(sessionId, runId, signal))
}
