package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

var feedT0 = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func feedTestNode(id string, status workspace.Status, started time.Time) *workspace.Node {
	return &workspace.Node{ID: id, Title: "Title " + id, Kind: workspace.KindWork, Role: "coder", Status: status, StartedAt: started}
}

func TestBuildFeedOrderAndMembership(t *testing.T) {
	lines := []workspace.Line{
		{Who: "you", Text: "task", At: feedT0},
		{Who: "lead", Text: "split", At: feedT0.Add(10 * time.Second)},
		{Who: "system", Text: "later", At: feedT0.Add(90 * time.Second)},
	}
	g := &workspace.Graph{Nodes: []*workspace.Node{
		feedTestNode("T2", workspace.StatusDone, feedT0.Add(60*time.Second)),
		feedTestNode("T1", workspace.StatusDone, feedT0.Add(30*time.Second)),
		feedTestNode("T3", workspace.StatusTodo, time.Time{}), // never started
	}}
	items := buildFeed(lines, g)
	var got []string
	for _, it := range items {
		if it.Kind == feedNode {
			got = append(got, "node:"+it.Node)
		} else {
			got = append(got, it.Who)
		}
	}
	want := []string{"you", "lead", "node:T1", "node:T2", "system"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if ids := feedNodeOrder(items); strings.Join(ids, ",") != "T1,T2" {
		t.Errorf("node order = %v", ids)
	}
}

func TestBuildFeedTiesKeepTranscriptFirstAndEmpty(t *testing.T) {
	g := &workspace.Graph{Nodes: []*workspace.Node{feedTestNode("T1", workspace.StatusRunning, feedT0)}}
	items := buildFeed([]workspace.Line{{Who: "lead", Text: "go", At: feedT0}}, g)
	if len(items) != 2 || items[0].Kind != feedMsg || items[1].Kind != feedNode {
		t.Fatalf("items = %+v", items)
	}
	if got := buildFeed(nil, nil); len(got) != 0 {
		t.Errorf("empty feed = %v", got)
	}
	if got := buildFeed(nil, &workspace.Graph{Nodes: []*workspace.Node{feedTestNode("T1", workspace.StatusTodo, time.Time{})}}); len(got) != 0 {
		t.Errorf("unstarted nodes must not be listed: %v", got)
	}
}

func TestFeedDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:                 "0s",
		37 * time.Second:             "37s",
		2 * time.Minute:              "2m",
		70 * time.Second:             "1m 10s",
		time.Hour + 5*time.Minute:    "1h 5m",
		26*time.Hour + 3*time.Minute: "26h 3m",
	} {
		if got := feedDuration(d); got != want {
			t.Errorf("feedDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRenderFeedMsgWrapsAndLabels(t *testing.T) {
	long := strings.Repeat("word ", 30) + strings.Repeat("x", 80)
	for _, who := range []string{"you", "lead", "system", "error"} {
		lines := renderFeedMsg(who, long, feedT0, 40)
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > 40 {
				t.Errorf("%s: line of %d cells: %q", who, w, ansi.Strip(l))
			}
		}
		if len(lines) < 4 {
			t.Errorf("%s: not wrapped: %d lines", who, len(lines))
		}
	}
	you := ansi.Strip(renderFeedMsg("you", "hi", feedT0, 40)[0])
	lead := ansi.Strip(renderFeedMsg("lead", "hi", feedT0, 40)[0])
	if !strings.HasPrefix(you, "You") || !strings.HasPrefix(lead, "Lead") || !strings.Contains(you, feedClock(feedT0)) {
		t.Errorf("labels: %q %q", you, lead)
	}
	if sys := ansi.Strip(renderFeedMsg("system", "note", feedT0, 40)[0]); !strings.HasPrefix(sys, "› note") {
		t.Errorf("system = %q", sys)
	}
}

// cardFixture builds a graph holding n with a CHECK node.
func cardFixture(n *workspace.Node) (*workspace.Graph, cardOpts) {
	g := &workspace.Graph{Nodes: []*workspace.Node{n}}
	return g, cardOpts{g: g, n: n, now: feedT0.Add(37 * time.Second), spin: "▖", runner: "claude", model: "claude:sonnet", out: nil}
}

func checkCardShape(t *testing.T, name string, lines []string, width int) {
	t.Helper()
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != width {
			t.Errorf("%s: line %d is %d cells, want %d: %q", name, i, w, width, ansi.Strip(l))
		}
		if strings.ContainsAny(l, "╔╗╚╝║═") {
			t.Errorf("%s: double border in %q", name, ansi.Strip(l))
		}
	}
	if len(lines) < 3 || !strings.HasPrefix(ansi.Strip(lines[0]), "╭") || !strings.HasPrefix(ansi.Strip(lines[len(lines)-1]), "╰") {
		t.Errorf("%s: not a rounded card:\n%s", name, ansi.Strip(strings.Join(lines, "\n")))
	}
}

