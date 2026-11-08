package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "conf.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsBindLoopback(t *testing.T) {
	loadTemp(t, "database:\n  dsn: x.db\n")
	if h := Get().Server.Host; h != "127.0.0.1" {
		t.Fatalf("default host %q, want 127.0.0.1", h)
	}
}

func TestEnsureTokenPersistsToLoadedPath0600(t *testing.T) {
	p := loadTemp(t, "server:\n  port: 9000\n")
	tok, gen, err := EnsureToken()
	if err != nil || !gen || len(tok) < 40 {
		t.Fatalf("EnsureToken = %q, %v, %v", tok, gen, err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v, want 0600", st.Mode().Perm())
	}
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	tok2, gen2, _ := EnsureToken()
	if gen2 || tok2 != tok {
		t.Fatalf("token not persisted: %q/%v vs %q", tok2, gen2, tok)
	}
}

func TestSecretsNeverJSON(t *testing.T) {
	loadTemp(t, "server:\n  token: tok-secret\ndatabase:\n  driver: postgres\n  dsn: host=x password=pg-secret\n")
	raw, _ := json.Marshal(Get())
	if strings.Contains(string(raw), "tok-secret") || strings.Contains(string(raw), "pg-secret") {
		t.Fatalf("secret leaked into JSON: %s", raw)
	}
}

func TestUpdateAgentJSONMergesPartially(t *testing.T) {
	loadTemp(t, "server:\n  token: keep\nagent:\n  concurrency: 4\n  max_step: 77\n")
	cfg, err := UpdateAgentJSON([]byte(`{"concurrency": 9}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Concurrency != 9 || cfg.Agent.MaxStep != 77 {
		t.Fatalf("merge wrong: concurrency=%d max_step=%d", cfg.Agent.Concurrency, cfg.Agent.MaxStep)
	}
	if Get().Server.Token != "keep" {
		t.Fatal("server section changed by agent merge")
	}
}
