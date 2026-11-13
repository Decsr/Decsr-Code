package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func TestWorkspaceCheckNodeAlwaysPresent(t *testing.T) {
	h := newWSHarness(t)
	h.checks = nil
	h.toGraph()
	c := h.eng.Snapshot().Node("CHECK")
	if c == nil || len(c.Commands) != 0 {
		t.Fatalf("want a deferred CHECK node, got %+v", c)
	}
	h.want("0/6 done")

	h2 := newWSHarness(t)
	h2.toGraph()
	if c := h2.eng.Snapshot().Node("CHECK"); c == nil || len(c.Commands) != 2 {
		t.Fatalf("want CHECK with commands, got %+v", c)
	}
}

func TestWorkspaceRoleFormReadOnly(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.sel = "T2"
	h.key("a", "enter") // the create row opens the form with the name filled in
	h.want("Can change files")
	h.want("yes")
	h.key("tab") // description -> toggle
	h.key("right")
	h.want("reports only")
	h.key("space")
	h.key(" ")
	h.key("enter")
	if len(h.roleCalls) != 1 || !h.roleCalls[0].ac.ReadOnly {
		t.Fatalf("saveRole calls = %+v", h.roleCalls)
	}
	h.want("docs_writer is read-only: it will report, not change files")
}

func TestWorkspaceRoleFormDefaultsToCanChange(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.sel = "T2"
	h.key("a", "enter", "enter")
	if len(h.roleCalls) != 1 || h.roleCalls[0].ac.ReadOnly {
		t.Fatalf("saveRole calls = %+v", h.roleCalls)
	}
}

func TestWorkspaceReadOnlyShownInPickerAndInspector(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.sel = "T5"
	h.want("security")
	h.key("a")
	h.want("read-only")
	h.key("enter")
	h.want("security is read-only: it will report, not change files")
	out := ansi.Strip(renderInspector(h.m.g, h.m.g.Node("T5"), h.m.cfg, plugins.Set{}, 36, 20, nil))
	if !strings.Contains(out, "read-only") {
		t.Fatalf("inspector lacks read-only:\n%s", out)
	}
}

func TestWorkspaceWarnings(t *testing.T) {
	g := wsFixtureGraph()
	g.Nodes[0].Status = workspace.StatusDone
	g.Nodes[0].Role = "coder"
	g.Nodes[0].Warnings = []string{"ignored write to .git/config", "second"}
	out := plain(g, canvasOpts{width: 100, height: 20, selected: "T1"})
	if !strings.Contains(out, "✓ done ⚠") {
		t.Fatalf("no warning mark in the title border:\n%s", out)
	}
	insp := ansi.Strip(renderInspector(g, g.Node("T1"), config.DefaultConfig(), plugins.Set{}, 36, 30, nil))
	i, j := strings.Index(insp, "WARNINGS"), strings.Index(insp, "⚠ ignored write")
	if i < 0 || j < i {
		t.Fatalf("inspector lacks WARNINGS:\n%s", insp)
	}
	g.Nodes[0].Summary = "did it"
	insp = ansi.Strip(renderInspector(g, g.Node("T1"), config.DefaultConfig(), plugins.Set{}, 36, 30, nil))
	if strings.Index(insp, "WARNINGS") > strings.Index(insp, "SUMMARY") {
		t.Fatalf("WARNINGS must come above SUMMARY:\n%s", insp)
	}

	h := newWSHarness(t)
	h.graph = func() *workspace.Graph { g := wsFixtureGraph(); g.Nodes[0].Warnings = []string{"a", "b"}; return g }
	h.toGraph()
	h.want("2 warnings")
	// A narrow node box drops the mark instead of overflowing.
	for _, l := range strings.Split(plain(g, canvasOpts{width: 100, height: 20}), "\n") {
		if ansi.StringWidth(l) != 100 {
			t.Fatalf("line width %d", ansi.StringWidth(l))
		}
	}
}

