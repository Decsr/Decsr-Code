package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// approvalHarness is a routing harness that shows the feed, where the card is.
func approvalHarness(t *testing.T, mode string, w, h int) *wsHarness {
	t.Helper()
	hh := routingHarness(t, mode)
	for _, role := range []string{"coder", "qa", "security"} {
		ac := hh.m.cfg.Agents[role]
		ac.Runner, ac.Model, ac.Effort = "claude", "claude-opus-5-5", "medium"
		hh.m.cfg.Agents[role] = ac
	}
	hh.size(w, h)
	hh.toGraph()
	hh.m.showGraph = false
	hh.m.feedFollow = true
	return hh
}

func TestApprovalCardRendersAtTwoWidths(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {100, 40}} {
		h := approvalHarness(t, config.RoutingAutoRole, size[0], size[1])
		for _, want := range []string{"Approve agents · 5 nodes · mode auto-role", "T1", "T2", "T3", "T4", "T5",
			"medium", "y approve · n cancel · e edit"} {
			h.want(want)
		}
		v := h.view()
		for _, l := range strings.Split(v, "\n") {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: line of %d cells: %q", size[0], size[1], w, l)
			}
		}
		if !strings.Contains(v, "$") {
			t.Errorf("%dx%d: no estimated cost on the card:\n%s", size[0], size[1], v)
		}
		if !strings.ContainsAny(v, "↓=≠↑") {
			t.Errorf("%dx%d: no marker on the card", size[0], size[1])
		}
	}
}

func TestApprovalCardRowsAndMarkers(t *testing.T) {
	c := approvalCard{Mode: "auto-connected", Total: 0.42, Sel: 1, Rows: []approvalRow{
		{ID: "T1", Title: "Data model", Grade: "small", Agent: "claude · haiku · low", Marker: "↓", Cost: 0.05, Reason: "small: cheap fits"},
		{ID: "T2", Title: "A very long title that must be cut to fit the row of the card", Grade: "large", Agent: "dsh · deepseek-v4-pro · high", Marker: "=", Reason: strings.Repeat("why ", 40)},
	}}
	for _, w := range []int{156, 96, 70} {
		lines := renderApprovalCard(c, w)
		flat := ansi.Strip(strings.Join(lines, "\n"))
		for _, l := range lines {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("width %d: line of %d cells", w, got)
			}
		}
		for _, want := range []string{"Approve agents · 2 nodes · mode auto-connected", "T1", "small", "↓", "$0.05", "–", "▸ T2"} {
			if !strings.Contains(flat, want) {
				t.Errorf("width %d: missing %q:\n%s", w, want, flat)
			}
		}
	}
}

func TestApproveKeyApprovesAndStarts(t *testing.T) {
	h := routingHarness(t, config.RoutingAutoRole)
	h.toGraph()
	h.key("A")
	if h.eng.Running() != 0 {
		t.Fatal("started before the approval")
	}
	h.key("y")
	if len(pendingIDs(h)) != 0 {
		t.Fatalf("still pending: %v", pendingIDs(h))
	}
	if h.status("T1") != workspace.StatusRunning {
		t.Errorf("T1 is %s after y", h.status("T1"))
	}
}

func TestRejectKeyCancelsTheSplit(t *testing.T) {
	h := approvalHarness(t, config.RoutingAutoRole, 160, 45)
	h.key("n")
	if len(pendingIDs(h)) != 0 || h.m.g.Node("T1") != nil {
		t.Fatal("n did not cancel the split")
	}
	if !transcriptHas(h, "cancelled the split: removed T1, T2, T3, T4, T5") {
		t.Errorf("no notice: %+v", h.m.sess.snapshot().Transcript)
	}
	h.wantNot("Approve agents")
}

func TestEditFlowSavesAManualAssignment(t *testing.T) {
	h := approvalHarness(t, config.RoutingAutoRole, 160, 45)
	before := h.eng.Snapshot().Node("T1").Assign
	h.key("e")
	if h.m.overlay != wsOverlayAgent {
		t.Fatalf("e did not open the editor:\n%s", h.view())
	}
	h.want("Agent of T1")
	h.key("right") // next runner
	h.key("tab")   // model
	for _, r := range "my-model" {
		h.key(string(r))
	}
	h.key("enter")
	if h.m.overlay != wsOverlayNone {
		t.Fatalf("editor still open:\n%s", h.view())
	}
	a := h.eng.Snapshot().Node("T1").Assign
	if a == nil || a.Source != workspace.SourceManual || !a.Pending || a.Runner == before.Runner || !strings.Contains(a.Model, "my-model") {
		t.Fatalf("assignment %+v (before %+v)", a, before)
	}
	if len(pendingIDs(h)) != 5 {
		t.Error("a manual edit must not approve the node")
	}
}

func TestEditorEscCancelsAndShowsRankings(t *testing.T) {
	h := approvalHarness(t, config.RoutingAutoRole, 160, 45)
	before := *h.eng.Snapshot().Node("T1").Assign
	h.key("e")
	h.want("balance")
	h.key("esc")
	if h.m.overlay != wsOverlayNone {
		t.Fatal("esc must close the editor")
	}
	if got := *h.eng.Snapshot().Node("T1").Assign; got != before {
		t.Errorf("esc changed the assignment: %+v", got)
	}
}

func TestApproveAndRejectCommandsMoved(t *testing.T) {
	for _, c := range []string{"/approve", "/reject"} {
		h := routingHarness(t, config.RoutingAutoRole)
		h.toGraph()
		h.key("A")
		h.key("c")
		h.typeCmd(c)
		h.want(c + " has moved: use y / n on the plan card")
		if len(pendingIDs(h)) == 0 {
			t.Fatalf("%s must run nothing", c)
		}
	}
}

