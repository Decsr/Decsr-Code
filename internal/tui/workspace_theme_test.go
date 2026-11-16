package tui

import (
	"regexp"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var sessionID = regexp.MustCompile(`decsr · \d+`)

// The Workspace has the same text layout on dark and light terminals; only
// the colours change.
func TestWorkspaceViewDarkAndLight(t *testing.T) {
	prev := lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(prev) })

	views := map[bool]string{}
	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		h := newWSHarness(t)
		h.graph = wsSmallGraph
		h.toGraph()
		h.size(120, 34)
		views[dark] = sessionID.ReplaceAllString(h.view(), "decsr · N")
		if views[dark] == "" {
			t.Fatalf("dark=%v: empty view", dark)
		}
	}
	if views[true] != views[false] {
		t.Errorf("layout differs between dark and light:\n--- dark\n%s\n--- light\n%s", views[true], views[false])
	}
}

// TestWorkspaceRenderSample logs the plain-text Workspace at 120x34 (go test -v).
func TestWorkspaceRenderSample(t *testing.T) {
	lipgloss.SetHasDarkBackground(true)
	h := newWSHarness(t)
	h.graph = wsSmallGraph
	h.toGraph()
	h.size(120, 34)
	t.Logf("\n%s", h.view())
}
