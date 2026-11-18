package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wantLine fails unless a line of the conversation is exactly text.
func (h *wsHarness) wantLine(text string) {
	h.t.Helper()
	for _, l := range h.m.sess.snapshot().Transcript {
		if l.Text == text {
			return
		}
	}
	h.t.Fatalf("no conversation line %q in %+v", text, h.m.sess.snapshot().Transcript)
}

// askHarness runs T1 until it ends with rep, with the feed shown.
func askHarness(t *testing.T, rep workspace.Report) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.exec.reports = map[string]workspace.Report{"T1": rep}
	h.toGraph()
	h.key("A", "r")
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status != workspace.StatusRunning })
	h.drain()
	h.m.showGraph = false
	h.m.setFocus(wsFocusGraph)
	return h
}

var portQuestion = workspace.Report{Status: workspace.ReportQuestion, Summary: "need a port", Question: "Which port should the health check use?",
	Options: []string{"8080 - matches the dev server", "9090 - separate from app traffic"}, Recommended: 1}

func TestQuestionCardShowsOptionsAndRecommendation(t *testing.T) {
	h := askHarness(t, portQuestion)
	h.wantLine("T1 asks: Which port should the health check use?")
	for _, want := range []string{"T1 asks · Data model", "Which port should the health check use?",
		"› 1. 8080 (Recommended) - matches the dev server", "2. 9090 - separate from app traffic", "3. Type your own answer",
		"↑↓ move · 1-3 pick · Enter choose · Esc later"} {
		h.want(want)
	}
	for _, l := range strings.Split(h.view(), "\n") {
		if ansi.StringWidth(l) > 140 {
			t.Errorf("line of %d cells", ansi.StringWidth(l))
		}
	}
}

func TestQuestionCardCursorStartsOnRecommendedAndMoves(t *testing.T) {
	r := portQuestion
	r.Recommended = 2
	h := askHarness(t, r)
	h.want("› 2. 9090 (Recommended)")
	h.key("up")
	h.want("› 1. 8080")
	h.key("down", "down", "down")
	h.want("› 3. Type your own answer")
}

func TestQuestionCardEnterAnswersTheNode(t *testing.T) {
	h := askHarness(t, portQuestion)
	h.key("enter")
	n := h.m.g.Node("T1")
	if n.Status != workspace.StatusRunning || n.Attempt != 2 || n.Reply != "Answer: 8080" {
		t.Fatalf("node = %+v", n)
	}
	h.wantNot("Type your own answer")
}

func TestQuestionCardDigitPicksAnOption(t *testing.T) {
	h := askHarness(t, portQuestion)
	h.key("2")
	if got := h.m.g.Node("T1").Reply; got != "Answer: 9090" {
		t.Errorf("reply = %q", got)
	}
}

func TestQuestionCardOwnAnswerFillsThePrompt(t *testing.T) {
	h := askHarness(t, portQuestion)
	h.key("3")
	if h.m.focus != wsFocusPrompt || h.m.input.Value() != "@T1 " {
		t.Fatalf("focus %v, prompt %q", h.m.focus, h.m.input.Value())
	}
	if h.m.g.Node("T1").Status != workspace.StatusWaiting {
		t.Error("the node keeps waiting")
	}
	// Keys go to the prompt while typing.
	h.key("1")
	if !strings.HasSuffix(h.m.input.Value(), "1") {
		t.Errorf("prompt = %q", h.m.input.Value())
	}
}

func TestQuestionCardWithoutOptionsOffersOwnAnswerOnly(t *testing.T) {
	h := askHarness(t, workspace.Report{Status: workspace.ReportQuestion, Summary: "s", Question: "Name it?"})
	h.want("› 1. Type your own answer")
	h.wantNot("2. ")
	h.key("enter")
	if h.m.input.Value() != "@T1 " {
		t.Errorf("prompt = %q", h.m.input.Value())
	}
}

func TestQuestionCardEscHidesAndAnswerBringsItBack(t *testing.T) {
	h := askHarness(t, portQuestion)
	h.key("esc")
	h.wantNot("Type your own answer")
	if h.m.g.Node("T1").Status != workspace.StatusWaiting {
		t.Fatal("the node stays waiting")
	}
	if len(h.m.inboxNeeds()) != 1 {
		t.Error("the inbox item stays")
	}
	h.typeCmd("/answer")
	h.want("Type your own answer")
}

func TestQuestionCardTakesNoKeysWhileTyping(t *testing.T) {
	h := askHarness(t, portQuestion)
	h.m.setFocus(wsFocusPrompt)
	h.key("2")
	if h.m.input.Value() != "2" || h.m.g.Node("T1").Reply != "" {
		t.Errorf("prompt %q, reply %q", h.m.input.Value(), h.m.g.Node("T1").Reply)
	}
}

func TestQuestionAnswerKeepsADraft(t *testing.T) {
	h := askHarness(t, portQuestion)
	h.m.input.SetValue("draft text")
	h.key("1")
	if h.m.input.Value() != "draft text" {
		t.Errorf("prompt = %q", h.m.input.Value())
	}
}

