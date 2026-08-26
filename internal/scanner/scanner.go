package scanner

import (
	"context"
	"fmt"
	"log"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
	"gorm.io/gorm"
)

type Scanner struct {
	fs         remotefs.RemoteFS
	repo       *db.Repository
	filterMode string
	filterDirs []string
}

// HandleLister is implemented by backends (NFS) that can list directories
// concurrently and resolve a directory by an opaque handle from a previous
// listing instead of re-walking the full path. Scans over such backends use
// a worker pool instead of the serial recursion.
type HandleLister interface {
	ListByHandle(ctx context.Context, path string, handle []byte) ([]remotefs.FileInfo, error)
	ListConcurrency() int
}

func New(fs remotefs.RemoteFS, repo *db.Repository, filterMode string, filterDirs []string) *Scanner {
	normalized := make([]string, 0, len(filterDirs))
	for _, p := range filterDirs {
		p = path.Clean(p)
		if p != "" && p != "." {
			normalized = append(normalized, p)
		}
	}
	if filterMode == "" {
		filterMode = "blacklist"
	}
	return &Scanner{fs: fs, repo: repo, filterMode: filterMode, filterDirs: normalized}
}

func (s *Scanner) Scan(ctx context.Context, session *db.ScanSession) error {
	session.Status = "scanning"
	if err := s.repo.UpdateSession(session); err != nil {
		return err
	}

	rootPath := session.RootPath
	var allFiles []db.FileEntry
	var err error

	if hl, ok := s.fs.(HandleLister); ok && hl.ListConcurrency() > 1 {
		err = s.scanConcurrent(ctx, hl, rootPath, session.ID, &allFiles)
	} else {
		err = s.scanDir(ctx, rootPath, rootPath, 0, session.ID, &allFiles)
	}
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	if err := db.WithRetry(func() error { return s.repo.CreateFiles(allFiles) }); err != nil {
		return fmt.Errorf("save files: %w", err)
	}

	// Update child counts for directories
	s.updateChildCounts(session.ID, allFiles)

	session.Status = "scanned"
	session.TotalFiles = len(allFiles)
	return s.repo.UpdateSession(session)
}

func (s *Scanner) scanDir(ctx context.Context, currentPath, rootPath string, depth int, sessionID uint, result *[]db.FileEntry) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	entries, err := s.fs.List(ctx, currentPath)
	if err != nil {
		// Accumulate listing errors instead of silently swallowing them.
		// The scan continues for other directories but reports the failure.
		log.Printf("warn: cannot list %s: %v", currentPath, err)
		return fmt.Errorf("list %s: %w", currentPath, err)
	}

	parentPath := currentPath
	if currentPath == rootPath {
		parentPath = ""
	}

	var errs []error
	for _, entry := range entries {
		entryPath := entry.Path
		if !path.IsAbs(entryPath) {
			entryPath = path.Join(currentPath, entry.Name)
		}

		// Skip excluded category directories and all their contents
		if entry.IsDir && s.shouldSkip(entryPath) {
			continue
		}

		// Prune well-known system junk directories (@eaDir & co.) at
		// traversal time: they are never listed and never reach the DB.
		if entry.IsDir && s.shouldPruneJunk(entry.Name) {
			continue
		}

		relDepth := depth
		if currentPath != rootPath {
			relDepth = depth
		}

		pp := parentPath
		if pp == rootPath {
			pp = rootPath
		}

		fe := db.FileEntry{
			ScanSessionID: sessionID,
			OriginalPath:  entryPath,
			Name:          entry.Name,
			Size:          entry.Size,
			ModTime:       entry.ModTime,
			Permissions:   entry.Permissions,
			FileType:      string(entry.FileType),
			ParentPath:    pp,
			Depth:         relDepth,
		}
		*result = append(*result, fe)

		if entry.IsDir {
			if err := s.scanDir(ctx, entryPath, rootPath, depth+1, sessionID, result); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// dirTask is one directory pending listing in the concurrent walk.
type dirTask struct {
	path   string
	handle []byte
	depth  int
}

// dirQueue is an unbounded work queue for the concurrent walk. A bounded
// channel deadlocks here: workers enqueue child directories while still
// holding their current task, and with every worker enqueueing at once a
// full channel has no consumer left to drain it.
type dirQueue struct {
	mu      sync.Mutex
	cond    sync.Cond
	items   []dirTask
	pending int  // queued + in-flight tasks
	closed  bool // set on completion (pending == 0) or abort
}

func newDirQueue() *dirQueue {
	q := &dirQueue{}
	q.cond.L = &q.mu
	return q
}

func (q *dirQueue) push(t dirTask) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, t)
	q.pending++
	q.cond.Signal()
}

// pop blocks for the next task; ok=false means the walk finished or aborted.
func (q *dirQueue) pop() (dirTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for !q.closed && len(q.items) == 0 {
		q.cond.Wait()
	}
	if q.closed {
		return dirTask{}, false
	}
	t := q.items[0]
	q.items = q.items[1:]
	return t, true
}

func (q *dirQueue) taskDone() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending--
	if q.pending == 0 {
		q.closed = true
		q.cond.Broadcast()
	}
}

// abort wakes every waiter so workers exit promptly on error/cancel.
func (q *dirQueue) abort() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}

