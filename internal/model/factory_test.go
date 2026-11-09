package model

import (
	"context"
	"testing"

	"FileEngine/internal/db"
)

func TestNewChatModelFromProvider_Construction(t *testing.T) {
	ctx := context.Background()
	cases := []db.ModelProvider{
		{Provider: "openai", APIKey: "k", Model: "m", BaseURL: "http://x/v1"},
		{Provider: "openai", APIKey: "k", Model: "m", BaseURL: "http://x/v1", ThinkingEnabled: true, ReasoningEffort: "low"},
		{Provider: "ollama", APIKey: "k", Model: "m", ThinkingEnabled: true, ReasoningEffort: "high"},
		{Provider: "claude", APIKey: "k", Model: "m", MaxTokens: 8192},
		{Provider: "claude", APIKey: "k", Model: "m", MaxTokens: 8192, ThinkingEnabled: true, ReasoningEffort: "medium"},
	}
	for i := range cases {
		if _, err := NewChatModelFromProvider(ctx, &cases[i]); err != nil {
			t.Errorf("case %d (%s/%q): %v", i, cases[i].Provider, cases[i].ReasoningEffort, err)
		}
	}
	if _, err := NewChatModelFromProvider(ctx, &db.ModelProvider{Provider: "bogus"}); err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestOpenAIThinkingParams(t *testing.T) {
	p := &db.ModelProvider{Model: "m", ReasoningEffort: "xhigh"}
	if got := openAIConfig(p, "").ReasoningEffort; got != "" {
		t.Fatalf("thinking off must omit reasoning_effort, got %q", got)
	}
	p.ThinkingEnabled = true
	if got := openAIConfig(p, "").ReasoningEffort; got != "xhigh" {
		t.Fatalf("thinking on: reasoning_effort = %q, want xhigh", got)
	}
	p.ReasoningEffort = ""
	if got := openAIConfig(p, "").ReasoningEffort; got != "" {
		t.Fatalf("empty effort must be omitted, got %q", got)
	}
}

func TestClaudeThinkingParams(t *testing.T) {
	off := claudeConfig(&db.ModelProvider{Model: "claude-sonnet-4-5", MaxTokens: 8192, ReasoningEffort: "high", Temperature: 0.3})
	if off.ThinkingConfig != nil || off.Temperature == nil || off.MaxTokens != 8192 {
		t.Fatalf("thinking off: thinking=%v temp=%v max=%d", off.ThinkingConfig, off.Temperature, off.MaxTokens)
	}

	cases := []struct {
		effort          string
		maxTokens       int
		budget, wantMax int
	}{
		{"low", 64000, 2048, 64000},
		{"medium", 64000, 8192, 64000},
		{"high", 64000, 16384, 64000},
		{"max", 64000, 32768, 64000},
		{"ultra", 64000, 32768, 64000},
		{"", 64000, 32768, 64000},
		{"high", 8192, 4096, 8192}, // capped below max_tokens
		{"low", 2000, 1024, 5120},  // max_tokens raised to fit the minimum budget
		{"medium", 0, 8192, 12288}, // unset max_tokens derived from the budget
	}
	for _, c := range cases {
		cfg := claudeConfig(&db.ModelProvider{Model: "claude-opus-4-1-20250805", MaxTokens: c.maxTokens, ThinkingEnabled: true, ReasoningEffort: c.effort})
		if cfg.ThinkingConfig == nil || cfg.ThinkingConfig.OfEnabled == nil {
			t.Fatalf("%q: thinking not enabled", c.effort)
		}
		if got := cfg.ThinkingConfig.OfEnabled.BudgetTokens; got != int64(c.budget) || cfg.MaxTokens != c.wantMax {
			t.Errorf("%q/%d: budget=%d max=%d, want %d/%d", c.effort, c.maxTokens, got, cfg.MaxTokens, c.budget, c.wantMax)
		}
		if cfg.ThinkingConfig.OfEnabled.BudgetTokens >= int64(cfg.MaxTokens) {
			t.Errorf("%q: budget must stay below max_tokens", c.effort)
		}
		if cfg.Temperature != nil {
			t.Errorf("%q: temperature must not be sent with thinking", c.effort)
		}
	}
}

func TestZeroTemperatureMeansModelDefault(t *testing.T) {
	if cfg := openAIConfig(&db.ModelProvider{Model: "m"}, ""); cfg.Temperature != nil {
		t.Fatalf("openai temperature 0 must not be sent: %v", *cfg.Temperature)
	}
	if cfg := claudeConfig(&db.ModelProvider{Model: "claude-3-7-sonnet-latest"}); cfg.Temperature != nil || cfg.MaxTokens != claudeDefaultMaxTokens {
		t.Fatalf("claude defaults: temperature=%v max_tokens=%d", cfg.Temperature, cfg.MaxTokens)
	}
	if cfg := openAIConfig(&db.ModelProvider{Model: "m", Temperature: 0.4}, ""); cfg.Temperature == nil || *cfg.Temperature != float32(0.4) {
		t.Fatal("non-zero temperature must be sent")
	}
}

func TestClaudeAdaptiveThinking(t *testing.T) {
	for _, name := range []string{"claude-3-7-sonnet-latest", "claude-opus-4-1-20250805", "claude-sonnet-4-5", "claude-haiku-4-5-20251001"} {
		if !claudeLegacyModel(name) {
			t.Errorf("%s must use budget thinking", name)
		}
	}
	for _, name := range []string{"claude-opus-5-5", "claude-sonnet-4-6", "claude-fable-5-1", "my-proxy-alias"} {
		if claudeLegacyModel(name) {
			t.Errorf("%s must use adaptive thinking", name)
		}
	}

	cfg := claudeConfig(&db.ModelProvider{Model: "claude-opus-5-5", ThinkingEnabled: true, ReasoningEffort: "ultra", Temperature: 0.3})
	if cfg.ThinkingConfig == nil || cfg.ThinkingConfig.OfAdaptive == nil || cfg.ThinkingConfig.OfEnabled != nil {
		t.Fatalf("thinking: %+v", cfg.ThinkingConfig)
	}
	if cfg.Temperature != nil || cfg.MaxTokens != claudeAdaptiveMaxTokens {
		t.Fatalf("temperature=%v max_tokens=%d", cfg.Temperature, cfg.MaxTokens)
	}
	if got := cfg.AdditionalRequestFields["output_config"].(map[string]any)["effort"]; got != "max" {
		t.Fatalf("effort = %v", got)
	}

	plain := claudeConfig(&db.ModelProvider{Model: "claude-sonnet-5-5", ReasoningEffort: "fast", Temperature: 0.3, MaxTokens: 4096})
	if plain.ThinkingConfig != nil || plain.Temperature != nil || plain.AdditionalRequestFields != nil || plain.MaxTokens != 4096 {
		t.Fatalf("thinking off: %+v", plain)
	}
}
