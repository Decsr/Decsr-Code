package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/automation"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

var inboxT0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// inboxGraph has one node of every kind the inbox reports, plus calm ones.
func inboxGraph() *workspace.Graph {
	mk := func(id string, st workspace.Status) *workspace.Node {
		return &workspace.Node{ID: id, Title: "Title " + id, Kind: workspace.KindWork, Role: "coder", Status: st,
			StartedAt: inboxT0.Add(-30 * time.Minute), EndedAt: inboxT0.Add(-2 * time.Minute)}
	}
	ask := mk("T1", workspace.StatusWaiting)
	ask.Report = &workspace.Report{Status: workspace.ReportQuestion, Question: "Which JSON field name for the commit?"}
	blocked := mk("T2", workspace.StatusWaiting)
	blocked.Report = &workspace.Report{Status: workspace.ReportBlocked, Summary: "no token"}
	limited := mk("T3", workspace.StatusWaiting)
	limited.Limit = &workspace.LimitInfo{Provider: "claude", ResetAt: inboxT0.Add(2 * time.Hour), Retries: 1}
	failed := mk("T4", workspace.StatusFailed)
	failed.Error = "go test: expected key \"commit\""
	stopped := mk("T5", workspace.StatusFailed)
	stopped.Error = "stopped"
	stalled := mk("T6", workspace.StatusRunning)
	stalled.LastActivityAt = inboxT0.Add(-6 * time.Minute)
	quiet := mk("T7", workspace.StatusRunning)
	quiet.LastActivityAt = inboxT0.Add(-2 * time.Minute)
	done := mk("T8", workspace.StatusDone)
	return &workspace.Graph{Nodes: []*workspace.Node{ask, blocked, limited, failed, stopped, stalled, quiet, done}}
}

func inboxIDs(items []inboxItem) string {
	var ids []string
	for _, it := range items {
		ids = append(ids, it.id)
	}
	return strings.Join(ids, " ")
}

func TestInboxItemsOfEveryKind(t *testing.T) {
	g := inboxGraph()
	g.PluginRequests = []workspace.PluginRequest{{Name: plugins.Catalog()[0].Name, Reason: "needs docs", Status: workspace.PluginPending}}
	rev := workspace.Review{Round: 2, At: inboxT0.Add(-5 * time.Minute), State: workspace.ReviewPending,
		Applied: make([]workspace.AppliedProposal, 3)}
	need, info := buildInbox(inboxInput{g: g, reviews: []workspace.Review{{Round: 1, State: workspace.ReviewApplied}, rev}, now: inboxT0,
		plugins: g.PendingPlugins()})
	if len(info) != 0 {
		t.Errorf("no info expected: %v", info)
	}
	want := "ask:T1 ask:T2 limit:T3 " + reviewKey(rev) + " failed:T4 stall:T6 plugin:" + plugins.Catalog()[0].Name
	if got := inboxIDs(need); got != want {
		t.Fatalf("ids = %q\nwant  %q", got, want)
	}
	byID := map[string]inboxItem{}
	for _, it := range need {
		byID[it.id] = it
	}
	if it := byID["ask:T1"]; it.label != "T1 asks" || !strings.Contains(it.text, "Which JSON field") || it.right != "2m" {
		t.Errorf("question: %+v", it)
	}
	if it := byID["ask:T2"]; it.label != "T2 blocked" {
		t.Errorf("blocked: %+v", it)
	}
	if it := byID["limit:T3"]; !strings.Contains(it.text, "claude") || !strings.Contains(it.text, "resets 14:00") || it.right != "retry 1/3" {
		t.Errorf("limit: %+v", it)
	}
	if it := byID[reviewKey(rev)]; it.label != "Lead review" || !strings.Contains(it.text, "3 changes") || it.right != "5m" {
		t.Errorf("review: %+v", it)
	}
	if it := byID["failed:T4"]; !strings.Contains(it.text, "expected key") {
		t.Errorf("failed: %+v", it)
	}
	if it := byID["stall:T6"]; !strings.Contains(it.text, "6m") {
		t.Errorf("stall: %+v", it)
	}
}

