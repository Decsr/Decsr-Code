package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestApplyProjectSetup_PersistsAndReloads(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}

	msg := setupDoneMsg{
		projectName: "My Project",
		agentOverrides: map[string]agentSetupOverride{
			"coder": {runner: "lmstudio", model: "qwen-coder", effort: "high"},
		},
	}

	cfg, ok := applyProjectSetup(root, msg)
	if !ok {
		t.Fatal("applyProjectSetup should succeed")
	}
	ac := cfg.Agents["coder"]
	if ac.Runner != "lmstudio" || ac.Model != "qwen-coder" || ac.Effort != "high" {
		t.Fatalf("expected lmstudio/qwen-coder/high, got %s/%s/%s", ac.Runner, ac.Model, ac.Effort)
	}

	// The override must be persisted to the project config file.
	proj := config.LoadProject(root)
	if proj.Agents["coder"].Model != "qwen-coder" {
		t.Fatalf("override not persisted, got %q", proj.Agents["coder"].Model)
	}
	if proj.Agents["coder"].Effort != "high" {
		t.Fatalf("effort override not persisted, got %q", proj.Agents["coder"].Effort)
	}
	if proj.Project.Name != "My Project" {
		t.Fatalf("project name not persisted, got %q", proj.Project.Name)
	}
}

func TestSetupModel_EffortDefaultsAndOverrides(t *testing.T) {
	projectOverrides := map[string]config.AgentConfig{
		"coder": {Runner: "claude", Model: "sonnet", Effort: "max"},
	}
	m := NewSetupModelWithOverrides("opencode", "gpt-5.2", "high", "go", "proj", projectOverrides)

	if m.effort != "high" {
		t.Fatalf("global effort = %q, want high", m.effort)
	}
	if ov := m.agentOverrides["coder"]; ov.effort != "max" {
		t.Fatalf("coder effort override = %q, want max", ov.effort)
	}

	choices := effortChoices([]string{"low", "medium", "high"})
	if got := effortLabels(choices); got[0] != "(provider default)" {
		t.Fatalf("default label = %q", got[0])
	}
	if idx, found := effortChoiceIndex(choices, "medium"); !found || idx != 2 {
		t.Fatalf("medium index = %d, found=%v", idx, found)
	}
	if _, found := effortChoiceIndex(choices, "xhigh"); found {
		t.Fatal("xhigh should not be found")
	}
}
