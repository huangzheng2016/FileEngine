import { download, get, list, post } from './http'
import type {
  AgentConfigPatch,
  AppConfig,
  ConfigVersion,
  Filesystem,
  Id,
  ModelProvider,
  ProbeDir,
  PromptInfo,
  Protocol,
  ProviderKind,
} from './types'

export interface FilesystemConn {
  protocol: Protocol
  base_path: string
  host: string
  port: number
  username: string
  // secrets: omitted keeps the stored value, '' clears it
  password?: string
  private_key?: string
  key_passphrase?: string
  // SFTP fingerprint the user saw in a test; pins (or re-pins) the server key
  host_key?: string
}

export interface FilesystemInput extends FilesystemConn {
  name: string
  description: string
}

export interface ProbeResult {
  success: boolean
  // plain sentence with the next step; detail is the raw error
  error: string
  detail: string
  host_key: string
  host_key_changed: boolean
  // folders at base_path; path is the base_path value that opens one
  dirs: ProbeDir[]
}

export async function listFilesystems(signal?: AbortSignal) {
  const r = await get<{ items: Filesystem[] | null }>('/api/filesystem/list', undefined, signal)
  return list(r.items)
}

export async function getFilesystem(id: Id, signal?: AbortSignal) {
  const r = await get<{ item: Filesystem }>('/api/filesystem/get', { id }, signal)
  return r.item
}

export async function createFilesystem(body: FilesystemInput) {
  const r = await post<{ item: Filesystem }>('/api/filesystem/create', { ...body, id: '0' })
  return r.item
}

export async function updateFilesystem(id: Id, body: FilesystemInput) {
  const r = await post<{ item: Filesystem }>('/api/filesystem/update', { ...body, id })
  return r.item
}

export async function deleteFilesystem(id: Id) {
  await post<void>('/api/filesystem/delete', { id })
}

// Tests the connection and lists the folders at base_path.
export async function testFilesystem(body: FilesystemConn & { id?: Id }, signal?: AbortSignal) {
  const r = await post<ProbeResult & { dirs: ProbeDir[] | null }>(
    '/api/filesystem/test',
    { ...body, id: body.id ?? '0' },
    signal,
  )
  return { ...r, dirs: list(r.dirs) }
}

export interface ModelInput {
  name: string
  provider: ProviderKind
  // sent only when the user typed a new one
  api_key?: string
  model: string
  base_url: string
  temperature: number
  max_tokens: number
  context_window: number
  vision_enabled: boolean
  thinking_enabled: boolean
  reasoning_effort?: string
}

export async function listModels(signal?: AbortSignal) {
  const r = await get<{ items: ModelProvider[] | null }>('/api/model/list', undefined, signal)
  return list(r.items)
}

export async function getModel(id: Id, signal?: AbortSignal) {
  const r = await get<{ item: ModelProvider }>('/api/model/get', { id }, signal)
  return r.item
}

export async function createModel(body: ModelInput) {
  const r = await post<{ item: ModelProvider }>('/api/model/create', { ...body, id: '0' })
  return r.item
}

export async function updateModel(id: Id, body: ModelInput) {
  const r = await post<{ item: ModelProvider }>('/api/model/update', { ...body, id })
  return r.item
}

export async function deleteModel(id: Id) {
  await post<void>('/api/model/delete', { id })
}

export function testModel(body: ModelInput & { id?: Id }) {
  return post<{ success: boolean; reply: string; error: string; detail: string }>('/api/model/test', {
    ...body,
    id: body.id ?? '0',
  })
}

export interface CatalogInput {
  provider: ProviderKind
  base_url: string
  api_key?: string
  id?: Id
}

// The models the provider says this key can use.
export async function listProviderModels(body: CatalogInput, signal?: AbortSignal) {
  const r = await post<{ success: boolean; models: string[] | null; error: string; detail: string }>(
    '/api/model/catalog',
    { ...body, id: body.id ?? '0' },
    signal,
  )
  return { ...r, models: list(r.models) }
}

export function getConfig(signal?: AbortSignal) {
  return get<AppConfig>('/api/config/get', undefined, signal)
}

// A patch: fields left out stay unchanged. baseVersion is the version the edit started from; a newer saved
// version answers 409 (isConflict).
export function saveConfig(agent: AgentConfigPatch, baseVersion: number) {
  return post<AppConfig>('/api/config/update', { agent, base_version: baseVersion })
}

export function getPrompt(signal?: AbortSignal) {
  return get<PromptInfo>('/api/config/prompt', undefined, signal)
}

// A prompt equal to the default resets to it.
export function savePrompt(prompt: string, baseVersion: number) {
  return post<PromptInfo>('/api/config/prompt_update', { prompt, base_version: baseVersion })
}

// Newest first.
export async function listConfigVersions(page: number, pageSize: number, signal?: AbortSignal) {
  const r = await get<{ items: ConfigVersion[] | null; total: number }>(
    '/api/config/versions',
    { page, page_size: pageSize },
    signal,
  )
  return { items: list(r.items), total: r.total ?? 0 }
}

export function backupDatabase(filename: string) {
  return download('/api/admin/backup', { method: 'POST', filename })
}
