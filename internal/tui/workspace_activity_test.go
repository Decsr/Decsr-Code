package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func actI(i int) *int { return &i }

func mkAct(id string, kind runner.ActivityKind, st runner.ActivityState, title string) runner.Activity {
	return runner.Activity{ID: id, Kind: kind, State: st, Title: title, At: feedT0}
}

func actPlain(s string) string { return ansi.Strip(s) }

func TestActivityLineFormats(t *testing.T) {
	now := feedT0.Add(12 * time.Second)
	cmdRun := mkAct("1", runner.ActCommand, runner.ActRunning, "go test ./...")
	cmdDone := mkAct("2", runner.ActCommand, runner.ActDone, "go test ./...")
	cmdDone.Duration = 3200 * time.Millisecond
	cmdFail := mkAct("3", runner.ActCommand, runner.ActFailed, "go test ./...")
	cmdFail.Duration, cmdFail.ExitCode = 3200*time.Millisecond, actI(1)
	edit := mkAct("4", runner.ActEdit, runner.ActDone, "cmd/version.go")
	edit.Added, edit.Removed = 18, 2
	add := mkAct("5", runner.ActWrite, runner.ActDone, "cmd/x.go")
	add.Added = 40
	noCounts := mkAct("6", runner.ActEdit, runner.ActDone, "cmd/y.go")
	think := mkAct("7", runner.ActThinking, runner.ActRunning, "thinking")
	think.Detail = "I should look at the flag parsing first and then the version command"
	msg := mkAct("8", runner.ActMessage, runner.ActDone, "All done here")
	cases := []struct {
		name string
		a    runner.Activity
		want []string
	}{
		{"running command", cmdRun, []string{"▸ go test ./...", "12s"}},
		{"done command", cmdDone, []string{"▸ go test ./...", "✓ 3.2s"}},
		{"failed command", cmdFail, []string{"▸ go test ./...", "✗ exit 1 · 3.2s"}},
		{"edit", edit, []string{"✎ Edited cmd/version.go", "+18 −2"}},
		{"write", add, []string{"✎ Added cmd/x.go", "+40"}},
		{"edit without counts", noCounts, []string{"✎ Edited cmd/y.go"}},
		{"read", mkAct("9", runner.ActRead, runner.ActDone, "cmd/version.go"), []string{"◦ Read cmd/version.go"}},
		{"search", mkAct("10", runner.ActSearch, runner.ActDone, "flag.Parse"), []string{`⌕ Searched "flag.Parse"`}},
		{"tool", mkAct("11", runner.ActTool, runner.ActDone, "TodoWrite"), []string{"⚙ TodoWrite"}},
		{"waiting", mkAct("12", runner.ActWaiting, runner.ActRunning, "rate limited"), []string{"◷ Waiting · rate limited"}},
		{"error", mkAct("13", runner.ActError, runner.ActFailed, "boom"), []string{"! boom"}},
		{"thinking", think, []string{`· thinking… "I should look at the flag parsing first`}},
		{"message", msg, []string{"› All done here"}},
	}
	for _, c := range cases {
		for _, w := range []int{40, 80} {
			got := actPlain(activityLine(c.a, now, w))
			if ansi.StringWidth(got) > w {
				t.Errorf("%s @%d: %d cells: %q", c.name, w, ansi.StringWidth(got), got)
			}
			for _, sub := range c.want {
				if w == 40 && c.name == "thinking" {
					continue // cut at the width
				}
				if !strings.Contains(got, sub) {
					t.Errorf("%s @%d: %q lacks %q", c.name, w, got, sub)
				}
			}
		}
	}
	// the meta sits on the right edge
	got := actPlain(activityLine(edit, now, 40))
	if !strings.HasSuffix(got, "+18 −2") || ansi.StringWidth(got) != 40 {
		t.Errorf("meta not right aligned: %q", got)
	}
	if got := actPlain(activityLine(noCounts, now, 40)); strings.Contains(got, "+") {
		t.Errorf("zero counts shown: %q", got)
	}
}

