import type { AgentRun, ScanSession } from '@/api/types'

export function sessionPath(s: Pick<ScanSession, 'scan_path'>): string {
  const p = s.scan_path.replace(/^\/+|\/+$/g, '')
  return p ? `/${p}` : '全部目录'
}

export type NextKind = 'rescan' | 'tag' | 'preview' | 'execute' | 'none'

export interface NextAction {
  kind: NextKind
  label: string
}

// The one thing the session page offers as its primary action right now.
export function nextAction(s: ScanSession, latest: AgentRun | undefined, onPreview: boolean): NextAction {
  if (s.status === 'scanning') return { kind: 'none', label: '' }
  if (s.status === 'error' || s.status === 'interrupted') return { kind: 'rescan', label: '重新扫描' }
  if (latest?.status === 'running') return { kind: 'none', label: '' }
  if (s.planned_ops === 0) return { kind: 'tag', label: '开始整理' }
  if (s.executed_ops < s.planned_ops)
    return onPreview ? { kind: 'execute', label: '执行' } : { kind: 'preview', label: '预览' }
  return { kind: 'none', label: '' }
}

export const runningRun = (runs: readonly AgentRun[]) => runs.find((r) => r.status === 'running')

export type Stage =
  | 'scanning'
  | 'scan_failed'
  | 'scan_interrupted'
  | 'survey'
  | 'tagging'
  | 'review'
  | 'chat'
  | 'executing'
  | 'ready'
  | 'planned'
  | 'done'

export function stageOf(s: ScanSession, running: AgentRun | undefined): Stage {
  if (s.status === 'scanning') return 'scanning'
  if (s.status === 'error') return 'scan_failed'
  if (s.status === 'interrupted') return 'scan_interrupted'
  if (running) return running.phase
  if (s.planned_ops === 0) return 'ready'
  return s.executed_ops < s.planned_ops ? 'planned' : 'done'
}
