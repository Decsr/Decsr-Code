package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// TestPanelRowsFitTheirFrame draws every panel at 80 and 120 columns and checks
// that no line is wider than the frame, that every line is as wide as the top
// border, and that the plain-text width agrees with the terminal width of the
// glyphs.
func TestPanelRowsFitTheirFrame(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	cmds := []string{"/usage", "/status", "/model", "/provider", "/config", "/context", "/doctor", "/memory", "/plugin"}
	for _, size := range [][2]int{{60, 24}, {80, 40}, {100, 24}, {120, 40}} {
		for _, line := range cmds {
			t.Run(line, func(t *testing.T) {
				h := panelHarness(t)
				h.size(size[0], size[1])
				h.toGraph()
				h.key("tab")
				h.typeCmd("/rename " + strings.Repeat("a very long session title ", 8))
				h.openPanelCmd(line)
				if h.m.overlay != wsOverlayPanel {
					t.Fatalf("%s did not open a panel", line)
				}
				// the panel kinds with long values: a long budget, a long plugin note
				h.m.panel.note, h.m.panel.noteKind = strings.Repeat("a long result line ", 12), ui.ResultOK
				for tab := 0; tab < max(len(tabsOf(h.m.panel.kind)), 1); tab++ {
					box := h.m.panelBox()
					lines := strings.Split(box, "\n")
					want := ansi.StringWidth(lines[0])
					if want > size[0]-2*ui.Margin {
						t.Fatalf("%s tab %d: frame of %d cells on %d columns", line, tab, want, size[0])
					}
					for _, l := range lines {
						plain := ansi.Strip(l)
						if w := ansi.StringWidth(l); w != want {
							t.Errorf("%s tab %d: line of %d cells in a frame of %d: %q", line, tab, w, want, plain)
						}
						if w := runewidth.StringWidth(plain); w > want {
							t.Errorf("%s tab %d: line of %d terminal cells in a frame of %d: %q", line, tab, w, want, plain)
						}
					}
					h.key("tab")
				}
			})
		}
	}
}
