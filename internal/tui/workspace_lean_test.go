package tui

import (
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func TestDirectAnswerIsSaidByTheLeadWithoutNodes(t *testing.T) {
	h := newWSHarness(t)
	h.graph = func() *workspace.Graph { return &workspace.Graph{Task: "what is this", Answer: "It is a **Go** CLI."} }
	h.sendTask("what is this")
	if h.m.hasNodes() || h.eng != nil {
		t.Fatalf("an answer must add no nodes and no Build & test (eng=%v)", h.eng)
	}
	if h.m.splitting || h.m.input.Value() != "" {
		t.Errorf("splitting=%v input=%q", h.m.splitting, h.m.input.Value())
	}
	var lead string
	for _, l := range h.m.sess.snapshot().Transcript {
		if l.Who == "lead" {
			lead = l.Text
		}
	}
	if lead != "It is a **Go** CLI." {
		t.Errorf("lead said %q", lead)
	}
	if s := h.m.sess.snapshot(); s.In != 100 || s.Out != 50 {
		t.Errorf("tokens = %d/%d, want counted like a split", s.In, s.Out)
	}
	if transcriptHas(h, "no sub-tasks") {
		t.Error("the no sub-tasks error must not show")
	}
}

// readOnlyGraph is a one-node review plan by the reviewer role.
func readOnlyGraph() *workspace.Graph {
	return &workspace.Graph{Task: "review it", Title: "Review", Nodes: []*workspace.Node{
		{ID: "T1", Title: "Review the project", Kind: workspace.KindWork, Status: workspace.StatusTodo,
			Role: "reviewer", SuggestedRole: "reviewer", ReadOnlyTask: true},
	}}
}

func TestReadOnlyPlanStartsWithoutTheCard(t *testing.T) {
	for _, mode := range []string{config.RoutingManual, config.RoutingAutoRole} {
		h := routingHarness(t, mode)
		h.graph = readOnlyGraph
		h.toGraph()
		if len(pendingIDs(h)) != 0 {
			t.Fatalf("mode %s: still pending %v", mode, pendingIDs(h))
		}
		if h.status("T1") != workspace.StatusRunning {
			t.Errorf("mode %s: T1 is %s, want running", mode, h.status("T1"))
		}
		if !transcriptHas(h, "read-only plan: started without approval") {
			t.Errorf("mode %s: no note in the transcript", mode)
		}
	}
}

func TestMixedPlanWaitsForApproval(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.graph = func() *workspace.Graph {
		g := readOnlyGraph()
		g.Nodes = append(g.Nodes, &workspace.Node{ID: "T2", Title: "Fix it", Kind: workspace.KindWork,
			Status: workspace.StatusTodo, Role: "coder", SuggestedRole: "coder"})
		return g
	}
	h.toGraph()
	if got := strings.Join(pendingIDs(h), ","); got != "T1,T2" {
		t.Fatalf("pending %s, want T1,T2", got)
	}
	if h.eng.Running() != 0 {
		t.Fatal("a plan that writes must wait for y")
	}
	h.key("y")
	if h.status("T2") != workspace.StatusTodo && h.status("T2") != workspace.StatusRunning {
		t.Errorf("T2 is %s", h.status("T2"))
	}
}

func TestPlanCardShowsRoleAgentAndAccessInManualRouting(t *testing.T) {
	h := approvalHarness(t, config.RoutingManual, 160, 45)
	for _, want := range []string{"Approve agents · 5 nodes · mode manual", "coder · writes", "security · read-only",
		"docs_writer (new · uses the pm's agent)", "y approve · n cancel · e edit agent · a role"} {
		h.want(want)
	}
}

func TestApprovingCreatesTheNewRoleForTheSessionOnly(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	var session []string
	h.m.deps.sessionRole = func(name string, _ config.AgentConfig) { session = append(session, name) }
	h.toGraph()
	if h.m.defined("docs_writer") {
		t.Fatal("the role must not exist before y")
	}
	h.key("y")
	if !h.m.defined("docs_writer") {
		t.Fatal("y did not create the role")
	}
	if got := h.eng.Snapshot().Node("T2").Role; got != "docs_writer" {
		t.Errorf("T2 role = %q", got)
	}
	if len(h.roleCalls) != 0 {
		t.Errorf("the role was written to project.yaml: %+v", h.roleCalls)
	}
	if strings.Join(session, ",") != "docs_writer" {
		t.Errorf("executor was told %v", session)
	}
	// The new role uses the pm's agent.
	r, _ := workspace.FindRole(h.m.cfg, "docs_writer")
	if pm := h.m.cfg.Agents["pm"]; r.Agent.Runner != pm.Runner || r.Agent.Model != pm.Model {
		t.Errorf("agent %+v, pm %+v", r.Agent, pm)
	}
	// An explicit save writes it.
	h.typeCmd("/roles save docs_writer")
	if len(h.roleCalls) != 1 || h.roleCalls[0].name != "docs_writer" {
		t.Fatalf("role calls %+v", h.roleCalls)
	}
	if _, still := h.m.sessRoles["docs_writer"]; still {
		t.Error("a saved role is no longer session-only")
	}
	h.typeCmd("/roles save nothing")
	if !transcriptHas(h, `"nothing" is not a role of this session`) {
		t.Error("no error for an unknown role")
	}
}

func TestBuildAndTestIsAddedOnlyWhenANodeWrites(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.graph = readOnlyGraph
	h.toGraph()
	if h.eng.Snapshot().Node("CHECK") != nil {
		t.Fatal("a read-only plan must not get Build & test")
	}

	h2 := routingHarness(t, config.RoutingManual)
	h2.graph = func() *workspace.Graph {
		g := readOnlyGraph()
		g.Nodes = append(g.Nodes, &workspace.Node{ID: "T2", Title: "Fix", Kind: workspace.KindWork,
			Status: workspace.StatusTodo, Role: "coder", SuggestedRole: "coder"})
		return g
	}
	h2.toGraph()
	if h2.eng.Snapshot().Node("CHECK") == nil {
		t.Fatal("a plan with a writing node needs Build & test")
	}
}

func TestFollowUpReviewKeepsTheGraphWithoutNewCheck(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.graph = readOnlyGraph
	h.toGraph()
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
	h.graph = func() *workspace.Graph {
		g := readOnlyGraph()
		g.Nodes[0].Title = "Second look"
		return g
	}
	h.key("tab")
	h.sendTask("look again")
	if h.eng.Snapshot().Node("CHECK") != nil {
		t.Error("a second read-only task must not add Build & test")
	}
	if h.eng.Snapshot().Node("T2") == nil {
		t.Error("the follow-up node is missing")
	}
}

func TestCheckCommandAddsAndRunsBuildAndTest(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.graph = readOnlyGraph
	h.toGraph()
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
	if h.eng.Snapshot().Node("CHECK") != nil {
		t.Fatal("setup: no check expected yet")
	}
	h.typeCmd("/check")
	c := h.eng.Snapshot().Node("CHECK")
	if c == nil || !c.ForceRun || c.Status != workspace.StatusRunning {
		t.Fatalf("check after /check = %+v", c)
	}
	h.exec.release("CHECK", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("CHECK").Status == workspace.StatusDone })
	if h.eng.Snapshot().Node("CHECK").ForceRun {
		t.Error("the force flag must clear once it ran")
	}
	// Again: a finished check is reset to todo and runs.
	h.typeCmd("/check")
	if got := h.eng.Snapshot().Node("CHECK").Status; got != workspace.StatusRunning {
		t.Errorf("second /check: status %s", got)
	}
}

