package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync/atomic"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
	"FileEngine/internal/remotefs"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ============ Tool Input/Output Types ============

type ListFilesInput struct {
	ParentPath   string `json:"parent_path" jsonschema_description:"Parent directory path to list files from"`
	FileType     string `json:"file_type,omitempty" jsonschema_description:"Filter by type: file, directory, symlink"`
	Tagged       *bool  `json:"tagged,omitempty" jsonschema_description:"Filter by tagged status"`
	CategoryPath string `json:"category_path,omitempty" jsonschema_description:"Filter by category path prefix (files whose target starts with this path)"`
	Offset       int    `json:"offset,omitempty" jsonschema_description:"Number of items to skip (default 0)"`
	Limit        int    `json:"limit,omitempty" jsonschema_description:"Max items to return (default 300)"`
}

type ListFilesOutput struct {
	Files []FileItem `json:"files"`
	Total int64      `json:"total"`
}

type FileItem struct {
	OriginalPath string `json:"original_path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	FileType     string `json:"file_type"`
	Description  string `json:"description,omitempty"`
	Tagged       bool   `json:"tagged"`
	NewPath      string `json:"new_path,omitempty"`
	ChildCount   int    `json:"child_count,omitempty"`
	// ArchiveSummary is the one-line listing hint for archive files (only
	// populated on harness untagged pulls — see ToolBuilder.archiveSummaries).
	ArchiveSummary string `json:"archive_summary,omitempty"`
}

type GetFileInfoInput struct {
	Path string `json:"path" jsonschema_description:"Original path of the file/directory"`
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
	Path string `json:"path" jsonschema_description:"Path of the file to read"`
}

type ReadFileOutput struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type UpdateDescriptionInput struct {
	Path        string `json:"path" jsonschema_description:"Original path of the file/directory"`
	Description string `json:"description" jsonschema_description:"AI-generated description"`
}

type UpdateDescriptionOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type MarkTaggedInput struct {
	Path string `json:"path" jsonschema_description:"Original path of the file or directory to mark as tagged (a directory's children are also marked)"`
}

type MarkTaggedOutput struct {
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

type SetTargetInput struct {
	Path    string `json:"path" jsonschema_description:"Original path of the file/directory"`
	NewPath string `json:"new_path" jsonschema_description:"Target path under a category folder. Empty string clears the target"`
}

type SetTargetOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	// ClearedChildren reports how many already-planned descendants the
	// directory cascade overrode (outer target wins over inner). Non-zero
	// means finer child classifications were just discarded.
	ClearedChildren int64  `json:"cleared_children,omitempty"`
	Guidance        string `json:"guidance,omitempty"`
}

type CreateCategoryInput struct {
	Name        string `json:"name" jsonschema_description:"Category name, e.g. 'Photos', 'Software'"`
	Path        string `json:"path" jsonschema_description:"Target path for this category, e.g. '/organized/photos'"`
	Description string `json:"description" jsonschema_description:"What kind of files belong in this category"`
}

type CreateCategoryOutput struct {
	Success bool   `json:"success"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	// Error carries an agent-recoverable rejection (guard rails, duplicates,
	// invalid input) with guidance for the next decision; Success is false.
	// System failures still abort the run as Go errors.
	Error string `json:"error,omitempty"`
	// SimilarCategories lists fuzzy lookalikes found after a successful
	// creation (path overlap/containment) — reference info for the model to
	// judge whether a merge is warranted; empty when there are none.
	SimilarCategories []SimilarCategoryItem `json:"similar_categories,omitempty"`
	Guidance          string                `json:"guidance,omitempty"`
}

// SimilarCategoryItem is one fuzzy-similar existing category attached to a
// successful create_category result.
type SimilarCategoryItem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	FileCount int64  `json:"file_count"`
	Reason    string `json:"reason"`
}

type UpdateCategoryInput struct {
	Name        string `json:"name" jsonschema_description:"Category name to update (used to find the category)"`
	NewName     string `json:"new_name,omitempty" jsonschema_description:"New name for the category (optional)"`
	NewPath     string `json:"new_path,omitempty" jsonschema_description:"New path for the category. Files under old path will be cascaded (optional)"`
	Description string `json:"description,omitempty" jsonschema_description:"New description (optional)"`
	Structure   string `json:"structure,omitempty" jsonschema_description:"New structure hint (optional)"`
}

