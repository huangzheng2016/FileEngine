// Package model is the hand-written schema. cmd/generate reads it to emit the
// typed query package in biz/dal/db/query, and db.Init migrates it at startup.
package model

import (
	"time"

	"gorm.io/gorm"

	"FileEngine/internal/config"
)

// All lists every table the app owns.
func All() []any {
	return []any{&FileEntry{}, &Category{}, &Filesystem{}, &ScanSession{}, &ModelProvider{},
		&AgentRun{}, &SessionEvent{}, &SessionPlan{}, &MemoryItem{}, &ChatTurn{}, &PlanRule{}, &ExecOp{}, &SettingsVersion{}}
}

type FileEntry struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	ScanSessionID uint       `gorm:"uniqueIndex:idx_uniq_session_path,priority:1;index:idx_session_hash,priority:1;index:idx_session_type_tagged_depth,priority:1;index:idx_session_parent,priority:1;index:idx_session_op_exec,priority:1;index:idx_session_newpath,priority:1;index:idx_session_newkey,priority:1;index:idx_session_role,priority:1" json:"scan_session_id"`
	OriginalPath  string     `gorm:"uniqueIndex:idx_uniq_session_path,priority:2;size:1024" json:"original_path"`
	NewPath       string     `gorm:"size:1024;index:idx_session_newpath,priority:2" json:"new_path"` // always "/"-rooted when set
	NewPathKey    string     `gorm:"size:1024;index:idx_session_newkey,priority:2" json:"-"`         // CollisionKey(NewPath), "" without a target; written with new_path
	Operation     string     `gorm:"size:16;index:idx_session_op_exec,priority:2" json:"operation"`  // "move" / "copy" / ""
	Role          string     `gorm:"size:16;index:idx_session_role,priority:2" json:"role"`          // decision role, see Role* in contract.go
	Reason        string     `gorm:"size:1024" json:"reason"`
	Confidence    float64    `json:"confidence"`                        // agent confidence 0-1; 0 = not given
	NeedsReview   bool       `gorm:"default:false" json:"needs_review"` // low-confidence decision: excluded from execution until the user confirms
	Sensitive     bool       `gorm:"default:false" json:"sensitive"`    // credentials / identity documents: never read by the agent
	Executed      bool       `gorm:"default:false;index:idx_session_op_exec,priority:3" json:"executed"`
	ExecutedAt    *time.Time `json:"executed_at"`
	ExecuteMode   string     `gorm:"size:16" json:"execute_mode"` // "copy" / "move"
	Name          string     `gorm:"size:512;index:idx_session_parent,priority:4" json:"name"`
	Size          int64      `json:"size"`
	ModTime       time.Time  `json:"mod_time"`
	Permissions   string     `gorm:"size:16" json:"permissions"`
	FileType      string     `gorm:"size:16;index:idx_session_type_tagged_depth,priority:2;index:idx_session_parent,priority:3" json:"file_type"` // "file" / "directory" / "symlink"
	Description   string     `gorm:"type:text" json:"description"`
	Tagged        bool       `gorm:"default:false;index:idx_session_type_tagged_depth,priority:3" json:"tagged"`
	ParentPath    string     `gorm:"size:1024;index:idx_session_parent,priority:2" json:"parent_path"`
	Depth         int        `gorm:"index:idx_session_type_tagged_depth,priority:4" json:"depth"`
	ChildCount    int        `json:"child_count"`  // direct children kept by the scan (directories only)
	SubdirCount   int        `json:"subdir_count"` // direct child directories (directories only)
	BatchIndex    int        `json:"batch_index"`
	// TakenAt is naive wall-clock time (stored as UTC) from EXIF/video headers,
	// else a date in the file name, else mtime.
	TakenAt    *time.Time `json:"taken_at"`
	Camera     string     `gorm:"size:128" json:"camera"`
	HasGPS     bool       `gorm:"default:false" json:"has_gps"`
	DateSource string     `gorm:"size:16" json:"date_source"` // exif / video / filename / mtime; "" = not extracted
	// Hash is set only for files that share their size with another file;
	// DuplicateOf points at the group's original.
	Hash        string    `gorm:"size:64;index:idx_session_hash,priority:2" json:"hash"`
	DuplicateOf *uint     `json:"duplicate_of"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Category struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	FilesystemID  uint      `gorm:"index;uniqueIndex:idx_fs_category_name,priority:1;uniqueIndex:idx_fs_category_path,priority:1" json:"filesystem_id"`
	Name          string    `gorm:"uniqueIndex:idx_fs_category_name,priority:2;size:256" json:"name"`
	Path          string    `gorm:"size:1024;uniqueIndex:idx_fs_category_path,priority:2" json:"path"`
	Structure     string    `gorm:"type:text" json:"structure"`
	Description   string    `gorm:"type:text" json:"description"`
	AgentCreated  bool      `gorm:"default:false" json:"agent_created"`
	AgentEditable bool      `gorm:"default:false" json:"agent_editable"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Filesystem struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Name          string    `gorm:"uniqueIndex;size:256" json:"name"`
	Description   string    `gorm:"type:text" json:"description"`
	Protocol      string    `gorm:"size:32" json:"protocol"` // local, sftp, ftp, smb, nfs
	BasePath      string    `gorm:"size:1024" json:"base_path"`
	Host          string    `gorm:"size:256" json:"host"`
	Port          int       `json:"port"`
	Username      string    `gorm:"size:256" json:"username"`
	Password      string    `gorm:"size:2048;serializer:secret" json:"-"` // AES-GCM encrypted at rest, never serialized
	PrivateKey    string    `gorm:"type:text;serializer:secret" json:"-"`
	KeyPassphrase string    `gorm:"size:2048;serializer:secret" json:"-"`
	HostKey       string    `gorm:"size:256" json:"host_key"` // SFTP: pinned server key fingerprint (SHA256:…), TOFU
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (f *Filesystem) ToRemoteFSConfig() config.RemoteFSConfig {
	return config.RemoteFSConfig{
		Protocol:      f.Protocol,
		BasePath:      f.BasePath,
		Host:          f.Host,
		Port:          f.Port,
		Username:      f.Username,
		Password:      f.Password,
		PrivateKey:    f.PrivateKey,
		KeyPassphrase: f.KeyPassphrase,
		HostKey:       f.HostKey,
	}
}

