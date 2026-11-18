package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

// resumeFake answers each call from its script: an id to report and whether a
// resume fails at start.
type resumeFake struct {
	mu         sync.Mutex
	reqs       []runner.CompletionRequest
	session    string
	failResume bool
}

func (f *resumeFake) Complete(_ context.Context, req runner.CompletionRequest) (<-chan runner.Token, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	ch := make(chan runner.Token, 5)
	if req.ResumeID != "" && f.failResume {
		ch <- runner.Token{Error: &runner.ResumeError{Err: errNoSession}}
		close(ch)
		return ch, nil
	}
	if f.session != "" {
		ch <- runner.Token{SessionID: f.session}
	}
	ch <- runner.Token{Text: "done\n===SUMMARY===\nok"}
	ch <- runner.Token{Done: true, Usage: &runner.TokenUsage{InputTokens: 1, OutputTokens: 1}}
	close(ch)
	return ch, nil
}

type noSession struct{}

func (noSession) Error() string { return "session not found" }

var errNoSession = noSession{}

func resumeNode() *Node {
	return &Node{ID: "T1", Title: "Docs", Description: "write", Kind: KindWork, Role: "docs_writer",
		Attempt: 2, Reply: "also add a section", SessionID: "S1", SessionRunner: "claude", SessionAttempt: 1,
		Attempts: []Attempt{{N: 1, Status: StatusDone, Summary: "wrote the intro"}}}
}

func resumeExec(t *testing.T, f *resumeFake, runnerName string) *executor {
	cfg := config.DefaultConfig()
	cfg.Agents["docs_writer"] = config.AgentConfig{Runner: runnerName, Model: "m1", Description: "Writes docs.", Mode: config.ModeAgent}
	ex := newTestExec(t, cfg, &execFake{}, nil)
	ex.newRunner = func(config.AgentConfig) (runner.LLMRunner, error) { return f, nil }
	return ex
}

func TestFirstAttemptKeepsAndReportsItsSession(t *testing.T) {
	f := &resumeFake{session: "S1"}
	ex := resumeExec(t, f, "claude")
	n := &Node{ID: "T1", Title: "Docs", Kind: KindWork, Role: "docs_writer"}
	var evs []Event
	if _, err := ex.Run(context.Background(), &Graph{Nodes: []*Node{n}}, n, collect(&evs)); err != nil {
		t.Fatal(err)
	}
	if !f.reqs[0].KeepSession || f.reqs[0].ResumeID != "" {
		t.Errorf("request = %+v", f.reqs[0])
	}
	var got *Event
	for i := range evs {
		if evs[i].Type == EventSession {
			got = &evs[i]
		}
	}
	if got == nil || got.Text != "S1" || got.Runner != "claude" || got.NodeID != "T1" {
		t.Errorf("session event = %+v", got)
	}
}

