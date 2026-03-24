package executor

import (
	"context"
	"fmt"
	"log"
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
			FileID:       f.ID,
			OriginalPath: f.OriginalPath,
			NewPath:      f.NewPath,
			Operation:    f.Operation,
			Name:         f.Name,
			FileType:     f.FileType,
		}
	}
	return items, nil
}

func (e *Executor) Execute(ctx context.Context, sessionID uint) error {
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

		var opErr error
		switch f.Operation {
		case "move":
			if err := e.fs.MkdirAll(ctx, parentDir(f.NewPath)); err != nil {
				log.Printf("mkdir error for %s: %v", f.NewPath, err)
			}
			opErr = e.fs.MoveFile(ctx, f.OriginalPath, f.NewPath)
		case "copy":
			if err := e.fs.MkdirAll(ctx, parentDir(f.NewPath)); err != nil {
				log.Printf("mkdir error for %s: %v", f.NewPath, err)
			}
			opErr = e.fs.CopyFile(ctx, f.OriginalPath, f.NewPath)
		default:
			e.mu.Lock()
			e.progress.Skipped++
			e.mu.Unlock()
			continue
		}

		if opErr != nil {
			log.Printf("execute %s %s -> %s: %v", f.Operation, f.OriginalPath, f.NewPath, opErr)
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

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
