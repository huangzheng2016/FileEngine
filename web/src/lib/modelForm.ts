import { fmtLabel, fmtNumber } from '@/base/format'
import { REASONING_EFFORT } from '@/base/labels'

export const EFFORT_PRESETS = ['low', 'medium', 'high', 'max'] as const

// Custom levels typed by the user fall through as-is.
export const effortLabel = (v: string) => fmtLabel(REASONING_EFFORT, v)

const EFFORT_RE = /^[a-zA-Z0-9_-]{1,32}$/

export function effortError(v: string): string {
  if (!v) return ''
  return EFFORT_RE.test(v) ? '' : '思考强度只能包含字母、数字、下划线和短横线，最多 32 个字符'
}

export interface EffortOption {
  value: string
  label: string
  hint?: string
}

// Options for the effort Combobox: the presets matching what was typed, plus the
// typed text itself so any level the provider accepts can be entered.
export function effortOptions(query: string): EffortOption[] {
  const q = query.trim()
  const presets: EffortOption[] = EFFORT_PRESETS.map((p) => ({ value: p, label: effortLabel(p), hint: p }))
  const shown = q ? presets.filter((o) => o.value.includes(q.toLowerCase()) || o.label.includes(q)) : presets
  if (q && !presets.some((o) => o.value === q || o.label === q)) shown.push({ value: q, label: q, hint: '自定义' })
  return shown
}

export const CONTEXT_PRESETS = [
  { value: 128_000, label: '128K' },
  { value: 200_000, label: '200K' },
  { value: 256_000, label: '256K' },
  { value: 512_000, label: '512K' },
  { value: 1_000_000, label: '1M' },
  { value: 2_000_000, label: '2M' },
] as const

export const DEFAULT_CONTEXT = 200_000

export function isContextPreset(v: number): boolean {
  return CONTEXT_PRESETS.some((p) => p.value === v)
}

// "1M", "256K", "300000" → tokens; 0 when not understood.
export function parseTokens(text: string): number {
  const m = /^\s*(\d+(?:\.\d+)?)\s*([kKmM万]?)\s*$/.exec(text)
  if (!m) return 0
  const n = Number(m[1])
  const unit = m[2].toLowerCase()
  const mult = unit === 'k' ? 1000 : unit === 'm' ? 1_000_000 : unit === '万' ? 10_000 : 1
  return Math.round(n * mult)
}

export function fmtTokens(n: number): string {
  if (!n) return '—'
  if (n % 1_000_000 === 0) return `${n / 1_000_000}M`
  if (n % 1000 === 0) return `${n / 1000}K`
  return fmtNumber(n)
}

export function contextError(v: number): string {
  if (!Number.isInteger(v) || v <= 0) return '请输入上下文长度，例如 300K'
  if (v < 4000) return '上下文长度至少 4K'
  return ''
}

export function baseUrlError(v: string): string {
  if (!v.trim()) return ''
  try {
    const u = new URL(v.trim())
    return u.protocol === 'http:' || u.protocol === 'https:' ? '' : '地址需以 http:// 或 https:// 开头'
  } catch {
    return '地址格式不正确'
  }
}
