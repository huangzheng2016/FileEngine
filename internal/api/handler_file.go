package api

import (
	"io"
	"net/http"
	"strconv"

	"FileEngine/internal/db"
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
	if page := c.Query("page"); page != "" {
		q.Page, _ = strconv.Atoi(page)
	}
	if ps := c.Query("page_size"); ps != "" {
		q.PageSize, _ = strconv.Atoi(ps)
	}

	files, total, err := s.repo.ListFiles(q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		file.Description = *req.Description
	}
	if req.NewPath != nil {
		file.NewPath = *req.NewPath
		if *req.NewPath != "" {
			file.Operation = "planned"
		} else {
			file.Operation = ""
		}
	}

	if err := s.repo.UpdateFile(file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, file)
}

func (s *Server) getFileContent(c *gin.Context) {
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

	// Resolve filesystem connection from session
	session, err := s.repo.GetSession(file.ScanSessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session not found"})
		return
	}

	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "filesystem not available: " + err.Error()})
		return
	}

	rfs, err := remotefs.NewFromConfig(fsCfg)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "connect fs: " + err.Error()})
		return
	}
	defer rfs.Close()

	reader, err := rfs.ReadFile(c.Request.Context(), file.OriginalPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()

	c.Header("Content-Disposition", "inline; filename=\""+file.Name+"\"")
	c.Header("Content-Type", "application/octet-stream")
	io.Copy(c.Writer, reader)
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