func TestRenderNodeCardPerStatusFitsWidths(t *testing.T) {
	done := feedTestNode("T1", workspace.StatusDone, feedT0)
	done.EndedAt = feedT0.Add(70 * time.Second)
	done.Summary = "Flag added and wired to the version handler, with a long tail that wraps over several lines " + strings.Repeat("and keeps going ", 8)
	for i := 0; i < 8; i++ {
		done.FileStats = append(done.FileStats, workspace.FileStat{Path: fmt.Sprintf("internal/some/deep/package/dir/file_%d.go", i), Added: i + 1, Removed: i})
	}
	done.Warnings = []string{"w1", "w2"}
	failed := feedTestNode("T2", workspace.StatusFailed, feedT0)
	failed.EndedAt = feedT0.Add(2 * time.Minute)
	failed.Error = strings.Repeat("compile error: undefined thing ", 12)
	running := feedTestNode("T3", workspace.StatusRunning, feedT0)
	todo := feedTestNode("T4", workspace.StatusTodo, feedT0)
	check := &workspace.Node{ID: "CHECK", Title: "Build & test", Kind: workspace.KindCheck, Status: workspace.StatusDone,
		Commands: []string{"go build ./...", "go test ./...", "golangci-lint run", "extra"}, StartedAt: feedT0, EndedAt: feedT0.Add(12 * time.Second)}
	for _, n := range []*workspace.Node{done, failed, running, todo, check} {
		for _, width := range []int{60, 100, 140} {
			for _, expanded := range []bool{false, true} {
				g, o := cardFixture(n)
				o.width, o.expanded, o.iso = width, expanded, true
				o.out = []string{"line one " + strings.Repeat("long ", 40), "", "last line"}
				o.selected = n == done
				if n == check {
					g.Nodes = append(g.Nodes, check)
				}
				checkCardShape(t, fmt.Sprintf("%s/%d/%v", n.ID, width, expanded), renderNodeCard(o), width)
			}
		}
	}
}

func cardText(o cardOpts) string { return ansi.Strip(strings.Join(renderNodeCard(o), "\n")) }

