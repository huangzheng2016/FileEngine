// Readable views of agent tool calls: a one-line subject for the folded head and a
// semantic body per common tool. Raw arguments and output stay in a folded CodeBlock.
import { TOOL_LABEL } from '@/base/labels'
import type { ToolItem } from './transcript'

type Obj = Record<string, unknown>
const obj = (v: unknown): Obj => (v && typeof v === 'object' && !Array.isArray(v) ? (v as Obj) : {})
const s = (v: unknown) => (typeof v === 'string' ? v : typeof v === 'number' ? String(v) : '')

export const toolLabel = (name: string) => TOOL_LABEL[name] ?? '其他操作'

export function toolSubject(t: ToolItem): string {
  const a = obj(t.args)
  switch (t.name) {
    case 'set_target':
      return a.new_path ? `${s(a.path)} → ${s(a.new_path)}` : `${s(a.path)} 取消目标`
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

// list_files / query_files: the first rows of what the agent saw. list_files
// returns compact TSV rows (名称/类型/大小/角色/目标/描述) relative to `parent`.
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
