import type { Protocol } from '@/api/types'

// What each connection can do with archives, in the operator's words.
export const PROTOCOL_HINT: Record<Protocol, string> = {
  smb: 'NAS 首选，压缩包可以直接查看内容',
  sftp: '压缩包可以直接查看内容',
  nfs: '压缩包可以直接查看内容',
  local: '运行 FileEngine 的这台机器上的目录',
  ftp: '超过 32 MB 的 zip 压缩包只能看到大小，建议改用 SMB 或 SFTP',
}