func TestActivityLineColours(t *testing.T) {
	run := mkAct("1", runner.ActCommand, runner.ActRunning, "x")
	if actColor(run) != ui.Accent {
		t.Error("running is not accent")
	}
	if actColor(mkAct("2", runner.ActCommand, runner.ActFailed, "x")) != ui.Fail {
		t.Error("failed is not red")
	}
	if actColor(mkAct("3", runner.ActEdit, runner.ActDone, "x")) != ui.Text {
		t.Error("done is not normal")
	}
}

func TestActivityPathTruncation(t *testing.T) {
	p := "internal/workspace/something/very/long/handler.go"
	got := midPath(p, 24)
	if !strings.HasPrefix(got, "…/") || !strings.HasSuffix(got, "handler.go") || ansi.StringWidth(got) > 24 {
		t.Errorf("midPath = %q", got)
	}
	if midPath("a.go", 24) != "a.go" {
		t.Error("short path changed")
	}
	a := mkAct("1", runner.ActEdit, runner.ActDone, p)
	a.Added = 5
	line := actPlain(activityLine(a, feedT0, 40))
	if ansi.StringWidth(line) > 40 || !strings.Contains(line, "handler.go") || !strings.Contains(line, "+5") {
		t.Errorf("line = %q", line)
	}
	// a title that is not a path is cut at the end
	c := mkAct("2", runner.ActCommand, runner.ActDone, strings.Repeat("word ", 30))
	if l := actPlain(activityLine(c, feedT0, 40)); !strings.Contains(l, "…") || ansi.StringWidth(l) > 40 {
		t.Errorf("command line = %q", l)
	}
}

func TestCtxLabel(t *testing.T) {
	n := &workspace.Node{ContextTokens: 18200}
	if got := ctxLabel(n); got != "ctx 18k" {
		t.Errorf("got %q", got)
	}
	n.ContextLimit = 128000
	if got := ctxLabel(n); got != "ctx 18k / 128k" {
		t.Errorf("got %q", got)
	}
	if got := ctxLabel(&workspace.Node{}); got != "" {
		t.Errorf("unknown size shows %q", got)
	}
}

func actNode(status workspace.Status, n int) *workspace.Node {
	nd := feedTestNode("T1", status, feedT0)
	for i := 0; i < n; i++ {
		nd.Activity = append(nd.Activity, mkAct(fmt.Sprintf("a%d", i), runner.ActRead, runner.ActDone, fmt.Sprintf("f%d.go", i)))
	}
	return nd
}

func actCardText(o cardOpts) string {
	return actPlain(strings.Join(renderNodeCard(o), "\n"))
}

func TestRunningCardShowsLastFiveSteps(t *testing.T) {
	n := actNode(workspace.StatusRunning, 8)
	n.ContextTokens = 18000
	n.Activity[7] = mkAct("a7", runner.ActCommand, runner.ActRunning, "go test ./...")
	g := &workspace.Graph{Nodes: []*workspace.Node{n}}
	o := cardOpts{g: g, n: n, width: 60, now: feedT0.Add(37 * time.Second), out: []string{"raw line"}}
	got := actCardText(o)
	if !strings.Contains(got, "Working for 37s · ctx 18k") {
		t.Errorf("duration line:\n%s", got)
	}
	for _, want := range []string{"f3.go", "f4.go", "f5.go", "f6.go", "go test ./..."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "f2.go") || strings.Contains(got, "raw line") {
		t.Errorf("more than the last five steps, or output shown:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if ansi.StringWidth(l) > 60 {
			t.Errorf("line wider than the card: %q", l)
		}
	}
}

func TestRunningCardFallsBackToOutput(t *testing.T) {
	n := actNode(workspace.StatusRunning, 0)
	g := &workspace.Graph{Nodes: []*workspace.Node{n}}
	got := actCardText(cardOpts{g: g, n: n, width: 60, now: feedT0.Add(5 * time.Second), out: []string{"first", "compiling…"}})
	if !strings.Contains(got, "compiling…") || strings.Contains(got, "ctx") {
		t.Errorf("fallback:\n%s", got)
	}
}

