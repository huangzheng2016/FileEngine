import { TOOL_LABEL } from '@/base/labels'
import { baseName, parentOf } from './paths'
import type { Item, Lane, RunView, ToolItem, UnitItem } from './transcript'

type Obj = Record<string, unknown>
const obj = (v: unknown): Obj => (v && typeof v === 'object' && !Array.isArray(v) ? (v as Obj) : {})
const s = (v: unknown) => (typeof v === 'string' ? v : typeof v === 'number' ? String(v) : '')
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : [])
const count = (v: unknown) => (Array.isArray(v) ? v.length : typeof v === 'number' ? v : 0)

export const toolLabel = (name: string) => TOOL_LABEL[name] ?? '其他操作'

// Tool output wraps untrusted text in <data> blocks; people read what is inside.
const unwrapData = (text: string) =>
  text
    .replace(/^\s*<data>\n?/, '')
    .replace(/\n?<\/data>\s*$/, '')
    .trimEnd()

// What the call is about, from its arguments alone (known while it runs).
export function toolSubject(t: ToolItem): string {
  const a = obj(t.args)
  switch (t.name) {
    case 'list_entries': {
      const paths = arr(a.paths)
      if (paths.length) return `${paths.length} 个路径`
      return shownPath(s(a.parent_path))
    }
    case 'view_image': {
      const paths = arr(a.paths)
      return paths.length === 1 ? baseName(s(paths[0])) : `${paths.length} 张`
    }
    case 'decide':
    case 'describe': {
      const n = arr(a.items).length
      return n ? `${n} 项` : ''
    }
    case 'create_category':
    case 'update_category':
    case 'delete_category':
      return [s(a.name), s(a.path)].filter(Boolean).join(' · ')
    case 'note':
    case 'remember':
      return s(a.content)
    case 'recall':
      return s(a.query) || s(a.path_prefix)
    case 'add_rule':
    case 'update_rule':
      return [s(a.name), s(a.target)].filter(Boolean).join(' → ')
    case 'delete_rule':
      return s(a.name)
    case 'apply_rules':
      return a.preview ? '预览' : s(a.rule)
    case 'run_script':
    case 'write_plan':
    case 'read_plan':
      return ''
    default:
      return s(a.path) || s(a.name) || s(a.query) || ''
  }
}

const plus = (...parts: string[]) => parts.filter(Boolean).join('，')

// The one line a step shows: what it did, in the person's terms. A step that is
// still running, or has nothing to report, shows its subject.
export function toolSummary(t: ToolItem): string {
  const subject = toolSubject(t)
  if (t.status === 'running') return subject
  const out = obj(t.output)
  const a = obj(t.args)
  const failed = arr(out.failed).length
  switch (t.name) {
    case 'decide': {
      if (!Array.isArray(a.items) || (t.status === 'failed' && !failed)) return subject
      const ok = typeof out.ok === 'number' ? out.ok : t.status === 'ok' ? a.items.length : 0
      const review = count(out.needs_review)
      return plus(`决定 ${ok} 项`, review ? `${review} 项待复查` : '', failed ? `${failed} 项未写入` : '')
    }
    case 'describe': {
      if (t.status === 'failed' && !failed) return subject
      const ok = typeof out.ok === 'number' ? out.ok : 0
      return plus(`写了 ${ok} 条说明`, failed ? `${failed} 条未写入` : '')
    }
    case 'list_entries': {
      if (t.status === 'failed') return subject
      const n = s(out.rows) ? unwrapData(s(out.rows)).split('\n').filter(Boolean).length : 0
      return plus(subject ? `${subject}：${n} 项` : `${n} 项`, out.has_more ? '还有更多' : '')
    }
    case 'read_file':
      return plus(subject, out.truncated ? '内容较长，只读了一部分' : '')
    case 'split_unit': {
      const n = typeof out.released === 'number' ? out.released : 0
      return plus(subject, n ? `放开 ${n} 项` : '')
    }
    case 'apply_rules': {
      const rules = arr(out.rules).map(obj)
      const matched = rules.reduce((n, r) => n + (typeof r.matched === 'number' ? r.matched : 0), 0)
      return plus(`${a.preview ? '预览' : '应用'} ${rules.length} 条规则`, `匹配 ${matched} 项`)
    }
    case 'recall':
      return plus(subject, `${arr(out.items).length} 条`)
    case 'write_plan':
      return typeof out.version === 'number' ? `方案第 ${out.version} 版` : ''
    case 'read_plan':
      return out.exists === false ? '还没有方案' : typeof out.version === 'number' ? `方案第 ${out.version} 版` : ''
    case 'create_category':
      return plus(subject, arr(out.similar_categories).length ? '有相似的分类' : '')
    case 'run_script':
      return out.truncated ? '输出较长，已截断' : ''
    default:
      return subject
  }
}

// Why a step failed: the tool's error and the way forward it suggested.
export function toolProblem(t: ToolItem): { error: string; hint: string } {
  const out = obj(t.output)
  return { error: t.error || s(out.error), hint: s(out.hint) }
}

export interface FileRow {
  name: string
  dir: boolean
  target: string
  review: boolean
}

// list_entries: the first rows of what the agent saw, from its TSV
// (name, type, size, role, target, description).
export function fileRows(output: unknown): { rows: FileRow[]; total: number; more: boolean } {
  const o = obj(output)
  const lines = unwrapData(s(o.rows)).split('\n').filter(Boolean)
  const rows = lines.slice(0, 8).map((line) => {
    const [name = '', type = '', , role = '', target = ''] = line.split('\t')
    return { name, dir: type === 'd', target, review: role === '?' }
  })
  return { rows, total: lines.length, more: o.has_more === true }
}

