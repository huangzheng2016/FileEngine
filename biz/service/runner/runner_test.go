package runner

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func newService(t *testing.T) (*Service, *db.Repository) {
	t.Helper()
	dsn := filepath.ToSlash(filepath.Join(t.TempDir(), "fe.db"))
	if err := db.Open(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	repo := db.NewRepository(dal.DB())
	t.Cleanup(func() {
		if sqlDB, err := dal.DB().DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return NewService(repo), repo
}

func newSession(t *testing.T, repo *db.Repository) uint {
	t.Helper()
	s := &model.ScanSession{Status: "scanned"}
	if err := repo.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func waitStatus(t *testing.T, repo *db.Repository, runID uint, want string) *model.AgentRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := repo.GetRun(runID); err == nil && r.Status == want {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _ := repo.GetRun(runID)
	t.Fatalf("run %d did not reach %q: %+v", runID, want, r)
	return nil
}

func TestLaunchFinishesRunAndSurvivesPanic(t *testing.T) {
	s, repo := newService(t)
	sid := newSession(t, repo)

	run, err := s.Launch(sid, db.RunPhaseChat, "ok", func(context.Context, *model.AgentRun) Outcome {
		return Outcome{Summary: "fine"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitStatus(t, repo, run.ID, db.RunDone); got.Summary != "fine" {
		t.Fatalf("summary = %q", got.Summary)
	}

	run, err = s.Launch(sid, db.RunPhaseChat, "boom", func(context.Context, *model.AgentRun) Outcome { panic("boom") })
	if err != nil {
		t.Fatal(err)
	}
	if got := waitStatus(t, repo, run.ID, db.RunError); got.Error == "" {
		t.Fatal("a panicking run must record an error")
	}
}

func TestStopAndShutdownCancelRuns(t *testing.T) {
	s, repo := newService(t)
	sid := newSession(t, repo)
	started := make(chan struct{})
	work := func(ctx context.Context, _ *model.AgentRun) Outcome {
		close(started)
		<-ctx.Done()
		return Outcome{Err: ctx.Err()}
	}
	run, err := s.Launch(sid, db.RunPhaseChat, "stop me", work)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if got, err := s.Stop(run.ID); err != nil || got.Status != db.RunStopped {
		t.Fatalf("Stop = %+v, %v", got, err)
	}

	started = make(chan struct{})
	run, err = s.Launch(sid, db.RunPhaseChat, "shutdown", work)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitStatus(t, repo, run.ID, db.RunStopped)
}

func TestScanRegistryCancelAndShutdown(t *testing.T) {
	s, _ := newService(t)
	stopped := make(chan struct{})
	s.StartScan(7, func(ctx context.Context) {
		<-ctx.Done()
		close(stopped)
	})
	if !s.CancelScan(7) {
		t.Fatal("scan did not stop")
	}
	<-stopped
	if !s.CancelScan(7) {
		t.Fatal("cancelling an absent scan is a no-op")
	}

	s.StartScan(8, func(ctx context.Context) { <-ctx.Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with a live scan: %v", err)
	}

	s.StartScan(9, func(context.Context) { panic("scan boom") })
	if !s.CancelScan(9) {
		t.Fatal("a panicking scan must still detach")
	}
}
