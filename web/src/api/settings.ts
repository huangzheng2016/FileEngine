import { http } from './http'
import type { Filesystem, ModelProvider, Protocol } from './types'

export interface FilesystemInput {
  name: string
  description: string
  protocol: Protocol
  base_path: string
  host: string
  port: number
  username: string
  // sent only when the user typed a new one
  password?: string
  key_path: string
  // SFTP fingerprint the user saw in a test; pins (or re-pins) the server key
  host_key?: string
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

export async function testFilesystem(body: FilesystemInput & { id?: number }) {
  const { data } = await http.post<{ success: boolean; error?: string; host_key?: string }>('/filesystems/test', body)
  return data
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
  const { data } = await http.post<{ success: boolean; reply?: string; error?: string }>('/models/test', body)
  return data
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
