import { download, get, list, post } from './http'
import type { ChatTurn, Id, PlanOp, Run, SessionEvent, SessionPlan } from './types'
import { followEvents, type FollowHandle, type FollowOptions } from '@/lib/followEvents'
import { connectEvents, parseEventData } from './eventTransport'
import type * as idl from './idl.gen'

export async function listRuns(sessionId: Id, signal?: AbortSignal) {
  const r = await get<{ items: Run[] | null }>('/api/run/list', { session_id: sessionId }, signal)
  return list(r.items)
}

export async function getRun(runId: Id, signal?: AbortSignal) {
  const r = await get<{ item: Run }>('/api/run/get', { run_id: runId }, signal)
  return r.item
}

export async function stopRun(runId: Id) {
  await post<void>('/api/run/stop', { run_id: runId })
}

export function toSessionEvent(e: idl.SessionEvent): SessionEvent {
  return { seq: e.seq, run_id: e.run_id, kind: e.kind, data: parseEventData(e.data), created_at: e.created_at }
}

export async function listEvents(
  sessionId: Id,
  p: { run_id?: Id; after?: number; before?: number; limit?: number },
  signal?: AbortSignal,
) {
  const r = await get<{ items: idl.SessionEvent[] | null; has_more: boolean }>(
    '/api/event/list',
    { session_id: sessionId, ...p },
    signal,
  )
  return { items: list(r.items).map(toSessionEvent), hasMore: r.has_more }
}

export async function loadRunEvents(sessionId: Id, runId: Id, signal?: AbortSignal) {
  const all: SessionEvent[] = []
  let after = 0
  for (;;) {
    const page = await listEvents(sessionId, { run_id: runId, after, limit: 1000 }, signal)
    all.push(...page.items)
    if (!page.hasMore || page.items.length === 0) return all
    after = page.items[page.items.length - 1].seq
  }
}

// Live session stream: replays the latest run from its first event, then follows. It reconnects by itself and
// resumes from the last seq it delivered; see lib/followEvents.ts.
export function followSession(
  sessionId: Id,
  onEvent: (e: SessionEvent) => void,
  opts: Pick<FollowOptions, 'onState' | 'after' | 'runId'> = {},
): FollowHandle {
  return followEvents({ ...opts, sessionId, onEvent, connect: connectEvents })
}

export async function getPlan(sessionId: Id, signal?: AbortSignal) {
  const r = await get<{ item: SessionPlan }>('/api/plan/get', { session_id: sessionId }, signal)
  return r.item
}

export async function draftPlan(sessionId: Id, instructions: string) {
  const r = await post<{ run_id: Id }>('/api/plan/draft', { session_id: sessionId, instructions })
  return r.run_id
}

// A stale version answers 409 (isConflict); re-read with getPlan.
export async function savePlan(sessionId: Id, content: string, version: number) {
  const r = await post<{ item: SessionPlan }>('/api/plan/update', { session_id: sessionId, content, version })
  return r.item
}

export async function approvePlan(sessionId: Id) {
  const r = await post<{ item: SessionPlan }>('/api/plan/approve', { session_id: sessionId })
  return r.item
}

export async function listPlanOps(sessionId: Id, signal?: AbortSignal) {
  const r = await get<{ items: PlanOp[] | null }>('/api/plan/ops', { session_id: sessionId }, signal)
  return list(r.items)
}

export function exportPlanOps(sessionId: Id, filename: string) {
  return download('/api/plan/ops_export', { query: { session_id: sessionId }, filename })
}

export async function listChat(sessionId: Id, signal?: AbortSignal) {
  const r = await get<{ items: ChatTurn[] | null }>('/api/chat/list', { session_id: sessionId }, signal)
  return list(r.items)
}

export async function sendChat(sessionId: Id, content: string, fileIds: Id[]) {
  const r = await post<{ run_id: Id }>('/api/chat/send', { session_id: sessionId, content, file_ids: fileIds })
  return r.run_id
}