func TestBlockedReportHasNoCard(t *testing.T) {
	h := askHarness(t, workspace.Report{Status: workspace.ReportBlocked, Summary: "no token"})
	h.wantLine("T1 is blocked: no token")
	h.wantNot("Type your own answer")
	if _, _, ok := h.m.activeAsk(); ok {
		t.Error("no card for a blocked node")
	}
}

func TestQuestionCardsOldestFirst(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	for _, id := range []string{"T1", "T2"} {
		n := h.m.g.Node(id)
		n.Status = workspace.StatusWaiting
		n.Report = &workspace.Report{Status: workspace.ReportQuestion, Question: "q of " + id}
	}
	h.m.g.Node("T1").EndedAt = feedT0.Add(time.Minute)
	h.m.g.Node("T2").EndedAt = feedT0
	cards := h.m.askCards()
	if len(cards) != 2 || cards[0].Node != "T2" || cards[1].Node != "T1" {
		t.Fatalf("cards = %+v", cards)
	}
}

func TestDeniedCardAllowForNodeAndRetry(t *testing.T) {
	h := newWSHarness(t)
	h.exec.denied = map[string][]runner.Denial{"T1": {{Tool: "Bash", Command: "cmake --build build -j8"}}}
	h.toGraph()
	h.key("A", "r")
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
	h.drain()
	h.m.showGraph = false
	h.m.setFocus(wsFocusGraph)
	for _, want := range []string{"⚠ T1 was refused 1 command", "cmake --build build -j8", "› 1. Allow for T1 and retry (Recommended)",
		"2. Always allow `cmake` in this project and retry", "3. No"} {
		h.want(want)
	}
	h.key("enter")
	n := h.m.g.Node("T1")
	if n.Status != workspace.StatusRunning || len(n.ExtraAllowed) != 1 || n.ExtraAllowed[0] != "cmake" ||
		n.Reply != "The command `cmake --build build -j8` is now allowed. Run it and continue." {
		t.Fatalf("node = %+v", n)
	}
	if len(h.m.cfg.Project.AllowedCommands) != 0 {
		t.Error("the project list is untouched")
	}
}

func TestDeniedCardAlwaysAllowSavesTheProject(t *testing.T) {
	h := newWSHarness(t)
	h.exec.denied = map[string][]runner.Denial{"T1": {{Tool: "Bash", Command: "FOO=1 make all && ctest"}}}
	h.toGraph()
	h.key("A", "r")
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
	h.drain()
	h.m.showGraph = false
	h.m.setFocus(wsFocusGraph)
	h.want("Always allow `make`, `ctest` in this project and retry")
	h.key("2")
	data, err := os.ReadFile(filepath.Join(h.root, ".decsr", "project.yaml"))
	if err != nil || !strings.Contains(string(data), "make") || !strings.Contains(string(data), "ctest") {
		t.Fatalf("project.yaml: %v\n%s", err, data)
	}
	n := h.m.g.Node("T1")
	if len(n.ExtraAllowed) != 2 || n.Status != workspace.StatusRunning {
		t.Errorf("node = %+v", n)
	}
	if got := h.m.cfg.Project.AllowedCommands; len(got) != 2 {
		t.Errorf("config = %v", got)
	}
}

func TestDeniedCardNoHidesIt(t *testing.T) {
	h := newWSHarness(t)
	h.exec.denied = map[string][]runner.Denial{"T1": {{Tool: "Bash", Command: "make"}}}
	h.toGraph()
	h.key("A", "r")
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
	h.drain()
	h.m.showGraph = false
	h.m.setFocus(wsFocusGraph)
	h.key("3")
	h.wantNot("was refused")
	if n := h.m.g.Node("T1"); len(n.ExtraAllowed) != 0 || n.Status != workspace.StatusDone {
		t.Errorf("node = %+v", n)
	}
}

func TestDeniedNonBashAndReadOnlyShowInfoOnly(t *testing.T) {
	h := newWSHarness(t)
	h.exec.denied = map[string][]runner.Denial{"T1": {{Tool: "Write"}, {Tool: "WebFetch"}}}
	h.toGraph()
	h.key("A", "r")
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
	h.drain()
	h.wantLine("T1 was refused Write, WebFetch: the runner did not allow it")
	if _, _, ok := h.m.activeAsk(); ok {
		t.Error("no card for tools other than Bash")
	}

	n := h.m.g.Node("T1")
	n.Denied = []runner.Denial{{Tool: "Bash", Command: "make"}}
	if _, _, ok := h.m.activeAsk(); !ok {
		t.Fatal("a writing role gets the card")
	}
	n.Role = "security"
	if r, ok := workspace.FindRole(h.m.cfg, "security"); !ok || !r.ReadOnly {
		t.Fatal("the security role must be read-only for this test")
	}
	if _, _, ok := h.m.activeAsk(); ok {
		t.Error("never offer allow for a read-only role")
	}
}
