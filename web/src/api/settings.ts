import { http } from './http'
import type { Filesystem, ModelProvider, Protocol } from './types'

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
  error?: string
  detail?: string
  host_key?: string
  host_key_changed?: boolean
  // folders at base_path; path is the base_path value that opens one
  dirs: { name: string; path: string }[]
}

export async function listFilesystems(signal?: AbortSignal) {
  const { data } = await http.get<Filesystem[] | null>('/filesystems', { signal })
  return data ?? []
}

export async function createFilesystem(body: FilesystemInput) {
  const { data } = await http.post<Filesystem>('/filesystems', body)
  return data
}

export async function updateFilesystem(id: number, body: FilesystemInput) {
  const { data } = await http.put<Filesystem>(`/filesystems/${id}`, body)
  return data
}

export async function deleteFilesystem(id: number) {
  await http.delete(`/filesystems/${id}`)
}

// Tests the connection and lists the folders at base_path.
export async function testFilesystem(body: FilesystemConn & { id?: number }, signal?: AbortSignal) {
  const { data } = await http.post<ProbeResult>('/filesystems/test', body, { signal })
  return { ...data, dirs: data.dirs ?? [] }
}

export interface ModelInput {
  name: string
  provider: ModelProvider['provider']
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
  const { data } = await http.get<ModelProvider[] | null>('/models', { signal })
  return data ?? []
}

export async function createModel(body: ModelInput) {
  const { data } = await http.post<ModelProvider>('/models', body)
  return data
}

export async function updateModel(id: number, body: ModelInput) {
  const { data } = await http.put<ModelProvider>(`/models/${id}`, body)
  return data
}

export async function deleteModel(id: number) {
  await http.delete(`/models/${id}`)
}

export async function testModel(body: ModelInput & { id?: number }) {
  const { data } = await http.post<{ success: boolean; reply?: string; error?: string; detail?: string }>(
    '/models/test',
    body,
  )
  return data
}

export interface CatalogInput {
  provider: ModelProvider['provider']
  base_url: string
  api_key?: string
  id?: number
}

// The models the provider says this key can use.
export async function listProviderModels(body: CatalogInput, signal?: AbortSignal) {
  const { data } = await http.post<{ success: boolean; models: string[]; error?: string; detail?: string }>(
    '/models/catalog',
    body,
    { signal },
  )
  return { ...data, models: data.models ?? [] }
}

export interface AgentConfig {
  concurrency: number
  max_step: number
  max_retries: number
  rate_limit_max_retries: number
  max_file_read_size: number
  agent_timeout: number
  script_timeout: number
  category_policy: string
  system_prompt: string
  [key: string]: unknown
}

export interface AppConfig {
  server: Record<string, unknown>
  database: Record<string, unknown>
  agent: AgentConfig
  // read-only; agent.category_policy equal to it means not customized
  default_category_policy?: string
}

export async function getConfig(signal?: AbortSignal) {
  const { data } = await http.get<AppConfig>('/config', { signal })
  return data
}

export async function saveConfig(cfg: AppConfig) {
  const { data } = await http.put<AppConfig>('/config', cfg)
  return data
}

export async function getPrompt(signal?: AbortSignal) {
  const { data } = await http.get<{ prompt: string; default_prompt: string; is_custom: boolean }>('/prompt', {
    signal,
  })
  return data
}
