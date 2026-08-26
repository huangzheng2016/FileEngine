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
	if strings.Contains(off, "创建新分类前必须") {
		t.Fatal("prompt without create_category must not teach category creation")
	}
	if !strings.Contains(off, "不能创建新分类") {
		t.Fatal("prompt without create_category must steer to the fallback category")
	}

	on := defaultSystemPrompt(true, true, "")
	if !strings.Contains(on, "创建新分类前必须 list_categories") {
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

// The instruct prompt's tool list must match BuildInstructTools gating:
// read_file only when AllowReadFile, create_category only when
// AllowAutoCategory, run_script only when AllowScript.
func TestInstructPromptMatchesToolAvailability(t *testing.T) {
	base := defaultInstructPrompt(&db.ScanSession{})
	if strings.Contains(base, "read_file") {
		t.Fatal("read_file must not be listed when AllowReadFile is false")
	}
	if strings.Contains(base, "create_category") {
		t.Fatal("create_category must not be listed when AllowAutoCategory is false")
	}
	if strings.Contains(base, "run_script") {
		t.Fatal("run_script must not be listed when AllowScript is false")
	}

	full := defaultInstructPrompt(&db.ScanSession{AllowReadFile: true, AllowAutoCategory: true, AllowScript: true})
	if !strings.Contains(full, "- read_file:") {
		t.Fatal("read_file must be listed when AllowReadFile is true")
	}
	if !strings.Contains(full, "- create_category:") {
		t.Fatal("create_category must be listed when AllowAutoCategory is true")
	}
	if !strings.Contains(full, "- run_script:") {
		t.Fatal("run_script must be listed when AllowScript is true")
	}

	custom := instructPromptForSession(config.AgentConfig{InstructSystemPrompt: "自定义"}, &db.ScanSession{})
	if custom != "自定义" {
		t.Fatalf("custom instruct prompt override: got %q", custom)
	}
}

// The prompts must teach the create_category guard rails explicitly: prefer
// existing categories, creation is guarded, a rejection returns the latest
// list to act on, and fuzzy-similar hits come back as merge hints.
func TestPromptsTeachCreateGuard(t *testing.T) {
	on := defaultSystemPrompt(true, true, "")
	for _, want := range []string{"优先使用现有分类", "护栏", "最新完整分类列表", "归并"} {
		if !strings.Contains(on, want) {
			t.Fatalf("auto-category prompt must teach %q", want)
		}
	}

	full := defaultInstructPrompt(&db.ScanSession{AllowAutoCategory: true})
	if !strings.Contains(full, "护栏") {
		t.Fatal("instruct prompt must mention the create_category guard")
	}
}
