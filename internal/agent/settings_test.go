package agent

import "FileEngine/internal/config"

// testCfg is the settings default with overrides, as a run's snapshot.
func testCfg(mut ...func(*config.AgentConfig)) config.AgentConfig {
	c := config.DefaultAgent()
	for _, m := range mut {
		m(&c)
	}
	return c
}

// runCfg is the snapshot the wire and brain tests run with.
func runCfg(c *config.AgentConfig) {
	c.MaxStep, c.ChatMaxStep, c.MaxRetries = 20, 20, 1
}
