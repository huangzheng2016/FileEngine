package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
	"FileEngine/internal/remotefs"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type ListFilesInput struct {
	ParentPath string `json:"parent_path" jsonschema_description:"目录的原路径；空字符串表示扫描根目录"`
	FileType   string `json:"file_type,omitempty" jsonschema_description:"只列出 file 或 directory；不填列出全部"`
	Undecided  bool   `json:"undecided,omitempty" jsonschema_description:"只列出尚未决定的条目"`
	Cursor     uint   `json:"cursor,omitempty" jsonschema_description:"上一页返回的 next_cursor；第一页不填"`
	Limit      int    `json:"limit,omitempty" jsonschema_description:"默认 100，最多 500"`
}

type ListFilesOutput struct {
	Parent     string `json:"parent"`
	Columns    string `json:"columns"`
	Rows       string `json:"rows"`
	HasMore    bool   `json:"has_more,omitempty"`
	NextCursor uint   `json:"next_cursor,omitempty"`
	Error      string `json:"error,omitempty"`
}

type GetFileInfoInput struct {
	Path string `json:"path" jsonschema_description:"条目的原路径"`
}

type GetFileInfoOutput struct {
	OriginalPath string `json:"original_path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	FileType     string `json:"file_type"`
	Description  string `json:"description"`
	Tagged       bool   `json:"tagged"`
	NewPath      string `json:"new_path"`
	ParentPath   string `json:"parent_path"`
	ChildCount   int    `json:"child_count"`
}

type ReadFileInput struct {
	Path   string `json:"path" jsonschema_description:"文件的原路径；读压缩包内的条目用 压缩包路径::条目路径"`
	Offset int    `json:"offset,omitempty" jsonschema_description:"起始字符位置；续读时传上次返回的 next_offset"`
}

type ReadFileOutput struct {
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated"`
	NextOffset int    `json:"next_offset,omitempty"`
}

type UpdateDescriptionInput struct {
	Path        string `json:"path" jsonschema_description:"条目的原路径"`
	Description string `json:"description" jsonschema_description:"一句中文描述，200 字以内"`
}

type UpdateDescriptionOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type ListCategoriesInput struct{}

type ListCategoriesOutput struct {
	Categories []CategoryItem `json:"categories"`
}

type CategoryItem struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Structure     string `json:"structure,omitempty"`
	Description   string `json:"description"`
	AgentEditable bool   `json:"agent_editable"`
}

type CreateCategoryInput struct {
	Name        string `json:"name" jsonschema_description:"分类名：2–6 个字的中文名词，如 照片、软件"`
	Path        string `json:"path" jsonschema_description:"分类路径，以 / 开头，如 /照片、/文档/合同"`
	Description string `json:"description" jsonschema_description:"放什么：一句话说明哪些文件属于这个分类"`
}

type CreateCategoryOutput struct {
	Success bool   `json:"success"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	// Error carries an agent-recoverable rejection with guidance; system
	// failures abort the run as Go errors.
	Error             string                `json:"error,omitempty"`
	SimilarCategories []SimilarCategoryItem `json:"similar_categories,omitempty"`
	Guidance          string                `json:"guidance,omitempty"`
}

type SimilarCategoryItem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	FileCount int64  `json:"file_count"`
	Reason    string `json:"reason"`
}

type UpdateCategoryInput struct {
	Name        string `json:"name" jsonschema_description:"要修改的分类的当前名称"`
	NewName     string `json:"new_name,omitempty" jsonschema_description:"新名称（可选）"`
	NewPath     string `json:"new_path,omitempty" jsonschema_description:"新路径（可选），以 / 开头；旧路径下已规划的目标随之更新"`
	Description string `json:"description,omitempty" jsonschema_description:"新的放什么说明（可选）"`
	Structure   string `json:"structure,omitempty" jsonschema_description:"分类下的子目录结构说明（可选），如 按 年/月 分目录"`
}

type UpdateCategoryOutput struct {
	Success bool   `json:"success"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Error   string `json:"error,omitempty"`
}

type DeleteCategoryInput struct {
	Name string `json:"name" jsonschema_description:"要删除的分类名称"`
}

type DeleteCategoryOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// eino makes any tool error fatal to the ReAct loop, so input the model can
// fix is reported as a success=false result instead.
type rejectError struct{ msg string }

func (e *rejectError) Error() string { return e.msg }

func rejectf(format string, args ...interface{}) error {
	return &rejectError{msg: fmt.Sprintf(format, args...)}
}

type ToolBuilder struct {
	repo               *db.Repository
	fs                 remotefs.RemoteFS
	sessionID          uint
	filesystemID       uint
	logger             *Logger
	cfg                config.AgentConfig
	session            *db.ScanSession
	modelVisionEnabled bool
	guard              categoryGuard

	// Tool-originated events carry RunID; 0 outside a run.
	RunID    uint
	unitRoot string
	notes    notesCursor
}

func NewToolBuilder(repo *db.Repository, fs remotefs.RemoteFS, sessionID uint, filesystemID uint, logger *Logger, cfg config.AgentConfig, session *db.ScanSession, modelVisionEnabled bool) *ToolBuilder {
	return &ToolBuilder{
		repo:               repo,
		fs:                 fs,
		sessionID:          sessionID,
		filesystemID:       filesystemID,
		logger:             logger,
		cfg:                cfg,
		session:            session,
		modelVisionEnabled: modelVisionEnabled,
	}
}

func (tb *ToolBuilder) buildVisionTool() (tool.BaseTool, error) {
	if !tb.modelVisionEnabled || !tb.session.AllowVision {
		return nil, nil
	}
	maxImages, maxPx, maxBytes := tb.visionLimits()
	desc := fmt.Sprintf("查看图片文件内容，返回缩略图（长边%dpx）。每次最多%d张，支持 jpg/jpeg/png/gif；HEIC/RAW 不支持会跳过；单文件超过%dMB拒绝。仅在确实需要确认图片内容时使用", maxPx, maxImages, maxBytes/(1024*1024))
	viewImage, err := utils.InferEnhancedTool("view_image", desc, tb.viewImage)
	if err != nil {
		return nil, fmt.Errorf("build view_image: %w", err)
	}
	return viewImage, nil
}

func normalizePath(p string) string {
	return strings.TrimPrefix(p, "/")
}

func ptrStr(s string) *string {
	return &s
}

const listColumns = "名称\t类型\t大小\t角色\t目标\t描述"

func (tb *ToolBuilder) listFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	parent := strings.Trim(normalizePath(input.ParentPath), "/")
	q := db.FileQuery{SessionID: tb.sessionID, ParentPath: &parent, FileType: input.FileType}
	if input.Undecided {
		role := db.RoleUndecided
		q.Role = &role
	}
	out := &ListFilesOutput{Parent: parent, Columns: listColumns}
	files, more, err := tb.repo.ListFilesAfter(q, input.Cursor, limit)
	if errors.Is(err, db.ErrBadCursor) {
		out.Error = "cursor 已失效，请不带 cursor 从头列出"
		return out, nil
	} else if err != nil {
		return nil, err
	}
	out.Rows = entryRows(files)
	if more {
		out.HasMore, out.NextCursor = true, files[len(files)-1].ID
	}
	return out, nil
}

// Names are sanitized since they are untrusted.
func entryRows(files []db.FileEntry) string {
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
		target := f.NewPath
		if f.Sensitive {
			target += " [敏感]"
		}
		desc := strings.Join(strings.Fields(f.Description), " ")
		if r := []rune(desc); len(r) > 60 {
			desc = string(r[:60]) + "…"
		}
		sb.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\n", SanitizeUntrustedName(f.Name), typ, size, role, target, SanitizeUntrustedName(desc)))
	}
	return sb.String()
}

func (tb *ToolBuilder) getFileInfo(ctx context.Context, input *GetFileInfoInput) (*GetFileInfoOutput, error) {

	f, err := tb.repo.GetFileByPath(tb.sessionID, normalizePath(input.Path))
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", input.Path)
	}

	out := &GetFileInfoOutput{
		OriginalPath: f.OriginalPath,
		Name:         f.Name,
		Size:         f.Size,
		FileType:     f.FileType,
		Description:  f.Description,
		Tagged:       f.Tagged,
		NewPath:      f.NewPath,
		ParentPath:   f.ParentPath,
		ChildCount:   f.ChildCount,
	}
	return out, nil
}

