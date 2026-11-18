package tui

import (
	"testing"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func changesOnly(cs ...workspace.Change) func() *workspace.Graph {
	return func() *workspace.Graph { return &workspace.Graph{Task: "t", Changes: cs} }
}

func TestWorkspaceChangesPlanCardAndApply(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.showGraph = false
	h.wsMarkDone("T1")
	h.graph = changesOnly(
		workspace.Change{Node: "T4", Action: workspace.ChangeUpdate, Description: "also handle the --port flag", Reason: "new task needs it"},
		workspace.Change{Node: "T5", Action: workspace.ChangeCancel, Reason: "replaced by T7"},
		workspace.Change{Node: "T1", Action: workspace.ChangeNote, Message: "keep the old flag name as an alias"},
	)
	h.key("tab")
	h.sendTask("more")
	if len(h.m.pendingChanges) != 3 {
		t.Fatalf("pending = %+v", h.m.pendingChanges)
	}
	h.want("~ T4 changed: also handle the --port flag (new task needs it)")
	h.want("✗ T5 cancelled: replaced by T7")
	h.want("↻ T1 starts again: keep the old flag name as an alias")
	h.want("y apply · n drop")
	if h.g().Node("T5") == nil {
		t.Fatal("nothing may change before y")
	}
	h.key("y")
	g := h.g()
	if len(h.m.pendingChanges) != 0 || g.Node("T5") != nil || g.Node("T4").Description != "also handle the --port flag" {
		t.Fatalf("after y: %+v", g.Nodes)
	}
	if n := g.Node("T1"); n.Reply != "keep the old flag name as an alias" {
		t.Fatalf("T1 = %+v", n)
	}
	h.wantNot("Approve changes")
}

func (h *wsHarness) g() *workspace.Graph { return h.eng.Snapshot() }

func TestWorkspaceChangesDroppedOnN(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.graph = changesOnly(workspace.Change{Node: "T5", Action: workspace.ChangeCancel})
	h.key("tab")
	h.sendTask("more")
	h.key("n")
	if len(h.m.pendingChanges) != 0 || h.g().Node("T5") == nil {
		t.Fatalf("n must drop the changes and keep the graph: %+v", h.g().Nodes)
	}
	if !transcriptHas(h, "dropped 1 change") {
		t.Fatal("no drop line")
	}
}

func TestWorkspaceTaskWaitsForPlanCard(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.graph = changesOnly(workspace.Change{Node: "T5", Action: workspace.ChangeCancel})
	h.key("tab")
	h.sendTask("more")
	h.m.input.SetValue("again")
	h.key("tab")
	h.key("enter")
	if h.m.splitting || !transcriptHas(h, "answer the plan card first") {
		t.Fatal("a second task must wait for the plan card")
	}
}

func TestWorkspaceQueuedNoteReachesRunningNode(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	h.graph = changesOnly(workspace.Change{Node: "T1", Action: workspace.ChangeNote, Message: "use tabs"})
	h.key("tab")
	h.sendTask("note it")
	if len(h.m.pendingChanges) != 1 {
		t.Fatal("the note must wait for y")
	}
	h.key("y")
	if got := h.g().Node("T1").PendingReply; got != "use tabs" {
		t.Fatalf("PendingReply = %q", got)
	}
	if h.g().Node("T1").Status != workspace.StatusRunning {
		t.Fatal("a running node is never stopped")
	}
}

func TestWorkspaceNewNodesWhileRunningAreAppended(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	h.graph = wsSmallGraph
	h.key("tab")
	h.sendTask("more")
	if h.g().Node("T6") == nil || h.g().Node("T1").Status != workspace.StatusRunning {
		t.Fatalf("graph = %+v", h.g().Nodes)
	}
}
