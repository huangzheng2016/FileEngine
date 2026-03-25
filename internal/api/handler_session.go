package api

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
	"FileEngine/internal/scanner"

	"github.com/gin-gonic/gin"
)

var (
	scanMu   sync.Mutex
	scanners = make(map[uint]context.CancelFunc)
)

// scanRootPath computes the FS-relative root path for scanning.
// ScanPath is the user-specified subdirectory; if empty, scan from FS root (".").
func scanRootPath(scanPath string) string {
	if scanPath != "" {
		return scanPath
	}
	return "."
}

// startScan launches a background goroutine that connects to the filesystem and scans.
// This is the single place where scan logic lives — used by both createSession and rescanSession.
func (s *Server) startScan(session *db.ScanSession, fsCfg config.RemoteFSConfig) {
	// Parse filter dirs from newline-separated string
	filterMode := session.FilterMode
	if filterMode == "" {
		filterMode = "blacklist"
	}
	var filterDirs []string
	for _, line := range strings.Split(session.FilterDirs, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			filterDirs = append(filterDirs, line)
		}
	}

	// Append category paths to blacklist if enabled
	if session.ExcludeCategoryDirs && filterMode == "blacklist" {
		if cats, err := s.repo.ListCategories(session.FilesystemID); err == nil {
			for _, c := range cats {
				if c.Path != "" {
					filterDirs = append(filterDirs, c.Path)
				}
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	scanMu.Lock()
	scanners[session.ID] = cancel
	scanMu.Unlock()

	go func() {
		defer func() {
			scanMu.Lock()
			delete(scanners, session.ID)
			scanMu.Unlock()
		}()

		fs, err := remotefs.NewFromConfig(fsCfg)
		if err != nil {
			log.Printf("scan session %d: connect fs failed: %v", session.ID, err)
			session.Status = "error: " + err.Error()
			s.repo.UpdateSession(session)
			return
		}
		defer fs.Close()
		sc := scanner.New(fs, s.repo, filterMode, filterDirs)
		if err := sc.Scan(ctx, session); err != nil {
			log.Printf("scan session %d failed: %v", session.ID, err)
			session.Status = "error: " + err.Error()
			s.repo.UpdateSession(session)
		}
	}()
}

// cancelScan cancels a running scan if one exists for the given session ID.
func cancelScan(sessionID uint) {
	scanMu.Lock()
	if cancel, ok := scanners[sessionID]; ok {
		cancel()
	}
	scanMu.Unlock()
}

type CreateSessionRequest struct {
	FilesystemID        uint   `json:"filesystem_id" binding:"required"`
	ScanPath            string `json:"scan_path"`
	ModelProviderID     uint   `json:"model_provider_id"`
	ExcludeCategoryDirs bool   `json:"exclude_category_dirs"`
	FilterMode          string `json:"filter_mode"`
	FilterDirs          string `json:"filter_dirs"`
}

func (s *Server) createSession(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filesystem, err := s.repo.GetFilesystem(req.FilesystemID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filesystem not found"})
		return
	}

	filterMode := req.FilterMode
	if filterMode == "" {
		filterMode = "blacklist"
	}

	session := &db.ScanSession{
		FilesystemID:        filesystem.ID,
		ScanPath:            req.ScanPath,
		RootPath:            scanRootPath(req.ScanPath),
		Protocol:            filesystem.Protocol,
		Status:              "scanning",
		AllowReadFile:       true,
		AllowAutoCategory:   false,
		ExcludeCategoryDirs: req.ExcludeCategoryDirs,
		FilterMode:          filterMode,
		FilterDirs:          req.FilterDirs,
		ModelProviderID:     req.ModelProviderID,
	}
	if err := s.repo.CreateSession(session); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	s.startScan(session, filesystem.ToRemoteFSConfig())
	c.JSON(http.StatusCreated, session)
}

func (s *Server) listSessions(c *gin.Context) {
	var sessions []db.ScanSession
	var err error

	if fsID := c.Query("filesystem_id"); fsID != "" {
		if id, e := strconv.ParseUint(fsID, 10, 32); e == nil {
			sessions, err = s.repo.ListSessionsByFilesystem(uint(id))
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filesystem_id"})
			return
		}
	} else {
		sessions, err = s.repo.ListSessions()
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sessions)
}

func (s *Server) getSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	_ = s.repo.RefreshSessionCounts(uint(id))
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	c.JSON(http.StatusOK, session)
}

func (s *Server) deleteSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	cancelScan(uint(id))

	if err := s.repo.DeleteSession(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

type UpdateSessionRequest struct {
	AllowReadFile       *bool   `json:"allow_read_file"`
	AllowAutoCategory   *bool   `json:"allow_auto_category"`
	ExcludeCategoryDirs *bool   `json:"exclude_category_dirs"`
	FilterMode          *string `json:"filter_mode"`
	FilterDirs          *string `json:"filter_dirs"`
	ModelProviderID     *uint   `json:"model_provider_id"`
}

func (s *Server) updateSessionConfig(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	var req UpdateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.AllowReadFile != nil {
		session.AllowReadFile = *req.AllowReadFile
	}
	if req.AllowAutoCategory != nil {
		session.AllowAutoCategory = *req.AllowAutoCategory
	}
	if req.ExcludeCategoryDirs != nil {
		session.ExcludeCategoryDirs = *req.ExcludeCategoryDirs
	}
	if req.FilterMode != nil {
		session.FilterMode = *req.FilterMode
	}
	if req.FilterDirs != nil {
		session.FilterDirs = *req.FilterDirs
	}
	if req.ModelProviderID != nil {
		session.ModelProviderID = *req.ModelProviderID
	}
	if err := s.repo.UpdateSession(session); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, session)
}

func (s *Server) rescanSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	cancelScan(session.ID)

	if err := s.repo.DeleteFilesBySession(session.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Recompute RootPath from ScanPath (fixes sessions created with old logic)
	session.RootPath = scanRootPath(session.ScanPath)
	session.Status = "scanning"
	session.TotalFiles = 0
	session.TaggedFiles = 0
	session.PlannedOps = 0
	session.ExecutedOps = 0
	s.repo.UpdateSession(session)

	filesystem, err := s.repo.GetFilesystem(session.FilesystemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "filesystem not found"})
		return
	}

	s.startScan(session, filesystem.ToRemoteFSConfig())
	c.JSON(http.StatusOK, session)
}
