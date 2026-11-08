package db

import (
	"time"

	"FileEngine/internal/config"
)

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
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:256" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	Protocol    string    `gorm:"size:32" json:"protocol"` // local, sftp, ftp, smb, nfs
	BasePath    string    `gorm:"size:1024" json:"base_path"`
	Host        string    `gorm:"size:256" json:"host"`
	Port        int       `json:"port"`
	Username    string    `gorm:"size:256" json:"username"`
	Password    string    `gorm:"size:2048;serializer:secret" json:"-"` // AES-GCM encrypted at rest, never serialized
	KeyPath     string    `gorm:"size:1024" json:"key_path"`
	HostKey     string    `gorm:"size:256" json:"host_key"` // SFTP: pinned server key fingerprint (SHA256:…), TOFU
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (f *Filesystem) ToRemoteFSConfig() config.RemoteFSConfig {
	return config.RemoteFSConfig{
		Protocol: f.Protocol,
		BasePath: f.BasePath,
		Host:     f.Host,
		Port:     f.Port,
		Username: f.Username,
		Password: f.Password,
		KeyPath:  f.KeyPath,
		HostKey:  f.HostKey,
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
	ErrorMessage        string    `gorm:"type:text" json:"error_message"`
	TaggedFiles         int       `json:"tagged_files"`
	PlannedOps          int       `json:"planned_ops"`
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

const JunkBatchIndex = -1