func TestDoneCardFoldsAndExpands(t *testing.T) {
	n := actNode(workspace.StatusDone, 6)
	n.EndedAt = feedT0.Add(72 * time.Second)
	n.Summary = "all good"
	n.ContextTokens, n.ContextLimit = 18000, 128000
	n.ActivityDropped = 37
	n.Activity[2].Kind = runner.ActThinking
	n.Activity[2].Detail = "thinking about the problem at length"
	g := &workspace.Graph{Nodes: []*workspace.Node{n}}
	o := cardOpts{g: g, n: n, width: 60, now: feedT0.Add(time.Hour)}
	folded := actCardText(o)
	if !strings.Contains(folded, "Worked for 1m 12s · ctx 18k / 128k ›") || !strings.Contains(folded, "all good") {
		t.Errorf("folded:\n%s", folded)
	}
	if strings.Contains(folded, "f1.go") {
		t.Errorf("steps shown while folded:\n%s", folded)
	}
	o.expanded = true
	exp := actCardText(o)
	if !strings.Contains(exp, "⌄") || !strings.Contains(exp, "… 37 earlier steps") || !strings.Contains(exp, "f5.go") || !strings.Contains(exp, "thinking about the problem") {
		t.Errorf("expanded:\n%s", exp)
	}
	for _, l := range strings.Split(exp, "\n") {
		if ansi.StringWidth(l) > 60 {
			t.Errorf("line wider than the card: %q", l)
		}
	}
	// without activity (an old session) the output tail stays
	old := actNode(workspace.StatusDone, 0)
	old.EndedAt = feedT0.Add(10 * time.Second)
	got := actCardText(cardOpts{g: &workspace.Graph{Nodes: []*workspace.Node{old}}, n: old, width: 60, expanded: true, out: []string{"old output"}})
	if !strings.Contains(got, "old output") || strings.Contains(got, "earlier steps") {
		t.Errorf("old session:\n%s", got)
	}
}

func TestStallText(t *testing.T) {
	n := actNode(workspace.StatusRunning, 0)
	n.LastActivityAt = feedT0
	cases := []struct {
		d    time.Duration
		want string
	}{
		{59 * time.Second, ""},
		{60 * time.Second, "no activity for 1m"},
		{119 * time.Second, "no activity for 1m"},
		{2 * time.Minute, "no activity for 2m"},
	}
	for _, c := range cases {
		if got := stallText(n, feedT0.Add(c.d)); got != c.want {
			t.Errorf("%s: %q, want %q", c.d, got, c.want)
		}
	}
	// without LastActivityAt the start counts
	n.LastActivityAt = time.Time{}
	if got := stallText(n, feedT0.Add(61*time.Second)); got != "no activity for 1m" {
		t.Errorf("fallback to StartedAt: %q", got)
	}
	n.Status = workspace.StatusDone
	if stallText(n, feedT0.Add(time.Hour)) != "" {
		t.Error("a finished node is stalled")
	}
}

func TestStallOnCardAndSidebar(t *testing.T) {
	h := newWSHarness(t)
	n := actNode(workspace.StatusRunning, 3)
	n.LastActivityAt = feedT0
	h.m.g = &workspace.Graph{Nodes: []*workspace.Node{n}}
	h.m.sel = "T1"
	h.m.now = func() time.Time { return feedT0.Add(30 * time.Second) }
	row := actPlain(h.m.nodeRows(n, 30, false, map[string]string{}, map[string]string{})[1])
	if !strings.Contains(row, "◦ f2.go") || strings.Contains(row, "no activity") {
		t.Errorf("current action row = %q", row)
	}
	h.m.now = func() time.Time { return feedT0.Add(2 * time.Minute) }
	row = actPlain(h.m.nodeRows(n, 30, false, map[string]string{}, map[string]string{})[1])
	if !strings.Contains(row, "no activity for 2m") {
		t.Errorf("stall row = %q", row)
	}
	card := actCardText(cardOpts{g: h.m.g, n: n, width: 70, now: h.m.now()})
	if !strings.Contains(card, "no activity for 2m") {
		t.Errorf("card:\n%s", card)
	}
}

