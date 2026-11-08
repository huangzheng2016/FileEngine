package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

const testCategoryPolicy = "分类创建规范：\n- 测试规范原文，逐字注入"

// toolDesc returns the description of the named tool from a built tool set.
func toolDesc(t *testing.T, tools []tool.BaseTool, name string) string {
	t.Helper()
	for _, bt := range tools {
		info, err := bt.Info(context.Background())
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		if info.Name == name {
			return info.Desc
		}
	}
	t.Fatalf("tool %q not found", name)
	return ""
}

// The 分类规范 section appears in the default system prompt only when a
// category policy is configured, and carries the policy text verbatim.
func TestSystemPromptCategoryPolicyTwoStates(t *testing.T) {
	without := defaultSystemPrompt(true, true, "")
	if strings.Contains(without, "分类规范") {
		t.Fatal("empty policy must not add a 分类规范 section")
	}

	with := defaultSystemPrompt(true, true, testCategoryPolicy)
	if !strings.Contains(with, "## 分类规范\n\n") || !strings.Contains(with, "以目标和方案为准）\n\n"+testCategoryPolicy) {
		t.Fatal("non-empty policy must appear verbatim under a 分类规范 section")
	}

	// systemPromptForSession routes the configured policy into the default.
	if got := systemPromptForSession(config.AgentConfig{CategoryPolicy: testCategoryPolicy}, &db.ScanSession{AllowAutoCategory: true, AllowScript: true}); got != with {
		t.Fatal("systemPromptForSession must inject cfg.CategoryPolicy into the default prompt")
	}

	// A custom prompt still wins verbatim — no policy section is appended.
	custom := systemPromptForSession(config.AgentConfig{SystemPrompt: "自定义", CategoryPolicy: testCategoryPolicy}, &db.ScanSession{AllowAutoCategory: true})
	if custom != "自定义" {
		t.Fatalf("custom prompt override with policy: got %q", custom)
	}
}

// The create_category tool description (autotag and instruct variants)
// appends the policy verbatim when configured, and stays bare without one.
func TestCreateCategoryDescCategoryPolicyTwoStates(t *testing.T) {
	session := &db.ScanSession{AllowAutoCategory: true}

	withPolicy := NewToolBuilder(nil, nil, 1, 1, NewLogger(nil, 1, 0), config.AgentConfig{CategoryPolicy: testCategoryPolicy}, session, false)
	tools, err := withPolicy.BuildTools()
	if err != nil {
		t.Fatalf("BuildTools: %v", err)
	}
	if desc := toolDesc(t, tools, "create_category"); !strings.HasSuffix(desc, "\n\n分类规范：\n"+testCategoryPolicy) {
		t.Fatalf("autotag create_category desc must end with the policy text: %q", desc)
	}
	instructTools, err := withPolicy.BuildInstructTools()
	if err != nil {
		t.Fatalf("BuildInstructTools: %v", err)
	}
	if desc := toolDesc(t, instructTools, "create_category"); !strings.HasSuffix(desc, "\n\n分类规范：\n"+testCategoryPolicy) {
		t.Fatalf("instruct create_category desc must end with the policy text: %q", desc)
	}

	noPolicy := NewToolBuilder(nil, nil, 1, 1, NewLogger(nil, 1, 0), config.AgentConfig{}, session, false)
	tools, err = noPolicy.BuildTools()
	if err != nil {
		t.Fatalf("BuildTools: %v", err)
	}
	if desc := toolDesc(t, tools, "create_category"); strings.Contains(desc, "分类规范") {
		t.Fatalf("empty policy must not add a 分类规范 suffix: %q", desc)
	}
}

// The run_script description's create_category builtin line follows the same
// two states: policy text appended when configured, absent otherwise.
func TestRunScriptDescCategoryPolicyTwoStates(t *testing.T) {
	session := &db.ScanSession{AllowScript: true, AllowAutoCategory: true}

	withPolicy := NewToolBuilder(nil, nil, 1, 1, NewLogger(nil, 1, 0), config.AgentConfig{CategoryPolicy: testCategoryPolicy}, session, false)
	st, err := withPolicy.buildScriptTool()
	if err != nil || st == nil {
		t.Fatalf("buildScriptTool: %v, nil=%v", err, st == nil)
	}
	info, err := st.Info(context.Background())
	if err != nil {
		t.Fatalf("tool info: %v", err)
	}
	if !strings.Contains(info.Desc, "同名/同路径拒绝）\n\n分类规范：\n"+testCategoryPolicy) {
		t.Fatalf("run_script desc must append the policy to the create_category builtin: %q", info.Desc)
	}

	noPolicy := NewToolBuilder(nil, nil, 1, 1, NewLogger(nil, 1, 0), config.AgentConfig{}, session, false)
	st, err = noPolicy.buildScriptTool()
	if err != nil || st == nil {
		t.Fatalf("buildScriptTool: %v, nil=%v", err, st == nil)
	}
	info, err = st.Info(context.Background())
	if err != nil {
		t.Fatalf("tool info: %v", err)
	}
	if strings.Contains(info.Desc, "分类规范") {
		t.Fatalf("empty policy must not add a 分类规范 suffix: %q", info.Desc)
	}
}
