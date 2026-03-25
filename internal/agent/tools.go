package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ============ Tool Input/Output Types ============

type ListFilesInput struct {
	ParentPath string `json:"parent_path" jsonschema_description:"Parent directory path to list files from"`
	FileType   string `json:"file_type,omitempty" jsonschema_description:"Filter by type: file, directory, symlink"`
	Tagged     *bool  `json:"tagged,omitempty" jsonschema_description:"Filter by tagged status"`
	Offset     int    `json:"offset,omitempty" jsonschema_description:"Number of items to skip (default 0)"`
	Limit      int    `json:"limit,omitempty" jsonschema_description:"Max items to return (default 300)"`
}

type ListFilesOutput struct {
	Files  []FileItem `json:"files"`
	Total  int64      `json:"total"`
	Offset int        `json:"offset"`
	Limit  int        `json:"limit"`
}

type FileItem struct {
	ID           uint   `json:"id"`
	OriginalPath string `json:"original_path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	FileType     string `json:"file_type"`
	Description  string `json:"description,omitempty"`
	Tagged       bool   `json:"tagged"`
	NewPath      string `json:"new_path,omitempty"`
	ChildCount   int    `json:"child_count,omitempty"`
}

type GetFileInfoInput struct {
	Path string `json:"path" jsonschema_description:"Original path of the file/directory"`
}

type GetFileInfoOutput struct {
	ID           uint   `json:"id"`
	OriginalPath string `json:"original_path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	FileType     string `json:"file_type"`
	ModTime      string `json:"mod_time"`
	Permissions  string `json:"permissions"`
	Description  string `json:"description"`
	Tagged       bool   `json:"tagged"`
	NewPath      string `json:"new_path"`
	ParentPath   string `json:"parent_path"`
	Depth        int    `json:"depth"`
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
	Success bool `json:"success"`
}

type MarkTaggedInput struct {
	Path string `json:"path" jsonschema_description:"Original path of the directory to mark as tagged (children are also marked)"`
}

type MarkTaggedOutput struct {
	Success       bool `json:"success"`
	ChildrenCount int  `json:"children_count"`
}

type ListCategoriesInput struct{}

type ListCategoriesOutput struct {
	Categories []CategoryItem `json:"categories"`
}

type CategoryItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Structure   string `json:"structure,omitempty"`
	Description string `json:"description"`
}

type ListCategoryFilesInput struct {
	CategoryPath string `json:"category_path" jsonschema_description:"Category path to list files from (the new_path prefix)"`
	Offset       int    `json:"offset,omitempty" jsonschema_description:"Number of items to skip (default 0)"`
	Limit        int    `json:"limit,omitempty" jsonschema_description:"Max items to return (default 300)"`
}

type ListCategoryFilesOutput struct {
	Files  []FileItem `json:"files"`
	Total  int64      `json:"total"`
	Offset int        `json:"offset"`
	Limit  int        `json:"limit"`
}

type SetTargetInput struct {
	Path    string `json:"path" jsonschema_description:"Original path of the file/directory"`
	NewPath string `json:"new_path" jsonschema_description:"Target path under a category folder"`
}

type SetTargetOutput struct {
	Success bool `json:"success"`
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
}

func NewToolBuilder(repo *db.Repository, fs remotefs.RemoteFS, sessionID uint, filesystemID uint, logger *Logger, cfg config.AgentConfig, session *db.ScanSession) *ToolBuilder {
	return &ToolBuilder{
		repo:         repo,
		fs:           fs,
		sessionID:    sessionID,
		filesystemID: filesystemID,
		logger:       logger,
		cfg:          cfg,
		session:      session,
	}
}

