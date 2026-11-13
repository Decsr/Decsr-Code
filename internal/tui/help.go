package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// helpSection is one titled group of keys in the help overlay.
type helpSection struct {
	title string
	keys  []keyHint
}

// renderHelp draws the full-screen keyboard help. Sections flow into two
// columns when width >= 100. The result has at most height lines and every
// line fits width; sections that do not fit are cut and end with "…".
func renderHelp(width, height int, title string, sections []helpSection) string {
	return renderHelpWithHeader(width, height, screenHeader(width, title, ""), sections)
}

// renderHelpPane is renderHelp for screens whose parent already draws a DECSR
// header: it starts with a single pane-style title line instead.
func renderHelpPane(width, height int, title string, sections []helpSection) string {
	head := lipgloss.NewStyle().Foreground(galaxyAzure).Bold(true).Render(title)
	return renderHelpWithHeader(width, height, ansi.Truncate(" "+head, width, "…"), sections)
}

func renderHelpWithHeader(width, height int, header string, sections []helpSection) string {
	footer := hintBar(width, []keyHint{{"? / Esc", "close"}}, "")
	bodyH := height - 2
	if bodyH < 0 {
		bodyH = 0
	}

	cols := 1
	if width >= 100 {
		cols = 2
	}
	colW := (width - 2) / cols

	keyW := 0
	for _, s := range sections {
		for _, k := range s.keys {
			if w := ansi.StringWidth(keycap(k.key)); w > keyW {
				keyW = w
			}
		}
	}

	titleStyle := lipgloss.NewStyle().Foreground(galaxyAzure).Bold(true)
	dim := lipgloss.NewStyle().Foreground(crt.dim)
	render := func(s helpSection) []string {
		lines := []string{titleStyle.Render(strings.ToUpper(s.title))}
		for _, k := range s.keys {
			cap := keycap(k.key)
			pad := strings.Repeat(" ", keyW-ansi.StringWidth(cap))
			lines = append(lines, ansi.Truncate(cap+pad+" "+dim.Render(k.label), colW, "…"))
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
		if len(c) > rows {
			rows = len(c)
		}
	}
	lines := make([]string, rows)
	for i := range lines {
		var sb strings.Builder
		sb.WriteString(" ")
		for ci, c := range columns {
			cell := ""
			if i < len(c) {
				cell = c[i]
			}
			if ci < len(columns)-1 {
				cell += strings.Repeat(" ", max(0, colW-ansi.StringWidth(cell)))
			}
			sb.WriteString(cell)
		}
		lines[i] = ansi.Truncate(sb.String(), width, "")
	}

	if len(lines) > bodyH {
		lines = lines[:bodyH]
		if bodyH > 0 {
			lines[bodyH-1] = " " + dim.Render("…")
		}
	}
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	return strings.Join(append(append([]string{header}, lines...), footer), "\n")
}

func homeHelp() []helpSection {
	return []helpSection{
		{"Navigate", []keyHint{
			{"↑↓ / k j", "move in the menu"},
			{"Home / End", "first / last entry"},
			{"← →", "scroll the page sideways"},
			{"Enter / Space", "run the selected entry"},
		}},
		{"Tasks", []keyHint{
			{"w", "workspace: chat, split into nodes, assign agents"},
			{"1", "classic pipeline (new task)"},
			{"r", "resume the interrupted task"},
			{"d", "done tasks"},
			{"c", "reset artifacts"},
		}},
		{"Settings", []keyHint{
			{"o", "open another project"},
			{"s", "project setup"},
			{"g", "global settings"},
			{"m", "model memory"},
		}},
		{"Exit", []keyHint{
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
