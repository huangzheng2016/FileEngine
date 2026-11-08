// Wire shapes of the backend (internal/db/models.go, internal/db/contract.go).
// Times are ISO strings on the wire; pages format them through toSec + time.ts.

export type SessionStatus = 'scanning' | 'scanned' | 'error' | 'interrupted'

export interface ScanSession {
  id: number
  filesystem_id: number
  scan_path: string
  root_path: string
  protocol: string
  status: SessionStatus
  goal: string
  total_files: number
  scanned_count: number
  current_path: string
  error_message: string
  tagged_files: number
  planned_ops: number
  executed_ops: number
  total_size: number
  prompt_tokens: number
  cached_tokens: number
  completion_tokens: number
  total_tokens: number
  allow_read_file: boolean
  allow_auto_category: boolean
  allow_vision: boolean
  allow_script: boolean
  exclude_category_dirs: boolean
  filter_mode: 'blacklist' | 'whitelist'
  filter_dirs: string
  // comma-separated extensions without dot; '' = all files
  scope_exts: string
  extract_metadata: boolean
  detect_duplicates: boolean
  model_provider_id: number
  // 0 = same as model_provider_id
  bulk_model_provider_id: number
  created_at: string
  updated_at: string
}

export type RunPhase = 'survey' | 'tagging' | 'review' | 'chat' | 'executing'
export type RunStatus = 'running' | 'done' | 'partial' | 'error' | 'stopped'

export interface AgentRun {
  id: number
  session_id: number
  phase: RunPhase
  status: RunStatus
  title: string
  summary: string
  error: string
  prompt_tokens: number
  cached_tokens: number
  completion_tokens: number
  total_tokens: number
  started_at: string
  ended_at: string | null
}

export interface SessionEvent {
  seq: number
  type: string
  data: Record<string, unknown>
  created_at?: string
}

export interface TokenCounts {
  prompt: number
  cached: number
  completion: number
  total: number
}

export type PlanStatus = 'draft' | 'approved'

export interface SessionPlan {
  session_id: number
  version: number
  status: PlanStatus
  content: string
  updated_at: string
  approved_at: string | null
}

export type MemoryScope = 'session' | 'filesystem' | 'global'
export type MemoryKind = 'preference' | 'convention' | 'correction' | 'fact' | 'note'

export interface MemoryItem {
  id: number
  scope: MemoryScope
  scope_id: number
  kind: MemoryKind
  key: string
  content: string
  path: string
  source: 'user' | 'agent' | 'correction' | 'reflection'
  pinned: boolean
  hits: number
  last_used_at: string | null
  created_at: string
  updated_at: string
}

export interface ChatTurn {
  id: number
  run_id: number
  role: 'user' | 'assistant'
  content: string
  created_at: string
}

export interface FileEntry {
  id: number
  scan_session_id: number
  original_path: string
  new_path: string
  operation: string
  executed: boolean
  name: string
  size: number
  mod_time: string
  file_type: 'file' | 'directory' | 'symlink'
  role: EntryRole
  reason: string
  needs_review: boolean
  // 0-1, how sure the agent was
  confidence: number
  sensitive: boolean
  taken_at: string | null
  camera: string
  date_source: '' | 'exif' | 'filename' | 'mtime'
  duplicate_of: number | null
  subdir_count: number
  description: string
  tagged: boolean
  parent_path: string
  depth: number
  child_count: number
}

export interface TreeNode {
  id: number
  label: string
  original_path: string
  file_type: string
  child_count: number
  tagged: boolean
  is_leaf: boolean
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

export type Protocol = 'local' | 'sftp' | 'ftp' | 'smb' | 'nfs'

export interface Filesystem {
  id: number
  name: string
  description: string
  protocol: Protocol
  base_path: string
  host: string
  port: number
  username: string
  password_set: boolean
  key_path: string
  host_key: string
  created_at: string
  updated_at: string
}

export interface ModelProvider {
  id: number
  name: string
  provider: 'openai' | 'claude' | 'ollama'
  model: string
  base_url: string
  temperature: number
  max_tokens: number
  context_window: number
  vision_enabled: boolean
  thinking_enabled: boolean
  reasoning_effort: string
  api_key_set: boolean
  created_at: string
  updated_at: string
}

export interface PreviewItem {
  name: string
  path: string
  is_dir: boolean
  file_count: number
  size: number
  file_id?: number
  original_path?: string
  // before side only
  status?: 'moved' | 'kept' | 'partial'
  new_path?: string
  moved_count?: number
  role?: EntryRole
  reason?: string
  needs_review?: boolean
  confidence?: number
  sensitive?: boolean
}

export interface ValidationIssue {
  entry_id: number
  original_path: string
  new_path: string
  code: string
  detail: string
}

export interface ExecuteCheck {
  total: number
  success: number
  failed: number
  skipped: number
  issues: ValidationIssue[]
  notes?: string[]
}

export type EntryRole = '' | 'unit' | 'container' | 'member' | 'file' | 'skip' | 'duplicate'

export interface PlanRule {
  id: number
  session_id: number
  position: number
  name: string
  exts: string
  name_regex: string
  path_prefix: string
  file_type: '' | 'file' | 'directory'
  min_size: number
  max_size: number
  target: string
  role: 'file' | 'unit'
  source: 'user' | 'agent' | 'preset'
  matched: number
  created_at: string
  updated_at: string
}

export interface RulePreview {
  rule_id: number
  matched: number
  samples: { original_path: string; new_path: string }[]
}

export interface LocateResult {
  entry_id: number
  original_path: string
  name: string
  file_type: string
  new_path: string
  executed: boolean
  executed_at: string | null
  execute_mode: string
}
