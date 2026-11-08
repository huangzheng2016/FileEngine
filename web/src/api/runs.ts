import type { SseEvent } from '@/base/sse'
import { followStream, http } from './http'
import type { AgentRun, ChatTurn, SessionEvent, SessionPlan } from './types'

export async function listRuns(sessionId: number, signal?: AbortSignal) {
  const { data } = await http.get<{ items: AgentRun[] }>(`/sessions/${sessionId}/runs`, { signal })
  return data.items ?? []
}

export async function stopRun(runId: number) {
  await http.post(`/runs/${runId}/stop`)
}

export async function listEvents(
  sessionId: number,
  p: { run_id?: number; after?: number; limit?: number },
  signal?: AbortSignal,
) {
  const { data } = await http.get<{ items: SessionEvent[]; has_more: boolean }>(`/sessions/${sessionId}/events`, {
    params: p,
    signal,
  })
  return { items: data.items ?? [], hasMore: data.has_more }
}

// Every page of one run's persisted events, oldest first.
export async function loadRunEvents(sessionId: number, runId: number, signal?: AbortSignal) {
  const all: SessionEvent[] = []
  let after = 0
  for (;;) {
    const page = await listEvents(sessionId, { run_id: runId, after, limit: 1000 }, signal)
    all.push(...page.items)
    if (!page.hasMore || page.items.length === 0) return all
    after = page.items[page.items.length - 1].seq
  }
}

export function parseEvent(e: SseEvent): SessionEvent {
  let data: Record<string, unknown> = {}
  try {
    data = JSON.parse(e.data) as Record<string, unknown>
  } catch {
    data = {}
  }
  return { seq: Number(e.id) || 0, type: e.event, data }
}

// Live session stream: replays the latest run from its first event, then follows.
export function followSession(sessionId: number, onEvent: (e: SessionEvent) => void, signal: AbortSignal) {
  return followStream(`/sessions/${sessionId}/events/stream`, (e) => onEvent(parseEvent(e)), signal)
}

// ---------- plan ----------

export async function getPlan(sessionId: number, signal?: AbortSignal) {
  const { data } = await http.get<SessionPlan>(`/sessions/${sessionId}/plan`, { signal })
  return data
}

export async function draftPlan(sessionId: number, instructions: string) {
  const { data } = await http.post<{ run_id: number }>(`/sessions/${sessionId}/plan/draft`, { instructions })
  return data.run_id
}

export async function savePlan(sessionId: number, content: string, version: number) {
  const { data } = await http.put<SessionPlan>(`/sessions/${sessionId}/plan`, { content, version })
  return data
}

export async function approvePlan(sessionId: number) {
  const { data } = await http.post<SessionPlan>(`/sessions/${sessionId}/plan/approve`)
  return data
}

// ---------- assistant ----------

export async function listChat(sessionId: number, signal?: AbortSignal) {
  const { data } = await http.get<{ items: ChatTurn[] }>(`/sessions/${sessionId}/chat`, { signal })
  return data.items ?? []
}

export async function sendChat(sessionId: number, content: string, fileIds: number[]) {
  const { data } = await http.post<{ run_id: number }>(`/sessions/${sessionId}/chat`, {
    content,
    file_ids: fileIds.length ? fileIds : undefined,
  })
  return data.run_id
}