func TestInboxActionableItemsClearWhenResolved(t *testing.T) {
	g := inboxGraph()
	g.PluginRequests = []workspace.PluginRequest{{Name: plugins.Catalog()[0].Name, Status: workspace.PluginPending}}
	rev := workspace.Review{At: inboxT0, State: workspace.ReviewPending}
	count := func() int {
		need, _ := buildInbox(inboxInput{g: g, reviews: []workspace.Review{rev}, now: inboxT0, plugins: g.PendingPlugins()})
		return len(need)
	}
	if n := count(); n != 7 {
		t.Fatalf("items = %d", n)
	}
	g.Node("T1").Status = workspace.StatusRunning // answered
	g.Node("T1").LastActivityAt = inboxT0
	g.Node("T2").Status = workspace.StatusDone    // unblocked
	g.Node("T3").Limit = nil                      // limit lifted
	g.Node("T3").Status = workspace.StatusRunning // retried
	g.Node("T3").LastActivityAt = inboxT0         // alive
	g.Node("T4").Status = workspace.StatusTodo    // fix node added or retried
	g.Node("T6").LastActivityAt = inboxT0         // activity again
	g.SetPluginStatus(plugins.Catalog()[0].Name, workspace.PluginDeclined)
	rev.State = workspace.ReviewApplied
	if n := count(); n != 0 {
		need, _ := buildInbox(inboxInput{g: g, reviews: []workspace.Review{rev}, now: inboxT0, plugins: g.PendingPlugins()})
		t.Errorf("after resolving, %d items remain: %s", n, inboxIDs(need))
	}
}

func TestInboxInfoDismissalReappearsWithNewValue(t *testing.T) {
	autos := []inboxAutoRun{
		{name: "deps-bump", entry: automation.Entry{Start: inboxT0.Add(-2 * time.Hour), End: inboxT0.Add(-time.Hour), Result: "failed: T1 failed"}},
		{name: "ok-one", entry: automation.Entry{Start: inboxT0, End: inboxT0, Result: "nothing to do"}},
		{name: "ask-one", entry: automation.Entry{Start: inboxT0, End: inboxT0, Result: "needs you"}},
		{name: "slow", entry: automation.Entry{Start: inboxT0, End: inboxT0, Result: "timed out"}},
	}
	in := inboxInput{now: inboxT0, spent: 4.10, limit: 5, warnPct: 80, autos: autos}
	_, info := buildInbox(in)
	if len(info) != 4 {
		t.Fatalf("info = %d: %v", len(info), inboxIDs(info))
	}
	if info[0].id != "budget:80" || !strings.Contains(info[0].text, "$4.10 of $5.00 (82%)") {
		t.Errorf("budget item: %+v", info[0])
	}
	if info[1].right != "1h" || !strings.Contains(info[1].text, "failed") {
		t.Errorf("automation item: %+v", info[1])
	}
	in.dismissed = []string{info[0].id, info[1].id}
	_, info = buildInbox(in)
	if len(info) != 2 {
		t.Errorf("dismissed items still shown: %v", inboxIDs(info))
	}
	in.spent = 4.60 // a new bucket
	_, info = buildInbox(in)
	if len(info) != 3 || info[0].id != "budget:90" {
		t.Errorf("a new budget value must show again: %v", inboxIDs(info))
	}
	in.autos[0].entry.Start = inboxT0 // a new run of the same automation
	_, info = buildInbox(in)
	if len(info) != 4 {
		t.Errorf("a new automation run must show again: %v", inboxIDs(info))
	}
	in.spent = 3.9 // under the warn percent
	if _, info = buildInbox(in); len(info) != 3 || strings.HasPrefix(info[0].id, "budget") {
		t.Errorf("no budget item under the warn percent: %v", inboxIDs(info))
	}
	if need, info := buildInbox(inboxInput{now: inboxT0, spent: 9, limit: 5, warnPct: 80, needsOnly: true}); len(need)+len(info) != 0 {
		t.Error("needsOnly skips info")
	}
}

func TestSessionInboxDismissedRoundTrips(t *testing.T) {
	s := workspace.NewSession()
	s.InboxDismissed = []string{"budget:80", "auto:x:2026-11-16T10:00:00Z"}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back workspace.Session
	if err := json.Unmarshal(data, &back); err != nil || len(back.InboxDismissed) != 2 || back.InboxDismissed[0] != "budget:80" {
		t.Errorf("round trip: %v %v", back.InboxDismissed, err)
	}
}

// inboxHarness is a Workspace at the main pane with the inbox graph.
func inboxHarness(t *testing.T) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.toGraph()
	h.m.showGraph = false
	h.m.now = func() time.Time { return inboxT0 }
	h.m.g = inboxGraph()
	h.m.sel = "T8"
	return h
}

