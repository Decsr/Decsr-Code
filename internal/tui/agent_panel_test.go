package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func TestAgentPanel_WrapsLongLinesToViewportWidth(t *testing.T) {
	m := NewAgentPanel("pm")
	m.SetSize(42, 10)
	m.addLine("To create a simple, engaging, and focused single-player competitive experience based on the classic game of Tic-Tac-Toe.")
	m.syncViewport()

	for _, line := range strings.Split(m.vp.View(), "\n") {
		if ansi.StringWidth(line) > m.vp.Width {
			t.Fatalf("line exceeds viewport width %d: %q", m.vp.Width, line)
		}
	}
}

func TestAgentPanel_ScrollBorderIsValidUTF8AndFullWidth(t *testing.T) {
	m := NewAgentPanel("pm")
	m.SetSize(42, 8)
	for i := 0; i < 40; i++ {
		m.addLine("line\n")
	}
	m.syncViewport()

	lines := strings.Split(m.View(), "\n")
	bottom := lines[len(lines)-1]
	if !utf8.ValidString(bottom) {
		t.Fatalf("bottom border is invalid UTF-8: %q", bottom)
	}
	if got, want := ansi.StringWidth(bottom), ansi.StringWidth(lines[0]); got != want {
		t.Fatalf("bottom border width = %d, want %d", got, want)
	}
	if !strings.Contains(bottom, "%") {
		t.Fatalf("expected scroll indicator in bottom border: %q", bottom)
	}
}

func TestAgentPanel_SetSizeSameSizeDoesNotResync(t *testing.T) {
	m := NewAgentPanel("pm")
	m.SetSize(42, 10)
	m.vp.SetContent("sentinel")
	m.SetSize(42, 10)
	if got := m.vp.View(); !strings.Contains(got, "sentinel") {
		t.Fatalf("SetSize with unchanged size re-synced the viewport: %q", got)
	}
	m.SetSize(50, 10)
	if got := m.vp.View(); strings.Contains(got, "sentinel") {
		t.Fatalf("SetSize with a new size should re-sync the viewport: %q", got)
	}
}
