package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"FileEngine/internal/db"
)

const (
	listDefaultLimit = 100
	listMaxLimit     = 500
	listMaxPaths     = 50

	describeMaxItems = 100
	describeMaxRunes = 200

	// A catalog row shows a short description; a path lookup shows the full one.
	listDescRunes = 60
)

type ListEntriesInput struct {
	ParentPath string   `json:"parent_path,omitempty" jsonschema_description:"要列出的目录的原路径；不填或空字符串表示扫描根目录。与 paths 二选一"`
	Paths      []string `json:"paths,omitempty" jsonschema:"maxItems=50" jsonschema_description:"按原路径查询这些具体条目（最多 50 个），返回完整描述；与 parent_path 二选一"`
	FileType   string   `json:"file_type,omitempty" jsonschema:"enum=file,enum=directory" jsonschema_description:"只列出文件或目录；不填列出全部"`
	Undecided  bool     `json:"undecided,omitempty" jsonschema_description:"只列出尚未决定的条目"`
	Cursor     uint     `json:"cursor,omitempty" jsonschema_description:"上一页返回的 next_cursor；第一页不填"`
	Limit      int      `json:"limit,omitempty" jsonschema_description:"每页条数，默认 100，最多 500"`
}

type ListEntriesOutput struct {
	Envelope
	Rows       string   `json:"rows,omitempty"`
	HasMore    bool     `json:"has_more,omitempty"`
	NextCursor uint     `json:"next_cursor,omitempty"`
	Missing    []string `json:"missing,omitempty"`
}

const (
	listColumns      = "名称\t类型\t大小\t角色\t目标\t描述"
	listPathsColumns = "路径\t类型\t大小\t角色\t目标\t描述"
)

func (tb *ToolBuilder) listEntries(ctx context.Context, in *ListEntriesInput) (*ListEntriesOutput, error) {
	if len(in.Paths) > 0 {
		if strings.TrimSpace(in.ParentPath) != "" {
			return failed[ListEntriesOutput]("parent_path 和 paths 只能给一个"), nil
		}
		return tb.entriesByPaths(in.Paths)
	}
	switch in.FileType {
	case "", "file", "directory":
	default:
		return failed[ListEntriesOutput]("file_type 只能是 file 或 directory"), nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = listDefaultLimit
	}
	limit = min(limit, listMaxLimit)
	parent := strings.Trim(normalizePath(in.ParentPath), "/")
	q := db.FileQuery{SessionID: tb.sessionID, ParentPath: &parent, FileType: in.FileType}
	if in.Undecided {
		role := db.RoleUndecided
		q.Role = &role
	}
	files, more, err := tb.repo.ListFilesAfter(q, in.Cursor, limit)
	if errors.Is(err, db.ErrBadCursor) {
		return failed[ListEntriesOutput]("cursor 已失效", "不带 cursor 从头列出"), nil
	} else if err != nil {
		return nil, err
	}
	out := succeeded[ListEntriesOutput]()
	out.Rows = WrapUntrusted("目录列表："+displayPath(parent)+"；列："+listColumns, entryRows(files, false))
	if more {
		out.HasMore, out.NextCursor = true, files[len(files)-1].ID
	}
	return out, nil
}

func (tb *ToolBuilder) entriesByPaths(paths []string) (*ListEntriesOutput, error) {
	if len(paths) > listMaxPaths {
		return failed[ListEntriesOutput](fmt.Sprintf("paths 最多 %d 个，本次 %d 个", listMaxPaths, len(paths)), "分批查询"), nil
	}
	want := make([]string, len(paths))
	for i, p := range paths {
		want[i] = strings.Trim(normalizePath(p), "/")
	}
	rows, err := tb.repo.GetEntriesByPaths(tb.sessionID, want)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]db.FileEntry, len(rows))
	for _, r := range rows {
		byPath[r.OriginalPath] = r
	}
	out := succeeded[ListEntriesOutput]()
	var found []db.FileEntry
	seen := map[string]bool{}
	for i, w := range want {
		if seen[w] {
			continue
		}
		seen[w] = true
		if e, ok := byPath[w]; ok {
			found = append(found, e)
		} else {
			out.Missing = append(out.Missing, SanitizeUntrustedName(paths[i]))
		}
	}
	if len(found) == 0 {
		return failed[ListEntriesOutput]("这些路径都不存在", "用 list_entries 列出父目录确认原路径"), nil
	}
	out.Rows = WrapUntrusted("条目；列："+listPathsColumns, entryRows(found, true))
	return out, nil
}

