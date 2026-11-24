import { get, list, post } from './http'
import type * as idl from './idl.gen'
import type { ExecMode, ExecuteCheck, FilterMode, Id, Issue, LocateItem, Session, SessionStats } from './types'

// Absent fields take the defaults of a new task (see idl/api/session.thrift).
export type SessionInput = Omit<idl.CreateSessionReq, 'filter_mode'> & { filter_mode?: FilterMode }
export type SessionOptions = Partial<Omit<idl.UpdateSessionReq, 'id' | 'filter_mode'>> & { filter_mode?: FilterMode }

export async function listSessions(signal?: AbortSignal, filesystemId?: Id) {
  const r = await get<{ items: Session[] | null }>('/api/session/list', { filesystem_id: filesystemId }, signal)
  return list(r.items)
}

export async function getSession(id: Id, signal?: AbortSignal) {
  const r = await get<{ item: Session }>('/api/session/get', { id }, signal)
  return r.item
}

export async function createSession(body: SessionInput) {
  const r = await post<{ item: Session }>('/api/session/create', body)
  return r.item
}

export async function updateSession(id: Id, body: SessionOptions) {
  const r = await post<{ item: Session }>('/api/session/update', { ...body, id })
  return r.item
}

export async function deleteSession(id: Id) {
  await post<void>('/api/session/delete', { id })
}

export async function rescanSession(id: Id) {
  const r = await post<{ item: Session }>('/api/session/rescan', { id })
  return r.item
}

export function getSessionStats(id: Id, signal?: AbortSignal) {
  return get<SessionStats>('/api/session/stats', { id }, signal)
}

export async function startTagging(sessionId: Id) {
  const r = await post<{ run_id: Id }>('/api/run/tag', { session_id: sessionId })
  return r.run_id
}

// sample: execute only that many planned entries first; 0 = all of them
export async function startExecute(sessionId: Id, mode: ExecMode, sample: number, categoryPath = '') {
  const r = await post<{ run_id: Id }>('/api/execute/start', {
    session_id: sessionId,
    mode,
    sample,
    category_path: categoryPath,
  })
  return r.run_id
}

export async function revertRun(runId: Id) {
  const r = await post<{ run_id: Id }>('/api/execute/revert', { run_id: runId })
  return r.run_id
}

export async function checkExecute(
  sessionId: Id,
  mode: ExecMode,
  sample: number,
  signal?: AbortSignal,
  categoryPath = '',
) {
  const r = await get<Omit<ExecuteCheck, 'issues' | 'notes'> & { issues: Issue[] | null; notes: string[] | null }>(
    '/api/execute/check',
    { session_id: sessionId, mode, sample, category_path: categoryPath },
    signal,
  )
  return { ...r, issues: list(r.issues).filter((i) => i.code !== 'nested'), notes: list(r.notes) }
}

export async function locateFile(sessionId: Id, q: string, signal?: AbortSignal) {
  const r = await get<{ items: LocateItem[] | null }>('/api/execute/locate', { session_id: sessionId, q }, signal)
  return list(r.items)
}

export async function confirmReview(sessionId: Id, fileIds: Id[]) {
  const r = await post<{ confirmed: number }>('/api/plan/confirm_review', { session_id: sessionId, file_ids: fileIds })
  return r.confirmed
}
