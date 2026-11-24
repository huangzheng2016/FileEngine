import { get, list, post } from './http'
import type { Id, MemoryItem, MemoryKind, MemoryScope } from './types'

export async function listMemory(scope: MemoryScope | '', scopeId: Id, signal?: AbortSignal) {
  const r = await get<{ items: MemoryItem[] | null }>(
    '/api/memory/list',
    { scope: scope || undefined, scope_id: scope && scope !== 'global' ? scopeId : undefined },
    signal,
  )
  return list(r.items)
}

export interface MemoryInput {
  scope: MemoryScope
  scope_id: Id
  kind: MemoryKind
  key: string
  content: string
  path: string
  pinned: boolean
}

// `created` is false when an item with the same key was updated instead.
export async function createMemory(body: MemoryInput) {
  const r = await post<{ item: MemoryItem; created: boolean }>('/api/memory/create', body)
  return r.item
}

export async function updateMemory(id: Id, body: Partial<Omit<MemoryInput, 'scope' | 'scope_id'>>) {
  const r = await post<{ item: MemoryItem }>('/api/memory/update', { ...body, id })
  return r.item
}

export async function deleteMemory(id: Id) {
  await post<void>('/api/memory/delete', { id })
}
