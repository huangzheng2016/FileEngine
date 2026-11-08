package model

import (
	"context"
	"fmt"
	"net/http"

	"FileEngine/internal/db"

	"github.com/anthropics/anthropic-sdk-go"
	claude "github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func NewChatModelFromProvider(ctx context.Context, p *db.ModelProvider) (model.ChatModel, error) {
	switch p.Provider {
	case "openai":
		return newOpenAIModel(ctx, p, p.BaseURL)
	case "claude":
		return newClaudeModel(ctx, p)
	case "ollama":
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434/v1"
		}
		return newOpenAIModel(ctx, p, baseURL)
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", p.Provider)
	}
}

// Use it on the shared-context message that precedes per-unit data; system,
// tools and the rolling last message get breakpoints automatically.
func MarkCacheBreakpoint(msg *schema.Message) *schema.Message {
	return claude.SetMessageBreakpoint(msg)
}

func ResolveBulkProvider(repo *db.Repository, session *db.ScanSession, main *db.ModelProvider) (*db.ModelProvider, error) {
	if session == nil || session.BulkModelProviderID == 0 || session.BulkModelProviderID == main.ID {
		return main, nil
	}
	p, err := repo.GetModelProvider(session.BulkModelProviderID)
	if err != nil {
		return nil, fmt.Errorf("批量模型不存在: %w", err)
	}
	return p, nil
}

func newOpenAIModel(ctx context.Context, p *db.ModelProvider, baseURL string) (model.ChatModel, error) {
	return openai.NewChatModel(ctx, openAIConfig(p, baseURL))
}

func openAIConfig(p *db.ModelProvider, baseURL string) *openai.ChatModelConfig {
	cfg := &openai.ChatModelConfig{
		Model:  p.Model,
		APIKey: p.APIKey,
	}
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	// The hint-capturing transport surfaces error status codes and Retry-After
	// headers, which the eino/go-openai error types drop. The client is shared by
	// all workers; hints travel per request context, so they never mix.
	cfg.HTTPClient = &http.Client{Transport: newHintCaptureTransport(nil)}
	temp := float32(p.Temperature)
	cfg.Temperature = &temp
	if p.MaxTokens > 0 {
		maxTokens := p.MaxTokens
		cfg.MaxTokens = &maxTokens
	}
	if p.ThinkingEnabled && p.ReasoningEffort != "" {
		cfg.ReasoningEffort = openai.ReasoningEffortLevel(p.ReasoningEffort)
	}
	return cfg
}

func newClaudeModel(ctx context.Context, p *db.ModelProvider) (model.ChatModel, error) {
	return claude.NewChatModel(ctx, claudeConfig(p))
}

var claudeThinkingBudgets = map[string]int{"low": 2048, "medium": 8192, "high": 16384}

const (
	claudeMinBudget    = 1024 // API minimum budget_tokens
	claudeMaxBudget    = 32768
	claudeAnswerTokens = 4096 // output room kept above the thinking budget
)

// The budget is capped below maxTokens, and maxTokens is raised when it
// cannot hold the minimum budget plus an answer.
func claudeThinking(effort string, maxTokens int) (budget, newMaxTokens int) {
	budget, ok := claudeThinkingBudgets[effort]
	if !ok {
		budget = claudeMaxBudget
	}
	if maxTokens <= 0 {
		return budget, budget + claudeAnswerTokens
	}
	if budget > maxTokens-claudeAnswerTokens {
		budget = maxTokens - claudeAnswerTokens
	}
	if budget < claudeMinBudget {
		return claudeMinBudget, claudeMinBudget + claudeAnswerTokens
	}
	return budget, maxTokens
}

func claudeConfig(p *db.ModelProvider) *claude.Config {
	cfg := &claude.Config{
		APIKey:    p.APIKey,
		Model:     p.Model,
		MaxTokens: p.MaxTokens,
		// Prompt caching: cache breakpoints on system, tools and the last
		// message of each turn, so ReAct rounds re-read the shared prefix.
		AutoCacheControl: &claude.CacheControl{},
	}
	if p.BaseURL != "" {
		baseURL := p.BaseURL
		cfg.BaseURL = &baseURL
	}
	if p.ThinkingEnabled {
		// Extended thinking only accepts the default temperature, so none is sent.
		budget, maxTokens := claudeThinking(p.ReasoningEffort, p.MaxTokens)
		cfg.MaxTokens = maxTokens
		thinking := anthropic.ThinkingConfigParamOfEnabled(int64(budget))
		cfg.ThinkingConfig = &thinking
		return cfg
	}
	temp := float32(p.Temperature)
	cfg.Temperature = &temp
	return cfg
}
