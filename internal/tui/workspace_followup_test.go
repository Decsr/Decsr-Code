package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// indep is a graph where T1 and T3 are needed by nobody, T2 by the todo T4.
func indepGraph() *workspace.Graph {
	g := wsFixtureGraph()
	g.Node("T2").DependsOn, g.Node("T3").DependsOn = nil, nil
	g.Node("T4").DependsOn = []string{"T2"}
	return g
}

func TestWorkspaceSplitGetsExistingIDsAndNext(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	if len(h.existing[0]) != 0 || h.nexts[0] != 0 {
		t.Fatalf("first split: %v %d", h.existing[0], h.nexts[0])
	}
	h.graph = wsSmallGraph
	h.key("tab")
	h.sendTask("more")
	if got := strings.Join(h.existing[1], ","); got != "T1,T2,T3,T4,T5,CHECK" || h.nexts[1] != 6 {
		t.Fatalf("follow-up split: %v next %d", h.existing[1], h.nexts[1])
	}
}

func TestWorkspaceCompactNoticeCountsKeptNodes(t *testing.T) {
	h := newWSHarness(t)
	h.graph = indepGraph
	h.toGraph()
	h.wsMarkDone("T1", "T2")
	h.key("tab")
	h.typeCmd("/compact")
	h.want("compacted 1 finished node; kept 1 that unfinished nodes still depend on")
	if h.m.g.Node("T2") == nil || h.m.g.Node("T1") != nil {
		t.Fatalf("graph: %+v", h.m.g.Nodes)
	}
	if h.summary == nil || !strings.Contains(h.summary(), "T1") {
		t.Fatal("workers do not get the session summary")
	}
}

func TestWorkspaceSingularSplitNotice(t *testing.T) {
	h := newWSHarness(t)
	h.graph = func() *workspace.Graph {
		return &workspace.Graph{Title: "One", Nodes: []*workspace.Node{{ID: "T1", Title: "Only", Kind: workspace.KindWork, Status: workspace.StatusTodo}}}
	}
	h.toGraph()
	h.want("split into 1 node: T1")
	h.wantNot("split into 1 nodes")
}

func TestWorkspaceProfileApplyRefusedWhileRunning(t *testing.T) {
	h := newWSHarness(t)
	if err := config.SaveProfile("cheap", map[string]config.AgentConfig{"coder": {Runner: "claude", Model: "haiku-x"}}); err != nil {
		t.Fatal(err)
	}
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	before := h.m.cfg.Agents["coder"].Model
	h.key("tab")
	h.typeCmd("/profile")
	h.key("enter")
	h.want("stop the running nodes first")
	if h.m.cfg.Agents["coder"].Model != before || h.m.cfg.Project.Profile != "" {
		t.Fatal("the profile was applied while nodes run")
	}
	if _, err := os.Stat(filepath.Join(h.root, ".decsr", "project.yaml")); err == nil {
		t.Fatal("project.yaml was written")
	}
	// Saving stays allowed.
	h.typeCmd("/profile")
	h.key("down", "enter")
	h.want("Save profile")
}

func TestWorkspaceSaveProfileKeepsOwnValuesOnly(t *testing.T) {
	h := newWSHarness(t)
	agents := map[string]config.AgentConfig{}
	for k, v := range h.m.cfg.Agents {
		agents[k] = v
	}
	agents["pm"] = config.AgentConfig{Runner: "claude", Model: "big"}
	agents["docs"] = config.AgentConfig{Description: "Docs.", ReadOnly: true, UserSkills: []string{"style"}}
	h.m.cfg.Agents = agents
	got := h.m.effectiveAgents()
	if d := got["docs"]; d.Runner != "" || d.Model != "" || d.Effort != "" || d.Description != "Docs." || !d.ReadOnly || len(d.UserSkills) != 1 {
		t.Errorf("custom role: %+v", d)
	}
	for _, name := range []string{"security", "ux_reviewer", "coder"} {
		if d := got[name]; d.Description != "" || d.ReadOnly {
			t.Errorf("built-in %s carries description/read_only: %+v", name, d)
		}
	}
	if got["pm"].Model != "big" {
		t.Errorf("pm = %+v", got["pm"])
	}
}

