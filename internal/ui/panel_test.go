package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func samplePanel(w, sw, sh int) PanelOpts {
	var body []string
	for i := 0; i < 60; i++ {
		body = append(body, "row "+strings.Repeat("x", i))
	}
	return PanelOpts{Title: "Usage", Color: GroupUsage, Tabs: []string{"Session", "Budget", "History"}, Active: 1,
		Body: body, Keys: []Hint{{"Tab", "tab"}, {"↑↓", "scroll"}, {"Esc", "close"}}, Width: w, ScreenW: sw, ScreenH: sh}
}

func TestPanelFitsTheScreen(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {40, 12}} {
		out := Panel(samplePanel(100, sz[0], sz[1]))
		lines := strings.Split(out, "\n")
		if len(lines) > sz[1]-2*Margin {
			t.Errorf("%v: %d rows exceed the screen minus the margins", sz, len(lines))
		}
		if w := maxWidth(out); w > sz[0]-2*Margin {
			t.Errorf("%v: width %d exceeds the screen minus the margins", sz, w)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) != ansi.StringWidth(lines[0]) {
				t.Fatalf("%v: ragged panel: %q", sz, plain(l))
			}
		}
	}
}

func TestPanelPartsInTheBorder(t *testing.T) {
	p := plain(Panel(samplePanel(80, 120, 40)))
	lines := strings.Split(p, "\n")
	if !strings.HasPrefix(lines[0], "╭─ Usage ") || !strings.HasSuffix(lines[0], "╮") {
		t.Errorf("title not in the top border: %q", lines[0])
	}
	if !strings.Contains(lines[0], " of 60 ") {
		t.Errorf("an overflowing body shows its position: %q", lines[0])
	}
	if !strings.Contains(lines[1], "Session") || !strings.Contains(lines[1], "Budget") {
		t.Errorf("tab bar: %q", lines[1])
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "╰─ ") || !strings.Contains(last, "Esc close") {
		t.Errorf("keys not in the bottom border: %q", last)
	}
}

func TestPanelActiveTabIsAPill(t *testing.T) {
	out := Panel(samplePanel(80, 120, 40))
	if !strings.Contains(strings.Split(out, "\n")[1], "\x1b[") {
		t.Fatal("tab bar has no styling")
	}
	active := lipgloss.NewStyle().Background(GroupUsage).Foreground(PanelInk).Bold(true).Render(" Budget ")
	if !strings.Contains(out, active) {
		t.Error("the active tab is not a filled pill")
	}
	if strings.Contains(out, lipgloss.NewStyle().Background(GroupUsage).Foreground(PanelInk).Bold(true).Render(" Session ")) {
		t.Error("an inactive tab is filled")
	}
}

func TestPanelFilterLine(t *testing.T) {
	o := samplePanel(80, 120, 40)
	if got := plain(Panel(o)); strings.Contains(got, "/ ab") {
		t.Error("filter line without a filter")
	}
	o.Filter = "ab"
	lines := strings.Split(plain(Panel(o)), "\n")
	if !strings.Contains(lines[2], "/ ab") {
		t.Errorf("filter line missing: %q", lines[2])
	}
}

func TestPanelScrollWindow(t *testing.T) {
	if got := PanelWindow(99, 60, 10); got != 50 {
		t.Errorf("PanelWindow clamps to the last page: %d", got)
	}
	if got := PanelWindow(-3, 60, 10); got != 0 {
		t.Errorf("PanelWindow below zero: %d", got)
	}
	if got := PanelWindow(5, 4, 10); got != 0 {
		t.Errorf("a short body does not scroll: %d", got)
	}
	o := samplePanel(80, 120, 40)
	o.Scroll = 7
	if !strings.Contains(plain(Panel(o)), "row xxxxxxx\n") && !strings.Contains(plain(Panel(o)), "row xxxxxxx ") {
		t.Error("scrolled panel does not start at row 7")
	}
}

