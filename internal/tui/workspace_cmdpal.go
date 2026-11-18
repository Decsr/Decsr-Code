package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

const (
	cmdPalMaxRows = 8  // command rows shown at once
	cmdPalNameW   = 24 // name column; a longer name is cut with …
	cmdPalArgsW   = 18 // args column; a longer hint is cut with …
)

// cmdMatchPositions says whether query (typed after the slash, lower case)
// matches name (without the slash) and which runes of name matched. tier 0 is
// a prefix, 1 a contiguous part, 2 the letters in order.
func cmdMatchPositions(name, query string) (tier int, pos []int, ok bool) {
	n, q := []rune(name), []rune(query)
	if len(q) == 0 {
		return 0, nil, true
	}
	if strings.HasPrefix(name, query) {
		for i := range q {
			pos = append(pos, i)
		}
		return 0, pos, true
	}
	if i := strings.Index(name, query); i >= 0 {
		start := len([]rune(name[:i]))
		for j := range q {
			pos = append(pos, start+j)
		}
		return 1, pos, true
	}
	qi := 0
	for i, r := range n {
		if qi < len(q) && r == q[qi] {
			pos = append(pos, i)
			qi++
		}
	}
	if qi == len(q) {
		return 2, pos, true
	}
	return 0, nil, false
}

// rankCommands filters cmds by the typed text (with its slash) and orders the
// matches best first, keeping the table order among equals. A bare "/" keeps
// every command.
func rankCommands(cmds []wsCommand, typed string) []wsCommand {
	q := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(typed)), "/")
	if q == "" {
		return append([]wsCommand(nil), cmds...)
	}
	var tiers [3][]wsCommand
	for _, c := range cmds {
		best, found := 3, false
		for _, nm := range append([]string{c.name}, c.aliases...) {
			if t, _, ok := cmdMatchPositions(cmdKey(nm), q); ok && t < best {
				best, found = t, true
			}
		}
		if found {
			tiers[best] = append(tiers[best], c)
		}
	}
	return append(append(tiers[0], tiers[1]...), tiers[2]...)
}

// palEntry is one display line of the command palette: a group header or a
// command row.
type palEntry struct {
	header string
	cmd    int // index into the matches; -1 for a header
}

// cmdPalCols are the widths of the name and args columns for a box content
// width: fixed for a given width, whatever rows are shown.
func cmdPalCols(inner int) (nameW, argsW int) {
	return min(cmdPalNameW, max(inner/4, 8)), min(cmdPalArgsW, max(inner/4, 0))
}

