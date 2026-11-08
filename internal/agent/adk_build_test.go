package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/schema"
)

func TestTruncateToolResult(t *testing.T) {
	h := truncateToolResult(5)
	short := &reduction.ToolDetail{ToolResult: &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: "短文本"}}}}
	if r, _ := h(context.Background(), short); r.NeedTrunc {
		t.Fatal("short result must pass through")
	}
	img := schema.ToolOutputPart{Type: schema.ToolPartTypeImage}
	long := &reduction.ToolDetail{ToolResult: &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: "一二三四五六七"}, img}}}
	r, _ := h(context.Background(), long)
	if !r.NeedTrunc || r.NeedOffload || len(r.ToolResult.Parts) != 2 || r.ToolResult.Parts[1].Type != schema.ToolPartTypeImage {
		t.Fatalf("result = %+v", r)
	}
	if text := r.ToolResult.Parts[0].Text; !strings.HasPrefix(text, "一二三四五\n") || !strings.Contains(text, "共 7 字符") {
		t.Fatalf("truncated text = %q", text)
	}
	if workerReduction.clearAt(1_000_000) != 35_000 || interactiveReduction.clearAt(200_000) != 150_000 {
		t.Fatal("reduction thresholds changed")
	}
}
