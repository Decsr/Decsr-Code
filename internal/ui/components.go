package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func fg(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Header is the top line of a screen: the brand glyph in Accent, left in Text
// and right in Muted at the right edge. The left side is truncated first.
func Header(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	rw := ansi.StringWidth(right)
	if rw >= width {
		return fg(Muted).Render(Truncate(right, width))
	}
	room := width - rw - 1
	brand := "◆ "
	if room < ansi.StringWidth(brand) {
		return pad("", width-rw) + fg(Muted).Render(right)
	}
	l := Truncate(left, room-2)
	used := 2 + ansi.StringWidth(l)
	out := fg(Accent).Render("◆") + " " + fg(Text).Render(l)
	return out + strings.Repeat(" ", max(width-used-rw, 0)) + fg(Muted).Render(right)
}

// PaneLabel names a pane: dim, or normal weight bold when the pane has focus.
func PaneLabel(text string, focused bool, width int) string {
	st := fg(Muted)
	if focused {
		st = fg(Text).Bold(true)
	}
	if width > 0 {
		text = Truncate(text, max(width-2, 0))
	}
	return "  " + st.Render(text)
}

// VDivider is a vertical line of height rows in Border.
func VDivider(height int) string {
	if height <= 0 {
		return ""
	}
	line := fg(Border).Render("│")
	return strings.TrimSuffix(strings.Repeat(line+"\n", height), "\n")
}

// Column is one column of JoinColumns: Body is cut or padded to Width.
type Column struct {
	Width int
	Body  string
}

// Gutter is the padding on each side of the divider between two columns.
const Gutter = 1

// JoinColumns places columns side by side, separated by a single "│" in
// Border, and never exceeds width. Column.Width includes the gutter: one
// space on each side of the divider, so text never touches it (a body should
// be built Gutter cells narrower per inner side). A column that does not fit
// is narrowed or dropped. Every row has the same width.
func JoinColumns(width int, cols ...Column) string {
	if width <= 0 || len(cols) == 0 {
		return ""
	}
	var widths []int
	var bodies [][]string
	used, height := 0, 0
	for i, c := range cols {
		sep := 0
		if i > 0 {
			sep = 1
		}
		w := min(max(c.Width, 0), width-used-sep)
		if w <= 0 && i > 0 {
			break
		}
		widths = append(widths, w)
		lines := strings.Split(c.Body, "\n")
		bodies = append(bodies, lines)
		height = max(height, len(lines))
		used += sep + w
	}
	div := fg(Border).Render("│")
	rows := make([]string, height)
	for r := range rows {
		var b strings.Builder
		for i, w := range widths {
			if i > 0 {
				b.WriteString(div)
			}
			cell := ""
			if r < len(bodies[i]) {
				cell = bodies[i][r]
			}
			lp, rp := 0, 0
			if i > 0 {
				lp = Gutter
			}
			if i < len(widths)-1 {
				rp = Gutter
			}
			inner := max(w-lp-rp, 0)
			b.WriteString(strings.Repeat(" ", min(lp, w)) + fit(cell, inner) + strings.Repeat(" ", min(rp, max(w-lp, 0))))
		}
		if extra := width - used; extra > 0 {
			b.WriteString(strings.Repeat(" ", extra))
		}
		rows[r] = b.String()
	}
	return strings.Join(rows, "\n")
}

// CardOpts describes a Card.
type CardOpts struct {
	Title, Meta      string
	Body             []string
	Width            int
	Selected, Failed bool
}

// Card is a rounded box: the title in bold with Meta dim at the right on the
// first line, then Body. The border is Accent when selected, Fail when failed
// and Border otherwise; a selected card is filled with Sel. Width includes the
// border; below 5 cells the card is empty.
func Card(o CardOpts) string {
	if o.Width < 5 {
		return ""
	}
	inner := o.Width - 4
	border := Border
	switch {
	case o.Selected:
		border = Accent
	case o.Failed:
		border = Fail
	}
	bs := fg(border)
	cell := lipgloss.NewStyle()
	if o.Selected {
		cell = cell.Background(Sel)
		bs = bs.Background(Sel)
	}
	title := Truncate(o.Title, inner)
	meta := o.Meta
	if ansi.StringWidth(meta)+1 >= inner {
		meta = ""
	}
	room := inner - ansi.StringWidth(meta)
	if meta != "" {
		room--
	}
	title = Truncate(title, room)
	first := cell.Foreground(Text).Bold(true).Render(title)
	gap := inner - ansi.StringWidth(title) - ansi.StringWidth(meta)
	first += cell.Render(strings.Repeat(" ", max(gap, 0))) + cell.Foreground(Muted).Render(meta)

	var out []string
	out = append(out, bs.Render("╭"+strings.Repeat("─", o.Width-2)+"╮"))
	row := func(content string) string {
		return bs.Render("│") + cell.Render(" ") + content + cell.Render(" ") + bs.Render("│")
	}
	out = append(out, row(first))
	for _, l := range o.Body {
		t := Truncate(l, inner)
		out = append(out, row(cell.Foreground(Text).Render(t)+cell.Render(strings.Repeat(" ", inner-ansi.StringWidth(t)))))
	}
	out = append(out, bs.Render("╰"+strings.Repeat("─", o.Width-2)+"╯"))
	return strings.Join(out, "\n")
}

// RowOpts describes a ListRow.
type RowOpts struct {
	Left, Right string
	Width       int
	Selected    bool
	Indent      int // spaces before Left
}

// ListRow is one row of a list, exactly Width cells wide. A selected row gets
// the Sel background across the whole width and a "▌" cursor in Accent.
func ListRow(o RowOpts) string {
	if o.Width <= 0 {
		return ""
	}
	st := lipgloss.NewStyle()
	if o.Selected {
		st = st.Background(Sel)
	}
	cursor := st.Render(" ")
	if o.Selected {
		cursor = st.Foreground(Accent).Render("▌")
	}
	rest := o.Width - 1
	if rest <= 0 {
		return cursor
	}
	indent := min(max(o.Indent, 0), rest)
	rest -= indent
	right := Truncate(o.Right, rest)
	rw := ansi.StringWidth(right)
	room := rest - rw
	if rw > 0 {
		room--
	}
	left := Truncate(o.Left, max(room, 0))
	gap := rest - ansi.StringWidth(left) - rw
	return cursor + st.Render(strings.Repeat(" ", indent)) +
		st.Foreground(Text).Render(left) +
		st.Render(strings.Repeat(" ", max(gap, 0))) +
		st.Foreground(Muted).Render(right)
}

// Hint is one "key label" pair of a HintBar.
type Hint struct{ Key, Label string }

// HintBar renders "key label · key label" in at most width cells: the key in
// Text, the label in Muted. Trailing hints are dropped until the bar fits.
func HintBar(hints []Hint, width int) string {
	if width <= 0 {
		return ""
	}
	dim := fg(Muted)
	build := func(hs []Hint) string {
		parts := make([]string, len(hs))
		for i, h := range hs {
			parts[i] = fg(Text).Render(h.Key) + dim.Render(" "+h.Label)
		}
		return "  " + strings.Join(parts, dim.Render(" · "))
	}
	for n := len(hints); n > 1; n-- {
		if s := build(hints[:n]); ansi.StringWidth(s) <= width {
			return s
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return ansi.Truncate(build(hints[:1]), width, "")
}

// InputBox wraps the view of a text input in a rounded box of width cells. An
// empty view shows the placeholder dim. The border is Accent when focused.
// chips, when set, is a dim row under the input.
func InputBox(view, placeholder string, width int, focused bool, chips string) string {
	if width < 5 {
		return ""
	}
	inner := width - 4
	bc := Border
	if focused {
		bc = Accent
	}
	bs := fg(bc)
	var lines []string
	if strings.TrimSpace(ansi.Strip(view)) == "" && placeholder != "" {
		lines = []string{fg(Muted).Render(Truncate(placeholder, inner))}
	} else {
		lines = strings.Split(view, "\n")
	}
	if chips != "" {
		lines = append(lines, fg(Muted).Render(Truncate(ansi.Strip(chips), inner)))
	}
	out := []string{bs.Render("╭" + strings.Repeat("─", width-2) + "╮")}
	for _, l := range lines {
		out = append(out, bs.Render("│")+" "+fit(l, inner)+" "+bs.Render("│"))
	}
	out = append(out, bs.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return strings.Join(out, "\n")
}

// Overlay draws a titled box of width cells around body lines: a rounded
// Accent border with the title in its top edge and, when hints are given, a
// hint bar as the last row. Lines are cut to fit.
func Overlay(title string, body []string, hints []Hint, width int) string {
	if width < 6 {
		return ""
	}
	inner := width - 4
	lines := make([]string, 0, len(body)+2)
	for _, l := range body {
		lines = append(lines, fit(l, inner))
	}
	if len(hints) > 0 {
		lines = append(lines, "", fit(strings.TrimPrefix(HintBar(hints, inner+2), "  "), inner))
	}
	st := lipgloss.NewStyle().Padding(0, 1).Width(width - 2)
	box := st.Border(lipgloss.RoundedBorder(), false, true, true, true).BorderForeground(Accent).Render(strings.Join(lines, "\n"))
	bs := fg(Accent)
	label := " " + Truncate(title, max(width-6, 1)) + " "
	fill := max(width-3-ansi.StringWidth(label), 0)
	top := bs.Render("╭─") + fg(Text).Bold(true).Render(label) + bs.Render(strings.Repeat("─", fill)+"╮")
	return top + "\n" + box
}

// PlaceOverlay draws overlay (multi-line) centred over base in a w x h
// screen, leaving the first and last line of base alone when it fits between
// them. The rows it covers are blanked on both sides.
func PlaceOverlay(base, overlay string, w, h int) string {
	bl := strings.Split(overlay, "\n")
	bw := 0
	for _, l := range bl {
		bw = max(bw, ansi.StringWidth(l))
	}
	x := max((w-bw)/2, 0)
	y := max((h-len(bl))/2, 1)
	if y+len(bl) > h-1 {
		y = max(h-1-len(bl), 0)
	}
	return PlaceAt(base, overlay, x, y, w, h)
}

// PlaceAt draws overlay over base with its top-left corner at (x, y) in a
// w x h screen.
func PlaceAt(base, overlay string, x, y, w, h int) string {
	lines := strings.Split(base, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	bl := strings.Split(overlay, "\n")
	bw := 0
	for _, l := range bl {
		bw = max(bw, ansi.StringWidth(l))
	}
	for i, l := range bl {
		if y+i >= len(lines) {
			break
		}
		lines[y+i] = strings.Repeat(" ", x) + pad(l, bw) + "\x1b[0m" + strings.Repeat(" ", max(w-x-bw, 0))
	}
	return strings.Join(lines, "\n")
}

// Status gives the glyph, word and colour of a status kind: todo, ready,
// blocked, running, done, failed, waiting, limited, unassigned or stalled. An
// unknown kind is a dim dot with the kind as its word. Status is always shown
// as glyph and word, never by colour alone.
func Status(kind string) (glyph, word string, color lipgloss.TerminalColor) {
	switch kind {
	case "done":
		return "✓", "done", OK
	case "failed":
		return "!", "failed", Fail
	case "waiting":
		return "◇", "waiting", Warn
	case "limited":
		return "◷", "waiting", Warn
	case "running":
		return "▖", "running", Accent
	case "unassigned":
		return "?", "unassigned", Warn
	case "blocked":
		return "⏸", "blocked", Muted
	case "ready":
		return "○", "ready", Text
	case "todo":
		return "○", "todo", Muted
	case "stalled":
		return "◌", "stalled", Warn
	}
	return "·", kind, Muted
}

// Chip is a small dim tag: "[text]".
func Chip(text string) string {
	return fg(Muted).Render("[" + text + "]")
}
