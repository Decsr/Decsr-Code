package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func chatAnswer(text string) func() *workspace.Graph {
	return func() *workspace.Graph { return &workspace.Graph{Task: "t", Answer: text} }
}

func leadLines(h *wsHarness) []string {
	var out []string
	for _, l := range h.m.sess.snapshot().Transcript {
		if l.Who == "lead" {
			out = append(out, l.Text)
		}
	}
	return out
}

func TestLeadChatAnswerCreatesNoNodes(t *testing.T) {
	h := newWSHarness(t)
	h.graph = chatAnswer("It is a Go CLI.")
	h.sendTask("what is this project?")
	if h.m.hasNodes() || h.m.splitting || len(h.m.pendingChanges) != 0 {
		t.Fatalf("a chat answer must not create nodes or a card: nodes=%v", h.m.hasNodes())
	}
	if got := leadLines(h); len(got) != 1 || got[0] != "It is a Go CLI." {
		t.Errorf("lead lines = %q", got)
	}
	if transcriptHas(h, "no sub-tasks") || transcriptHas(h, "split into") {
		t.Error("a chat answer is not a split")
	}
	if s := h.m.sess.snapshot(); s.In != 100 || s.Out != 50 {
		t.Errorf("lead usage = %d/%d", s.In, s.Out)
	}
}

func TestLeadAnswerWithNodesSaysReplyThenSplit(t *testing.T) {
	h := newWSHarness(t)
	h.graph = func() *workspace.Graph { g := wsSmallGraph(); g.Chat = "I will add search."; return g }
	h.sendTask("add search")
	lines := leadLines(h)
	if len(lines) < 2 || lines[0] != "I will add search." || !strings.HasPrefix(lines[1], "split into 2 nodes") {
		t.Errorf("lead lines = %q", lines)
	}
}

func TestLeadConversationIsKeptAndResumed(t *testing.T) {
	h := newWSHarness(t)
	h.graph = chatAnswer("one")
	h.leadHook = func(r *workspace.LeadResult) {
		r.Conv = workspace.LeadConv{SessionID: "sess-1", Runner: "claude", Model: "opus"}
		r.Context = 4200
	}
	h.sendTask("first")
	s := h.m.sess.snapshot()
	if s.LeadSessionID != "sess-1" || s.LeadRunner != "claude" || s.LeadModel != "opus" || s.LeadContext != 4200 {
		t.Fatalf("session = %+v", s)
	}
	h.sendTask("second")
	if len(h.convs) != 2 || h.convs[0] != (workspace.LeadConv{}) || h.convs[1].SessionID != "sess-1" {
		t.Errorf("conversations handed to the lead: %+v", h.convs)
	}
	// It survives a save and a reload.
	loaded, err := workspace.LoadSession(h.root, h.sessionIDs()[0])
	if err != nil || loaded.LeadSessionID != "sess-1" || loaded.LeadRunner != "claude" {
		t.Errorf("saved session: %v %+v", err, loaded)
	}
}

func TestLeadSaysWhenTheConversationBeganAgain(t *testing.T) {
	for name, hook := range map[string]func(*workspace.LeadResult){
		"resume failed": func(r *workspace.LeadResult) { r.ResumeFailed = true },
		"agent changed": func(r *workspace.LeadResult) { r.AgentChanged = true },
	} {
		h := newWSHarness(t)
		h.graph = chatAnswer("ok")
		h.leadHook = hook
		h.sendTask("go on")
		h.leadHook = nil
		want := "⚠ lead: could not resume the conversation, started fresh with the summary"
		if name == "agent changed" {
			want = "⚠ lead: the lead agent changed, started a new conversation with the summary"
		}
		if !transcriptHas(h, want) {
			t.Errorf("%s: no chat line", name)
		}
		h.sendTask("again")
		n := 0
		for _, l := range h.m.sess.snapshot().Transcript {
			if strings.HasPrefix(l.Text, "⚠ lead:") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: %d warnings, want one", name, n)
		}
	}
}

