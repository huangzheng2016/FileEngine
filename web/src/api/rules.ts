import { get, list, post } from './http'
import type { Id, PlanRule, RuleApplyStat } from './types'

export type RuleInput = Pick<
  PlanRule,
  'name' | 'exts' | 'name_regex' | 'path_prefix' | 'file_type' | 'min_size' | 'max_size' | 'target' | 'role'
> & { position?: number; source?: PlanRule['source'] }

export async function listRules(sessionId: Id, signal?: AbortSignal) {
  const r = await get<{ items: PlanRule[] | null }>('/api/rule/list', { session_id: sessionId }, signal)
  return list(r.items)
    .slice()
    .sort((a, b) => a.position - b.position)
}

export async function createRule(sessionId: Id, body: RuleInput) {
  const r = await post<{ item: PlanRule }>('/api/rule/create', { ...body, session_id: sessionId })
  return r.item
}

export async function updateRule(id: Id, body: Partial<RuleInput>) {
  const r = await post<{ item: PlanRule }>('/api/rule/update', { ...body, id })
  return r.item
}

export async function deleteRule(id: Id) {
  await post<void>('/api/rule/delete', { id })
}

export async function previewRules(sessionId: Id, signal?: AbortSignal) {
  const r = await post<{ items: RuleApplyStat[] | null }>(
    '/api/rule/apply',
    { session_id: sessionId, preview: true },
    signal,
  )
  return list(r.items)
}

export async function applyRulesSummary(sessionId: Id) {
  const r = await post<{ applied: number; skipped: number }>('/api/rule/apply', {
    session_id: sessionId,
    preview: false,
  })
  return { applied: r.applied ?? 0, skipped: r.skipped ?? 0 }
}
