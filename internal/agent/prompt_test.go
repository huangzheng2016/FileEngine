package agent

import (
	"strings"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func TestSystemPromptMatchesToolAvailability(t *testing.T) {
	off := defaultSystemPrompt(false, true)
	if strings.Contains(off, "create_category") {
		t.Fatal("prompt without create_category must not teach category creation")
	}
	if !strings.Contains(off, "不能创建新分类") {
		t.Fatal("prompt without create_category must steer to the fallback category")
	}

	on := defaultSystemPrompt(true, true)
	if !strings.Contains(on, "确无合适分类才 create_category") {
		t.Fatal("prompt with create_category must teach the create workflow")
	}
	if strings.Contains(on, "不能创建新分类") {
		t.Fatal("prompt with create_category must not forbid creation")
	}

	if DefaultSystemPrompt != defaultSystemPrompt(true, true) {
		t.Fatal("DefaultSystemPrompt must equal the AllowAutoCategory=true, AllowScript=true variant")
	}

	custom := systemPromptForSession(config.AgentConfig{SystemPrompt: "自定义"}, &db.ScanSession{AllowAutoCategory: true})
	if custom != "自定义" {
		t.Fatalf("custom prompt override: got %q", custom)
	}
}

func TestSystemPromptMatchesScriptAvailability(t *testing.T) {
	cfg := config.AgentConfig{}
	for _, allow := range []bool{true, false} {
		s := &db.ScanSession{AllowScript: allow}
		for name, p := range map[string]string{
			"tagging": defaultSystemPrompt(false, allow),
			"chat":    chatSystemPrompt(cfg, s),
			"review":  reviewSystemPrompt(cfg, s),
		} {
			if strings.Contains(p, "run_script") != allow {
				t.Fatalf("%s prompt mentions run_script = %v, want %v", name, !allow, allow)
			}
		}
	}
}

func TestSurveyPromptMatchesCategoryCreation(t *testing.T) {
	cfg := config.AgentConfig{}
	if p := surveySystemPrompt(cfg, &db.ScanSession{AllowAutoCategory: false}); strings.Contains(p, "create_category") {
		t.Fatal("survey without create_category must not teach it")
	}
	if p := surveySystemPrompt(cfg, &db.ScanSession{AllowAutoCategory: true}); !strings.Contains(p, "create_category") {
		t.Fatal("survey with create_category must teach it")
	}
}
