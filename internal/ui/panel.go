package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// Margin is the empty space a Panel keeps from every screen edge.
const Margin = 2

// NoColor reports whether colour output is off (NO_COLOR, or a terminal
// without colour). Glyphs and brackets then carry what colour would.
func NoColor() bool { return lipgloss.ColorProfile() == termenv.Ascii }

// PanelOpts describes a Panel.
type PanelOpts struct {
	Title            string
	Color            lipgloss.TerminalColor // border, title and header colour: the group colour
	Tabs             []string
	Active           int      // index of the active tab
	Filter           string   // the filter line shows when this is not empty
	Body             []string // content rows, already styled
	Scroll           int      // first visible body row
	Keys             []Hint   // footer, drawn in the bottom border
	Width            int      // preferred width including the border; 0 = 100
	ScreenW, ScreenH int
}

// Panel draws a centred overlay: a rounded border in the group colour, the
// title in the top border, a tab bar whose active tab is a filled pill, a
// filter line when Filter is set, the body window and the keys in the bottom
// border. It keeps Margin cells from the screen edges, is never wider than
// the screen and shows only the rows that fit; Scroll picks the first one
// (PanelWindow clamps it). Place the result with PlaceOverlay.
func Panel(o PanelOpts) string {
	if o.Color == nil {
		o.Color = Accent
	}
	pw := o.Width
	if pw <= 0 {
		pw = 100
	}
	if o.ScreenW > 0 {
		pw = min(pw, o.ScreenW-2*Margin)
	}
	if pw < 12 {
		return ""
	}
	inner := pw - 4
	var head []string
	if len(o.Tabs) > 0 {
		head = append(head, tabBar(o.Tabs, o.Active, o.Color))
	}
	if o.Filter != "" {
		head = append(head, fg(Muted).Render("/ ")+fg(Text).Render(o.Filter)+fg(Accent).Render("▏"))
	}
	rows := PanelRows(o)
	first := PanelWindow(o.Scroll, len(o.Body), rows)
	body := o.Body[first:min(first+rows, len(o.Body))]

	bs := fg(o.Color)
	var out []string
	out = append(out, panelTop(o, pw, first, rows, len(o.Body)))
	row := func(s string) string { return bs.Render("│") + " " + fit(s, inner) + " " + bs.Render("│") }
	for _, l := range head {
		out = append(out, row(l))
	}
	for _, l := range body {
		out = append(out, row(l))
	}
	out = append(out, panelBottom(o, pw))
	return strings.Join(out, "\n")
}

// PanelRows is the number of body rows a Panel shows: all of them, or as many
// as the screen leaves under the border, the tab bar and the filter line.
func PanelRows(o PanelOpts) int {
	head := 0
	if len(o.Tabs) > 0 {
		head++
	}
	if o.Filter != "" {
		head++
	}
	rows := len(o.Body)
	if o.ScreenH > 0 {
		rows = min(rows, max(o.ScreenH-2*Margin-2-head, 1))
	}
	return rows
}

// PanelWindow clamps scroll so that rows body rows starting there stay inside
// total rows.
func PanelWindow(scroll, total, rows int) int {
	return min(max(scroll, 0), max(total-rows, 0))
}

func tabBar(tabs []string, active int, color lipgloss.TerminalColor) string {
	var parts []string
	for i, t := range tabs {
		switch {
		case i == active && NoColor():
			parts = append(parts, "["+t+"]")
		case i == active:
			parts = append(parts, lipgloss.NewStyle().Background(color).Foreground(PanelInk).Bold(true).Render(" "+t+" "))
		default:
			parts = append(parts, fg(Muted).Render(" "+t+" "))
		}
	}
	return strings.Join(parts, " ")
}