const defaultReadChars = 8000

func (tb *ToolBuilder) readPageChars() int {
	if tb.cfg.MaxFileReadSize > 0 {
		return tb.cfg.MaxFileReadSize
	}
	return defaultReadChars
}

const sensitiveNotice = "[敏感文件（密钥/凭证/证件/税务类），不读取内容；只按文件名和位置判断]"

func (tb *ToolBuilder) isSensitivePath(p string) bool {
	p, _, _ = strings.Cut(p, "::")
	if db.IsSensitiveName(p) {
		return true
	}
	if tb.repo == nil {
		return false
	}
	f, err := tb.repo.GetFileByPath(tb.sessionID, strings.Trim(normalizePath(p), "/"))
	return err == nil && f.Sensitive
}

func (tb *ToolBuilder) readFile(ctx context.Context, input *ReadFileInput) (*ReadFileOutput, error) {
	if tb.isSensitivePath(input.Path) {
		return &ReadFileOutput{Content: sensitiveNotice}, nil
	}
	limit := tb.readPageChars()
	offset := max(input.Offset, 0)
	out, err := tb.readFileRaw(ctx, input, offset+limit)
	if err != nil || out == nil {
		return out, err
	}
	runes := []rune(out.Content)
	if offset >= len(runes) {
		return &ReadFileOutput{Content: ""}, nil
	}
	page := runes[offset:]
	if len(page) > limit {
		page = page[:limit]
	}
	res := &ReadFileOutput{Content: string(page), Truncated: out.Truncated || offset+len(page) < len(runes)}
	if res.Truncated {
		res.NextOffset = offset + len(page)
	}
	return res, nil
}

