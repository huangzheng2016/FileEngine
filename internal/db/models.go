package db

import (
	"time"

	"FileEngine/internal/config"
)

type FileEntry struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	ScanSessionID uint      `gorm:"uniqueIndex:idx_uniq_session_path,priority:1;index:idx_session_type_tagged_depth,priority:1;index:idx_session_parent,priority:1;index:idx_session_op_exec,priority:1" json:"scan_session_id"`
	OriginalPath  string    `gorm:"uniqueIndex:idx_uniq_session_path,priority:2;size:1024" json:"original_path"`
	NewPath       string    `gorm:"size:1024" json:"new_path"`
	Operation     string    `gorm:"size:16;index:idx_session_op_exec,priority:2" json:"operation"` // "move" / "copy" / ""
	Executed      bool      `gorm:"default:false;index:idx_session_op_exec,priority:3" json:"executed"`
	Name          string    `gorm:"size:512" json:"name"`
	Size          int64     `json:"size"`
	ModTime       time.Time `json:"mod_time"`
	Permissions   string    `gorm:"size:16" json:"permissions"`
	FileType      string    `gorm:"size:16;index:idx_session_type_tagged_depth,priority:2;index:idx_session_parent,priority:3" json:"file_type"` // "file" / "directory" / "symlink"
	Description   string    `gorm:"type:text" json:"description"`
	Tagged        bool      `gorm:"default:false;index:idx_session_type_tagged_depth,priority:3;index:idx_session_parent,priority:4" json:"tagged"`
	ParentPath    string    `gorm:"size:1024;index:idx_session_parent,priority:2" json:"parent_path"`
	Depth         int       `gorm:"index:idx_session_type_tagged_depth,priority:4" json:"depth"`
	ChildCount    int       `json:"child_count"`
	BatchIndex    int       `json:"batch_index"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Category struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	FilesystemID   uint      `gorm:"index;uniqueIndex:idx_fs_category_name,priority:1" json:"filesystem_id"`
	Name           string    `gorm:"uniqueIndex:idx_fs_category_name,priority:2;size:256" json:"name"`
	Path           string    `gorm:"size:1024" json:"path"`
	Structure      string    `gorm:"type:text" json:"structure"`
	Description    string    `gorm:"type:text" json:"description"`
	AgentCreated   bool      `gorm:"default:false" json:"agent_created"`
	AgentEditable  bool      `gorm:"default:false" json:"agent_editable"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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
	ID           uint      `gorm:"primaryKey" json:"id"`
	FilesystemID uint      `gorm:"index" json:"filesystem_id"`
	ScanPath     string    `gorm:"size:1024" json:"scan_path"`
	RootPath     string    `gorm:"size:1024" json:"root_path"`
	Protocol     string    `gorm:"size:32" json:"protocol"`
	Status       string    `gorm:"size:32" json:"status"` // scanning/scanned/tagging/tagged/executing/done
	TotalFiles   int       `json:"total_files"`
	TaggedFiles  int       `json:"tagged_files"`
	PlannedOps   int       `json:"planned_ops"`
	ExecutedOps       int       `json:"executed_ops"`
	TotalSize         int64     `json:"total_size"`
	PromptTokens      int       `json:"prompt_tokens"`
	CompletionTokens  int       `json:"completion_tokens"`
	TotalTokens       int       `json:"total_tokens"`
	AllowReadFile       bool      `gorm:"default:true" json:"allow_read_file"`
	AllowAutoCategory   bool      `gorm:"default:false" json:"allow_auto_category"`
	ExcludeCategoryDirs bool      `gorm:"default:false" json:"exclude_category_dirs"`
	FilterMode          string    `gorm:"size:16;default:blacklist" json:"filter_mode"`
	FilterDirs          string    `gorm:"type:text" json:"filter_dirs"`
	ModelProviderID     uint      `gorm:"index" json:"model_provider_id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ModelProvider struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:256" json:"name"`
	Provider    string    `gorm:"size:32" json:"provider"` // openai / claude / ollama
	APIKey      string    `gorm:"size:512" json:"-"`
	Model       string    `gorm:"size:256" json:"model"`
	BaseURL     string    `gorm:"size:512" json:"base_url"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AgentLog struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	ScanSessionID uint      `gorm:"index:idx_log_session_batch,priority:1" json:"scan_session_id"`
	BatchIndex    int       `gorm:"index:idx_log_session_batch,priority:2" json:"batch_index"`
	Role          string    `gorm:"size:32" json:"role"` // "system" / "user" / "assistant" / "tool"
	ToolName      string    `gorm:"size:128" json:"tool_name"`
	ToolInput     string    `gorm:"type:text" json:"tool_input"`
	ToolOutput    string    `gorm:"type:text" json:"tool_output"`
	Content          string    `gorm:"type:text" json:"content"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CreatedAt        time.Time `json:"created_at"`
}