func TestWorkspaceEngineClosedOnClear(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	old := h.eng
	pump := wsWaitEvent(old)
	h.m.g.Nodes[0].Status = workspace.StatusDone
	// /clear: nothing is running, the graph is unfinished.
	h.key("tab")
	h.typeCmd("/clear")
	h.key("y")
	if h.m.eng != nil || h.m.hasNodes() {
		t.Fatalf("the graph was not cleared:\n%s", h.view())
	}
	select {
	case <-old.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("old engine was not closed")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- pump() }()
	select {
	case msg := <-done:
		// The initial event may still be buffered; either way the pump returns.
		_ = msg
	case <-time.After(3 * time.Second):
		t.Fatal("event pump still blocked on the closed engine")
	}
	// A pump started after Close and with no events returns nil.
	for {
		select {
		case <-old.Events():
			continue
		default:
		}
		break
	}
	if msg := wsWaitEvent(old)(); msg != nil {
		t.Fatalf("closed engine pump returned %#v", msg)
	}
}

func TestWorkspaceQuitClosesEngine(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	cmd := h.key("q")
	if cmd == nil {
		t.Fatal("q did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("quit command did not return QuitMsg")
	}
	select {
	case <-h.eng.Done():
	default:
		t.Fatal("engine not closed on quit")
	}
}

func TestWorkspaceFixAddedNotice(t *testing.T) {
	h := newWSHarness(t)
	h.graph = func() *workspace.Graph {
		g := wsFixtureGraph()
		g.NewRoles = nil
		g.Nodes = g.Nodes[:1]
		g.Nodes[0].Status = workspace.StatusDone
		g.Nodes[0].Role = "coder"
		c := g.AddCheck([]string{"go test ./..."})
		c.Status = workspace.StatusFailed
		c.Error = "FAIL"
		return g
	}
	h.toGraph()
	h.m.sel = "CHECK"
	h.key("f")
	if want := "Added F1. Assign it, run it, then run Build & test again (or press R)."; h.m.notice != want {
		t.Fatalf("notice = %q", h.m.notice)
	}
}

func TestWorkspaceAssignJoinsRunningBatch(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "R")
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusRunning })
	h.m.sel = "T4"
	h.key("a", "enter")
	if want := "T4 joins the running batch and starts when its dependencies finish"; h.m.notice != want {
		t.Fatalf("notice = %q", h.m.notice)
	}
}

func TestWorkspacePaletteTouchesInput(t *testing.T) {
	for _, sz := range [][2]int{{60, 16}, {80, 24}, {120, 40}} {
		h := newWSHarness(t)
		h.size(sz[0], sz[1])
		h.key("/")
		lines := strings.Split(h.view(), "\n")
		last, box := -1, -1
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "▸ /") || strings.HasPrefix(strings.TrimSpace(l), "/") {
				last = i
			}
			if box < 0 && strings.HasPrefix(l, "┌") && i > 3 && last >= 0 {
				box = i
			}
		}
		if last < 0 || box != last+1 {
			t.Fatalf("%v: command list ends at line %d, input box starts at %d:\n%s", sz, last, box, h.view())
		}
	}
}

func TestWorkspaceInputClearedWhileSplitting(t *testing.T) {
	h := newWSHarness(t)
	h.m.input.SetValue("Build a todo CLI")
	h.key("enter")
	if h.m.input.Value() != "" {
		t.Fatalf("input not cleared: %q", h.m.input.Value())
	}
	h.send(wsSplitDoneMsg{gen: h.m.splitGen, err: fmt.Errorf("boom")})
	if h.m.input.Value() != "Build a todo CLI" {
		t.Fatalf("input not restored: %q", h.m.input.Value())
	}
}

func TestWorkspaceSecondTaskDoesNotAskToReplace(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("tab")
	h.m.input.SetValue("another task")
	h.key("enter")
	if !h.m.splitting || h.m.overlay != wsOverlayNone {
		t.Fatal("a second task must go straight to the lead")
	}
	h.wantNot("Replace the unfinished graph")
}