func TestCheckCommandWithoutAGraph(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/check")
	if !transcriptHas(h, "nothing to check yet") {
		t.Error("no message")
	}
}

func TestRunSummaryTellsASkippedBuildAndTest(t *testing.T) {
	g := summaryGraph()
	g.Nodes[3].Skipped = true
	got := runSummary(g, []string{"T1", "T2", "T3", "CHECK"}, true, 120)
	if !strings.Contains(got, "– Build & test skipped · nothing changed") || strings.Contains(got, "passed") {
		t.Errorf("summary:\n%s", got)
	}
	if strings.Contains(got, "/merge") {
		t.Errorf("a skipped check must not offer /merge:\n%s", got)
	}
}

func TestSkippedBuildAndTestOpensNoPullRequest(t *testing.T) {
	h, gh, _, _ := prHarness(t)
	h.m.cfg.Workspace.Autonomy = 3
	h.m.cfg.Workspace.BudgetUSD = 5
	if err := h.eng.Replace(func(g *workspace.Graph) { g.Node("CHECK").Skipped = true }); err != nil {
		t.Fatal(err)
	}
	h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "CHECK", Type: workspace.EventDone}})
	for _, l := range h.autoLines() {
		if strings.Contains(l, "pushing") {
			t.Errorf("auto line %q", l)
		}
	}
	if h.m.pr.working || h.m.pr.watching || gh.count("pr create") != 0 {
		t.Error("a skipped Build & test must not open a pull request")
	}
}

func TestReadOnlyAnswerIsSaidInTheChatAndSummarised(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.graph = readOnlyGraph
	h.exec.answers = map[string]string{"T1": "## Findings\n\n- the parser is fine"}
	h.toGraph()
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return transcriptHas(h, "Run finished") })
	var said, summary string
	for _, l := range h.m.sess.snapshot().Transcript {
		if l.Who == "lead" && strings.HasPrefix(l.Text, "T1 · reviewer:") {
			said = l.Text
		}
		if strings.HasPrefix(l.Text, "Run finished") {
			summary = l.Text
		}
	}
	if said != "T1 · reviewer:\n\n## Findings\n\n- the parser is fine" {
		t.Errorf("chat answer = %q", said)
	}
	if !strings.Contains(summary, "✓ T1 Review the project - answer above") {
		t.Errorf("summary = %q", summary)
	}
	if strings.Contains(summary, "Build & test") {
		t.Errorf("a read-only run has no Build & test line:\n%s", summary)
	}
}
