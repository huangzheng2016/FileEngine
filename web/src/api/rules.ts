import { http } from './http'
import type { PlanRule, RulePreview } from './types'

export type RuleInput = Pick<
  PlanRule,
  'name' | 'exts' | 'name_regex' | 'path_prefix' | 'file_type' | 'min_size' | 'max_size' | 'target' | 'role'
> & { position?: number; source?: PlanRule['source'] }

export async function listRules(sessionId: number, signal?: AbortSignal) {
  const { data } = await http.get<{ items: PlanRule[] | null }>(`/sessions/${sessionId}/rules`, { signal })
  return (data.items ?? []).slice().sort((a, b) => a.position - b.position)
}

export async function createRule(sessionId: number, body: RuleInput) {
  const { data } = await http.post<PlanRule>(`/sessions/${sessionId}/rules`, body)
  return data
}

export async function updateRule(id: number, body: Partial<RuleInput>) {
  const { data } = await http.put<PlanRule>(`/rules/${id}`, body)
  return data
}

export async function deleteRule(id: number) {
  await http.delete(`/rules/${id}`)
}

export async function previewRules(sessionId: number, signal?: AbortSignal) {
  const { data } = await http.post<{ items: RulePreview[] | null }>(`/sessions/${sessionId}/rules/apply`, null, {
    params: { preview: 1 },
    signal,
  })
  return data.items ?? []
}

export async function applyRules(sessionId: number) {
  const { data } = await http.post<{ items: RulePreview[] | null }>(`/sessions/${sessionId}/rules/apply`)
  return (data.items ?? []).reduce((n, r) => n + r.matched, 0)
}
