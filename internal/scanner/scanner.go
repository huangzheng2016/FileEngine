package scanner

import (
	"context"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// ApplyRulesHook runs after every successful scan (first scan and rescans)
// so the session's plan rules are re-applied to new and reset entries. Set by
// the agent layer at startup; nil means no rules engine is wired.
var ApplyRulesHook func(sessionID uint)

type Scanner struct {
	fs         remotefs.RemoteFS
	repo       *db.Repository
	filterMode string
	filterDirs []string
	progress   *Progress
	pruneJunk  bool // config read once per scan
	commits    int  // walk transactions committed by the last scan
}

// Progress tracks one scan's live counters. Workers update it concurrently
// (atomics throughout); the reporter goroutine persists it onto the session
// row every progressReportInterval.
type Progress struct {
	entries int64        // entries kept by the walk so far
	dirs    int64        // directories listed
	path    atomic.Value // string: directory (or file, in post passes) being processed
}

func (p *Progress) addEntries(n int64)  { atomic.AddInt64(&p.entries, n) }
func (p *Progress) addDir()             { atomic.AddInt64(&p.dirs, 1) }
func (p *Progress) setPath(path string) { p.path.Store(path) }
func (p *Progress) Entries() int64      { return atomic.LoadInt64(&p.entries) }
func (p *Progress) Dirs() int64         { return atomic.LoadInt64(&p.dirs) }
func (p *Progress) CurrentPath() string {
	if v := p.path.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// progressReportInterval is how often the live counters are persisted to the
// session row. A var so tests can shrink it.
var progressReportInterval = 2 * time.Second

// HandleLister is implemented by backends (NFS, SMB) that list directories
// concurrently on their own connection pool and can resolve a directory by an
// opaque handle from a previous listing instead of re-walking its path.
type HandleLister interface {
	ListByHandle(ctx context.Context, path string, handle []byte) ([]remotefs.FileInfo, error)
	ListConcurrency() int
}

// defaultWorkers sizes the walk and file-read pools for backends that run
// operations in parallel without a pool of their own (local disk, SMB reads).
const defaultWorkers = 8

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

// Scan walks the session root and streams entries into the DB. When the
// session already has entries the scan is incremental: unchanged entries keep
// their decisions, changed ones are reset, new ones inserted and vanished
// ones deleted. Post passes (media metadata, duplicates) follow the walk; the
// session stays "scanning" until they finish.
func (s *Scanner) Scan(ctx context.Context, session *db.ScanSession) error {
	session.Status = "scanning"
	if err := s.repo.UpdateSession(session); err != nil {
		return err
	}
	cfg := config.Get()
	s.pruneJunk = cfg == nil || !cfg.Agent.JunkFilterDisabled

	s.progress = &Progress{}
	s.progress.setPath(session.RootPath)
	stopReport := s.startProgressReporter(session.ID)
	defer stopReport()

	index, err := s.loadIndex(session.ID)
	if err != nil {
		return fmt.Errorf("load existing entries: %w", err)
	}

	start := time.Now()
	walkCtx, cancelWalk := context.WithCancel(ctx)
	w := newWriter(s.repo, cancelWalk)
	walkErr := s.walk(walkCtx, session.RootPath, session.ID, index, w)
	saveErr := w.close()
	s.commits = w.commits
	cancelWalk()
	session.ScannedCount = int(s.progress.Entries())
	if saveErr != nil {
		return fmt.Errorf("save files: %w", saveErr)
	}
	if walkErr != nil {
		return fmt.Errorf("scan failed: %w", walkErr)
	}
	log.Printf("scan session %d: walked %d entries (%d dirs) in %v, %d commits",
		session.ID, session.ScannedCount, s.progress.Dirs(), time.Since(start).Round(time.Millisecond), s.commits)

	if index != nil {
		if err := s.finishRescan(session.ID, index); err != nil {
			return err
		}
	}
	if session.ExtractMetadata {
		if err := s.extractMetadata(ctx, session.ID); err != nil {
			return fmt.Errorf("media metadata: %w", err)
		}
	}
	if err := s.detectDuplicates(ctx, session.ID, session.DetectDuplicates); err != nil {
		return fmt.Errorf("duplicates: %w", err)
	}
	if err := s.repo.Optimize(); err != nil {
		log.Printf("warn: scan session %d: PRAGMA optimize: %v", session.ID, err)
	}

	stopReport()
	session.Status = "scanned"
	session.TotalFiles = session.ScannedCount
	session.CurrentPath = ""
	session.ErrorMessage = ""
	if err := s.repo.UpdateSession(session); err != nil {
		return err
	}
	if ApplyRulesHook != nil {
		ApplyRulesHook(session.ID)
	}
	return nil
}

// startProgressReporter launches the goroutine that persists the live
// counters to the session row until the returned stop function is called.
// Stop is idempotent and blocks until the goroutine exits, so no reporter
// write can land after the caller's own final session save.
func (s *Scanner) startProgressReporter(sessionID uint) func() {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(progressReportInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if err := db.WithRetry(func() error {
					return s.repo.UpdateScanProgress(sessionID, int(s.progress.Entries()), s.progress.CurrentPath())
				}); err != nil {
					log.Printf("warn: scan progress update failed: %v", err)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			wg.Wait()
			s.progress.setPath("")
			if err := db.WithRetry(func() error {
				return s.repo.UpdateScanProgress(sessionID, int(s.progress.Entries()), "")
			}); err != nil {
				log.Printf("warn: scan progress final update failed: %v", err)
			}
		})
	}
}

// walkConcurrency picks the walk pool size and listing function: backends
// with their own listing pool (NFS, SMB) size it themselves; FTP and SFTP run
// one operation at a time per connection, so extra workers would only queue.
func (s *Scanner) walkConcurrency() (func(context.Context, string, []byte) ([]remotefs.FileInfo, error), int) {
	if hl, ok := s.fs.(HandleLister); ok {
		return hl.ListByHandle, max(1, hl.ListConcurrency())
	}
	list := func(ctx context.Context, p string, _ []byte) ([]remotefs.FileInfo, error) { return s.fs.List(ctx, p) }
	switch s.fs.(type) {
	case *remotefs.FTPFS, *remotefs.SFTPFS:
		return list, 1
	}
	return list, defaultWorkers
}

// readConcurrency sizes the per-file read pools (metadata headers, hashes).
// NFS serializes each RPC on its primary connection; two workers still let
// one parse while the other waits on the wire.
func (s *Scanner) readConcurrency() int {
	switch s.fs.(type) {
	case *remotefs.FTPFS, *remotefs.SFTPFS:
		return 1
	case *remotefs.NFSFS:
		return 2
	}
	return defaultWorkers
}

// dirTask is one directory pending listing. Its own row is emitted when it is
// listed, so the child counts are known before the row is written.
type dirTask struct {
	path   string
	handle []byte
	depth  int           // depth of the directory's children
	row    *db.FileEntry // nil for the scan root (not an entry)
}

// dirQueue is an unbounded work queue for the walk. A bounded channel
// deadlocks here: workers enqueue child directories while still holding
// their current task, and with every worker enqueueing at once a full
// channel has no consumer left to drain it.
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
	// LIFO: depth-first keeps the queue (and memory) small on wide trees.
	t := q.items[len(q.items)-1]
	q.items = q.items[:len(q.items)-1]
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

// walk lists the tree with a worker pool and streams rows to the writer. The
// first listing error aborts the walk.
func (s *Scanner) walk(ctx context.Context, rootPath string, sessionID uint, index scanIndex, w *scanWriter) error {
	list, workers := s.walkConcurrency()
	queue := newDirQueue()
	queue.push(dirTask{path: rootPath})

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
	// Cancellation (user stop, writer failure) must also wake idle workers.
	stopWatch := context.AfterFunc(walkCtx, queue.abort)
	defer stopWatch()

	var pruned int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				t, ok := queue.pop()
				if !ok {
					return
				}
				if walkCtx.Err() != nil {
					queue.taskDone()
					return
				}
				s.progress.setPath(t.path)
				entries, err := list(walkCtx, t.path, t.handle)
				if err != nil {
					queue.taskDone()
					if walkCtx.Err() != nil {
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
				var batch writeBatch
				children, subdirs := 0, 0
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
					fe := db.FileEntry{
						ScanSessionID: sessionID,
						OriginalPath:  entryPath,
						Name:          entry.Name,
						Size:          entry.Size,
						ModTime:       entry.ModTime,
						Permissions:   entry.Permissions,
						FileType:      string(entry.FileType),
						ParentPath:    parentPath,
						Depth:         t.depth,
					}
					children++
					if entry.IsDir {
						subdirs++
						queue.push(dirTask{path: entryPath, handle: entry.Handle, depth: t.depth + 1, row: &fe})
						continue
					}
					index.emit(&batch, fe)
				}
				if t.row != nil {
					t.row.ChildCount = children
					t.row.SubdirCount = subdirs
					index.emit(&batch, *t.row)
				}
				s.progress.addDir()
				s.progress.addEntries(int64(children))
				w.send(batch)
				queue.taskDone()
			}
		}()
	}
	wg.Wait()

	if p := atomic.LoadInt64(&pruned); p > 0 {
		log.Printf("scan: pruned %d system junk directories (@eaDir & co.)", p)
	}
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// finishRescan deletes entries the walk did not find and folds new or
// reset entries under units into those units.
func (s *Scanner) finishRescan(sessionID uint, index scanIndex) error {
	var gone []uint
	for _, e := range index {
		if !e.seen.Load() {
			gone = append(gone, e.id)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i] < gone[j] })
	if err := s.repo.DeleteEntriesByIDs(gone); err != nil {
		return fmt.Errorf("delete vanished entries: %w", err)
	}
	adopted, err := s.repo.AdoptUnitMembers(sessionID)
	if err != nil {
		return fmt.Errorf("adopt unit members: %w", err)
	}
	log.Printf("scan session %d: rescan removed %d vanished entries, %d new entries joined their unit", sessionID, len(gone), adopted)
	return nil
}

// shouldPruneJunk reports whether a directory is well-known system junk that
// the scan prunes at traversal time (never listed, never persisted).
func (s *Scanner) shouldPruneJunk(name string) bool {
	if !s.pruneJunk {
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
