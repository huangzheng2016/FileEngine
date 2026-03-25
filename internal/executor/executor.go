package executor

import (
	"context"
	"fmt"
	"log"
	"path"
	"sync"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

type PlanItem struct {
	FileID       uint   `json:"file_id"`
	OriginalPath string `json:"original_path"`
	NewPath      string `json:"new_path"`
	Operation    string `json:"operation"`
	Name         string `json:"name"`
	FileType     string `json:"file_type"`
}

type ExecuteResult struct {
	Total    int `json:"total"`
	Success  int `json:"success"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
}

type Executor struct {
	repo *db.Repository
	fs   remotefs.RemoteFS

	mu       sync.Mutex
	running  bool
	cancelFn context.CancelFunc
	progress ExecuteResult
}

func New(repo *db.Repository, fs remotefs.RemoteFS) *Executor {
	return &Executor{repo: repo, fs: fs}
}

func (e *Executor) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *Executor) Progress() ExecuteResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.progress
}

func (e *Executor) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancelFn != nil {
		e.cancelFn()
	}
}

func (e *Executor) DryRun(sessionID uint) ([]PlanItem, error) {
	files, err := e.repo.GetPlannedFiles(sessionID)
	if err != nil {
		return nil, err
	}
	items := make([]PlanItem, len(files))
	for i, f := range files {
		items[i] = PlanItem{
			FileID: f.ID, OriginalPath: f.OriginalPath, NewPath: f.NewPath,
			Operation: f.Operation, Name: f.Name, FileType: f.FileType,
		}
	}
	return items, nil
}

func (e *Executor) Execute(ctx context.Context, sessionID uint, mode string) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("executor is already running")
	}
	e.running = true
	ctx, cancel := context.WithCancel(ctx)
	e.cancelFn = cancel
	e.progress = ExecuteResult{}
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.running = false
		e.cancelFn = nil
		e.mu.Unlock()
	}()

	session, err := e.repo.GetSession(sessionID)
	if err != nil {
		return err
	}
	session.Status = "executing"
	if err := e.repo.UpdateSession(session); err != nil {
		return err
	}

	files, err := e.repo.GetPlannedFiles(sessionID)
	if err != nil {
		return err
	}

	// Deduplicate target paths — add (1), (2) suffix for conflicts
	deduplicatePaths(files, e.repo)

	e.mu.Lock()
	e.progress.Total = len(files)
	e.mu.Unlock()

	for _, f := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if f.Executed {
			e.mu.Lock()
			e.progress.Skipped++
			e.mu.Unlock()
			continue
		}

		op := mode
		if op == "" {
			if f.Operation == "planned" {
				op = "copy"
			} else {
				op = f.Operation
			}
		}

		var opErr error
		if f.FileType == "directory" {
			opErr = e.executeDir(ctx, f.OriginalPath, f.NewPath, op)
		} else {
			opErr = e.executeFile(ctx, f.OriginalPath, f.NewPath, op)
		}

		if opErr != nil {
			log.Printf("execute %s %s -> %s: %v", op, f.OriginalPath, f.NewPath, opErr)
			e.mu.Lock()
			e.progress.Failed++
			e.mu.Unlock()
			continue
		}

		f.Executed = true
		if err := e.repo.UpdateFile(&f); err != nil {
			log.Printf("update file record: %v", err)
		}

		e.mu.Lock()
		e.progress.Success++
		e.mu.Unlock()
	}

	_ = e.repo.RefreshSessionCounts(sessionID)
	session, _ = e.repo.GetSession(sessionID)
	session.Status = "done"
	return e.repo.UpdateSession(session)
}

// executeFile handles a single file copy or move.
func (e *Executor) executeFile(ctx context.Context, src, dst, op string) error {
	if err := e.fs.MkdirAll(ctx, parentDir(dst)); err != nil {
		log.Printf("mkdir error for %s: %v", dst, err)
	}
	switch op {
	case "move":
		return e.fs.MoveFile(ctx, src, dst)
	case "copy":
		return e.fs.CopyFile(ctx, src, dst)
	default:
		return fmt.Errorf("unknown operation: %s", op)
	}
}

// executeDir handles directory copy or move.
// Move: try rename first (fast, atomic). If rename fails, fall back to recursive copy + delete.
// Copy: always recursive.
func (e *Executor) executeDir(ctx context.Context, src, dst, op string) error {
	if err := e.fs.MkdirAll(ctx, parentDir(dst)); err != nil {
		log.Printf("mkdir error for %s: %v", dst, err)
	}

	if op == "move" {
		// Try direct rename first (works on same filesystem/share)
		if err := e.fs.MoveFile(ctx, src, dst); err == nil {
			return nil
		}
		// Rename failed — fall back to recursive copy then we'd need delete
		// For safety, just do recursive copy (user chose "move" but cross-share move isn't atomic)
		log.Printf("directory rename failed for %s, falling back to recursive copy", src)
	}

	// Recursive copy
	return e.recursiveCopy(ctx, src, dst)
}

func (e *Executor) recursiveCopy(ctx context.Context, src, dst string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := e.fs.MkdirAll(ctx, dst); err != nil {
		return fmt.Errorf("mkdir %s: %w", dst, err)
	}

	entries, err := e.fs.List(ctx, src)
	if err != nil {
		return fmt.Errorf("list %s: %w", src, err)
	}

	for _, entry := range entries {
		srcPath := path.Join(src, entry.Name)
		dstPath := path.Join(dst, entry.Name)

		if entry.IsDir {
			if err := e.recursiveCopy(ctx, srcPath, dstPath); err != nil {
				log.Printf("recursive copy %s: %v", srcPath, err)
				// Continue with other entries
			}
		} else {
			if err := e.fs.CopyFile(ctx, srcPath, dstPath); err != nil {
				log.Printf("copy file %s -> %s: %v", srcPath, dstPath, err)
			}
		}
	}
	return nil
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

// deduplicatePaths detects duplicate NewPath values and adds (1), (2) suffixes.
// Updates both the slice and the DB records.
func deduplicatePaths(files []db.FileEntry, repo *db.Repository) {
	seen := map[string]int{}
	for i, f := range files {
		if f.NewPath == "" || f.Executed {
			continue
		}
		count := seen[f.NewPath]
		seen[f.NewPath]++
		if count > 0 {
			newPath := addSuffix(f.NewPath, count)
			log.Printf("deduplicate: %s -> %s", f.NewPath, newPath)
			files[i].NewPath = newPath
			repo.UpdateFile(&files[i])
		}
	}
}

// addSuffix adds a numeric suffix to a path: /a/b.txt → /a/b(1).txt, /a/dir → /a/dir(1)
func addSuffix(p string, n int) string {
	suffix := fmt.Sprintf("(%d)", n)
	dot := -1
	slash := -1
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '.' && dot == -1 && slash == -1 {
			dot = i
		}
		if p[i] == '/' {
			slash = i
			break
		}
	}
	if dot > 0 && dot > slash {
		// Has extension: /a/b.txt → /a/b(1).txt
		return p[:dot] + suffix + p[dot:]
	}
	// No extension (directory or extensionless file)
	return p + suffix
}
