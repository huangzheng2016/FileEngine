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
	Page       int    `json:"page,omitempty" jsonschema_description:"Page number (default 1)"`
	PageSize   int    `json:"page_size,omitempty" jsonschema_description:"Items per page (default 20)"`
}

type ListFilesOutput struct {
	Files []FileItem `json:"files"`
	Total int64      `json:"total"`
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

type PlanMoveInput struct {
	Path    string `json:"path" jsonschema_description:"Original path of the file/directory"`
	NewPath string `json:"new_path" jsonschema_description:"Target path under a category folder"`
}

type PlanMoveOutput struct {
	Success bool `json:"success"`
}

type PlanCopyInput struct {
	Path    string `json:"path" jsonschema_description:"Original path of the file/directory"`
	NewPath string `json:"new_path" jsonschema_description:"Target path under a category folder"`
}

type PlanCopyOutput struct {
	Success bool `json:"success"`
}

// ============ Tool Builder ============

type ToolBuilder struct {
	repo         *db.Repository
	fs           remotefs.RemoteFS
	sessionID    uint
	filesystemID uint
	logger       *Logger
	cfg          config.AgentConfig
}

func NewToolBuilder(repo *db.Repository, fs remotefs.RemoteFS, sessionID uint, filesystemID uint, logger *Logger, cfg config.AgentConfig) *ToolBuilder {
	return &ToolBuilder{
		repo:         repo,
		fs:           fs,
		sessionID:    sessionID,
		filesystemID: filesystemID,
		logger:    logger,
		cfg:       cfg,
	}
}

func (tb *ToolBuilder) BuildTools() ([]tool.BaseTool, error) {
	listFiles, err := utils.InferTool("list_files", "List files in a directory from the database. Supports filtering by parent_path, file_type, and tagged status.", tb.listFiles)
	if err != nil {
		return nil, fmt.Errorf("build list_files: %w", err)
	}

	getFileInfo, err := utils.InferTool("get_file_info", "Get detailed metadata for a single file or directory.", tb.getFileInfo)
	if err != nil {
		return nil, fmt.Errorf("build get_file_info: %w", err)
	}

	readFile, err := utils.InferTool("read_file", "Read file content from the remote filesystem. Limited to text files under the configured size limit.", tb.readFile)
	if err != nil {
		return nil, fmt.Errorf("build read_file: %w", err)
	}

	updateDesc, err := utils.InferTool("update_description", "Set AI-generated description for a file or directory.", tb.updateDescription)
	if err != nil {
		return nil, fmt.Errorf("build update_description: %w", err)
	}

	markTagged, err := utils.InferTool("mark_tagged", "Mark a directory as fully processed. All children are also marked as tagged.", tb.markTagged)
	if err != nil {
		return nil, fmt.Errorf("build mark_tagged: %w", err)
	}

	listCats, err := utils.InferTool("list_categories", "List all user-defined category folders where files can be organized into.", tb.listCategories)
	if err != nil {
		return nil, fmt.Errorf("build list_categories: %w", err)
	}

	planMove, err := utils.InferTool("plan_move", "Plan to move a file/directory to a new path under a category folder. Only modifies the database, not the actual filesystem.", tb.planMove)
	if err != nil {
		return nil, fmt.Errorf("build plan_move: %w", err)
	}

	planCopy, err := utils.InferTool("plan_copy", "Plan to copy a file/directory to a new path under a category folder. Only modifies the database, not the actual filesystem.", tb.planCopy)
	if err != nil {
		return nil, fmt.Errorf("build plan_copy: %w", err)
	}

	return []tool.BaseTool{listFiles, getFileInfo, readFile, updateDesc, markTagged, listCats, planMove, planCopy}, nil
}

// ============ Tool Implementations ============

func (tb *ToolBuilder) listFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {
	tb.logger.LogToolCall("list_files", input, nil)

	page := input.Page
	if page <= 0 {
		page = 1
	}
	pageSize := input.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	q := db.FileQuery{
		SessionID:  tb.sessionID,
		ParentPath: &input.ParentPath,
		FileType:   input.FileType,
		Tagged:     input.Tagged,
		Page:       page,
		PageSize:   pageSize,
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

	out := &ListFilesOutput{Files: items, Total: total}
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

	// Also mark all children as tagged
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

func (tb *ToolBuilder) planMove(ctx context.Context, input *PlanMoveInput) (*PlanMoveOutput, error) {
	tb.logger.LogToolCall("plan_move", input, nil)

	f, err := tb.repo.GetFileByPath(tb.sessionID, input.Path)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", input.Path)
	}

	f.NewPath = input.NewPath
	f.Operation = "move"
	if err := tb.repo.UpdateFile(f); err != nil {
		return nil, err
	}

	out := &PlanMoveOutput{Success: true}
	tb.logger.LogToolCall("plan_move", input, out)
	return out, nil
}

func (tb *ToolBuilder) planCopy(ctx context.Context, input *PlanCopyInput) (*PlanCopyOutput, error) {
	tb.logger.LogToolCall("plan_copy", input, nil)

	f, err := tb.repo.GetFileByPath(tb.sessionID, input.Path)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", input.Path)
	}

	f.NewPath = input.NewPath
	f.Operation = "copy"
	if err := tb.repo.UpdateFile(f); err != nil {
		return nil, err
	}

	out := &PlanCopyOutput{Success: true}
	tb.logger.LogToolCall("plan_copy", input, out)
	return out, nil
}

// isTextContent checks if content appears to be text (not binary)
func isTextContent(s string) bool {
	if len(s) == 0 {
		return true
	}
	// Check for null bytes as a binary indicator
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
