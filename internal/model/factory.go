package model

import (
	"context"
	"fmt"

	"FileEngine/internal/db"

	"github.com/cloudwego/eino/components/model"
	claude "github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/openai"
)

// NewChatModelFromProvider creates a model from a DB ModelProvider entity.
func NewChatModelFromProvider(ctx context.Context, p *db.ModelProvider) (model.ChatModel, error) {
	return newModel(ctx, p.Provider, p.APIKey, p.Model, p.BaseURL, p.Temperature, p.MaxTokens)
}

func newModel(ctx context.Context, provider, apiKey, modelName, baseURL string, temperature float64, maxTokens int) (model.ChatModel, error) {
	switch provider {
	case "openai":
		return newOpenAIModel(ctx, apiKey, modelName, baseURL, temperature, maxTokens)
	case "claude":
		return newClaudeModel(ctx, apiKey, modelName, baseURL, temperature, maxTokens)
	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434/v1"
		}
		return newOpenAIModel(ctx, apiKey, modelName, baseURL, temperature, maxTokens)
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", provider)
	}
}

func newOpenAIModel(ctx context.Context, apiKey, modelName, baseURL string, temperature float64, maxTokens int) (model.ChatModel, error) {
	cfg := &openai.ChatModelConfig{
		Model:  modelName,
		APIKey: apiKey,
	}
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	temp := float32(temperature)
	cfg.Temperature = &temp
	if maxTokens > 0 {
		cfg.MaxTokens = &maxTokens
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
