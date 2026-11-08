import { http } from './http'
import type { Category, FileEntry, PreviewItem, TreeNode } from './types'

export interface FileQuery {
  session_id: number
  parent_path?: string
  search?: string
  category_path?: string
  tagged?: '' | 'true' | 'false'
  needs_review?: boolean
  page: number
  size: number
}

export async function listFiles(q: FileQuery, signal?: AbortSignal) {
  const { data } = await http.get<{ files: FileEntry[] | null; total: number }>('/files', {
    params: {
      session_id: q.session_id,
      parent_path: q.search ? undefined : q.parent_path,
      search: q.search || undefined,
      category_path: q.category_path || undefined,
      tagged: q.tagged || undefined,
      needs_review: q.needs_review || undefined,
      page: q.page,
      page_size: q.size,
    },
    signal,
  })
  return { items: data.files ?? [], total: data.total }
}

export async function getFileTree(sessionId: number, parentPath: string, signal?: AbortSignal) {
  const { data } = await http.get<TreeNode[] | null>('/files/tree', {
    params: { session_id: sessionId, parent_path: parentPath },
    signal,
  })
  return data ?? []
}

export async function updateFileTarget(id: number, newPath: string) {
  const { data } = await http.patch<FileEntry>(`/files/${id}`, { new_path: newPath })
  return data
}

export async function fileContent(id: number, signal?: AbortSignal) {
  const { data } = await http.get<Blob>(`/files/${id}/content`, { responseType: 'blob', signal })
  return data
}

export async function resetPlans(sessionId: number, fileIds: number[]) {
  const { data } = await http.post<{ reset: number }>(`/sessions/${sessionId}/plans/reset`, { file_ids: fileIds })
  return data.reset
}

export const PREVIEW_LIMIT = 500

export async function previewTree(
  sessionId: number,
  side: 'before' | 'after',
  path: string,
  signal?: AbortSignal,
): Promise<{ items: PreviewItem[]; total: number }> {
  const { data } = await http.get<{ items: PreviewItem[] | null; total: number }>(
    `/sessions/${sessionId}/preview/tree`,
    { params: { side, path, offset: 0, limit: PREVIEW_LIMIT }, signal },
  )
  const items = data.items ?? []
  return { items, total: data.total ?? items.length }
}

export async function listCategories(filesystemId: number, signal?: AbortSignal) {
  const { data } = await http.get<Category[] | null>('/categories', { params: { filesystem_id: filesystemId }, signal })
  return data ?? []
}

export interface CategoryInput {
  filesystem_id: number
  name: string
  path: string
  description: string
  agent_editable: boolean
}

export async function createCategory(body: CategoryInput) {
  const { data } = await http.post<Category>('/categories', body)
  return data
}

export async function updateCategory(id: number, body: CategoryInput) {
  const { data } = await http.put<Category>(`/categories/${id}`, body)
  return data
}

export async function deleteCategory(id: number) {
  await http.delete(`/categories/${id}`)
}

export async function importCategories(
  filesystemId: number,
  items: { name: string; path: string; description: string }[],
) {
  const { data } = await http.post<{ created: number; skipped: number }>('/categories/import', items, {
    params: { filesystem_id: filesystemId },
  })
  return data
}
