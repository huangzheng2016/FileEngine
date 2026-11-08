package api

import (
	"io"
	"log"
	"log/slog"
	"net/http"
	"strconv"

	"FileEngine/internal/agent"
	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
	"FileEngine/internal/remotefs"

	"github.com/gin-gonic/gin"
)

func (s *Server) listFiles(c *gin.Context) {
	q := db.FileQuery{}

	if sid := c.Query("session_id"); sid != "" {
		if id, err := strconv.ParseUint(sid, 10, 32); err == nil {
			q.SessionID = uint(id)
		}
	}
	if pp := c.Query("parent_path"); pp != "" || c.Query("parent_path") != "" {
		val := c.Query("parent_path")
		q.ParentPath = &val
	}
	q.FileType = c.Query("type")
	if tagged := c.Query("tagged"); tagged != "" {
		val := tagged == "true"
		q.Tagged = &val
	}
	q.Search = c.Query("search")
	if categorized := c.Query("categorized"); categorized != "" {
		val := categorized == "true"
		q.Categorized = &val
	}
	if cp := c.Query("category_path"); cp != "" {
		q.CategoryPath = &cp
	}
	if v := c.Query("needs_review"); v != "" {
		b := v == "true"
		q.NeedsReview = &b
	}
	if v := c.Query("sensitive"); v != "" {
		b := v == "true"
		q.Sensitive = &b
	}
	if v, ok := c.GetQuery("role"); ok {
		q.Role = &v
	}
	if page := c.Query("page"); page != "" {
		q.Page, _ = strconv.Atoi(page)
	}
	if ps := c.Query("page_size"); ps != "" {
		q.PageSize, _ = strconv.Atoi(ps)
	}

	files, total, err := s.repo.ListFiles(q)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": files, "total": total})
}

func (s *Server) getFile(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	file, err := s.repo.GetFile(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	c.JSON(http.StatusOK, file)
}

type UpdateFileRequest struct {
	Description *string `json:"description"`
	NewPath     *string `json:"new_path"`
}

// updateFile is the user's manual edit of one entry. A changed target goes
// through the same validation and plan-time collision resolution as the
// agent's set_target, and is learned as a filesystem-scope correction memory
// so later runs classify that kind of file the user's way.
func (s *Server) updateFile(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	file, err := s.repo.GetFile(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	var req UpdateFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Description != nil {
		if err := s.repo.UpdateFileFields(file.ID, map[string]any{"description": *req.Description}); err != nil {
			internalError(c, err)
			return
		}
	}
	if req.NewPath != nil {
		if err := pathutil.ValidateFSPath(*req.NewPath); err != nil {
			badRequest(c, "目标路径非法："+err.Error())
			return
		}
		oldTarget := file.NewPath
		role := db.RoleFile
		if *req.NewPath == "" {
			role = db.RoleUndecided
		}
		res, err := s.repo.Decide(file.ScanSessionID, db.Decision{Path: file.OriginalPath, Role: role, NewPath: *req.NewPath, Reason: "用户修改", Override: true})
		if db.IsRejection(err) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if err != nil {
			internalError(c, err)
			return
		}
		if err := s.learnCorrection(*file, oldTarget, res.NewPath); err != nil {
			log.Printf("file %d: record correction memory: %v", file.ID, err)
		}
	}

	updated, err := s.repo.GetFile(file.ID)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

// learnCorrection records a user re-targeting as a correction memory of the
// session's filesystem (no-op when there is nothing to learn).
func (s *Server) learnCorrection(f db.FileEntry, oldTarget, newTarget string) error {
	session, err := s.repo.GetSession(f.ScanSessionID)
	if err != nil {
		return err
	}
	m := agent.CorrectionMemory(f, oldTarget, newTarget, session.FilesystemID)
	if m == nil {
		return nil
	}
	if _, err := s.repo.UpsertMemory(m); err != nil {
		return err
	}
	db.Emit(session.ID, 0, db.EvMemoryUpdated, db.MemoryUpdatedData{Scope: m.Scope, ID: m.ID})
	return nil
}

func (s *Server) getFileContent(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的文件 ID")
		return
	}
	file, err := s.repo.GetFile(uint(id))
	if err != nil {
		notFound(c, "文件不存在")
		return
	}
	src, err := pathutil.RootRelative(file.OriginalPath)
	if err != nil {
		badRequest(c, "文件路径非法")
		return
	}
	session, err := s.repo.GetSession(file.ScanSessionID)
	if err != nil {
		internalError(c, err)
		return
	}
	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		internalError(c, err)
		return
	}
	rfs, err := remotefs.NewFromConfig(fsCfg)
	if err != nil {
		slog.Warn("file content: connect filesystem", "file", file.ID, "err", err)
		abortError(c, http.StatusServiceUnavailable, "无法连接文件系统")
		return
	}
	defer rfs.Close()

	reader, err := rfs.ReadFile(c.Request.Context(), src)
	if err != nil {
		slog.Warn("file content: read", "file", file.ID, "err", err)
		abortError(c, http.StatusBadGateway, "读取文件失败")
		return
	}
	defer reader.Close()

	c.Header("Content-Disposition", contentDisposition("inline", file.Name))
	c.Header("Content-Type", "application/octet-stream")
	if _, err := io.Copy(c.Writer, reader); err != nil {
		slog.Warn("file content: stream", "file", file.ID, "err", err)
	}
}

type TreeNode struct {
	ID           uint       `json:"id"`
	Label        string     `json:"label"`
	OriginalPath string     `json:"original_path"`
	FileType     string     `json:"file_type"`
	ChildCount   int        `json:"child_count"`
	Tagged       bool       `json:"tagged"`
	IsLeaf       bool       `json:"is_leaf"`
	Children     []TreeNode `json:"children,omitempty"`
}

func (s *Server) getFileTree(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Query("session_id"), 10, 32)
	parentPath := c.Query("parent_path")

	if sessionID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
		return
	}

	files, err := s.repo.GetTreeNodes(uint(sessionID), parentPath)
	if err != nil {
		internalError(c, err)
		return
	}

	// Count subdirectories for each node to determine leaf status
	subDirCounts := make(map[string]int)
	if len(files) > 0 {
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.OriginalPath
		}
		type countResult struct {
			ParentPath string
			Cnt        int
		}
		var results []countResult
		s.repo.DB().Model(&db.FileEntry{}).
			Select("parent_path, count(*) as cnt").
			Where("scan_session_id = ? AND parent_path IN ? AND file_type = ?", sessionID, paths, "directory").
			Group("parent_path").
			Find(&results)
		for _, r := range results {
			subDirCounts[r.ParentPath] = r.Cnt
		}
	}

	nodes := make([]TreeNode, len(files))
	for i, f := range files {
		nodes[i] = TreeNode{
			ID:           f.ID,
			Label:        f.Name,
			OriginalPath: f.OriginalPath,
			FileType:     f.FileType,
			ChildCount:   f.ChildCount,
			Tagged:       f.Tagged,
			IsLeaf:       subDirCounts[f.OriginalPath] == 0,
		}
	}
	c.JSON(http.StatusOK, nodes)
}