func (tb *ToolBuilder) BuildTools() ([]tool.BaseTool, error) {
	listFiles, err := utils.InferTool("list_files", "列出目录中的文件。已打标的文件会包含描述和目标路径。支持 offset+limit 分页（默认 offset=0, limit=300），如果 total > limit 需要用 offset 翻页", tb.listFiles)
	if err != nil {
		return nil, fmt.Errorf("build list_files: %w", err)
	}

	getFileInfo, err := utils.InferTool("get_file_info", "获取单个文件或目录的详细元数据", tb.getFileInfo)
	if err != nil {
		return nil, fmt.Errorf("build get_file_info: %w", err)
	}

	readFile, err := utils.InferTool("read_file", "从远程文件系统读取文件内容，仅限文本文件且有大小限制", tb.readFile)
	if err != nil {
		return nil, fmt.Errorf("build read_file: %w", err)
	}

	updateDesc, err := utils.InferTool("update_description", "为文件或目录设置 AI 生成的描述", tb.updateDescription)
	if err != nil {
		return nil, fmt.Errorf("build update_description: %w", err)
	}

	markTagged, err := utils.InferTool("mark_tagged", "标记目录为已处理，所有子项也会被标记", tb.markTagged)
	if err != nil {
		return nil, fmt.Errorf("build mark_tagged: %w", err)
	}

	listCats, err := utils.InferTool("list_categories", "列出所有用户定义的分类目录", tb.listCategories)
	if err != nil {
		return nil, fmt.Errorf("build list_categories: %w", err)
	}

	setTarget, err := utils.InferTool("set_target", "为文件/目录设置整理目标路径（仅修改数据库，不操作实际文件）", tb.setTarget)
	if err != nil {
		return nil, fmt.Errorf("build set_target: %w", err)
	}

	listCatFiles, err := utils.InferTool("list_category_files", "列出某个分类下已规划的文件，支持 offset+limit 分页。用于了解分类现状以做重构决策", tb.listCategoryFiles)
	if err != nil {
		return nil, fmt.Errorf("build list_category_files: %w", err)
	}

	allTools := []tool.BaseTool{listFiles, getFileInfo, updateDesc, markTagged, listCats, listCatFiles, setTarget}

	// update_category is always available
	updateCat, err := utils.InferTool("update_category", "修改已有分类的名称、路径、描述等。如果修改了路径，该分类下所有文件的目标路径会自动更新", tb.updateCategory)
	if err != nil {
		return nil, fmt.Errorf("build update_category: %w", err)
	}
	allTools = append(allTools, updateCat)

	if tb.session.AllowReadFile {
		allTools = append(allTools, readFile)
	}

	if tb.session.AllowAutoCategory {
		createCat, err := utils.InferTool("create_category", "当没有合适的现有分类时创建新分类，谨慎使用", tb.createCategory)
		if err != nil {
			return nil, fmt.Errorf("build create_category: %w", err)
		}
		allTools = append(allTools, createCat)
	}

	return allTools, nil
}

// BuildInstructTools returns tools for user-directed instruct mode (no list_files, read_file, mark_tagged).
func (tb *ToolBuilder) BuildInstructTools() ([]tool.BaseTool, error) {
	updateDesc, err := utils.InferTool("update_description", "修改文件/目录的描述", tb.updateDescription)
	if err != nil {
		return nil, err
	}
	setTarget, err := utils.InferTool("set_target", "设置文件/目录的整理目标路径", tb.setTarget)
	if err != nil {
		return nil, err
	}
	listCats, err := utils.InferTool("list_categories", "列出所有分类目录", tb.listCategories)
	if err != nil {
		return nil, err
	}
	listCatFiles, err := utils.InferTool("list_category_files", "列出某个分类下已规划的文件", tb.listCategoryFiles)
	if err != nil {
		return nil, err
	}
	allTools := []tool.BaseTool{updateDesc, setTarget, listCats, listCatFiles}

	updateCat, err := utils.InferTool("update_category", "修改已有分类的名称、路径、描述。路径变更会级联更新文件", tb.updateCategory)
	if err != nil {
		return nil, err
	}
	allTools = append(allTools, updateCat)

	if tb.session.AllowAutoCategory {
		createCat, err := utils.InferTool("create_category", "创建新分类目录", tb.createCategory)
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, createCat)
	}
	return allTools, nil
}

// ============ Tool Implementations ============

