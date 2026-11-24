import { listEvents } from '@/api/runs'
import type { Id } from '@/api/types'
import { applyEvents, emptyTimeline, type Progress } from './transcript'

// The newest events end with the run's final counters; one request replaces replaying the whole run.
const TAIL = 100

export async function loadRunProgress(sessionId: Id, runId: Id, signal?: AbortSignal): Promise<Progress | undefined> {
  const page = await listEvents(sessionId, { run_id: runId, before: Number.MAX_SAFE_INTEGER, limit: TAIL }, signal)
  return applyEvents(emptyTimeline(), page.items).runs[runId]?.progress
}
