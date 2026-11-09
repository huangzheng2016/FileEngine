package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
	"FileEngine/internal/scanner"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// done closes when the goroutine has fully exited, all DB writes included.
type scanHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

var (
	scanMu   sync.Mutex
	scanners = make(map[uint]*scanHandle)
)

func scanRootPath(scanPath string) string {
	return strings.Trim(path.Clean("/"+scanPath), "/")
}

// autoTag starts a tagging run once the scan has completed.
func (s *Server) startScan(session *db.ScanSession, fsCfg config.RemoteFSConfig, autoTag bool) {
	// Scan on a copy: the caller may still be serializing its own struct into
	// the HTTP response while the goroutine mutates status fields — sharing
	// the pointer is a data race.
	sess := *session
	session = &sess
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
			return
		}
		if autoTag {
			if _, err := s.startTaggingRun(session.ID); err != nil {
				log.Printf("scan session %d: auto tag: %v", session.ID, err)
			}
		}
	}()
}

var scanStopTimeout = 30 * time.Second

// Waits so the scan's last writes cannot land after the caller's own session
// update.
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
	BulkModelProviderID uint   `json:"bulk_model_provider_id"`
	ExcludeCategoryDirs bool   `json:"exclude_category_dirs"`
	FilterMode          string `json:"filter_mode"`
	FilterDirs          string `json:"filter_dirs"`
	AllowVision         bool   `json:"allow_vision"`
	ExtractMetadata     *bool  `json:"extract_metadata"` // nil = on
	DetectDuplicates    bool   `json:"detect_duplicates"`
	Goal                string `json:"goal"`
	AutoTag             bool   `json:"auto_tag"`
}

func (s *Server) createSession(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badBody(c, err)
		return
	}

	filesystem, err := s.repo.GetFilesystem(req.FilesystemID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		badRequest(c, msgNoFilesystem)
		return
	}
	if err != nil {
		internalError(c, err)
		return
	}

	if !s.modelExists(c, req.ModelProviderID, false) || !s.modelExists(c, req.BulkModelProviderID, true) {
		return
	}

	filterMode := req.FilterMode
	if filterMode == "" {
		filterMode = "blacklist"
	}

	session := &db.ScanSession{
		AllowVision:         req.AllowVision,
		Goal:                strings.TrimSpace(req.Goal),
		FilesystemID:        filesystem.ID,
		ScanPath:            scanRootPath(req.ScanPath),
		RootPath:            scanRootPath(req.ScanPath),
		Protocol:            filesystem.Protocol,
		Status:              "scanning",
		AllowReadFile:       true,
		ExcludeCategoryDirs: req.ExcludeCategoryDirs,
		FilterMode:          filterMode,
		FilterDirs:          req.FilterDirs,
		ModelProviderID:     req.ModelProviderID,
		DetectDuplicates:    req.DetectDuplicates,
		BulkModelProviderID: req.BulkModelProviderID,
	}
	if err := s.repo.CreateSession(session); err != nil {
		internalError(c, err)
		return
	}
	// GORM omits a false bool on insert, so the DB default (on) wins; write it explicitly.
	session.ExtractMetadata = req.ExtractMetadata == nil || *req.ExtractMetadata
	if !session.ExtractMetadata {
		if err := s.repo.DB().Model(session).Update("extract_metadata", false).Error; err != nil {
			internalError(c, err)
			return
		}
	}

	s.startScan(session, filesystem.ToRemoteFSConfig(), req.AutoTag)
	c.JSON(http.StatusCreated, session)
}

const msgPickModel = "请选择模型"

// On failure it has already answered the request; optional allows id 0.
func (s *Server) modelExists(c *gin.Context, id uint, optional bool) bool {
	if id == 0 && optional {
		return true
	}
	if id == 0 {
		badRequest(c, msgPickModel)
		return false
	}
	if _, err := s.repo.GetModelProvider(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			badRequest(c, msgPickModel)
		} else {
			internalError(c, err)
		}
		return false
	}
	return true
}

type sessionView struct {
	*db.ScanSession
	LatestRun *db.RunRef `json:"latest_run,omitempty"`
}

