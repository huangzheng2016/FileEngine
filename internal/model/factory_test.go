package model

import (
	"context"
	"testing"

	"FileEngine/internal/db"
)

// TestNewChatModelFromProvider_Construction verifies providers construct
// without network I/O, including the reasoning_effort passthrough (empty and
// low) — the openai/ollama paths must accept it, claude must ignore it.
func TestNewChatModelFromProvider_Construction(t *testing.T) {
	ctx := context.Background()
	cases := []db.ModelProvider{
		{Provider: "openai", APIKey: "k", Model: "m", BaseURL: "http://x/v1"},
		{Provider: "openai", APIKey: "k", Model: "m", BaseURL: "http://x/v1", ReasoningEffort: "low"},
		{Provider: "ollama", APIKey: "k", Model: "m", ReasoningEffort: "high"},
		{Provider: "claude", APIKey: "k", Model: "m", ReasoningEffort: "low"},
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
