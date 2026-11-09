// Paths are slash paths relative to the scan root; '' is the root itself.
export function normalize(p: string): string {
  return p
    .replace(/\\/g, '/')
    .replace(/\/+/g, '/')
    .replace(/^\/|\/$/g, '')
}

export function joinPath(dir: string, name: string): string {
  const d = normalize(dir)
  return d ? `${d}/${normalize(name)}` : normalize(name)
}

export function parentOf(p: string): string {
  const n = normalize(p)
  const i = n.lastIndexOf('/')
  return i < 0 ? '' : n.slice(0, i)
}

export function baseName(p: string): string {
  const n = normalize(p)
  return n.slice(n.lastIndexOf('/') + 1)
}

export interface Crumb {
  label: string
  path: string
}

// rooted: the side's paths start with '/' (the after side of the preview)
export function crumbs(p: string, rootLabel = '全部', rooted = false): Crumb[] {
  const parts = normalize(p).split('/').filter(Boolean)
  const lead = rooted ? '/' : ''
  return [
    { label: rootLabel, path: '' },
    ...parts.map((label, i) => ({ label, path: lead + parts.slice(0, i + 1).join('/') })),
  ]
}

export function sortItems<T extends { is_dir: boolean; name: string }>(items: readonly T[]): T[] {
  return [...items].sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.name.localeCompare(b.name, 'zh-CN'))
}
