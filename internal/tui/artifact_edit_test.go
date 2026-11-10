package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func longArtifactViewer(t *testing.T, lines int) (ArtifactViewerModel, string) {
	t.Helper()
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	path := filepath.Join(dir, "context_pack.md")
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newArtifactViewer(dir, "context_pack.md", 100, 30, nil)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	return nm, path
}

func TestArtifactEdit_NewlineInLongFile(t *testing.T) {
	m, path := longArtifactViewer(t, 150)
	if !m.editMode {
		t.Fatal("expected edit mode")
	}
	nm := m
	nm, _ = nm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm, _ = nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("NEWTEXT")})
	nm, _ = nm.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	_ = nm
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got < 151 {
		t.Fatalf("expected a new line inserted, newline count = %d", got)
	}
	if !strings.Contains(string(data), "NEWTEXT") {
		t.Fatal("typed text not saved")
	}
}

func TestArtifactEdit_EscWarnsOnUnsavedChanges(t *testing.T) {
	m, path := longArtifactViewer(t, 10)
	before, _ := os.ReadFile(path)
	nm := m
	nm, _ = nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	nm, _ = nm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	v := nm
	if !v.editMode {
		t.Fatal("first Esc with unsaved changes must keep edit mode")
	}
	if !strings.Contains(v.View(), "unsaved changes") {
		t.Fatal("expected unsaved-changes warning in view")
	}
	nm, _ = nm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if nm.editMode {
		t.Fatal("second Esc must discard")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("file must be unchanged after discard")
	}
}

func TestArtifactEdit_EscWithoutChangesExitsImmediately(t *testing.T) {
	m, _ := longArtifactViewer(t, 10)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if nm.editMode {
		t.Fatal("Esc without changes must exit edit mode")
	}
}
