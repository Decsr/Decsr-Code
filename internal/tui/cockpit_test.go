package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
)

func cockpitFixture(t *testing.T) (CockpitModel, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := `{"tasks":[
		{"key":"T1","title":"Add session model","description":"model","priority":2},
		{"key":"T2","title":"Wire login handler","description":"handler","priority":2,"depends_on":["T1"]},
		{"key":"T3","title":"Add auth middleware","description":"middleware","priority":2,"depends_on":["T2"]},
		{"key":"T4","title":"Integration tests","description":"tests","priority":2,"depends_on":["T2"]},
		{"key":"T5","title":"Update docs","description":"docs","priority":3,"depends_on":["T1","T3"]}
	],"id_by_key":{"T1":"b1","T2":"b2","T3":"b3","T4":"b4","T5":"b5"}}`
	if err := os.WriteFile(filepath.Join(dir, ".decsr", "sub_tasks.json"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCockpit(config.Config{}, dir, filepath.Join(dir, ".decsr"))
	c.SetTaskInput("Add JWT auth to the login endpoint")
	c.SetStarted(time.Now().Add(-12 * time.Minute))
	return c, dir
}

func TestCockpitObservesWorkflow(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)

	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "pipeline: green"))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "planning"))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "decomposed into 5 sub-tasks"))
	c.LoadPlan()
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "── Sub-task 1/5 (b1): Add session model ──"))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 1200, OutputTokens: 300}))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgEvent, bus.TokenPayload{Text: "⚙ edit internal/auth/middleware.go +48 -12\n$ go test ./internal/auth/...\n"}))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "── Sub-task 2/5 (b2): Wire login handler ──"))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 2200, OutputTokens: 400}))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "all tests passed"))

	if len(c.subtasks) != 5 {
		t.Fatalf("subtasks = %d, want 5", len(c.subtasks))
	}
	if c.subtasks[0].status != "done" {
		t.Errorf("T1 status = %q, want done", c.subtasks[0].status)
	}
	if c.subtasks[1].status != "active" {
		t.Errorf("T2 status = %q, want active", c.subtasks[1].status)
	}
	if c.tests.status != "pass" {
		t.Errorf("tests = %q, want pass", c.tests.status)
	}
	if c.totalUsage.InputTokens != 3400 {
		t.Errorf("input tokens = %d, want 3400", c.totalUsage.InputTokens)
	}
	if c.subtasks[0].tokens == 0 || c.subtasks[1].tokens == 0 {
		t.Errorf("expected per-subtask tokens, got %d/%d", c.subtasks[0].tokens, c.subtasks[1].tokens)
	}

	view := c.View()
	for _, want := range []string{"WORKFLOW", "Add session model", "TOKENS", "TESTS", "T2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestCockpitLoadPlanIsIdempotent(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.LoadPlan()
	c.LoadPlan()
	if len(c.subtasks) != 5 {
		t.Fatalf("subtasks = %d, want 5 after two loads", len(c.subtasks))
	}
}

func TestCockpitCompactView(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(80, 24)
	c.LoadPlan()
	view := c.View()
	if !strings.Contains(view, "WORKFLOW") {
		t.Fatalf("compact view missing workflow pane:\n%s", view)
	}
	if !strings.Contains(view, "tok ") {
		t.Fatalf("compact footer missing token summary:\n%s", view)
	}
}

func TestCockpitTabAndFocus(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyTab})
	if c.focus != cockpitFocusCenter {
		t.Fatalf("focus = %v, want center", c.focus)
	}
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	if c.tab != 3 {
		t.Fatalf("tab = %d, want 3 (Tokens)", c.tab)
	}
}

func TestModelCockpitObservesBusMessages(t *testing.T) {
	c, root := cockpitFixture(t)
	_ = c
	m := New(nil, root, filepath.Join(root, ".decsr"), nil, config.Config{}).WithTaskInput("Add JWT auth to the login endpoint")
	m.width, m.height = 120, 40
	m.layout()
	m.cockpit.LoadPlan()

	for _, ev := range []string{
		"pipeline: green",
		"planning",
		"decomposed into 5 sub-tasks",
		"── Sub-task 1/5 (b1): Add session model ──",
	} {
		updated, _ := m.Update(BusMessageMsg{Msg: bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, ev)})
		m = updated.(Model)
	}
	m.cockpit.LoadPlan()
	view := m.View()
	for _, want := range []string{"WORKFLOW", "Add session model", "TOKENS", "TESTS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("model view missing %q:\n%s", want, view)
		}
	}
}