func TestInboxSidebarCount(t *testing.T) {
	h := inboxHarness(t)
	h.want("● 5 need you") // T1 T2 T3 T4 T6
	h.m.g.Node("T4").Status = workspace.StatusTodo
	h.want("● 4 need you")
	for _, id := range []string{"T1", "T2", "T3", "T6"} {
		h.m.g.Node(id).Status = workspace.StatusDone
		h.m.g.Node(id).Limit = nil
	}
	h.wantNot("need you")
	h.m.g.Node("T1").Status = workspace.StatusWaiting
	h.m.g.Node("T1").Report = &workspace.Report{Status: workspace.ReportQuestion, Question: "q"}
	h.want("● 1 needs you")
}

func TestInboxOverlayKeysAndJumps(t *testing.T) {
	h := inboxHarness(t)
	h.m.setFocus(wsFocusGraph)
	h.key("N")
	if h.m.overlay != wsOverlayInbox {
		t.Fatal("N opens the inbox")
	}
	h.want("Needs you · 5")
	h.want("T1 asks")
	h.want("T3 limit")
	h.key("esc")
	if h.m.overlay != wsOverlayNone {
		t.Fatal("Esc closes")
	}
	h.key("N", "down", "down", "enter") // T3, the limited node
	if h.m.overlay != wsOverlayNone || h.m.sel != "T3" || h.m.focus != wsFocusGraph {
		t.Errorf("jump: overlay=%v sel=%q focus=%v", h.m.overlay, h.m.sel, h.m.focus)
	}
	h.key("N", "j", "k", "up", "up") // stays on the first item
	if h.m.inboxIdx != 0 {
		t.Errorf("idx = %d", h.m.inboxIdx)
	}
	h.key("x") // a needs-you item is not cleared
	if len(h.m.sess.snapshot().InboxDismissed) != 0 || len(h.m.inboxNeeds()) != 5 {
		t.Error("x clears info items only")
	}
	h.key("down", "down", "down", "down", "enter") // T6, the stalled node
	if h.m.sel != "T6" {
		t.Errorf("sel = %q", h.m.sel)
	}
}

