export interface FileEntry {
  id: number
  scan_session_id: number
  original_path: string
  new_path: string
  operation: string
  executed: boolean
  executed_at: string | null
  execute_mode: string
  name: string
  size: number
  mod_time: string
  permissions: string
  file_type: string
  description: string
  tagged: boolean
  parent_path: string
  depth: number
  child_count: number
  batch_index: number
}

export interface Category {
  id: number
  filesystem_id: number
  name: string
  path: string
  structure: string
  description: string
  agent_created: boolean
  agent_editable: boolean
}

export interface Filesystem {
  id: number
  name: string
  description: string
  protocol: string
  base_path: string
  host: string
  port: number
  username: string
  password?: string
  has_password: boolean
  key_path: string
  created_at: string
  updated_at: string
}

export interface ScanSession {
  id: number
  filesystem_id: number
  scan_path: string
  root_path: string
  protocol: string
  status: string
  total_files: number
  scanned_count: number
  current_path: string
  error_message: string
  tagged_files: number
  planned_ops: number
  executed_ops: number
  total_size: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  allow_read_file: boolean
  allow_auto_category: boolean
  allow_vision: boolean
  allow_script: boolean
  exclude_category_dirs: boolean
  filter_mode: string
  filter_dirs: string
  model_provider_id: number
  created_at: string
  updated_at: string
}

export interface ModelProvider {
  id: number
  name: string
  provider: string
  api_key: string
  model: string
  base_url: string
  temperature: number
  max_tokens: number
  context_window: number
  vision_enabled: boolean
  reasoning_effort: string
  created_at: string
  updated_at: string
}

export interface AgentLog {
  id: number
  scan_session_id: number
  batch_index: number
  worker_id: number
  role: string
  tool_name: string
  tool_input: string
  tool_output: string
  content: string
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  created_at: string
}

export interface TreeNode {
  id: number
  label: string
  original_path: string
  file_type: string
  child_count: number
  tagged: boolean
  is_leaf: boolean
  children?: TreeNode[]
}

export interface PlanItem {
  file_id: number
  original_path: string
  new_path: string
  operation: string
  name: string
  file_type: string
}

export interface Config {
  server: { port: number; host: string }
  database: { driver: string; dsn: string }
  agent: {
    batch_size: number
    concurrency: number
    max_file_read_size: number
    max_retries: number
  }
}

export interface PageResult<T> {
  files?: T[]
  logs?: T[]
  total: number
}

export interface ValidationIssue {
  original_path: string
  new_path: string
  issue: string
  detail: string
}

export interface ValidationResult {
  ok: boolean
  issues: ValidationIssue[]
}

export interface CategoryDistribution {
  name: string
  path: string
  file_count: number
  total_size: number
}

export interface SessionStats {
  category_distribution: CategoryDistribution[]
  total_files: number
  tagged_files: number
  planned_ops: number
  total_tokens: number
}

export interface CategoryExportItem {
  name: string
  path: string
  structure?: string
  description?: string
}

// ===== Session Event Stream (SSE) — mirrors internal/db/events.go payloads =====
// Live channel: GET /api/v1/sessions/:id/events/stream (EventSource).
// History stays in GET /logs; the stream carries only new events.

export type RunPhase = 'tagging' | 'instruct' | 'executing'
export type RunStatus = 'started' | 'done' | 'error' | 'stopped'

export interface RunStatusEvent {
  session_id: number
  phase: RunPhase
  status: RunStatus
  message?: string
}

export interface PullBeginEvent {
  session_id: number
  worker_id: number
  label: string
  count?: number
  cursor?: number
}

export interface PullDoneEvent {
  session_id: number
  worker_id: number
  label?: string
  tagged?: number
  failed?: number
}

export interface MessageEvent {
  session_id: number
  worker_id: number
  role: 'user' | 'assistant' | 'system'
  content: string
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
}

export interface ToolCallEvent {
  session_id: number
  worker_id: number
  tool_name: string
  input: string
}

export interface ToolResultEvent {
  session_id: number
  worker_id: number
  tool_name: string
  output: string
  ok: boolean
}

export interface ProgressEvent {
  session_id: number
  phase: RunPhase
  total: number
  tagged?: number
  planned?: number
  success?: number
  failed?: number
  skipped?: number
}

export interface RunDoneEvent {
  session_id: number
  phase: RunPhase
  status: 'done' | 'error' | 'stopped'
  summary?: string
}

export interface SessionEventMap {
  run_status: RunStatusEvent
  pull_begin: PullBeginEvent
  pull_done: PullDoneEvent
  message: MessageEvent
  tool_call: ToolCallEvent
  tool_result: ToolResultEvent
  progress: ProgressEvent
  run_done: RunDoneEvent
}

export type SessionEventType = keyof SessionEventMap
