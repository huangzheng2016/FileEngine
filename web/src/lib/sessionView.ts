import type { AgentRun, ScanSession } from '@/api/types'
import { fmtNumber } from '@/base/format'

export function sessionPath(s: Pick<ScanSession, 'scan_path'>): string {
  const p = s.scan_path.replace(/^\/+|\/+$/g, '')
  return p ? `/${p}` : '整个存储'
}

type Counts = Pick<ScanSession, 'planned_ops' | 'executed_ops' | 'review_count'>

// Moves the executor will carry out; entries awaiting review are skipped.
export const executable = (s: Counts) => Math.max(0, s.planned_ops - s.executed_ops - (s.review_count ?? 0))

export type NextKind = 'rescan' | 'tag' | 'plan_tag' | 'execute' | 'review' | 'none'

export interface NextAction {
  kind: NextKind
  label: string
}

// hasDraft: a plan draft exists that has not been approved yet.
export function nextAction(s: ScanSession, running: Pick<AgentRun, 'phase'> | undefined, hasDraft = false): NextAction {
  if (s.status === 'scanning') return { kind: 'none', label: '' }
  if (s.status === 'error' || s.status === 'interrupted') return { kind: 'rescan', label: '重新扫描' }
  if (running) return { kind: 'none', label: '' }
  if (s.planned_ops === 0) {
    if (s.tagged_files > 0) return { kind: 'tag', label: '继续整理' }
    return hasDraft ? { kind: 'plan_tag', label: '按方案整理' } : { kind: 'tag', label: '开始整理' }
  }
  const n = executable(s)
  if (n > 0) return { kind: 'execute', label: s.executed_ops > 0 ? `执行其余 ${fmtNumber(n)} 项` : '执行' }
  if (s.review_count > 0) return { kind: 'review', label: '去确认' }
  return { kind: 'none', label: '' }
}

export const runningRun = (runs: readonly AgentRun[]) => runs.find((r) => r.status === 'running')

// The running run of a list item, from its latest_run summary.
export const listRunning = (s: ScanSession) => (s.latest_run?.status === 'running' ? s.latest_run : undefined)

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
  | 'no_moves'
  | 'planned'
  | 'done'

// A chat turn does not move the task along; the page shows the stage under it.
export const flowRun = <T extends Pick<AgentRun, 'phase'>>(running: T | undefined) =>
  running?.phase === 'chat' ? undefined : running

export function stageOf(s: ScanSession, running: Pick<AgentRun, 'phase'> | undefined): Stage {
  if (s.status === 'scanning') return 'scanning'
  if (s.status === 'error') return 'scan_failed'
  if (s.status === 'interrupted') return 'scan_interrupted'
  if (running) return running.phase === 'revert' ? 'executing' : running.phase
  if (s.planned_ops === 0) return s.tagged_files > 0 ? 'no_moves' : 'ready'
  return executable(s) > 0 || s.review_count > 0 ? 'planned' : 'done'
}

// The four steps a task goes through, as the page's progress line shows them.
export const FLOW_STEPS = [
  { key: 'scan', label: '扫描' },
  { key: 'organize', label: 'AI 整理' },
  { key: 'confirm', label: '确认结果' },
  { key: 'execute', label: '执行' },
] as const

// Index of the current step; FLOW_STEPS.length once everything is done.
export function flowStep(stage: Stage): number {
  switch (stage) {
    case 'scanning':
    case 'scan_failed':
    case 'scan_interrupted':
      return 0
    case 'ready':
    case 'chat':
    case 'survey':
    case 'tagging':
    case 'review':
      return 1
    case 'planned':
    case 'no_moves':
      return 2
    case 'executing':
      return 3
    case 'done':
      return FLOW_STEPS.length
  }
}
