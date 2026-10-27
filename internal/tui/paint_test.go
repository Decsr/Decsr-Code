package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func forceTrueColor(t *testing.T) string {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	return termenv.TrueColor.Color(string(crt.bg)).Sequence(true)
}

func TestPaintBackgroundPadsPlainLine(t *testing.T) {
	seq := forceTrueColor(t)
	lines := strings.Split(paintBackground("hi", 6, 1), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "\x1b["+seq+"m") {
		t.Fatalf("unexpected output %q", lines)
	}
	if w := ansi.StringWidth(lines[0]); w != 6 {
		t.Fatalf("width = %d, want 6", w)
	}
	if !strings.HasSuffix(lines[0], "\x1b[0m") {
		t.Fatalf("line not reset: %q", lines[0])
	}
}

func TestPaintBackgroundReappliesAfterReset(t *testing.T) {
	seq := "\x1b[" + forceTrueColor(t) + "m"
	got := paintBackground("\x1b[31mred\x1b[0m tail", 20, 1)
	if !strings.Contains(got, "\x1b[0m"+seq+" tail") {
		t.Fatalf("background not re-applied after reset: %q", got)
	}
}

func TestPaintBackgroundPadsHeight(t *testing.T) {
	forceTrueColor(t)
	if n := len(strings.Split(paintBackground("a", 4, 3), "\n")); n != 3 {
		t.Fatalf("lines = %d, want 3", n)
	}
	if n := len(strings.Split(paintBackground("a\nb\nc", 4, 2), "\n")); n != 3 {
		t.Fatalf("taller view lines = %d, want 3", n)
	}
}

func TestPaintBackgroundUnknownSize(t *testing.T) {
	forceTrueColor(t)
	if got := paintBackground("x", 0, 0); got != "x" {
		t.Fatalf("got %q", got)
	}
}