func TestRoutingCommandShowsAndSets(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/routing")
	if !transcriptHas(h, "Routing mode is manual (from default)") {
		t.Fatalf("show: %+v", h.m.sess.snapshot().Transcript)
	}
	h.typeCmd("/routing auto-connected")
	if got := h.m.routingMode(); got != config.RoutingAutoConnected {
		t.Fatalf("mode = %q", got)
	}
	if got := config.LoadProject(h.root).Routing.Mode; got != config.RoutingAutoConnected {
		t.Fatalf("project.yaml mode = %q", got)
	}
	h.typeCmd("/routing")
	if !transcriptHas(h, "Routing mode is auto-connected (from this session)") {
		t.Fatalf("show after set: %+v", h.m.sess.snapshot().Transcript)
	}
	h.typeCmd("/routing nope")
	if !transcriptHas(h, routingUsage) {
		t.Error("no usage for a bad mode")
	}
	if got := config.LoadProject(h.root).Routing.Mode; got != config.RoutingAutoConnected {
		t.Errorf("a bad mode changed project.yaml: %q", got)
	}
}

func TestRoutingSourceProject(t *testing.T) {
	h := newWSHarness(t)
	if err := config.SaveProjectRoutingMode(h.root, config.RoutingAutoRole); err != nil {
		t.Fatal(err)
	}
	if got := h.m.routingSource(); got != "project" {
		t.Fatalf("source = %q", got)
	}
}

func TestUnattendedRunShowsNoApprovalCard(t *testing.T) {
	h, _ := autoHarness(t, 2)
	t.Setenv("HOME", t.TempDir())
	h.keepPending = true
	h.m.cfg.Routing.Mode = config.RoutingAutoRole
	h.toGraph()
	h.m.showGraph = false
	h.wantNot("Approve agents")
	if transcriptHas(h, "wait for approval of their agent") {
		t.Error("an unattended run must not announce a wait")
	}
}

func TestArrowsMoveTheCardSelection(t *testing.T) {
	h := approvalHarness(t, config.RoutingAutoRole, 160, 45)
	h.key("down", "down", "down")
	if got := h.m.approvalTarget().ID; got != "T4" {
		t.Fatalf("selected %s, want T4", got)
	}
	h.key("up")
	if got := h.m.approvalTarget().ID; got != "T3" {
		t.Fatalf("selected %s, want T3", got)
	}
	h.key("e")
	h.want("Agent of T3")
}

func TestNodeAgentShowsTheAssignment(t *testing.T) {
	cfg := config.DefaultConfig()
	ac := cfg.Agents["coder"]
	ac.Runner, ac.Model, ac.Effort = "claude", "claude-opus-5-5", "medium"
	cfg.Agents["coder"] = ac
	n := &workspace.Node{ID: "T1", Role: "coder"}

	a, ok := nodeAgent(cfg, n)
	if !ok || a.Model != "claude-opus-5-5" || a.Mark != "" {
		t.Fatalf("no assignment: %+v", a)
	}
	n.Assign = &workspace.AgentAssign{Runner: "claude", Model: "claude-sonnet-5-5", Effort: "low", Source: workspace.SourceAuto, Pending: true}
	a, _ = nodeAgent(cfg, n)
	if a.Model != "claude-sonnet-5-5" || a.Effort != "low" || a.Mark != "↓" {
		t.Fatalf("pending auto pick: %+v", a)
	}
	n.Assign.Pending, n.Assign.Source = false, workspace.SourceManual
	if a, _ = nodeAgent(cfg, n); a.Model != "claude-sonnet-5-5" || a.Mark != "" {
		t.Fatalf("approved manual pick: %+v", a)
	}
	n.Assign.Source = workspace.SourceAuto
	n.Assign.Model, n.Assign.Effort = "claude-opus-5-5", "medium"
	if a, _ = nodeAgent(cfg, n); a.Mark != "" {
		t.Fatalf("the same pair must not be marked: %+v", a)
	}
	n.Assign.Model, n.Assign.FallbackNote = "claude-sonnet-5-5", "sonnet not connected"
	if a, _ = nodeAgent(cfg, n); a.Model != "claude-opus-5-5" || a.Mark != "" {
		t.Fatalf("fallback must show the role's agent: %+v", a)
	}
}

func TestViewsShowTheAssignedAgent(t *testing.T) {
	h := approvalHarness(t, config.RoutingAutoRole, 160, 45)
	h.key("A")
	n := h.eng.Snapshot().Node("T1")
	if n.Assign == nil {
		t.Fatal("no assignment")
	}
	if err := h.eng.SetAssignment("T1", workspace.AgentAssign{Runner: "claude", Model: "claude-haiku-5-5", Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	rn, md := h.m.nodeLook(h.m.g.Node("T1"))
	if rn != "claude" || !strings.Contains(md, "haiku") {
		t.Fatalf("nodeLook = %q %q", rn, md)
	}
	if !strings.Contains(h.flat(), "claude-haiku-5-5") && !strings.Contains(h.flat(), "haiku") {
		t.Errorf("the node list does not show the assigned model:\n%s", h.view())
	}
	if !strings.Contains(ansi.Strip(renderInspector(h.m.g, h.m.g.Node("T1"), h.m.cfg, h.m.plug, h.m.now(), 40, 30, nil)), "haiku") {
		t.Error("the inspector does not show the assigned model")
	}
	if _, rm := h.m.nodeLooks(h.m.g.Node("T1")); !strings.Contains(rm, "haiku") {
		t.Errorf("running line model = %q", rm)
	}
}
