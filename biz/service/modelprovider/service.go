// Package modelprovider manages the configured language models: CRUD, the
// in-use check on delete, the connection test and the provider's model catalog.
package modelprovider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	modelfactory "FileEngine/internal/model"

	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

const (
	// Otherwise a stored key could be sent to an arbitrary base URL.
	msgReenterAPIKey = "接口地址已变化，请重新输入 API Key"
	msgNoModel       = "模型不存在"
)

var errNoModel = errs.NotFound(msgNoModel)

var validProviders = map[string]bool{"openai": true, "claude": true, "ollama": true}

// Providers keep inventing levels, so only charset and length are checked.
var reasoningEffortPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

type Service struct {
	q  *query.Query
	rq *dal.Reader
}

func NewService(q *query.Query, rq *dal.Reader) *Service { return &Service{q: q, rq: rq} }

// SaveInput: nil optional fields keep the stored value on update and take the
// default on create.
type SaveInput struct {
	// 0 creates
	ID              uint
	Name            string
	Provider        string
	Model           string
	BaseURL         string
	Temperature     float64
	MaxTokens       int
	APIKey          *string
	ContextWindow   *int
	VisionEnabled   *bool
	ThinkingEnabled *bool
	ReasoningEffort *string
}

func (s *Service) List(ctx context.Context) ([]*model.ModelProvider, error) {
	m := s.rq.ModelProvider
	return m.WithContext(ctx).Order(m.ID).Find()
}

func (s *Service) Get(ctx context.Context, id uint) (*model.ModelProvider, error) {
	m := s.q.ModelProvider
	row, err := m.WithContext(ctx).Where(m.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNoModel
	}
	return row, err
}

func (s *Service) Create(ctx context.Context, in SaveInput) (*model.ModelProvider, error) {
	if err := validate(&in); err != nil {
		return nil, err
	}
	row := &model.ModelProvider{
		Name: in.Name, Provider: in.Provider, APIKey: deref(in.APIKey), Model: in.Model, BaseURL: in.BaseURL,
		Temperature: in.Temperature, MaxTokens: in.MaxTokens,
		ContextWindow:   pick(in.ContextWindow, model.DefaultContextWindow),
		ReasoningEffort: pick(in.ReasoningEffort, ""),
		VisionEnabled:   pick(in.VisionEnabled, false),
		ThinkingEnabled: pick(in.ThinkingEnabled, false),
	}
	if err := s.q.ModelProvider.WithContext(ctx).Create(row); errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil, errs.Conflict("名称已存在")
	} else if err != nil {
		return nil, err
	}
	return row, nil
}

func (s *Service) Update(ctx context.Context, in SaveInput) (*model.ModelProvider, error) {
	stored, err := s.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := validate(&in); err != nil {
		return nil, err
	}
	if in.APIKey == nil && stored.APIKey != "" && !sameEndpoint(stored, in.Provider, in.BaseURL) {
		return nil, errs.Invalid(msgReenterAPIKey)
	}
	next := &model.ModelProvider{
		Name: in.Name, Provider: in.Provider, APIKey: pick(in.APIKey, stored.APIKey), Model: in.Model, BaseURL: in.BaseURL,
		Temperature: in.Temperature, MaxTokens: in.MaxTokens,
		ContextWindow:   pick(in.ContextWindow, stored.ContextWindow),
		ReasoningEffort: pick(in.ReasoningEffort, stored.ReasoningEffort),
		VisionEnabled:   pick(in.VisionEnabled, stored.VisionEnabled),
		ThinkingEnabled: pick(in.ThinkingEnabled, stored.ThinkingEnabled),
	}
	m := s.q.ModelProvider
	_, err = m.WithContext(ctx).Where(m.ID.Eq(stored.ID)).
		Select(m.Name, m.Provider, m.APIKey, m.Model, m.BaseURL, m.Temperature, m.MaxTokens,
			m.ContextWindow, m.ReasoningEffort, m.VisionEnabled, m.ThinkingEnabled).
		Updates(next)
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil, errs.Conflict("名称已存在")
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, stored.ID)
}

// Delete refuses while tasks use the model; deleting a missing one succeeds.
func (s *Service) Delete(ctx context.Context, id uint) error {
	return s.q.Transaction(func(tx *query.Query) error {
		ss := tx.ScanSession
		n, err := ss.WithContext(ctx).Where(ss.ModelProviderID.Eq(id)).Or(ss.BulkModelProviderID.Eq(id)).Count()
		if err != nil {
			return err
		}
		if n > 0 {
			return errs.Conflict(fmt.Sprintf("有 %d 个整理任务在用这个模型，先删除这些任务", n))
		}
		_, err = tx.ModelProvider.WithContext(ctx).Where(tx.ModelProvider.ID.Eq(id)).Delete()
		return err
	})
}

