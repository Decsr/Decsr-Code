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
	cmdPalMinW    = 40 // narrowest box content
	cmdPalNameW   = 16 // widest name column before the rest is pushed right
	cmdPalArgsW   = 18 // widest args column before a hint is cut with …
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

// renderCmdPalette draws the "/" palette as a rounded box: grouped under
// headers while the query is bare, ranked with a group tag at the right while
// typing. It shows at most cmdPalMaxRows commands, and at most maxRows lines
// in all, keeps idx visible and ends with "↓ N more · ↑↓ Tab" when rows are
// hidden. width is the room the box may use.
func renderCmdPalette(cmds []wsCommand, typed string, idx, width, maxRows int) []string {
	if len(cmds) == 0 || maxRows < 3 || width < cmdPalMinW/2 {
		return nil
	}
	q := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(typed)), "/")
	bare := q == ""
	idx = min(max(idx, 0), len(cmds)-1)

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

	// The window: cmdPalMaxRows rows (headers count as rows when bare), cut
	// to the lines the screen leaves, one of them kept for the "more" line.
	vis := min(cmdPalMaxRows, maxRows-2)
	selLine := 0
	for i, e := range entries {
		if e.cmd == idx {
			selLine = i
		}
	}
	more := len(entries) > vis
	if more {
		vis = max(min(cmdPalMaxRows, maxRows-3), 1)
	}
	start := 0
	if selLine >= vis {
		start = selLine - vis + 1
	}
	if bare && start > 0 && entries[start].cmd >= 0 && start-1 >= 0 && entries[start-1].cmd < 0 {
		start-- // keep the group header of the first visible row
	}
	end := min(start+vis, len(entries))
	for start > 0 && end-start < vis { // nothing left below: show a full page
		start--
	}

	maxName := 0
	for _, c := range cmds {
		maxName = max(maxName, ansi.StringWidth(c.name))
	}
	nameW := min(maxName, cmdPalNameW)
	argsOf := func(c wsCommand) string {
		a := c.args
		if c.hint != "" {
			a = c.hint
		}
		return a
	}
	// The args column is as wide as the longest hint among the shown rows,
	// capped; no shown row with args means no column.
	argsW := 0
	for _, e := range entries[start:end] {
		if e.cmd >= 0 {
			argsW = max(argsW, ansi.StringWidth(argsOf(cmds[e.cmd])))
		}
	}
	argsW = min(argsW, cmdPalArgsW)
	// Natural width of the content: marker, name, args, description, tag.
	natural := cmdPalMinW
	for _, e := range entries[start:end] {
		if e.cmd < 0 {
			continue
		}
		c := cmds[e.cmd]
		w := 2 + max(nameW, ansi.StringWidth(c.name)) + 2 + ansi.StringWidth(c.desc)
		if argsW > 0 {
			w += argsW + 2
		}
		if !bare {
			w += 2 + ansi.StringWidth(groupTag(c.group))
		}
		natural = max(natural, w)
	}
	inner := min(natural, width-4)
	if inner < 20 {
		return nil
	}

	var lines []string
	for _, e := range entries[start:end] {
		if e.cmd < 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(ui.GroupColor(e.header)).Bold(true).Render(strings.ToUpper(e.header)))
			continue
		}
		lines = append(lines, cmdPalRow(cmds[e.cmd], q, e.cmd == idx, bare, inner, nameW, argsW, argsOf(cmds[e.cmd])))
	}
	if more {
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
		note := fmt.Sprintf("↓ %d more · ↑↓ Tab", below)
		if below == 0 {
			note = fmt.Sprintf("↑ %d above · ↑↓ Tab", above)
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(ui.Muted).Render(note))
	}

	bc := ui.Border
	if c := cmds[idx]; c.group != "" {
		bc = ui.GroupColor(c.group)
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
	for i, r := range []rune(c.name) {
		st := bg(lipgloss.NewStyle().Foreground(ui.Text).Bold(sel))
		if hit[i] {
			st = st.Foreground(gc).Underline(true).Bold(true)
		}
		name.WriteString(st.Render(string(r)))
	}
	nw := ansi.StringWidth(c.name)
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
