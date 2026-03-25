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
  permissions: string
  file_type: string
  description: string
  tagged: boolean
  parent_path: string
  depth: number
  child_count: number
}

export interface Category {
  id: number
  filesystem_id: number
  name: string
  path: string
  structure: string
  description: string
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
  tagged_files: number
  planned_ops: number
  executed_ops: number
  total_size: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  allow_read_file: boolean
  allow_auto_category: boolean
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
  created_at: string
  updated_at: string
}

export interface AgentLog {
  id: number
  scan_session_id: number
  batch_index: number
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
  model: {
    provider: string
    api_key: string
    model: string
    base_url: string
    temperature: number
    max_tokens: number
  }
  agent: {
    batch_size: number
    concurrency: number
    max_file_read_size: number
    max_retries: number
    allow_auto_category: boolean
    allow_read_file: boolean
  }
}

export interface PageResult<T> {
  files?: T[]
  logs?: T[]
  total: number
}
