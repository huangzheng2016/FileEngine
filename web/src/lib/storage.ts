import type { Protocol } from '@/api/types'

export const PROTOCOL_HINT: Record<Protocol, string> = {
  smb: '',
  sftp: '',
  nfs: '',
  local: '',
  ftp: '大于 32 MB 的 zip 看不到内容，建议用 SMB 或 SFTP',
}