func (tb *ToolBuilder) readFileRaw(ctx context.Context, input *ReadFileInput, maxSize int) (*ReadFileOutput, error) {
	// Split first; the filesystem-facing part is validated like every other
	// path.
	archivePath, entryName, hasEntry := strings.Cut(input.Path, "::")
	if err := pathutil.ValidateFSPath(archivePath); err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	if hasEntry && entryName == "" {
		return nil, fmt.Errorf("empty archive entry name in %q", input.Path)
	}

	ext := documentExt(archivePath)
	arch := archiveKind(archivePath)
	if hasEntry && arch == "" {
		return nil, fmt.Errorf("entry syntax (::) is only valid for zip/tar archives: %q", input.Path)
	}

	if arch != "" {
		return tb.readArchive(ctx, input, archivePath, entryName, arch, maxSize)
	}

	// Documents must be parsed whole (zip central directories and PDF xref
	// tables are at the end), so they get a larger read budget.
	readCap := int64(maxSize)*4 + 1
	if ext != "" {
		readCap = maxDocumentReadBytes + 1
	}

	reader, err := tb.fs.ReadFile(ctx, normalizePath(archivePath))
	if err != nil {
		return nil, fmt.Errorf("cannot read file: %w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, readCap))
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	if ext != "" {
		out, err := extractDocument(archivePath, data, maxSize)
		if err != nil {
			return nil, err
		}
		return out, nil
	}

	peek := data
	if len(peek) > 1024 {
		peek = peek[:1024]
	}
	if len(peek) > 0 && !isTextContent(string(peek)) {
		out := &ReadFileOutput{Content: "[binary file - content not displayable]", Truncated: false}
		return out, nil
	}

	content, cut := capRunes(string(data), maxSize)
	out := &ReadFileOutput{Content: content, Truncated: cut || int64(len(data)) >= readCap}
	return out, nil
}

func (tb *ToolBuilder) readArchive(ctx context.Context, input *ReadFileInput, archivePath, entryName, kind string, maxChars int) (*ReadFileOutput, error) {
	log := func(out *ReadFileOutput) (*ReadFileOutput, error) {
		return out, nil
	}

	if kind == "rar" || kind == "7z" {
		return log(&ReadFileOutput{Content: fmt.Sprintf("[unsupported archive format: .%s — supported: zip, tar, tar.gz/tgz]", kind)})
	}

	maxEntry := int64(tb.cfg.ArchiveEntryMaxSize)
	if maxEntry <= 0 {
		maxEntry = defaultArchiveEntryMaxSize
	}

	rc, err := tb.fs.ReadFile(ctx, normalizePath(archivePath))
	if err != nil {
		return nil, fmt.Errorf("cannot read file: %w", err)
	}

	if kind == "tar" || kind == "tar.gz" {
		defer rc.Close()
		gz := kind == "tar.gz"
		if entryName != "" {
			out, err := readTarEntry(rc, gz, entryName, maxEntry)
			if err != nil {
				return nil, err
			}
			return log(out)
		}
		listing, err := listTarArchive(rc, gz)
		if err != nil {
			return nil, fmt.Errorf("list tar archive: %w", err)
		}
		content, cut := capRunes(listing, maxChars)
		return log(&ReadFileOutput{Content: content, Truncated: cut})
	}

	// Entry reads are gated on the archive size; listing is not, since the
	// central directory alone is cheap on random-access backends.
	size := int64(-1)
	if st, serr := tb.fs.Stat(ctx, normalizePath(archivePath)); serr == nil {
		size = st.Size
	}
	if entryName != "" && size > maxDocumentReadBytes {
		rc.Close()
		return log(&ReadFileOutput{Content: fmt.Sprintf("[archive too large (%s over the %d MB limit): entry reads are disabled — listing only]",
			humanBytes(size), maxDocumentReadBytes/(1<<20))})
	}
	ra, actualSize, notice, cleanup, err := resolveArchiveReaderAt(rc, size, maxDocumentReadBytes, tb.session.Protocol)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	if notice != "" {
		return log(&ReadFileOutput{Content: notice})
	}
	if entryName != "" {
		out, err := readZipEntry(ra, actualSize, entryName, maxEntry)
		if err != nil {
			return nil, err
		}
		return log(out)
	}
	listing, err := listZipArchive(ra, actualSize)
	if err != nil {
		return nil, fmt.Errorf("list zip archive: %w", err)
	}
	content, cut := capRunes(listing, maxChars)
	return log(&ReadFileOutput{Content: content, Truncated: cut})
}

func capRunes(s string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		return s, false
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s, false
	}
	return string(runes[:maxChars]), true
}

func (tb *ToolBuilder) updateDescription(ctx context.Context, input *UpdateDescriptionInput) (*UpdateDescriptionOutput, error) {

	f, err := tb.repo.GetFileByPath(tb.sessionID, normalizePath(input.Path))
	if err != nil {
		out := &UpdateDescriptionOutput{Error: fmt.Sprintf("条目不存在：%s", input.Path)}
		return out, nil
	}

	if f.Description == input.Description {
		out := &UpdateDescriptionOutput{Success: true}
		return out, nil
	}

	// Column-scoped: a full-row Save of the struct loaded above races with
	// parallel tool calls on the same row and would restore their fields to
	// stale values.
	if err := tb.repo.UpdateDescription(tb.sessionID, normalizePath(input.Path), input.Description); err != nil {
		return nil, err
	}

	out := &UpdateDescriptionOutput{Success: true}
	return out, nil
}

func (tb *ToolBuilder) listCategories(ctx context.Context, input *ListCategoriesInput) (*ListCategoriesOutput, error) {

	cats, err := tb.repo.ListCategories(tb.filesystemID)
	if err != nil {
		return nil, err
	}

	items := make([]CategoryItem, len(cats))
	for i, c := range cats {
		items[i] = CategoryItem{
			Name:          c.Name,
			Path:          c.Path,
			Structure:     c.Structure,
			Description:   c.Description,
			AgentEditable: c.AgentEditable,
		}
	}

	// The agent is receiving the category list: record the snapshot for the
	// create_category version guard.
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	tb.guard.note(names)

	out := &ListCategoriesOutput{Categories: items}
	return out, nil
}

func (tb *ToolBuilder) createCategory(ctx context.Context, input *CreateCategoryInput) (*CreateCategoryOutput, error) {
	out, err := tb.createCategoryCore(ctx, input)
	if err != nil {
		var rj *rejectError
		if errors.As(err, &rj) {
			out := &CreateCategoryOutput{Error: rj.msg}
			return out, nil
		}
		return nil, err
	}
	return out, nil
}

// Shared by the tool handler and the run_script builtin so a script can never
// bypass the guard rails; only the tool handler logs.
func (tb *ToolBuilder) createCategoryCore(ctx context.Context, input *CreateCategoryInput) (*CreateCategoryOutput, error) {

	// Category paths become new_path prefixes, so they follow the same root
	// confinement as set_target.
	if err := pathutil.ValidateFSPath(input.Path); err != nil || input.Path == "" {
		if err == nil {
			err = errors.New("路径不能为空")
		}
		return nil, rejectf("分类路径非法：%v", err)
	}

	usages, err := tb.repo.GetCategoryUsageSummary(tb.filesystemID, tb.sessionID)
	if err != nil {
		return nil, fmt.Errorf("create category: read current categories: %w", err)
	}
	names := make([]string, len(usages))
	for i, u := range usages {
		names[i] = u.Name
	}

	// The rejection doubles as the agent's re-read, so it is recorded as the new
	// snapshot: a retry decided from that list passes unless the set changed
	// again.
	if lastFP, seen := tb.guard.last(); !seen || lastFP != categoryFingerprint(names) {
		tb.guard.note(names)
		list := strings.TrimSpace(buildCategorySummary(usages))
		if list == "" {
			list = "（当前没有任何分类）"
		}
		return nil, rejectf("分类列表自你上次查看后已变化（或你尚未查看过），本次创建已拒绝。最新完整分类列表：\n%s\n请基于该列表重新决定：优先用 set_targets 把文件归入现有分类；确认没有合适分类后再调用 create_category", list)
	}

	hard, soft := findSimilarCategories(usages, input.Name, input.Path)
	if len(hard) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("已存在相同分类（名称或路径重复），拒绝创建 %q (%s)：\n", input.Name, input.Path))
		for _, s := range hard {
			sb.WriteString(fmt.Sprintf("- %s (%s): 已归入 %d 项（%s）\n", s.usage.Name, s.usage.Path, s.usage.FileCount, s.reason))
		}
		sb.WriteString("请改用 set_targets 把文件归入上述现有分类（或用 update_category 调整它）；确需新分类时换用不同的名称和路径")
		return nil, rejectf("%s", sb.String())
	}

	cat := &db.Category{
		FilesystemID:  tb.filesystemID,
		Name:          input.Name,
		Path:          input.Path,
		Description:   input.Description,
		AgentCreated:  true,
		AgentEditable: true,
	}
	created, err := tb.repo.CreateCategoryWithConflict(cat)
	if err != nil {
		return nil, fmt.Errorf("create category: %w", err)
	}
	if !created {
		return nil, rejectf("名称 %q 或路径 %q 已被其他分类使用，未创建：请用 set_targets 把文件归入现有分类，或用 update_category 调整它", input.Name, input.Path)
	}

	// The agent knows the post-create set, so back-to-back creations are not
	// re-challenged; anyone else's change still forces a re-read.
	tb.guard.note(append(names, cat.Name))

	out := &CreateCategoryOutput{Success: true, Name: cat.Name, Path: cat.Path}
	for _, s := range soft {
		out.SimilarCategories = append(out.SimilarCategories, SimilarCategoryItem{
			Name:      s.usage.Name,
			Path:      s.usage.Path,
			FileCount: s.usage.FileCount,
			Reason:    s.reason,
		})
	}
	if len(out.SimilarCategories) > 0 {
		out.Guidance = "已创建，但存在路径重叠的相似分类（见 similar_categories）。如果职责重复，用 set_targets 把文件归入现有分类并删除新分类；确有必要保留则无需处理"
	}
	return out, nil
}

