package db

import (
	"time"
	"unicode/utf8"

	"FileEngine/internal/config"
)

type FileEntry struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	ScanSessionID uint       `gorm:"uniqueIndex:idx_uniq_session_path,priority:1;index:idx_session_type_tagged_depth,priority:1;index:idx_session_parent,priority:1;index:idx_session_op_exec,priority:1" json:"scan_session_id"`
	OriginalPath  string     `gorm:"uniqueIndex:idx_uniq_session_path,priority:2;size:1024" json:"original_path"`
	NewPath       string     `gorm:"size:1024" json:"new_path"`
	Operation     string     `gorm:"size:16;index:idx_session_op_exec,priority:2" json:"operation"` // "move" / "copy" / ""
	Executed      bool       `gorm:"default:false;index:idx_session_op_exec,priority:3" json:"executed"`
	ExecutedAt    *time.Time `json:"executed_at"`
	ExecuteMode   string     `gorm:"size:16" json:"execute_mode"` // "copy" / "move"
	Name          string     `gorm:"size:512" json:"name"`
	Size          int64      `json:"size"`
	ModTime       time.Time  `json:"mod_time"`
	Permissions   string     `gorm:"size:16" json:"permissions"`
	FileType      string     `gorm:"size:16;index:idx_session_type_tagged_depth,priority:2;index:idx_session_parent,priority:3" json:"file_type"` // "file" / "directory" / "symlink"
	Description   string     `gorm:"type:text" json:"description"`
	Tagged        bool       `gorm:"default:false;index:idx_session_type_tagged_depth,priority:3;index:idx_session_parent,priority:4" json:"tagged"`
	ParentPath    string     `gorm:"size:1024;index:idx_session_parent,priority:2" json:"parent_path"`
	Depth         int        `gorm:"index:idx_session_type_tagged_depth,priority:4" json:"depth"`
	ChildCount    int        `json:"child_count"`
	BatchIndex    int        `json:"batch_index"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
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
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:256" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	Protocol    string    `gorm:"size:32" json:"protocol"` // local, sftp, ftp, smb, nfs
	BasePath    string    `gorm:"size:1024" json:"base_path"`
	Host        string    `gorm:"size:256" json:"host"`
	Port        int       `json:"port"`
	Username    string    `gorm:"size:256" json:"username"`
	Password    string    `gorm:"size:512" json:"-"` // excluded from default JSON
	KeyPath     string    `gorm:"size:1024" json:"key_path"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToRemoteFSConfig converts a Filesystem to RemoteFSConfig for remotefs factory.
func (f *Filesystem) ToRemoteFSConfig() config.RemoteFSConfig {
	return config.RemoteFSConfig{
		Protocol: f.Protocol,
		BasePath: f.BasePath,
		Host:     f.Host,
		Port:     f.Port,
		Username: f.Username,
		Password: f.Password,
		KeyPath:  f.KeyPath,
	}
}

