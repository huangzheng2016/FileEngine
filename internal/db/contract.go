package db

import "time"

// Run phases.
const (
	RunPhaseSurvey    = "survey"
	RunPhaseTagging   = "tagging"
	RunPhaseReview    = "review"
	RunPhaseChat      = "chat"
	RunPhaseExecuting = "executing"
)

// Run statuses.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunPartial = "partial"
	RunError   = "error"
	RunStopped = "stopped"
)

// AgentRun is one agent or executor activity of a session. Token totals of a
// session are the sum over its runs.
type AgentRun struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	SessionID        uint       `gorm:"index:idx_run_session,priority:1" json:"session_id"`
	Phase            string     `gorm:"size:16" json:"phase"`
	Status           string     `gorm:"size:16;index" json:"status"`
	Title            string     `gorm:"size:512" json:"title"`
	Summary          string     `gorm:"type:text" json:"summary"`
	Error            string     `gorm:"type:text" json:"error"`
	PromptTokens     int        `json:"prompt_tokens"`
	CachedTokens     int        `json:"cached_tokens"`
	CompletionTokens int        `json:"completion_tokens"`
	TotalTokens      int        `json:"total_tokens"`
	StartedAt        time.Time  `gorm:"index:idx_run_session,priority:2" json:"started_at"`
	EndedAt          *time.Time `json:"ended_at"`
}

// SessionEvent is one persisted stream event; ID is the stream sequence number
// used for SSE ids and Last-Event-ID resume.
type SessionEvent struct {
	ID        uint      `gorm:"primaryKey" json:"seq"`
	SessionID uint      `gorm:"index:idx_event_session_run,priority:1" json:"session_id"`
	RunID     uint      `gorm:"index:idx_event_session_run,priority:2" json:"run_id"`
	Type      string    `gorm:"size:32" json:"type"`
	Data      string    `gorm:"type:text" json:"data"`
	CreatedAt time.Time `json:"created_at"`
}

// Plan statuses.
const (
	PlanDraft    = "draft"
	PlanApproved = "approved"
)