func TestSidebarCurrentAction(t *testing.T) {
	h := newWSHarness(t)
	n := actNode(workspace.StatusRunning, 2)
	n.LastActivityAt = feedT0
	n.Activity = append(n.Activity,
		mkAct("c", runner.ActCommand, runner.ActRunning, "go test ./..."),
		mkAct("d", runner.ActRead, runner.ActDone, "x.go"))
	h.m.g = &workspace.Graph{Nodes: []*workspace.Node{n}}
	h.m.now = func() time.Time { return feedT0.Add(5 * time.Second) }
	row := func() string {
		return actPlain(h.m.nodeRows(n, 32, false, map[string]string{}, map[string]string{})[1])
	}
	if r := row(); !strings.Contains(r, "▸ go test ./...") || strings.Contains(r, "now") {
		t.Errorf("running item first: %q", r)
	}
	n.Activity[2].State = runner.ActDone
	if r := row(); !strings.Contains(r, "◦ x.go") {
		t.Errorf("latest item: %q", r)
	}
	n.Activity = append(n.Activity, mkAct("t", runner.ActThinking, runner.ActRunning, "thinking"))
	if r := row(); !strings.Contains(r, "· thinking…") {
		t.Errorf("thinking: %q", r)
	}
	e := mkAct("e", runner.ActEdit, runner.ActRunning, "cmd/version.go")
	n.Activity = []runner.Activity{e}
	if r := row(); !strings.Contains(r, "✎ cmd/version.go") {
		t.Errorf("edit: %q", r)
	}
	// no activity yet: the age stays
	n.Activity = nil
	if r := row(); !strings.Contains(r, "now") {
		t.Errorf("no activity: %q", r)
	}
}

func TestStallNotifiesOncePerEpisode(t *testing.T) {
	h, notes := autoHarness(t, 0)
	n := actNode(workspace.StatusRunning, 1)
	n.LastActivityAt = feedT0
	h.m.g = &workspace.Graph{Nodes: []*workspace.Node{n}}
	count := func() int {
		notes.mu.Lock()
		defer notes.mu.Unlock()
		c := 0
		for _, x := range notes.notes {
			if x.Level == notify.NeedsYou && strings.Contains(x.Body, "T1 has had no activity for 5m") {
				c++
			}
		}
		return c
	}
	at := func(d time.Duration) {
		h.m.now = func() time.Time { return feedT0.Add(d) }
		h.m.checkStalls()
	}
	at(4*time.Minute + 59*time.Second)
	if count() != 0 {
		t.Fatal("notified before 5 min")
	}
	at(5 * time.Minute)
	at(6 * time.Minute)
	at(9 * time.Minute)
	if count() != 1 {
		t.Fatalf("notes = %d, want 1", count())
	}
	// activity resumes, then stalls again
	n.LastActivityAt = feedT0.Add(10 * time.Minute)
	at(11 * time.Minute)
	at(15 * time.Minute)
	if count() != 2 {
		t.Fatalf("notes = %d after a second episode, want 2", count())
	}
}

func TestInspectorShowsContextAndActivity(t *testing.T) {
	n := actNode(workspace.StatusDone, 4)
	n.Activity[1].State = runner.ActFailed
	n.Activity[3].State = runner.ActFailed
	n.ContextTokens, n.ContextLimit = 18000, 128000
	g := &workspace.Graph{Nodes: []*workspace.Node{n}}
	out := actPlain(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, time.Now(), 50, 40, nil))
	for _, want := range []string{"Context  18k / 128k tokens", "Activity  4 steps (2 failed)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	n.ContextLimit = 0
	n.Activity = n.Activity[:1]
	n.Activity[0].State = runner.ActDone
	out = actPlain(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, time.Now(), 50, 40, nil))
	if !strings.Contains(out, "Context  18k tokens") || !strings.Contains(out, "Activity  1 step") || strings.Contains(out, "failed") {
		t.Errorf("inspector:\n%s", out)
	}
	// a short pane still fits
	out = renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, time.Now(), 50, 8, nil)
	if got := len(strings.Split(out, "\n")); got > 8 {
		t.Errorf("%d lines in an 8 row pane", got)
	}
}