type TestInput struct {
	// 0 tests unsaved settings; otherwise the stored key may be reused.
	ID              uint
	Provider        string
	Model           string
	BaseURL         string
	Temperature     float64
	MaxTokens       int
	APIKey          *string
	ThinkingEnabled *bool
	ReasoningEffort *string
	ContextWindow   *int
}

// TestResult: a failed test is a normal answer (Success false, Error set).
type TestResult struct {
	Success bool
	Reply   string
	Error   string
	Detail  string
}

func (s *Service) Test(ctx context.Context, in TestInput) (*TestResult, error) {
	if !validProviders[in.Provider] {
		return nil, errs.Invalid("不支持的模型提供方")
	}
	if in.ContextWindow != nil && *in.ContextWindow <= 0 {
		return nil, errs.Invalid("上下文长度需为正整数")
	}
	effort, err := resolveEffort(in.ReasoningEffort, "")
	if err != nil {
		return nil, err
	}
	apiKey, err := s.resolveKey(ctx, in.ID, in.APIKey, in.Provider, in.BaseURL)
	if err != nil {
		return nil, err
	}
	p := &model.ModelProvider{
		Provider: in.Provider, APIKey: apiKey, Model: in.Model, BaseURL: in.BaseURL,
		Temperature: in.Temperature, MaxTokens: in.MaxTokens,
		ThinkingEnabled: pick(in.ThinkingEnabled, false), ReasoningEffort: effort,
	}
	chat, err := modelfactory.NewChatModelFromProvider(ctx, p)
	if err != nil {
		return &TestResult{Error: "模型配置无效", Detail: err.Error()}, nil
	}
	ctx, hints := modelfactory.WithResponseHints(ctx)
	out, err := chat.Generate(ctx, []*schema.Message{{Role: schema.User, Content: "Hi"}})
	if err != nil {
		return &TestResult{Error: modelfactory.ExplainError(err, hints.StatusCode()), Detail: err.Error()}, nil
	}
	res := &TestResult{Success: true}
	if out != nil {
		res.Reply = out.Content
	}
	return res, nil
}

type CatalogInput struct {
	ID       uint
	Provider string
	BaseURL  string
	APIKey   *string
}

// CatalogResult: a failed listing is a normal answer (Success false, Error set).
type CatalogResult struct {
	Success bool
	Models  []string
	Error   string
	Detail  string
}

// Catalog asks the provider which models the key can use, so the form offers a
// list instead of a blank name field.
func (s *Service) Catalog(ctx context.Context, in CatalogInput) (*CatalogResult, error) {
	if !validProviders[in.Provider] {
		return nil, errs.Invalid("不支持的模型提供方")
	}
	apiKey, err := s.resolveKey(ctx, in.ID, in.APIKey, in.Provider, in.BaseURL)
	if err != nil {
		return nil, err
	}
	models, err := modelfactory.ListModels(ctx, in.Provider, in.BaseURL, apiKey)
	if err != nil {
		return &CatalogResult{Models: []string{}, Error: modelfactory.ExplainError(err, 0), Detail: err.Error()}, nil
	}
	return &CatalogResult{Success: true, Models: models}, nil
}

// resolveKey is the typed key, or the stored one of provider id when the
// endpoint is unchanged.
func (s *Service) resolveKey(ctx context.Context, id uint, typed *string, provider, baseURL string) (string, error) {
	if typed != nil || id == 0 {
		return deref(typed), nil
	}
	stored, err := s.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if stored.APIKey != "" && !sameEndpoint(stored, provider, baseURL) {
		return "", errs.Invalid(msgReenterAPIKey)
	}
	return stored.APIKey, nil
}

func sameEndpoint(m *model.ModelProvider, provider, baseURL string) bool {
	return m.Provider == provider && m.BaseURL == baseURL
}

func validate(in *SaveInput) error {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Model) == "" {
		return errs.Invalid("请输入名称和模型")
	}
	if !validProviders[in.Provider] {
		return errs.Invalid("不支持的模型提供方")
	}
	if in.ContextWindow != nil && *in.ContextWindow <= 0 {
		return errs.Invalid("上下文长度需为正整数")
	}
	if in.MaxTokens < 0 {
		return errs.Invalid("最大输出长度不能为负数")
	}
	_, err := resolveEffort(in.ReasoningEffort, "")
	return err
}

func resolveEffort(v *string, fallback string) (string, error) {
	if v == nil {
		return fallback, nil
	}
	if *v != "" && !reasoningEffortPattern.MatchString(*v) {
		return "", errs.Invalid("思考强度只能为空或 1-32 个字母、数字、_、-（如 low、medium、high、max）")
	}
	return *v, nil
}

func pick[T any](v *T, fallback T) T {
	if v == nil {
		return fallback
	}
	return *v
}

func deref(p *string) string { return pick(p, "") }
