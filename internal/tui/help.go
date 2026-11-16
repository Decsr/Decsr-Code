package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// helpSection is one titled group of keys in the help overlay.
type helpSection struct {
	title string
	keys  []keyHint
}

// helpTitle turns an ALL-CAPS title ("KEYBOARD — HOME") into sentence case.
func helpTitle(t string) string {
	if t != strings.ToUpper(t) {
		return t
	}
	r := []rune(strings.ToLower(t))
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

// renderHelp draws the keyboard help as an overlay on a blank screen under
// the DECSR header. Sections flow into two columns when width >= 100. The
// result has at most height lines and every line fits width; sections that do
// not fit are cut and end with "…".
func renderHelp(width, height int, title string, sections []helpSection) string {
	return renderHelpWithHeader(width, height, ui.Header("decsr · help", "", width), title, sections)
}

// renderHelpPane is renderHelp for screens whose parent already draws a DECSR
// header: it starts with a single pane-style title line instead.
func renderHelpPane(width, height int, title string, sections []helpSection) string {
	return renderHelpWithHeader(width, height, ansi.Truncate(ui.PaneLabel(helpTitle(title), true, width), width, "…"), title, sections)
}

func renderHelpWithHeader(width, height int, header, title string, sections []helpSection) string {
	base := make([]string, max(height, 1))
	base[0] = header

	cols := 1
	if width >= 100 {
		cols = 2
	}
	ow := min(width-4, 66)
	if cols == 2 {
		ow = min(width-4, 96)
	}
	inner := ow - 4
	colW := inner
	if cols == 2 {
		colW = (inner - 2) / 2
	}

	keyW := 0
	for _, s := range sections {
		for _, k := range s.keys {
			keyW = max(keyW, ansi.StringWidth(k.key))
		}
	}
	keyW = min(keyW, max(colW/2, 1))

	render := func(s helpSection) []string {
		lines := []string{ui.Truncate(dim(s.title), colW)}
		for _, k := range s.keys {
			key := ui.Truncate(k.key, keyW)
			pad := strings.Repeat(" ", keyW-ansi.StringWidth(key))
			lines = append(lines, ui.Truncate(text(key)+pad+"  "+dim(k.label), colW))
		}
		return append(lines, "")
	}

	columns := make([][]string, cols)
	total := 0
	for _, s := range sections {
		total += len(s.keys) + 2
	}
	col := 0
	for _, s := range sections {
		if col < cols-1 && len(columns[col]) >= (total+cols-1)/cols {
			col++
		}
		columns[col] = append(columns[col], render(s)...)
	}
	rows := 0
	for _, c := range columns {
		rows = max(rows, len(c))
	}
	lines := make([]string, rows)
	for i := range lines {
		var sb strings.Builder
		for ci, c := range columns {
			cell := ""
			if i < len(c) {
				cell = c[i]
			}
			if ci < len(columns)-1 {
				cell += strings.Repeat(" ", max(colW+2-ansi.StringWidth(cell), 0))
			}
			sb.WriteString(cell)
		}
		lines[i] = strings.TrimRight(sb.String(), " ")
	}
	for len(lines) > 0 && ansi.StringWidth(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}

	// The box takes a border row top and bottom, a blank row and the hint row.
	if room := height - 2 - 4; len(lines) > room {
		if room < 1 {
			return strings.Join(base, "\n")
		}
		lines = append(lines[:room-1], dim("…"))
	}
	box := ui.Overlay(helpTitle(title), lines, []ui.Hint{{Key: "? / Esc", Label: "close"}}, ow)
	return ui.PlaceOverlay(strings.Join(base, "\n"), box, width, height)
}

func homeHelp() []helpSection {
	return []helpSection{
		{"Navigate", []keyHint{
			{"↑↓", "move in the column"},
			{"Tab / ← →", "switch between projects and actions"},
			{"Home / End", "first / last row"},
			{"Enter / Space", "open the selected row"},
		}},
		{"Start", []keyHint{
			{"w", "workspace: chat, split into nodes, assign agents"},
			{"r", "resume the interrupted task"},
			{"o", "open another project"},
			{"d", "done tasks"},
		}},
		{"Settings", []keyHint{
			{"g", "global settings"},
			{"s", "project setup"},
			{"k", "connect a provider"},
			{"m", "model memory"},
		}},
		{"More", []keyHint{
			{"1", "classic pipeline (frozen)"},
			{"c", "reset artifacts"},
			{"?", "this help"},
			{"q", "quit"},
		}},
	}
}

func cockpitHelp() []helpSection {
	return []helpSection{
		{"Navigate", []keyHint{
			{"Tab / Shift+Tab", "cycle focus: workflow, center, inspector"},
			{"↑↓ / j k", "move in the focused pane"},
			{"PgUp / PgDn", "scroll the center pane"},
			{"Home / End", "top / follow the latest"},
			{"Enter", "focus the center / inspect the item"},
		}},
		{"Views", []keyHint{
			{"1-7", "switch center tab"},
			{"p a x f t o", "plan, agents, tokens, files, tests, output"},
			{"i", "toggle the inspector"},
		}},
		{"Run", []keyHint{
			{"Ctrl+A", "approve the current gate"},
			{"Ctrl+O", "reopen the gate artifact"},
			{"Ctrl+G", "git diff view"},
			{"Ctrl+C", "PM chat"},
			{"Ctrl+X", "cancel the run"},
			{"Ctrl+P", "pause to change the model"},
			{"Ctrl+E", "resume the build-fix loop"},
		}},
		{"Exit", []keyHint{
			{"Esc", "cancel / back to the menu when done"},
			{"q", "quit"},
		}},
	}
}