func feedSessions(c *CockpitModel) {
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "planning"))
	c.LoadPlan()
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "── Sub-task 1/5 (b1): Add session model ──"))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 1200, OutputTokens: 300}))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "── Sub-task 2/5 (b2): Wire login handler ──"))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 2200, OutputTokens: 400}))
}

func TestCockpitRecordsSessions(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	feedSessions(&c)
	c.phase = "spec"
	c.Observe(bus.NewMessage(bus.RolePM, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 500, OutputTokens: 100}))
	if len(c.sessions) != 3 {
		t.Fatalf("sessions = %d, want 3", len(c.sessions))
	}
	if c.sessions[0].subtask != "T1" {
		t.Errorf("subtask = %q, want T1", c.sessions[0].subtask)
	}
	if c.sessions[2].role != bus.RolePM || c.sessions[2].subtask != "" {
		t.Errorf("session[2] = %+v", c.sessions[2])
	}
	if c.agents[bus.RoleCoder].calls != 2 {
		t.Errorf("coder calls = %d, want 2", c.agents[bus.RoleCoder].calls)
	}
}

func TestCockpitSessionLogCapped(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	for i := 0; i < 600; i++ {
		c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: i + 1, OutputTokens: 1}))
	}
	if len(c.sessions) != 500 {
		t.Fatalf("sessions = %d, want 500", len(c.sessions))
	}
	want := 600 * 601 / 2
	if c.totalUsage.InputTokens != want {
		t.Errorf("input = %d, want %d", c.totalUsage.InputTokens, want)
	}
}

func TestCockpitAgentStripShowsActiveAgent(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(150, 40)
	c.Observe(bus.NewMessage(bus.RolePM, bus.RoleCoder, bus.MsgRequest, "go"))
	got := ansi.Strip(c.renderAgentStrip(150))
	if strings.Contains(got, "○ Coder") {
		t.Errorf("coder shown as waiting: %q", got)
	}
	for _, want := range []string{"Coder — ×0", "○ PM"} {
		if !strings.Contains(got, want) {
			t.Errorf("strip missing %q: %q", want, got)
		}
	}
}

func TestCockpitViewFitsWidth(t *testing.T) {
	for _, w := range []int{60, 90, 110, 150} {
		c, _ := cockpitFixture(t)
		c.SetSize(w, 40)
		feedSessions(&c)
		lines := strings.Split(ansi.Strip(c.View()), "\n")
		if len(lines) != 40 {
			t.Errorf("w=%d lines = %d, want 40", w, len(lines))
		}
		for i, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Errorf("w=%d line %d width %d: %q", w, i, ansi.StringWidth(l), l)
			}
		}
	}
}

func TestCockpitHeaderShowsPipelineAndState(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(90, 40)
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "pipeline: green"))
	c.SetError(errors.New("boom"))
	first := strings.Split(ansi.Strip(c.View()), "\n")[0]
	for _, want := range []string{"GREEN", "FAILED"} {
		if !strings.Contains(first, want) {
			t.Errorf("header missing %q: %q", want, first)
		}
	}
	g, _ := cockpitFixture(t)
	g.SetSize(90, 40)
	g.SetGate("task_plan.md")
	first = strings.Split(ansi.Strip(g.View()), "\n")[0]
	if !strings.Contains(first, "GATE") {
		t.Errorf("header missing GATE: %q", first)
	}
}

func TestTokensTabEmpty(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	c.SetTab(3)
	if got := ansi.Strip(c.View()); !strings.Contains(got, "no usage reported yet") {
		t.Errorf("view missing empty message:\n%s", got)
	}
}

func TestTokensTabShowsCallChartAndLog(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(150, 40)
	feedSessions(&c)
	c.phase = "spec"
	c.Observe(bus.NewMessage(bus.RolePM, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 500, OutputTokens: 100}))
	lines := c.tokenLines(100, 40)
	got := ansi.Strip(strings.Join(lines, "\n"))
	t.Logf("\n%s", got)
	for _, want := range []string{"3 calls", "TOKENS PER CALL", "CALL LOG", "PER AGENT"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	found := false
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#2") {
			found = true
			for _, want := range []string{"build", "T2", "2.6k"} {
				if !strings.Contains(l, want) {
					t.Errorf("log row %q missing %q", l, want)
				}
			}
		}
	}
	if !found {
		t.Errorf("no log row #2:\n%s", got)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 100 {
			t.Errorf("line too wide: %q", ansi.Strip(l))
		}
	}
}

