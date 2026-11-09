package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server" json:"server"`
	Database DatabaseConfig `yaml:"database" json:"database"`
	Agent    AgentConfig    `yaml:"agent" json:"agent"`
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

// Save always writes back to the loaded file, never to a CWD-relative guess.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = createDefault(path)
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	setDefaults(&cfg)
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
	setDefaults(&cfg)
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

func Path() string {
	mu.RLock()
	defer mu.RUnlock()
	return loadedPath
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

func UpdateAgentJSON(raw []byte) (*Config, error) {
	cur := Get()
	if cur == nil {
		return nil, errors.New("config not loaded")
	}
	next := *cur
	if err := json.Unmarshal(raw, &next.Agent); err != nil {
		return nil, err
	}
	Update(&next)
	return &next, nil
}

// 0600: it holds the API token and possibly a DSN password. Temp file +
// rename so a crash never leaves a truncated config.
func Save() error {
	mu.RLock()
	cfg, path := global, loadedPath
	mu.RUnlock()
	if cfg == nil || path == "" {
		return errors.New("config not loaded")
	}
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

// The caller prints the token once; it is shown nowhere else.
func EnsureToken() (token string, generated bool, err error) {
	cur := Get()
	if cur == nil {
		return "", false, errors.New("config not loaded")
	}
	if cur.Server.Token != "" {
		return cur.Server.Token, false, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", false, err
	}
	next := *cur
	next.Server.Token = base64.RawURLEncoding.EncodeToString(b)
	Update(&next)
	if err := Save(); err != nil {
		return "", false, err
	}
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

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
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
	if cfg.Agent.Concurrency == 0 {
		cfg.Agent.Concurrency = 6
	}
	if cfg.Agent.MaxFileReadSize == 0 {
		cfg.Agent.MaxFileReadSize = 8000
	}
	if cfg.Agent.ArchiveEntryMaxSize == 0 {
		cfg.Agent.ArchiveEntryMaxSize = 64 * 1024
	}
	if cfg.Agent.MaxRetries == 0 {
		cfg.Agent.MaxRetries = 3
	}
	if cfg.Agent.RateLimitMaxRetries <= 0 {
		cfg.Agent.RateLimitMaxRetries = 8
	}
	if cfg.Agent.RetryBaseDelay <= 0 {
		cfg.Agent.RetryBaseDelay = 2
	}
	if cfg.Agent.RetryMaxDelay <= 0 {
		cfg.Agent.RetryMaxDelay = 60
	}
	if cfg.Agent.RetryMaxDelay < cfg.Agent.RetryBaseDelay {
		cfg.Agent.RetryMaxDelay = cfg.Agent.RetryBaseDelay
	}
	if cfg.Agent.MaxStep == 0 {
		cfg.Agent.MaxStep = 150
	}
	if cfg.Agent.ChatMaxStep == 0 {
		cfg.Agent.ChatMaxStep = 50
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
		cfg.Agent.ScriptMaxResultSize = 8 * 1024
	}
	if cfg.Agent.ArchivePromptMaxEntries == 0 {
		cfg.Agent.ArchivePromptMaxEntries = 6
	}
	if cfg.Agent.CategoryPolicy == "" {
		cfg.Agent.CategoryPolicy = DefaultCategoryPolicy
	}
}
