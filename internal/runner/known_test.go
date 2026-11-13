package runner

import (
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

// config keeps its own list of runner names (it cannot import runner); this
// keeps it in step with the factory.
func TestConfigKnownRunnerMatchesFactory(t *testing.T) {
	for _, name := range []string{"", "opencode", "claude", "ollama", "mlx", "codex", "copilot", "lmstudio", "nope", "Claude"} {
		_, err := New(config.AgentConfig{Runner: name}, nil, "")
		if (err == nil) != config.KnownRunner(name) {
			t.Errorf("runner %q: factory ok=%v, config.KnownRunner=%v", name, err == nil, config.KnownRunner(name))
		}
	}
}
