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
	if cfg.Agent.BatchSize != 50 || cfg.Agent.MaxRetries != 3 || cfg.Agent.MaxStep != 150 {
		t.Fatalf("agent defaults not applied: %+v", cfg.Agent)
	}
}
