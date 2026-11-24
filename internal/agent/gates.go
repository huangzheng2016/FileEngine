package agent

import "FileEngine/internal/db"

type ReadFileInput struct {
	Path   string `json:"path" jsonschema_description:"文件的原路径；读压缩包内的条目用 压缩包路径::条目路径"`
	Offset int    `json:"offset,omitempty" jsonschema_description:"起始字符位置；续读时传上次返回的 next_offset"`
}

type ReadFileOutput struct {
	Envelope
	Content    string `json:"content,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	NextOffset int    `json:"next_offset,omitempty"`
}

// Survey runs never decide: they shape categories, rules and the plan only.
// Gates live in handlers, never in the tool list, so tools stay byte-identical.
func (tb *ToolBuilder) decidingGate() string {
	if tb.RunKind == db.RunPhaseSurvey {
		return "调研阶段只改分类、规则和方案，不决定具体条目"
	}
	return ""
}