func TestLeadActivityLineAndCollapse(t *testing.T) {
	h := newWSHarness(t)
	h.graph = chatAnswer("Here is what I found.")
	h.m.input.SetValue("how does the runner work?")
	h.key("enter")
	if !h.m.splitting {
		t.Fatal("no lead turn started")
	}
	h.m.stream.act.Note(runner.Activity{ID: "1", Kind: runner.ActRead, Title: h.root + "/internal/runner/claude.go"})
	h.want("lead · reading internal/runner/claude.go")
	h.m.stream.act.Note(runner.Activity{ID: "2", Kind: runner.ActSearch, Title: "RateLimit"})
	h.want(`lead · searching "RateLimit"`)
	for _, id := range []string{"3", "4", "5"} {
		h.m.stream.act.Note(runner.Activity{ID: id, Kind: runner.ActRead, Title: "f" + id})
	}
	h.m.stream.act.Note(runner.Activity{ID: "6", Kind: runner.ActSearch, Title: "x"})
	h.finishSplit()
	h.wantNot("lead · searching")
	if !transcriptHas(h, "read 4 files · 2 searches") {
		t.Errorf("no collapsed line:\n%s", h.view())
	}
}

func TestLeadNoToolsNoCollapsedLine(t *testing.T) {
	h := newWSHarness(t)
	h.graph = chatAnswer("hi")
	h.sendTask("hello")
	for _, l := range h.m.sess.snapshot().Transcript {
		if strings.Contains(l.Text, "read ") && strings.Contains(l.Text, "file") {
			t.Errorf("unexpected tools line %q", l.Text)
		}
	}
}

func TestClearStartsFresh(t *testing.T) {
	h := newWSHarness(t)
	h.leadHook = func(r *workspace.LeadResult) {
		r.Conv = workspace.LeadConv{SessionID: "sess-1", Runner: "claude", Model: "opus"}
	}
	h.toGraph()
	h.wsMarkDone("T1", "T2", "T3", "T4", "T5")
	h.m.sess.update(func(s *workspace.Session) { s.Summary = "OLD-SUMMARY" })
	old := h.m.sess.snapshot().ID
	h.key("tab")
	h.typeCmd("/clear")
	if h.m.overlay == wsOverlayConfirm {
		h.key("y")
	}
	s := h.m.sess.snapshot()
	if s.ID == old || s.Summary != "" || len(s.Transcript) != 0 || s.Graph != nil || s.LeadSessionID != "" || s.LeadRunner != "" || s.LeadContext != 0 {
		t.Fatalf("the new session carries the old one: %+v", s)
	}
	if h.m.hasNodes() || h.m.earlierContext() != "" {
		t.Fatal("the new session has nodes or an earlier context")
	}
	h.graph = chatAnswer("fresh")
	h.sendTask("new topic")
	last := len(h.convs) - 1
	if h.convs[last] != (workspace.LeadConv{}) || h.earlier[last] != "" || len(h.existing[last]) != 0 {
		t.Errorf("the first message of the new session carries the old one: conv=%+v summary=%q existing=%v", h.convs[last], h.earlier[last], h.existing[last])
	}
	// The old session is saved and listed, with its conversation.
	saved, err := workspace.LoadSession(h.root, old)
	if err != nil || saved.Summary != "OLD-SUMMARY" || saved.LeadSessionID != "sess-1" || len(saved.Transcript) == 0 {
		t.Errorf("old session: %v %+v", err, saved)
	}
	if got := h.sessionIDs(); len(got) != 2 {
		t.Errorf("sessions = %v", got)
	}
}

func TestClearRefusedWhileTheLeadWorks(t *testing.T) {
	h := newWSHarness(t)
	h.m.input.SetValue("do something")
	h.key("enter")
	id := h.m.sess.snapshot().ID
	h.m.input.SetValue("/clear")
	h.key("enter")
	if h.m.sess.snapshot().ID != id {
		t.Error("/clear replaced the session while the lead was working")
	}
}

