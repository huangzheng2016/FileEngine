package config

import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server" json:"server"`
	Database DatabaseConfig `yaml:"database" json:"database"`
	Agent    AgentConfig    `yaml:"agent" json:"agent"`
}

type ServerConfig struct {
	Port int    `yaml:"port" json:"port"`
	Host string `yaml:"host" json:"host"`
}

type DatabaseConfig struct {
	// Driver selects the SQL dialect: "sqlite" (default), "mysql" or
	// "postgres". The DSN format follows the driver: a file path for SQLite,
	// a go-sql-driver DSN for MySQL, a libpq keyword/value string (or
	// postgres:// URL) for PostgreSQL.
	Driver string `yaml:"driver" json:"driver"`
	DSN    string `yaml:"dsn" json:"dsn"`
}

// RemoteFSConfig is the internal representation used by remotefs.NewFromConfig().
type RemoteFSConfig struct {
	Protocol string
	BasePath string
	Host     string
	Port     int
	Username string
	Password string
	KeyPath  string
}

type AgentConfig struct {
	// Vision cost guardrails for the view_image tool (only reachable when both
	// ModelProvider.VisionEnabled and ScanSession.AllowVision are on).
	VisionMaxImagesPerBatch int `yaml:"vision_max_images_per_batch" json:"vision_max_images_per_batch"` // max images per view_image call
	VisionImageMaxPx        int `yaml:"vision_image_max_px" json:"vision_image_max_px"`                 // thumbnail long-edge px
	VisionMaxImageBytes     int `yaml:"vision_max_image_bytes" json:"vision_max_image_bytes"`           // refuse reading larger source files
	// BatchSize is the number of loose files per agent batch (default 200,
	// capped at 500). With the script bulk workflow (run_script: query_files →
	// batch set_category → mark_tagged loop) step count no longer scales with
	// files per batch, so large batches fit comfortably in the context window;
	// the prompt steers agents to that workflow for big batches. The per-file
	// fallback still costs ≥2 tool calls per file — batches it leaves behind
	// are caught by recheckBatchTagged and the end-of-run sweeps.
	BatchSize       int `yaml:"batch_size" json:"batch_size"`
	Concurrency     int `yaml:"concurrency" json:"concurrency"`
	MaxFileReadSize int `yaml:"max_file_read_size" json:"max_file_read_size"`
	// ArchiveEntryMaxSize caps which archive entries read_file will extract
	// inline via the path::entry syntax (larger entries are listing-only).
	ArchiveEntryMaxSize  int    `yaml:"archive_entry_max_size" json:"archive_entry_max_size"`
	MaxRetries           int    `yaml:"max_retries" json:"max_retries"`
	MaxStep              int    `yaml:"max_step" json:"max_step"` // tool-call rounds per agent run; script bulk mode decouples it from BatchSize
	InstructMaxStep      int    `yaml:"instruct_max_step" json:"instruct_max_step"`
	SystemPrompt         string `yaml:"system_prompt" json:"system_prompt"`
	InstructSystemPrompt string `yaml:"instruct_system_prompt" json:"instruct_system_prompt"`
	// AgentTimeout caps one full agent run (the whole ReAct loop) in seconds.
	// <= 0 or unset means no timeout — the default; a run is then bounded only
	// by user stop and max_step. Set a positive value to enforce an upper bound.
	AgentTimeout int `yaml:"agent_timeout" json:"agent_timeout"`
	// run_script guardrails (only reachable when ScanSession.AllowScript is on).
	// ScriptTimeout is per script execution and deliberately independent of
	// AgentTimeout: agent runs are unlimited by default, scripts never are.
	ScriptMaxSize       int `yaml:"script_max_size" json:"script_max_size"`               // max script source bytes
	ScriptTimeout       int `yaml:"script_timeout" json:"script_timeout"`                 // seconds per script run
	ScriptMaxResultSize int `yaml:"script_max_result_size" json:"script_max_result_size"` // max bytes of print output and of return value, each
	// JunkFilterDisabled turns off the default system-junk auto-ignore
	// (.DS_Store/Thumbs.db/desktop.ini/._*/@eaDir/__MACOSX/$RECYCLE.BIN …):
	// when false (default) such entries are marked tagged without an agent
	// batch — no tokens spent — and reported in the run reconciliation.
	JunkFilterDisabled bool `yaml:"junk_filter_disabled" json:"junk_filter_disabled"`
	// ArchivePromptDisabled turns off archive content hints in loose-file
	// batch prompts. When false (default), zip/tar files get a one-line
	// listing summary (kind + entry count + first few names) so the agent
	// classifies them without read_file calls.
	ArchivePromptDisabled   bool `yaml:"archive_prompt_disabled" json:"archive_prompt_disabled"`
	ArchivePromptMaxEntries int  `yaml:"archive_prompt_max_entries" json:"archive_prompt_max_entries"` // names per hint, default 6
}

var (
	global *Config
	mu     sync.RWMutex
)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	setDefaults(&cfg)
	mu.Lock()
	global = &cfg
	mu.Unlock()
	return &cfg, nil
}

func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return global
}

func Update(cfg *Config) {
	setDefaults(cfg)
	mu.Lock()
	global = cfg
	mu.Unlock()
}

func Save(path string) error {
	mu.RLock()
	cfg := global
	mu.RUnlock()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.DSN == "" {
		if cfg.Database.Driver == "postgres" {
			cfg.Database.DSN = "host=localhost port=5432 user=postgres dbname=fileengine sslmode=disable"
		} else {
			cfg.Database.DSN = "fileengine.db"
		}
	}
	if cfg.Agent.BatchSize == 0 {
		cfg.Agent.BatchSize = 200
	}
	if cfg.Agent.BatchSize > 500 {
		cfg.Agent.BatchSize = 500
	}
	if cfg.Agent.Concurrency == 0 {
		cfg.Agent.Concurrency = 1
	}
	if cfg.Agent.MaxFileReadSize == 0 {
		cfg.Agent.MaxFileReadSize = 102400
	}
	if cfg.Agent.ArchiveEntryMaxSize == 0 {
		cfg.Agent.ArchiveEntryMaxSize = 64 * 1024
	}
	if cfg.Agent.MaxRetries == 0 {
		cfg.Agent.MaxRetries = 3
	}
	if cfg.Agent.MaxStep == 0 {
		cfg.Agent.MaxStep = 150
	}
	if cfg.Agent.InstructMaxStep == 0 {
		cfg.Agent.InstructMaxStep = 30
	}
	if cfg.Agent.VisionMaxImagesPerBatch == 0 {
		cfg.Agent.VisionMaxImagesPerBatch = 4
	}
	if cfg.Agent.VisionImageMaxPx == 0 {
		cfg.Agent.VisionImageMaxPx = 512
	}
	if cfg.Agent.VisionMaxImageBytes == 0 {
		cfg.Agent.VisionMaxImageBytes = 20 * 1024 * 1024
	}
	if cfg.Agent.ScriptMaxSize == 0 {
		cfg.Agent.ScriptMaxSize = 16 * 1024
	}
	if cfg.Agent.ScriptTimeout == 0 {
		cfg.Agent.ScriptTimeout = 10
	}
	if cfg.Agent.ScriptMaxResultSize == 0 {
		cfg.Agent.ScriptMaxResultSize = 32 * 1024
	}
	if cfg.Agent.ArchivePromptMaxEntries == 0 {
		cfg.Agent.ArchivePromptMaxEntries = 6
	}
}
