package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config is the process configuration read from config.yaml. Agent is not part
// of the file: it is the runtime settings document (biz/service/settings), which
// installs each saved version here with SetAgent for the agent packages to read.
type Config struct {
	Server   ServerConfig   `yaml:"server" json:"server"`
	Database DatabaseConfig `yaml:"database" json:"database"`
	Agent    AgentConfig    `yaml:"-" json:"agent"`
}

type ServerConfig struct {
	Port  int    `yaml:"port" json:"port"`
	Host  string `yaml:"host" json:"host"`
	Token string `yaml:"token" json:"-"`
}

type DatabaseConfig struct {
	Driver string `yaml:"driver" json:"driver"`
	DSN    string `yaml:"dsn" json:"-"` // may carry a password: never exposed through the API
}

type RemoteFSConfig struct {
	Protocol string
	BasePath string
	Host     string
	Port     int
	Username string
	Password string
	// PEM private key content; KeyPassphrase decrypts it when protected.
	PrivateKey    string
	KeyPassphrase string
	// SFTP connections refuse to start without a pinned key and fail on
	// mismatch.
	HostKey string
}

type AgentConfig struct {
	VisionMaxImagesPerBatch int    `yaml:"vision_max_images_per_batch" json:"vision_max_images_per_batch"` // max images per view_image call
	VisionImageMaxPx        int    `yaml:"vision_image_max_px" json:"vision_image_max_px"`                 // thumbnail long-edge px
	VisionMaxImageBytes     int    `yaml:"vision_max_image_bytes" json:"vision_max_image_bytes"`           // refuse reading larger source files
	Concurrency             int    `yaml:"concurrency" json:"concurrency"`                                 // tagging workers (default 6)
	MaxFileReadSize         int    `yaml:"max_file_read_size" json:"max_file_read_size"`                   // read_file page size in characters (default 8000)
	ArchiveEntryMaxSize     int    `yaml:"archive_entry_max_size" json:"archive_entry_max_size"`
	MaxRetries              int    `yaml:"max_retries" json:"max_retries"`
	RateLimitMaxRetries     int    `yaml:"rate_limit_max_retries" json:"rate_limit_max_retries"`
	RetryBaseDelay          int    `yaml:"retry_base_delay" json:"retry_base_delay"`
	RetryMaxDelay           int    `yaml:"retry_max_delay" json:"retry_max_delay"`
	MaxStep                 int    `yaml:"max_step" json:"max_step"`           // tool-call rounds of one agent run (one tagging unit, survey, review)
	ChatMaxStep             int    `yaml:"chat_max_step" json:"chat_max_step"` // tool-call rounds of one session assistant turn
	SystemPrompt            string `yaml:"system_prompt" json:"system_prompt"`
	// <= 0 means no timeout: a run is then bounded only by user stop and
	// max_step.
	AgentTimeout int `yaml:"agent_timeout" json:"agent_timeout"`
	// ScriptTimeout is deliberately independent of AgentTimeout: agent runs are
	// unlimited by default, scripts never are.
	ScriptMaxSize           int    `yaml:"script_max_size" json:"script_max_size"`               // max script source bytes
	ScriptTimeout           int    `yaml:"script_timeout" json:"script_timeout"`                 // seconds per script run
	ScriptMaxResultSize     int    `yaml:"script_max_result_size" json:"script_max_result_size"` // max bytes of print output and of return value, each
	JunkFilterDisabled      bool   `yaml:"junk_filter_disabled" json:"junk_filter_disabled"`
	ArchivePromptDisabled   bool   `yaml:"archive_prompt_disabled" json:"archive_prompt_disabled"`
	ArchivePromptMaxEntries int    `yaml:"archive_prompt_max_entries" json:"archive_prompt_max_entries"` // names per hint, default 6
	CategoryPolicy          string `yaml:"category_policy" json:"category_policy"`
}

const DefaultCategoryPolicy = `- 命名：分类用 2–6 个字的中文名词，路径与名称一致（照片 → /照片）。不用英文、拼音、日期、"其他""杂项"作分类名。
- 层级：分类最多两级（/文档、/文档/合同）。分类下只按年份、月份、软件名、版本再分，目标路径最多 3 层目录（/照片/2023/05/IMG_0012.jpg）。整体移动的文件夹内部保持原样，不计层数。
- 新建与复用：先用现有分类，近义的并入（相片、图片 → /照片）。现有分类都不合适、且至少有 10 个文件或一个完整文件夹要放时才新建。
- 照片、视频：按拍摄年份分目录，单年很多时再按月：/照片/2023/IMG_0012.jpg、/视频/2021/08/VID_0031.mp4。相册、活动文件夹整体放进对应年份：/照片/2023/云南旅行/。
- 文档：按用途分子分类（/文档/合同、/文档/发票、/文档/简历、/文档/学习资料），同一事务的文件放在一起，不按扩展名拆。
- 软件、安装包：按软件名建目录，各版本放在一起：/软件/Python/3.12.1/python-3.12.1-amd64.exe、/软件/Python/3.11.4/python-3.11.4-amd64.exe。
- 项目、代码：含 .git、package.json、go.mod 等标记的文件夹是一个整体，原样移动不拆分：/项目/blog-site/。
- 压缩包：能从名称或内容看出是什么的，跟同类文件放（照片压缩包进 /照片）；看不出的放 /压缩包。
- 重复：内容重复的文件由系统标记，只隔离不删除；同名但内容不同的文件都保留。
- 跳过：系统和程序生成的文件（缓存、临时文件、Thumbs.db、.DS_Store、@eaDir、回收站）保持原位，不整理。
- 兜底：看不出用途的放 /未分类，并交给用户确认。`

