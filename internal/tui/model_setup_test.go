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

func TestApplyProjectSetup_KeepsCustomRoleFields(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "agents:\n  docs_writer:\n    description: Writes docs\n    read_only: true\n  qa:\n    description: custom qa\n    read_only: true\n    model: old\n"
	if err := os.WriteFile(filepath.Join(root, ".decsr", "project.yaml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModelSetupModel(root, config.LoadProject(root))
	if _, ok := m.setup.agentOverrides["qa"]; !ok {
		t.Fatal("setup model should load the qa override")
	}
	msg := setupDoneMsg{
		projectName: "p",
		agentOverrides: map[string]agentSetupOverride{
			"coder":       {runner: "opencode", model: "new-model"},
			"docs_writer": {model: "writer-model"},
		},
	}
	if _, ok := applyProjectSetup(root, msg); !ok {
		t.Fatal("save failed")
	}
	proj := config.LoadProject(root)
	dw, ok := proj.Agents["docs_writer"]
	if !ok || dw.Description != "Writes docs" || !dw.ReadOnly || dw.Model != "writer-model" {
		t.Fatalf("docs_writer = %+v", dw)
	}
	if proj.Agents["coder"].Model != "new-model" {
		t.Fatalf("coder = %+v", proj.Agents["coder"])
	}
	// Built-in entries keep description and read-only too.
	msg.agentOverrides["qa"] = agentSetupOverride{model: "q2"}
	if _, ok := applyProjectSetup(root, msg); !ok {
		t.Fatal("save failed")
	}
	qa := config.LoadProject(root).Agents["qa"]
	if qa.Description != "custom qa" || !qa.ReadOnly || qa.Model != "q2" {
		t.Fatalf("qa = %+v", qa)
	}
}

func TestApplyProjectSetup_KeepsUserSkillsAndProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "project:\n  profile: team\nagents:\n  coder:\n    skills: [style]\n  qa:\n    skills: [review]\n  docs_writer:\n    description: Writes docs\n    skills: [style, tone]\n"
	if err := os.WriteFile(filepath.Join(root, ".decsr", "project.yaml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := setupDoneMsg{
		projectName: "p",
		agentOverrides: map[string]agentSetupOverride{
			"coder": {model: "new-model"},
		},
	}
	if _, ok := applyProjectSetup(root, msg); !ok {
		t.Fatal("save failed")
	}
	proj := config.LoadProject(root)
	if got := proj.Agents["coder"]; got.Model != "new-model" || len(got.UserSkills) != 1 || got.UserSkills[0] != "style" {
		t.Errorf("coder = %+v", got)
	}
	if got := proj.Agents["qa"]; len(got.UserSkills) != 1 || got.UserSkills[0] != "review" {
		t.Errorf("qa (built-in, no override) = %+v", got)
	}
	if got := proj.Agents["docs_writer"]; len(got.UserSkills) != 2 {
		t.Errorf("docs_writer = %+v", got)
	}
	if proj.Project.Profile != "team" {
		t.Errorf("profile = %q", proj.Project.Profile)
	}
}