func panelTop(o PanelOpts, pw, first, rows, total int) string {
	bs := fg(o.Color)
	label := " " + Truncate(o.Title, max(pw-8, 1)) + " "
	right := ""
	if total > rows {
		right = fmt.Sprintf(" %d-%d of %d ", first+1, min(first+rows, total), total)
	}
	fill := pw - 3 - ansi.StringWidth(label) - ansi.StringWidth(right)
	if fill < 0 {
		right, fill = "", max(pw-3-ansi.StringWidth(label), 0)
	}
	return bs.Render("╭─") + lipgloss.NewStyle().Foreground(o.Color).Bold(true).Render(label) +
		bs.Render(strings.Repeat("─", fill)) + fg(Muted).Render(right) + bs.Render("╮")
}

func panelBottom(o PanelOpts, pw int) string {
	bs := fg(o.Color)
	if len(o.Keys) == 0 {
		return bs.Render("╰" + strings.Repeat("─", pw-2) + "╯")
	}
	bar := strings.TrimPrefix(HintBar(o.Keys, pw-6), "  ")
	label := " " + bar + " "
	fill := max(pw-3-ansi.StringWidth(label), 0)
	return bs.Render("╰─") + label + bs.Render(strings.Repeat("─", fill)+"╯")
}

// PanelState is the cursor of a Panel: the active tab, the scroll position and
// the filter text. The zero value is ready to use.
type PanelState struct {
	Tab, Scroll int
	Filter      string
}

// Key handles the keys every panel shares and reports whether it used the key:
// Tab and Shift+Tab (also Right and Left) switch tabs, Up, Down, PgUp and PgDn
// scroll. tabs is the number of tabs; a panel without tabs ignores the
// switching keys. Scrolling is clamped when the panel is drawn, through
// PanelWindow.
func (s *PanelState) Key(key string, tabs int) bool {
	switch key {
	case "tab", "right":
		if tabs == 0 {
			return false
		}
		s.Tab, s.Scroll = (s.Tab+1)%tabs, 0
	case "shift+tab", "left":
		if tabs == 0 {
			return false
		}
		s.Tab, s.Scroll = (s.Tab+tabs-1)%tabs, 0
	case "up", "k":
		s.Scroll = max(s.Scroll-1, 0)
	case "down", "j":
		s.Scroll++
	case "pgup":
		s.Scroll = max(s.Scroll-10, 0)
	case "pgdown":
		s.Scroll += 10
	default:
		return false
	}
	return true
}

// TypeFilter appends typed text to the filter and returns to the first row.
func (s *PanelState) TypeFilter(text string) {
	s.Filter += text
	s.Scroll = 0
}

// BackspaceFilter removes the last typed rune of the filter.
func (s *PanelState) BackspaceFilter() {
	if r := []rune(s.Filter); len(r) > 0 {
		s.Filter = string(r[:len(r)-1])
	}
	s.Scroll = 0
}

// ── Table ────────────────────────────────────────────────────────────────────

// Align is the alignment of a table column.
type Align int

// Column alignments.
const (
	Left Align = iota
	Right
)

// Col is one column of a Table. Cells may carry styling; Max caps the width
// (0 = no cap) and the widest left-aligned column gives way when the table is
// wider than the space it has.
type Col struct {
	Title string
	Align Align
	Max   int
}

// TableOpts describes a Table.
type TableOpts struct {
	Cols   []Col
	Rows   [][]string
	Header lipgloss.TerminalColor // colour of the header row; nil = Muted
	Width  int                    // total width, 0 = natural
}

