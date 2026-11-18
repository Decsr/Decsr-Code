package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func summaryGraph() *workspace.Graph {
	t1 := &workspace.Node{ID: "T1", Title: "Add /health endpoint", Kind: workspace.KindWork, Status: workspace.StatusDone,
		Summary: "raw", Report: &workspace.Report{Status: "done", Summary: "Added the handler and route."},
		FileStats: []workspace.FileStat{{Path: "a.go"}, {Path: "b.go"}}}
	t2 := &workspace.Node{ID: "T2", Title: "Write tests", Kind: workspace.KindWork, Status: workspace.StatusDone,
		Summary: "Added table tests for the handler.", Files: []string{"h_test.go"}}
	t3 := &workspace.Node{ID: "T3", Title: "Pick a port", Kind: workspace.KindWork, Status: workspace.StatusWaiting,
		Report: &workspace.Report{Status: "question", Question: "Which port?"}}
	return &workspace.Graph{Nodes: []*workspace.Node{t1, t2, t3,
		{ID: "CHECK", Title: "Build & test", Kind: workspace.KindCheck, Status: workspace.StatusDone}}}
}

func TestRunSummaryLinesAndNextSteps(t *testing.T) {
	got := runSummary(summaryGraph(), []string{"T1", "T2", "T3", "CHECK"}, true, 120)
	want := strings.Join([]string{
		"Run finished · 3 done · 1 waiting · 0 failed",
		"✓ T1 Add /health endpoint - Added the handler and route. (2 files)",
		"✓ T2 Write tests - Added table tests for the handler. (1 file)",
		"◇ T3 Pick a port - waits for your answer",
		"✓ Build & test passed",
		"Next: answer T3, /merge to bring the result into your checkout, or type a follow-up task.",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunSummaryFailedAndNotRun(t *testing.T) {
	g := summaryGraph()
	g.Nodes[2].Status = workspace.StatusFailed
	g.Nodes[2].Error = "worker: empty response"
	g.Nodes[3].Status = workspace.StatusTodo
	got := runSummary(g, []string{"T3"}, false, 120)
	for _, want := range []string{"2 done · 0 waiting · 1 failed", "! T3 Pick a port - worker: empty response", "· Build & test not run",
		"Next: retry with `@T3 ...`, or type a follow-up task."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "T1 Add") || strings.Contains(got, "/merge") {
		t.Errorf("only the ended nodes belong, and /merge needs isolation:\n%s", got)
	}
}

func TestRunSummaryOnlyApplicableNextSteps(t *testing.T) {
	g := &workspace.Graph{Nodes: []*workspace.Node{{ID: "T1", Title: "x", Kind: workspace.KindWork, Status: workspace.StatusDone, Summary: "s"}}}
	if got := runSummary(g, []string{"T1"}, true, 80); !strings.HasSuffix(got, "Next: type a follow-up task.") {
		t.Errorf("got:\n%s", got)
	}
	if runSummary(g, nil, true, 80) != "" {
		t.Error("nothing ended, nothing to say")
	}
}

func TestRunSummaryLinesFitTheWidth(t *testing.T) {
	g := summaryGraph()
	g.Nodes[0].Report.Summary = strings.Repeat("long ", 40)
	for _, l := range strings.Split(runSummary(g, []string{"T1"}, false, 60), "\n") {
		if ansi.StringWidth(l) > 60 {
			t.Errorf("line of %d cells: %q", ansi.StringWidth(l), l)
		}
	}
}

func TestRunSummaryIsSaidWhenTheRunEnds(t *testing.T) {
	h := newWSHarness(t)
	h.graph = wsSmallGraph
	h.toGraph()
	h.key("A", "R")
	h.finish(nil, "T1", "T2")
	h.finish(nil, "CHECK")
	var found string
	for _, l := range h.m.sess.snapshot().Transcript {
		if strings.HasPrefix(l.Text, "Run finished") {
			if found != "" {
				t.Fatalf("said twice:\n%s\n%s", found, l.Text)
			}
			found = l.Text
		}
	}
	if !strings.Contains(found, "✓ T1 Search index - ok T1") || !strings.Contains(found, "✓ Build & test passed") || !strings.Contains(found, "3 done") {
		t.Errorf("summary:\n%s", found)
	}
	if len(h.m.runEnded) != 0 {
		t.Errorf("ended list not reset: %v", h.m.runEnded)
	}
}
