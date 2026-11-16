package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func TestParseReply(t *testing.T) {
	for _, c := range []struct {
		in, id, msg string
		ok          bool
	}{
		{"@T3 fix it", "T3", "fix it", true},
		{"@T3   spaced  ", "T3", "spaced", true},
		{"@F12 line one\nline two", "F12", "line one\nline two", true},
		{"@CHECK why", "CHECK", "why", true},
		{"@T3", "", "", false},
		{"@T3 ", "", "", false},
		{"@ T3 x", "", "", false},
		{"hello @T3 x", "", "", false},
		{"/help", "", "", false},
	} {
		id, msg, ok := parseReply(c.in)
		if id != c.id || msg != c.msg || ok != c.ok {
			t.Errorf("parseReply(%q) = %q %q %v", c.in, id, msg, ok)
		}
	}
}

// replyHarness is a harness whose T1 ran and is done, with the prompt in focus.
func replyHarness(t *testing.T) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
	return h
}

func (h *wsHarness) submitPrompt(text string) {
	h.t.Helper()
	h.m.setFocus(wsFocusPrompt)
	h.m.input.SetValue(text)
	h.key("enter")
}

func TestReplySubmitStartsNewAttempt(t *testing.T) {
	h := replyHarness(t)
	h.submitPrompt("@T1 please add\ntests")
	if h.m.input.Value() != "" {
		t.Errorf("prompt = %q", h.m.input.Value())
	}
	tr := h.m.sess.snapshot().Transcript
	if got := tr[len(tr)-2]; got.Who != "you" || got.Text != "→ T1: please add …" {
		t.Errorf("conversation line = %+v", got)
	}
	if got := lastLine(h.m); got != "T1 attempt 2 started" {
		t.Errorf("result line = %q", got)
	}
	n := h.m.g.Node("T1")
	if n.Status != workspace.StatusRunning || n.Attempt != 2 || n.Reply != "please add\ntests" || len(n.Attempts) != 1 {
		t.Fatalf("node = %+v", n)
	}
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool {
		return m.g.Node("T1").Status == workspace.StatusDone && m.g.Node("T1").Attempt == 2
	})
	if rows := ansi.Strip(strings.Join(h.m.sidebarRows(34, 30), "\n")); !strings.Contains(rows, "attempt 2") {
		t.Errorf("sidebar lacks the attempt marker:\n%s", rows)
	}
}

func TestReplyToRunningNodeIsQueuedAndStartsLater(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusRunning })
	h.submitPrompt("@t1 and also this")
	if got := lastLine(h.m); !strings.HasPrefix(got, "queued for T1") {
		t.Errorf("result line = %q", got)
	}
	n := h.m.g.Node("T1")
	if n.PendingReply != "and also this" || n.Attempt > 1 {
		t.Fatalf("node = %+v", n)
	}
	h.m.showGraph = false
	h.m.feedExpanded["T1"] = false
	h.want(`reply queued: "and also this"`)
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return strings.Contains(lastLine(m), "T1 attempt 2 started") })
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
}

func TestReplyWaitingAndErrorsKeepThePrompt(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.submitPrompt("@T2 later")
	if got := lastLine(h.m); !strings.HasPrefix(got, "T2 waiting: ") {
		t.Errorf("result line = %q", got)
	}
	if h.m.g.Node("T2").Reply != "later" {
		t.Error("the reply is kept for the first run")
	}
	for _, text := range []string{"@T9 hi", "@CHECK hi"} {
		h.submitPrompt(text)
		tr := h.m.sess.snapshot().Transcript
		if last := tr[len(tr)-1]; last.Who != "error" {
			t.Errorf("%s: last line = %+v", text, last)
		}
		if h.m.input.Value() != text {
			t.Errorf("%s: the text must stay in the prompt, got %q", text, h.m.input.Value())
		}
	}
	if got := lastLine(h.m); !strings.Contains(got, "work or fix node") {
		t.Errorf("CHECK error = %q", got)
	}
}

