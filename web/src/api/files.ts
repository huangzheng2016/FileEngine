import { fetchBlob, get, list, post } from './http'
import type { Category, CategoryTemplate, FileEntry, Id, PreviewItem, PreviewSide, TreeNode } from './types'

export interface FileQuery {
  session_id: Id
  // present and empty = the children of the scan root; absent = no folder filter
  parent_path?: string
  search?: string
  category_path?: string
  needs_review?: boolean
  page: number
  size: number
}

export async function listFiles(q: FileQuery, signal?: AbortSignal) {
  const r = await get<{ items: FileEntry[] | null; total: number }>(
    '/api/file/list',
    {
      session_id: q.session_id,
      parent_path: q.search ? undefined : q.parent_path,
      search: q.search || undefined,
      category_path: q.category_path || undefined,
      needs_review: q.needs_review || undefined,
      page: q.page,
      page_size: q.size,
    },
    signal,
  )
  return { items: list(r.items), total: r.total }
}

export async function getFile(id: Id, signal?: AbortSignal) {
  const r = await get<{ item: FileEntry }>('/api/file/get', { id }, signal)
  return r.item
}

export async function getFileTree(sessionId: Id, parentPath: string, signal?: AbortSignal) {
  const r = await get<{ items: TreeNode[] | null }>(
    '/api/file/tree',
    { session_id: sessionId, parent_path: parentPath },
    signal,
  )
  return list(r.items)
}

// Absent fields stay unchanged; new_path is a user decision on the entry.
export async function updateFile(id: Id, patch: { description?: string; new_path?: string }) {
  const r = await post<{ item: FileEntry }>('/api/file/update', { ...patch, id })
  return r.item
}

export const updateFileTarget = (id: Id, newPath: string) => updateFile(id, { new_path: newPath })

export async function fileContent(id: Id, signal?: AbortSignal) {
  return (await fetchBlob('GET', '/api/file/content', { query: { id }, signal })).blob
}

export async function resetPlans(sessionId: Id, fileIds: Id[]) {
  const r = await post<{ reset: number }>('/api/plan/ops_reset', { session_id: sessionId, file_ids: fileIds })
  return r.reset
}

export const PREVIEW_LIMIT = 500

export async function previewTree(
  sessionId: Id,
  side: PreviewSide,
  path: string,
  signal?: AbortSignal,
): Promise<{ items: PreviewItem[]; total: number }> {
  const r = await get<{ items: PreviewItem[] | null; total: number }>(
    '/api/plan/preview',
    { session_id: sessionId, side, path, offset: 0, limit: PREVIEW_LIMIT },
    signal,
  )
  const items = list(r.items)
  return { items, total: r.total ?? items.length }
}

export async function listCategories(filesystemId: Id, signal?: AbortSignal) {
  const r = await get<{ items: Category[] | null }>('/api/category/list', { filesystem_id: filesystemId }, signal)
  return list(r.items)
}

export interface CategoryInput {
  filesystem_id: Id
  name: string
  path: string
  structure?: string
  description: string
}

export async function createCategory(body: CategoryInput) {
  const r = await post<{ item: Category }>('/api/category/create', { ...body, id: '0' })
  return r.item
}

export async function updateCategory(id: Id, body: CategoryInput) {
  const r = await post<{ item: Category }>('/api/category/update', { ...body, id })
  return r.item
}

export async function deleteCategory(id: Id) {
  await post<void>('/api/category/delete', { id })
}

export async function exportCategories(filesystemId: Id, signal?: AbortSignal) {
  const r = await get<{ items: CategoryTemplate[] | null }>(
    '/api/category/export',
    { filesystem_id: filesystemId },
    signal,
  )
  return list(r.items)
}

export function importCategories(
  filesystemId: Id,
  items: (Pick<CategoryTemplate, 'name' | 'path'> & Partial<CategoryTemplate>)[],
) {
  const full = items.map((c) => ({ structure: '', description: '', ...c }))
  return post<{ created: number; skipped: number }>('/api/category/import', {
    filesystem_id: filesystemId,
    items: full,
  })
}