// ── Output view ──────────────────────────────────────────────────────────────

func outHarness(t *testing.T, n *workspace.Node) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.m.g = &workspace.Graph{Nodes: []*workspace.Node{n}}
	h.m.sel = n.ID
	h.m.showGraph = false
	h.size(100, 30)
	return h
}

func TestOutputViewTimelineKeys(t *testing.T) {
	n := actNode(workspace.StatusDone, 4)
	n.EndedAt = feedT0.Add(72 * time.Second)
	n.ContextTokens = 18000
	n.Activity[1] = mkAct("a1", runner.ActCommand, runner.ActFailed, "go vet ./...")
	n.Activity[1].Detail = "vet: boom"
	n.Activity[1].ExitCode = actI(1)
	h := outHarness(t, n)
	h.m = h.m.openOutput(n)
	if h.m.screen != wsScreenOutput || h.m.outRaw {
		t.Fatalf("screen %v raw %v", h.m.screen, h.m.outRaw)
	}
	h.want("go vet ./...")
	h.want("✓")
	h.want("1m 12s")
	h.want("ctx 18k")
	if h.m.outCur != 3 {
		t.Fatalf("cursor starts at %d, want the newest (3)", h.m.outCur)
	}
	h.key("k")
	h.key("k")
	if h.m.outCur != 1 {
		t.Fatalf("cursor %d after k k", h.m.outCur)
	}
	h.wantNot("vet: boom")
	h.key("enter")
	h.want("vet: boom")
	h.key("space")
	h.wantNot("vet: boom")
	h.key("j")
	if h.m.outCur != 2 {
		t.Errorf("cursor %d after j", h.m.outCur)
	}
	h.key("t")
	if !h.m.outRaw {
		t.Fatal("t did not switch to raw")
	}
	h.want("no output yet")
	h.key("t")
	if h.m.outRaw {
		t.Fatal("t did not switch back")
	}
	h.key("esc")
	if h.m.screen != wsScreenMain {
		t.Error("esc did not go back")
	}
}

func TestOutputViewOldSessionOpensRaw(t *testing.T) {
	n := actNode(workspace.StatusDone, 0)
	h := outHarness(t, n)
	h.m.outputs["T1"] = []string{"legacy output line"}
	h.m = h.m.openOutput(n)
	if !h.m.outRaw {
		t.Fatal("a node without activity must open raw")
	}
	h.want("legacy output line")
	h.key("t")
	h.want("no activity recorded")
}

func TestOutputViewFollowsWhileRunning(t *testing.T) {
	n := actNode(workspace.StatusRunning, 3)
	n.LastActivityAt = feedT0
	h := outHarness(t, n)
	h.m.now = func() time.Time { return feedT0.Add(time.Second) }
	h.m = h.m.openOutput(n)
	if !h.m.outFollow || h.m.outCur != 2 {
		t.Fatalf("follow %v cursor %d", h.m.outFollow, h.m.outCur)
	}
	n.Activity = append(n.Activity, mkAct("new", runner.ActEdit, runner.ActRunning, "cmd/new.go"))
	h.m.refreshOutput()
	if h.m.outCur != 3 {
		t.Errorf("cursor %d, want to follow to 3", h.m.outCur)
	}
	h.want("cmd/new.go")
	// moving up stops following
	h.key("k")
	n.Activity = append(n.Activity, mkAct("new2", runner.ActRead, runner.ActRunning, "later.go"))
	h.m.refreshOutput()
	if h.m.outFollow || h.m.outCur != 2 {
		t.Errorf("follow %v cursor %d after k", h.m.outFollow, h.m.outCur)
	}
	// G returns to the newest
	h.key("G")
	if !h.m.outFollow || h.m.outCur != 4 {
		t.Errorf("follow %v cursor %d after G", h.m.outFollow, h.m.outCur)
	}
}

