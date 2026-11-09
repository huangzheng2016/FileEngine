package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func TestFrontendDeepLinkServesIndex(t *testing.T) {
	if err := db.Init(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "t.db")}); err != nil {
		t.Fatal(err)
	}
	front := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>app</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	s := NewServer(db.NewRepository(db.GetDB()), front, Options{Token: "test-token-0123456789"})

	for _, p := range []string{"/sessions/1", "/sessions", "/models"} {
		w := do(s, "GET", p, nil, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<title>app</title>") {
			t.Fatalf("GET %s = %d %q, want index.html", p, w.Code, w.Body.String())
		}
	}
	if w := do(s, "GET", "/assets/app.js", nil, nil); w.Code != http.StatusOK || w.Body.String() != "console.log(1)" {
		t.Fatalf("asset = %d %q", w.Code, w.Body.String())
	}
}
