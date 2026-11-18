package ui

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

var widths = []int{20, 40, 80, 120}

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	os.Exit(m.Run())
}

func maxWidth(s string) int {
	w := 0
	for _, l := range strings.Split(s, "\n") {
		w = max(w, ansi.StringWidth(l))
	}
	return w
}

func plain(s string) string { return ansi.Strip(s) }

func TestTokensDarkValues(t *testing.T) {
	want := map[string]string{
		Text.Dark: "#d5e0f7", Muted.Dark: "#7d8db3", Border.Dark: "#2f4270",
		Accent.Dark: "#7c9cff", OK.Dark: "#6ff2b4", Warn.Dark: "#ffd166",
		Fail.Dark: "#ff5c7a", Sel.Dark: "#1c2b4d", Claude.Dark: "#D97757", DeepSeek.Dark: "#4D6BFE",
		DiffAdd.Dark: "#1b383d", DiffDel.Dark: "#372135",
	}
	for got, w := range want {
		if got != w {
			t.Errorf("dark token = %s, want %s", got, w)
		}
	}
}

func TestApply(t *testing.T) {
	t.Cleanup(func() { Apply("dark", nil) })
	cases := []struct {
		name   string
		mode   string
		detect func() (bool, bool)
		want   bool
	}{
		{"dark", "dark", nil, true},
		{"light", "light", nil, false},
		{"auto light", "auto", func() (bool, bool) { return false, true }, false},
		{"empty uses detector", "", func() (bool, bool) { return false, true }, false},
		{"invalid is auto", "blue", func() (bool, bool) { return false, true }, false},
		{"not ok", "auto", func() (bool, bool) { return false, false }, true},
		{"auto dark", "auto", func() (bool, bool) { return true, true }, true},
	}
	for _, c := range cases {
		Apply("light", nil)
		Apply(c.mode, c.detect)
		if IsDark() != c.want {
			t.Errorf("%s: IsDark = %v, want %v", c.name, IsDark(), c.want)
		}
	}
}

func TestApplyRunsDetectorSynchronously(t *testing.T) {
	t.Cleanup(func() { Apply("dark", nil) })
	before := runtime.NumGoroutine()
	var callerCalls int
	Apply("auto", func() (bool, bool) { callerCalls++; return false, true })
	if callerCalls != 1 || IsDark() {
		t.Fatalf("detector calls = %d, dark = %v", callerCalls, IsDark())
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("Apply left goroutines behind: %d -> %d", before, after)
	}
}

func TestDetectTerminalSkipsUnsafeQueries(t *testing.T) {
	asked := false
	query := func() bool { asked = true; return false }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	skips := map[string]struct {
		env map[string]string
		tty bool
	}{
		"not a tty": {map[string]string{"TERM": "xterm-256color"}, false},
		"no TERM":   {map[string]string{}, true},
		"dumb":      {map[string]string{"TERM": "dumb"}, true},
		"tmux":      {map[string]string{"TERM": "xterm-256color", "TMUX": "/tmp/tmux-1/default,1,0"}, true},
		"tmux TERM": {map[string]string{"TERM": "tmux-256color"}, true},
		"screen":    {map[string]string{"TERM": "screen", "STY": "123.pts"}, true},
	}
	for name, c := range skips {
		dark, ok := detectTerminal(env(c.env), c.tty, query)
		if ok || !dark || asked {
			t.Errorf("%s: dark=%v ok=%v asked=%v, want a skipped query", name, dark, ok, asked)
		}
	}
	dark, ok := detectTerminal(env(map[string]string{"TERM": "xterm-256color"}), true, query)
	if !ok || dark || !asked {
		t.Errorf("plain terminal: dark=%v ok=%v asked=%v, want the query's answer", dark, ok, asked)
	}
}

func TestApplyLightWinsInsideTmux(t *testing.T) {
	t.Cleanup(func() { Apply("dark", nil) })
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	Apply("light", nil)
	if IsDark() {
		t.Error("ui.theme light must apply inside tmux")
	}
	Apply("auto", nil)
	if !IsDark() {
		t.Error("auto inside tmux must be dark")
	}
}