type UpdateCategoryOutput struct {
	Success bool   `json:"success"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Error   string `json:"error,omitempty"`
}

type DeleteCategoryInput struct {
	Name string `json:"name" jsonschema_description:"Name of the category to delete"`
}

type DeleteCategoryOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// rejectError marks agent-recoverable tool rejections: the model can fix its
// input (guard-rail rejection, duplicate, not-found, invalid argument) and
// continue, so tool handlers report these as success=false results instead of
// aborting the whole agent run — eino makes any tool error fatal to the ReAct
// loop. System failures (DB/FS errors) stay ordinary fatal errors.
type rejectError struct{ msg string }

func (e *rejectError) Error() string { return e.msg }

func rejectf(format string, args ...interface{}) error {
	return &rejectError{msg: fmt.Sprintf(format, args...)}
}

// ============ Tool Builder ============

type ToolBuilder struct {
	repo         *db.Repository
	fs           remotefs.RemoteFS
	sessionID    uint
	filesystemID uint
	logger       *Logger
	cfg          config.AgentConfig
	session      *db.ScanSession
	// modelVisionEnabled is the model-level vision switch
	// (ModelProvider.VisionEnabled); view_image is registered only when it and
	// ScanSession.AllowVision are both on.
	modelVisionEnabled bool
	// guard remembers the category-set snapshot this worker's agent last saw
	// and backs the create_category guard rails (see category_guard.go).
	guard categoryGuard
	// harness, when non-nil, marks this builder as belonging to a continuous
	// (long-run) worker: untagged pulls (list_files / query_files with
	// tagged=false) are auto-sharded by id % numWorkers = workerID so
	// concurrent harness workers never see each other's files, and every
	// such pull rolls a fresh pull on the worker's lane (pull_done for the
	// previous pull + pull_begin for the new one).
	harness *harnessContext
}

type harnessContext struct {
	workerID     int
	numWorkers   int
	batchCounter *int32
	// lastPull remembers the most recent non-empty untagged pull (file ids
	// and label) so the next pull — or harnessSettlePull at run end — can
	// close it out with a pull_done event (tagged/missed settlement).
	lastPullIDs   []uint
	lastPullLabel string
}

// withHarness marks the builder as a continuous worker's toolset.
func (tb *ToolBuilder) withHarness(workerID, numWorkers int, batchCounter *int32) *ToolBuilder {
	tb.harness = &harnessContext{workerID: workerID, numWorkers: numWorkers, batchCounter: batchCounter}
	return tb
}

// harnessShard applies harness semantics to an untagged file query before it
// runs: shard it to this worker so concurrent harness workers never see each
// other's files. Returns true when the query is an untagged pull under an
// active harness — the caller then rolls a new pull once the query comes
// back non-empty (harnessNotePull).
func (tb *ToolBuilder) harnessShard(q *db.FileQuery) bool {
	if tb.harness == nil || q.Tagged == nil || *q.Tagged {
		return false
	}
	if tb.harness.numWorkers > 1 {
		q.IDMod, q.IDModRem = tb.harness.numWorkers, tb.harness.workerID
	}
	return true
}

// harnessNotePull rolls a fresh pull for a non-empty untagged pull in a
// continuous run: settles the previous pull (pull_done), advances the
// internal batch cursor, and announces the new pull (pull_begin).
func (tb *ToolBuilder) harnessNotePull(label string, files []db.FileEntry) {
	if tb.harness == nil || len(files) == 0 {
		return
	}
	tb.harnessSettlePull()
	batchIdx := atomic.AddInt32(tb.harness.batchCounter, 1)
	tb.logger.SetBatch(int(batchIdx))
	ids := make([]uint, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	tb.logger.EmitPullBegin(label, len(files), minFileID(files))
	tb.harness.lastPullIDs = ids
	tb.harness.lastPullLabel = label
}

// harnessSettlePull closes out the last open pull with a pull_done event:
// files tagged since the pull count as classified, the rest as missed.
// Called before each new pull and once at run end (the agent's tool stream
// is sequential, so by the next pull the previous one has been processed).
func (tb *ToolBuilder) harnessSettlePull() {
	if tb.harness == nil || len(tb.harness.lastPullIDs) == 0 {
		return
	}
	ids := tb.harness.lastPullIDs
	label := tb.harness.lastPullLabel
	tb.harness.lastPullIDs = nil
	tagged, err := tb.repo.TaggedStatusByIDs(ids)
	if err != nil {
		log.Printf("harness worker %d: pull settle failed, pull_done skipped: %v", tb.harness.workerID, err)
		return
	}
	taggedCount := 0
	for _, id := range ids {
		if tagged[id] {
			taggedCount++
		}
	}
	tb.logger.EmitPullDone(label, taggedCount, len(ids)-taggedCount)
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

func (tb *ToolBuilder) BuildTools() ([]tool.BaseTool, error) {
	listFiles, err := utils.InferTool("list_files", "列出目录中的文件。已打标的文件会包含描述和目标路径。支持 offset+limit 分页（默认 offset=0, limit=300），如果 total > limit 需要用 offset 翻页。可用 category_path 过滤某分类下已规划的文件", tb.listFiles)
	if err != nil {
		return nil, fmt.Errorf("build list_files: %w", err)
	}

	getFileInfo, err := utils.InferTool("get_file_info", "获取单个文件或目录的详细元数据", tb.getFileInfo)
	if err != nil {
		return nil, fmt.Errorf("build get_file_info: %w", err)
	}

	readFile, err := utils.InferTool("read_file", "从远程文件系统读取文件内容：文本按大小截断；PDF/docx/xlsx/pptx 提取文本（.doc 不支持）；zip/tar/tar.gz 列出包内目录，用 path::entry 语法读取包内小文本条目（rar/7z 不支持；无随机访问的后端超大压缩包不可用）", tb.readFile)
	if err != nil {
		return nil, fmt.Errorf("build read_file: %w", err)
	}

	updateDesc, err := utils.InferTool("update_description", "为文件或目录设置 AI 生成的描述", tb.updateDescription)
	if err != nil {
		return nil, fmt.Errorf("build update_description: %w", err)
	}

	markTagged, err := utils.InferTool("mark_tagged", "标记文件或目录为已处理；目录的所有子项会级联标记。散文件批次中每个文件处理完都要单独标记", tb.markTagged)
	if err != nil {
		return nil, fmt.Errorf("build mark_tagged: %w", err)
	}

	listCats, err := utils.InferTool("list_categories", "列出所有用户定义的分类目录", tb.listCategories)
	if err != nil {
		return nil, fmt.Errorf("build list_categories: %w", err)
	}

	setTarget, err := utils.InferTool("set_target", "为文件/目录设置整理目标路径（仅修改数据库，不操作实际文件）。new_path 传空字符串可清除已有目标", tb.setTarget)
	if err != nil {
		return nil, fmt.Errorf("build set_target: %w", err)
	}

	allTools := []tool.BaseTool{listFiles, getFileInfo, updateDesc, markTagged, listCats, setTarget}

	// update_category is always available
	updateCat, err := utils.InferTool("update_category", "修改已有分类的名称、路径、描述等。如果修改了路径，该分类下所有文件的目标路径会自动更新", tb.updateCategory)
	if err != nil {
		return nil, fmt.Errorf("build update_category: %w", err)
	}
	allTools = append(allTools, updateCat)

	deleteCat, err := utils.InferTool("delete_category", "删除一个分类。合并分类时先将文件迁移到目标分类再删除旧分类", tb.deleteCategory)
	if err != nil {
		return nil, fmt.Errorf("build delete_category: %w", err)
	}
	allTools = append(allTools, deleteCat)

	if tb.session.AllowReadFile {
		allTools = append(allTools, readFile)
	}

	if tb.session.AllowAutoCategory {
		createCat, err := utils.InferTool("create_category", "当没有合适的现有分类时创建新分类，谨慎使用。受护栏校验：分类列表自你上次查看后有变化会被拒绝并返回最新列表；同名（含大小写变体）或同路径的重复创建会被拒绝。创建成功时若存在路径重叠的相似分类，返回值会附 similar_categories 参考，请评估是否归并"+categoryPolicySuffix(tb.cfg.CategoryPolicy), tb.createCategory)
		if err != nil {
			return nil, fmt.Errorf("build create_category: %w", err)
		}
		allTools = append(allTools, createCat)
	}

	if visionTool, err := tb.buildVisionTool(); err != nil {
		return nil, err
	} else if visionTool != nil {
		allTools = append(allTools, visionTool)
	}

	if scriptTool, err := tb.buildScriptTool(); err != nil {
		return nil, err
	} else if scriptTool != nil {
		allTools = append(allTools, scriptTool)
	}

	return allTools, nil
}

// buildVisionTool returns the view_image tool when both vision switches are on
// (model-level ModelProvider.VisionEnabled ∩ session-level AllowVision), or
// (nil, nil) when gated off. Default state is off in both places, so the tool
// is absent unless explicitly enabled.
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

// BuildInstructTools returns tools for user-directed instruct mode.
func (tb *ToolBuilder) BuildInstructTools() ([]tool.BaseTool, error) {
	listFiles, err := utils.InferTool("list_files", "列出目录中的文件，支持 offset+limit 分页。可用 category_path 过滤某分类下已规划的文件", tb.listFiles)
	if err != nil {
		return nil, err
	}
	getFileInfo, err := utils.InferTool("get_file_info", "获取单个文件或目录的详细元数据", tb.getFileInfo)
	if err != nil {
		return nil, err
	}
	updateDesc, err := utils.InferTool("update_description", "修改文件/目录的描述", tb.updateDescription)
	if err != nil {
		return nil, err
	}
	markTagged, err := utils.InferTool("mark_tagged", "标记文件或目录为已处理；目录的所有子项会级联标记。散文件批次中每个文件处理完都要单独标记", tb.markTagged)
	if err != nil {
		return nil, err
	}
	setTarget, err := utils.InferTool("set_target", "设置文件/目录的整理目标路径。new_path 传空字符串可清除已有目标", tb.setTarget)
	if err != nil {
		return nil, err
	}
	listCats, err := utils.InferTool("list_categories", "列出所有分类目录", tb.listCategories)
	if err != nil {
		return nil, err
	}
	allTools := []tool.BaseTool{listFiles, getFileInfo, updateDesc, markTagged, setTarget, listCats}

	updateCat, err := utils.InferTool("update_category", "修改已有分类的名称、路径、描述。路径变更会级联更新文件", tb.updateCategory)
	if err != nil {
		return nil, err
	}
	allTools = append(allTools, updateCat)

	deleteCat, err := utils.InferTool("delete_category", "删除一个分类。合并分类时先将文件迁移到目标分类再删除旧分类", tb.deleteCategory)
	if err != nil {
		return nil, err
	}
	allTools = append(allTools, deleteCat)

	if tb.session.AllowReadFile {
		readFile, err := utils.InferTool("read_file", "从远程文件系统读取文件内容：文本按大小截断；PDF/docx/xlsx/pptx 提取文本（.doc 不支持）；zip/tar/tar.gz 列出包内目录，用 path::entry 语法读取包内小文本条目（rar/7z 不支持；无随机访问的后端超大压缩包不可用）", tb.readFile)
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, readFile)
	}

	if tb.session.AllowAutoCategory {
		createCat, err := utils.InferTool("create_category", "创建新分类目录。受护栏校验：需先 list_categories 了解最新分类；同名（含大小写变体）或同路径的重复会被拒绝；创建成功时可能附 similar_categories 相似参考，请评估归并"+categoryPolicySuffix(tb.cfg.CategoryPolicy), tb.createCategory)
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, createCat)
	}

	if visionTool, err := tb.buildVisionTool(); err != nil {
		return nil, err
	} else if visionTool != nil {
		allTools = append(allTools, visionTool)
	}

	if scriptTool, err := tb.buildScriptTool(); err != nil {
		return nil, err
	} else if scriptTool != nil {
		allTools = append(allTools, scriptTool)
	}
	return allTools, nil
}

// ============ Tool Implementations ============

// normalizePath strips leading slash to match DB storage format.
func normalizePath(p string) string {
	return strings.TrimPrefix(p, "/")
}

func ptrStr(s string) *string {
	return &s
}

func (tb *ToolBuilder) listFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {

	limit := input.Limit
	if limit <= 0 {
		limit = 300
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	// Convert offset+limit to page+pageSize for the repository
	page := (offset / limit) + 1
	q := db.FileQuery{
		SessionID:  tb.sessionID,
		ParentPath: ptrStr(normalizePath(input.ParentPath)),
		FileType:   input.FileType,
		Tagged:     input.Tagged,
		Page:       page,
		PageSize:   limit,
	}
	if input.CategoryPath != "" {
		q.CategoryPath = &input.CategoryPath
	}

	isPull := tb.harnessShard(&q)
	files, total, err := tb.repo.ListFiles(q)
	if err != nil {
		return nil, err
	}
	if isPull && total > 0 {
		// A fresh untagged pull in a continuous run rolls a new pull on the
		// worker's lane (emitted before the tool log so it orders correctly).
		tb.harnessNotePull(fmt.Sprintf("%d files", len(files)), files)
	}

	items := make([]FileItem, len(files))
	for i, f := range files {
		items[i] = FileItem{
			OriginalPath: f.OriginalPath,
			Name:         f.Name,
			Size:         f.Size,
			FileType:     f.FileType,
			Description:  f.Description,
			Tagged:       f.Tagged,
			NewPath:      f.NewPath,
			ChildCount:   f.ChildCount,
		}
	}
	if isPull && total > 0 {
		// Harness pulls carry the same archive listing summaries the batch
		// prompt promises (bounded per pull — see archiveSummaries).
		summaries := tb.archiveSummaries(ctx, files)
		for i, f := range files {
			if s := summaries[f.ID]; s != "" {
				items[i].ArchiveSummary = s
			}
		}
	}

	out := &ListFilesOutput{Files: items, Total: total}
	tb.logger.LogToolCall("list_files", input, out)
	return out, nil
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
	tb.logger.LogToolCall("get_file_info", input, out)
	return out, nil
}

func (tb *ToolBuilder) readFile(ctx context.Context, input *ReadFileInput) (*ReadFileOutput, error) {
	// Archive entry syntax: "path/to/archive.zip::entry/inside.txt". Split
	// first; the filesystem-facing part is validated like every other path.
	archivePath, entryName, hasEntry := strings.Cut(input.Path, "::")
	if err := pathutil.ValidateFSPath(archivePath); err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	if hasEntry && entryName == "" {
		return nil, fmt.Errorf("empty archive entry name in %q", input.Path)
	}

	maxSize := tb.cfg.MaxFileReadSize
	if maxSize <= 0 {
		maxSize = 102400
	}

	ext := documentExt(archivePath)
	arch := archiveKind(archivePath)
	if hasEntry && arch == "" {
		return nil, fmt.Errorf("entry syntax (::) is only valid for zip/tar archives: %q", input.Path)
	}

	if arch != "" {
		return tb.readArchive(ctx, input, archivePath, entryName, arch, maxSize)
	}

	// Document formats (pdf/docx/xlsx/pptx) must be parsed whole — zip
	// central directories and PDF xref tables live at the end of the file —
	// so they get a larger read budget; the extracted text is still capped
	// to maxSize runes. Everything else stays on the text path.
	readCap := int64(maxSize) + 1
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
		tb.logger.LogToolCall("read_file", input, out)
		return out, nil
	}

	// Peek at the first 1KB to detect binary content.
	peek := data
	if len(peek) > 1024 {
		peek = peek[:1024]
	}
	if len(peek) > 0 && !isTextContent(string(peek)) {
		out := &ReadFileOutput{Content: "[binary file - content not displayable]", Truncated: false}
		tb.logger.LogToolCall("read_file", input, out)
		return out, nil
	}

	truncated := len(data) > maxSize
	if truncated {
		data = data[:maxSize]
	}

	out := &ReadFileOutput{Content: string(data), Truncated: truncated}
	tb.logger.LogToolCall("read_file", input, out)
	return out, nil
}

// readArchive handles zip/tar/tar.gz files: a capped listing of the archive's
// directory, or one entry's text via the path::entry syntax. rar/7z are
// explicitly unsupported. zip needs random access (central directory at the
// end of the file); how that is served per backend is in
// resolveArchiveReaderAt — stream-only backends beyond the download cap get
// an explicit degradation notice, never a silent retry.
func (tb *ToolBuilder) readArchive(ctx context.Context, input *ReadFileInput, archivePath, entryName, kind string, maxChars int) (*ReadFileOutput, error) {
	log := func(out *ReadFileOutput) (*ReadFileOutput, error) {
		tb.logger.LogToolCall("read_file", input, out)
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

	// zip: random access required. Entry reads are gated on the archive size
	// ("包整体过大时只列目录"), listing is not — the central directory alone
	// is cheap on random-access backends.
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

// capRunes truncates s to maxChars runes, reporting whether it cut anything.
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
		out := &UpdateDescriptionOutput{Error: fmt.Sprintf("file not found: %s", input.Path)}
		tb.logger.LogToolCall("update_description", input, out)
		return out, nil
	}

	// Idempotency check: description already matches
	if f.Description == input.Description {
		out := &UpdateDescriptionOutput{Success: true}
		tb.logger.LogToolCall("update_description", input, out)
		return out, nil
	}

	// Column-scoped write: a full-row Save of the struct loaded above races
	// with parallel tool calls on the same row (e.g. set_target) and would
	// silently restore their fields to stale values.
	if err := tb.repo.UpdateDescription(tb.sessionID, normalizePath(input.Path), input.Description); err != nil {
		return nil, err
	}

	out := &UpdateDescriptionOutput{Success: true}
	tb.logger.LogToolCall("update_description", input, out)
	return out, nil
}

func (tb *ToolBuilder) markTagged(ctx context.Context, input *MarkTaggedInput) (*MarkTaggedOutput, error) {

	f, err := tb.repo.GetFileByPath(tb.sessionID, normalizePath(input.Path))
	if err != nil {
		out := &MarkTaggedOutput{Error: fmt.Sprintf("file not found: %s", input.Path)}
		tb.logger.LogToolCall("mark_tagged", input, out)
		return out, nil
	}

	// Idempotency check: already tagged, skip DB write
	if f.Tagged {
		out := &MarkTaggedOutput{Success: true}
		tb.logger.LogToolCall("mark_tagged", input, out)
		return out, nil
	}

	batch := tb.logger.GetBatch()
	if err := tb.repo.MarkTaggedAtomic(tb.sessionID, normalizePath(input.Path), batch); err != nil {
		return nil, err
	}

	out := &MarkTaggedOutput{Success: true}
	tb.logger.LogToolCall("mark_tagged", input, out)
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

	// The agent is receiving the category list — record its snapshot for the
	// create_category version guard.
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	tb.guard.note(names)

	out := &ListCategoriesOutput{Categories: items}
	tb.logger.LogToolCall("list_categories", input, out)
	return out, nil
}

func (tb *ToolBuilder) setTarget(ctx context.Context, input *SetTargetInput) (*SetTargetOutput, error) {
	cleared, err := tb.setTargetCore(ctx, input.Path, input.NewPath)
	if err != nil {
		var rj *rejectError
		if errors.As(err, &rj) {
			out := &SetTargetOutput{Error: rj.msg}
			tb.logger.LogToolCall("set_target", input, out)
			return out, nil
		}
		return nil, err
	}
	out := &SetTargetOutput{Success: true}
	if cleared > 0 {
		out.ClearedChildren = cleared
		out.Guidance = fmt.Sprintf("已级联覆盖并清除 %d 个子项的既有规划路径（外层目标覆盖内层）。若这些子项需要更细的独立归类，请对它们重新 set_target；若本目录就应作为整体归类，则无需处理", cleared)
	}
	tb.logger.LogToolCall("set_target", input, out)
	return out, nil
}

// setTargetCore is the set_target logic shared by the tool handler and the
// run_script builtin: same validation, same idempotency, same atomic write.
// Only the tool handler logs; script-internal calls are covered by the single
// run_script log entry. Returns how many planned children the cascade cleared.
func (tb *ToolBuilder) setTargetCore(ctx context.Context, path, newPath string) (int64, error) {
	// Validate before any DB access: an illegal target must be rejected
	// loudly, never written. The executor resolves new_path against the
	// filesystem root, so anything that could escape it is refused here.
	if err := pathutil.ValidateFSPath(newPath); err != nil {
		return 0, rejectf("invalid new_path: %v", err)
	}

	f, err := tb.repo.GetFileByPath(tb.sessionID, normalizePath(path))
	if err != nil {
		return 0, rejectf("file not found: %s", path)
	}

	op := ""
	if newPath != "" {
		op = "planned"
	}

	// Idempotency check: already in target state, skip DB write
	if f.NewPath == newPath && f.Operation == op {
		return 0, nil
	}

	cleared, err := tb.repo.SetTargetAtomic(tb.sessionID, normalizePath(path), newPath, op, f.FileType)
	if err != nil {
		return 0, fmt.Errorf("failed to set target: %w", err)
	}
	return cleared, nil
}

func (tb *ToolBuilder) createCategory(ctx context.Context, input *CreateCategoryInput) (*CreateCategoryOutput, error) {
	out, err := tb.createCategoryCore(ctx, input)
	if err != nil {
		// Guard rejections are guidance, not failure: hand them to the model
		// as a success=false result so the run continues and the next
		// decision is made from the rejection's reference info.
		var rj *rejectError
		if errors.As(err, &rj) {
			out := &CreateCategoryOutput{Error: rj.msg}
			tb.logger.LogToolCall("create_category", input, out)
			return out, nil
		}
		return nil, err
	}
	tb.logger.LogToolCall("create_category", input, out)
	return out, nil
}

// createCategoryCore is the create_category logic shared by the tool handler
// and the run_script builtin: ValidateFSPath, the categoryGuard version
// fingerprint, and the M14 similarity semantics all live here so a script can
// never bypass the guard rails. Only the tool handler logs.
func (tb *ToolBuilder) createCategoryCore(ctx context.Context, input *CreateCategoryInput) (*CreateCategoryOutput, error) {

	// Category paths become new_path prefixes for planned files; apply the
	// same root-confinement rules as set_target.
	if err := pathutil.ValidateFSPath(input.Path); err != nil || input.Path == "" {
		if err == nil {
			err = fmt.Errorf("path must not be empty")
		}
		return nil, rejectf("invalid category path: %v", err)
	}

	// Fresh category state feeds both guard rails below (and its per-category
	// file counts feed the reference info in rejections).
	usages, err := tb.repo.GetCategoryUsageSummary(tb.filesystemID, tb.sessionID)
	if err != nil {
		return nil, fmt.Errorf("create category: read current categories: %w", err)
	}
	names := make([]string, len(usages))
	for i, u := range usages {
		names[i] = u.Name
	}

	// Version guard: creation is only allowed from a category list the agent
	// has just seen (batch-prompt summary or list_categories). If the set
	// changed since — or the agent never saw one — reject and hand back the
	// latest complete list. The rejection doubles as the agent's re-read, so
	// it is also recorded as the new snapshot: a retry decided from that list
	// passes (unless the set changed again).
	if lastFP, seen := tb.guard.last(); !seen || lastFP != categoryFingerprint(names) {
		tb.guard.note(names)
		list := strings.TrimSpace(buildCategorySummary(usages))
		if list == "" {
			list = "（当前没有任何分类）"
		}
		return nil, rejectf("分类列表自你上次查看后已变化（或你尚未查看过），本次创建已拒绝。最新完整分类列表：\n%s\n请基于该列表重新决定：优先用 set_target 把文件归入现有分类；确认没有合适分类后，再重新调用 create_category", list)
	}

	// Similarity guard, two classes: objective duplicates (normalized identical
	// name or path) are hard-rejected with the existing category as reference;
	// fuzzy lookalikes (path containment/prefix overlap) never block — the
	// creation proceeds and they are attached to the result as reference info
	// so the model itself can judge whether to merge.
	hard, soft := findSimilarCategories(usages, input.Name, input.Path)
	if len(hard) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("已存在相同分类（名称或路径重复），拒绝创建 %q (%s)：\n", input.Name, input.Path))
		for _, s := range hard {
			sb.WriteString(fmt.Sprintf("- %s (%s): 已归入 %d 项（%s）\n", s.usage.Name, s.usage.Path, s.usage.FileCount, s.reason))
		}
		sb.WriteString("请改用 set_target 把文件归入上述现有分类（或用 update_category 调整它）；若确需新分类，换用不同的名称和路径")
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
		return nil, rejectf("category with name '%s' or path '%s' already exists. Use set_target to assign files to it, or update_category to modify it", input.Name, input.Path)
	}

	// The agent knows the post-create set (its snapshot plus its own
	// creation), so back-to-back creations are not re-challenged; a change
	// made by anyone else still mismatches and forces a re-read.
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
		out.Guidance = "提示：存在路径重叠的相似分类（见 similar_categories）。若语义重复，请改用 set_target 把文件归入现有分类（或用 update_category/delete_category 归并）；确认确有必要则无需处理"
	}
	return out, nil
}

func (tb *ToolBuilder) updateCategory(ctx context.Context, input *UpdateCategoryInput) (*UpdateCategoryOutput, error) {

	reject := func(msg string) (*UpdateCategoryOutput, error) {
		out := &UpdateCategoryOutput{Error: msg}
		tb.logger.LogToolCall("update_category", input, out)
		return out, nil
	}

	cat, err := tb.repo.GetCategoryByName(tb.filesystemID, input.Name)
	if err != nil {
		return reject(fmt.Sprintf("category not found: %s", input.Name))
	}

	if !cat.AgentEditable {
		return reject(fmt.Sprintf("category '%s' is not editable by agent (user-managed)", input.Name))
	}

	// Guard against no-op: at least one field must be provided for update
	if input.NewName == "" && input.NewPath == "" && input.Description == "" && input.Structure == "" {
		return reject(fmt.Sprintf("no update fields provided for category '%s'. Provide at least one of: new_name, new_path, description, structure", input.Name))
	}

	// A changed category path cascades into planned files' new_path; apply the
	// same root-confinement rules as set_target.
	if input.NewPath != "" {
		if err := pathutil.ValidateFSPath(input.NewPath); err != nil {
			return reject(fmt.Sprintf("invalid new_path: %v", err))
		}
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
	tb.logger.LogToolCall("update_category", input, out)
	return out, nil
}

func (tb *ToolBuilder) deleteCategory(ctx context.Context, input *DeleteCategoryInput) (*DeleteCategoryOutput, error) {

	reject := func(msg string) (*DeleteCategoryOutput, error) {
		out := &DeleteCategoryOutput{Error: msg}
		tb.logger.LogToolCall("delete_category", input, out)
		return out, nil
	}

	cat, err := tb.repo.GetCategoryByName(tb.filesystemID, input.Name)
	if err != nil {
		return reject(fmt.Sprintf("category not found: %s", input.Name))
	}

	if !cat.AgentEditable {
		return reject(fmt.Sprintf("category '%s' is not deletable by agent (user-managed)", input.Name))
	}

	// Clear all planned files under this category and reset tagged for re-classification
	if err := tb.repo.ClearPlannedByTargetPath(tb.filesystemID, cat.Path); err != nil {
		return nil, fmt.Errorf("failed to clear planned files: %w", err)
	}

	if err := tb.repo.DeleteCategory(cat.ID); err != nil {
		return nil, fmt.Errorf("delete category: %w", err)
	}

	out := &DeleteCategoryOutput{Success: true}
	tb.logger.LogToolCall("delete_category", input, out)
	return out, nil
}

// isTextContent checks if content appears to be text (not binary).
// It samples multiple positions in the string to avoid missing null bytes
// that appear after the first 512 bytes.
func isTextContent(s string) bool {
	if len(s) == 0 {
		return true
	}
	// Sample positions: start, middle, end
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
