package workspace

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/agent"
)

func reportResult(status, summary, question string) func(context.Context) (Result, error) {
	return func(context.Context) (Result, error) {
		return Result{Summary: "sum", Report: Report{Status: status, Summary: summary, Question: question}}, nil
	}
}

func waitFor(t *testing.T, e *Engine, id string, st Status) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		if status(e, id) == st {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never became %s (is %s)", id, st, status(e, id))
}

func TestEngineQuestionAndBlockedEndWaiting(t *testing.T) {
	for _, tc := range []struct{ status, question, text string }{
		{agent.ReportQuestion, "Which db?", "Which db?"},
		{agent.ReportQuestion, "", "why"},
		{agent.ReportBlocked, "", "why"},
	} {
		ex := newFakeExec()
		ex.behavior["T1"] = reportResult(tc.status, "why", tc.question)
		g := &Graph{Nodes: []*Node{work("T1"), work("T2", "T1"), {ID: "CHECK", Kind: KindCheck, Status: StatusTodo, DependsOn: []string{"T1", "T2"}}}}
		e := NewEngine(g, ex, 2, "")
		if err := e.Run(context.Background(), "T1"); err != nil {
			t.Fatal(err)
		}
		ev := waitEvent(t, e, EventWaiting, "T1")
		if ev.Text != tc.text {
			t.Errorf("event text = %q, want %q", ev.Text, tc.text)
		}
		n := e.Snapshot().Node("T1")
		if n.Status != StatusWaiting || n.Report == nil || n.Report.Status != tc.status || n.Summary != "sum" {
			t.Fatalf("node = %+v", n)
		}
		w := e.Waiting()
		if len(w) != 1 || w[0].ID != "T1" {
			t.Fatalf("Waiting() = %v", w)
		}
		w[0].Report.Summary = "changed"
		if e.Snapshot().Node("T1").Report.Summary != "why" {
			t.Error("Waiting() must return copies")
		}
		// Dependents and Build & test stay blocked; run-all starts nothing.
		if b := e.Snapshot().Blockers("T2"); len(b) != 1 || b[0] != "T1" {
			t.Errorf("blockers = %v", b)
		}
		if err := e.Run(context.Background(), "T2"); err == nil || !strings.Contains(err.Error(), "blocked by T1") {
			t.Errorf("run T2: %v", err)
		}
		if err := e.Run(context.Background(), "T1"); err == nil || !strings.Contains(err.Error(), "waiting for your reply") {
			t.Errorf("run T1: %v", err)
		}
		e.RunAll(context.Background())
		time.Sleep(50 * time.Millisecond)
		if status(e, "T2") != StatusTodo || status(e, "CHECK") != StatusTodo || e.Running() != 0 {
			t.Error("a waiting node must keep its dependents blocked")
		}
		e.StopAll()
	}
}

func TestEngineDoneReportAndMissingReport(t *testing.T) {
	ex := newFakeExec()
	ex.behavior["T1"] = reportResult(agent.ReportDone, "all good", "")
	g := &Graph{Nodes: []*Node{work("T1"), work("T2")}}
	e := NewEngine(g, ex, 2, "")
	defer e.StopAll()
	finishRun(t, e, "T1")
	finishRun(t, e, "T2") // fakeExec returns no report
	s := e.Snapshot()
	if s.Node("T1").Status != StatusDone || s.Node("T1").Report == nil || s.Node("T1").Report.Summary != "all good" {
		t.Errorf("T1 = %+v", s.Node("T1"))
	}
	if s.Node("T2").Status != StatusDone || s.Node("T2").Report != nil {
		t.Errorf("T2 = %+v", s.Node("T2"))
	}
}

func TestWaitingNodeDoesNotPauseOthers(t *testing.T) {
	ex := newFakeExec()
	ex.behavior["T1"] = reportResult(agent.ReportQuestion, "s", "q?")
	g := &Graph{Nodes: []*Node{work("T1"), work("T2"), work("T3", "T1")}}
	e := NewEngine(g, ex, 1, "")
	defer e.StopAll()
	e.RunAll(context.Background())
	waitFor(t, e, "T1", StatusWaiting)
	waitFor(t, e, "T2", StatusDone)
	if status(e, "T3") != StatusTodo {
		t.Error("T3 depends on the waiting node")
	}
}

func TestReplyToWaitingNodeStartsAttempt2WithQuestion(t *testing.T) {
	ex := &seenExec{}
	ex.run = func(n *Node, _ func(Event)) (Result, error) {
		if n.Attempt < 2 {
			return Result{Summary: "first", Report: Report{Status: "question", Summary: "need db", Question: "Postgres or SQLite?"}}, nil
		}
		return Result{Summary: "second", Report: Report{Status: "done", Summary: "used it"}}, nil
	}
	g := &Graph{Nodes: []*Node{work("T1"), work("T2", "T1")}}
	e := replyEngine(g, ex)
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventWaiting, "T1")
	res, err := e.Reply("T1", "SQLite")
	if err != nil || res.State != ReplyStarted || res.Attempt != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	waitEvent(t, e, EventDone, "T1")
	n := e.Snapshot().Node("T1")
	if n.Status != StatusDone || n.Report.Summary != "used it" || len(n.Attempts) != 1 {
		t.Fatalf("node = %+v", n)
	}
	a := n.Attempts[0]
	if a.Status != StatusWaiting || a.Report == nil || a.Report.Question != "Postgres or SQLite?" {
		t.Errorf("attempt = %+v", a)
	}
	// The next attempt's context holds the question.
	x := &executor{}
	n.Status = StatusRunning
	ctx := x.workContext(e.Snapshot(), n)
	for _, want := range []string{"Previous attempt (attempt 1, waiting — asked a question):", "Your question was:\nPostgres or SQLite?", "The user replied:\nSQLite"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context lacks %q:\n%s", want, ctx)
		}
	}
	if strings.Index(ctx, "Your question was") > strings.Index(ctx, "The user replied") {
		t.Error("the question comes before the reply")
	}
}