func TestShortModel(t *testing.T) {
	for _, c := range []struct{ runner, model, want string }{
		{"opencode", "opencode-go/deepseek-v4.1-flash", "deepseek-v4.1-flash"},
		{"", "a/b/c", "c"},
		{"claude", "sonnet", "claude:sonnet"},
		{"claude", "", "claude"},
		{"opencode", "", ""},
	} {
		if got := shortModel(c.runner, c.model); got != c.want {
			t.Errorf("shortModel(%q,%q) = %q, want %q", c.runner, c.model, got, c.want)
		}
	}
}

func TestCanvasShowsShortModel(t *testing.T) {
	cfg := config.DefaultConfig()
	ac := cfg.Agents["coder"]
	ac.Runner, ac.Model = "claude", "anthropic/sonnet"
	cfg.Agents["coder"] = ac
	g := diamond()
	models := map[string]string{}
	for _, r := range workspace.Roles(cfg) {
		models[r.Name] = shortModel(r.Agent.Runner, r.Agent.Model)
	}
	out := plain(g, canvasOpts{width: 100, height: 10, models: models})
	if !strings.Contains(out, "coder · claude:sonnet") {
		t.Fatalf("no short model:\n%s", out)
	}
}

func TestInspectorDescriptionUsesFreeSpace(t *testing.T) {
	g := wsFixtureGraph()
	n := g.Node("T1")
	n.Description = strings.Join([]string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9", "l10", "l11", "l12"}, "\n")
	n.Description = strings.Repeat("word ", 60) // about 9 lines at width 34
	out := ansi.Strip(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, 34, 30, nil))
	if strings.Count(out, "word") < 55 || strings.Contains(out, "…") {
		t.Fatalf("description was cut although the pane is free:\n%s", out)
	}
	// With output on a running node the OUTPUT keeps four lines.
	n.Status = workspace.StatusRunning
	n.Role = "coder"
	tail := []string{"o1", "o2", "o3", "o4", "o5", "o6"}
	out = ansi.Strip(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, 34, 16, tail))
	for _, want := range []string{"o3", "o4", "o5", "o6", "OUTPUT"} {
		if !strings.Contains(out, want) {
			t.Fatalf("OUTPUT lost %q:\n%s", want, out)
		}
	}
	assertFits(t, "inspector", out, 34, 16)
}

func TestWorkspaceFramesAndFit(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	v := h.view()
	for _, want := range []string{"┌─ GRAPH", "┌─ T1 "} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing frame %q:\n%s", want, v)
		}
	}
	lines := strings.Split(v, "\n")
	if len(lines) != 34 {
		t.Fatalf("%d lines", len(lines))
	}
	for sel, wantTitle := range map[string]string{"T2": "┌─ T2 ", "CHECK": "┌─ CHECK "} {
		h.m.sel = sel
		if !strings.Contains(h.view(), wantTitle) {
			t.Fatalf("inspector title for %s missing", sel)
		}
	}
}

func TestPickRowKeepsDescription(t *testing.T) {
	it := wsPickItem{kind: "role", name: "docs_writer", desc: "Writes user docs for everything", meta: "claude:very-long-model-name-for-testing", suggested: true}
	row := ansi.Strip(renderPickRow(it, false, 54, 14))
	if ansi.StringWidth(row) != 54 {
		t.Fatalf("row width %d", ansi.StringWidth(row))
	}
	if !strings.Contains(row, "Writes user") {
		t.Fatalf("description crowded out: %q", row)
	}
	row = ansi.Strip(renderPickRow(wsPickItem{kind: "role", name: "qa", desc: "Writes tests", meta: "deepseek-v4.1-flash"}, false, 66, 14))
	if !strings.Contains(row, "deepseek-v4.1-flash") || !strings.Contains(row, "Writes tests") {
		t.Fatalf("row = %q", row)
	}
}

