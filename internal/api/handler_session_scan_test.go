package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FileEngine/internal/db"
)

func itoa(id uint) string { return fmt.Sprintf("%d", id) }

// A filesystem whose base path does not exist fails at connect time — the
// session must end in status=error with the reason in error_message, never
// silently stuck in "scanning".
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

// A scan_path that does not exist fails mid-walk — same surfacing contract:
// status=error plus a descriptive error_message.
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

// A healthy scan lands coherent progress fields: scanned_count converged to
// total_files, heartbeat cleared, no error message.
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

// Rescanning a failed session wipes the error state and the stale counters,
// so a fixed filesystem scans clean.
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

	// Repair the filesystem (base path now exists with one file), then rescan.
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