type ScanSession struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	FilesystemID uint   `gorm:"index" json:"filesystem_id"`
	ScanPath     string `gorm:"size:1024" json:"scan_path"`
	RootPath     string `gorm:"size:1024" json:"root_path"`
	Protocol     string `gorm:"size:32" json:"protocol"`
	Status       string `gorm:"size:255" json:"status"` // scanning/scanned/tagging/tagged/tagged_partial/executing/done/error/interrupted (+ legacy "error:..." strings truncated via TruncateStatus)
	TotalFiles   int    `json:"total_files"`
	// ScannedCount is the live entries counter during "scanning": the scanner
	// persists it every few seconds so the UI shows progress before the
	// full-tree TotalFiles lands at the end.
	ScannedCount int `json:"scanned_count"`
	// CurrentPath is the directory the scan is listing right now (heartbeat);
	// empty when the walk is not running (saving results / terminal states).
	CurrentPath string `gorm:"size:1024" json:"current_path"`
	// ErrorMessage carries the failure detail when Status is "error" (or the
	// interruption note for "interrupted") — kept out of Status so the enum
	// stays short and the full message survives.
	ErrorMessage        string    `gorm:"type:text" json:"error_message"`
	TaggedFiles         int       `json:"tagged_files"`
	PlannedOps          int       `json:"planned_ops"`
	ExecutedOps         int       `json:"executed_ops"`
	TotalSize           int64     `json:"total_size"`
	PromptTokens        int       `json:"prompt_tokens"`
	CachedTokens        int       `json:"cached_tokens"` // cache-hit subset of prompt_tokens, as reported by the provider
	CompletionTokens    int       `json:"completion_tokens"`
	TotalTokens         int       `json:"total_tokens"`
	AllowReadFile       bool      `gorm:"default:true" json:"allow_read_file"`
	AllowAutoCategory   bool      `gorm:"default:true" json:"allow_auto_category"` // create_category tool switch; DB default ON (owner decision) — GORM omits the zero value on insert, so unset means on
	AllowVision         bool      `gorm:"default:false" json:"allow_vision"`       // session-level switch; view_image requires ModelProvider.VisionEnabled too
	AllowScript         bool      `gorm:"default:true" json:"allow_script"`        // run_script tool switch; DB default ON (owner decision) — GORM omits the zero value on insert, so unset means on
	ExcludeCategoryDirs bool      `gorm:"default:false" json:"exclude_category_dirs"`
	FilterMode          string    `gorm:"size:16;default:blacklist" json:"filter_mode"`
	FilterDirs          string    `gorm:"type:text" json:"filter_dirs"`
	Goal                string    `gorm:"type:text" json:"goal"` // the user's organizing intent, injected into every agent prompt
	ModelProviderID     uint      `gorm:"index" json:"model_provider_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ModelProvider struct {
	ID            uint    `gorm:"primaryKey" json:"id"`
	Name          string  `gorm:"uniqueIndex;size:256" json:"name"`
	Provider      string  `gorm:"size:32" json:"provider"` // openai / claude / ollama
	APIKey        string  `gorm:"size:512" json:"-"`
	Model         string  `gorm:"size:256" json:"model"`
	BaseURL       string  `gorm:"size:512" json:"base_url"`
	Temperature   float64 `json:"temperature"`
	MaxTokens     int     `json:"max_tokens"`     // output completion cap passed to the LLM API
	ContextWindow int     `json:"context_window"` // model context window in tokens (drives compaction thresholds)
	// ReasoningEffort is passed through to OpenAI-compatible APIs as
	// reasoning_effort: "" keeps the model default, otherwise any free-form
	// level the backend accepts (suggested none/low/medium/high/max; e.g. low
	// cuts kimi-k3 thinking tokens ~85% on tool-heavy runs). The API validates
	// charset/length ([a-zA-Z0-9_-]{1,32}) but never hard-rejects unknown
	// levels — providers keep inventing new ones.
	ReasoningEffort string    `gorm:"size:32" json:"reasoning_effort"`
	VisionEnabled   bool      `gorm:"default:false" json:"vision_enabled"`   // model can read images (gates the view_image tool)
	ThinkingEnabled bool      `gorm:"default:false" json:"thinking_enabled"` // request model thinking; reasoning_effort applies only when on
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DefaultContextWindow is the fallback context window (tokens) used when a
// ModelProvider does not specify one. It matches the smallest suggested tier
// (200K); common tiers are 200K/256K/500K/900K/1M, custom values allowed.
const DefaultContextWindow = 200_000

// EffectiveContextWindow returns the configured context window or the default.
func (p *ModelProvider) EffectiveContextWindow() int {
	if p.ContextWindow > 0 {
		return p.ContextWindow
	}
	return DefaultContextWindow
}

type AgentLog struct {
	ID            uint `gorm:"primaryKey" json:"id"`
	ScanSessionID uint `gorm:"index:idx_log_session_batch,priority:1" json:"scan_session_id"`
	BatchIndex    int  `gorm:"index:idx_log_session_batch,priority:2" json:"batch_index"`
	// WorkerID identifies the agent worker lane that produced the row (0 for
	// single-worker instruct runs and for rows written before lanes existed).
	// default:0 makes AutoMigrate backfill existing rows with 0 on all
	// dialects — a bare ADD COLUMN would leave NULLs that fail int scans.
	WorkerID         int       `gorm:"default:0" json:"worker_id"`
	Role             string    `gorm:"size:32" json:"role"` // "system" / "user" / "assistant" / "tool"
	ToolName         string    `gorm:"size:128" json:"tool_name"`
	ToolInput        string    `gorm:"type:text" json:"tool_input"`
	ToolOutput       string    `gorm:"type:text" json:"tool_output"`
	Content          string    `gorm:"type:text" json:"content"`
	PromptTokens     int       `json:"prompt_tokens"`
	CachedTokens     int       `json:"cached_tokens"` // cache-hit subset of PromptTokens; 0 on rows written before it existed
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CreatedAt        time.Time `json:"created_at"`
}

// StatusMaxLen is the storage limit for ScanSession.Status. Error-derived
// statuses ("error: ...") must be truncated with TruncateStatus before saving
// so MySQL strict mode does not reject over-long values.
const StatusMaxLen = 255

// JunkBatchIndex is the sentinel batch_index stamped on entries the autotag
// junk filter auto-ignored (system files like .DS_Store, @eaDir) without an
// agent batch. It distinguishes them from agent-tagged rows for reporting.
const JunkBatchIndex = -1

// TruncateStatus truncates s to StatusMaxLen runes for safe storage in
// ScanSession.Status.
func TruncateStatus(s string) string {
	if utf8.RuneCountInString(s) <= StatusMaxLen {
		return s
	}
	return string([]rune(s)[:StatusMaxLen])
}
