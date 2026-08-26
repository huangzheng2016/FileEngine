package api

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
	"FileEngine/internal/scanner"

	"github.com/gin-gonic/gin"
)

// scanHandle tracks one in-flight scan goroutine: cancel stops its walk,
// done closes when the goroutine has fully exited (all DB writes included).
type scanHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

var (
	scanMu   sync.Mutex
	scanners = make(map[uint]*scanHandle)
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
	// Scan on a copy: the caller may still be serializing its own struct into
	// the HTTP response while the goroutine mutates status fields — sharing
	// the pointer is a data race.
	sess := *session
	session = &sess
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
	h := &scanHandle{cancel: cancel, done: make(chan struct{})}
	scanMu.Lock()
	scanners[session.ID] = h
	scanMu.Unlock()

	go func() {
		// Registered first, runs last: waiters on done must only wake once
		// every write (and the map detach) of this goroutine is complete.
		defer close(h.done)
		defer func() {
			scanMu.Lock()
			// Detach only if we are still the registered scan — a rescan
			// that raced past the stop wait may have replaced us.
			if scanners[session.ID] == h {
				delete(scanners, session.ID)
			}
			scanMu.Unlock()
		}()

		fs, err := remotefs.NewFromConfig(fsCfg)
		if err != nil {
			log.Printf("scan session %d: connect fs failed: %v", session.ID, err)
			session.Status = "error"
			session.ErrorMessage = err.Error()
			s.repo.UpdateSession(session)
			return
		}
		defer fs.Close()
		sc := scanner.New(fs, s.repo, filterMode, filterDirs)
		if err := sc.Scan(ctx, session); err != nil {
			if ctx.Err() != nil {
				// User-initiated cancel (rescan/delete) is not a failure —
				// whoever canceled owns the session state; writing an error
				// here would race the fresh rescan's "scanning" status.
				log.Printf("scan session %d canceled", session.ID)
				return
			}
			log.Printf("scan session %d failed: %v", session.ID, err)
			session.Status = "error"
			session.ErrorMessage = err.Error()
			s.repo.UpdateSession(session)
		}
	}()
}

// scanStopTimeout bounds how long rescan/delete wait for a canceled scan
// goroutine to unwind. The scan's DB tail (bulk insert + final save) never
// consults ctx, so a huge tree can take seconds to finish writing — but a
// wedged goroutine must not block the API forever. A var so tests can shrink.
var scanStopTimeout = 30 * time.Second

// cancelAndWaitScan cancels a running scan (if any) and waits for its
// goroutine to exit. The canceled scan's DB tail has no ctx checks; without
// this wait its bulk insert and final save can land AFTER the caller's own
// reset — the rescan then hits idx_uniq_session_path duplicates and its
// "scanning" status gets clobbered by the old goroutine's "scanned". Returns
// false when the wait timed out (caller logs and proceeds anyway).
func cancelAndWaitScan(sessionID uint) bool {
	scanMu.Lock()
	h := scanners[sessionID]
	scanMu.Unlock()
	if h == nil {
		return true
	}
	h.cancel()
	select {
	case <-h.done:
		return true
	case <-time.After(scanStopTimeout):
		return false
	}
}

type CreateSessionRequest struct {
	FilesystemID        uint   `json:"filesystem_id" binding:"required"`
	ScanPath            string `json:"scan_path"`
	ModelProviderID     uint   `json:"model_provider_id"`
	ExcludeCategoryDirs bool   `json:"exclude_category_dirs"`
	FilterMode          string `json:"filter_mode"`
	FilterDirs          string `json:"filter_dirs"`
	// AllowVision is additive: old clients omit it (nil → false). When set it
	// configures the session's vision switch at creation, so the frontend no
	// longer needs a compensating PATCH after create.
	AllowVision *bool `json:"allow_vision"`
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
		AllowVision:         req.AllowVision != nil && *req.AllowVision,
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

	if !cancelAndWaitScan(uint(id)) {
		log.Printf("delete session %d: timed out waiting for the scan to stop; proceeding anyway", id)
	}

	if err := s.repo.DeleteSession(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

type UpdateSessionRequest struct {
	AllowVision         *bool   `json:"allow_vision"`
	AllowReadFile       *bool   `json:"allow_read_file"`
	AllowAutoCategory   *bool   `json:"allow_auto_category"`
	AllowScript         *bool   `json:"allow_script"`
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
	if req.AllowVision != nil {
		session.AllowVision = *req.AllowVision
	}
	if req.AllowScript != nil {
		session.AllowScript = *req.AllowScript
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

	if !cancelAndWaitScan(session.ID) {
		log.Printf("rescan session %d: timed out waiting for the previous scan to stop; proceeding anyway", session.ID)
	}

	if err := s.repo.DeleteFilesBySession(session.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Recompute RootPath from ScanPath (fixes sessions created with old logic)
	session.RootPath = scanRootPath(session.ScanPath)
	session.Status = "scanning"
	session.TotalFiles = 0
	session.ScannedCount = 0
	session.CurrentPath = ""
	session.ErrorMessage = ""
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

func (s *Server) getSessionStats(c *gin.Context) {
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
	dist, err := s.repo.GetSessionStats(uint(id), session.FilesystemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"category_distribution": dist,
		"total_files":           session.TotalFiles,
		"tagged_files":          session.TaggedFiles,
		"planned_ops":           session.PlannedOps,
		"total_tokens":          session.TotalTokens,
	})
}