func TestOverlayClearsBackground(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("n") // confirm overlay
	lines := strings.Split(h.view(), "\n")
	inBox := false
	for _, l := range lines {
		if strings.Contains(l, "┌─ Confirm") {
			inBox = true
		}
		if inBox {
			if i := strings.Index(l, "│"); i >= 0 {
				if strings.TrimSpace(l[:i]) != "" {
					t.Fatalf("background text left of the box: %q", l)
				}
			}
			if strings.TrimSpace(l) == "" {
				continue
			}
		}
		if inBox && strings.Contains(l, "└") {
			break
		}
	}
	for i, l := range lines {
		if strings.Contains(l, "┌─ Confirm") {
			if strings.TrimSpace(strings.Split(l, "┌")[0]) != "" || strings.TrimSpace(l[strings.LastIndex(l, "┐")+len("┐"):]) != "" {
				t.Fatalf("line %d has text around the box: %q", i, l)
			}
		}
	}
}

func TestWorkspaceOutputWaitingAndSanitizing(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "R")
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusRunning })
	h.m.outputs["T1"] = nil
	h.m.partial["T1"] = ""
	h.m.sel = "T1"
	h.key("o")
	h.want("waiting for coder's first output… (")
	h.m.appendOutput("T1", "\x1b[31mred\x1b[0m\tx\nsecond\x1b[")
	h.m.appendOutput("T1", "2Kline\r\n")
	lines := h.m.outputLines("T1")
	if len(lines) != 2 || lines[0] != "red    x" || lines[1] != "secondline" {
		t.Fatalf("lines = %q", lines)
	}
}

func TestWorkspaceOutputCaps(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	for i := 0; i < 3000; i++ {
		h.m.appendOutput("T1", fmt.Sprintf("line %d\n", i))
	}
	l := h.m.outputs["T1"]
	if len(l) != wsMaxOutput || l[len(l)-1] != "line 2999" {
		t.Fatalf("%d lines, last %q", len(l), l[len(l)-1])
	}
	big := strings.Repeat("x", 100<<10) + "\n"
	for i := 0; i < 30; i++ {
		h.m.appendOutput("T2", big)
	}
	total := 0
	for _, s := range h.m.outputs["T2"] {
		total += len(s) + 1
	}
	if total > wsMaxOutputBytes {
		t.Fatalf("stored %d bytes", total)
	}
}

func TestWsStreamLastLine(t *testing.T) {
	s := &wsStream{}
	s.add("first\nsecond\n")
	if got := s.lastLine(); got != "second" {
		t.Fatalf("got %q", got)
	}
	s.add("thi")
	s.add("rd")
	if got := s.lastLine(); got != "third" {
		t.Fatalf("got %q", got)
	}
	s.add("\n\n  \n")
	if got := s.lastLine(); got != "third" {
		t.Fatalf("got %q", got)
	}
	s.add(strings.Repeat("é", 20000))
	if len(s.cur) > wsStreamCur+4 {
		t.Fatalf("current line not capped: %d", len(s.cur))
	}
}

func TestCanvasWideCharacters(t *testing.T) {
	title := "修复登录 bug 🚀 for the login handler"
	g := &workspace.Graph{Nodes: []*workspace.Node{
		{ID: "T1", Title: title, Kind: workspace.KindWork, Status: workspace.StatusTodo, Role: "coder"},
		{ID: "T2", Title: title, Kind: workspace.KindWork, Status: workspace.StatusTodo, DependsOn: []string{"T1"}, SuggestedRole: "修复"},
	}}
	for _, sz := range [][2]int{{60, 12}, {120, 30}, {30, 6}} {
		out := plain(g, canvasOpts{width: sz[0], height: sz[1], selected: "T2", models: map[string]string{"coder": "模型🚀"}})
		lines := strings.Split(out, "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%d lines", len(lines))
		}
		for i, l := range lines {
			if ansi.StringWidth(l) != sz[0] {
				t.Fatalf("%v line %d is %d cells: %q", sz, i, ansi.StringWidth(l), l)
			}
		}
	}
}
