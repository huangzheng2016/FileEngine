package model

import (
	"context"
	"fmt"
	"net/http"

	"FileEngine/internal/db"

	claude "github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// NewChatModelFromProvider creates a model from a DB ModelProvider entity.
func NewChatModelFromProvider(ctx context.Context, p *db.ModelProvider) (model.ChatModel, error) {
	return newModel(ctx, p.Provider, p.APIKey, p.Model, p.BaseURL, p.Temperature, p.MaxTokens, p.ReasoningEffort)
}

func newModel(ctx context.Context, provider, apiKey, modelName, baseURL string, temperature float64, maxTokens int, reasoningEffort string) (model.ChatModel, error) {
	switch provider {
	case "openai":
		return newOpenAIModel(ctx, apiKey, modelName, baseURL, temperature, maxTokens, reasoningEffort)
	case "claude":
		return newClaudeModel(ctx, apiKey, modelName, baseURL, temperature, maxTokens)
	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434/v1"
		}
		return newOpenAIModel(ctx, apiKey, modelName, baseURL, temperature, maxTokens, reasoningEffort)
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", provider)
	}
}

func newOpenAIModel(ctx context.Context, apiKey, modelName, baseURL string, temperature float64, maxTokens int, reasoningEffort string) (model.ChatModel, error) {
	cfg := &openai.ChatModelConfig{
		Model:  modelName,
		APIKey: apiKey,
	}
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	// Inject the hint-capturing transport so error responses' status code and
	// Retry-After header reach the agent retry classifier via ResponseHints
	// (the eino/go-openai error types drop response headers). The client is
	// shared by all workers; the hints store travels per request context, so
	// concurrent requests never mix. Timeout stays zero, matching eino's
	// default client when ChatModelConfig.Timeout is unset.
	cfg.HTTPClient = &http.Client{Transport: newHintCaptureTransport(nil)}
	temp := float32(temperature)
	cfg.Temperature = &temp
	if maxTokens > 0 {
		cfg.MaxTokens = &maxTokens
	}
	if reasoningEffort != "" {
		cfg.ReasoningEffort = openai.ReasoningEffortLevel(reasoningEffort)
	}
	return openai.NewChatModel(ctx, cfg)
}

func newClaudeModel(ctx context.Context, apiKey, modelName, baseURL string, temperature float64, maxTokens int) (model.ChatModel, error) {
	cfg := &claude.Config{
		APIKey:    apiKey,
		Model:     modelName,
		MaxTokens: maxTokens,
	}
	if baseURL != "" {
		cfg.BaseURL = &baseURL
	}
	temp := float32(temperature)
	cfg.Temperature = &temp
	return claude.NewChatModel(ctx, cfg)
}