func (tb *ToolBuilder) updateCategory(ctx context.Context, input *UpdateCategoryInput) (*UpdateCategoryOutput, error) {

	reject := func(msg string) (*UpdateCategoryOutput, error) {
		out := &UpdateCategoryOutput{Error: msg}
		return out, nil
	}

	cat, err := tb.repo.GetCategoryByName(tb.filesystemID, input.Name)
	if err != nil {
		return reject(fmt.Sprintf("分类不存在：%s", input.Name))
	}

	if !cat.AgentEditable {
		return reject(fmt.Sprintf("分类「%s」由用户维护，不能修改", input.Name))
	}

	if input.NewName == "" && input.NewPath == "" && input.Description == "" && input.Structure == "" {
		return reject(fmt.Sprintf("没有要修改的字段：至少给出 new_name、new_path、description、structure 之一（分类「%s」）", input.Name))
	}

	// A changed category path cascades into planned targets, so it follows the
	// same root confinement as set_target.
	if input.NewPath != "" {
		if err := pathutil.ValidateFSPath(input.NewPath); err != nil {
			return reject(fmt.Sprintf("new_path 非法：%v", err))
		}
	}
	if msg, err := tb.categoryClash(cat.ID, input.NewName, input.NewPath); err != nil || msg != "" {
		if err != nil {
			return nil, err
		}
		return reject(msg)
	}

	oldPath := cat.Path
	if input.NewName != "" {
		cat.Name = input.NewName
	}
	if input.NewPath != "" {
		cat.Path = input.NewPath
	}
	if input.Description != "" {
		cat.Description = input.Description
	}
	if input.Structure != "" {
		cat.Structure = input.Structure
	}

	if err := tb.repo.UpdateCategoryPath(cat, oldPath); err != nil {
		return nil, fmt.Errorf("update category: %w", err)
	}

	out := &UpdateCategoryOutput{Success: true, Name: cat.Name, Path: cat.Path}
	return out, nil
}

