package model

import (
	"context"
	"fmt"

	"FileEngine/internal/config"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino-ext/components/model/openai"
)

func NewChatModel(ctx context.Context, cfg config.ModelConfig) (model.ChatModel, error) {
	switch cfg.Provider {
	case "openai":
		return newOpenAIModel(ctx, cfg)
	case "claude":
		return newClaudeModel(ctx, cfg)
	case "ollama":
		return newOllamaModel(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", cfg.Provider)
	}
}

func newOpenAIModel(ctx context.Context, cfg config.ModelConfig) (model.ChatModel, error) {
	modelCfg := &openai.ChatModelConfig{
		Model:  cfg.Model,
		APIKey: cfg.APIKey,
	}
	if cfg.BaseURL != "" {
		modelCfg.BaseURL = cfg.BaseURL
	}
	temp := float32(cfg.Temperature)
	modelCfg.Temperature = &temp
	if cfg.MaxTokens > 0 {
		modelCfg.MaxTokens = &cfg.MaxTokens
	}
	return openai.NewChatModel(ctx, modelCfg)
}

func newClaudeModel(ctx context.Context, cfg config.ModelConfig) (model.ChatModel, error) {
	// Claude uses OpenAI-compatible endpoint with base_url pointing to claude proxy
	modelCfg := &openai.ChatModelConfig{
		Model:  cfg.Model,
		APIKey: cfg.APIKey,
	}
	if cfg.BaseURL != "" {
		modelCfg.BaseURL = cfg.BaseURL
	}
	temp := float32(cfg.Temperature)
	modelCfg.Temperature = &temp
	if cfg.MaxTokens > 0 {
		modelCfg.MaxTokens = &cfg.MaxTokens
	}
	return openai.NewChatModel(ctx, modelCfg)
}

func newOllamaModel(ctx context.Context, cfg config.ModelConfig) (model.ChatModel, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	modelCfg := &openai.ChatModelConfig{
		Model:   cfg.Model,
		BaseURL: baseURL,
	}
	temp := float32(cfg.Temperature)
	modelCfg.Temperature = &temp
	if cfg.MaxTokens > 0 {
		modelCfg.MaxTokens = &cfg.MaxTokens
	}
	return openai.NewChatModel(ctx, modelCfg)
}
