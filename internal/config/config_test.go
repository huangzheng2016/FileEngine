package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name, yaml, want string
	}{
		{"unknown key", "server:\n  prot: 9\n", "prot"},
		{"agent section is settings now", "agent:\n  concurrency: 4\n", "agent"},
		{"postgres without dsn", "database:\n  driver: postgres\n", "dsn"},
		{"mysql without dsn", "database:\n  driver: mysql\n", "dsn"},
		{"unknown driver", "database:\n  driver: oracle\n  dsn: x\n", "driver"},
		{"port out of range", "server:\n  port: 70000\n", "port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestDatabaseExplicitDSNPreserved(t *testing.T) {
	cfg, err := Load(writeConfig(t, "database:\n  driver: postgres\n  dsn: postgres://u:p@db.internal:5433/fe?sslmode=require\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Database.DSN != "postgres://u:p@db.internal:5433/fe?sslmode=require" {
		t.Fatalf("explicit DSN overwritten: got %q", cfg.Database.DSN)
	}
}

func TestAgentStartsAtDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.Concurrency != 6 || cfg.Agent.MaxStep != 150 || cfg.Agent.AgentTimeout != 0 || cfg.Agent.CategoryPolicy != DefaultCategoryPolicy {
		t.Fatalf("agent defaults: %+v", cfg.Agent)
	}
}

func TestSetAgentSwapsSnapshot(t *testing.T) {
	cfg, _ := Load(writeConfig(t, "server:\n  token: keep\n"))
	a := cfg.Agent
	a.Concurrency = 9
	SetAgent(a)
	if Get().Agent.Concurrency != 9 || Get().Server.Token != "keep" {
		t.Fatalf("SetAgent: %+v", Get())
	}
	if cfg.Agent.Concurrency == 9 {
		t.Fatal("an earlier snapshot must not change")
	}
}

func TestLoadCreatesMissingConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8080 || cfg.Database.Driver != "sqlite" || Path() != p {
		t.Fatalf("defaults not applied: %+v path=%s", cfg.Server, Path())
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", st.Mode().Perm())
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("reload of the created file: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing-dir", "config.yaml")); err == nil {
		t.Fatal("a missing parent directory must fail, not be created")
	}
}
