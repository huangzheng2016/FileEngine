package agent

import (
	"strings"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func TestSystemPromptMatchesToolAvailability(t *testing.T) {
	off := defaultSystemPrompt(false, true, "")
	if strings.Contains(off, "create_category") {
		t.Fatal("prompt without create_category must not teach category creation")
	}
	if !strings.Contains(off, "不能创建新分类") {
		t.Fatal("prompt without create_category must steer to the fallback category")
	}

	on := defaultSystemPrompt(true, true, "")
	if !strings.Contains(on, "确无合适分类才 create_category") {
		t.Fatal("prompt with create_category must teach the create workflow")
	}
	if strings.Contains(on, "不能创建新分类") {
		t.Fatal("prompt with create_category must not forbid creation")
	}

	if DefaultSystemPrompt != defaultSystemPrompt(true, true, config.DefaultCategoryPolicy) {
		t.Fatal("DefaultSystemPrompt must equal the AllowAutoCategory=true, AllowScript=true variant with the default category policy")
	}

	custom := systemPromptForSession(config.AgentConfig{SystemPrompt: "自定义"}, &db.ScanSession{AllowAutoCategory: true})
	if custom != "自定义" {
		t.Fatalf("custom prompt override: got %q", custom)
	}
}

func TestSystemPromptMatchesScriptAvailability(t *testing.T) {
	on := defaultSystemPrompt(false, true, "")
	if !strings.Contains(on, "run_script") {
		t.Fatal("prompt with AllowScript must mention run_script")
	}
	off := defaultSystemPrompt(false, false, "")
	if strings.Contains(off, "run_script") {
		t.Fatal("prompt without AllowScript must not mention run_script")
	}
}