func TestPanelStateKeys(t *testing.T) {
	var s PanelState
	for _, k := range []string{"tab", "tab", "tab"} {
		if !s.Key(k, 3) {
			t.Fatalf("%s not handled", k)
		}
	}
	if s.Tab != 0 {
		t.Errorf("tab wraps: %d", s.Tab)
	}
	s.Key("shift+tab", 3)
	if s.Tab != 2 {
		t.Errorf("shift+tab wraps back: %d", s.Tab)
	}
	s.Key("left", 3)
	s.Key("right", 3)
	if s.Tab != 2 {
		t.Errorf("left then right: %d", s.Tab)
	}
	s.Key("down", 3)
	s.Key("down", 3)
	s.Key("up", 3)
	if s.Scroll != 1 {
		t.Errorf("scroll: %d", s.Scroll)
	}
	s.Key("tab", 3)
	if s.Scroll != 0 {
		t.Error("switching tabs resets the scroll")
	}
	if s.Key("tab", 0) || s.Key("x", 3) {
		t.Error("no tabs or an unknown key must not be handled")
	}
	s.TypeFilter("ab")
	s.BackspaceFilter()
	if s.Filter != "a" {
		t.Errorf("filter %q", s.Filter)
	}
}

func TestTableAlignsAndFormats(t *testing.T) {
	rows := [][]string{{"coder", "claude", Num(12345), Money(0.42)}, {"qa", "dsh", Num(7), Money(-1)}}
	out := Table(TableOpts{Cols: []Col{{Title: "Role"}, {Title: "Agent"}, {Title: "Tokens", Align: Right}, {Title: "Cost", Align: Right}}, Rows: rows, Header: GroupUsage})
	p := make([]string, len(out))
	for i, l := range out {
		p[i] = plain(l)
	}
	want := []string{
		"Role   Agent   Tokens   Cost",
		"coder  claude  12,345  $0.42",
		"qa     dsh          7      –",
	}
	for i := range want {
		if p[i] != want[i] {
			t.Errorf("row %d:\n got %q\nwant %q", i, p[i], want[i])
		}
	}
	if !strings.Contains(out[0], "\x1b[") {
		t.Error("header has no colour")
	}
}

func TestTableShrinksTheWidestLeftColumn(t *testing.T) {
	out := Table(TableOpts{Cols: []Col{{Title: "Name"}, {Title: "N", Align: Right}},
		Rows: [][]string{{strings.Repeat("a", 50), "1"}}, Width: 30})
	for _, l := range out {
		if w := ansi.StringWidth(l); w > 30 {
			t.Errorf("line of %d cells: %q", w, plain(l))
		}
	}
	if !strings.Contains(plain(out[1]), "…") {
		t.Errorf("a cut cell ends in an ellipsis: %q", plain(out[1]))
	}
}

func TestNumAndMoney(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := Num(in); got != want {
			t.Errorf("Num(%d) = %q, want %q", in, got, want)
		}
	}
	if Money(0.4) != "$0.40" || Money(12) != "$12.00" || Money(-1) != "–" || Money(0) != "$0.00" {
		t.Errorf("Money: %s %s %s %s", Money(0.4), Money(12), Money(-1), Money(0))
	}
}

func TestMeter(t *testing.T) {
	if got := plain(Meter(0.7, 10)); got != "███████░░░ 70%" {
		t.Errorf("Meter(0.7) = %q", got)
	}
	if got := plain(Meter(0, 10)); got != "░░░░░░░░░░ 0%" {
		t.Errorf("Meter(0) = %q", got)
	}
	if got := plain(Meter(1.2, 10)); got != "██████████ 120%" {
		t.Errorf("Meter(1.2) = %q", got)
	}
	if got := plain(Meter(-1, 4)); got != "░░░░ 0%" {
		t.Errorf("Meter(-1) = %q", got)
	}
	if Meter(0.5, 0) != "" {
		t.Error("a zero width meter is empty")
	}
	if Meter(0.1, 10) == Meter(0.9, 10) || !strings.Contains(Meter(0.9, 10), "255") {
		t.Error("the meter colour must change from green to red")
	}
}

func TestMeterColorGradient(t *testing.T) {
	if MeterColor(0).Dark != OK.Dark || MeterColor(1).Dark != Fail.Dark || MeterColor(0.5).Dark != Warn.Dark {
		t.Errorf("gradient ends: %v %v %v", MeterColor(0), MeterColor(0.5), MeterColor(1))
	}
	if SparkColor(0).Dark != GroupProject.Dark || SparkColor(1).Dark != GroupUsage.Dark {
		t.Error("sparkline gradient runs teal to amber")
	}
}