func TestNodeCardContentByStatus(t *testing.T) {
	// running: spinner, live elapsed time and the last output line
	run := feedTestNode("T1", workspace.StatusRunning, feedT0)
	_, o := cardFixture(run)
	o.width = 100
	o.out = []string{"first", "", "compiling package x", "  "}
	txt := cardText(o)
	for _, want := range []string{"T1", "Title T1", "Working for 37s", "compiling package x", feedClock(feedT0)} {
		if !strings.Contains(txt, want) {
			t.Errorf("running card lacks %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "first") {
		t.Error("only the last output line belongs on a running card")
	}

	// done with stats: counts, Added for new files, +N more
	done := feedTestNode("T2", workspace.StatusDone, feedT0)
	done.EndedAt = feedT0.Add(70 * time.Second)
	done.Summary = "All done."
	done.FileStats = []workspace.FileStat{
		{Path: "cmd/version.go", Added: 18, Removed: 2},
		{Path: "cmd/new.go", Added: 5, New: true},
		{Path: "a.png", Binary: true},
		{Path: "d", Added: 1}, {Path: "e", Added: 1}, {Path: "f", Added: 1}, {Path: "g", Added: 1},
	}
	_, o = cardFixture(done)
	o.width = 100
	txt = cardText(o)
	for _, want := range []string{"Worked for 1m 10s", "All done.", "Edited cmd/version.go", "+18 −2", "Added cmd/new.go", "+5", "binary", "+2 more files"} {
		if !strings.Contains(txt, want) {
			t.Errorf("done card lacks %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "Edited f") {
		t.Errorf("more than %d file rows:\n%s", cardFileRows, txt)
	}
	// one hidden file reads "+1 more file"
	done.FileStats = done.FileStats[:cardFileRows+1]
	if txt = cardText(o); !strings.Contains(txt, "+1 more file") || strings.Contains(txt, "+1 more files") {
		t.Errorf("singular missing:\n%s", txt)
	}

	// done without stats: names only
	plain := feedTestNode("T3", workspace.StatusDone, feedT0)
	plain.EndedAt = feedT0.Add(5 * time.Second)
	plain.Files = []string{"x.go"}
	plain.Observed = []string{"y.go"}
	_, o = cardFixture(plain)
	o.width = 100
	txt = cardText(o)
	if !strings.Contains(txt, "Edited x.go") || !strings.Contains(txt, "Edited y.go") || strings.Contains(txt, "+") {
		t.Errorf("no-stats card:\n%s", txt)
	}

	// failed: reason, hint and the red border
	bad := feedTestNode("T4", workspace.StatusFailed, feedT0)
	bad.EndedAt = feedT0.Add(2 * time.Minute)
	bad.Error = "go test: boom"
	_, o = cardFixture(bad)
	o.width = 100
	txt = cardText(o)
	for _, want := range []string{"Failed after 2m", "go test: boom", "r retry · a change agent"} {
		if !strings.Contains(txt, want) {
			t.Errorf("failed card lacks %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "f add fix") {
		t.Error("f add fix belongs to the check node only")
	}
	stopped := feedTestNode("T5", workspace.StatusFailed, feedT0)
	stopped.EndedAt = feedT0.Add(time.Minute)
	stopped.Error = "stopped"
	_, o = cardFixture(stopped)
	o.width = 100
	if txt = cardText(o); !strings.Contains(txt, "Stopped after 1m") {
		t.Errorf("stopped card:\n%s", txt)
	}

	// warnings
	done.Warnings = []string{"a"}
	_, o = cardFixture(done)
	o.width = 100
	if txt = cardText(o); !strings.Contains(txt, "⚠ 1 warning") || strings.Contains(txt, "⚠ 1 warnings") {
		t.Errorf("warning line:\n%s", txt)
	}
	done.Warnings = []string{"a", "b", "c"}
	if txt = cardText(o); !strings.Contains(txt, "⚠ 3 warnings") {
		t.Errorf("warnings line:\n%s", txt)
	}
}

func TestNodeCardErrorTruncatedToThreeLines(t *testing.T) {
	bad := feedTestNode("T1", workspace.StatusFailed, feedT0)
	bad.EndedAt = feedT0.Add(time.Minute)
	bad.Error = strings.Repeat("alpha beta gamma ", 40)
	_, o := cardFixture(bad)
	o.width = 60
	txt := cardText(o)
	if !strings.Contains(txt, "…") {
		t.Errorf("error not cut:\n%s", txt)
	}
	// header + status + 3 error rows + hint + borders
	if n := len(strings.Split(txt, "\n")); n != 2+1+1+cardErrorLines+1 {
		t.Errorf("%d rows:\n%s", n, txt)
	}
}

func TestNodeCardCheckNode(t *testing.T) {
	check := &workspace.Node{ID: "CHECK", Title: "Build & test", Kind: workspace.KindCheck, Status: workspace.StatusDone,
		Commands: []string{"go build ./...", "go test ./..."}, StartedAt: feedT0, EndedAt: feedT0.Add(12 * time.Second), Summary: "all checks passed"}
	_, o := cardFixture(check)
	o.width, o.iso = 100, true
	txt := cardText(o)
	for _, want := range []string{"Build & test", "Passed in 12s", "$ go build ./...", "$ go test ./...", "/merge to apply"} {
		if !strings.Contains(txt, want) {
			t.Errorf("check card lacks %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "all checks passed") {
		t.Error("the check summary repeats Passed")
	}
	o.iso = false
	if txt = cardText(o); strings.Contains(txt, "/merge") {
		t.Errorf("/merge hint without isolation:\n%s", txt)
	}
	check.Status = workspace.StatusFailed
	check.Error = "FAIL: TestX"
	_, o = cardFixture(check)
	o.width = 100
	txt = cardText(o)
	if !strings.Contains(txt, "Failed after 12s") || !strings.Contains(txt, "r retry · f add fix") || strings.Contains(txt, "a change agent") {
		t.Errorf("failed check card:\n%s", txt)
	}
}

func TestNodeCardExpandedShowsLastFifteenLines(t *testing.T) {
	n := feedTestNode("T1", workspace.StatusDone, feedT0)
	n.EndedAt = feedT0.Add(time.Second)
	_, o := cardFixture(n)
	o.width, o.expanded = 100, true
	for i := 1; i <= 40; i++ {
		o.out = append(o.out, fmt.Sprintf("out%02d", i))
	}
	txt := cardText(o)
	if !strings.Contains(txt, "out40") || !strings.Contains(txt, "out26") || strings.Contains(txt, "out25") {
		t.Errorf("want the last %d lines:\n%s", cardOutputLines, txt)
	}
	o.expanded = false
	if txt = cardText(o); strings.Contains(txt, "out40") {
		t.Error("collapsed card shows output")
	}
}

func TestNodeCardBorderColours(t *testing.T) {
	seq := func(c string) string { return termenv.TrueColor.Color(c).Sequence(false) }
	forceTrueColor(t)
	failed := feedTestNode("T1", workspace.StatusFailed, feedT0)
	_, o := cardFixture(failed)
	o.width = 60
	if top := renderNodeCard(o)[0]; !strings.Contains(top, seq(ui.Fail.Dark)) {
		t.Errorf("failed border is not ui.Fail: %q", top)
	}
	o.selected = true
	if top := renderNodeCard(o)[0]; !strings.Contains(top, seq(ui.Accent.Dark)) {
		t.Errorf("selected border is not ui.Accent: %q", top)
	}
	o.selected = false
	o.n = feedTestNode("T2", workspace.StatusDone, feedT0)
	if top := renderNodeCard(o)[0]; !strings.Contains(top, seq(ui.Border.Dark)) {
		t.Errorf("plain border is not ui.Border: %q", top)
	}
}

func TestFileRowAlignsCountsRight(t *testing.T) {
	row := ansi.Strip(fileRow(workspace.FileStat{Path: "cmd/version.go", Added: 18, Removed: 2}, true, 40))
	if ansi.StringWidth(row) != 40 || !strings.HasSuffix(row, "+18 −2") || !strings.HasPrefix(row, "Edited cmd/version.go") {
		t.Errorf("row = %q", row)
	}
	long := ansi.Strip(fileRow(workspace.FileStat{Path: strings.Repeat("dir/", 20) + "file.go", Added: 1}, true, 40))
	if ansi.StringWidth(long) > 40 || !strings.Contains(long, "…") || !strings.Contains(long, "file.go") {
		t.Errorf("long path = %q", long)
	}
	if got := ansi.Strip(fileRow(workspace.FileStat{Path: "x.go", Removed: 4}, true, 30)); !strings.HasSuffix(got, "−4") || strings.Contains(got, "+0") {
		t.Errorf("only removals = %q", got)
	}
	if got := ansi.Strip(fileRow(workspace.FileStat{Path: "x.go"}, false, 30)); got != "Edited x.go" {
		t.Errorf("no stats = %q", got)
	}
}

func TestFeedWindow(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f"}
	if vis, top := feedWindow(lines, 3, 0, true); top != 3 || strings.Join(vis, "") != "def" {
		t.Errorf("follow: %v %d", vis, top)
	}
	if vis, top := feedWindow(lines, 3, 1, false); top != 1 || strings.Join(vis, "") != "bcd" {
		t.Errorf("top 1: %v %d", vis, top)
	}
	if vis, top := feedWindow(lines, 10, 4, false); top != 0 || len(vis) != 6 {
		t.Errorf("short feed: %v %d", vis, top)
	}
	if _, top := feedWindow(lines, 3, 99, false); top != 3 {
		t.Errorf("clamp: %d", top)
	}
}

// ── integration through the model ────────────────────────────────────────────

// feedHarness is a harness in feed mode with the fixture graph.
func feedHarness(t *testing.T) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.toGraph()
	h.m.showGraph = false
	return h
}

func TestFeedModeIsDefaultOfARealModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newWorkspaceModel(t.TempDir(), newWSHarness(t).m.cfg, wsDeps{})
	if m.showGraph || !m.feedFollow || m.sbSess != -1 {
		t.Fatalf("defaults: showGraph=%v follow=%v sbSess=%d", m.showGraph, m.feedFollow, m.sbSess)
	}
}

func TestFeedShowsConversationAndNoChatStrip(t *testing.T) {
	h := feedHarness(t)
	h.want("Activity · g graph")
	h.want("Build a todo CLI")
	h.want("split into 5 nodes")
	h.wantNot("you ›")
	h.wantNot("Inspector")
	h.wantNot("Graph · g activity")
	h.key("g")
	h.want("Graph · g activity")
	h.want("you › Build a todo CLI")
	h.want("Inspector")
	h.key("g")
	h.want("Activity · g graph")
}

func TestFeedEmptySessionShowsStartScreen(t *testing.T) {
	h := newWSHarness(t)
	h.m.showGraph = false
	h.want("No nodes yet")
	h.want("Activity · g graph")
}

func TestFeedGKeyIsTextInPrompt(t *testing.T) {
	h := feedHarness(t)
	h.key("c") // the prompt
	if h.m.focus != wsFocusPrompt {
		t.Fatalf("focus = %v", h.m.focus)
	}
	h.key("g", "b", "G")
	if h.m.input.Value() != "gbG" || h.m.showGraph || h.m.sbFlip {
		t.Fatalf("input %q showGraph=%v sbFlip=%v", h.m.input.Value(), h.m.showGraph, h.m.sbFlip)
	}
}

func TestFocusCycleWithAndWithoutSidebar(t *testing.T) {
	h := feedHarness(t) // 140 wide: the sidebar is inline
	if !h.m.sidebarInline() {
		t.Fatal("no sidebar at 140")
	}
	h.key("c") // prompt
	var seq []wsFocus
	for i := 0; i < 3; i++ {
		h.key("tab")
		seq = append(seq, h.m.focus)
		if i < 2 {
			continue
		}
	}
	want := []wsFocus{wsFocusSidebar, wsFocusGraph, wsFocusPrompt}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("forward cycle = %v, want %v", seq, want)
		}
	}
	h.key("shift+tab")
	if h.m.focus != wsFocusGraph {
		t.Fatalf("shift+tab from prompt = %v", h.m.focus)
	}
	h.key("shift+tab")
	if h.m.focus != wsFocusSidebar {
		t.Fatalf("shift+tab from main = %v", h.m.focus)
	}
	h.key("shift+tab")
	if h.m.focus != wsFocusPrompt {
		t.Fatalf("shift+tab from sidebar = %v", h.m.focus)
	}

	h.size(80, 24) // narrow: prompt <-> main only
	h.key("tab")
	if h.m.focus != wsFocusGraph {
		t.Fatalf("narrow tab = %v", h.m.focus)
	}
	h.key("tab")
	if h.m.focus != wsFocusPrompt {
		t.Fatalf("narrow tab 2 = %v", h.m.focus)
	}
}

func TestSidebarToggleWideAndNarrow(t *testing.T) {
	h := feedHarness(t)
	h.key("b")
	if h.m.sidebarVisible() || h.m.mainWidth() != h.m.width {
		t.Fatal("b must hide the inline sidebar")
	}
	h.key("b")
	if !h.m.sidebarInline() || h.m.focus != wsFocusSidebar {
		t.Fatal("b must show it again and focus it")
	}

	h.size(80, 24)
	h.key("tab", "tab") // main focus
	h.key("b")
	if !h.m.sidebarOverlay() || h.m.focus != wsFocusSidebar {
		t.Fatalf("narrow b: overlay=%v focus=%v", h.m.sidebarOverlay(), h.m.focus)
	}
	v := h.view()
	if !strings.Contains(v, "╭─ Nodes") || !strings.Contains(v, "T1 Data model") {
		t.Fatalf("no sidebar box:\n%s", v)
	}
	checkFits(t, h, 80, 24)
	h.key("esc")
	if h.m.sidebarOverlay() || h.m.focus != wsFocusGraph {
		t.Fatal("Esc must close the box and return to main")
	}
	h.key("b", "b")
	if h.m.sidebarOverlay() {
		t.Fatal("b must close the box")
	}
	h.key("b", "enter") // Enter opens the node output and closes the box
	if h.m.screen != wsScreenOutput || h.m.sidebarOverlay() {
		t.Fatalf("enter: screen=%v overlay=%v", h.m.screen, h.m.sidebarOverlay())
	}
}

func TestSidebarSelectionIsSharedWithFeedAndGraph(t *testing.T) {
	h := feedHarness(t)
	h.key("A", "r") // runs T1 so it has a card
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusDone })
	h.key("tab") // main -> prompt
	h.key("tab") // prompt -> sidebar
	if h.m.focus != wsFocusSidebar {
		t.Fatalf("focus = %v", h.m.focus)
	}
	h.key("j")
	if h.m.sel != "T2" {
		t.Fatalf("sel = %s after j", h.m.sel)
	}
	h.key("down", "down")
	if h.m.sel != "T4" {
		t.Fatalf("sel = %s", h.m.sel)
	}
	h.key("k")
	if h.m.sel != "T3" {
		t.Fatalf("sel = %s after k", h.m.sel)
	}
	h.key("g")
	if !h.m.showGraph || h.m.sel != "T3" {
		t.Fatal("g in the sidebar must switch the main pane and keep the selection")
	}
	h.key("tab") // main
	h.key("right")
	if h.m.sel == "T3" {
		t.Fatal("graph navigation did not move the selection")
	}
}

func TestSidebarEnterOpensOutput(t *testing.T) {
	h := feedHarness(t)
	h.key("tab", "tab") // main -> prompt -> sidebar
	h.key("enter")
	if h.m.screen != wsScreenOutput || h.m.outNode != h.m.sel {
		t.Fatalf("screen=%v outNode=%q sel=%q", h.m.screen, h.m.outNode, h.m.sel)
	}
}

func TestSidebarRowsContent(t *testing.T) {
	h := feedHarness(t)
	h.m.now = func() time.Time { return feedT0.Add(3 * 24 * time.Hour) }
	g := h.m.g
	g.Node("T1").Status, g.Node("T1").Role = workspace.StatusDone, "coder"
	g.Node("T1").EndedAt = feedT0.Add(3*24*time.Hour - 2*time.Minute)
	g.Node("T2").Status, g.Node("T2").Role = workspace.StatusRunning, "coder"
	g.Node("T3").Status, g.Node("T3").Role = workspace.StatusFailed, "qa"
	g.Node("T3").EndedAt = feedT0
	rows := ansi.Strip(strings.Join(h.m.sidebarRows(30, 30), "\n"))
	for _, want := range []string{"Todo CLI", "1/6", "1 failed", "✓ T1 Data model", "2m", "T2 Write docs", "now", "! T3", "failed", "→ coder", "→ security", "CHECK Build & test", "checks"} {
		if !strings.Contains(rows, want) {
			t.Errorf("sidebar lacks %q:\n%s", want, rows)
		}
	}
	for i, l := range strings.Split(rows, "\n") {
		if ansi.StringWidth(l) > 30 {
			t.Errorf("row %d is %d cells", i, ansi.StringWidth(l))
		}
	}
}

func TestSbAge(t *testing.T) {
	now := feedT0.Add(100 * time.Hour)
	for d, want := range map[time.Duration]string{
		-time.Minute:       "now",
		30 * time.Second:   "now",
		2 * time.Minute:    "2m",
		59 * time.Minute:   "59m",
		time.Hour:          "1h",
		23 * time.Hour:     "23h",
		24 * time.Hour:     "1d",
		3*24*time.Hour + 5: "3d",
	} {
		if got := sbAge(now, now.Add(-d)); got != want {
			t.Errorf("sbAge(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSidebarTruncatesLongTitles(t *testing.T) {
	h := feedHarness(t)
	h.m.g.Node("T1").Title = strings.Repeat("very long title ", 10)
	h.m.sess.update(func(s *workspace.Session) { s.Title = strings.Repeat("session ", 20) })
	for _, w := range []int{28, 34} {
		for i, l := range h.m.sidebarRows(w, 20) {
			if ansi.StringWidth(l) > w {
				t.Errorf("w=%d row %d is %d cells: %q", w, i, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
	if w := h.m.sidebarW(); w < 28 || w > 34 {
		t.Errorf("sidebarW = %d", w)
	}
	h.size(300, 40)
	if w := h.m.sidebarW(); w != 34 {
		t.Errorf("sidebarW at 300 = %d", w)
	}
}

func TestSidebarSelectedRowHasBackgroundAcrossWidth(t *testing.T) {
	forceTrueColor(t)
	bg := termenv.TrueColor.Color(ui.Sel.Dark).Sequence(true)
	h := feedHarness(t)
	rows := h.m.sidebarRows(30, 20)
	// rows 0-1 are the title; T1 (selected) takes rows 2-3, T2 rows 4-5
	for i := 2; i <= 3; i++ {
		if !strings.Contains(rows[i], bg) {
			t.Errorf("selected row %d has no background: %q", i, rows[i])
		}
		if ansi.StringWidth(rows[i]) != 30 {
			t.Errorf("selected row %d is %d cells", i, ansi.StringWidth(rows[i]))
		}
	}
	for i := 4; i <= 5; i++ {
		if strings.Contains(rows[i], bg) {
			t.Errorf("unselected row %d has a background", i)
		}
	}
}

func TestSidebarSessionsListAndResume(t *testing.T) {
	h := feedHarness(t)
	for i, title := range []string{"Older job", "Other job"} {
		s := workspace.NewSession()
		s.Title = title
		s.UpdatedAt = feedT0.Add(time.Duration(i) * time.Hour)
		s.Graph = &workspace.Graph{Nodes: []*workspace.Node{
			{ID: "T1", Title: "x", Kind: workspace.KindWork, Status: workspace.StatusDone},
			{ID: "T2", Title: "y", Kind: workspace.KindWork, Status: workspace.StatusTodo},
		}}
		if err := s.Save(h.root); err != nil {
			t.Fatal(err)
		}
	}
	h.m.refreshSaved()
	h.m.now = time.Now
	rows := ansi.Strip(strings.Join(h.m.sidebarRows(32, 30), "\n"))
	if !strings.Contains(rows, "Sessions") || !strings.Contains(rows, "○ Older job") || !strings.Contains(rows, "○ Other job") || !strings.Contains(rows, "1/2") {
		t.Fatalf("sessions block:\n%s", rows)
	}
	if strings.Count(rows, "Todo CLI") != 1 {
		t.Errorf("the open session must not be listed twice:\n%s", rows)
	}
	// at most five
	for i := 0; i < 8; i++ {
		s := workspace.NewSession()
		s.Title = fmt.Sprintf("extra %d", i)
		s.AddLine("you", "x")
		if err := s.Save(h.root); err != nil {
			t.Fatal(err)
		}
	}
	h.m.refreshSaved()
	if n := len(h.m.otherSessions()); n != wsSidebarSessions {
		t.Errorf("%d other sessions, want %d", n, wsSidebarSessions)
	}

	// walk down past the last node into the sessions, then Enter resumes
	h.key("tab", "tab") // sidebar
	for i := 0; i < len(h.m.g.Nodes)+1; i++ {
		h.key("j")
	}
	if h.m.sbSess < 0 {
		t.Fatalf("cursor stayed on the nodes (sel %s)", h.m.sel)
	}
	h.want("Enter resume")
	id := h.m.otherSessions()[h.m.sbSess].ID
	h.key("enter")
	if h.m.sess.snapshot().ID != id {
		t.Fatalf("session = %s, want %s", h.m.sess.snapshot().ID, id)
	}
	if h.m.sbSess != -1 {
		t.Error("the cursor must leave the sessions")
	}
}

func TestSidebarSessionEnterRefusedWhileRunning(t *testing.T) {
	h := feedHarness(t)
	s := workspace.NewSession()
	s.Title = "Elsewhere"
	s.AddLine("you", "x")
	if err := s.Save(h.root); err != nil {
		t.Fatal(err)
	}
	h.m.refreshSaved()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusRunning })
	h.key("tab", "tab")
	for i := 0; i < len(h.m.g.Nodes)+1; i++ {
		h.key("j")
	}
	before := h.m.sess.snapshot().ID
	h.key("enter")
	if h.m.sess.snapshot().ID != before {
		t.Fatal("a session was loaded while a node runs")
	}
	h.exec.release("T1", nil)
}

func TestFeedKeysSpaceAndCardNavigation(t *testing.T) {
	h := feedHarness(t)
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	h.want("Working for")
	h.want("working on T1")
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusDone })
	h.m.sel = "T3"
	h.key("r") // T3 runs next
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T3"]) > 0 })
	h.exec.release("T3", nil)
	h.pump(func(m workspaceModel) bool { return m.status("T3") == workspace.StatusDone })
	h.want("Worked for")
	h.wantNot("working on T1") // collapsed

	h.m.sel = "T1"
	h.key("space")
	if !h.m.feedExpanded["T1"] {
		t.Fatal("space did not expand the card")
	}
	h.want("working on T1")
	h.key("space")
	if h.m.feedExpanded["T1"] {
		t.Fatal("space did not collapse the card")
	}

	h.key("down")
	if h.m.sel != "T3" {
		t.Fatalf("down selected %s", h.m.sel)
	}
	h.key("down") // last card: stays
	if h.m.sel != "T3" {
		t.Fatalf("down past the last card selected %s", h.m.sel)
	}
	h.key("up")
	if h.m.sel != "T1" {
		t.Fatalf("up selected %s", h.m.sel)
	}
	// o still opens the output
	h.key("o")
	if h.m.screen != wsScreenOutput || h.m.outNode != "T1" {
		t.Fatal("o did not open the output view")
	}
}

func TestFeedSpaceIgnoredInGraphMode(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("space")
	if len(h.m.feedExpanded) != 0 {
		t.Fatal("space expanded a card in graph mode")
	}
}

func TestFeedScrollFollowAndJumpToBottom(t *testing.T) {
	h := newWSHarness(t)
	h.size(100, 24)
	h.toGraph()
	h.m.showGraph = false
	for i := 0; i < 30; i++ {
		h.m.say("system", fmt.Sprintf("message number %d", i))
	}
	h.want("message number 29")
	if !h.m.feedFollow {
		t.Fatal("not following at the start")
	}
	h.key("pgup")
	if h.m.feedFollow {
		t.Fatal("PgUp must stop following")
	}
	top := h.m.feedTop
	h.want("Activity · g graph")
	h.want("G latest ↓")
	h.m.say("system", "a new line arrives")
	if h.m.feedTop != top || h.m.feedFollow {
		t.Fatal("a new line moved a feed that was scrolled up")
	}
	h.wantNot("a new line arrives")
	h.key("tab") // sidebar? narrow 100: sidebar inline -> focus moves to sidebar
	h.key("tab") // main
	h.key("G")
	if !h.m.feedFollow {
		t.Fatal("G must jump to the newest")
	}
	h.want("a new line arrives")
	h.key("pgup", "pgdown", "pgdown", "pgdown", "pgdown", "pgdown", "pgdown")
	if !h.m.feedFollow {
		t.Fatal("scrolling to the bottom must resume following")
	}
}

func TestFeedScrollKeysFromPromptAndYouJumpsToEnd(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.showGraph = false
	for i := 0; i < 40; i++ {
		h.m.say("system", fmt.Sprintf("line %d", i))
	}
	h.key("c", "pgup")
	if h.m.feedFollow {
		t.Fatal("PgUp in the prompt did not scroll the feed")
	}
	h.m.say("you", "my reply")
	if !h.m.feedFollow {
		t.Fatal("sending a message must follow the bottom")
	}
}

func TestFeedRevealScrollsSelectedCardIntoView(t *testing.T) {
	h := newWSHarness(t)
	h.size(100, 24)
	h.toGraph()
	h.m.showGraph = false
	h.key("A")
	now := time.Now()
	for i, id := range []string{"T1", "T2", "T3", "T4", "T5"} {
		n := h.m.g.Node(id)
		n.Status = workspace.StatusDone
		n.StartedAt = now.Add(time.Duration(i) * time.Second)
		n.EndedAt = n.StartedAt.Add(time.Second)
		n.Summary = "summary of " + id
	}
	h.m.sel = "T5"
	h.m = h.m.feedReveal("T5")
	h.want("summary of T5")
	h.m = h.m.feedSelect(-1)
	h.m = h.m.feedSelect(-1)
	h.m = h.m.feedSelect(-1)
	h.m = h.m.feedSelect(-1)
	if h.m.sel != "T1" {
		t.Fatalf("sel = %s", h.m.sel)
	}
	h.want("summary of T1")
	if h.m.feedFollow {
		t.Fatal("revealing an earlier card must stop following")
	}
	h.m = h.m.feedSelect(1)
	h.m = h.m.feedSelect(1)
	h.m = h.m.feedSelect(1)
	h.m = h.m.feedSelect(1)
	h.want("summary of T5")
	if !h.m.feedFollow {
		t.Fatal("the last card is the bottom: follow again")
	}
}

func TestFeedLayoutHasNoLineWiderThanPane(t *testing.T) {
	for _, sz := range [][2]int{{60, 16}, {80, 24}, {100, 30}, {160, 46}} {
		h := newWSHarness(t)
		h.size(sz[0], sz[1])
		h.toGraph()
		h.m.showGraph = false
		h.m.say("lead", strings.Repeat("long text ", 40)+strings.Repeat("y", 200))
		h.key("A", "r")
		h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
		v := h.view()
		for i, l := range strings.Split(v, "\n") {
			if ansi.StringWidth(l) > sz[0] {
				t.Fatalf("%v: line %d is %d cells", sz, i, ansi.StringWidth(l))
			}
		}
		if n := len(strings.Split(v, "\n")); n > sz[1] {
			t.Fatalf("%v: %d rows", sz, n)
		}
		for _, l := range strings.Split(v, "\n") {
			if strings.ContainsAny(l, "╔╗╚╝║═") {
				t.Fatalf("%v: double border: %q", sz, l)
			}
		}
		h.exec.release("T1", nil)
	}
}

func TestInspectorOverlayOnFeed(t *testing.T) {
	h := feedHarness(t)
	h.key("i")
	if !h.m.inspFlip {
		t.Fatal("i did not open the inspector")
	}
	v := h.view()
	if !strings.Contains(v, "╭─ T1") || !strings.Contains(v, "Data model") {
		t.Fatalf("no inspector box over the feed:\n%s", v)
	}
	h.key("esc")
	if h.m.inspFlip {
		t.Fatal("Esc did not close the inspector")
	}
}

func TestCompactLeavesSummaryLineInFeed(t *testing.T) {
	h := feedHarness(t)
	err := h.eng.Replace(func(g *workspace.Graph) {
		for _, n := range g.Nodes {
			if n.Kind == workspace.KindWork {
				n.Status, n.Role = workspace.StatusDone, "coder"
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.typeCmd("/compact")
	h.want("compacted")
}

func TestSidebarScrollsToKeepSelectionVisible(t *testing.T) {
	h := feedHarness(t)
	var nodes []*workspace.Node
	for i := 1; i <= 20; i++ {
		nodes = append(nodes, &workspace.Node{ID: fmt.Sprintf("N%d", i), Title: fmt.Sprintf("Node %d", i), Kind: workspace.KindWork, Status: workspace.StatusTodo})
	}
	h.m.g = &workspace.Graph{Nodes: nodes}
	for _, sel := range []string{"N1", "N10", "N20"} {
		h.m.sel = sel
		rows := h.m.sidebarRows(30, 16)
		if len(rows) != 16 {
			t.Fatalf("%d rows, want 16", len(rows))
		}
		if !strings.Contains(ansi.Strip(strings.Join(rows, "\n")), " "+sel+" ") {
			t.Errorf("%s is not visible:\n%s", sel, ansi.Strip(strings.Join(rows, "\n")))
		}
	}
}
