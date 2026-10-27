package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDirBrowser_NarrowWidthDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 12; i++ {
		if err := os.Mkdir(filepath.Join(root, "dir"+strings.Repeat("x", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := DirBrowserModel{cwd: root, width: 80, height: 24}
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	for w := -1; w <= 8; w++ {
		m.SetSize(w, 24)
		_ = m.View()
	}
}

func TestDirBrowser_TruncatesWideNamesAsValidUTF8(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, strings.Repeat("日本語", 20)), 0o755); err != nil {
		t.Fatal(err)
	}
	m := DirBrowserModel{cwd: root, width: 30, height: 24}
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	if v := m.View(); !utf8.ValidString(v) {
		t.Fatalf("invalid UTF-8 in view: %q", v)
	}
}

func TestFileTree_TruncatesWideNamesAsValidUTF8(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, strings.Repeat("日本語", 25)+".md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewFileTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if v := m.View(); !utf8.ValidString(v) {
		t.Fatalf("invalid UTF-8 in view: %q", v)
	}
}

func TestProjectPicker_NarrowWidthDoesNotPanic(t *testing.T) {
	m := NewProjectPicker(t.TempDir())
	for w := -1; w <= 8; w++ {
		m.width = w
		_ = m.View()
	}
}

func TestDirBrowser_UnreadableDirKeepsCwd(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	m := DirBrowserModel{cwd: root, width: 80, height: 24}
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	for i, e := range m.entries {
		if e.name == "locked" {
			m.cursor = i
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cwd != root {
		t.Fatalf("cwd = %q, want %q after failed read", m.cwd, root)
	}
	if m.err == "" || !strings.Contains(m.View(), "permission") {
		t.Fatalf("expected a visible permission error, err=%q", m.err)
	}
}