func withLatestRun(s *db.ScanSession, latest map[uint]db.RunRef) sessionView {
	v := sessionView{ScanSession: s}
	if ref, ok := latest[s.ID]; ok {
		v.LatestRun = &ref
	}
	return v
}

func (s *Server) listSessions(c *gin.Context) {
	var sessions []db.ScanSession
	var err error

	if fsID := c.Query("filesystem_id"); fsID != "" {
		if id, e := strconv.ParseUint(fsID, 10, 32); e == nil {
			sessions, err = s.repo.ListSessionsByFilesystem(uint(id))
		} else {
			badRequest(c, "filesystem_id 无效")
			return
		}
	} else {
		sessions, err = s.repo.ListSessions()
	}

	if err != nil {
		internalError(c, err)
		return
	}
	ids := make([]uint, len(sessions))
	for i := range sessions {
		ids[i] = sessions[i].ID
	}
	latest, err := s.repo.LatestRuns(ids)
	if err != nil {
		internalError(c, err)
		return
	}
	out := make([]sessionView, len(sessions))
	for i := range sessions {
		out[i] = withLatestRun(&sessions[i], latest)
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) getSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}

	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoSession)
		return
	}
	tokens, err := s.repo.SessionTokens(session.ID)
	if err != nil {
		internalError(c, err)
		return
	}
	latest, err := s.repo.LatestRuns([]uint{session.ID})
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, struct {
		sessionView
		Tokens db.TokenCounts `json:"tokens"`
	}{withLatestRun(session, latest), tokens})
}

func (s *Server) deleteSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}

	if !cancelAndWaitScan(uint(id)) {
		log.Printf("delete session %d: timed out waiting for the scan to stop; proceeding anyway", id)
	}
	s.stopSessionRuns(uint(id))

	if err := s.repo.DeleteSession(uint(id)); err != nil {
		internalError(c, err)
		return
	}
	if err := s.repo.DeleteSessionBrainData(uint(id)); err != nil {
		internalError(c, err)
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
	ExtractMetadata     *bool   `json:"extract_metadata"`
	DetectDuplicates    *bool   `json:"detect_duplicates"`
	BulkModelProviderID *uint   `json:"bulk_model_provider_id"`
	Goal                *string `json:"goal"`
}

func (s *Server) updateSessionConfig(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoSession)
		return
	}
	var req UpdateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badBody(c, err)
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
		if !s.modelExists(c, *req.ModelProviderID, false) {
			return
		}
		session.ModelProviderID = *req.ModelProviderID
	}
	if req.ExtractMetadata != nil {
		session.ExtractMetadata = *req.ExtractMetadata
	}
	if req.DetectDuplicates != nil {
		session.DetectDuplicates = *req.DetectDuplicates
	}
	if req.BulkModelProviderID != nil {
		if !s.modelExists(c, *req.BulkModelProviderID, true) {
			return
		}
		session.BulkModelProviderID = *req.BulkModelProviderID
	}
	if req.Goal != nil {
		session.Goal = strings.TrimSpace(*req.Goal)
	}
	if err := s.repo.UpdateSession(session); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (s *Server) rescanSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}

	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoSession)
		return
	}

	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		fsConfigFailed(c, err)
		return
	}

	if !cancelAndWaitScan(session.ID) {
		log.Printf("rescan session %d: timed out waiting for the previous scan to stop; proceeding anyway", session.ID)
	}

	session.RootPath = scanRootPath(session.ScanPath)
	session.Status = "scanning"
	session.ScannedCount = 0
	session.CurrentPath = ""
	session.ErrorMessage = ""
	if err := s.repo.UpdateSession(session); err != nil {
		internalError(c, err)
		return
	}

	s.startScan(session, fsCfg, false)
	c.JSON(http.StatusOK, session)
}

func (s *Server) getSessionStats(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoSession)
		return
	}
	dist, err := s.repo.GetSessionStats(uint(id), session.FilesystemID)
	if err != nil {
		internalError(c, err)
		return
	}
	tokens, err := s.repo.SessionTokens(session.ID)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"category_distribution": dist,
		"total_files":           session.TotalFiles,
		"tagged_files":          session.TaggedFiles,
		"planned_ops":           session.PlannedOps,
		"total_tokens":          tokens.Total,
	})
}