func TestCompactSummarizesAndStartsANewConversation(t *testing.T) {
	h := newWSHarness(t)
	h.leadHook = func(r *workspace.LeadResult) {
		r.Conv = workspace.LeadConv{SessionID: "sess-1", Runner: "claude", Model: "opus"}
		r.Context = 42000
	}
	h.graph = indepGraph
	h.toGraph()
	h.wsMarkDone("T1")
	h.key("tab")
	h.compact(" the data model")
	if len(h.compactIns) != 1 {
		t.Fatalf("%d compaction calls", len(h.compactIns))
	}
	for _, want := range []string{"User: Build a todo CLI", "## Focus\nthe data model", "- T1 \"Data model\" [done]"} {
		if !strings.Contains(h.compactIns[0], want) {
			t.Errorf("compaction input lacks %q:\n%s", want, h.compactIns[0])
		}
	}
	s := h.m.sess.snapshot()
	if !strings.HasPrefix(s.Summary, "SUMMARY of the chat") || s.LeadSessionID != "" || s.LeadContext != 0 {
		t.Errorf("session after compact: summary=%q lead=%q ctx=%d", s.Summary, s.LeadSessionID, s.LeadContext)
	}
	if h.m.g.Node("T1") != nil {
		t.Error("the finished node was not folded")
	}
	if !transcriptHas(h, "compacted · ≈42K → 5 tokens") || !transcriptHas(h, "folded 1 finished node") {
		t.Errorf("no result lines:\n%s", h.view())
	}
	// The next message starts a new conversation seeded with the summary.
	h.graph = chatAnswer("ok")
	h.sendTask("next")
	last := len(h.convs) - 1
	if h.convs[last] != (workspace.LeadConv{}) || !strings.Contains(h.earlier[last], "SUMMARY of the chat") {
		t.Errorf("next turn: conv=%+v summary=%q", h.convs[last], h.earlier[last])
	}
	// What the next compaction summarizes starts after this one.
	in := h.m.compactInput("")
	if strings.Contains(in, "Build a todo CLI") || !strings.Contains(in, "User: next") {
		t.Errorf("second compaction input:\n%s", in)
	}
}

func TestCompactEstimatesWithoutALeadTurn(t *testing.T) {
	h := newWSHarness(t)
	h.m.sess.update(func(s *workspace.Session) { s.Summary = strings.Repeat("x", 4000) })
	h.compact("")
	if !transcriptHas(h, "compacted · ≈1K → 5 tokens") {
		t.Errorf("no estimate line:\n%s", h.view())
	}
}

func TestCompactErrorKeepsEverything(t *testing.T) {
	h := newWSHarness(t)
	h.graph = chatAnswer("hi")
	h.leadHook = func(r *workspace.LeadResult) {
		r.Conv = workspace.LeadConv{SessionID: "sess-1", Runner: "claude", Model: "opus"}
	}
	h.sendTask("hello")
	h.compactErr = errors.New("compact: runner down")
	h.compact("")
	s := h.m.sess.snapshot()
	if s.LeadSessionID != "sess-1" || s.Summary != "" || h.m.splitting {
		t.Errorf("a failed compaction changed the session: %+v", s)
	}
	h.want("compact: runner down")
}

func TestCompactNothingToDo(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/compact")
	h.want("nothing to compact")
	if len(h.compactIns) != 0 {
		t.Error("an empty session must not call the lead")
	}
}

func autoCompactHarness(t *testing.T, context int) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.m.cfg.Agents = map[string]config.AgentConfig{"pm": {Runner: "claude", Model: "opus"}}
	h.graph = chatAnswer("ok")
	h.leadHook = func(r *workspace.LeadResult) {
		r.Conv = workspace.LeadConv{SessionID: "sess-1", Runner: "claude", Model: "opus"}
		r.Context = context
	}
	h.sendTask("first")
	return h
}

