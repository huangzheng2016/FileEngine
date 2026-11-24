import { reactive } from 'vue'
import type { Id } from '@/api/types'

export interface ChatFile {
  id: Id
  name: string
}

const picked = reactive(new Map<Id, ChatFile[]>())

export function chatFiles(sessionId: Id): ChatFile[] {
  return picked.get(sessionId) ?? []
}

export function addChatFiles(sessionId: Id, files: ChatFile[]) {
  const cur = chatFiles(sessionId)
  const ids = new Set(cur.map((f) => f.id))
  picked.set(sessionId, [...cur, ...files.filter((f) => !ids.has(f.id))])
}

export function removeChatFile(sessionId: Id, id: Id) {
  picked.set(
    sessionId,
    chatFiles(sessionId).filter((f) => f.id !== id),
  )
}

export function clearChatFiles(sessionId: Id) {
  picked.delete(sessionId)
}
