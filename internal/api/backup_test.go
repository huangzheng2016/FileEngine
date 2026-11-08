package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestBackupStreamsSQLiteSnapshot(t *testing.T) {
	s := newSecTestServer(t, Options{})
	w := do(s, "POST", "/api/v1/admin/backup", nil, bearer(s.opts.Token))
	if w.Code != http.StatusOK {
		t.Fatalf("backup = %d %s", w.Code, w.Body)
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatal("backup is not a SQLite database")
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("disposition = %q", w.Header().Get("Content-Disposition"))
	}
	if w := do(s, "POST", "/api/v1/admin/backup", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated backup = %d", w.Code)
	}
}