export function readExcerpt(output: unknown, max = 600): string {
  const o = obj(output)
  const text = unwrapData(s(o.content) || s(o.output) || (typeof output === 'string' ? output : ''))
  return text.length > max ? `${text.slice(0, max)}…` : text
}

export interface RuleRow {
  name: string
  matched: number
  samples: string[]
}

export function ruleRows(output: unknown): RuleRow[] {
  return arr(obj(output).rules).map((r) => {
    const o = obj(r)
    return {
      name: s(o.name),
      matched: typeof o.matched === 'number' ? o.matched : 0,
      samples: arr(o.samples).map((x) => `${baseName(s(obj(x).path))} → ${s(obj(x).target)}`),
    }
  })
}

export interface DecisionRow {
  key: string
  path: string
  // '' when the entry stays where it is
  target: string
  role: 'unit' | 'file' | 'skip'
  reason: string
}

const ROLE_OF: Record<string, DecisionRow['role']> = { move: 'file', unit: 'unit', skip: 'skip' }
const rowsOf = new WeakMap<ToolItem, DecisionRow[]>()

// What a decide call actually wrote: its items minus the rejected ones, with
// server-side renames applied. Containers and undos are left out: they decide nothing.
export function toolDecisions(t: ToolItem): DecisionRow[] {
  if (t.status === 'running' || t.name !== 'decide') return []
  const cached = rowsOf.get(t)
  if (cached) return cached
  const rows = decisionRows(t)
  rowsOf.set(t, rows)
  return rows
}

function decisionRows(t: ToolItem): DecisionRow[] {
  const out = obj(t.output)
  if (t.status === 'failed' && !Array.isArray(out.failed)) return []
  const failed = new Set(arr(out.failed).map((f) => s(obj(f).path)))
  const renamed = new Map(arr(out.renamed).map((r) => [s(obj(r).path), s(obj(r).new_path)] as const))
  const rows: DecisionRow[] = []
  for (const raw of arr(obj(t.args).items)) {
    const it = obj(raw)
    const path = s(it.path)
    const role = ROLE_OF[s(it.action)]
    if (!path || !role || failed.has(path)) continue
    rows.push({
      key: `${t.key}:${path}`,
      path,
      target: role === 'skip' ? '' : renamed.get(path) || s(it.new_path),
      role,
      reason: s(it.reason),
    })
  }
  return rows
}

export function failedDecisions(t: ToolItem): { path: string; error: string }[] {
  return arr(obj(t.output).failed).map((f) => ({ path: s(obj(f).path), error: s(obj(f).error) }))
}

export interface Activity {
  // the unit each worker is on now
  current: UnitItem[]
  // newest first
  decisions: DecisionRow[]
}

export const MAX_DECISIONS = 200

interface LaneScan {
  items: readonly Item[]
  open?: UnitItem
  // finished decide calls with at least one row, in lane order
  decided: ToolItem[]
  // index of each entry of `decided` in `items`
  at: number[]
}

function scanLane(lane: Lane, prev: LaneScan | undefined): LaneScan {
  const items = lane.items
  let k = 0
  if (prev) {
    const n = Math.min(items.length, prev.items.length)
    while (k < n && items[k] === prev.items[k]) k++
  }
  let keep = 0
  if (prev) while (keep < prev.at.length && prev.at[keep] < k) keep++
  const decided = prev ? prev.decided.slice(0, keep) : []
  const at = prev ? prev.at.slice(0, keep) : []
  let open: UnitItem | undefined
  for (let i = k - 1; i >= 0; i--) {
    const it = items[i]
    if (it.kind === 'unit') {
      open = it.done ? undefined : it
      break
    }
  }
  for (let i = k; i < items.length; i++) {
    const it = items[i]
    if (it.kind === 'unit') open = it.done ? undefined : it
    else if (it.kind === 'tool' && toolDecisions(it).length) {
      decided.push(it)
      at.push(i)
    }
  }
  return { items, open, decided, at }
}

function collect(run: RunView, scans: LaneScan[]): Activity {
  const current: UnitItem[] = []
  const tools: ToolItem[] = []
  for (const sc of scans) {
    if (sc.open && !run.done) current.push(sc.open)
    tools.push(...sc.decided)
  }
  tools.sort((a, b) => (b.seq ?? 0) - (a.seq ?? 0))
  const decisions: DecisionRow[] = []
  for (const t of tools) {
    decisions.push(...toolDecisions(t))
    if (decisions.length >= MAX_DECISIONS) break
  }
  return { current, decisions: decisions.slice(0, MAX_DECISIONS) }
}

// Full rebuild; createRunActivity gives the same result without rescanning lanes
// that did not change.
export function runActivity(run: RunView): Activity {
  return collect(
    run,
    run.lanes.map((l) => scanLane(l, undefined)),
  )
}

export function createRunActivity() {
  const scans = new Map<number, LaneScan>()
  return (run: RunView): Activity => {
    const next = run.lanes.map((l) => {
      const prev = scans.get(l.workerId)
      const sc = prev && prev.items === l.items ? prev : scanLane(l, prev)
      scans.set(l.workerId, sc)
      return sc
    })
    return collect(run, next)
  }
}

// Paths as people read them: no leading slash; the scan root has a name.
export const shownPath = (p: string) => p.replace(/^\/+|\/+$/g, '') || '根文件夹'

// Where an entry lands, as the folder it goes into; the full path only when
// the name changes on the way.
export function landing(from: string, target: string): string {
  if (!target) return '保持原位'
  return baseName(target) === baseName(from) ? shownPath(parentOf(target)) : shownPath(target)
}
