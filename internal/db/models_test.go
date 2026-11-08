package db

import (
	"testing"
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