func TestTokensTabManyCalls(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	for i := 0; i < 200; i++ {
		c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: i + 1, OutputTokens: 1}))
	}
	lines := c.tokenLines(60, 40)
	if got := ansi.Strip(strings.Join(lines, "\n")); !strings.Contains(got, "earlier calls") {
		t.Errorf("missing earlier calls:\n%s", got)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 60 {
			t.Errorf("line too wide: %q", ansi.Strip(l))
		}
	}
}

func TestTokensTabEstimated(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	for i := 0; i < 3; i++ {
		c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 100, OutputTokens: 10, Estimated: true}))
	}
	got := ansi.Strip(strings.Join(c.tokenLines(100, 40), "\n"))
	rows := strings.Split(got, "\n")
	if !strings.Contains(rows[0], "330~") {
		t.Errorf("TOTAL line = %q", rows[0])
	}
	n := 0
	for _, l := range rows {
		if strings.HasPrefix(l, "#") {
			n++
			if !strings.Contains(l, "~") {
				t.Errorf("log row without ~: %q", l)
			}
		}
	}
	if n != 3 {
		t.Errorf("log rows = %d, want 3", n)
	}
}

func TestTokensTabPerAgentUsesLifetimeUsage(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	for i := 0; i < 501; i++ {
		c.Observe(bus.NewMessage(bus.RolePM, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 10}))
	}
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 10}))
	for _, l := range c.tokenLines(100, 60) {
		l = ansi.Strip(l)
		if strings.HasPrefix(l, "PM ") {
			if n := strings.Count(l, "█"); n != 100-24 {
				t.Errorf("PM bar cells = %d, want %d: %q", n, 100-24, l)
			}
			return
		}
	}
	t.Error("no PM row")
}

func TestAgentBarFullWidth(t *testing.T) {
	got := ansi.Strip(agentBar(50, 50, 100, 7, lipgloss.Color("#fff")))
	if n := ansi.StringWidth(got); n != 7 {
		t.Errorf("cells = %d, want 7: %q", n, got)
	}
}

func TestTokensTabFitsPane(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {100, 30}} {
		c, _ := cockpitFixture(t)
		c.SetSize(sz[0], sz[1])
		for i := 0; i < 3; i++ {
			c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 100, OutputTokens: 10, Estimated: true}))
		}
		c.SetTab(3)
		view := ansi.Strip(c.View())
		if !strings.Contains(view, "~") {
			t.Errorf("%v: no ~ in view", sz)
		}
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, "…") {
				t.Errorf("%v: truncated line %q", sz, l)
			}
		}
	}
}

func TestInspectorShowsCallSparkline(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(150, 40)
	feedSessions(&c)
	c.Observe(bus.NewMessage(bus.RolePM, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 500, OutputTokens: 100}))
	lines := strings.Split(ansi.Strip(c.renderInspector(40, 40)), "\n")
	in, found := false, false
	for _, l := range lines {
		switch {
		case strings.Contains(l, "TOKENS"):
			in = true
		case strings.Contains(l, "TESTS"):
			in = false
		case in && strings.ContainsAny(l, "▁▂▃▄▅▆▇"):
			found = true
		}
		if ansi.StringWidth(l) > 40 {
			t.Errorf("line too wide: %q", l)
		}
	}
	if !found {
		t.Errorf("no sparkline glyph between TOKENS and TESTS:\n%s", strings.Join(lines, "\n"))
	}
}

func TestInspectorAgentRowsShowCalls(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(150, 40)
	feedSessions(&c)
	out := ansi.Strip(c.renderInspector(40, 40))
	if !strings.Contains(out, "×2") {
		t.Errorf("no ×2 calls in inspector:\n%s", out)
	}
}

