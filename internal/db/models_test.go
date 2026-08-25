package db

import (
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
)

func TestEffectiveContextWindow(t *testing.T) {
	p := &ModelProvider{}
	if got := p.EffectiveContextWindow(); got != DefaultContextWindow {
		t.Fatalf("zero value: got %d, want %d", got, DefaultContextWindow)
	}
	p.ContextWindow = 256_000
	if got := p.EffectiveContextWindow(); got != 256_000 {
		t.Fatalf("explicit value: got %d, want %d", got, 256_000)
	}
}

func TestBackfillContextWindow(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Legacy rows (created before the column existed) read back as 0.
	legacy := &ModelProvider{Name: "legacy", Provider: "openai", Model: "gpt-4o", MaxTokens: 4096}
	modern := &ModelProvider{Name: "modern", Provider: "claude", Model: "claude-sonnet-4", MaxTokens: 8192, ContextWindow: 256_000}
	if err := DB.Create(legacy).Error; err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	if err := DB.Create(modern).Error; err != nil {
		t.Fatalf("create modern: %v", err)
	}

	if err := backfillContextWindow(DB); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var gotLegacy, gotModern ModelProvider
	if err := DB.First(&gotLegacy, legacy.ID).Error; err != nil {
		t.Fatalf("reload legacy: %v", err)
	}
	if err := DB.First(&gotModern, modern.ID).Error; err != nil {
		t.Fatalf("reload modern: %v", err)
	}
	if gotLegacy.ContextWindow != DefaultContextWindow {
		t.Fatalf("legacy context_window: got %d, want %d", gotLegacy.ContextWindow, DefaultContextWindow)
	}
	if gotLegacy.MaxTokens != 4096 {
		t.Fatalf("legacy max_tokens must stay untouched: got %d, want 4096", gotLegacy.MaxTokens)
	}
	if gotModern.ContextWindow != 256_000 {
		t.Fatalf("modern context_window: got %d, want %d", gotModern.ContextWindow, 256_000)
	}
}
