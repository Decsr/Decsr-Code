package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/automation"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// mcpGraph is a one-node read-only plan that the lead marked for MCP.
func mcpGraph() *workspace.Graph {
	g := readOnlyGraph()
	g.Nodes[0].MCP = []string{"github", "google-workspace"}
	return g
}

// showCard sizes the window and shows the feed, where the plan card is.
func showCard(h *wsHarness) {
	h.size(160, 45)
	h.m.showGraph = false
	h.m.feedFollow = true
}

func TestPlanCardShowsMCPAndWaitsEvenWhenReadOnly(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.graph = mcpGraph
	h.toGraph()
	if got := strings.Join(pendingIDs(h), ","); got != "T1" {
		t.Fatalf("pending %q, a read-only plan with MCP must wait for y", got)
	}
	if h.eng.Running() != 0 || transcriptHas(h, "read-only plan: started without approval") {
		t.Fatal("the read-only plan started without y")
	}
	showCard(h)
	h.want("uses MCP: github, google-workspace")
}

func TestPlanCardWarnsWhenTheAgentIsNotClaude(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.graph = mcpGraph
	h.toGraph()
	if err := h.eng.SetAssignment("T1", workspace.AgentAssign{Runner: "codex", Model: "gpt-5", Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	showCard(h)
	h.want("⚠ MCP needs a claude agent; T1 will run without it")

	if err := h.eng.SetAssignment("T1", workspace.AgentAssign{Runner: "claude", Model: "claude-haiku-5-5", Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.want("uses MCP: github")
	h.wantNot("MCP needs a claude agent")
}

func TestPlanWithoutMCPKeepsTheReadOnlyAutoStart(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.graph = readOnlyGraph
	h.toGraph()
	if h.status("T1") != workspace.StatusRunning {
		t.Fatalf("T1 is %s, want running", h.status("T1"))
	}
}

func TestAutonomyLevelsStillWaitForAnMCPPlan(t *testing.T) {
	for _, level := range []int{2, 3} {
		h, notes := autoHarness(t, level)
		t.Setenv("HOME", t.TempDir())
		h.keepPending = true
		h.m.cfg.Routing.Mode = config.RoutingAutoRole
		h.graph = mcpGraph
		h.toGraph()
		if got := strings.Join(pendingIDs(h), ","); got != "T1" {
			t.Fatalf("level %d: pending %q, want T1", level, got)
		}
		if h.eng.Running() != 0 {
			t.Fatalf("level %d: the MCP node started without y", level)
		}
		h.wantAuto("T1 uses MCP")
		if !notes.has(notify.NeedsYou, "") {
			t.Errorf("level %d: no needs-you note: %+v", level, notes.notes)
		}
	}
}

func TestAutomationRunStopsForAnMCPPlan(t *testing.T) {
	h, _ := autoRun(t, 2, true)
	h.keepPending = true
	h.graph = func() *workspace.Graph {
		g := mcpGraph()
		g.Nodes = append(g.Nodes, &workspace.Node{ID: "T2", Title: "Other", Kind: workspace.KindWork,
			Status: workspace.StatusTodo, Role: "reviewer", SuggestedRole: "reviewer", ReadOnlyTask: true, MCP: []string{"github"}})
		return g
	}
	h.toGraph()
	if h.eng.Running() != 0 {
		t.Fatal("an automation run started an MCP node")
	}
	res, lvl, done := h.verdict()
	if !done || res != "needs you: T1, T2 use MCP" || lvl != notify.NeedsYou {
		t.Fatalf("verdict %q %v done=%v", res, lvl, done)
	}
	_, info := buildInbox(inboxInput{now: inboxT0, autos: []inboxAutoRun{
		{name: "nightly", entry: automation.Entry{Start: inboxT0, End: inboxT0.Add(time.Minute), Result: res}},
	}})
	if len(info) != 1 || !strings.Contains(info[0].text, "needs you: T1, T2 use MCP") {
		t.Fatalf("inbox %+v", info)
	}
}