// ── palette Enter ────────────────────────────────────────────────────────────

func TestWorkspacePaletteEnterRule(t *testing.T) {
	typeOnly := func(h *wsHarness, s string) {
		for _, r := range s {
			h.key(string(r))
		}
	}
	// Several matches: nothing runs, the text stays.
	for _, pre := range []string{"/co", "/c"} {
		h := newWSHarness(t)
		typeOnly(h, pre)
		h.key("enter")
		h.want("! " + pre + " matches several commands: ")
		h.want("/compact")
		h.want("keep typing or press Tab")
		if h.m.input.Value() != pre || h.m.overlay != wsOverlayNone || h.m.showHelp {
			t.Fatalf("%s ran something (value %q)", pre, h.m.input.Value())
		}
	}
	h := newWSHarness(t)
	typeOnly(h, "/co")
	h.key("enter")
	h.want("! /co matches several commands: /compact, /config, /cost — keep typing or press Tab")

	// Exact name runs.
	h = newWSHarness(t)
	typeOnly(h, "/compact")
	h.key("enter")
	h.want("nothing to compact")

	// Unique prefix runs.
	h = newWSHarness(t)
	typeOnly(h, "/comp")
	h.key("enter")
	h.want("nothing to compact")

	// An arrow-selected highlight runs.
	h = newWSHarness(t)
	typeOnly(h, "/co")
	h.key("down", "enter") // /compact, /cost, /config: the second one is /cost
	if h.m.overlay != wsOverlayInfo || h.m.infoName != "Cost" {
		t.Fatalf("the highlighted /cost did not run:\n%s", h.view())
	}

	// Tab completes, then Enter runs the completed name.
	h = newWSHarness(t)
	typeOnly(h, "/co")
	h.key("tab")
	if h.m.input.Value() != "/compact" {
		t.Fatalf("tab gave %q", h.m.input.Value())
	}
	h.key("enter")
	h.want("nothing to compact")
}

// ── tasks that start with a slash ────────────────────────────────────────────

func TestWorkspaceSlashPathIsATask(t *testing.T) {
	h := newWSHarness(t)
	h.sendTask("/usr/lib is missing libfoo, fix the build")
	if got := h.tasks[len(h.tasks)-1]; got != "/usr/lib is missing libfoo, fix the build" {
		t.Fatalf("task = %q", got)
	}
	h2 := newWSHarness(t)
	h2.sendTask("//compact the logs")
	if got := h2.tasks[len(h2.tasks)-1]; got != "/compact the logs" {
		t.Fatalf("task = %q", got)
	}
	// A plain unknown command is still an error.
	h3 := newWSHarness(t)
	h3.m.input.SetValue("/nonesuch")
	h3.key("enter")
	h3.want("unknown command /nonesuch")
	if h3.m.splitting {
		t.Fatal("an unknown command became a task")
	}
}

// ── /project ─────────────────────────────────────────────────────────────────

func TestWorkspaceProjectPathKeepsSpaces(t *testing.T) {
	h := newWSHarness(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, " spaced ")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	h.typeCmd("/project")
	h.key("down", "enter")
	h.m.textIn.SetValue(dir)
	h.key("enter")
	if h.m.switchTo != dir {
		t.Fatalf("switchTo = %q, want %q (err %q)", h.m.switchTo, dir, h.m.textErr)
	}
}

func TestWorkspaceProjectRecentFileIsMissing(t *testing.T) {
	h := newWSHarness(t)
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.recent = []RecentProject{{Path: f, Name: "afile"}}
	h.typeCmd("/project")
	h.want("afile (missing)")
	h.key("enter")
	if h.m.switchTo != "" {
		t.Fatal("a file was selectable as a project")
	}
}

// ── inspector overlay on narrow terminals ────────────────────────────────────

