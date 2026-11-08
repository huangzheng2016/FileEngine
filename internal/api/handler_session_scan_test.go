package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"FileEngine/internal/db"
)

func itoa(id uint) string { return fmt.Sprintf("%d", id) }

func TestScanConnectErrorSurfaced(t *testing.T) {
	s := setupSessionTestServer(t)
	repo := db.NewRepository(db.GetDB())
	missing := filepath.Join(t.TempDir(), "missing")
	if err := repo.CreateFilesystem(&db.Filesystem{Name: "broken", Protocol: "local", BasePath: missing}); err != nil {
		t.Fatalf("create broken filesystem: %v", err)
	}

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 2,
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d, resp %v", code, created)
	}
	sessID := uint(created["id"].(float64))
	waitForScanDone(t, s, sessID)

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/"+itoa(sessID), nil)
	if code != http.StatusOK {
		t.Fatalf("get session: code %d", code)
	}
	if got["status"] != "error" {
		t.Errorf("status = %v, want error", got["status"])
	}
	msg, _ := got["error_message"].(string)
	if !strings.Contains(msg, "base path not accessible") {
		t.Errorf("error_message = %q, want the connect failure reason", msg)
	}
}

func TestScanWalkErrorSurfaced(t *testing.T) {
	s := setupSessionTestServer(t)

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 1,
		"scan_path":     "no/such/dir",
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d, resp %v", code, created)
	}
	waitForScanDone(t, s, 1)

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get session: code %d", code)
	}
	if got["status"] != "error" {
		t.Errorf("status = %v, want error", got["status"])
	}
	msg, _ := got["error_message"].(string)
	if !strings.Contains(msg, "no/such/dir") {
		t.Errorf("error_message = %q, want it to name the failing path", msg)
	}
}

func TestScanSuccessPopulatesProgressFields(t *testing.T) {
	s := setupSessionTestServer(t)
	repo := db.NewRepository(db.GetDB())
	fs, err := repo.GetFilesystem(1)
	if err != nil {
		t.Fatalf("get filesystem: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fs.BasePath, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(fs.BasePath, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 1,
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d, resp %v", code, created)
	}
	waitForScanDone(t, s, 1)

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get session: code %d", code)
	}
	if got["status"] != "scanned" {
		t.Fatalf("status = %v, want scanned", got["status"])
	}
	// a.txt + sub = 2 entries.
	if got["total_files"] != float64(2) || got["scanned_count"] != float64(2) {
		t.Errorf("total_files=%v scanned_count=%v, want 2/2", got["total_files"], got["scanned_count"])
	}
	if got["current_path"] != "" || got["error_message"] != "" {
		t.Errorf("current_path=%v error_message=%v, want both empty", got["current_path"], got["error_message"])
	}
}

func TestRescanClearsErrorAndProgress(t *testing.T) {
	s := setupSessionTestServer(t)
	repo := db.NewRepository(db.GetDB())
	missing := filepath.Join(t.TempDir(), "missing")
	if err := repo.CreateFilesystem(&db.Filesystem{Name: "broken", Protocol: "local", BasePath: missing}); err != nil {
		t.Fatalf("create broken filesystem: %v", err)
	}
	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 2,
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d", code)
	}
	sessID := uint(created["id"].(float64))
	waitForScanDone(t, s, sessID)

	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(missing, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	code, _ = doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/"+itoa(sessID)+"/rescan", nil)
	if code != http.StatusOK {
		t.Fatalf("rescan: code %d", code)
	}
	waitForScanDone(t, s, sessID)

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/"+itoa(sessID), nil)
	if code != http.StatusOK {
		t.Fatalf("get session: code %d", code)
	}
	if got["status"] != "scanned" {
		t.Errorf("status = %v, want scanned after rescan", got["status"])
	}
	if got["error_message"] != "" {
		t.Errorf("error_message = %v, want cleared by rescan", got["error_message"])
	}
	if got["scanned_count"] != float64(1) || got["total_files"] != float64(1) {
		t.Errorf("scanned_count=%v total_files=%v, want 1/1", got["scanned_count"], got["total_files"])
	}
}

func registerFakeScan(t *testing.T, sessionID uint, cancel func()) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	h := &scanHandle{cancel: cancel, done: done}
	scanMu.Lock()
	scanners[sessionID] = h
	scanMu.Unlock()
	t.Cleanup(func() {
		scanMu.Lock()
		if scanners[sessionID] == h {
			delete(scanners, sessionID)
		}
		scanMu.Unlock()
	})
	return done
}

// The canceled scan's DB tail has no ctx checks, so without the wait its
// writes land on top of the fresh scan.
func TestRescanWaitsForPreviousScan(t *testing.T) {
	s := setupSessionTestServer(t)
	code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{"filesystem_id": 1})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d", code)
	}
	waitForScanDone(t, s, 1)

	canceled := make(chan struct{})
	done := registerFakeScan(t, 1, func() { close(canceled) })

	rescanDone := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/rescan", nil)
		w := httptest.NewRecorder()
		serveAuthed(s, w, req)
		rescanDone <- w.Code
	}()

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("rescan did not cancel the previous scan")
	}
	select {
	case <-rescanDone:
		t.Fatal("rescan returned while the previous scan was still running")
	case <-time.After(150 * time.Millisecond):
	}

	close(done)
	select {
	case rc := <-rescanDone:
		if rc != http.StatusOK {
			t.Fatalf("rescan code %d, want 200", rc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rescan did not proceed after the previous scan exited")
	}
	waitForScanDone(t, s, 1) // the fresh scan the rescan kicked off
}

func TestRescanScanStopTimeoutFallback(t *testing.T) {
	s := setupSessionTestServer(t)
	code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{"filesystem_id": 1})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d", code)
	}
	waitForScanDone(t, s, 1)

	prev := scanStopTimeout
	scanStopTimeout = 50 * time.Millisecond
	defer func() { scanStopTimeout = prev }()
	registerFakeScan(t, 1, func() {}) // done never closes

	start := time.Now()
	code, _ = doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/rescan", nil)
	if code != http.StatusOK {
		t.Fatalf("rescan: code %d", code)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("rescan blocked %v despite the stop timeout", el)
	}
	waitForScanDone(t, s, 1)
}

// The canceled goroutine's final Save would otherwise resurrect the deleted
// row (Save inserts when the update matches nothing).
func TestDeleteWaitsForScan(t *testing.T) {
	s := setupSessionTestServer(t)
	code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{"filesystem_id": 1})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d", code)
	}
	waitForScanDone(t, s, 1)

	done := registerFakeScan(t, 1, func() {})

	deleteDone := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/1", nil)
		w := httptest.NewRecorder()
		serveAuthed(s, w, req)
		deleteDone <- w.Code
	}()

	select {
	case <-deleteDone:
		t.Fatal("delete returned while the scan was still running")
	case <-time.After(150 * time.Millisecond):
	}
	close(done)
	select {
	case rc := <-deleteDone:
		if rc != http.StatusOK {
			t.Fatalf("delete code %d, want 200", rc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not proceed after the scan exited")
	}

	code, _ = doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1", nil)
	if code != http.StatusNotFound {
		t.Errorf("get after delete: code %d, want 404", code)
	}
}
