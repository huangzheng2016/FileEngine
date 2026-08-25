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
	VisionMaxImagesPerBatch int    `yaml:"vision_max_images_per_batch" json:"vision_max_images_per_batch"` // max images per view_image call
	VisionImageMaxPx        int    `yaml:"vision_image_max_px" json:"vision_image_max_px"`                 // thumbnail long-edge px
	VisionMaxImageBytes     int    `yaml:"vision_max_image_bytes" json:"vision_max_image_bytes"`           // refuse reading larger source files
	BatchSize               int    `yaml:"batch_size" json:"batch_size"`
	Concurrency             int    `yaml:"concurrency" json:"concurrency"`
	MaxFileReadSize         int    `yaml:"max_file_read_size" json:"max_file_read_size"`
	MaxRetries              int    `yaml:"max_retries" json:"max_retries"`
	MaxStep                 int    `yaml:"max_step" json:"max_step"`
	InstructMaxStep         int    `yaml:"instruct_max_step" json:"instruct_max_step"`
	SystemPrompt            string `yaml:"system_prompt" json:"system_prompt"`
	InstructSystemPrompt    string `yaml:"instruct_system_prompt" json:"instruct_system_prompt"`
	MaxCategorySummaryCount int    `yaml:"max_category_summary_count" json:"max_category_summary_count"`
	AgentTimeout            int    `yaml:"agent_timeout" json:"agent_timeout"`
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
		cfg.Database.DSN = "fileengine.db"
	}
	if cfg.Agent.BatchSize == 0 {
		cfg.Agent.BatchSize = 10
	}
	if cfg.Agent.Concurrency == 0 {
		cfg.Agent.Concurrency = 1
	}
	if cfg.Agent.MaxFileReadSize == 0 {
		cfg.Agent.MaxFileReadSize = 102400
	}
	if cfg.Agent.MaxRetries == 0 {
		cfg.Agent.MaxRetries = 3
	}
	if cfg.Agent.MaxStep == 0 {
		cfg.Agent.MaxStep = 50
	}
	if cfg.Agent.InstructMaxStep == 0 {
		cfg.Agent.InstructMaxStep = 30
	}
	if cfg.Agent.MaxCategorySummaryCount == 0 {
		cfg.Agent.MaxCategorySummaryCount = 20
	}
	if cfg.Agent.AgentTimeout == 0 {
		cfg.Agent.AgentTimeout = 60
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
}