func (tb *ToolBuilder) listFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {
	tb.logger.LogToolCall("list_files", input, nil)

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
		ParentPath: &input.ParentPath,
		FileType:   input.FileType,
		Tagged:     input.Tagged,
		Page:       page,
		PageSize:   limit,
	}

	files, total, err := tb.repo.ListFiles(q)
	if err != nil {
		return nil, err
	}

	items := make([]FileItem, len(files))
	for i, f := range files {
		items[i] = FileItem{
			ID:           f.ID,
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

	out := &ListFilesOutput{Files: items, Total: total, Offset: offset, Limit: limit}
	tb.logger.LogToolCall("list_files", input, out)
	return out, nil
}

func (tb *ToolBuilder) getFileInfo(ctx context.Context, input *GetFileInfoInput) (*GetFileInfoOutput, error) {
	tb.logger.LogToolCall("get_file_info", input, nil)

	f, err := tb.repo.GetFileByPath(tb.sessionID, input.Path)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", input.Path)
	}

	out := &GetFileInfoOutput{
		ID:           f.ID,
		OriginalPath: f.OriginalPath,
		Name:         f.Name,
		Size:         f.Size,
		FileType:     f.FileType,
		ModTime:      f.ModTime.Format("2006-01-02 15:04:05"),
		Permissions:  f.Permissions,
		Description:  f.Description,
		Tagged:       f.Tagged,
		NewPath:      f.NewPath,
		ParentPath:   f.ParentPath,
		Depth:        f.Depth,
		ChildCount:   f.ChildCount,
	}
	tb.logger.LogToolCall("get_file_info", input, out)
	return out, nil
}

func (tb *ToolBuilder) readFile(ctx context.Context, input *ReadFileInput) (*ReadFileOutput, error) {
	tb.logger.LogToolCall("read_file", input, nil)

	maxSize := tb.cfg.MaxFileReadSize
	if maxSize <= 0 {
		maxSize = 102400
	}

	reader, err := tb.fs.ReadFile(ctx, input.Path)
	if err != nil {
		return nil, fmt.Errorf("cannot read file: %w", err)
	}
	defer reader.Close()

	buf := make([]byte, maxSize+1)
	n, err := io.ReadFull(reader, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("read error: %w", err)
	}

	truncated := n > maxSize
	if truncated {
		n = maxSize
	}

	content := string(buf[:n])
	if !isTextContent(content) {
		out := &ReadFileOutput{Content: "[binary file - content not displayable]", Truncated: false}
		tb.logger.LogToolCall("read_file", input, out)
		return out, nil
	}

	out := &ReadFileOutput{Content: content, Truncated: truncated}
	tb.logger.LogToolCall("read_file", input, out)
	return out, nil
}

func (tb *ToolBuilder) updateDescription(ctx context.Context, input *UpdateDescriptionInput) (*UpdateDescriptionOutput, error) {
	tb.logger.LogToolCall("update_description", input, nil)

	f, err := tb.repo.GetFileByPath(tb.sessionID, input.Path)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", input.Path)
	}

	f.Description = input.Description
	if err := tb.repo.UpdateFile(f); err != nil {
		return nil, err
	}

	out := &UpdateDescriptionOutput{Success: true}
	tb.logger.LogToolCall("update_description", input, out)
	return out, nil
}

func (tb *ToolBuilder) markTagged(ctx context.Context, input *MarkTaggedInput) (*MarkTaggedOutput, error) {
	tb.logger.LogToolCall("mark_tagged", input, nil)

	f, err := tb.repo.GetFileByPath(tb.sessionID, input.Path)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", input.Path)
	}

	f.Tagged = true
	if err := tb.repo.UpdateFile(f); err != nil {
		return nil, err
	}

	if err := tb.repo.MarkChildrenTagged(tb.sessionID, input.Path); err != nil {
		return nil, err
	}

	out := &MarkTaggedOutput{Success: true}
	tb.logger.LogToolCall("mark_tagged", input, out)
	return out, nil
}