// Table lays rows out in aligned columns under a header row in the group
// colour; columns are two cells apart. Every line is at most Width cells.
func Table(o TableOpts) []string {
	n := len(o.Cols)
	if n == 0 {
		return nil
	}
	w := make([]int, n)
	for i, c := range o.Cols {
		w[i] = ansi.StringWidth(c.Title)
	}
	for _, r := range o.Rows {
		for i := 0; i < n && i < len(r); i++ {
			w[i] = max(w[i], ansi.StringWidth(r[i]))
		}
	}
	for i, c := range o.Cols {
		if c.Max > 0 {
			w[i] = min(w[i], c.Max)
		}
	}
	if o.Width > 0 {
		for sum(w)+2*(n-1) > o.Width {
			j := -1
			for i, c := range o.Cols {
				if c.Align == Left && w[i] > 4 && (j < 0 || w[i] > w[j]) {
					j = i
				}
			}
			if j < 0 {
				break
			}
			w[j]--
		}
	}
	hc := o.Header
	if hc == nil {
		hc = Muted
	}
	line := func(cells []string, style func(string) string) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			cell = Truncate(cell, w[i])
			gap := strings.Repeat(" ", max(w[i]-ansi.StringWidth(cell), 0))
			if style != nil {
				cell = style(cell)
			}
			if o.Cols[i].Align == Right {
				cell = gap + cell
			} else if i < n-1 {
				cell += gap
			}
			b.WriteString(cell)
			if i < n-1 {
				b.WriteString("  ")
			}
		}
		return b.String()
	}
	titles := make([]string, n)
	for i, c := range o.Cols {
		titles[i] = c.Title
	}
	hs := lipgloss.NewStyle().Foreground(hc).Bold(true)
	out := []string{line(titles, func(s string) string { return hs.Render(s) })}
	for _, r := range o.Rows {
		out = append(out, line(r, nil))
	}
	if o.Width > 0 {
		for i := range out {
			out[i] = ansi.Truncate(out[i], o.Width, "")
		}
	}
	return out
}

func sum(v []int) int {
	t := 0
	for _, x := range v {
		t += x
	}
	return t
}

// Num formats a count with thousands separators: 12,345.
func Num(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// Money formats a cost as $0.42; a negative value means unknown and gives "–".
func Money(v float64) string {
	if v < 0 {
		return "–"
	}
	return fmt.Sprintf("$%.2f", v)
}

// ── Meter, Sparkline, result lines ───────────────────────────────────────────

// Meter draws a bar of width cells filled to frac and the percentage after it:
// "███████░░░ 70%". The colour goes from green through amber to red as frac
// grows; a fraction over 1 shows a full red bar and its real percentage.
func Meter(frac float64, width int) string {
	if width < 1 {
		return ""
	}
	pct := max(int(frac*100+0.5), 0)
	f := min(max(frac, 0), 1)
	full := min(int(f*float64(width)+0.5), width)
	bar := lipgloss.NewStyle().Foreground(MeterColor(f)).Render(strings.Repeat("█", full)) +
		fg(Border).Render(strings.Repeat("░", width-full))
	return bar + fg(Muted).Render(fmt.Sprintf(" %d%%", pct))
}

var sparkGlyphs = []rune("▁▂▃▄▅▆▇█")

// Sparkline draws one glyph per value, from ▁ (zero) to █ (the largest value),
// coloured teal when low and amber when high.
func Sparkline(values []float64) string {
	top := 0.0
	for _, v := range values {
		top = max(top, v)
	}
	var b strings.Builder
	for _, v := range values {
		frac := 0.0
		if top > 0 && v > 0 {
			frac = v / top
		}
		g := sparkGlyphs[min(int(frac*float64(len(sparkGlyphs)-1)+0.5), len(sparkGlyphs)-1)]
		b.WriteString(fg(SparkColor(frac)).Render(string(g)))
	}
	return b.String()
}

// Result kinds of ResultLine.
const (
	ResultOK    = "ok"
	ResultError = "error"
	ResultWarn  = "warn"
	ResultInfo  = "info"
)

// ResultGlyph is the glyph and colour of a result kind: ✓ done, ✗ error, ⚠
// warning, › info (any other kind).
func ResultGlyph(kind string) (string, lipgloss.TerminalColor) {
	switch kind {
	case ResultOK:
		return "✓", OK
	case ResultError:
		return "✗", Fail
	case ResultWarn:
		return "⚠", Warn
	}
	return "›", Muted
}

// ResultLine is one line of a command result: the glyph of kind, then text.
// The text is Text for ok, Fail for an error, Warn for a warning and Muted
// for info.
func ResultLine(kind, text string) string {
	g, c := ResultGlyph(kind)
	tc := c
	if kind == ResultOK {
		tc = Text
	}
	return fg(c).Bold(true).Render(g) + " " + fg(tc).Render(text)
}
