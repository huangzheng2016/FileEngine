package db

const (
	RunPhaseSurvey    = "survey"
	RunPhaseTagging   = "tagging"
	RunPhaseReview    = "review"
	RunPhaseChat      = "chat"
	RunPhaseExecuting = "executing"
	RunPhaseRevert    = "revert" // undoes one executing run (reverts_run_id)
)

const (
	RunRunning = "running"
	RunDone    = "done"
	RunPartial = "partial"
	RunError   = "error"
	RunStopped = "stopped"
)

const (
	PlanDraft    = "draft"
	PlanApproved = "approved"
)

const (
	MemoryScopeSession    = "session"
	MemoryScopeFilesystem = "filesystem"
	MemoryScopeGlobal     = "global"
)

const (
	MemoryPreference = "preference"
	MemoryConvention = "convention"
	MemoryCorrection = "correction"
	MemoryFact       = "fact"
	MemoryNote       = "note"
)

const (
	MemorySourceUser       = "user"
	MemorySourceAgent      = "agent"
	MemorySourceCorrection = "correction"
	MemorySourceReflection = "reflection"
)

// A summary turn condenses every turn with ID <= CoversThrough; it is prompt
// history only and never listed to the user.
const (
	ChatRoleUser      = "user"
	ChatRoleAssistant = "assistant"
	ChatRoleSummary   = "summary"
)

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
	// Payload {}: the session's stored counters changed; refetch the session.
	EvSessionUpdated = "session_updated"
)

type TokenCounts struct {
	Prompt     int `json:"prompt"`
	Cached     int `json:"cached"`
	CacheWrite int `json:"cache_write"`
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
	RoleDuplicate = "duplicate" // content duplicate of model.FileEntry.DuplicateOf (scanner); plan quarantines it, never deletes
)

const JunkBatchIndex = -1
