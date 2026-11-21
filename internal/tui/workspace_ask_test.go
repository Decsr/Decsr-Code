package tui

import (
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func dbAsk() *workspace.LeadAsk {
	return &workspace.LeadAsk{Question: "Which database should the service use?",
		Options: []string{"SQLite - no server to run", "PostgreSQL - better for many writers"}, Recommended: 1}
}

// askLeadHarness is a Workspace whose lead just asked dbAsk.
func askLeadHarness(t *testing.T) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.m.showGraph = false
	h.graph = func() *workspace.Graph {
		return &workspace.Graph{Task: "t", Answer: "Two ways to do it.", Ask: dbAsk()}
	}
	h.sendTask("store the data")
	return h
}

func TestLeadAskCardShowsOptionsAndRecommendation(t *testing.T) {
	h := askLeadHarness(t)
	for _, want := range []string{"lead asks", "Which database should the service use?",
		"› 1. SQLite (Recommended) - no server to run", "2. PostgreSQL - better for many writers", "3. Type your own answer",
		"↑↓ move · 1-3 pick · Enter choose · Esc later"} {
		h.want(want)
	}
	if h.m.splitting || len(h.tasks) != 1 {
		t.Errorf("no turn starts without a key press: splitting=%v tasks=%q", h.m.splitting, h.tasks)
	}
}

func TestLeadAskChoosingSendsTheAnswerAsAUserTurn(t *testing.T) {
	for name, keys := range map[string][]string{
		"enter on the recommended": {"enter"},
		"digit":                    {"1"},
		"arrows":                   {"down", "up", "enter"},
	} {
		h := askLeadHarness(t)
		h.key(keys...)
		if !h.m.splitting {
			t.Fatalf("%s: no lead turn started", name)
		}
		if h.m.leadAsk != nil {
			t.Errorf("%s: the question is answered", name)
		}
		h.finishSplit()
		if got := h.tasks[len(h.tasks)-1]; got != "Answer: SQLite" {
			t.Errorf("%s: task = %q", name, got)
		}
		h.wantLine("Answer: SQLite")
	}
	h := askLeadHarness(t)
	h.key("down", "enter")
	h.finishSplit()
	if got := h.tasks[len(h.tasks)-1]; got != "Answer: PostgreSQL" {
		t.Errorf("task = %q", got)
	}
}

func TestLeadAskTypeYourOwnFocusesThePrompt(t *testing.T) {
	h := askLeadHarness(t)
	h.key("3")
	if h.m.focus != wsFocusPrompt || h.m.splitting {
		t.Fatalf("focus=%v splitting=%v", h.m.focus, h.m.splitting)
	}
	if h.m.leadAsk == nil {
		t.Error("the question stays until the user answers")
	}
}

func TestLeadAskEscHidesAndAnswerBringsItBack(t *testing.T) {
	h := askLeadHarness(t)
	h.key("esc")
	h.wantNot("lead asks")
	h.m.setFocus(wsFocusPrompt)
	h.typeCmd("/answer")
	h.want("lead asks")
}

func TestInstallCardArrowsAndEnterWorkWithHotKeys(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	h.propose(pluginAct("code-review@claude-plugins-official"))
	h.key("down", "up", "down", "enter")
	if len(h.m.installQ) != 0 {
		t.Error("Skip must drop the install")
	}
}

func TestFollowUpCardArrowsEnterAndDigits(t *testing.T) {
	h := followHarness(t)
	h.want("1. Plan them")
	h.key("down", "enter")
	if len(h.m.followups) != 0 || h.m.splitting {
		t.Errorf("Drop them sends nothing: %q splitting=%v", h.m.followups, h.m.splitting)
	}
	h = followHarness(t)
	h.key("1")
	if !h.m.splitting {
		t.Error("1 plans the follow-ups")
	}
}

func planPickHarness(t *testing.T) *wsHarness {
	t.Helper()
	h := approvalHarness(t, config.RoutingAutoRole, 160, 45)
	if !h.m.planPending() {
		t.Fatal("the plan card is not waiting")
	}
	return h
}

func TestPlanCardEnterOpensThePicker(t *testing.T) {
	h := planPickHarness(t)
	h.key("enter")
	for _, want := range []string{"› 1. Approve", "2. Edit agents", "3. Cancel"} {
		h.want(want)
	}
	h.key("esc")
	h.wantNot("2. Edit agents")
	if !h.m.planPending() {
		t.Error("Esc leaves the plan waiting")
	}
}

func TestPlanCardPickerApproves(t *testing.T) {
	h := planPickHarness(t)
	h.key("enter", "enter")
	if h.m.planPending() || h.m.planPick {
		t.Errorf("Approve starts the plan: pending=%v", pendingIDs(h))
	}
}

func TestPlanCardPickerEditsAgents(t *testing.T) {
	h := planPickHarness(t)
	h.key("enter", "down", "enter")
	if h.m.agentEd == nil {
		t.Error("Edit agents opens the editor")
	}
}

func TestPlanCardPickerCancels(t *testing.T) {
	h := planPickHarness(t)
	h.key("enter", "3")
	if h.m.planPending() || h.m.planPick {
		t.Errorf("Cancel drops the plan: pending=%v", pendingIDs(h))
	}
	if h.m.g != nil && len(h.m.g.PendingApproval()) > 0 {
		t.Error("nodes still wait for approval")
	}
}

func TestPlanCardHotKeysStillWork(t *testing.T) {
	h := planPickHarness(t)
	h.key("e")
	if h.m.agentEd == nil {
		t.Error("e opens the editor")
	}
	h = planPickHarness(t)
	h.key("n")
	if h.m.planPending() {
		t.Error("n cancels the plan")
	}
	h = planPickHarness(t)
	h.key("y")
	if h.m.planPending() {
		t.Error("y approves the plan")
	}
	h = planPickHarness(t)
	h.key("enter", "y")
	if h.m.planPending() || h.m.planPick {
		t.Error("y on the picker approves")
	}
}

func TestAutomationDoesNotAnswerALeadQuestion(t *testing.T) {
	h, _ := autoRun(t, 2, true)
	h.graph = func() *workspace.Graph {
		return &workspace.Graph{Task: "t", Ask: dbAsk()}
	}
	h.send(wsAutoTaskMsg{})
	h.finishSplit()
	a := h.m.auto
	if !a.finished || !strings.HasPrefix(a.result.Result, "failed: the lead asked a question and created no nodes: Which database") {
		t.Errorf("result %+v", a.result)
	}
	if h.m.leadAsk != nil || len(h.tasks) != 1 {
		t.Errorf("nothing answers it: ask=%v tasks=%q", h.m.leadAsk, h.tasks)
	}
}
