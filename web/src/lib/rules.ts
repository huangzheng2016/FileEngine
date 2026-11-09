import { fmtBytes } from '@/base/format'
import type { PlanRule } from '@/api/types'

export function splitExts(text: string): string[] {
  return [
    ...new Set(
      text
        .split(/[\s,，、;；]+/)
        .map((e) =>
          e
            .trim()
            .replace(/^\*?\./, '')
            .toLowerCase(),
        )
        .filter(Boolean),
    ),
  ]
}

export const joinExts = (exts: readonly string[]) => exts.join(',')

export function describeMatch(
  r: Pick<PlanRule, 'exts' | 'name_regex' | 'path_prefix' | 'file_type' | 'min_size' | 'max_size'>,
): string {
  const parts: string[] = []
  if (r.file_type === 'directory') parts.push('目录')
  const exts = splitExts(r.exts)
  if (exts.length) parts.push(`扩展名 ${exts.slice(0, 6).join('、')}${exts.length > 6 ? ` 等 ${exts.length} 种` : ''}`)
  if (r.name_regex) parts.push(`文件名符合 ${r.name_regex}`)
  if (r.path_prefix) parts.push(`位于 /${r.path_prefix.replace(/^\//, '')}`)
  if (r.min_size > 0 && r.max_size > 0) parts.push(`大小 ${fmtBytes(r.min_size)} 到 ${fmtBytes(r.max_size)}`)
  else if (r.min_size > 0) parts.push(`大于 ${fmtBytes(r.min_size)}`)
  else if (r.max_size > 0) parts.push(`小于 ${fmtBytes(r.max_size)}`)
  return parts.join(' · ') || '所有未整理的条目'
}

export const TEMPLATE_VARS = [
  '{name}',
  '{stem}',
  '{ext}',
  '{year}',
  '{month}',
  '{day}',
  '{parent}',
  '{rel}',
  '{taken_year}',
  '{taken_month}',
  '{camera}',
] as const

const KNOWN = new Set<string>(TEMPLATE_VARS)

export function targetError(t: string): string {
  const v = t.trim()
  if (!v) return '请输入目标位置'
  if (v.split('/').some((s) => s === '..')) return '目标位置不能包含 ..'
  const unknown = [...v.matchAll(/\{[^}]*\}/g)].map((m) => m[0]).filter((m) => !KNOWN.has(m))
  if (unknown.length) return `无法识别 ${unknown.join('、')}`
  return ''
}

export function regexError(r: string): string {
  if (!r) return ''
  try {
    new RegExp(r)
    return ''
  } catch {
    return '不是有效的正则表达式'
  }
}