func (tb *ToolBuilder) listCategories(ctx context.Context, input *ListCategoriesInput) (*ListCategoriesOutput, error) {
	tb.logger.LogToolCall("list_categories", input, nil)

	cats, err := tb.repo.ListCategories(tb.filesystemID)
	if err != nil {
		return nil, err
	}

	items := make([]CategoryItem, len(cats))
	for i, c := range cats {
		items[i] = CategoryItem{
			Name:        c.Name,
			Path:        c.Path,
			Structure:   c.Structure,
			Description: c.Description,
		}
	}

	out := &ListCategoriesOutput{Categories: items}
	tb.logger.LogToolCall("list_categories", input, out)
	return out, nil
}

func (tb *ToolBuilder) listCategoryFiles(ctx context.Context, input *ListCategoryFilesInput) (*ListCategoryFilesOutput, error) {
	tb.logger.LogToolCall("list_category_files", input, nil)

	limit := input.Limit
	if limit <= 0 {
		limit = 300
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}

	page := (offset / limit) + 1
	categoryPath := input.CategoryPath
	q := db.FileQuery{
		SessionID:    tb.sessionID,
		CategoryPath: &categoryPath,
		Page:         page,
		PageSize:     limit,
	}

	files, total, err := tb.repo.ListFiles(q)
	if err != nil {
		return nil, err
	}

	items := make([]FileItem, len(files))
	for i, f := range files {
		items[i] = FileItem{
			ID: f.ID, OriginalPath: f.OriginalPath, Name: f.Name,
			Size: f.Size, FileType: f.FileType, Description: f.Description,
			Tagged: f.Tagged, NewPath: f.NewPath, ChildCount: f.ChildCount,
		}
	}

	out := &ListCategoryFilesOutput{Files: items, Total: total, Offset: offset, Limit: limit}
	tb.logger.LogToolCall("list_category_files", input, out)
	return out, nil
}

// PLACEHOLDER_SETTARGET

func (tb *ToolBuilder) setTarget(ctx context.Context, input *SetTargetInput) (*SetTargetOutput, error) {
	tb.logger.LogToolCall("set_target", input, nil)

	f, err := tb.repo.GetFileByPath(tb.sessionID, input.Path)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", input.Path)
	}

	f.NewPath = input.NewPath
	f.Operation = "planned"
	if err := tb.repo.UpdateFile(f); err != nil {
		return nil, err
	}

	// If target is a directory, clear children's targets (outer target overrides inner)
	if f.FileType == "directory" {
		_ = tb.repo.ClearChildrenTarget(tb.sessionID, input.Path)
	}

	out := &SetTargetOutput{Success: true}
	tb.logger.LogToolCall("set_target", input, out)
	return out, nil
}

func (tb *ToolBuilder) createCategory(ctx context.Context, input *CreateCategoryInput) (*CreateCategoryOutput, error) {
	tb.logger.LogToolCall("create_category", input, nil)

	cat := &db.Category{
		FilesystemID: tb.filesystemID,
		Name:         input.Name,
		Path:         input.Path,
		Description:  input.Description,
	}
	if err := tb.repo.CreateCategory(cat); err != nil {
		return nil, fmt.Errorf("create category: %w", err)
	}

	out := &CreateCategoryOutput{Success: true, Name: cat.Name, Path: cat.Path}
	tb.logger.LogToolCall("create_category", input, out)
	return out, nil
}

func (tb *ToolBuilder) updateCategory(ctx context.Context, input *UpdateCategoryInput) (*UpdateCategoryOutput, error) {
	tb.logger.LogToolCall("update_category", input, nil)

	// Find category by name
	cats, err := tb.repo.ListCategories(tb.filesystemID)
	if err != nil {
		return nil, err
	}
	var cat *db.Category
	for i := range cats {
		if cats[i].Name == input.Name {
			cat = &cats[i]
			break
		}
	}
	if cat == nil {
		return nil, fmt.Errorf("category not found: %s", input.Name)
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

// isTextContent checks if content appears to be text (not binary)
func isTextContent(s string) bool {
	if len(s) == 0 {
		return true
	}
	for i := 0; i < len(s) && i < 512; i++ {
		if s[i] == 0 {
			return false
		}
	}
	return !strings.ContainsRune(s[:min(len(s), 512)], '\x00')
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