// scanConcurrent walks the tree with a worker pool: each worker lists a
// directory (by server handle when the backend provides one, skipping the
// per-component path resolution) and enqueues child directories. Error
// semantics match the serial walk: the first listing error aborts the scan.
func (s *Scanner) scanConcurrent(ctx context.Context, hl HandleLister, rootPath string, sessionID uint, result *[]db.FileEntry) error {
	workers := hl.ListConcurrency()
	queue := newDirQueue()
	queue.push(dirTask{path: rootPath, depth: 0})

	walkCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstErr error
	var errOnce sync.Once
	fail := func(err error) {
		errOnce.Do(func() {
			firstErr = err
			cancel()
			queue.abort()
		})
	}

	var dirsDone, entriesDone, pruned int64
	start := time.Now()
	// Progress logging: without it a multi-hour scan is a black box (rows
	// are only written to the DB at the end).
	stopLog := make(chan struct{})
	var logWg sync.WaitGroup
	logWg.Add(1)
	go func() {
		defer logWg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopLog:
				return
			case <-ticker.C:
				d := atomic.LoadInt64(&dirsDone)
				e := atomic.LoadInt64(&entriesDone)
				el := time.Since(start).Seconds()
				log.Printf("scan progress: dirs=%d entries=%d elapsed=%.0fs rate=%.0f dirs/min %.0f entries/min",
					d, e, el, float64(d)/el*60, float64(e)/el*60)
			}
		}
	}()
	defer func() { close(stopLog); logWg.Wait() }()

	perWorker := make([][]db.FileEntry, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rows := perWorker[w]
			defer func() { perWorker[w] = rows }()
			for {
				t, ok := queue.pop()
				if !ok {
					return
				}
				if walkCtx.Err() != nil {
					queue.taskDone()
					return
				}
				entries, err := hl.ListByHandle(walkCtx, t.path, t.handle)
				if err != nil {
					queue.taskDone()
					if walkCtx.Err() != nil {
						// Cancellation, not a real listing failure.
						return
					}
					log.Printf("warn: cannot list %s: %v", t.path, err)
					fail(fmt.Errorf("list %s: %w", t.path, err))
					return
				}
				parentPath := t.path
				if t.path == rootPath {
					parentPath = ""
				}
				for _, entry := range entries {
					entryPath := entry.Path
					if !path.IsAbs(entryPath) {
						entryPath = path.Join(t.path, entry.Name)
					}
					if entry.IsDir && s.shouldSkip(entryPath) {
						continue
					}
					if entry.IsDir && s.shouldPruneJunk(entry.Name) {
						atomic.AddInt64(&pruned, 1)
						continue
					}
					rows = append(rows, db.FileEntry{
						ScanSessionID: sessionID,
						OriginalPath:  entryPath,
						Name:          entry.Name,
						Size:          entry.Size,
						ModTime:       entry.ModTime,
						Permissions:   entry.Permissions,
						FileType:      string(entry.FileType),
						ParentPath:    parentPath,
						Depth:         t.depth,
					})
					if entry.IsDir {
						queue.push(dirTask{path: entryPath, handle: entry.Handle, depth: t.depth + 1})
					}
				}
				atomic.AddInt64(&dirsDone, 1)
				atomic.AddInt64(&entriesDone, int64(len(entries)))
				queue.taskDone()
			}
		}(w)
	}
	wg.Wait()

	for _, rows := range perWorker {
		*result = append(*result, rows...)
	}
	if p := atomic.LoadInt64(&pruned); p > 0 {
		log.Printf("scan: pruned %d system junk directories (@eaDir & co.)", p)
	}
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Scanner) updateChildCounts(sessionID uint, files []db.FileEntry) {
	childMap := make(map[string]int)
	for _, f := range files {
		if f.ParentPath != "" {
			childMap[f.ParentPath]++
		}
	}

	// Batch update: direct UPDATE without reading each row first. One
	// transaction for the whole loop — autocommit would fsync per directory.
	s.repo.DB().Transaction(func(tx *gorm.DB) error {
		for parentPath, count := range childMap {
			tx.Model(&db.FileEntry{}).
				Where("scan_session_id = ? AND original_path = ?", sessionID, parentPath).
				Update("child_count", count)
		}
		return nil
	})
}

// shouldPruneJunk reports whether a directory is well-known system junk that
// the scan should prune at traversal time (never listed, never persisted).
// This is the scanner's traversal-level filter, distinct from the agent
// layer's post-scan MarkJunkEntries which still handles junk files and any
// junk dirs from scans that ran before pruning existed. Honors the same
// junk_filter_disabled config flag as the agent layer.
func (s *Scanner) shouldPruneJunk(name string) bool {
	cfg := config.Get()
	if cfg != nil && cfg.Agent.JunkFilterDisabled {
		return false
	}
	lower := strings.ToLower(name)
	for _, jd := range db.JunkDirNames {
		if lower == jd {
			return true
		}
	}
	return false
}

// shouldSkip checks if a directory should be skipped based on filter mode.
func (s *Scanner) shouldSkip(entryPath string) bool {
	if len(s.filterDirs) == 0 {
		return false
	}
	cleaned := path.Clean(entryPath)
	matched := false
	for _, fp := range s.filterDirs {
		if cleaned == fp || strings.HasPrefix(cleaned, fp+"/") {
			matched = true
			break
		}
	}
	switch s.filterMode {
	case "whitelist":
		if matched {
			return false
		}
		// Keep ancestor directories so we can recurse into them
		for _, fp := range s.filterDirs {
			if strings.HasPrefix(fp, cleaned+"/") {
				return false
			}
		}
		return true
	default: // blacklist
		return matched
	}
}
