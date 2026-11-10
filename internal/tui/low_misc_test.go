package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestMDPicker_HomeEndJump(t *testing.T) {
	m := mdPicker{files: []string{"a.md", "b.md", "c.md", "d.md"}, selected: map[string]bool{}, height: 20}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if m.cursor != 3 {
		t.Fatalf("end: cursor = %d, want 3", m.cursor)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !m.selected["d.md"] {
		t.Fatalf("space after end should toggle last file, got %v", m.selected)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if m.cursor != 0 {
		t.Fatalf("home: cursor = %d, want 0", m.cursor)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.cursor == 0 {
		t.Fatalf("pgdown did not move cursor")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.cursor != 0 {
		t.Fatalf("pgup should return to 0, got %d", m.cursor)
	}
}

func TestRemoveLastRecentProject_WritesEmptyArray(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()
	if err := SaveRecentProject(proj); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRecentProject(proj); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(projectsFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "[]" {
		t.Fatalf("projects.json = %q, want []", got)
	}
	if got := LoadRecentProjects(); len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}
}

func TestLoadRecentProjects_NullFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := projectsFilePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadRecentProjects(); len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}
	if err := SaveRecentProject(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := LoadRecentProjects(); len(got) != 1 {
		t.Fatalf("expected 1 project after save, got %v", got)
	}
}

func TestNeedsModulePath_GoModWithoutModuleLine(t *testing.T) {
	cfg := config.Config{}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("go 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NeedsModulePath(root, cfg) {
		t.Fatal("go.mod without module directive should need a module path")
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("// c\nmodule example.com/x\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if NeedsModulePath(root, cfg) {
		t.Fatal("go.mod with module directive should not need a module path")
	}
}

func TestEnsureGoModModule(t *testing.T) {
	root := t.TempDir()
	gm := filepath.Join(root, "go.mod")
	// No go.mod: untouched.
	if err := ensureGoModModule(root, "example.com/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gm); err == nil {
		t.Fatal("go.mod must not be created when absent")
	}
	if err := os.WriteFile(gm, []byte("go 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureGoModModule(root, "example.com/x"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(gm)
	if string(data) != "module example.com/x\n\ngo 1.22\n" {
		t.Fatalf("unexpected go.mod: %q", data)
	}
	// Existing module line is preserved.
	if err := ensureGoModModule(root, "example.com/y"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(gm)
	if !strings.Contains(string(data), "example.com/x") || strings.Contains(string(data), "example.com/y") {
		t.Fatalf("existing module overwritten: %q", data)
	}
}

func TestSetup_DeleteAgentPromptsFeedback(t *testing.T) {
	m := NewSetupModelWithOverrides("claude", "sonnet", "high", "go", "proj", nil)
	m.root = t.TempDir()
	role := m.agentRoles[m.agentCursor]

	m.deleteAgentPrompts()
	if want := "no exported prompt for " + role; m.promptStatus != want {
		t.Fatalf("status = %q, want %q", m.promptStatus, want)
	}

	m.exportAgentPrompts()
	if !m.hasCustomPrompt(role) {
		t.Skip("role has no exportable prompt")
	}
	m.deleteAgentPrompts()
	if !strings.HasPrefix(m.promptStatus, "deleted .decsr/prompts/") {
		t.Fatalf("status = %q, want deleted .decsr/prompts/...", m.promptStatus)
	}
	if m.hasCustomPrompt(role) {
		t.Fatal("prompt still present")
	}
}
