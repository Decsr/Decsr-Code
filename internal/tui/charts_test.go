package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestStackedBarWidthAndMinimum(t *testing.T) {
	a, b, c := lipgloss.Color("#4a86ff"), lipgloss.Color("#5ce1e6"), lipgloss.Color("#ffd166")
	got := ansi.Strip(stackedBar([]chartSegment{{900, a}, {100, b}, {1, c}}, 20))
	if got != strings.Repeat("█", 20) {
		t.Fatalf("want 20 full blocks, got %q", got)
	}
	got = ansi.Strip(stackedBar([]chartSegment{{1, c}}, 20))
	if got != strings.Repeat("█", 20) {
		t.Fatalf("single segment: got %q", got)
	}
	forceTrueColor(t)
	raw := stackedBar([]chartSegment{{1000000, a}, {1, c}}, 20)
	if !strings.Contains(raw, lipgloss.NewStyle().Foreground(c).Render("█")) {
		t.Fatalf("tiny segment missing from %q", raw)
	}
	if w := ansi.StringWidth(raw); w != 20 {
		t.Fatalf("width %d", w)
	}
}

func TestStackedBarMoreSegmentsThanWidth(t *testing.T) {
	c := lipgloss.Color("#fff")
	got := ansi.StringWidth(stackedBar([]chartSegment{{1, c}, {1, c}, {1, c}}, 2))
	if got != 2 {
		t.Fatalf("width %d, want 2", got)
	}
	segs := []chartSegment{{100, c}, {1, c}, {1, c}, {1, c}, {1, c}, {1, c}, {1, c}}
	if got := ansi.StringWidth(stackedBar(segs, 5)); got != 5 {
		t.Fatalf("width %d, want 5", got)
	}
}

func TestStackedBarEmpty(t *testing.T) {
	if got := ansi.Strip(stackedBar(nil, 10)); got != strings.Repeat("░", 10) {
		t.Fatalf("got %q", got)
	}
	if got := ansi.Strip(stackedBar([]chartSegment{{0, "#fff"}}, 10)); got != strings.Repeat("░", 10) {
		t.Fatalf("zero: got %q", got)
	}
}

func TestSparklineScales(t *testing.T) {
	if got := ansi.Strip(sparkline([]int{0, 1, 8}, 5, "#fff")); got != "   ▁█" {
		t.Fatalf("got %q", got)
	}
}

func TestSparklineTrimsToWidth(t *testing.T) {
	vals := make([]int, 30)
	for i := range vals {
		vals[i] = i + 1
	}
	if n := len([]rune(ansi.Strip(sparkline(vals, 10, "#fff")))); n != 10 {
		t.Fatalf("got %d runes", n)
	}
}

func TestColumnChartHiddenAndMinimum(t *testing.T) {
	cols := make([]chartColumn, 30)
	for i := range cols {
		cols[i] = chartColumn{value: 300, color: "#fff"}
	}
	cols[0].value = 120000
	lines, hidden := columnChart(cols, 20, 4)
	if hidden != 20 {
		t.Fatalf("hidden %d", hidden)
	}
	if len(lines) != 5 {
		t.Fatalf("lines %d", len(lines))
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w != 20 {
			t.Fatalf("width %d for %q", w, l)
		}
	}
	// recent 10 columns are visible; make the first visible one tall too.
	cols[20].value = 120000
	lines, _ = columnChart(cols, 20, 4)
	bottom := []rune(ansi.Strip(lines[3]))
	if bottom[0] != '█' || bottom[2] != '▁' {
		t.Fatalf("bottom row %q", string(bottom))
	}
}

func TestColumnChartEmpty(t *testing.T) {
	lines, hidden := columnChart(nil, 20, 4)
	if len(lines) != 5 || hidden != 0 {
		t.Fatalf("lines %d hidden %d", len(lines), hidden)
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w != 20 {
			t.Fatalf("width %d", w)
		}
	}
}

func TestColumnChartOddWidth(t *testing.T) {
	lines, _ := columnChart([]chartColumn{{5, "#fff", ""}}, 7, 3)
	for _, l := range lines {
		if w := ansi.StringWidth(l); w != 7 {
			t.Fatalf("width %d", w)
		}
	}
}
