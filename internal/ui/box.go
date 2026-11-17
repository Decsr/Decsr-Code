package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Box draws body lines in a square box of exactly width cells with the title
// embedded in the top border: "┌─ ◆ TITLE ───┐". The border is Border, or
// Accent when focused; the title is Accent. Body lines get one cell of padding
// on each side and are cut or padded to width-4 cells. Below 8 cells the box
// is empty.
func Box(title string, body []string, width int, focused bool) string {
	if width < 8 {
		return ""
	}
	bc := Border
	if focused {
		bc = Accent
	}
	bs := fg(bc)
	label := " ◆ " + Truncate(title, width-7) + " "
	fill := max(width-3-ansi.StringWidth(label), 0)
	out := []string{bs.Render("┌─") + fg(Accent).Bold(true).Render(label) + bs.Render(strings.Repeat("─", fill)+"┐")}
	side := bs.Render("│")
	for _, l := range body {
		out = append(out, side+" "+fit(l, width-4)+" "+side)
	}
	out = append(out, bs.Render("└"+strings.Repeat("─", width-2)+"┘"))
	return strings.Join(out, "\n")
}