func TestAgentsTabShowsCalls(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(150, 40)
	feedSessions(&c)
	out := ansi.Strip(strings.Join(c.agentLines(100, 40), "\n"))
	for _, want := range []string{"calls 2", "last call 2.6k", "avg "} {
		if !strings.Contains(out, want) {
			t.Errorf("agents tab missing %q:\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "▁▂▃▄▅▆▇") {
		t.Errorf("agents tab has no per-agent sparkline:\n%s", out)
	}
}

func TestFocusedPaneMarker(t *testing.T) {
	on := ansi.Strip(cockpitPane("RUN", 30, 5, nil, true, galaxyBlue))
	off := ansi.Strip(cockpitPane("RUN", 30, 5, nil, false, galaxyBlue))
	if !strings.Contains(on, "▌") {
		t.Errorf("focused pane has no marker:\n%s", on)
	}
	if strings.Contains(off, "▌") {
		t.Errorf("unfocused pane has marker:\n%s", off)
	}
	topOn := strings.Split(on, "\n")[0]
	topOff := strings.Split(off, "\n")[0]
	if ansi.StringWidth(topOn) != ansi.StringWidth(topOff) || ansi.StringWidth(topOn) != 30 {
		t.Errorf("top border widths differ: %d vs %d", ansi.StringWidth(topOn), ansi.StringWidth(topOff))
	}
}

func TestInspectorFitsPane(t *testing.T) {
	for _, sz := range [][2]int{{110, 40}, {150, 42}} {
		c, _ := cockpitFixture(t)
		c.SetSize(sz[0], sz[1])
		feedSessions(&c)
		c.Observe(bus.NewMessage(bus.RolePM, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 500, OutputTokens: 100}))
		c.SetTab(2)
		rightW := 34
		if sz[0] >= 150 {
			rightW = 40
		}
		view := ansi.Strip(c.View())
		spark := false
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, "…") {
				t.Errorf("%v: truncated line %q", sz, l)
			}
			if strings.ContainsAny(ansi.Cut(l, sz[0]-rightW, sz[0]), "▁▂▃▄▅▆▇") {
				spark = true
			}
		}
		if !spark {
			t.Errorf("%v: no sparkline glyph in view", sz)
		}
	}
}

func TestTokensTabCallNumbersAfterCap(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	for i := 0; i < 600; i++ {
		c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: i + 1, OutputTokens: 1}))
	}
	lines := c.tokenLog(100)
	if got := ansi.Strip(lines[1]); !strings.HasPrefix(got, "#600") {
		t.Errorf("newest log row = %q, want #600 prefix", got)
	}
	// chart width 60 shows 30 columns; 600-30 calls are hidden in total.
	chart := ansi.Strip(strings.Join(c.tokenChart(60, 40), "\n"))
	if !strings.Contains(chart, "+570 earlier calls") {
		t.Errorf("chart note wrong:\n%s", chart)
	}
}

func TestCockpitSetDoneStopsAgents(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(150, 40)
	c.Observe(bus.NewMessage(bus.RolePM, bus.RoleCoder, bus.MsgRequest, "go"))
	c.SetDone(errors.New("x"))
	if got := c.agents[bus.RoleCoder].status; got != AgentError {
		t.Errorf("coder status = %v, want AgentError", got)
	}
	if got := ansi.Strip(c.renderAgentStrip(150)); !strings.Contains(got, "! Coder") {
		t.Errorf("strip missing error chip: %q", got)
	}

	d, _ := cockpitFixture(t)
	d.Observe(bus.NewMessage(bus.RolePM, bus.RoleCoder, bus.MsgRequest, "go"))
	d.SetDone(nil)
	if got := d.agents[bus.RoleCoder].status; got != AgentDone {
		t.Errorf("coder status = %v, want AgentDone", got)
	}
}

func TestInspectorShowsErrorWhenShort(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(110, 30)
	for i := 0; i < 3; i++ {
		c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 100, OutputTokens: 10}))
	}
	c.SetError(errors.New("build failed: boom"))
	got := ansi.Strip(c.View())
	for _, want := range []string{"ERROR", "boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("view missing %q:\n%s", want, got)
		}
	}
}

func TestTokensTabSubtaskUsageBeforeLog(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	for i := 0; i < 500; i++ {
		c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: i + 1, OutputTokens: 1}))
	}
	got := ansi.Strip(strings.Join(c.tokenLines(100, 40), "\n"))
	if !strings.Contains(got, "SUBTASK USAGE") {
		t.Errorf("SUBTASK USAGE not visible at scroll 0:\n%s", got)
	}
}

func TestInspectorMiniBarMinimumCell(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(110, 40)
	c.Observe(bus.NewMessage(bus.RolePM, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 500, OutputTokens: 100}))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 4000, OutputTokens: 100}))
	for _, l := range strings.Split(ansi.Strip(c.renderInspector(40, 60)), "\n") {
		if strings.Contains(l, "PM") && strings.Contains(l, "600") {
			if !strings.Contains(l, "█") {
				t.Errorf("PM mini bar empty: %q", l)
			}
			return
		}
	}
	t.Error("PM row not found")
}
