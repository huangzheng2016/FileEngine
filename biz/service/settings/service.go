// Package settings holds the runtime settings the agent packages read: one
// document (config.AgentConfig, which includes the system prompt), saved as
// numbered, append-only versions. A save names the version it was edited from;
// if another version was saved since, it fails with a conflict instead of
// overwriting it. Order of a save: validate, persist, then swap the in-process
// snapshot, so a failed save never changes behaviour.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/internal/agent"
	"FileEngine/internal/config"

	"gorm.io/gorm"
)

var ErrConflict = errs.Conflict("设置已被其他人修改，请刷新后再保存")

type Snapshot struct {
	// 0 means never saved: Agent is config.DefaultAgent().
	Version int64
	Agent   config.AgentConfig
	SavedAt time.Time
}

type Version struct {
	Version int64
	SavedAt time.Time
	// JSON names of the settings that differ from the previous version.
	Changed []string
}

type Service struct {
	q *query.Query
	// Serializes save + swap so the installed snapshot is always the newest
	// saved one; the unique version index still guards other processes.
	mu sync.Mutex
}

// NewService installs the current saved version for the agent packages.
func NewService(q *query.Query) (*Service, error) {
	s := &Service{q: q}
	cur, err := s.Current(context.Background())
	if err != nil {
		return nil, err
	}
	config.SetAgent(cur.Agent)
	return s, nil
}

func (s *Service) Current(ctx context.Context) (Snapshot, error) {
	v := s.q.SettingsVersion
	// Limit(1).Find instead of First: "no saved version" is normal, not an error to log
	rows, err := v.WithContext(ctx).Order(v.Version.Desc()).Limit(1).Find()
	if err != nil {
		return Snapshot{}, err
	}
	if len(rows) == 0 {
		return Snapshot{Agent: config.DefaultAgent()}, nil
	}
	return decode(rows[0])
}

// Save applies change to the current settings and stores the result as the
// version after base. Saving unchanged values writes nothing.
func (s *Service) Save(ctx context.Context, base int64, change func(*config.AgentConfig)) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.Current(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if cur.Version != base {
		return Snapshot{}, ErrConflict
	}
	next := cur.Agent
	change(&next)
	if next.CategoryPolicy == "" {
		next.CategoryPolicy = config.DefaultCategoryPolicy
	}
	if err := validate(next); err != nil {
		return Snapshot{}, err
	}
	if reflect.DeepEqual(cur.Agent, next) {
		return cur, nil
	}
	data, err := json.Marshal(next)
	if err != nil {
		return Snapshot{}, err
	}
	row := &model.SettingsVersion{Version: base + 1, Data: string(data)}
	if err := s.q.SettingsVersion.WithContext(ctx).Create(row); errors.Is(err, gorm.ErrDuplicatedKey) {
		return Snapshot{}, ErrConflict
	} else if err != nil {
		return Snapshot{}, err
	}
	config.SetAgent(next)
	return Snapshot{Version: row.Version, Agent: next, SavedAt: row.CreatedAt}, nil
}

func DefaultPrompt() string { return agent.DefaultSystemPrompt }

// Prompt is the effective system prompt and whether it was customized.
func Prompt(a config.AgentConfig) (prompt string, custom bool) {
	if a.SystemPrompt == "" {
		return agent.DefaultSystemPrompt, false
	}
	return a.SystemPrompt, true
}

// SavePrompt stores prompt; one equal to the default resets to it.
func (s *Service) SavePrompt(ctx context.Context, base int64, prompt string) (Snapshot, error) {
	if prompt == agent.DefaultSystemPrompt {
		prompt = ""
	}
	return s.Save(ctx, base, func(a *config.AgentConfig) { a.SystemPrompt = prompt })
}