// File-derived text: names, targets and descriptions are sanitized.
func entryRows(files []db.FileEntry, withPath bool) string {
	var sb strings.Builder
	for _, f := range files {
		typ := "f"
		size := humanBytes(f.Size)
		if f.FileType == "directory" {
			typ = "d"
			size = fmt.Sprintf("%d项", f.ChildCount)
		} else if f.FileType == "symlink" {
			typ = "l"
		}
		role := f.Role
		if role == "" {
			role = "-"
		}
		if f.NeedsReview {
			role += "?"
		}
		target := SanitizeUntrustedName(f.NewPath)
		if f.Sensitive {
			target += " [敏感]"
		}
		desc := strings.Join(strings.Fields(f.Description), " ")
		maxRunes := listDescRunes
		if withPath {
			maxRunes = describeMaxRunes
		}
		if r := []rune(desc); len(r) > maxRunes {
			desc = string(r[:maxRunes]) + "…"
		}
		first := f.Name
		if withPath {
			first = f.OriginalPath
		}
		sb.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\n", SanitizeUntrustedName(first), typ, size, role, target, SanitizeUntrustedName(desc)))
	}
	return sb.String()
}

type DescribeItem struct {
	Path        string `json:"path" jsonschema_description:"条目的原路径"`
	Description string `json:"description" jsonschema_description:"一句中文描述，200 字以内"`
}

type DescribeInput struct {
	Items []DescribeItem `json:"items" jsonschema:"maxItems=100" jsonschema_description:"最多 100 条"`
}

type DescribeOutput struct {
	Envelope
	OK     int           `json:"ok"`
	Failed []BatchFailed `json:"failed,omitempty"`
}

func (tb *ToolBuilder) describe(ctx context.Context, in *DescribeInput) (*DescribeOutput, error) {
	if msg := tb.decidingGate(); msg != "" {
		return failed[DescribeOutput](msg), nil
	}
	if len(in.Items) == 0 {
		return failed[DescribeOutput]("items 不能为空"), nil
	}
	if len(in.Items) > describeMaxItems {
		return failed[DescribeOutput](fmt.Sprintf("一次最多 %d 条，本次 %d 条", describeMaxItems, len(in.Items)), "分批调用"), nil
	}
	out := succeeded[DescribeOutput]()
	for _, it := range in.Items {
		p := strings.Trim(normalizePath(it.Path), "/")
		desc := strings.TrimSpace(it.Description)
		switch {
		case desc == "":
			out.Failed = append(out.Failed, BatchFailed{Path: SanitizeUntrustedName(it.Path), Error: "description 不能为空"})
			continue
		case utf8.RuneCountInString(desc) > describeMaxRunes:
			out.Failed = append(out.Failed, BatchFailed{Path: SanitizeUntrustedName(it.Path), Error: fmt.Sprintf("描述超过 %d 字", describeMaxRunes)})
			continue
		}
		f, err := tb.repo.GetFileByPath(tb.sessionID, p)
		if err != nil {
			out.Failed = append(out.Failed, BatchFailed{Path: SanitizeUntrustedName(it.Path), Error: "条目不存在"})
			continue
		}
		if f.Description != desc {
			// Column-scoped: parallel tool calls on the same row must not be
			// overwritten with this stale copy.
			if err := tb.repo.UpdateDescription(tb.sessionID, p, desc); err != nil {
				return nil, err
			}
		}
		out.OK++
	}
	out.Success = len(out.Failed) == 0
	if !out.Success {
		out.Error = fmt.Sprintf("%d 条未写入，见 failed", len(out.Failed))
	}
	return out, nil
}