// renderCmdPalette draws the "/" palette as a rounded box of a fixed size:
// width cells wide, and always the same number of lines (up to cmdPalMaxRows
// rows, one hint line and the borders; fewer only when maxRows is smaller).
// The rows are grouped under headers while the query is bare, ranked with a
// group tag at the right while typing; short lists are padded with blank
// rows, and no match says so in the first row. The name and args columns have
// fixed widths, longer text is cut with …. idx stays visible; the hint line
// counts the rows that are hidden.
func renderCmdPalette(cmds []wsCommand, typed string, idx, width, maxRows int) []string {
	rows := min(cmdPalMaxRows, maxRows-3)
	inner := width - 4
	if rows < 1 || inner < 20 {
		return nil
	}
	q := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(typed)), "/")
	bare := q == ""
	idx = min(max(idx, 0), max(len(cmds)-1, 0))

	var entries []palEntry
	if bare {
		last := ""
		for i, c := range cmds {
			if c.group != last {
				entries = append(entries, palEntry{header: c.group, cmd: -1})
				last = c.group
			}
			entries = append(entries, palEntry{cmd: i})
		}
	} else {
		for i := range cmds {
			entries = append(entries, palEntry{cmd: i})
		}
	}

	// The window: rows lines, headers counting as rows when bare.
	selLine := 0
	for i, e := range entries {
		if e.cmd == idx {
			selLine = i
		}
	}
	start := 0
	if selLine >= rows {
		start = selLine - rows + 1
	}
	if bare && start > 0 && entries[start].cmd >= 0 && entries[start-1].cmd < 0 {
		start-- // keep the group header of the first visible row
	}
	end := min(start+rows, len(entries))
	for start > 0 && end-start < rows { // nothing left below: show a full page
		start--
	}

	nameW, argsW := cmdPalCols(inner)
	argsOf := func(c wsCommand) string {
		if c.hint != "" {
			return c.hint
		}
		return c.args
	}
	blank := strings.Repeat(" ", inner)
	var lines []string
	if len(cmds) == 0 {
		msg := ansi.Truncate("no command matches /"+q, inner, "…")
		lines = append(lines, lipgloss.NewStyle().Foreground(ui.Muted).Render(msg))
	}
	for _, e := range entries[start:end] {
		if e.cmd < 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(ui.GroupColor(e.header)).Bold(true).Render(strings.ToUpper(e.header)))
			continue
		}
		lines = append(lines, cmdPalRow(cmds[e.cmd], q, e.cmd == idx, bare, inner, nameW, argsW, argsOf(cmds[e.cmd])))
	}
	for len(lines) < rows {
		lines = append(lines, blank)
	}

	below, above := 0, 0
	for i, e := range entries {
		if e.cmd < 0 {
			continue
		}
		switch {
		case i >= end:
			below++
		case i < start:
			above++
		}
	}
	note := ""
	switch {
	case above > 0 && below > 0:
		note = fmt.Sprintf("↑ %d above · ↓ %d more · ↑↓ Tab", above, below)
	case below > 0:
		note = fmt.Sprintf("↓ %d more · ↑↓ Tab", below)
	case above > 0:
		note = fmt.Sprintf("↑ %d above · ↑↓ Tab", above)
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(ui.Muted).Render(note))

	bc := ui.Border
	if len(cmds) > 0 {
		if c := cmds[idx]; c.group != "" {
			bc = ui.GroupColor(c.group)
		}
	}
	bs := lipgloss.NewStyle().Foreground(bc)
	out := []string{bs.Render("╭" + strings.Repeat("─", inner+2) + "╮")}
	for _, l := range lines {
		l = ansi.Truncate(l, inner, "…")
		out = append(out, bs.Render("│")+" "+l+strings.Repeat(" ", max(inner-ansi.StringWidth(l), 0))+" "+bs.Render("│"))
	}
	return append(out, bs.Render("╰"+strings.Repeat("─", inner+2)+"╯"))
}

// cmdPalRow draws one command row exactly inner cells wide.
func cmdPalRow(c wsCommand, q string, sel, bare bool, inner, nameW, argsW int, args string) string {
	gc := ui.GroupColor(c.group)
	bg := func(st lipgloss.Style) lipgloss.Style {
		if sel {
			return st.Background(ui.Sel)
		}
		return st
	}
	marker := bg(lipgloss.NewStyle()).Render("  ")
	if sel {
		marker = bg(lipgloss.NewStyle().Foreground(gc).Bold(true)).Render("› ")
	}
	_, pos, _ := cmdMatchPositions(cmdKey(c.name), q)
	hit := map[int]bool{}
	for _, p := range pos {
		hit[p+1] = true // the slash is rune 0
	}
	var name strings.Builder
	nr := []rune(c.name)
	if len(nr) > nameW {
		nr = append(nr[:max(nameW-1, 0)], '…')
	}
	for i, r := range nr {
		st := bg(lipgloss.NewStyle().Foreground(ui.Text).Bold(sel))
		if hit[i] {
			st = st.Foreground(gc).Underline(true).Bold(true)
		}
		name.WriteString(st.Render(string(r)))
	}
	nw := len(nr)
	dim := bg(lipgloss.NewStyle().Foreground(ui.Muted))
	gap := dim.Render(strings.Repeat(" ", max(nameW-nw, 0)+2))

	left := marker + name.String() + gap
	if argsW > 0 {
		args = ansi.Truncate(args, argsW, "…")
		left += dim.Render(args + strings.Repeat(" ", argsW-ansi.StringWidth(args)+2))
	}
	left += dim.Render(c.desc)
	if c.tag != "" {
		left += dim.Render(" " + c.tag)
	}
	tag := ""
	if !bare {
		tag = groupTag(c.group)
	}
	room := inner
	if tag != "" {
		room -= ansi.StringWidth(tag) + 2
	}
	left = ansi.Truncate(left, max(room, 1), "…")
	fill := inner - ansi.StringWidth(left)
	if tag != "" {
		fill -= ansi.StringWidth(tag)
	}
	out := left + bg(lipgloss.NewStyle()).Render(strings.Repeat(" ", max(fill, 0)))
	if tag != "" {
		out += bg(lipgloss.NewStyle().Foreground(ui.Muted)).Render(tag)
	}
	return out
}
