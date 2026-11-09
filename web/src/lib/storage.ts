import type { Filesystem, Protocol } from '@/api/types'

export interface ProtocolMeta {
  title: string
  desc: string
  port: number
  // where folder browsing starts; NFS starts at the export the user typed
  browseRoot: string
  pathLabel: string
  hint?: string
}

export const PROTOCOLS: Record<Protocol, ProtocolMeta> = {
  smb: {
    title: 'NAS 共享（SMB）',
    desc: '群晖、威联通、飞牛等 NAS 和 Windows 共享文件夹',
    port: 445,
    browseRoot: '',
    pathLabel: '共享文件夹',
  },
  sftp: {
    title: 'SFTP',
    desc: '能用 SSH 登录的服务器或 NAS',
    port: 22,
    browseRoot: '/',
    pathLabel: '根目录',
  },
  nfs: {
    title: 'NFS',
    desc: 'Linux 或 NAS 的 NFS 共享，需在 NAS 上允许本机 IP',
    port: 2049,
    browseRoot: '',
    pathLabel: '导出路径',
  },
  ftp: {
    title: 'FTP',
    desc: '只有 FTP 时使用；大于 32 MB 的 zip 看不到内容',
    port: 21,
    browseRoot: '/',
    pathLabel: '根目录',
    hint: '大于 32 MB 的 zip 看不到内容，建议用 SMB 或 SFTP',
  },
  local: {
    title: '本机目录',
    desc: 'FileEngine 所在电脑上的文件夹',
    port: 0,
    browseRoot: '',
    pathLabel: '本机目录',
  },
}

export const PROTOCOL_ORDER: Protocol[] = ['smb', 'sftp', 'nfs', 'ftp', 'local']

export type AuthMethod = 'password' | 'key'

export const isRemote = (p: Protocol) => p !== 'local'
export const hasLogin = (p: Protocol) => p !== 'local' && p !== 'nfs'

export const authOf = (f: Pick<Filesystem, 'protocol' | 'private_key_set'>): AuthMethod =>
  f.protocol === 'sftp' && f.private_key_set ? 'key' : 'password'

const SEP = /[\\/]+/

// The folder above `path`, never above `top` (where browsing started). Local
// roots go back to the root list (''), which the caller passes as `roots`.
export function parentPath(protocol: Protocol, path: string, top: string, roots: string[] = []): string | null {
  if (path === top) return null
  if (protocol === 'local' && roots.includes(path)) return ''
  const trimmed = path.replace(/[\\/]+$/, '')
  const cut = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
  if (cut < 0) return protocol === 'smb' ? '' : top
  const parent = trimmed.slice(0, cut)
  if (!parent) return protocol === 'smb' ? '' : '/'
  // "C:" alone is not a usable folder on Windows
  if (/^[A-Za-z]:$/.test(parent)) return `${parent}\\`
  return parent
}

// Breadcrumb under the browse root: each crumb carries the path that opens it.
export function crumbs(protocol: Protocol, path: string, top: string, roots: string[] = []) {
  const out: { label: string; path: string }[] = []
  let p: string | null = path
  while (p !== null && p !== top && p !== '') {
    const name = p.split(SEP).filter(Boolean).pop() ?? p
    out.unshift({ label: name, path: p })
    if (roots.includes(p)) break
    p = parentPath(protocol, p, top, roots)
  }
  return out
}

// A readable default name: the chosen folder, else the host.
export function suggestName(protocol: Protocol, host: string, basePath: string): string {
  const last = basePath.split(SEP).filter(Boolean).pop() ?? ''
  if (protocol === 'local') return last || '本机目录'
  return last ? `${last}（${host}）` : host
}
