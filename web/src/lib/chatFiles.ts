import { reactive } from 'vue'

export interface ChatFile {
  id: number
  name: string
}

const picked = reactive(new Map<number, ChatFile[]>())

export function chatFiles(sessionId: number): ChatFile[] {
  return picked.get(sessionId) ?? []
}

export function addChatFiles(sessionId: number, files: ChatFile[]) {
  const cur = chatFiles(sessionId)
  const ids = new Set(cur.map((f) => f.id))
  picked.set(sessionId, [...cur, ...files.filter((f) => !ids.has(f.id))])
}

export function removeChatFile(sessionId: number, id: number) {
  picked.set(
    sessionId,
    chatFiles(sessionId).filter((f) => f.id !== id),
  )
}

export function clearChatFiles(sessionId: number) {
  picked.delete(sessionId)
}
