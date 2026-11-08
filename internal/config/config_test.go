package config

import (
	"os"
	"path/filepath"
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

// agent_timeout defaults to unlimited: an absent value must stay 0 (no
// timeout), not be replaced by a hard-coded cap.
func TestAgentTimeoutDefaultsToUnlimited(t *testing.T) {
	cfg, err := Load(writeConfig(t, "server:\n  port: 9999\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.AgentTimeout != 0 {
		t.Fatalf("agent_timeout default: got %d, want 0 (unlimited)", cfg.Agent.AgentTimeout)
	}
}

// Explicit agent_timeout values are preserved as configured, including
// non-positive values (explicit unlimited) and positive caps.
func TestAgentTimeoutExplicitValuesPreserved(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want int
	}{
		{"positive cap", "agent:\n  agent_timeout: 120\n", 120},
		{"explicit unlimited", "agent:\n  agent_timeout: -1\n", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.yaml))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Agent.AgentTimeout != tc.want {
				t.Fatalf("agent_timeout: got %d, want %d", cfg.Agent.AgentTimeout, tc.want)
			}
		})
	}
}

// Other agent defaults must still be applied alongside the timeout change.
func TestAgentDefaultsStillApplied(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.Concurrency != 6 || cfg.Agent.MaxRetries != 3 || cfg.Agent.MaxStep != 150 {
		t.Fatalf("agent defaults not applied: %+v", cfg.Agent)
	}
	if cfg.Agent.RateLimitMaxRetries != 8 || cfg.Agent.RetryBaseDelay != 2 || cfg.Agent.RetryMaxDelay != 60 {
		t.Fatalf("retry defaults not applied: %+v", cfg.Agent)
	}
}

// retry_max_delay below retry_base_delay is clamped up to the base.
func TestRetryMaxDelayClampedToBase(t *testing.T) {
	cfg, err := Load(writeConfig(t, "agent:\n  retry_base_delay: 10\n  retry_max_delay: 5\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.RetryMaxDelay != 10 {
		t.Fatalf("retry_max_delay: got %d, want clamped to base 10", cfg.Agent.RetryMaxDelay)
	}
}

// The empty-DSN default follows the driver: PostgreSQL gets a libpq-style
// DSN, sqlite/mysql keep the historical SQLite file default.
func TestDatabaseDSNDefaultFollowsDriver(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"unset driver", "", "fileengine.db"},
		{"sqlite", "database:\n  driver: sqlite\n", "fileengine.db"},
		{"mysql", "database:\n  driver: mysql\n", "fileengine.db"},
		{"postgres", "database:\n  driver: postgres\n", "host=localhost port=5432 user=postgres dbname=fileengine sslmode=disable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.yaml))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Database.DSN != tc.want {
				t.Fatalf("default DSN: got %q, want %q", cfg.Database.DSN, tc.want)
			}
		})
	}
}

// An explicit DSN is never replaced by the driver default.
func TestDatabaseExplicitDSNPreserved(t *testing.T) {
	cfg, err := Load(writeConfig(t, "database:\n  driver: postgres\n  dsn: postgres://u:p@db.internal:5433/fe?sslmode=require\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Database.DSN != "postgres://u:p@db.internal:5433/fe?sslmode=require" {
		t.Fatalf("explicit DSN overwritten: got %q", cfg.Database.DSN)
	}
}

// category_policy defaults to the built-in policy text when the key is absent.
func TestCategoryPolicyDefaultApplied(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.CategoryPolicy != DefaultCategoryPolicy {
		t.Fatalf("category_policy default: got %q", cfg.Agent.CategoryPolicy)
	}
}

// An explicit category_policy (custom text) is preserved verbatim, including
// through the Update path used by the config API (hot reload).
func TestCategoryPolicyExplicitPreserved(t *testing.T) {
	custom := "自定义规范：\n- 只按年份建类"
	cfg, err := Load(writeConfig(t, "agent:\n  category_policy: \"自定义规范：\\n- 只按年份建类\"\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.CategoryPolicy != custom {
		t.Fatalf("explicit category_policy: got %q, want %q", cfg.Agent.CategoryPolicy, custom)
	}

	updated := &Config{Agent: AgentConfig{CategoryPolicy: custom}}
	Update(updated)
	if Get().Agent.CategoryPolicy != custom {
		t.Fatalf("Update must preserve explicit category_policy: got %q", Get().Agent.CategoryPolicy)
	}
}

// A missing config file is created with the defaults, private to the owner,
// and loads like any other.
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
	again, err := Load(p)
	if err != nil || again.Server.Port != 8080 || again.Agent.MaxStep != cfg.Agent.MaxStep {
		t.Fatalf("reload: %+v %v", again, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing-dir", "config.yaml")); err == nil {
		t.Fatal("a missing parent directory must fail, not be created")
	}
}
