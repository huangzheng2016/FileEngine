package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// callTool runs a built tool by name exactly like the ReAct loop does and
// returns its decoded JSON result.
func callTool(t *testing.T, tb *ToolBuilder, kind ToolsetKind, name string, args any) map[string]any {
	t.Helper()
	raw, ok := callToolRaw(t, tb, kind, name, args)
	if !ok {
		t.Fatalf("tool %s is not in toolset %d", name, kind)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("tool %s result is not JSON: %v: %s", name, err, raw)
	}
	if _, has := out["success"].(bool); !has {
		t.Fatalf("tool %s result has no success flag: %s", name, raw)
	}
	return out
}

func callToolRaw(t *testing.T, tb *ToolBuilder, kind ToolsetKind, name string, args any) (string, bool) {
	t.Helper()
	tools, err := tb.BuildToolset(kind)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		info, _ := tl.Info(context.Background())
		if info.Name != name {
			continue
		}
		text, isStr := args.(string)
		if !isStr {
			b, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			text = string(b)
		}
		it, ok := tl.(tool.InvokableTool)
		if !ok {
			et := tl.(tool.EnhancedInvokableTool)
			res, err := et.InvokableRun(context.Background(), &schema.ToolArgument{Text: text})
			if err != nil {
				t.Fatalf("tool %s: %v", name, err)
			}
			return res.Parts[0].Text, true
		}
		out, err := it.InvokableRun(context.Background(), text)
		if err != nil {
			t.Fatalf("tool %s returned a Go error: %v", name, err)
		}
		return out, true
	}
	return "", false
}

func errText(out map[string]any) string {
	s, _ := out["error"].(string)
	return s
}

func okOf(out map[string]any) bool {
	b, _ := out["success"].(bool)
	return b
}

func mustOK(t *testing.T, out map[string]any) {
	t.Helper()
	if !okOf(out) {
		t.Fatalf("want success, got %s", fmt.Sprint(out))
	}
}