func TestValidTheme(t *testing.T) {
	for _, ok := range []string{"", "auto", "dark", "light", " Dark "} {
		if !ValidTheme(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	if ValidTheme("neon") {
		t.Error("neon should be invalid")
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("hello world", 8); got != "hello w…" {
		t.Errorf("Truncate = %q", got)
	}
	if got := Truncate("hi", 8); got != "hi" {
		t.Errorf("Truncate short = %q", got)
	}
	if Truncate("hi", 0) != "" {
		t.Error("zero width must be empty")
	}
	for _, w := range []int{0, 1, 2, 5, 9, 15, 30} {
		got := TruncateMiddle("internal/tui/workspace_model.go", w)
		if ansi.StringWidth(got) > w {
			t.Errorf("TruncateMiddle w=%d: %q is %d wide", w, got, ansi.StringWidth(got))
		}
	}
	got := TruncateMiddle("internal/tui/workspace_model.go", 15)
	if !strings.HasPrefix(got, "inter") || !strings.HasSuffix(got, ".go") || !strings.Contains(got, "…") {
		t.Errorf("TruncateMiddle keeps both ends: %q", got)
	}
	if TruncateMiddle("a/b.go", 20) != "a/b.go" {
		t.Error("short path must stay")
	}
}

func TestRightAlign(t *testing.T) {
	for _, w := range widths {
		got := RightAlign(strings.Repeat("L", 200), "right", w)
		if ansi.StringWidth(got) > w {
			t.Errorf("w=%d: %d wide", w, ansi.StringWidth(got))
		}
		if !strings.HasSuffix(got, "right") {
			t.Errorf("w=%d: right side lost: %q", w, got)
		}
	}
	if got := RightAlign("ab", "cd", 10); got != "ab      cd" {
		t.Errorf("got %q", got)
	}
	if got := RightAlign("ab", strings.Repeat("r", 50), 10); ansi.StringWidth(got) != 10 {
		t.Errorf("long right: %q", got)
	}
}

func TestHeader(t *testing.T) {
	for _, w := range widths {
		h := plain(Header("decsr · "+strings.Repeat("project", 30), "0/3 done", w))
		if ansi.StringWidth(h) > w {
			t.Errorf("w=%d: header %d wide", w, ansi.StringWidth(h))
		}
		if !strings.HasPrefix(h, "◆ decsr") || !strings.HasSuffix(h, "0/3 done") {
			t.Errorf("w=%d: %q", w, h)
		}
	}
	if got := plain(Header("decsr", "meta", 30)); ansi.StringWidth(got) != 30 {
		t.Errorf("short header should fill the width: %q", got)
	}
	if ansi.StringWidth(plain(Header("a", strings.Repeat("r", 90), 20))) > 20 {
		t.Error("oversized right side")
	}
}

func TestPaneLabel(t *testing.T) {
	if got := plain(PaneLabel("Graph", false, 0)); got != "  Graph" {
		t.Errorf("got %q", got)
	}
	if PaneLabel("Graph", true, 0) == PaneLabel("Graph", false, 0) {
		t.Error("focused label must differ in style")
	}
	for _, w := range widths {
		if ansi.StringWidth(PaneLabel(strings.Repeat("x", 200), true, w)) > w {
			t.Errorf("w=%d exceeded", w)
		}
	}
}

func TestVDividerAndJoinColumns(t *testing.T) {
	if got := plain(VDivider(3)); got != "│\n│\n│" {
		t.Errorf("VDivider = %q", got)
	}
	if VDivider(0) != "" {
		t.Error("zero height")
	}
	for _, w := range widths {
		out := JoinColumns(w,
			Column{Width: w / 3, Body: strings.Repeat("a", 300) + "\nsecond"},
			Column{Width: w / 3, Body: "b"},
			Column{Width: w, Body: "c\nd\ne"})
		if maxWidth(out) > w {
			t.Errorf("w=%d: %d wide", w, maxWidth(out))
		}
		for i, l := range strings.Split(plain(out), "\n") {
			if ansi.StringWidth(l) != w {
				t.Errorf("w=%d row %d is %d wide", w, i, ansi.StringWidth(l))
			}
			if strings.Contains(l, "││") {
				t.Errorf("double divider: %q", l)
			}
		}
		if n := strings.Count(plain(out), "│"); n != 2*3 {
			t.Errorf("w=%d: %d dividers, want 6", w, n)
		}
	}
	if got := plain(JoinColumns(10, Column{Width: 4, Body: "ab"}, Column{Width: 5, Body: "cd"})); got != "ab  │ cd  " {
		t.Errorf("got %q", got)
	}
}

func TestCard(t *testing.T) {
	for _, w := range widths {
		for _, o := range []CardOpts{
			{Title: strings.Repeat("T", 90), Meta: "16:00", Body: []string{strings.Repeat("b", 200), "short"}, Width: w},
			{Title: "T1 Search", Meta: "16:00", Body: []string{"x"}, Width: w, Selected: true},
			{Title: "T1 Search", Meta: strings.Repeat("m", 90), Width: w, Failed: true},
		} {
			c := Card(o)
			if maxWidth(c) != w {
				t.Errorf("w=%d: card is %d wide", w, maxWidth(c))
			}
			ls := strings.Split(plain(c), "\n")
			if !strings.HasPrefix(ls[0], "╭") || !strings.HasPrefix(ls[len(ls)-1], "╰") {
				t.Errorf("card corners: %q", ls)
			}
		}
	}
	c := plain(Card(CardOpts{Title: "T1 Parse", Meta: "done", Body: []string{"body"}, Width: 30}))
	if first := strings.Split(c, "\n")[1]; !strings.Contains(first, "T1 Parse") || !strings.HasSuffix(first, "done │") {
		t.Errorf("first line: %q", first)
	}
	if Card(CardOpts{Title: "x", Width: 3}) != "" {
		t.Error("tiny card should be empty")
	}
}

func TestCardVariantsDiffer(t *testing.T) {
	lipgloss.SetHasDarkBackground(true)
	base := CardOpts{Title: "T", Body: []string{"b"}, Width: 30}
	sel, fail := base, base
	sel.Selected, fail.Failed = true, true
	a, b, c := Card(base), Card(sel), Card(fail)
	if a == b || a == c || b == c {
		t.Error("plain, selected and failed cards must be styled differently")
	}
	if !strings.Contains(c, "255;92;12") {
		t.Errorf("failed card lacks the Fail colour: %q", c)
	}
	if !strings.Contains(b, "124;156;255") {
		t.Errorf("selected card lacks the Accent colour: %q", b)
	}
}

func TestListRow(t *testing.T) {
	for _, w := range widths {
		for _, sel := range []bool{false, true} {
			r := ListRow(RowOpts{Left: strings.Repeat("L", 200), Right: "5m", Width: w, Selected: sel, Indent: 2})
			if ansi.StringWidth(r) != w {
				t.Errorf("w=%d sel=%v: %d wide", w, sel, ansi.StringWidth(r))
			}
			if strings.HasPrefix(plain(r), "▌") != sel {
				t.Errorf("w=%d: cursor mismatch sel=%v: %q", w, sel, plain(r))
			}
		}
	}
	if got := plain(ListRow(RowOpts{Left: "a", Right: "b", Width: 10, Indent: 1})); got != "  a      b" {
		t.Errorf("got %q", got)
	}
	if ansi.StringWidth(ListRow(RowOpts{Left: "abc", Right: "def", Width: 2, Indent: 5})) > 2 {
		t.Error("tiny row")
	}
}

func TestHintBar(t *testing.T) {
	hints := []Hint{{"Enter", "send"}, {"Esc", "close"}, {"Tab", "focus"}, {"?", "help"}}
	full := plain(HintBar(hints, 200))
	if full != "  Enter send · Esc close · Tab focus · ? help" {
		t.Errorf("full = %q", full)
	}
	for _, w := range []int{50, 30, 20, 8} {
		got := plain(HintBar(hints, w))
		if ansi.StringWidth(got) > w {
			t.Errorf("w=%d: %q is %d wide", w, got, ansi.StringWidth(got))
		}
	}
	if got := plain(HintBar(hints, 35)); got != "  Enter send · Esc close" {
		t.Errorf("trailing hints should drop: %q", got)
	}
	if HintBar(nil, 20) != "" {
		t.Error("no hints")
	}
}

func TestInputBox(t *testing.T) {
	for _, w := range widths {
		for _, f := range []bool{false, true} {
			b := InputBox("typed text", "Type a task", w, f, "shared  opencode")
			if maxWidth(b) != w {
				t.Errorf("w=%d: %d wide", w, maxWidth(b))
			}
		}
	}
	empty := plain(InputBox("   ", "Type a task", 40, true, ""))
	if !strings.Contains(empty, "Type a task") {
		t.Errorf("placeholder missing: %q", empty)
	}
	if got := plain(InputBox("hello", "Type a task", 40, true, "chips")); strings.Contains(got, "Type a task") || !strings.Contains(got, "hello") || !strings.Contains(got, "chips") {
		t.Errorf("filled box: %q", got)
	}
}

func TestOverlayAndPlacement(t *testing.T) {
	for _, w := range []int{20, 40, 80} {
		o := Overlay("A rather long title for the box", []string{strings.Repeat("x", 300), "two"}, []Hint{{"y", "yes"}, {"n", "no"}}, w, nil)
		if maxWidth(o) != w {
			t.Errorf("w=%d: overlay %d wide", w, maxWidth(o))
		}
		if !strings.Contains(plain(o), "yes") && w >= 40 {
			t.Errorf("w=%d: hints missing", w)
		}
	}
	const sw, sh = 40, 12
	base := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", sw)+"\n", sh), "\n")
	ov := Overlay("T", []string{"one", "two"}, nil, 20, nil)
	out := PlaceOverlay(base, ov, sw, sh)
	ls := strings.Split(plain(out), "\n")
	if len(ls) != sh {
		t.Fatalf("height %d", len(ls))
	}
	if ls[0] != strings.Repeat(".", sw) || ls[sh-1] != strings.Repeat(".", sw) {
		t.Error("first and last base line must stay")
	}
	found := false
	for _, l := range ls {
		if strings.Contains(l, "│ one") {
			found = true
			if ansi.StringWidth(l) != sw || !strings.HasPrefix(l, strings.Repeat(" ", 10)) {
				t.Errorf("not centred / not blanked: %q", l)
			}
		}
	}
	if !found {
		t.Errorf("overlay body missing:\n%s", plain(out))
	}
	at := strings.Split(plain(PlaceAt(base, "ab\ncd", 3, 1, sw, sh)), "\n")
	if !strings.HasPrefix(at[1], "   ab") || !strings.HasPrefix(at[2], "   cd") {
		t.Errorf("PlaceAt: %q", at[1:3])
	}
}

func TestStatus(t *testing.T) {
	kinds := []string{"todo", "ready", "blocked", "running", "done", "failed", "waiting", "limited", "unassigned", "stalled"}
	seen := map[string]bool{}
	for _, k := range kinds {
		g, w, c := Status(k)
		if g == "" || w == "" || c == nil {
			t.Errorf("%s: incomplete (%q %q %v)", k, g, w, c)
		}
		seen[k+g] = true
	}
	for k, want := range map[string][2]string{
		"done": {"✓", "done"}, "failed": {"!", "failed"}, "waiting": {"◇", "waiting"},
		"limited": {"◷", "waiting"}, "running": {"▖", "running"}, "unassigned": {"?", "unassigned"},
		"blocked": {"⏸", "blocked"}, "ready": {"○", "ready"},
	} {
		if g, w, _ := Status(k); g != want[0] || w != want[1] {
			t.Errorf("%s = %q %q", k, g, w)
		}
	}
	if g, w, _ := Status("mystery"); g == "" || w != "mystery" {
		t.Errorf("unknown kind: %q %q", g, w)
	}
}

func TestChip(t *testing.T) {
	if got := plain(Chip("shared")); got != "[shared]" {
		t.Errorf("chip = %q", got)
	}
}

func TestJoinColumnsKeepsGutterAroundDivider(t *testing.T) {
	out := plain(JoinColumns(30,
		Column{Width: 10, Body: strings.Repeat("a", 40)},
		Column{Width: 10, Body: strings.Repeat("b", 40)},
		Column{Width: 9, Body: strings.Repeat("c", 40)}))
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "a│") || strings.Contains(l, "│b") || strings.Contains(l, "b│") || strings.Contains(l, "│c") {
			t.Errorf("text touches a divider: %q", l)
		}
		if ansi.StringWidth(l) != 30 {
			t.Errorf("row is %d wide", ansi.StringWidth(l))
		}
	}
}