var (
	global     *Config
	loadedPath string
	mu         sync.RWMutex
)

// Load reads and validates the file; a missing file is created once with the
// explicit defaults. Unknown keys and invalid values fail startup.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = createDefault(path)
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	applyServerDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	cfg.Agent = DefaultAgent()
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	global = &cfg
	loadedPath = abs
	mu.Unlock()
	return &cfg, nil
}

func createDefault(path string) ([]byte, error) {
	var cfg Config
	applyServerDefaults(&cfg)
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	return data, f.Close()
}

// Optional keys: the listen address and the SQLite file. Everything a server
// cannot guess (a PostgreSQL or MySQL DSN) is required by validate.
func applyServerDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.DSN == "" && cfg.Database.Driver == "sqlite" {
		cfg.Database.DSN = "fileengine.db"
	}
}

func validate(cfg *Config) error {
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", cfg.Server.Port)
	}
	switch cfg.Database.Driver {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("database.driver must be sqlite, mysql or postgres, got %q", cfg.Database.Driver)
	}
	if cfg.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required for driver %s", cfg.Database.Driver)
	}
	return nil
}

func Path() string {
	mu.RLock()
	defer mu.RUnlock()
	return loadedPath
}

// Get returns the current snapshot; callers must not modify it. Every change
// installs a new snapshot, so a reader never sees a half-applied update.
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return global
}

// Update installs cfg as the current snapshot. Zero agent fields take their
// defaults, so tests and embedders can build a Config piecemeal.
func Update(cfg *Config) {
	if cfg != nil {
		fillAgentDefaults(&cfg.Agent)
	}
	mu.Lock()
	global = cfg
	mu.Unlock()
}

// SetAgent installs a saved settings version for the agent packages. The caller
// has already validated and persisted it.
func SetAgent(a AgentConfig) {
	mu.Lock()
	defer mu.Unlock()
	next := Config{}
	if global != nil {
		next = *global
	}
	next.Agent = a
	global = &next
}

// 0600: the file holds the API token and possibly a DSN password. Temp file +
// rename so a crash never leaves a truncated config.
func save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// The caller prints the token once; it is shown nowhere else. The new token is
// written before it becomes visible.
func EnsureToken() (token string, generated bool, err error) {
	mu.Lock()
	defer mu.Unlock()
	if global == nil || loadedPath == "" {
		return "", false, errors.New("config not loaded")
	}
	if global.Server.Token != "" {
		return global.Server.Token, false, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", false, err
	}
	next := *global
	next.Server.Token = base64.RawURLEncoding.EncodeToString(b)
	if err := save(&next, loadedPath); err != nil {
		return "", false, err
	}
	global = &next
	return next.Server.Token, true, nil
}

// Filesystem connections may never reach into it.
func DataDir(cfg *Config) string {
	if cfg.Database.Driver == "sqlite" && cfg.Database.DSN != "" && !strings.HasPrefix(cfg.Database.DSN, ":memory:") && !strings.HasPrefix(cfg.Database.DSN, "file:") {
		if abs, err := filepath.Abs(cfg.Database.DSN); err == nil {
			return filepath.Dir(abs)
		}
	}
	if p := Path(); p != "" {
		return filepath.Dir(p)
	}
	wd, _ := os.Getwd()
	return wd
}

// DefaultAgent is the agent document before the first save.
func DefaultAgent() AgentConfig {
	var a AgentConfig
	fillAgentDefaults(&a)
	return a
}

func fillAgentDefaults(a *AgentConfig) {
	if a.Concurrency == 0 {
		a.Concurrency = 6
	}
	if a.MaxFileReadSize == 0 {
		a.MaxFileReadSize = 8000
	}
	if a.ArchiveEntryMaxSize == 0 {
		a.ArchiveEntryMaxSize = 64 * 1024
	}
	if a.MaxRetries == 0 {
		a.MaxRetries = 3
	}
	if a.RateLimitMaxRetries <= 0 {
		a.RateLimitMaxRetries = 8
	}
	if a.RetryBaseDelay <= 0 {
		a.RetryBaseDelay = 2
	}
	if a.RetryMaxDelay <= 0 {
		a.RetryMaxDelay = 60
	}
	if a.RetryMaxDelay < a.RetryBaseDelay {
		a.RetryMaxDelay = a.RetryBaseDelay
	}
	if a.MaxStep == 0 {
		a.MaxStep = 150
	}
	if a.ChatMaxStep == 0 {
		a.ChatMaxStep = 50
	}
	if a.VisionMaxImagesPerBatch == 0 {
		a.VisionMaxImagesPerBatch = 4
	}
	if a.VisionImageMaxPx == 0 {
		a.VisionImageMaxPx = 512
	}
	if a.VisionMaxImageBytes == 0 {
		a.VisionMaxImageBytes = 20 * 1024 * 1024
	}
	if a.ScriptMaxSize == 0 {
		a.ScriptMaxSize = 16 * 1024
	}
	if a.ScriptTimeout == 0 {
		a.ScriptTimeout = 10
	}
	if a.ScriptMaxResultSize == 0 {
		a.ScriptMaxResultSize = 8 * 1024
	}
	if a.ArchivePromptMaxEntries == 0 {
		a.ArchivePromptMaxEntries = 6
	}
	if a.CategoryPolicy == "" {
		a.CategoryPolicy = DefaultCategoryPolicy
	}
}
