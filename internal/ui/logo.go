package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Tagline is the line under the logo.
const Tagline = "AI-powered multi-agent coding pipeline"

// LogoWidth is the width of Logo in cells.
const LogoWidth = 24

// brandStops are the colours of the logo gradient, left to right: the colours
// of the command groups.
var brandStops = []lipgloss.AdaptiveColor{GroupSession, GroupRun, GroupAgents, GroupUsage}

// BrandColor is the colour of the brand glyph: the first stop of the logo
// gradient.
var BrandColor = brandStops[0]

// Dark pins an adaptive colour to its dark variant, for screens that paint
// their own dark background whatever the theme is. Other colours are returned
// unchanged.
func Dark(c lipgloss.TerminalColor) lipgloss.TerminalColor {
	if ac, ok := c.(lipgloss.AdaptiveColor); ok {
		return lipgloss.Color(ac.Dark)
	}
	return c
}

// logoGlyphs are the letters of DECSR, 4 pixels wide and 5 tall ('#' filled).
var logoGlyphs = map[rune][5]string{
	'D': {"###.", "#..#", "#..#", "#..#", "###."},
	'E': {"####", "#...", "###.", "#...", "####"},
	'C': {".###", "#...", "#...", "#...", ".###"},
	'S': {"####", "#...", ".##.", "...#", "####"},
	'R': {"###.", "#..#", "###.", "#.#.", "#..#"},
}

// Logo draws DECSR in three rows of █ ▀ ▄ with a horizontal gradient over the
// columns: cyan, violet, pink, amber. With pinDark the dark variants are used
// whatever the theme (Home paints its own dark background).
func Logo(pinDark bool) []string {
	var px [5]string
	for i, ch := range "DECSR" {
		for r := range px {
			if i > 0 {
				px[r] += "."
			}
			px[r] += logoGlyphs[ch][r][:]
		}
	}
	rows := make([]strings.Builder, 3)
	for x := 0; x < len(px[0]); x++ {
		t := float64(x) / float64(len(px[0])-1) * float64(len(brandStops)-1)
		i := min(int(t), len(brandStops)-2)
		var c lipgloss.TerminalColor = lerpColor(brandStops[i], brandStops[i+1], t-float64(i))
		if pinDark {
			c = Dark(c)
		}
		st := lipgloss.NewStyle().Foreground(c)
		for r, pair := range [3][2]int{{0, 1}, {2, 3}, {4, -1}} {
			top := px[pair[0]][x] == '#'
			bot := pair[1] >= 0 && px[pair[1]][x] == '#'
			ch := " "
			switch {
			case top && bot:
				ch = "█"
			case top:
				ch = "▀"
			case bot:
				ch = "▄"
			}
			if ch == " " {
				rows[r].WriteString(ch)
			} else {
				rows[r].WriteString(st.Render(ch))
			}
		}
	}
	out := make([]string, 3)
	for i := range rows {
		out[i] = rows[i].String()
	}
	return out
}