func TestMentionPaletteFiltersAndCompletes(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.setFocus(wsFocusPrompt)
	h.key("@")
	for _, want := range []string{"@T1", "Data model", "@T5", "Security pass"} {
		h.want(want)
	}
	h.wantNot("@CHECK")
	h.want("Tab/Enter complete")
	h.key("T", "3")
	h.want("@T3")
	h.wantNot("@T1 ")
	h.key("tab")
	if h.m.input.Value() != "@T3 " {
		t.Fatalf("Tab gave %q", h.m.input.Value())
	}
	h.m.input.Reset()
	h.key("@", "T")
	h.key("down", "enter")
	if h.m.input.Value() != "@T2 " {
		t.Fatalf("Enter on the second item gave %q", h.m.input.Value())
	}
	h.m.input.Reset()
	h.key("@", "Z")
	h.want("no such node")
	h.key("esc")
	if h.m.input.Value() != "" {
		t.Errorf("Esc left %q", h.m.input.Value())
	}
	// The command palette is untouched.
	h.key("/")
	h.want("/help")
	h.wantNot("@T1")
}

func TestReplyKeyFillsPrompt(t *testing.T) {
	for _, feed := range []bool{false, true} {
		h := newWSHarness(t)
		h.toGraph()
		h.m.showGraph = !feed
		h.m.sel = "T2"
		h.key("m")
		if h.m.focus != wsFocusPrompt || h.m.input.Value() != "@T2 " {
			t.Fatalf("feed=%v: focus %v input %q", feed, h.m.focus, h.m.input.Value())
		}
		// A second m with a mention in the prompt replaces the mention.
		h.m.setFocus(wsFocusGraph)
		h.m.sel = "T3"
		h.key("m")
		if h.m.input.Value() != "@T3 " {
			t.Errorf("feed=%v: second m gave %q", feed, h.m.input.Value())
		}
	}
	h := newWSHarness(t)
	h.toGraph()
	h.m.sel = "CHECK"
	h.key("m")
	if h.m.focus == wsFocusPrompt || !strings.Contains(h.m.notice, "work or fix node") {
		t.Errorf("CHECK: focus %v notice %q", h.m.focus, h.m.notice)
	}
	h.m.sel = "T1"
	h.m.setFocus(wsFocusSidebar)
	h.m.input.SetValue("a draft")
	h.key("m")
	if h.m.input.Value() != "a draft" || h.m.focus == wsFocusPrompt {
		t.Errorf("a draft must not be overwritten: %q", h.m.input.Value())
	}
}

func TestReplyHintsAndHelp(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.sel = "T1"
	h.want("m reply")
	h.key("?")
	h.want("@T3 message")
}

// attemptNode is a done node on attempt 2 with one earlier, failed attempt.
func attemptNode() *workspace.Node {
	n := feedTestNode("T1", workspace.StatusDone, feedT0.Add(5*time.Minute))
	n.EndedAt = feedT0.Add(6 * time.Minute)
	n.Summary = "second summary"
	n.Attempt = 2
	n.Reply = "use tabs, please"
	n.Attempts = []workspace.Attempt{{
		N: 1, Reply: "first note", Status: workspace.StatusFailed, StartedAt: feedT0, EndedAt: feedT0.Add(130 * time.Second),
		Error: "go vet failed", Summary: "first summary",
		Activity: []runner.Activity{{ID: "a", Kind: runner.ActCommand, Title: "go vet ./...", State: runner.ActFailed}},
	}}
	return n
}

