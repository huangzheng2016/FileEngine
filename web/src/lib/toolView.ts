import { TOOL_LABEL } from '@/base/labels'
import { baseName, parentOf } from './paths'
import type { RunView, ToolItem, UnitItem } from './transcript'

type Obj = Record<string, unknown>
const obj = (v: unknown): Obj => (v && typeof v === 'object' && !Array.isArray(v) ? (v as Obj) : {})
const s = (v: unknown) => (typeof v === 'string' ? v : typeof v === 'number' ? String(v) : '')

export const toolLabel = (name: string) => TOOL_LABEL[name] ?? '其他操作'

export function toolSubject(t: ToolItem): string {
  const a = obj(t.args)
  switch (t.name) {
    case 'set_target':
      return a.new_path ? `${s(a.path)} → ${s(a.new_path)}` : `${s(a.path)} 保持原位`
    case 'set_targets':
    case 'decide_dirs': {
      const items = Array.isArray(a.items) ? a.items : Array.isArray(t.args) ? (t.args as unknown[]) : []
      return items.length ? `${items.length} 项` : ''
    }
    case 'create_category':
    case 'update_category':
    case 'delete_category':
      return [s(a.name), s(a.path)].filter(Boolean).join(' · ')
    case 'note':
    case 'remember':
      return s(a.content)
    case 'recall':
      return s(a.query)
    case 'add_rule':
    case 'update_rule':
      return [s(a.name), s(a.target)].filter(Boolean).join(' → ')
    default:
      return s(a.path) || s(a.parent_path) || s(a.name) || s(a.query) || ''
  }
}

export interface FileRow {
  name: string
  dir: boolean
  target: string
}

// list_files / query_files: the first rows of what the agent saw; list_files
// returns compact TSV rows relative to `parent`.
export function fileRows(output: unknown): { rows: FileRow[]; total: number } {
  const o = obj(output)
  if (typeof o.rows === 'string') {
    const lines = o.rows.split('\n').filter(Boolean)
    const rows = lines.slice(0, 8).map((line) => {
      const [name = '', type = '', , , target = ''] = line.split('\t')
      return { name, dir: type === 'd', target }
    })
    return { rows, total: typeof o.total === 'number' ? o.total : lines.length }
  }
  const list = Array.isArray(o.files) ? o.files : Array.isArray(o.items) ? o.items : Array.isArray(output) ? output : []
  const rows = list.slice(0, 8).map((f) => {
    const r = obj(f)
    return {
      name: s(r.name) || s(r.path) || s(r.original_path),
      dir: r.file_type === 'directory' || r.is_dir === true,
      target: s(r.new_path),
    }
  })
  const total = typeof o.total === 'number' ? o.total : list.length
  return { rows, total }
}

export function readExcerpt(output: unknown, max = 600): string {
  const o = obj(output)
  const text = s(o.content) || s(o.text) || (typeof output === 'string' ? output : '')
  return text.length > max ? `${text.slice(0, max)}…` : text
}

export interface DecisionRow {
  key: string
  path: string
  // '' when the entry stays where it is
  target: string
  role: 'unit' | 'file' | 'skip'
  reason: string
  // below the confidence at which the server asks the person to confirm
  review: boolean
}

const REVIEW_CONFIDENCE = 0.6

// What a set_targets / decide_dirs call actually wrote: its items minus the
// rejected ones, with server-side renames applied. Containers are left out:
// they only say the folder's contents are decided one by one later.
export function toolDecisions(t: ToolItem): DecisionRow[] {
  if (t.status === 'running' || (t.name !== 'set_targets' && t.name !== 'decide_dirs')) return []
  const out = obj(t.output)
  if (out.error || (t.status === 'failed' && !Array.isArray(out.failed))) return []
  const failed = new Set((Array.isArray(out.failed) ? out.failed : []).map((f) => s(obj(f).path)))
  const renamed = new Map(
    (Array.isArray(out.renamed) ? out.renamed : []).map((r) => [s(obj(r).path), s(obj(r).new_path)] as const),
  )
  const items = obj(t.args).items
  const rows: DecisionRow[] = []
  for (const raw of Array.isArray(items) ? items : []) {
    const it = obj(raw)
    const path = s(it.path)
    if (!path || failed.has(path)) continue
    const role = t.name === 'set_targets' ? (s(it.new_path) ? 'file' : '') : s(it.role)
    if (role !== 'unit' && role !== 'file' && role !== 'skip') continue
    const target = role === 'skip' ? '' : renamed.get(path) || s(it.new_path)
    const confidence = typeof it.confidence === 'number' ? it.confidence : 0
    rows.push({
      key: `${t.id}:${path}`,
      path,
      target,
      role,
      reason: s(it.reason),
      review: !!target && confidence > 0 && confidence < REVIEW_CONFIDENCE,
    })
  }
  return rows
}

export interface Activity {
  // the unit each worker is on now
  current: UnitItem[]
  // newest first
  decisions: DecisionRow[]
}

export function runActivity(run: RunView): Activity {
  const current: UnitItem[] = []
  const done: ToolItem[] = []
  for (const lane of run.lanes) {
    let open: UnitItem | undefined
    for (const it of lane.items) {
      if (it.kind === 'unit') {
        open = it.done ? undefined : it
      } else if (it.kind === 'tool' && it.status !== 'running') done.push(it)
    }
    if (open && !run.done) current.push(open)
  }
  done.sort((a, b) => (b.seq ?? 0) - (a.seq ?? 0))
  return { current, decisions: done.flatMap((t) => toolDecisions(t)) }
}

// Paths as people read them: no leading slash; the scan root has a name.
export const shownPath = (p: string) => p.replace(/^\/+|\/+$/g, '') || '根文件夹'

// Where an entry lands, as the folder it goes into; the full path only when
// the name changes on the way.
export function landing(from: string, target: string): string {
  if (!target) return '保持原位'
  return baseName(target) === baseName(from) ? shownPath(parentOf(target)) : shownPath(target)
}