func TestLaterAttemptResumesAndSendsOnlyTheReply(t *testing.T) {
	f := &resumeFake{session: "S1"}
	ex := resumeExec(t, f, "claude")
	n := resumeNode()
	if _, err := ex.Run(context.Background(), &Graph{Nodes: []*Node{n}}, n, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	req := f.reqs[0]
	if req.ResumeID != "S1" || len(req.Messages) != 1 || req.Messages[0].Content != "also add a section" {
		t.Errorf("request = %+v", req)
	}
	if strings.Contains(req.SystemPrompt, "Previous attempt") || strings.Contains(req.SystemPrompt, "wrote the intro") {
		t.Errorf("a resumed attempt gets no excerpt of the earlier one:\n%s", req.SystemPrompt)
	}
	if !req.Agent || len(req.AllowedTools) == 0 {
		t.Errorf("the allow list and agent mode stay: %+v", req)
	}
}

func TestRetryOfTheSameAttemptContinues(t *testing.T) {
	f := &resumeFake{}
	ex := resumeExec(t, f, "claude")
	n := resumeNode()
	n.SessionAttempt = 2 // the session already got the reply: a retry after a limit
	if _, err := ex.Run(context.Background(), &Graph{Nodes: []*Node{n}}, n, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if got := f.reqs[0]; got.ResumeID != "S1" || got.Messages[0].Content != continueMessage {
		t.Errorf("request = %+v", got)
	}
}

func TestNoResumeForAnotherRunnerOrWithoutSession(t *testing.T) {
	for name, mut := range map[string]func(n *Node){
		"other runner": func(n *Node) { n.SessionRunner = "codex" },
		"no session":   func(n *Node) { n.SessionID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			f := &resumeFake{}
			ex := resumeExec(t, f, "claude")
			n := resumeNode()
			mut(n)
			if _, err := ex.Run(context.Background(), &Graph{Nodes: []*Node{n}}, n, func(Event) {}); err != nil {
				t.Fatal(err)
			}
			req := f.reqs[0]
			if req.ResumeID != "" || !strings.Contains(req.SystemPrompt, "Previous attempt") || !strings.Contains(req.SystemPrompt, "also add a section") {
				t.Errorf("a fresh attempt keeps the excerpt: %+v", req)
			}
		})
	}
}

func TestResumeFailureFallsBackToAFreshAttemptWithTheSummary(t *testing.T) {
	f := &resumeFake{failResume: true, session: "S2"}
	ex := resumeExec(t, f, "claude")
	n := resumeNode()
	var evs []Event
	if _, err := ex.Run(context.Background(), &Graph{Nodes: []*Node{n}}, n, collect(&evs)); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 2 || f.reqs[0].ResumeID != "S1" || f.reqs[1].ResumeID != "" {
		t.Fatalf("requests = %+v", f.reqs)
	}
	if p := f.reqs[1].SystemPrompt; !strings.Contains(p, "Previous attempt") || !strings.Contains(p, "wrote the intro") || !strings.Contains(p, "also add a section") {
		t.Errorf("the fresh attempt needs the summary:\n%s", p)
	}
	warns := 0
	for _, e := range evs {
		if e.Type == EventResumeFailed {
			warns++
			if want := "⚠ T1: could not resume its conversation, started fresh with a summary"; e.Text != want {
				t.Errorf("warning = %q", e.Text)
			}
		}
	}
	if warns != 1 {
		t.Errorf("want one warning, got %d", warns)
	}
}

func TestNodeSessionJSONRoundTrip(t *testing.T) {
	n := resumeNode()
	n.Attempts[0].SessionID = "S0"
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var back Node
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.SessionID != "S1" || back.SessionRunner != "claude" || back.SessionAttempt != 1 || back.Attempts[0].SessionID != "S0" {
		t.Errorf("round trip = %+v", back)
	}
	if b, _ := json.Marshal(&Node{ID: "T2"}); strings.Contains(string(b), "session") {
		t.Errorf("an empty session is not written: %s", b)
	}
}

func TestEngineKeepsSessionAndArchivesItPerAttempt(t *testing.T) {
	ex := &seenExec{run: func(n *Node, emit func(Event)) (Result, error) {
		emit(Event{NodeID: n.ID, Type: EventSession, Text: "S-x", Runner: "claude"})
		return Result{Summary: "ok"}, nil
	}}
	e := replyEngine(&Graph{Nodes: []*Node{work("T1")}}, ex)
	defer e.StopAll()
	finishRun(t, e, "T1")
	if n := e.Snapshot().Node("T1"); n.SessionID != "S-x" || n.SessionRunner != "claude" || n.SessionAttempt != 1 {
		t.Fatalf("after the first attempt: %+v", n)
	}
	if _, err := e.Reply("T1", "more"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	n := e.Snapshot().Node("T1")
	if len(n.Attempts) != 1 || n.Attempts[0].SessionID != "S-x" || n.SessionAttempt != 2 {
		t.Errorf("archived attempt / session = %+v", n)
	}
	if ex.runs[1].SessionID != "S-x" || ex.runs[1].SessionAttempt != 1 {
		t.Errorf("the next attempt starts with the node's session: %+v", ex.runs[1])
	}
}
