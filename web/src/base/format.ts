import type { LabelMap } from './labels'

// Sizes, counts, ratios and enum text on screen go through these, like times go through
// time.ts. Each one owns its unit, so a missing value renders as a bare '—' and never as
// '— ms' or '— %'. Templates never append a unit to a formatted value.

export function fmtBytes(n?: number | null): string {
  if (n == null || !Number.isFinite(n)) return '—'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

// 850 ms / 1.2 s / 3 分 4 秒
export function fmtMs(ms?: number | null): string {
  if (ms == null || !Number.isFinite(ms)) return '—'
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)} 分 ${s % 60} 秒`
}

export function fmtNumber(n?: number | null): string {
  return n == null || !Number.isFinite(n) ? '—' : n.toLocaleString('zh-CN')
}

// 0.125 -> 12.5%; digits applies below 10% where precision matters
export function fmtRatio(r?: number | null, digits = 1): string {
  if (r == null || !Number.isFinite(r)) return '—'
  const p = r * 100
  return `${Math.abs(p) < 10 ? p.toFixed(digits) : Math.round(p)}%`
}

// For quota-like fields where the backend stores "unlimited" as 0 or a negative number.
export function fmtLimit(n?: number | null, fmt: (n: number) => string = fmtNumber): string {
  if (n == null || !Number.isFinite(n)) return '—'
  return n <= 0 ? '不限' : fmt(n)
}

// Enum text outside a StatusPill (sentences, select options, CSV). Unknown values show
// as-is so a new backend value is visible instead of silently blank.
export function fmtLabel(map: LabelMap, value?: string | number | null): string {
  if (value == null || value === '') return '—'
  return map[String(value)]?.text ?? String(value)
}

// Options for Segmented / select / Combobox built from the label registry, so filters never
// ask the user to type or read a raw enum code.
export function labelOptions(map: LabelMap): { value: string; label: string }[] {
  return Object.entries(map).map(([value, l]) => ({ value, label: l.text }))
}
