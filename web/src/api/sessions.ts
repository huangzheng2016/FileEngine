import { http } from './http'
import type { LocateResult, ScanSession, ValidationIssue } from './types'

export interface SessionOptions {
  model_provider_id: number
  allow_read_file: boolean
  allow_auto_category: boolean
  allow_vision: boolean
  allow_script: boolean
  exclude_category_dirs: boolean
  filter_mode: 'blacklist' | 'whitelist'
  filter_dirs: string
  scope_exts: string
  extract_metadata: boolean
  detect_duplicates: boolean
  bulk_model_provider_id: number
}

export interface SessionInput extends SessionOptions {
  filesystem_id: number
  scan_path: string
  goal: string
}

export async function listSessions(signal?: AbortSignal) {
  const { data } = await http.get<ScanSession[]>('/sessions', { signal })
  return data ?? []
}

export async function getSession(id: number, signal?: AbortSignal) {
  const { data } = await http.get<ScanSession>(`/sessions/${id}`, { signal })
  return data
}

export async function createSession(body: SessionInput) {
  const { data } = await http.post<ScanSession>('/sessions', body)
  return data
}

export async function updateSession(id: number, body: Partial<SessionOptions & { goal: string }>) {
  const { data } = await http.patch<ScanSession>(`/sessions/${id}`, body)
  return data
}

export async function deleteSession(id: number) {
  await http.delete(`/sessions/${id}`)
}

export async function rescanSession(id: number) {
  await http.post(`/sessions/${id}/rescan`)
}

// Starting returns the new run; the page follows it through the event stream.
export async function startTagging(id: number) {
  const { data } = await http.post<{ run_id?: number }>(`/sessions/${id}/tag`)
  return data.run_id ?? 0
}

export type ExecMode = 'copy' | 'move'

// sample: execute only that many planned entries first; 0 = all of them
export async function startExecute(id: number, mode: ExecMode, sample: number) {
  const { data } = await http.post<{ run_id?: number }>(`/sessions/${id}/execute`, {
    mode,
    sample: sample || undefined,
  })
  return data.run_id ?? 0
}

export async function revertRun(runId: number) {
  const { data } = await http.post<{ run_id?: number }>(`/runs/${runId}/revert`)
  return data.run_id ?? 0
}

export async function locateFile(id: number, q: string, signal?: AbortSignal) {
  const { data } = await http.get<{ items: LocateResult[] | null }>(`/sessions/${id}/locate`, { params: { q }, signal })
  return data.items ?? []
}

// Low-confidence decisions wait for the user: confirm keeps them, reset sends them back.
export async function confirmReview(id: number, fileIds: number[]) {
  await http.post(`/sessions/${id}/review/confirm`, { file_ids: fileIds })
}

export async function validatePlans(id: number, signal?: AbortSignal) {
  const { data } = await http.get<{ ok: boolean; issues: ValidationIssue[] | null }>(
    `/sessions/${id}/execute/validate`,
    { signal },
  )
  return { ok: data.ok, issues: data.issues ?? [] }
}

export const plansExportUrl = (id: number) => `/sessions/${id}/plans/export`