// SessionPlan is the session's 整理方案: the Markdown spec every worker follows.
type SessionPlan struct {
	SessionID  uint       `gorm:"primaryKey;autoIncrement:false" json:"session_id"`
	Version    int        `json:"version"`
	Status     string     `gorm:"size:16" json:"status"`
	Content    string     `gorm:"type:text" json:"content"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ApprovedAt *time.Time `json:"approved_at"`
}

// Memory scopes.
const (
	MemoryScopeSession    = "session"
	MemoryScopeFilesystem = "filesystem"
	MemoryScopeGlobal     = "global"
)

// Memory kinds.
const (
	MemoryPreference = "preference"
	MemoryConvention = "convention"
	MemoryCorrection = "correction"
	MemoryFact       = "fact"
	MemoryNote       = "note"
)

// Memory sources.
const (
	MemorySourceUser       = "user"
	MemorySourceAgent      = "agent"
	MemorySourceCorrection = "correction"
	MemorySourceReflection = "reflection"
)

// MemoryItem is one remembered fact. Session-scoped items are working notes
// shared live by the session's workers; filesystem and global items persist
// across sessions.
type MemoryItem struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Scope      string     `gorm:"size:16;index:idx_memory_scope,priority:1" json:"scope"`
	ScopeID    uint       `gorm:"index:idx_memory_scope,priority:2" json:"scope_id"`
	Kind       string     `gorm:"size:16" json:"kind"`
	Key        string     `gorm:"size:512;index" json:"key"` // normalized subject (e.g. "ext:.psd", "path:照片/2023"); one live item per (scope, scope_id, key)
	Content    string     `gorm:"type:text" json:"content"`
	Path       string     `gorm:"size:1024" json:"path"`
	Source     string     `gorm:"size:16" json:"source"`
	Pinned     bool       `json:"pinned"`
	Hits       int        `json:"hits"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ChatTurn is one persisted turn of the session assistant conversation.
type ChatTurn struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SessionID uint      `gorm:"index" json:"session_id"`
	RunID     uint      `json:"run_id"`
	Role      string    `gorm:"size:16" json:"role"` // user / assistant
	Content   string    `gorm:"type:text" json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Stream event types.
const (
	EvRunStatus     = "run_status"
	EvRunDone       = "run_done"
	EvPullBegin     = "pull_begin"
	EvPullDone      = "pull_done"
	EvMessage       = "message"
	EvText          = "text"
	EvReasoning     = "reasoning"
	EvToolCall      = "tool_call"
	EvToolResult    = "tool_result"
	EvProgress      = "progress"
	EvPlanUpdated   = "plan_updated"
	EvMemoryUpdated = "memory_updated"
)

type TokenCounts struct {
	Prompt     int `json:"prompt"`
	Cached     int `json:"cached"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

type RunStatusData struct {
	RunID  uint   `json:"run_id"`
	Phase  string `json:"phase"`
	Status string `json:"status"`
	Title  string `json:"title,omitempty"`
	Error  string `json:"error,omitempty"`
}

type RunDoneData struct {
	RunID   uint        `json:"run_id"`
	Phase   string      `json:"phase"`
	Status  string      `json:"status"`
	Summary string      `json:"summary,omitempty"`
	Error   string      `json:"error,omitempty"`
	Tokens  TokenCounts `json:"tokens"`
}

type PullBeginData struct {
	RunID    uint   `json:"run_id"`
	WorkerID int    `json:"worker_id"`
	UnitID   string `json:"unit_id"`
	Label    string `json:"label"`
	Count    int    `json:"count,omitempty"`
}

type PullDoneData struct {
	RunID    uint   `json:"run_id"`
	WorkerID int    `json:"worker_id"`
	UnitID   string `json:"unit_id"`
	Tagged   int    `json:"tagged"`
	Failed   int    `json:"failed"`
}

type MessageData struct {
	RunID    uint         `json:"run_id"`
	WorkerID int          `json:"worker_id"`
	MsgID    string       `json:"msg_id"`
	Role     string       `json:"role"` // user / assistant / system
	Content  string       `json:"content"`
	Tokens   *TokenCounts `json:"tokens,omitempty"`
}

// DeltaData is the payload of both text and reasoning events.
type DeltaData struct {
	RunID    uint   `json:"run_id"`
	WorkerID int    `json:"worker_id"`
	MsgID    string `json:"msg_id"`
	Delta    string `json:"delta"`
}

type ToolCallData struct {
	RunID    uint   `json:"run_id"`
	WorkerID int    `json:"worker_id"`
	CallID   string `json:"call_id"`
	Name     string `json:"name"`
	Args     any    `json:"args"`
}

type ToolResultData struct {
	RunID      uint   `json:"run_id"`
	WorkerID   int    `json:"worker_id"`
	CallID     string `json:"call_id"`
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	Output     any    `json:"output"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type ProgressData struct {
	RunID   uint   `json:"run_id"`
	Phase   string `json:"phase"`
	Total   int    `json:"total"`
	Tagged  int    `json:"tagged,omitempty"`
	Planned int    `json:"planned,omitempty"`
	Success int    `json:"success,omitempty"`
	Failed  int    `json:"failed,omitempty"`
	Skipped int    `json:"skipped,omitempty"`
}

type PlanUpdatedData struct {
	RunID   uint   `json:"run_id,omitempty"`
	Version int    `json:"version"`
	Status  string `json:"status"`
}

type MemoryUpdatedData struct {
	RunID uint   `json:"run_id,omitempty"`
	Scope string `json:"scope"`
	ID    uint   `json:"id"`
}

// Entry roles: every path has at most one effective decision — the nearest
// ancestor-or-self that is a unit or a file.
const (
	RoleUndecided = ""
	RoleUnit      = "unit"      // moved whole; descendants become members
	RoleContainer = "container" // not moved itself; children decided individually
	RoleMember    = "member"    // inside a unit, follows it
	RoleFile      = "file"      // loose file with its own decision
	RoleSkip      = "skip"      // out of scope, junk or explicitly left in place
	RoleDuplicate = "duplicate" // content duplicate of FileEntry.DuplicateOf (scanner); plan quarantines it, never deletes
)

// PlanRule is a deterministic classification rule applied in bulk to every
// undecided entry it matches; rules belong to the session plan and are
// re-applied after a rescan.
type PlanRule struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	SessionID uint   `gorm:"index" json:"session_id"`
	Position  int    `json:"position"` // lower runs first; first matching rule wins
	Name      string `gorm:"size:256" json:"name"`
	// Match conditions (all non-empty conditions must hold).
	Exts       string `gorm:"size:1024" json:"exts"` // comma-separated, lower-case, without dot
	NameRegex  string `gorm:"size:512" json:"name_regex"`
	PathPrefix string `gorm:"size:1024" json:"path_prefix"`
	FileType   string `gorm:"size:16" json:"file_type"` // file / directory / ""
	MinSize    int64  `json:"min_size"`
	MaxSize    int64  `json:"max_size"`
	// Target template, e.g. "照片/{year}/{month}/{name}". Variables: {name}
	// (base name), {stem}, {ext}, {year}, {month}, {day} (modification time),
	// {parent} (parent dir name), {rel} (path relative to path_prefix).
	Target    string    `gorm:"size:1024" json:"target"`
	Role      string    `gorm:"size:16" json:"role"`   // file or unit (directories)
	Source    string    `gorm:"size:16" json:"source"` // user / agent / preset
	Matched   int       `json:"matched"`               // entries decided by this rule in the last apply
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
