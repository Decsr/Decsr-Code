package tui

import (
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type chartSegment struct {
	value int
	color lipgloss.Color
}

type chartColumn struct {
	value int
	color lipgloss.Color
	label string
}

var eighthBlocks = []rune("▁▂▃▄▅▆▇█")

// stackedBar renders one line of exactly width cells, split proportionally
// between segments. Any nonzero segment gets at least one cell; rounding drift
// is absorbed by the largest segment.
func stackedBar(segs []chartSegment, width int) string {
	if width <= 0 {
		return ""
	}
	total := 0
	for _, s := range segs {
		if s.value > 0 {
			total += s.value
		}
	}
	if total == 0 {
		return lipgloss.NewStyle().Foreground(crt.muted).Render(strings.Repeat("░", width))
	}
	cells := make([]int, len(segs))
	nonzero := 0
	for _, s := range segs {
		if s.value > 0 {
			nonzero++
		}
	}
	if nonzero > width {
		// Minimums cannot all fit: keep the largest segments, one cell each.
		idx := make([]int, 0, nonzero)
		for i, s := range segs {
			if s.value > 0 {
				idx = append(idx, i)
			}
		}
		sort.SliceStable(idx, func(a, b int) bool { return segs[idx[a]].value > segs[idx[b]].value })
		for _, i := range idx[:width] {
			cells[i] = 1
		}
		return renderCells(segs, cells)
	}
	sum, largest := 0, -1
	for i, s := range segs {
		if s.value <= 0 {
			continue
		}
		n := int(math.Round(float64(s.value) / float64(total) * float64(width)))
		if n < 1 {
			n = 1
		}
		cells[i] = n
		sum += n
		if largest < 0 || s.value > segs[largest].value {
			largest = i
		}
	}
	cells[largest] += width - sum
	if cells[largest] < 1 {
		cells[largest] = 1
	}
	return renderCells(segs, cells)
}

func renderCells(segs []chartSegment, cells []int) string {
	var b strings.Builder
	for i, s := range segs {
		if cells[i] > 0 {
			b.WriteString(lipgloss.NewStyle().Foreground(s.color).Render(strings.Repeat("█", cells[i])))
		}
	}
	return b.String()
}

// sparkline renders the last width values as eighth-blocks scaled to their max.
func sparkline(values []int, width int, color lipgloss.Color) string {
	if width <= 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	maxV := 0
	for _, v := range values {
		if v > maxV {
			maxV = v
		}
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-len(values)))
	var bars strings.Builder
	for _, v := range values {
		if v <= 0 || maxV == 0 {
			bars.WriteRune(' ')
			continue
		}
		lvl := int(math.Round(float64(v) / float64(maxV) * 8))
		if lvl < 1 {
			lvl = 1
		}
		bars.WriteRune(eighthBlocks[lvl-1])
	}
	b.WriteString(lipgloss.NewStyle().Foreground(color).Render(bars.String()))
	return b.String()
}

// columnChart renders height rows of vertical bars (bar cell plus gap cell per
// column) and a baseline row, each exactly width cells. It shows the most
// recent width/2 columns and returns how many earlier ones were hidden.
func columnChart(cols []chartColumn, width, height int) (lines []string, hidden int) {
	if width <= 0 || height <= 0 {
		return nil, 0
	}
	maxCols := width / 2
	if len(cols) > maxCols {
		hidden = len(cols) - maxCols
		cols = cols[hidden:]
	}
	maxV := 0
	for _, c := range cols {
		if c.value > maxV {
			maxV = c.value
		}
	}
	steps := make([]int, len(cols))
	for i, c := range cols {
		if c.value <= 0 || maxV == 0 {
			continue
		}
		e := int(math.Round(float64(c.value) / float64(maxV) * float64(height*8)))
		if e < 1 {
			e = 1
		}
		steps[i] = e
	}
	pad := strings.Repeat(" ", width-2*maxCols+2*(maxCols-len(cols)))
	for row := height - 1; row >= 0; row-- {
		var b strings.Builder
		for i, c := range cols {
			rem := steps[i] - row*8
			switch {
			case rem >= 8:
				b.WriteString(lipgloss.NewStyle().Foreground(c.color).Render("█"))
			case rem > 0:
				b.WriteString(lipgloss.NewStyle().Foreground(c.color).Render(string(eighthBlocks[rem-1])))
			default:
				b.WriteString(" ")
			}
			b.WriteString(" ")
		}
		b.WriteString(pad)
		lines = append(lines, b.String())
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.border).Render(strings.Repeat("─", width)))
	return lines, hidden
}
