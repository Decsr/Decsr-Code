package tui

import (
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func routingHarness(t *testing.T, mode string) *wsHarness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	h := newWSHarness(t)
	h.keepPending = true
	h.m.cfg.Routing.Mode = mode
	return h
}

func pendingIDs(h *wsHarness) []string {
	var ids []string
	for _, n := range h.eng.PendingApproval() {
		ids = append(ids, n.ID)
	}
	return ids
}

func TestSplitProposesPendingAgentsInEveryMode(t *testing.T) {
	for _, mode := range []string{"", config.RoutingManual, config.RoutingAutoRole, config.RoutingAutoConnected} {
		h := routingHarness(t, mode)
		h.toGraph()
		// Defined roles only: T1 coder, T3 qa, T4 coder, T5 security.
		if got := strings.Join(pendingIDs(h), ","); got != "T1,T3,T4,T5" {
			t.Fatalf("mode %q: pending %s", mode, got)
		}
		want := mode
		if want == "" {
			want = config.RoutingManual
		}
		if got := h.m.sess.snapshot().RoutingMode; got != want {
			t.Errorf("mode %q: frozen %q", mode, got)
		}
		if a := h.eng.Snapshot().Node("T1").Assign; a == nil || !a.Pending || a.Source == "" {
			t.Errorf("mode %q: T1 assign %+v", mode, a)
		}
	}
}

func TestPendingNodesDoNotStartUntilApproved(t *testing.T) {
	h := routingHarness(t, config.RoutingAutoRole)
	h.toGraph()
	h.key("A") // accept the suggested roles
	if err := h.eng.Run(h.m.runCtx, "T1"); err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("run before approval: %v", err)
	}
	h.key("R")
	if h.eng.Running() != 0 {
		t.Fatal("a node started before the approval")
	}
	h.key("y")
	if len(pendingIDs(h)) != 0 {
		t.Fatal("y must approve every pending agent")
	}
	h.key("R")
	if h.status("T1") != workspace.StatusRunning {
		t.Errorf("T1 is %s after the approval", h.status("T1"))
	}
}

func TestRejectingTheSplitDropsItsNodes(t *testing.T) {
	h := routingHarness(t, config.RoutingManual)
	h.toGraph()
	h.key("n")
	for _, id := range []string{"T1", "T3", "T4", "T5"} {
		if h.m.g.Node(id) != nil {
			t.Errorf("%s survived the rejection", id)
		}
	}
	if len(pendingIDs(h)) != 0 {
		t.Error("nodes still pending")
	}
}

func TestUnattendedRunApprovesTheAgents(t *testing.T) {
	h, _ := autoHarness(t, 2)
	t.Setenv("HOME", t.TempDir())
	h.keepPending = true
	h.m.cfg.Routing.Mode = config.RoutingAutoRole
	h.toGraph()
	if len(pendingIDs(h)) != 0 {
		t.Fatalf("an unattended run must approve: %v", pendingIDs(h))
	}
	if a := h.eng.Snapshot().Node("T1").Assign; a == nil || a.Pending {
		t.Fatalf("T1 assign %+v", a)
	}
	if h.status("T1") != workspace.StatusRunning {
		t.Errorf("T1 is %s, want running", h.status("T1"))
	}
	h.wantAuto("approved the agent of 4 nodes")
}

func TestUnattendedRunWaitsWhenAutoApproveIsOff(t *testing.T) {
	h, notes := autoHarness(t, 2)
	t.Setenv("HOME", t.TempDir())
	h.keepPending = true
	off := false
	h.m.cfg.Routing = config.RoutingConfig{Mode: config.RoutingAutoRole, AutoApprove: &off}
	h.toGraph()
	if len(pendingIDs(h)) != 4 {
		t.Fatalf("pending %v", pendingIDs(h))
	}
	if h.eng.Running() != 0 {
		t.Fatal("nothing may start without the approval")
	}
	h.wantAuto("routing.auto_approve is off")
	if len(notes.notes) == 0 {
		t.Error("the user was not notified")
	}
}

func TestSessionKeepsItsRoutingMode(t *testing.T) {
	h := routingHarness(t, config.RoutingAutoRole)
	h.toGraph()
	h.m.cfg.Routing.Mode = config.RoutingManual // the config changes later
	if got := h.m.routingMode(); got != config.RoutingAutoRole {
		t.Fatalf("mode = %q: the session must keep the frozen one", got)
	}
}

func TestRoutingConnectedExcludesClaudeForUnattendedSubscription(t *testing.T) {
	in := func(m workspaceModel) bool { _, ok := m.routingConnected()["claude"]; return ok }
	withClaudeAuth(t, runner.AuthSubscription)
	var m workspaceModel
	m.auto = &wsAuto{}
	if in(m) {
		t.Error("claude routable in an unattended subscription run")
	}
	m.auto.opts.AllowSubscription = true
	if !in(m) {
		t.Error("claude left out despite allow_subscription")
	}
	m.auto = nil
	if !in(m) {
		t.Error("claude left out of an interactive session")
	}
	withClaudeAuth(t, runner.AuthAPIKey)
	m.auto = &wsAuto{}
	if !in(m) {
		t.Error("claude left out with an API key")
	}
}
