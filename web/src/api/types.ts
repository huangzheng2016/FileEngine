// Wire types come from the IDL (idl.gen.ts, generated). This file narrows the string fields that are enums
// and names the types pages use. Ids are decimal strings and "0" means none; times are unix seconds and 0
// means none (time.ts renders 0 as a dash).
import type * as idl from './idl.gen'

export type { Id } from './idl.gen'

type Narrow<T, M> = Omit<T, keyof M> & M

// "0" is an id that points at nothing; it is truthy, so test ids with hasId.
export const NO_ID: idl.Id = '0'
export const hasId = (id: idl.Id | null | undefined): id is idl.Id => !!id && id !== NO_ID

// Orders ids numerically without parsing them: int64 ids may exceed Number.MAX_SAFE_INTEGER.
export const cmpId = (a: idl.Id, b: idl.Id) => a.length - b.length || (a < b ? -1 : a > b ? 1 : 0)

export type SessionStatus = 'scanning' | 'scanned' | 'error' | 'interrupted'
export type RunPhase = 'survey' | 'tagging' | 'review' | 'chat' | 'executing' | 'revert'
export type RunStatus = 'running' | 'done' | 'partial' | 'error' | 'stopped'
export type ExecMode = 'copy' | 'move'
export type FilterMode = 'blacklist' | 'whitelist'
export type PlanStatus = 'draft' | 'approved'
export type MemoryScope = 'session' | 'filesystem' | 'global'
export type MemoryKind = 'preference' | 'convention' | 'correction' | 'fact' | 'note'
export type MemorySource = 'user' | 'agent' | 'correction' | 'reflection'
export type EntryRole = '' | 'unit' | 'container' | 'member' | 'file' | 'skip' | 'duplicate'
export type FileType = 'file' | 'directory' | 'symlink'
export type DateSource = '' | 'exif' | 'video' | 'filename' | 'mtime'
export type Protocol = 'local' | 'sftp' | 'ftp' | 'smb' | 'nfs'
export type ProviderKind = 'openai' | 'claude' | 'ollama'
export type RuleRole = 'file' | 'unit'
export type RuleSource = 'user' | 'agent' | 'preset'
export type PreviewSide = 'after' | 'before'
export type PreviewStatus = '' | 'moved' | 'kept' | 'partial'
export type IssueCode =
  | 'invalid_target'
  | 'into_self'
  | 'nested'
  | 'collision'
  | 'target_exists'
  | 'source_changed'
  | 'source_missing'
  | 'failed'
// journal state of the op that decided an entry; empty when it has none
export type OpState = '' | 'pending' | 'verified' | 'done' | 'failed' | 'reverted'

export type SessionEventKind =
  | 'run_status'
  | 'run_done'
  | 'pull_begin'
  | 'pull_done'
  | 'message'
  | 'text'
  | 'reasoning'
  | 'tool_call'
  | 'tool_result'
  | 'progress'
  | 'plan_updated'
  | 'memory_updated'
  // payload {}: stored session counters changed; refetch the session
  | 'session_updated'

export type RunRef = Narrow<idl.RunRef, { phase: RunPhase; status: RunStatus }>
export type TokenCounts = idl.TokenCounts

export type Session = Narrow<
  idl.Session,
  { status: SessionStatus; filter_mode: FilterMode; latest_run?: RunRef; tokens?: TokenCounts }
>

export type Run = Narrow<idl.Run, { phase: RunPhase; status: RunStatus; exec_mode: ExecMode | '' }>

// The wire event (idl.SessionEvent) keeps `data` as JSON text; the client parses it once, in the adapter.
export interface SessionEvent {
  seq: number
  run_id: idl.Id
  kind: SessionEventKind | (string & {})
  data: Record<string, unknown>
  created_at: number
}

export type SessionPlan = Narrow<idl.SessionPlan, { status: PlanStatus }>

export type MemoryItem = Narrow<idl.MemoryItem, { scope: MemoryScope; kind: MemoryKind; source: MemorySource }>

export type ChatTurn = Narrow<idl.ChatTurn, { role: 'user' | 'assistant' }>

export type FileEntry = Narrow<
  idl.FileEntry,
  {
    role: EntryRole
    file_type: FileType
    date_source: DateSource
    operation: '' | ExecMode
    execute_mode: '' | ExecMode
  }
>

export type TreeNode = idl.TreeNode
export type Category = idl.Category
export type CategoryTemplate = idl.CategoryTemplate
export type Filesystem = Narrow<idl.Filesystem, { protocol: Protocol }>
export type ModelProvider = Narrow<idl.ModelProvider, { provider: ProviderKind }>

export type PreviewItem = Narrow<idl.PreviewItem, { role: EntryRole; status: PreviewStatus }>
export type Issue = Narrow<idl.Issue, { code: IssueCode }>
export type ExecuteCheck = Narrow<idl.CheckExecuteResp, { issues: Issue[] }>
export type PlanOp = Narrow<idl.PlanOp, { operation: ExecMode }>
export type PlanRule = Narrow<
  idl.PlanRule,
  { file_type: '' | 'file' | 'directory'; role: RuleRole; source: RuleSource }
>
export type RuleApplyStat = idl.RuleApplyStat
export type LocateItem = Narrow<idl.LocateItem, { execute_mode: '' | ExecMode; op_state: OpState }>

export type AgentConfig = idl.AgentConfig
export type AgentConfigPatch = idl.AgentConfigPatch
export type AppConfig = Narrow<idl.ConfigResp, { agent: AgentConfig }>
export type PromptInfo = idl.PromptResp
export type ProbeDir = idl.ProbeDir
export type ProbeResult = idl.TestFilesystemResp
export type SessionStats = idl.SessionStatsResp
export type CategoryDistribution = idl.CategoryDistribution