func TestAutoCompactAboveSeventyPercent(t *testing.T) {
	h := autoCompactHarness(t, 150000)
	h.leadHook = nil
	h.sendTask("second")
	if len(h.compactIns) != 1 {
		t.Fatalf("%d compaction calls, want 1", len(h.compactIns))
	}
	if !transcriptHas(h, "auto › compacted the conversation (≈150K → 5 tokens)") {
		t.Errorf("no auto line:\n%s", h.view())
	}
	if strings.Contains(h.compactIns[0], "second") {
		t.Error("the new message must not be part of what is compacted")
	}
	last := len(h.convs) - 1
	if h.tasks[last] != "second" || h.convs[last] != (workspace.LeadConv{}) || !strings.Contains(h.earlier[last], "SUMMARY of the chat") {
		t.Errorf("message after the compaction: task=%q conv=%+v summary=%q", h.tasks[last], h.convs[last], h.earlier[last])
	}
	if h.m.splitting {
		t.Error("the lead turn did not finish")
	}
}

func TestAutoCompactNotBelowTheLimit(t *testing.T) {
	h := autoCompactHarness(t, 140000) // exactly 70% of 200K
	h.sendTask("second")
	if len(h.compactIns) != 0 || h.convs[len(h.convs)-1].SessionID != "sess-1" {
		t.Errorf("compacted at the limit: calls=%d conv=%+v", len(h.compactIns), h.convs)
	}
}

func TestAutoCompactOffBySetting(t *testing.T) {
	h := autoCompactHarness(t, 190000)
	off := false
	h.m.cfg.Workspace.AutoCompact = &off
	h.sendTask("second")
	if len(h.compactIns) != 0 {
		t.Error("workspace.auto_compact off still compacted")
	}
}

func TestAutoCompactFailureStillSendsTheMessage(t *testing.T) {
	h := autoCompactHarness(t, 190000)
	h.compactErr = errors.New("runner down")
	h.sendTask("second")
	if h.tasks[len(h.tasks)-1] != "second" || !transcriptHas(h, "auto › could not compact the conversation: runner down") {
		t.Errorf("tasks=%v\n%s", h.tasks, h.view())
	}
	if h.convs[len(h.convs)-1].SessionID != "sess-1" {
		t.Error("after a failed compaction the message goes on in the old conversation")
	}
}

func TestAutoCompactEscKeepsTheText(t *testing.T) {
	h := autoCompactHarness(t, 190000)
	h.m.input.SetValue("second")
	h.key("enter")
	if !h.m.splitting || h.m.leadKind != "auto" {
		t.Fatal("the compaction did not start")
	}
	h.key("esc")
	if h.m.splitting || h.m.input.Value() != "second" {
		t.Errorf("esc: splitting=%v input=%q", h.m.splitting, h.m.input.Value())
	}
	if len(h.tasks) != 1 {
		t.Error("a cancelled compaction must not send the message")
	}
}

func TestAutoCompactNeverInUnattendedRuns(t *testing.T) {
	h := autoCompactHarness(t, 190000)
	h.m.auto = &wsAuto{}
	if h.m.autoCompactDue() {
		t.Error("an automation run must not compact by itself")
	}
}

func TestCompactHelpForm(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/help compact")
	h.want("/compact [focus]")
}

func TestStatusLineWaitsForApprovalNotDependencies(t *testing.T) {
	h := approvalHarness(t, config.RoutingAutoRole, 160, 45)
	if len(h.m.g.PendingApproval()) == 0 {
		t.Fatal("the plan card is not waiting")
	}
	short, _, _ := h.m.guidanceParts()
	if short != "Waiting for your approval on the plan card (y / e / n)" {
		t.Errorf("status = %q", short)
	}
	if strings.Contains(short, "dependencies") {
		t.Error("the nodes do not wait on dependencies")
	}
}
