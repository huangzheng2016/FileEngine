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
	BatchSize       int    `yaml:"batch_size" json:"batch_size"`
	Concurrency     int    `yaml:"concurrency" json:"concurrency"`
	MaxFileReadSize int    `yaml:"max_file_read_size" json:"max_file_read_size"`
	MaxRetries      int    `yaml:"max_retries" json:"max_retries"`
	MaxStep         int    `yaml:"max_step" json:"max_step"`
	InstructMaxStep int    `yaml:"instruct_max_step" json:"instruct_max_step"`
	SystemPrompt    string `yaml:"system_prompt" json:"system_prompt"`
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
}
