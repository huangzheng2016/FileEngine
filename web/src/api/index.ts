import axios from 'axios'
import type { FileEntry, Filesystem, Category, ScanSession, AgentLog, TreeNode, PlanItem, Config, PageResult } from '../types'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
})

// Sessions
export const createSession = (data: { filesystem_id: number; scan_path?: string }) =>
  api.post<ScanSession>('/sessions', data)

export const listSessions = (filesystemId?: number) =>
  api.get<ScanSession[]>('/sessions', { params: filesystemId ? { filesystem_id: filesystemId } : {} })

export const getSession = (id: number) =>
  api.get<ScanSession>(`/sessions/${id}`)

export const deleteSession = (id: number) =>
  api.delete(`/sessions/${id}`)

export const rescanSession = (id: number) =>
  api.post(`/sessions/${id}/rescan`)

// Agent tasks
export const startTagging = (id: number) =>
  api.post(`/sessions/${id}/tag`)

export const stopTagging = (id: number) =>
  api.post(`/sessions/${id}/tag/stop`)

export const getTagStatus = (id: number) =>
  api.get(`/sessions/${id}/tag/status`)

export const startExecute = (id: number, mode: string = 'copy') =>
  api.post(`/sessions/${id}/execute`, null, { params: { mode } })

export const stopExecute = (id: number) =>
  api.post(`/sessions/${id}/execute/stop`)

export const getExecuteStatus = (id: number) =>
  api.get(`/sessions/${id}/execute/status`)

export const getPlans = (id: number) =>
  api.get<PlanItem[]>(`/sessions/${id}/plans`)

// Files
export const listFiles = (params: Record<string, any>) =>
  api.get<PageResult<FileEntry>>('/files', { params })

export const getFile = (id: number) =>
  api.get<FileEntry>(`/files/${id}`)

export const updateFile = (id: number, data: Partial<FileEntry>) =>
  api.patch<FileEntry>(`/files/${id}`, data)

export const getFileTree = (sessionId: number, parentPath: string) =>
  api.get<TreeNode[]>('/files/tree', { params: { session_id: sessionId, parent_path: parentPath } })

export const getFileContent = (id: number) =>
  api.get(`/files/${id}/content`, { responseType: 'blob' })

// Categories
export const listCategories = (filesystemId: number) =>
  api.get<Category[]>('/categories', { params: { filesystem_id: filesystemId } })

export const createCategory = (data: Partial<Category>) =>
  api.post<Category>('/categories', data)

export const updateCategory = (id: number, data: Partial<Category>) =>
  api.put<Category>(`/categories/${id}`, data)

export const deleteCategory = (id: number) =>
  api.delete(`/categories/${id}`)

// Filesystems
export const listFilesystems = () =>
  api.get<Filesystem[]>('/filesystems')

export const createFilesystem = (data: Partial<Filesystem>) =>
  api.post<Filesystem>('/filesystems', data)

export const getFilesystem = (id: number) =>
  api.get<Filesystem>(`/filesystems/${id}`)

export const updateFilesystem = (id: number, data: Partial<Filesystem>) =>
  api.put<Filesystem>(`/filesystems/${id}`, data)

export const deleteFilesystem = (id: number) =>
  api.delete(`/filesystems/${id}`)

export const testFilesystemConnection = (data: Partial<Filesystem>) =>
  api.post('/filesystems/test', data)

// Config
export const getConfig = () =>
  api.get<Config>('/config')

export const updateConfig = (data: Config) =>
  api.put<Config>('/config', data)

export const testModel = (data: Config['model']) =>
  api.post('/config/test-model', data)

// Logs
export const listLogs = (params: Record<string, any>) =>
  api.get<PageResult<AgentLog>>('/logs', { params })

export const listBatches = (sessionId: number, page: number = 1, pageSize: number = 50) =>
  api.get<{ batches: number[]; total: number }>('/logs/batches', { params: { session_id: sessionId, page, page_size: pageSize } })

// Prompt
export const getPrompt = () =>
  api.get<{ prompt: string; default_prompt: string; is_custom: boolean }>('/prompt')

export const updatePrompt = (prompt: string) =>
  api.put<{ prompt: string; is_custom: boolean }>('/prompt', { prompt })

export default api