// Name and path are unique per filesystem; a clash is the agent's to fix,
// not a database error.
func (tb *ToolBuilder) categoryClash(id uint, newName, newPath string) (string, error) {
	if newName == "" && newPath == "" {
		return "", nil
	}
	cats, err := tb.repo.ListCategories(tb.filesystemID)
	if err != nil {
		return "", err
	}
	for _, c := range cats {
		if c.ID == id {
			continue
		}
		if newName != "" && normalizeCategoryName(c.Name) == normalizeCategoryName(newName) {
			return fmt.Sprintf("名称「%s」已被分类「%s」使用", newName, c.Name), nil
		}
		if newPath != "" && normalizeCategoryPath(c.Path) == normalizeCategoryPath(newPath) {
			return fmt.Sprintf("路径 %s 已被分类「%s」使用；合并分类时先把规划改到保留的分类，再删除多余的分类", db.NormalizeTargetPath(newPath), c.Name), nil
		}
	}
	return "", nil
}

func (tb *ToolBuilder) deleteCategory(ctx context.Context, input *DeleteCategoryInput) (*DeleteCategoryOutput, error) {

	reject := func(msg string) (*DeleteCategoryOutput, error) {
		out := &DeleteCategoryOutput{Error: msg}
		return out, nil
	}

	cat, err := tb.repo.GetCategoryByName(tb.filesystemID, input.Name)
	if err != nil {
		return reject(fmt.Sprintf("分类不存在：%s", input.Name))
	}

	if !cat.AgentEditable {
		return reject(fmt.Sprintf("分类「%s」由用户维护，不能删除", input.Name))
	}

	if err := tb.repo.ClearPlannedByTargetPath(tb.filesystemID, cat.Path); err != nil {
		return nil, fmt.Errorf("failed to clear planned files: %w", err)
	}

	if err := tb.repo.DeleteCategory(cat.ID); err != nil {
		return nil, fmt.Errorf("delete category: %w", err)
	}

	out := &DeleteCategoryOutput{Success: true}
	return out, nil
}

// Samples several positions so NUL bytes past the first 512 bytes are not
// missed.
func isTextContent(s string) bool {
	if len(s) == 0 {
		return true
	}
	positions := []int{0, len(s) / 2, len(s) - 512}
	for _, start := range positions {
		if start < 0 {
			start = 0
		}
		end := start + 512
		if end > len(s) {
			end = len(s)
		}
		for i := start; i < end; i++ {
			if s[i] == 0 {
				return false
			}
		}
	}
	return true
}