func TestCardShowsAttemptsAndQueuedReply(t *testing.T) {
	n := attemptNode()
	n.PendingReply = strings.Repeat("long ", 30)
	for _, w := range []int{40, 60, 100} {
		_, o := cardFixture(n)
		o.width = w
		collapsed := cardText(o)
		for _, want := range []string{"attempt 2", `reply queued: "long long`} {
			if !strings.Contains(collapsed, want) {
				t.Errorf("w=%d: card lacks %q:\n%s", w, want, collapsed)
			}
		}
		if strings.Contains(collapsed, "Attempt 1") {
			t.Errorf("w=%d: earlier attempts are folded until the card opens", w)
		}
		o.expanded = true
		checkCardShape(t, fmt.Sprintf("w=%d", w), renderNodeCard(o), w)
		text := cardText(o)
		if w < 60 {
			continue // too narrow for the whole attempt line
		}
		for _, want := range []string{"Attempt 1 · failed · Worked for 2m 10s ›", "first summary", "go vet failed", "go vet ./..."} {
			if !strings.Contains(strings.Join(strings.Fields(text), " "), want) {
				t.Errorf("w=%d: expanded card lacks %q:\n%s", w, want, text)
			}
		}
		if !strings.Contains(collapsed, "…") {
			t.Errorf("w=%d: the queued line is cut with an ellipsis", w)
		}
	}
	_, o := cardFixture(feedTestNode("T2", workspace.StatusDone, feedT0))
	o.width = 60
	if strings.Contains(cardText(o), "attempt") {
		t.Error("a first attempt shows no attempt marker")
	}
}

func TestInspectorShowsAttemptAndReply(t *testing.T) {
	n := attemptNode()
	n.PendingReply = "next one\nsecond line"
	g := &workspace.Graph{Nodes: []*workspace.Node{n}}
	out := ansi.Strip(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, 40, 30, nil))
	for _, want := range []string{"Attempt  2 of 2", "Reply  use tabs, please", "Queued  next one"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspector lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "second line") {
		t.Error("only the first line of a reply is shown")
	}
}

func TestOutputViewSwitchesAttempts(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	n := attemptNode()
	n.Activity = []runner.Activity{{ID: "b", Kind: runner.ActCommand, Title: "go build ./...", State: runner.ActDone}}
	h.m.g.Nodes[0] = n
	h.m.sel = "T1"
	h.key("o")
	h.want("attempt 2 of 2")
	h.want("go build ./...")
	h.wantNot("go vet ./...")
	h.want("[ ] attempt")
	h.key("[")
	h.want("Attempt 1 · failed")
	h.want("go vet ./...")
	h.want("first summary")
	h.want("go vet failed")
	h.want("reply:")
	h.wantNot("go build ./...")
	h.key("[") // already the first
	h.want("Attempt 1 · failed")
	h.key("t") // no raw text for an earlier attempt
	h.want("go vet ./...")
	h.key("]")
	h.want("go build ./...")
	h.wantNot("go vet ./...")
	h.key("]")
	h.want("go build ./...")

	// An attempt whose activity was not kept says so.
	n.Attempts[0].Activity = nil
	h.key("[")
	h.want("activity of this attempt was not kept")

	for _, size := range [][2]int{{140, 34}, {80, 24}, {50, 20}} {
		h.size(size[0], size[1])
		for i, l := range strings.Split(h.m.View(), "\n") {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Fatalf("%dx%d: line %d is %d cells: %q", size[0], size[1], i, w, ansi.Strip(l))
			}
		}
	}
}

func TestReplyViewsFitTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{140, 34}, {100, 30}, {60, 24}, {40, 16}} {
		h := newWSHarness(t)
		h.toGraph()
		h.m.g.Nodes[0] = attemptNode()
		h.m.g.Nodes[0].PendingReply = "queued"
		h.m.sel = "T1"
		h.size(size[0], size[1])
		for _, graph := range []bool{false, true} {
			h.m.showGraph = graph
			h.m.feedExpanded["T1"] = true
			h.m.setFocus(wsFocusPrompt)
			h.m.input.SetValue("@")
			for i, l := range strings.Split(h.m.View(), "\n") {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Fatalf("%dx%d graph=%v: line %d is %d cells: %q", size[0], size[1], graph, i, w, ansi.Strip(l))
				}
			}
		}
	}
}