func TestInboxReviewAndPluginJumps(t *testing.T) {
	h := inboxHarness(t)
	rev := workspace.Review{Round: 1, At: inboxT0.Add(-time.Minute), State: workspace.ReviewPending, Applied: make([]workspace.AppliedProposal, 1)}
	h.m.sess.update(func(s *workspace.Session) { s.Reviews = []workspace.Review{rev} })
	h.m.showGraph = true
	h.m.setFocus(wsFocusSidebar)
	h.key("N", "down", "down", "down", "enter")
	if h.m.sel != reviewKey(rev) || h.m.showGraph || h.m.focus != wsFocusGraph {
		t.Errorf("review jump: sel=%q graph=%v focus=%v", h.m.sel, h.m.showGraph, h.m.focus)
	}
	h.m.g.PluginRequests = []workspace.PluginRequest{{Name: plugins.Catalog()[0].Name, Reason: "docs", Status: workspace.PluginPending}}
	h.m.sess.update(func(s *workspace.Session) { s.Reviews = nil })
	h.key("N")
	items := h.m.inboxAll()
	idx := -1
	for i, it := range items {
		if it.kind == inboxPlugin {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no plugin item: %v", inboxIDs(items))
	}
	h.m.inboxIdx = idx
	h.key("enter")
	if h.m.overlay != wsOverlayConfirm || h.m.confirm.kind != "plugin" {
		t.Errorf("the plugin prompt opens: overlay=%v kind=%q", h.m.overlay, h.m.confirm.kind)
	}
}

func TestInboxInfoItemsBudgetAndAutomation(t *testing.T) {
	h := inboxHarness(t)
	h.m.sess.update(func(s *workspace.Session) { s.BudgetUSD = 5 })
	h.m.g.Node("T8").InputTokens, h.m.g.Node("T8").CostUSD, h.m.g.Node("T8").CostKnown = 1000, 4.10, true
	h.m.deps.cfg = nil
	e := automation.Entry{Start: inboxT0.Add(-2 * time.Hour), End: inboxT0.Add(-time.Hour), Result: "failed: T1 failed"}
	if err := automation.AppendLog(h.root, "deps-bump", e); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.root, ".decsr", "automations", "plain.log"), []byte("cron noise\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.m.setFocus(wsFocusGraph)
	h.key("N")
	h.want("Info")
	h.want("Budget $4.10 of $5.00 (82%)")
	h.want("deps-bump automation failed: T1 failed 1h")
	h.wantNot("plain")
	if len(h.m.inboxNeeds()) != 5 {
		t.Error("info items do not count as needs-you")
	}

	h.key("down", "down", "down", "down", "down", "x") // the budget item
	if got := h.m.sess.snapshot().InboxDismissed; len(got) != 1 || got[0] != "budget:80" {
		t.Fatalf("dismissed = %v", got)
	}
	h.wantNot("Budget $4.10")
	h.key("enter") // the automation: its log lines
	if h.m.overlay != wsOverlayInfo || !strings.Contains(h.flat(), "result=failed: T1 failed") {
		t.Errorf("automation log overlay: %v\n%s", h.m.overlay, h.view())
	}
	h.key("esc")
	// A fresh budget bucket shows the item again.
	h.m.g.Node("T8").CostUSD = 4.70
	h.key("N")
	h.want("Budget $4.70 of $5.00 (94%)")
	h.m.inboxIdx = len(h.m.inboxAll()) - 1
	for i, it := range h.m.inboxAll() {
		if it.kind == inboxBudget {
			h.m.inboxIdx = i
		}
	}
	h.key("enter")
	if h.m.overlay != wsOverlayInfo || h.m.infoName != "Budget" {
		t.Errorf("budget jump: %v %q", h.m.overlay, h.m.infoName)
	}
}

func TestInboxEmptyAndKeysDoNotCollide(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.setFocus(wsFocusGraph)
	h.key("N")
	h.want("Nothing needs you.")
	h.key("esc")
	// In the prompt N is text and Ctrl+K opens the palette.
	h.m.setFocus(wsFocusPrompt)
	h.key("N")
	if h.m.overlay != wsOverlayNone || h.m.input.Value() != "N" {
		t.Errorf("N in the prompt is text: overlay=%v value=%q", h.m.overlay, h.m.input.Value())
	}
	h.send(ctrlK())
	if h.m.overlay != wsOverlayPalette || h.m.input.Value() != "N" {
		t.Errorf("Ctrl+K: overlay=%v value=%q", h.m.overlay, h.m.input.Value())
	}
	// The inbox keys are not bound elsewhere in the graph keys.
	h.key("esc", "esc")
	h.m.setFocus(wsFocusGraph)
	h.m.sel = "T1"
	h.key("x")
	if h.m.overlay != wsOverlayNone {
		t.Error("x does nothing outside the inbox")
	}
}

func TestInboxViewsFitTheTerminal(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 34}} {
		h := inboxHarness(t)
		h.size(sz[0], sz[1])
		h.m.sess.update(func(s *workspace.Session) {
			s.Reviews = []workspace.Review{{Round: 1, At: inboxT0.Add(-5 * time.Minute), State: workspace.ReviewPending}}
		})
		h.m.setFocus(wsFocusGraph)
		for _, open := range []string{"N", "ctrl+k"} {
			h.key("esc")
			if open == "N" {
				h.key("N")
			} else {
				h.send(ctrlK())
			}
			lines := strings.Split(h.m.View(), "\n")
			if len(lines) > sz[1] {
				t.Errorf("%v %s: %d lines", sz, open, len(lines))
			}
			for i, l := range lines {
				if ansi.StringWidth(l) > sz[0] {
					t.Errorf("%v %s: line %d is %d cells", sz, open, i, ansi.StringWidth(l))
				}
			}
		}
	}
}

func TestInboxRenderSample(t *testing.T) {
	h := inboxHarness(t)
	h.size(120, 34)
	h.m.sess.update(func(s *workspace.Session) {
		s.Reviews = []workspace.Review{{Round: 1, At: inboxT0.Add(-5 * time.Minute), State: workspace.ReviewPending,
			Applied: make([]workspace.AppliedProposal, 3)}}
		s.BudgetUSD = 5
	})
	h.m.g.Node("T8").InputTokens, h.m.g.Node("T8").CostUSD, h.m.g.Node("T8").CostKnown = 1000, 4.10, true
	_ = automation.AppendLog(h.root, "deps-bump", automation.Entry{Start: inboxT0.Add(-2 * time.Hour), End: inboxT0.Add(-time.Hour), Result: "failed: T1 failed"})
	h.m.setFocus(wsFocusGraph)
	h.key("N")
	t.Log("\n" + h.view())
}

func ctrlK() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlK} }