func TestOutputViewFitsWidths(t *testing.T) {
	n := actNode(workspace.StatusRunning, 30)
	n.LastActivityAt = feedT0
	n.Activity[5].Kind, n.Activity[5].Detail = runner.ActThinking, strings.Repeat("a long thought ", 40)
	h := outHarness(t, n)
	h.m.now = func() time.Time { return feedT0 }
	for _, w := range []int{80, 100} {
		h.size(w, 30)
		h.m = h.m.openOutput(n)
		h.key("g")
		for i := 0; i < 5; i++ {
			h.key("j")
		}
		h.key("enter")
		for _, l := range strings.Split(h.m.View(), "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: line of %d cells: %q", w, ansi.StringWidth(l), actPlain(l))
			}
		}
		h.key("esc")
	}
}

// ── Auto-compact ─────────────────────────────────────────────────────────────

// addDone adds n finished work nodes D<i> to the live graph.
func (h *wsHarness) addDone(from, to int) {
	h.t.Helper()
	if err := h.eng.Replace(func(g *workspace.Graph) {
		for i := from; i <= to; i++ {
			g.Nodes = append(g.Nodes, &workspace.Node{ID: fmt.Sprintf("D%d", i), Title: "done", Kind: workspace.KindWork,
				Status: workspace.StatusDone, Role: "coder", Summary: "ok", EndedAt: feedT0})
		}
	}); err != nil {
		h.t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
}

func (h *wsHarness) doneEvent(id string) {
	h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: id, Type: workspace.EventDone}})
}

func compactLines(h *wsHarness) int {
	n := 0
	for _, l := range h.autoLines() {
		if strings.Contains(l, "compacted") {
			n++
		}
	}
	return n
}

func TestAutoCompactAtLevelTwo(t *testing.T) {
	h, _ := autoHarness(t, 0)
	h.toGraph()
	h.addDone(1, 9)
	h.m.cfg.Workspace.Autonomy = 2
	h.doneEvent("D1")
	if compactLines(h) != 0 {
		t.Fatal("compacted at 9 done nodes")
	}
	h.addDone(10, 10)
	h.doneEvent("D10")
	h.wantAuto("compacted 10 finished nodes")
	if h.m.g.Node("D1") != nil {
		t.Error("D1 was not folded")
	}
	h.doneEvent("D10")
	if compactLines(h) != 1 {
		t.Errorf("compacted again without 10 new nodes: %q", h.autoLines())
	}
	h.addDone(11, 20)
	h.doneEvent("D20")
	if compactLines(h) != 2 {
		t.Errorf("did not re-arm after a compact: %q", h.autoLines())
	}
}

func TestAutoCompactNotAtLevelOneOrPaused(t *testing.T) {
	h, _ := autoHarness(t, 0)
	h.toGraph()
	h.addDone(1, 10)
	h.doneEvent("D1")
	if compactLines(h) != 0 || h.m.g.Node("D1") == nil {
		t.Fatal("level 1 compacted")
	}
	h.m.cfg.Workspace.Autonomy = 2
	h.m.autoPaused = true
	h.doneEvent("D1")
	if compactLines(h) != 0 || h.m.g.Node("D1") == nil {
		t.Fatal("paused automation compacted")
	}
	h.m.autoPaused = false
	h.doneEvent("D1")
	if compactLines(h) != 1 {
		t.Fatalf("lines %q", h.autoLines())
	}
}

func TestAutoCompactWaitsForRunningNodes(t *testing.T) {
	h, _ := autoHarness(t, 0)
	h.toGraph()
	h.addDone(1, 10)
	if err := h.eng.Replace(func(g *workspace.Graph) { g.Node("T1").Role = "coder" }); err != nil {
		t.Fatal(err)
	}
	h.eng.RunAll(h.m.runCtx)
	h.pump(func(m workspaceModel) bool { m.g = h.eng.Snapshot(); return h.eng.Running() > 0 })
	h.m.cfg.Workspace.Autonomy = 2
	h.doneEvent("D1")
	if compactLines(h) != 0 {
		t.Fatal("compacted while a node runs")
	}
}