func TestSparkline(t *testing.T) {
	if got := plain(Sparkline([]float64{0, 1, 4, 8, 2})); got != "▁▂▅█▃" {
		t.Errorf("Sparkline = %q", got)
	}
	if got := plain(Sparkline([]float64{0, 0})); got != "▁▁" {
		t.Errorf("all zero: %q", got)
	}
	if Sparkline(nil) != "" {
		t.Error("no values, no glyphs")
	}
}

func TestResultLine(t *testing.T) {
	for kind, glyph := range map[string]string{ResultOK: "✓", ResultError: "✗", ResultWarn: "⚠", ResultInfo: "›", "other": "›"} {
		if got := plain(ResultLine(kind, "text")); got != glyph+" text" {
			t.Errorf("%s: %q", kind, got)
		}
	}
	if ResultLine(ResultOK, "x") == ResultLine(ResultError, "x") {
		t.Error("ok and error must differ in colour")
	}
}

func TestNoColorOutputIsPlain(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	outs := []string{Panel(samplePanel(80, 120, 40)), Meter(0.5, 10), Sparkline([]float64{1, 2}), ResultLine(ResultError, "x"),
		strings.Join(Table(TableOpts{Cols: []Col{{Title: "A"}}, Rows: [][]string{{"1"}}, Header: GroupUsage}), "\n")}
	for _, o := range outs {
		if strings.Contains(o, "\x1b") {
			t.Errorf("NO_COLOR output has escapes: %q", o)
		}
	}
	if !strings.Contains(strings.Split(outs[0], "\n")[1], "[Budget]") {
		t.Errorf("without colour the active tab is bracketed: %q", strings.Split(outs[0], "\n")[1])
	}
}

func TestRoleColor(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range []string{"pm", "coder", "coder_fixer", "reviewer", "qa", "security", "ux_reviewer"} {
		c := RoleColor(r).(lipgloss.AdaptiveColor)
		if seen[c.Dark] {
			t.Errorf("%s repeats a colour", r)
		}
		seen[c.Dark] = true
	}
	if RoleColor("docs") != RoleColor("docs") {
		t.Error("the hash colour must be stable")
	}
	if RoleColor("sec:audit") == nil || RoleColor("") != Muted {
		t.Error("unknown and empty roles")
	}
	hashed := map[lipgloss.TerminalColor]bool{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		hashed[RoleColor(n)] = true
	}
	if len(hashed) < 3 {
		t.Errorf("the hash spreads over %d hues only", len(hashed))
	}
}

func TestRunnerColor(t *testing.T) {
	want := map[string]lipgloss.AdaptiveColor{"claude": Claude, "dsh": DeepSeek, "deepseek": DeepSeek, "codex": Codex, "openai": Codex,
		"opencode": OpenCode, "": OpenCode, "ollama": Local, "mlx": Local, "lmstudio": Local, "CLAUDE": Claude}
	for r, c := range want {
		if RunnerColor(r) != c {
			t.Errorf("RunnerColor(%q) = %v, want %v", r, RunnerColor(r), c)
		}
	}
	if RunnerColor("copilot") != Muted {
		t.Error("a runner without a colour is muted")
	}
	if Claude.Dark != "#D97757" || DeepSeek.Dark != "#4D6BFE" || Codex.Dark != "#10a37f" || OpenCode.Dark != "#f5c542" || Local.Dark != "#a8a2d0" {
		t.Error("runner colours changed")
	}
}

func TestGroupColor(t *testing.T) {
	want := map[string]string{GroupNameSession: "#5ee0ff", GroupNameRun: "#b48cff", GroupNameAgents: "#ff7ac6", GroupNameUsage: "#ffb86b", GroupNameProject: "#4fd6b0"}
	for g, hex := range want {
		if GroupColor(g).Dark != hex || GroupColor(g).Light == "" {
			t.Errorf("%s: %v", g, GroupColor(g))
		}
	}
	if GroupColor("nope") != Accent {
		t.Error("an unknown group is accent")
	}
}