// Versions lists saved versions, newest first, with what each one changed.
func (s *Service) Versions(ctx context.Context, offset, limit int) ([]Version, int64, error) {
	v := s.q.SettingsVersion
	rows, total, err := v.WithContext(ctx).Order(v.Version.Desc()).FindByPage(offset, limit)
	if err != nil {
		return nil, 0, err
	}
	prevNums := make([]int64, 0, len(rows))
	for _, r := range rows {
		if r.Version > 1 {
			prevNums = append(prevNums, r.Version-1)
		}
	}
	prev := map[int64]*model.SettingsVersion{}
	if len(prevNums) > 0 {
		olds, err := v.WithContext(ctx).Where(v.Version.In(prevNums...)).Find()
		if err != nil {
			return nil, 0, err
		}
		for _, o := range olds {
			prev[o.Version] = o
		}
	}
	out := make([]Version, len(rows))
	for i, r := range rows {
		after, err := decode(r)
		if err != nil {
			return nil, 0, err
		}
		before := Snapshot{Agent: config.DefaultAgent()}
		if p := prev[r.Version-1]; p != nil {
			if before, err = decode(p); err != nil {
				return nil, 0, err
			}
		}
		out[i] = Version{Version: r.Version, SavedAt: r.CreatedAt, Changed: changedFields(before.Agent, after.Agent)}
	}
	return out, total, nil
}

func changedFields(a, b config.AgentConfig) []string {
	am, bm := toMap(a), toMap(b)
	out := []string{}
	for k, bv := range bm {
		if !reflect.DeepEqual(am[k], bv) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func toMap(a config.AgentConfig) map[string]any {
	raw, _ := json.Marshal(a)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func decode(row *model.SettingsVersion) (Snapshot, error) {
	// decoding over the defaults gives fields added after this version their default
	a := config.DefaultAgent()
	if err := json.Unmarshal([]byte(row.Data), &a); err != nil {
		return Snapshot{}, fmt.Errorf("settings version %d: %w", row.Version, err)
	}
	return Snapshot{Version: row.Version, Agent: a, SavedAt: row.CreatedAt}, nil
}

func within(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return errs.Invalid(fmt.Sprintf("%s需在 %d 到 %d 之间", name, lo, hi))
	}
	return nil
}

func validate(a config.AgentConfig) error {
	const (
		kb = 1024
		mb = 1024 * 1024
	)
	checks := []error{
		within("并发数", a.Concurrency, 1, 64),
		within("读取文件的分页大小", a.MaxFileReadSize, 500, 1_000_000),
		within("压缩包条目读取上限", a.ArchiveEntryMaxSize, kb, 16*mb),
		within("失败重试次数", a.MaxRetries, 0, 20),
		within("限流重试次数", a.RateLimitMaxRetries, 0, 50),
		within("重试初始等待秒数", a.RetryBaseDelay, 1, 600),
		within("重试最长等待秒数", a.RetryMaxDelay, 1, 3600),
		within("单次整理的工具调用轮数", a.MaxStep, 1, 1000),
		within("对话的工具调用轮数", a.ChatMaxStep, 1, 500),
		within("单次整理的超时秒数", a.AgentTimeout, -1, 86400),
		within("脚本大小上限", a.ScriptMaxSize, kb, mb),
		within("脚本超时秒数", a.ScriptTimeout, 1, 600),
		within("脚本输出上限", a.ScriptMaxResultSize, kb, mb),
		within("压缩包提示的条目数", a.ArchivePromptMaxEntries, 1, 100),
		within("每次查看的图片数", a.VisionMaxImagesPerBatch, 1, 16),
		within("图片缩略边长", a.VisionImageMaxPx, 64, 4096),
		within("图片大小上限", a.VisionMaxImageBytes, mb, 200*mb),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	if a.RetryMaxDelay < a.RetryBaseDelay {
		return errs.Invalid("重试最长等待秒数不能小于初始等待秒数")
	}
	if len([]rune(a.SystemPrompt)) > 50000 {
		return errs.Invalid("系统提示词不超过 50000 个字符")
	}
	if len([]rune(a.CategoryPolicy)) > 50000 {
		return errs.Invalid("分类规范不超过 50000 个字符")
	}
	return nil
}
