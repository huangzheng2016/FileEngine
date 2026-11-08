import { http } from './http'
import type { MemoryItem, MemoryKind, MemoryScope } from './types'

export async function listMemory(scope: MemoryScope | '', scopeId: number, signal?: AbortSignal) {
  const { data } = await http.get<{ items: MemoryItem[] | null }>('/memory', {
    params: { scope: scope || undefined, scope_id: scope && scope !== 'global' ? scopeId : undefined },
    signal,
  })
  return data.items ?? []
}

export interface MemoryInput {
  scope: MemoryScope
  scope_id: number
  kind: MemoryKind
  key: string
  content: string
  path: string
  pinned: boolean
}

export async function createMemory(body: MemoryInput) {
  const { data } = await http.post<MemoryItem>('/memory', body)
  return data
}

export async function updateMemory(id: number, body: Partial<MemoryInput>) {
  const { data } = await http.put<MemoryItem>(`/memory/${id}`, body)
  return data
}

export async function deleteMemory(id: number) {
  await http.delete(`/memory/${id}`)
}
