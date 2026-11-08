package agent

import (
	"strings"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// The default autotag prompt must describe only the category capabilities the
// session actually has: create_category guidance appears only when the tool
// is registered (AllowAutoCategory), otherwise the prompt steers to the
// fallback category instead of inviting hallucinated tool calls.
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

	// The exported default matches the default session (AllowAutoCategory=true,
	// AllowScript=true) under the built-in category policy.
	if DefaultSystemPrompt != defaultSystemPrompt(true, true, config.DefaultCategoryPolicy) {
		t.Fatal("DefaultSystemPrompt must equal the AllowAutoCategory=true, AllowScript=true variant with the default category policy")
	}

	// A custom configured prompt always wins, verbatim.
	custom := systemPromptForSession(config.AgentConfig{SystemPrompt: "自定义"}, &db.ScanSession{AllowAutoCategory: true})
	if custom != "自定义" {
		t.Fatalf("custom prompt override: got %q", custom)
	}
}

// The run_script guidance follows the session switch: present only when
// AllowScript is on (its default), absent when the tool is not registered.
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
