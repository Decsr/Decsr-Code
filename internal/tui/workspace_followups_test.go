package tui

import (
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func followGraph() *workspace.Graph {
	t1 := &workspace.Node{ID: "T1", Title: "Add --port", Kind: workspace.KindWork, Status: workspace.StatusDone, Summary: "s",
		Report: &workspace.Report{Status: "done", Summary: "Added the flag.", Next: []string{"update the README for the new --port flag", "add a test for the empty config file"}}}
	t2 := &workspace.Node{ID: "T2", Title: "Docs", Kind: workspace.KindWork, Status: workspace.StatusDone, Summary: "s",
		Report: &workspace.Report{Status: "done", Summary: "Docs.", Next: []string{"Update the README for the new --port flag", "  "}}}
	t3 := &workspace.Node{ID: "T3", Title: "Old", Kind: workspace.KindWork, Status: workspace.StatusDone, Summary: "s",
		Report: &workspace.Report{Status: "done", Next: []string{"not in this run"}}}
	return &workspace.Graph{Nodes: []*workspace.Node{t1, t2, t3}}
}

func TestFollowUpsAreCollectedOnceFromTheNodesOfTheRun(t *testing.T) {
	got := followUps(followGraph(), []string{"T1", "T2"})
	want := []string{"T1: update the README for the new --port flag", "T1: add a test for the empty config file"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := followUps(followGraph(), []string{"T3"}); len(got) != 1 || got[0] != "T3: not in this run" {
		t.Errorf("got %q", got)
	}
	if got := followUps(summaryGraph(), []string{"T1", "T2"}); len(got) != 0 {
		t.Errorf("a node without next has no follow-ups: %q", got)
	}
}

func TestRunSummaryListsTheFollowUps(t *testing.T) {
	got := runSummary(followGraph(), []string{"T1", "T2"}, false, 120)
	want := "Suggested follow-ups\n  1. T1: update the README for the new --port flag\n  2. T1: add a test for the empty config file\n  y plan them · n drop\nNext: type a follow-up task."
	if !strings.Contains(got, want) {
		t.Errorf("got:\n%s", got)
	}
	if got := runSummary(summaryGraph(), []string{"T1", "T2"}, false, 120); strings.Contains(got, "Suggested follow-ups") {
		t.Errorf("no section without follow-ups:\n%s", got)
	}
}

// followHarness runs a one-node plan whose report suggests follow-ups, until
// the run summary is said.
func followHarness(t *testing.T) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.graph = wsSmallGraph
	h.exec.reports = map[string]workspace.Report{"T1": {Status: workspace.ReportDone, Summary: "ok",
		Next: []string{"update the README", "add a test"}}}
	h.toGraph()
	h.key("A", "R")
	h.finish(nil, "T1", "T2")
	h.finish(nil, "CHECK")
	h.drain()
	h.m.showGraph = false
	h.m.setFocus(wsFocusGraph)
	return h
}

func TestFollowUpCardPlansThemWithOneTask(t *testing.T) {
	h := followHarness(t)
	h.want("Suggested follow-ups")
	h.want("y plan them · n drop")
	h.key("y")
	if !h.m.splitting {
		t.Fatalf("y must send the follow-ups to the lead:\n%s", h.view())
	}
	h.finishSplit()
	want := "Follow-ups suggested by the finished nodes:\n- T1: update the README\n- T1: add a test"
	if len(h.tasks) == 0 || h.tasks[len(h.tasks)-1] != want {
		t.Errorf("task = %q, want %q", h.tasks, want)
	}
	if len(h.m.followups) != 0 {
		t.Errorf("the follow-ups are used up: %q", h.m.followups)
	}
}

func TestFollowUpCardDropClearsThem(t *testing.T) {
	h := followHarness(t)
	n := len(h.tasks)
	h.key("n")
	if len(h.m.followups) != 0 || h.m.splitting || len(h.tasks) != n {
		t.Errorf("n drops them and sends nothing: %q splitting=%v", h.m.followups, h.m.splitting)
	}
	if len(h.m.askCards()) != 0 {
		t.Errorf("the card is gone: %+v", h.m.askCards())
	}
}

func TestFollowUpKeysDoNotStealTyping(t *testing.T) {
	h := followHarness(t)
	h.m.setFocus(wsFocusPrompt)
	h.key("y")
	if len(h.m.followups) != 2 || h.m.splitting || h.m.input.Value() != "y" {
		t.Errorf("typing y goes to the prompt: followups=%q splitting=%v input=%q", h.m.followups, h.m.splitting, h.m.input.Value())
	}
}
