package db

import (
	"testing"

	"FileEngine/biz/dal/db/model"
)

func TestEffectiveContextWindow(t *testing.T) {
	p := &model.ModelProvider{}
	if got := p.EffectiveContextWindow(); got != model.DefaultContextWindow {
		t.Fatalf("zero value: got %d, want %d", got, model.DefaultContextWindow)
	}
	p.ContextWindow = 256_000
	if got := p.EffectiveContextWindow(); got != 256_000 {
		t.Fatalf("explicit value: got %d, want %d", got, 256_000)
	}
}