func TestWorkContextBlockedAttempt(t *testing.T) {
	x := &executor{}
	n := &Node{ID: "T1", Kind: KindWork, Attempt: 2, Reply: "token is in .env",
		Attempts: []Attempt{{N: 1, Status: StatusWaiting, Summary: "s", Report: &Report{Status: "blocked", Summary: "no API token"}}}}
	ctx := x.workContext(&Graph{Nodes: []*Node{n}}, n)
	for _, want := range []string{"Previous attempt (attempt 1, waiting — blocked):", "You reported:\nno API token", "The user replied:\ntoken is in .env"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context lacks %q:\n%s", want, ctx)
		}
	}
}

func TestCompactNeverFoldsWaiting(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusDone},
		{ID: "T2", Kind: KindWork, Status: StatusWaiting, DependsOn: []string{"T1"}},
		{ID: "T3", Kind: KindWork, Status: StatusDone},
	}}
	folded := g.Compact()
	if len(folded) != 1 || folded[0].ID != "T3" || g.Node("T2") == nil || g.Node("T1") == nil {
		t.Errorf("folded %v", folded)
	}
}

func TestWaitingCountsAndBudget(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusWaiting, CostUSD: 6, CostKnown: true},
		work("T2"),
	}}
	if g.CountWaiting() != 1 {
		t.Error("CountWaiting")
	}
	if todo, running, done, failed := g.Counts(); todo != 1 || running+done+failed != 0 {
		t.Errorf("Counts must not include waiting: %d %d %d %d", todo, running, done, failed)
	}
	e := NewEngine(g, newFakeExec(), 1, "")
	e.SetBudget(func() Budget { return Budget{LimitUSD: 5} })
	if err := e.Run(context.Background(), "T2"); err == nil || !strings.Contains(err.Error(), "Budget reached") {
		t.Errorf("the cost of a waiting node counts: %v", err)
	}
}

func TestPRBodyListsWaiting(t *testing.T) {
	s := Session{ID: "sid", Graph: &Graph{Nodes: []*Node{
		{ID: "T1", Title: "a", Kind: KindWork, Status: StatusWaiting, Summary: "partial", Report: &Report{Status: "question", Question: "Which db?"}},
		{ID: "CHECK", Kind: KindCheck, Status: StatusTodo},
	}}}
	body := PRBody(s)
	if !strings.Contains(body, "| T1 | a | - | waiting |") || !strings.Contains(body, "## Waiting for an answer\n\n- T1: Which db?") {
		t.Errorf("body:\n%s", body)
	}
	if strings.Contains(body, "## Changes") {
		t.Error("a waiting node is not a finished change")
	}
}

func TestSessionJSONReportCompat(t *testing.T) {
	var g Graph
	old := `{"nodes":[{"id":"T1","title":"x","kind":"work","status":"done"}]}`
	if err := json.Unmarshal([]byte(old), &g); err != nil || g.Node("T1").Report != nil {
		t.Fatalf("%v %+v", err, g.Node("T1"))
	}
	data, _ := json.Marshal(&g)
	if strings.Contains(string(data), `"report"`) {
		t.Errorf("report must be omitted when nil: %s", data)
	}
	n := g.Node("T1")
	n.Status = StatusWaiting
	n.Report = &Report{Status: "question", Summary: "s", Question: "q?"}
	n.Attempts = []Attempt{{N: 1, Report: &Report{Status: "blocked"}}}
	data, _ = json.Marshal(&g)
	var back Graph
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	b := back.Node("T1")
	if b.Status != StatusWaiting || !reflect.DeepEqual(*b.Report, *n.Report) || b.Attempts[0].Report.Status != "blocked" {
		t.Errorf("round trip = %+v", b)
	}
	c := g.Clone()
	c.Nodes[0].Report.Summary = "changed"
	c.Nodes[0].Attempts[0].Report.Status = "changed"
	if n.Report.Summary != "s" || n.Attempts[0].Report.Status != "blocked" {
		t.Error("Clone shares reports")
	}
	dst := &Graph{}
	dst.Append(&g, 1)
	dst.Nodes[0].Report.Summary = "changed"
	if n.Report.Summary != "s" {
		t.Error("Append shares reports")
	}
	if err := back.prepare("x"); err != nil {
		t.Errorf("a waiting node must load: %v", err)
	}
}

func TestExecWaitingNodeMergesItsChanges(t *testing.T) {
	repo := newRepo(t)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Add c", Kind: KindWork, Role: "coder"}}}
	f := &execFake{text: "**c.go**\n```go\npackage c\n```\n===SUMMARY===\nwrote c.go, db unclear\n\nREPORT\nstatus: question\nsummary: partial\nquestion: Which database?\n"}
	iso := newIso(t, repo)
	ex := isolatedExec(t, repo, iso, f)
	e := NewEngine(g, ex, 1, "")
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, e, EventWaiting, "T1")
	if ev.Text != "Which database?" {
		t.Errorf("event text = %q", ev.Text)
	}
	n := e.Snapshot().Node("T1")
	if n.Status != StatusWaiting || n.Commit == "" || len(n.Files) != 1 {
		t.Fatalf("node = %+v", n)
	}
	if strings.Contains(n.Summary, "REPORT") || n.Summary != "wrote c.go, db unclear" {
		t.Errorf("summary = %q", n.Summary)
	}
	if out := gitT(t, repo, "show", iso.Branch()+":c.go"); out != "package c" {
		t.Errorf("c.go on the integration branch = %q", out)
	}
}
