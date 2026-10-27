package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/gitclient"
)

func testGitPanel(n int) GitPanelModel {
	files := make([]gitclient.FileStatus, n)
	for i := range files {
		files[i] = gitclient.FileStatus{Path: fmt.Sprintf("f%03d.go", i), Code: " M", Unstaged: true}
	}
	return GitPanelModel{files: files, diff: viewport.New(40, 10), width: 80, height: 20}
}

func TestGitPanel_FileListScrollsWithCursor(t *testing.T) {
	m := testGitPanel(100)
	m.cursor = 80
	view := m.View()
	if !strings.Contains(view, "f080.go") {
		t.Fatal("selected file must be visible")
	}
	if strings.Contains(view, "f000.go") {
		t.Fatal("entries above the window should be scrolled out")
	}
	if got := strings.Count(view, "\n") + 1; got > m.height+2 {
		t.Fatalf("view has %d lines, exceeds terminal height %d", got, m.height)
	}
}

func TestGitPanel_CommitInputAcceptsPasteAndUnicode(t *testing.T) {
	m := testGitPanel(1)
	m.focus = gitFocusCommit
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix: 日本語 café\nbody"), Paste: true})
	if want := "fix: 日本語 café body"; m.commitMsg != want {
		t.Fatalf("commitMsg = %q, want %q", m.commitMsg, want)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if want := "fix: 日本語 café bod"; m.commitMsg != want {
		t.Fatalf("after backspace commitMsg = %q, want %q", m.commitMsg, want)
	}
	m.commitMsg = "日"
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.commitMsg != "" {
		t.Fatalf("backspace must remove a whole rune, got %q", m.commitMsg)
	}
}

func TestGitPanel_ResetRequiresConfirmation(t *testing.T) {
	m := testGitPanel(2) // gc is nil: any reset attempt would panic
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if !m.confirmReset || cmd != nil {
		t.Fatalf("r must only ask for confirmation (confirm=%v cmd=%v)", m.confirmReset, cmd != nil)
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.confirmReset || cmd != nil || !strings.Contains(m.statusMsg, "cancel") {
		t.Fatalf("n must cancel (confirm=%v status=%q)", m.confirmReset, m.statusMsg)
	}
}