type ScanSession struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	FilesystemID uint   `gorm:"index" json:"filesystem_id"`
	ScanPath     string `gorm:"size:1024" json:"scan_path"`
	RootPath     string `gorm:"size:1024" json:"root_path"`
	Protocol     string `gorm:"size:32" json:"protocol"`
	Status       string `gorm:"size:32" json:"status"` // scanning / scanned / error / interrupted; agent activity lives on runs
	TotalFiles   int    `json:"total_files"`
	ScannedCount int    `json:"scanned_count"`
	CurrentPath  string `gorm:"size:1024" json:"current_path"`
	// Kept out of Status so the enum stays short and the full message survives.
	ErrorMessage string `gorm:"type:text" json:"error_message"`
	// Set on a successfully scanned session: what the walk had to skip.
	ScanWarning         string    `gorm:"type:text" json:"scan_warning"`
	TaggedFiles         int       `json:"tagged_files"`
	PlannedOps          int       `json:"planned_ops"`
	ReviewCount         int       `json:"review_count"` // planned, unexecuted entries waiting in needs_review
	ExecutedOps         int       `json:"executed_ops"`
	TotalSize           int64     `json:"total_size"`
	AllowReadFile       bool      `gorm:"default:true" json:"allow_read_file"`
	AllowAutoCategory   bool      `gorm:"default:true" json:"allow_auto_category"` // create_category switch; DB default on (GORM omits the zero value on insert)
	AllowVision         bool      `gorm:"default:false" json:"allow_vision"`       // session-level switch; view_image requires ModelProvider.VisionEnabled too
	AllowScript         bool      `gorm:"default:true" json:"allow_script"`        // run_script switch; DB default on (GORM omits the zero value on insert)
	ExtractMetadata     bool      `gorm:"default:true" json:"extract_metadata"`    // media metadata pass; DB default on (GORM omits the zero value on insert)
	DetectDuplicates    bool      `gorm:"default:false" json:"detect_duplicates"`  // size → partial hash → full hash duplicate grouping after the walk
	ExcludeCategoryDirs bool      `gorm:"default:false" json:"exclude_category_dirs"`
	FilterMode          string    `gorm:"size:16;default:blacklist" json:"filter_mode"`
	FilterDirs          string    `gorm:"type:text" json:"filter_dirs"`
	Goal                string    `gorm:"type:text" json:"goal"`
	ScopeExts           string    `gorm:"type:text" json:"scope_exts"` // comma-separated extensions to organize; empty = all files; others become role=skip
	ModelProviderID     uint      `gorm:"index" json:"model_provider_id"`
	BulkModelProviderID uint      `json:"bulk_model_provider_id"` // tagging workers + summarization; 0 = same as ModelProviderID
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ModelProvider struct {
	ID            uint    `gorm:"primaryKey" json:"id"`
	Name          string  `gorm:"uniqueIndex;size:256" json:"name"`
	Provider      string  `gorm:"size:32" json:"provider"` // openai / claude / ollama
	APIKey        string  `gorm:"size:2048;serializer:secret" json:"-"`
	Model         string  `gorm:"size:256" json:"model"`
	BaseURL       string  `gorm:"size:512" json:"base_url"`
	Temperature   float64 `json:"temperature"`
	MaxTokens     int     `json:"max_tokens"`     // output completion cap passed to the LLM API
	ContextWindow int     `json:"context_window"` // model context window in tokens (drives compaction thresholds)
	// Used only when ThinkingEnabled: openai-compatible APIs get it verbatim,
	// claude maps it to an extended-thinking budget.
	ReasoningEffort string    `gorm:"size:32" json:"reasoning_effort"`
	VisionEnabled   bool      `gorm:"default:false" json:"vision_enabled"`   // model can read images (gates the view_image tool)
	ThinkingEnabled bool      `gorm:"default:false" json:"thinking_enabled"` // request model thinking; reasoning_effort applies only when on
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

const DefaultContextWindow = 200_000

func (p *ModelProvider) EffectiveContextWindow() int {
	if p.ContextWindow > 0 {
		return p.ContextWindow
	}
	return DefaultContextWindow
}

// Token totals of a session are the sum over its runs.
type AgentRun struct {
	ID              uint   `gorm:"primaryKey" json:"id"`
	SessionID       uint   `gorm:"index:idx_run_session,priority:1" json:"session_id"`
	Phase           string `gorm:"size:16" json:"phase"`
	Status          string `gorm:"size:16;index" json:"status"`
	Title           string `gorm:"size:512" json:"title"`
	ExecMode        string `gorm:"size:16" json:"exec_mode,omitempty"`    // executing runs: copy / move
	RevertsRunID    *uint  `gorm:"index" json:"reverts_run_id,omitempty"` // set on a revert run
	RevertedByRunID *uint  `gorm:"-" json:"reverted_by_run_id,omitempty"` // derived from the revert run's reverts_run_id
	// Final counters of executing and revert runs, written when the run ends.
	ExecTotal        int        `json:"exec_total"`
	ExecSuccess      int        `json:"exec_success"`
	ExecFailed       int        `json:"exec_failed"`
	ExecSkipped      int        `json:"exec_skipped"`
	Summary          string     `gorm:"type:text" json:"summary"`
	Error            string     `gorm:"type:text" json:"error"`
	PromptTokens     int        `json:"prompt_tokens"`
	CachedTokens     int        `json:"cached_tokens"`
	CacheWriteTokens int        `json:"cache_write_tokens"`
	CacheHitRate     float64    `gorm:"-" json:"cache_hit_rate"` // cached / prompt, computed
	CompletionTokens int        `json:"completion_tokens"`
	TotalTokens      int        `json:"total_tokens"`
	StartedAt        time.Time  `gorm:"index:idx_run_session,priority:2" json:"started_at"`
	EndedAt          *time.Time `json:"ended_at"`
}

// ID is the stream sequence number used for SSE ids and Last-Event-ID resume.
type SessionEvent struct {
	ID        uint      `gorm:"primaryKey" json:"seq"`
	SessionID uint      `gorm:"index:idx_event_session_run,priority:1" json:"session_id"`
	RunID     uint      `gorm:"index:idx_event_session_run,priority:2" json:"run_id"`
	Type      string    `gorm:"size:32" json:"type"`
	Data      string    `gorm:"type:text" json:"data"`
	CreatedAt time.Time `json:"created_at"`
}

type SessionPlan struct {
	SessionID  uint       `gorm:"primaryKey;autoIncrement:false" json:"session_id"`
	Version    int        `json:"version"`
	Status     string     `gorm:"size:16" json:"status"`
	Content    string     `gorm:"type:text" json:"content"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ApprovedAt *time.Time `json:"approved_at"`
}

// Session-scoped items are working notes shared live by the session's
// workers; filesystem and global items persist across sessions.
type MemoryItem struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Scope     string    `gorm:"size:16;index:idx_memory_scope,priority:1" json:"scope"`
	ScopeID   uint      `gorm:"index:idx_memory_scope,priority:2" json:"scope_id"`
	Kind      string    `gorm:"size:16" json:"kind"`
	Key       string    `gorm:"size:512;index" json:"key"` // normalized subject (e.g. "ext:.psd"); one live item per (scope, scope_id, key)
	Content   string    `gorm:"type:text" json:"content"`
	Path      string    `gorm:"size:1024" json:"path"`
	Source    string    `gorm:"size:16" json:"source"`
	Pinned    bool      `json:"pinned"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatTurn struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	SessionID     uint      `gorm:"index" json:"session_id"`
	RunID         uint      `json:"run_id"`
	Role          string    `gorm:"size:16" json:"role"` // user / assistant / summary
	Content       string    `gorm:"type:text" json:"content"`
	CoversThrough uint      `json:"-"` // summary turns only
	CreatedAt     time.Time `json:"created_at"`
}

// Rules belong to the session plan and are re-applied after a rescan.
type PlanRule struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	SessionID uint   `gorm:"uniqueIndex:idx_rule_session_name,priority:1" json:"session_id"`
	Position  int    `json:"position"` // lower runs first; first matching rule wins
	Name      string `gorm:"size:256;uniqueIndex:idx_rule_session_name,priority:2" json:"name"`
	// Match conditions (all non-empty conditions must hold).
	Exts       string `gorm:"size:1024" json:"exts"` // comma-separated, lower-case, without dot
	NameRegex  string `gorm:"size:512" json:"name_regex"`
	PathPrefix string `gorm:"size:1024" json:"path_prefix"`
	FileType   string `gorm:"size:16" json:"file_type"` // file / directory / ""
	MinSize    int64  `json:"min_size"`
	MaxSize    int64  `json:"max_size"`
	// Target template variables: {name}, {stem}, {ext}, {year}, {month}, {day}
	// (modification time), {parent} (parent dir name), {rel} (path relative to
	// path_prefix).
	Target    string    `gorm:"size:1024" json:"target"`
	Role      string    `gorm:"size:16" json:"role"`   // file or unit (directories)
	Source    string    `gorm:"size:16" json:"source"` // user / agent / preset
	Matched   int       `json:"matched"`               // entries decided by this rule in the last apply
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ExecOp struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	RunID      uint      `gorm:"index" json:"run_id"`
	SessionID  uint      `gorm:"index:idx_execop_session_state,priority:1;index:idx_execop_session_dst,priority:1" json:"session_id"`
	EntryID    uint      `gorm:"index" json:"entry_id"` // FileEntry whose decision produced the op (0 for none)
	Kind       string    `gorm:"size:16" json:"kind"`
	Src        string    `gorm:"size:1024" json:"src"`
	Dst        string    `gorm:"size:1024;index:idx_execop_session_dst,priority:2" json:"dst"`
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`     // source size when the op started
	ModTime    time.Time `json:"mod_time"` // source mtime when the op started
	DstSize    int64     `json:"dst_size"` // dst stat after verification (revert checks it is unchanged)
	DstModTime time.Time `json:"dst_mod_time"`
	// DstCreated is journaled once the backend created Dst for this op. Only
	// then may recovery delete a half-written Dst; without it a file at Dst
	// may belong to someone else.
	DstCreated bool      `json:"dst_created"`
	State      string    `gorm:"size:16;index:idx_execop_session_state,priority:2;index:idx_execop_session_dst,priority:3" json:"state"`
	Error      string    `gorm:"type:text" json:"error"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (r *AgentRun) AfterFind(*gorm.DB) error {
	r.FillCacheHitRate()
	return nil
}

func (r *AgentRun) FillCacheHitRate() {
	r.CacheHitRate = 0
	if r.PromptTokens > 0 {
		r.CacheHitRate = float64(r.CachedTokens) / float64(r.PromptTokens)
	}
}

// SettingsVersion is one saved version of the runtime settings document
// (internal/config.AgentConfig as JSON). Versions are append-only; the unique
// index lets exactly one of two concurrent saves from the same base through.
type SettingsVersion struct {
	ID        uint   `gorm:"primaryKey"`
	Version   int64  `gorm:"uniqueIndex"`
	Data      string `gorm:"type:text"`
	CreatedAt time.Time
}