func TestWorkspaceInspectorOverlayOnNarrow(t *testing.T) {
	for _, sz := range [][2]int{{60, 16}, {80, 24}, {99, 30}} {
		h := newWSHarness(t)
		h.size(sz[0], sz[1])
		h.toGraph()
		h.wantNot("INSPECTOR")
		base := h.view()
		h.key("i")
		v := h.view()
		if !strings.Contains(v, "GRAPH") {
			t.Fatalf("%v: the graph must stay the base view:\n%s", sz, v)
		}
		if v == base || !strings.Contains(v, "Data model") {
			t.Fatalf("%v: no inspector box:\n%s", sz, v)
		}
		checkFits(t, h, sz[0], sz[1])
		h.key("esc")
		if h.m.inspFlip || h.view() != base {
			t.Fatalf("%v: Esc did not close the inspector", sz)
		}
		h.key("i", "i")
		if h.m.inspFlip {
			t.Fatalf("%v: i did not close the inspector", sz)
		}
	}
	// Wide: i still hides the side inspector.
	h := newWSHarness(t)
	h.toGraph()
	h.want("T1")
	h.key("i")
	if !h.m.inspFlip {
		t.Fatal("i must toggle")
	}
}

func checkFits(t *testing.T, h *wsHarness, w, hh int) {
	t.Helper()
	lines := strings.Split(h.view(), "\n")
	if len(lines) > hh {
		t.Fatalf("%dx%d: view has %d lines", w, hh, len(lines))
	}
	for _, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			t.Fatalf("%dx%d: line is %d cells wide: %q", w, hh, n, l)
		}
	}
}

// ── bursts and pastes ────────────────────────────────────────────────────────

func TestWorkspaceBurstWithGraphFocusGoesToPrompt(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	if h.m.focus != wsFocusGraph {
		t.Fatal("setup")
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/project")})
	if h.m.focus != wsFocusPrompt || h.m.input.Value() != "/project" {
		t.Fatalf("focus %v value %q", h.m.focus, h.m.input.Value())
	}
	if h.m.screen != wsScreenMain || h.m.overlay != wsOverlayNone {
		t.Fatal("something else was triggered")
	}
	// A paste with graph focus, including keys that are graph commands.
	h2 := newWSHarness(t)
	h2.toGraph()
	h2.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R"), Paste: true})
	if h2.m.focus != wsFocusPrompt || h2.m.input.Value() != "R" {
		t.Fatalf("paste: focus %v value %q", h2.m.focus, h2.m.input.Value())
	}
}

func TestWorkspacePasteWithNewlinesIsOnlyText(t *testing.T) {
	h := newWSHarness(t)
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/help\nsecond line\r\nthird"), Paste: true})
	if h.m.splitting || h.m.showHelp {
		t.Fatal("a paste was sent or run")
	}
	if got := h.m.input.Value(); got != "/help\nsecond line\nthird" {
		t.Fatalf("value = %q", got)
	}
	// Without bracketed paste a multi-rune burst with a line break is text too.
	h2 := newWSHarness(t)
	h2.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix a\nfix b")})
	if h2.m.splitting || h2.m.input.Value() != "fix a\nfix b" {
		t.Fatalf("burst: splitting=%v value=%q", h2.m.splitting, h2.m.input.Value())
	}
}

// ── untrusted text ───────────────────────────────────────────────────────────

func TestWorkspaceViewHasNoInjectedEscapes(t *testing.T) {
	h := newWSHarness(t)
	evil := "x\x1b[2Jy\x1b]52;c;ZXZpbA==\x07z"
	h.graph = func() *workspace.Graph {
		g := wsFixtureGraph()
		g.Title = evil
		g.Nodes[0].Title = evil
		g.Nodes[0].Description = evil
		return g
	}
	h.toGraph()
	h.say(evil)
	for _, sz := range [][2]int{{60, 16}, {80, 24}, {120, 40}} {
		h.size(sz[0], sz[1])
		h.key("i")
		checkFits(t, h, sz[0], sz[1])
		v := h.m.View()
		for _, bad := range []string{"\x1b[2J", "\x1b]52", "\x07"} {
			if strings.Contains(v, bad) {
				t.Fatalf("%v: view contains %q", sz, bad)
			}
		}
	}
	h.want("xyz")
}

func (h *wsHarness) say(text string) {
	h.m.say("lead", text)
}
