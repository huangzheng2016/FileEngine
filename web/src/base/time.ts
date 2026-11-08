// Every timestamp on screen goes through these. Input is unix seconds; empty or
// invalid values render as '—' so tables never show blank cells or "Invalid Date".

const pad = (n: number) => String(n).padStart(2, '0')

function toDate(sec?: number | null): Date | null {
  if (!sec) return null
  const d = new Date(sec * 1000)
  return isNaN(d.getTime()) ? null : d
}

// 2026-10-26 14:03
export function fmtTime(sec?: number | null): string {
  const d = toDate(sec)
  if (!d) return '—'
  return `${fmtDate(sec)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// 2026-10-26
export function fmtDate(sec?: number | null): string {
  const d = toDate(sec)
  if (!d) return '—'
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// 刚刚 / 5 分钟前 / 3 小时后; beyond 30 days falls back to the date
export function fmtRelative(sec?: number | null, nowSec = Math.floor(Date.now() / 1000)): string {
  if (!toDate(sec)) return '—'
  const delta = sec! - nowSec
  const abs = Math.abs(delta)
  if (abs < 60) return '刚刚'
  if (abs >= 30 * 86400) return fmtDate(sec)
  const n =
    abs < 3600
      ? `${Math.floor(abs / 60)} 分钟`
      : abs < 86400
        ? `${Math.floor(abs / 3600)} 小时`
        : `${Math.floor(abs / 86400)} 天`
  return delta < 0 ? `${n}前` : `${n}后`
}

// 45 秒 / 12 分钟 / 3 小时 20 分钟 / 2 天 4 小时
export function fmtDuration(sec?: number | null): string {
  if (sec == null || !Number.isFinite(sec)) return '—'
  const s = Math.max(0, Math.floor(sec))
  if (s < 60) return `${s} 秒`
  if (s < 3600) return `${Math.floor(s / 60)} 分钟`
  if (s < 86400) {
    const m = Math.floor((s % 3600) / 60)
    return `${Math.floor(s / 3600)} 小时${m ? ` ${m} 分钟` : ''}`
  }
  const h = Math.floor((s % 86400) / 3600)
  return `${Math.floor(s / 86400)} 天${h ? ` ${h} 小时` : ''}`
}

// <input type="datetime-local"> value <-> unix seconds
export function toInputValue(sec?: number | null): string {
  const d = toDate(sec)
  if (!d) return ''
  return `${fmtDate(sec)}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function fromInputValue(value: string): number {
  if (!value) return 0
  const t = new Date(value).getTime()
  return Number.isFinite(t) ? Math.floor(t / 1000) : 0
}

// Date-range filters keep one URL-friendly string: '' (any time), a preset key,
// or 'YYYY-MM-DD~YYYY-MM-DD'. rangeBounds turns it into [from, to) unix seconds.
export const RANGE_PRESETS = [
  { value: 'today', label: '今天', days: 1 },
  { value: '7d', label: '最近 7 天', days: 7 },
  { value: '30d', label: '最近 30 天', days: 30 },
] as const

export function rangeBounds(range: string, now = new Date()): { from: number; to: number } | null {
  const startOfDay = (d: Date) => Math.floor(new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() / 1000)
  const preset = RANGE_PRESETS.find((p) => p.value === range)
  if (preset) {
    const to = startOfDay(now) + 86400
    return { from: to - preset.days * 86400, to }
  }
  const m = /^(\d{4}-\d{2}-\d{2})~(\d{4}-\d{2}-\d{2})$/.exec(range)
  if (!m) return null
  const from = startOfDay(new Date(`${m[1]}T00:00`))
  const to = startOfDay(new Date(`${m[2]}T00:00`)) + 86400
  return Number.isFinite(from) && Number.isFinite(to) && from < to ? { from, to } : null
}
