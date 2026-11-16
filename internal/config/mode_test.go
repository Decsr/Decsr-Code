package config

import (
	"strings"
	"testing"
)

func TestEffectiveModeDefaults(t *testing.T) {
	for runner, want := range map[string]string{
		"": ModeAgent, "opencode": ModeAgent, "codex": ModeAgent,
		"claude": ModeText, "ollama": ModeText, "openai": ModeText, "copilot": ModeText,
	} {
		if got := (AgentConfig{Runner: runner}).EffectiveMode(); got != want {
			t.Errorf("runner %q: mode = %q, want %q", runner, got, want)
		}
	}
	if got := (AgentConfig{Runner: "claude", Mode: ModeAgent}).EffectiveMode(); got != ModeAgent {
		t.Errorf("explicit agent = %q", got)
	}
	if got := (AgentConfig{Runner: "codex", Mode: ModeText}).EffectiveMode(); got != ModeText {
		t.Errorf("explicit text = %q", got)
	}
}

func TestModeError(t *testing.T) {
	tests := []struct {
		ac   AgentConfig
		want string // "" = valid
	}{
		{AgentConfig{Runner: "claude"}, ""},
		{AgentConfig{Runner: "claude", Mode: ModeAgent}, ""},
		{AgentConfig{Runner: "codex", Mode: ModeText}, ""},
		{AgentConfig{Runner: "opencode"}, ""},
		{AgentConfig{Runner: "ollama", Mode: ModeAgent}, "agent mode needs claude, opencode or codex"},
		{AgentConfig{Runner: "openai", Mode: ModeAgent}, "agent mode needs claude, opencode or codex"},
		{AgentConfig{Runner: "opencode", Mode: ModeText}, "no read-only switch"},
		{AgentConfig{Mode: ModeText}, "no read-only switch"}, // empty runner is opencode
		{AgentConfig{Runner: "claude", Mode: "bogus"}, "invalid mode"},
	}
	for _, tc := range tests {
		err := tc.ac.ModeError()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%+v: unexpected error %v", tc.ac, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%+v: error = %v, want %q", tc.ac, err, tc.want)
		}
	}
}

func TestModeMergeAndProfileRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := Save(root, Config{Agents: map[string]AgentConfig{"coder": {Runner: "claude", Mode: ModeAgent}}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents["coder"].Mode; got != ModeAgent {
		t.Fatalf("mode after load = %q", got)
	}
	if err := SaveProfile("agentic", cfg.Agents); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProfile("agentic")
	if err != nil || p.Agents["coder"].Mode != ModeAgent {
		t.Fatalf("profile = %+v, %v", p.Agents["coder"], err)
	}
	// Apply to a project without a mode, then to one that has another mode.
	other := t.TempDir()
	if err := ApplyProfile(other, "agentic"); err != nil {
		t.Fatal(err)
	}
	if got := LoadProject(other).Agents["coder"].Mode; got != ModeAgent {
		t.Errorf("applied mode = %q", got)
	}
	// A role saved through SaveProjectRole keeps the mode it is given.
	if err := SaveProjectRole(other, "docs", AgentConfig{Runner: "claude", Mode: ModeText}); err != nil {
		t.Fatal(err)
	}
	if got := LoadProject(other).Agents["docs"].Mode; got != ModeText {
		t.Errorf("saved role mode = %q", got)
	}
}
