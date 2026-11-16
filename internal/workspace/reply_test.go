package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

// seenExec records the node as the executor got it at every run.
type seenExec struct {
	mu    sync.Mutex
	runs  []Node
	run   func(n *Node, emit func(Event)) (Result, error)
	start chan string
}

func (s *seenExec) Run(ctx context.Context, _ *Graph, n *Node, emit func(Event)) (Result, error) {
	s.mu.Lock()
	s.runs = append(s.runs, *n)
	s.mu.Unlock()
	if s.start != nil {
		s.start <- n.ID
	}
	if s.run != nil {
		return s.run(n, emit)
	}
	return Result{Summary: fmt.Sprintf("ok %s #%d", n.ID, n.Attempt)}, nil
}

func (s *seenExec) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs)
}

func replyEngine(g *Graph, ex Executor) *Engine { return NewEngine(g, ex, 3, "") }

func finishRun(t *testing.T, e *Engine, id string) {
	t.Helper()
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, id)
}

func TestReplyErrors(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1"), {ID: "CHECK", Kind: KindCheck, Status: StatusTodo}}}
	e := replyEngine(g, &seenExec{})
	defer e.StopAll()
	if _, err := e.Reply("T9", "x"); err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Errorf("unknown: %v", err)
	}
	if _, err := e.Reply("CHECK", "x"); err == nil || !strings.Contains(err.Error(), "work or fix node") {
		t.Errorf("check: %v", err)
	}
	if _, err := e.Reply("T1", "  \n "); err == nil {
		t.Error("an empty reply must fail")
	}
}

func TestReplyTodoNeverRun(t *testing.T) {
	ex := &seenExec{}
	g := &Graph{Nodes: []*Node{work("T1"), work("T2", "T1")}}
	e := replyEngine(g, ex)
	defer e.StopAll()

	res, err := e.Reply("T2", "use tabs")
	if err != nil || res.State != ReplyWaiting || !strings.Contains(res.Reason, "T1") {
		t.Fatalf("blocked: %+v %v", res, err)
	}
	if n := e.Snapshot().Node("T2"); n.Reply != "use tabs" || n.Status != StatusTodo || len(n.Attempts) != 0 {
		t.Errorf("node = %+v", n)
	}

	res, err = e.Reply("T1", "be brief")
	if err != nil || res.State != ReplyStarted || res.Attempt != 1 {
		t.Fatalf("ready: %+v %v", res, err)
	}
	waitEvent(t, e, EventDone, "T1")
	if ex.runs[0].Reply != "be brief" || ex.runs[0].Attempt > 1 {
		t.Errorf("first run saw %+v", ex.runs[0])
	}
}

func TestReplyUnassignedWaits(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Status: StatusTodo}}}
	e := replyEngine(g, &seenExec{})
	defer e.StopAll()
	res, err := e.Reply("T1", "hi")
	if err != nil || res.State != ReplyWaiting || !strings.Contains(res.Reason, "role") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestReplyRunningQueuesAndStartsAfterFinish(t *testing.T) {
	for _, how := range []string{"done", "failed", "stopped"} {
		t.Run(how, func(t *testing.T) {
			release := make(chan struct{})
			ex := &seenExec{start: make(chan string, 8)}
			ex.run = func(n *Node, _ func(Event)) (Result, error) {
				if n.Attempt > 1 {
					return Result{Summary: "second"}, nil
				}
				switch how {
				case "failed":
					<-release
					return Result{}, errors.New("boom")
				case "stopped":
					<-release
					return Result{}, context.Canceled
				}
				<-release
				return Result{Summary: "first"}, nil
			}
			g := &Graph{Nodes: []*Node{work("T1"), {ID: "CHECK", Kind: KindCheck, Status: StatusDone, DependsOn: []string{"T1"}}}}
			e := replyEngine(g, ex)
			defer e.StopAll()
			if err := e.Run(context.Background(), "T1"); err != nil {
				t.Fatal(err)
			}
			<-ex.start
			res, err := e.Reply("T1", "one")
			if err != nil || res.State != ReplyQueued || res.Attempt != 2 {
				t.Fatalf("%+v %v", res, err)
			}
			if _, err := e.Reply("T1", "two"); err != nil {
				t.Fatal(err)
			}
			if n := e.Snapshot().Node("T1"); n.PendingReply != "one\n\ntwo" || n.Status != StatusRunning {
				t.Fatalf("pending = %q status %s", n.PendingReply, n.Status)
			}
			if how == "stopped" {
				e.Stop("T1")
			}
			close(release)
			waitTerminal(t, e, 2)
			n := e.Snapshot().Node("T1")
			if n.Status != StatusDone || n.Attempt != 2 || n.Reply != "one\n\ntwo" || n.PendingReply != "" {
				t.Fatalf("node = %+v", n)
			}
			if len(n.Attempts) != 1 || n.Attempts[0].N != 1 {
				t.Fatalf("attempts = %+v", n.Attempts)
			}
			wantStatus := StatusDone
			if how != "done" {
				wantStatus = StatusFailed
			}
			if n.Attempts[0].Status != wantStatus {
				t.Errorf("archived status = %s", n.Attempts[0].Status)
			}
			if how == "failed" && n.Attempts[0].Error != "boom" {
				t.Errorf("archived error = %q", n.Attempts[0].Error)
			}
			if n.Summary != "second" {
				t.Errorf("summary = %q", n.Summary)
			}
			if c := e.Snapshot().Node("CHECK"); c.Status != StatusTodo {
				t.Errorf("CHECK = %s", c.Status)
			}
		})
	}
}

func TestReplyDoneAndFailedStartAttempt2(t *testing.T) {
	for _, st := range []Status{StatusDone, StatusFailed} {
		t.Run(string(st), func(t *testing.T) {
			ex := &seenExec{}
			g := &Graph{Nodes: []*Node{
				{ID: "T1", Title: "t", Kind: KindWork, Role: "coder", Status: st, Summary: "s1", Error: errIf(st), Files: []string{"a.go"}, Commit: "abc"},
				{ID: "CHECK", Kind: KindCheck, Status: StatusFailed, Error: "bad", DependsOn: []string{"T1"}},
			}}
			e := replyEngine(g, ex)
			defer e.StopAll()
			res, err := e.Reply("T1", "again, please")
			if err != nil || res.State != ReplyStarted || res.Attempt != 2 {
				t.Fatalf("%+v %v", res, err)
			}
			waitEvent(t, e, EventDone, "T1")
			n := e.Snapshot().Node("T1")
			if n.Attempt != 2 || n.Reply != "again, please" || n.Summary != "ok T1 #2" || n.Error != "" || len(n.Files) != 0 {
				t.Errorf("node = %+v", n)
			}
			a := n.Attempts[0]
			if a.N != 1 || a.Status != st || a.Summary != "s1" || a.Commit != "abc" {
				t.Errorf("attempt = %+v", a)
			}
			if c := e.Snapshot().Node("CHECK"); c.Status != StatusTodo || c.Error != "" {
				t.Errorf("CHECK = %+v", c)
			}
			if ex.runs[0].Reply != "again, please" || ex.runs[0].Attempt != 2 {
				t.Errorf("executor saw %+v", ex.runs[0])
			}
		})
	}
}

func errIf(st Status) string {
	if st == StatusFailed {
		return "failed once"
	}
	return ""
}

func TestReplyWaitsOnBudgetAndDependencies(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "a", Kind: KindWork, Role: "coder", Status: StatusDone, CostUSD: 5, CostKnown: true},
		{ID: "T2", Title: "b", Kind: KindWork, Role: "coder", Status: StatusDone, DependsOn: []string{"T1"}},
	}}
	ex := &seenExec{}
	e := replyEngine(g, ex)
	defer e.StopAll()
	e.SetBudget(func() Budget { return Budget{LimitUSD: 5} })
	res, err := e.Reply("T1", "more")
	if err != nil || res.State != ReplyWaiting || !strings.Contains(res.Reason, "Budget reached") {
		t.Fatalf("budget: %+v %v", res, err)
	}
	if n := e.Snapshot().Node("T1"); n.Status != StatusTodo || n.Attempt != 2 || n.Reply != "more" || len(n.Attempts) != 1 {
		t.Errorf("node = %+v", n)
	}
	res, err = e.Reply("T2", "and you")
	if err != nil || res.State != ReplyWaiting || !strings.Contains(res.Reason, "T1") {
		t.Fatalf("deps: %+v %v", res, err)
	}
	if ex.count() != 0 {
		t.Error("nothing may have started")
	}
	// Raising the budget lets the waiting node start like any todo node.
	e.SetBudget(func() Budget { return Budget{LimitUSD: 50} })
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	if ex.runs[0].Reply != "more" {
		t.Errorf("run saw %+v", ex.runs[0])
	}
}

func TestReplyKeepsActivityOfLastThreeAttempts(t *testing.T) {
	ex := &seenExec{}
	ex.run = func(n *Node, emit func(Event)) (Result, error) {
		a := runner.Activity{ID: "a", Kind: runner.ActCommand, Title: fmt.Sprintf("go test #%d", n.Attempt), State: runner.ActDone}
		emit(Event{Type: EventActivity, Activity: &a})
		return Result{Summary: "ok"}, nil
	}
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := replyEngine(g, ex)
	defer e.StopAll()
	finishRun(t, e, "T1")
	for i := 0; i < 5; i++ {
		if _, err := e.Reply("T1", fmt.Sprintf("r%d", i)); err != nil {
			t.Fatal(err)
		}
		waitEvent(t, e, EventDone, "T1")
	}
	n := e.Snapshot().Node("T1")
	if n.Attempt != 6 || len(n.Attempts) != 5 {
		t.Fatalf("attempt %d, archived %d", n.Attempt, len(n.Attempts))
	}
	for i, a := range n.Attempts {
		kept := len(a.Activity) > 0
		if want := i >= 2; kept != want {
			t.Errorf("attempt %d: activity kept = %v, want %v", a.N, kept, want)
		}
		if a.Summary != "ok" || a.Status != StatusDone {
			t.Errorf("attempt %d lost its summary: %+v", a.N, a)
		}
	}
	if n.Attempts[4].Activity[0].Title != "go test #5" {
		t.Errorf("activity = %+v", n.Attempts[4].Activity)
	}
}

func TestReplyNotesStaleDependents(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		work("T1"), work("T5", "T1"), work("T6", "T1"), work("T7", "T1"),
	}}
	for _, id := range []string{"T1", "T5", "T6"} {
		g.Node(id).Status = StatusDone
	}
	e := replyEngine(g, &seenExec{})
	defer e.StopAll()
	if _, err := e.Reply("T1", "redo"); err != nil {
		t.Fatal(err)
	}
	var got string
	waitDone := false
	for !waitDone {
		ev := <-e.Events()
		if ev.Type == EventOutput && ev.NodeID == "T1" && strings.Contains(ev.Text, "depend on") {
			got = ev.Text
		}
		waitDone = ev.Type == EventDone && ev.NodeID == "T1"
	}
	if got != "T5, T6 depend on T1 and were not re-run\n" {
		t.Errorf("note = %q", got)
	}
}

func TestReplyAtStopAllKeepsReplyAsTodo(t *testing.T) {
	ex := &seenExec{start: make(chan string, 4)}
	release := make(chan struct{})
	ex.run = func(n *Node, _ func(Event)) (Result, error) { <-release; return Result{}, context.Canceled }
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := replyEngine(g, ex)
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	<-ex.start
	if _, err := e.Reply("T1", "later"); err != nil {
		t.Fatal(err)
	}
	close(release)
	e.StopAll()
	n := e.Snapshot().Node("T1")
	if n.Status != StatusTodo || n.Reply != "later" || n.Attempt != 2 || ex.count() != 1 {
		t.Errorf("node = %+v runs %d", n, ex.count())
	}
}

func TestCloneAndAppendCopyAttempts(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Kind: KindWork, Attempts: []Attempt{{N: 1, Activity: []runner.Activity{{ID: "a"}}, FileStats: []FileStat{{Path: "x"}}}}}}}
	c := g.Clone()
	c.Nodes[0].Attempts[0].Activity[0].ID = "changed"
	c.Nodes[0].Attempts[0].FileStats[0].Path = "changed"
	if g.Nodes[0].Attempts[0].Activity[0].ID != "a" || g.Nodes[0].Attempts[0].FileStats[0].Path != "x" {
		t.Error("Clone shares attempts")
	}
	dst := &Graph{}
	dst.Append(g, 1)
	dst.Nodes[0].Attempts[0].Activity[0].ID = "changed"
	if g.Nodes[0].Attempts[0].Activity[0].ID != "a" || len(dst.Nodes[0].Attempts) != 1 {
		t.Error("Append shares attempts")
	}
}

func TestSessionJSONAttemptsCompat(t *testing.T) {
	old := `{"task":"t","nodes":[{"id":"T1","title":"x","description":"","kind":"work","status":"done"}]}`
	var g Graph
	if err := json.Unmarshal([]byte(old), &g); err != nil {
		t.Fatal(err)
	}
	n := g.Node("T1")
	if n.AttemptNumber() != 1 || n.Reply != "" || len(n.Attempts) != 0 {
		t.Errorf("old node = %+v", n)
	}
	data, _ := json.Marshal(&g)
	for _, k := range []string{"attempt", "reply", "pending_reply", "attempts"} {
		if strings.Contains(string(data), `"`+k+`"`) {
			t.Errorf("%s must be omitted when empty: %s", k, data)
		}
	}
	n.Attempt, n.Reply, n.PendingReply = 2, "r", "p"
	n.Attempts = []Attempt{{N: 1, Status: StatusFailed, Error: "e", Activity: []runner.Activity{{ID: "a", Title: "t"}}}}
	data, _ = json.Marshal(&g)
	var back Graph
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	b := back.Node("T1")
	if b.Attempt != 2 || b.Reply != "r" || b.PendingReply != "p" || len(b.Attempts) != 1 || b.Attempts[0].Activity[0].Title != "t" || b.Attempts[0].Error != "e" {
		t.Errorf("round trip = %+v", b)
	}
}

// ── ReplyExcerpt ─────────────────────────────────────────────────────────────

func exit(n int) *int { return &n }

func TestReplyExcerptPriorityAndExclusions(t *testing.T) {
	prev := Attempt{N: 1, Status: StatusFailed, Summary: "added the parser", Error: "exit status 1", Activity: []runner.Activity{
		{ID: "1", Kind: runner.ActThinking, Title: "thinking", Detail: "SECRET REASONING"},
		{ID: "2", Kind: runner.ActCommand, Title: "go build", State: runner.ActDone, Detail: "built"},
		{ID: "3", Kind: runner.ActCommand, Title: "go test", State: runner.ActFailed, ExitCode: exit(1), Detail: "FAIL parser_test"},
		{ID: "4", Kind: runner.ActError, Title: "tool error", Detail: "denied"},
		{ID: "5", Kind: runner.ActEdit, Title: "parser.go", State: runner.ActDone},
		{ID: "6", Kind: runner.ActMessage, Title: "msg", Detail: "I could not make the test pass"},
	}, FileStats: []FileStat{{Path: "parser.go"}, {Path: "go.mod"}}}
	out := ReplyExcerpt(&Node{ID: "T1"}, prev, ReplyContextChars, nil)
	if strings.Contains(out, "SECRET REASONING") {
		t.Error("reasoning must be excluded")
	}
	order := []string{"Summary: added the parser", "$ go test (exit 1)", "$ go build", "error: tool error", "Files touched: parser.go, go.mod", "Last message: I could not make the test pass"}
	last := -1
	for _, s := range order {
		i := strings.Index(out, s)
		if i < 0 || i < last {
			t.Fatalf("%q missing or out of order in:\n%s", s, out)
		}
		last = i
	}
	if strings.Contains(out, "Error: exit status 1") {
		t.Error("the node error repeats a failed command and must be left out")
	}
}

func TestReplyExcerptUsesFullDetail(t *testing.T) {
	prev := Attempt{Activity: []runner.Activity{{ID: "3", Kind: runner.ActCommand, Title: "go test", State: runner.ActFailed, Detail: "short"}}}
	detail := func(node, act string) (string, bool) {
		if node == "T1" && act == "3" {
			return "the full output", true
		}
		return "", false
	}
	if out := ReplyExcerpt(&Node{ID: "T1"}, prev, 500, detail); !strings.Contains(out, "the full output") {
		t.Errorf("out = %q", out)
	}
}

func TestReplyExcerptBudget(t *testing.T) {
	long := strings.Repeat("x", 5000) + "ünï€"
	var acts []runner.Activity
	for i := 0; i < 60; i++ {
		acts = append(acts, runner.Activity{ID: fmt.Sprint(i), Kind: runner.ActCommand, Title: "cmd " + long[:50], State: runner.ActFailed, Detail: long})
	}
	acts = append(acts, runner.Activity{ID: "m", Kind: runner.ActMessage, Detail: long})
	prev := Attempt{Summary: long, Error: long, Activity: acts}
	for _, max := range []int{1, 50, 200, 1000, ReplyContextChars} {
		out := ReplyExcerpt(&Node{ID: "T1"}, prev, max, nil)
		if n := utf8.RuneCountInString(out); n > max {
			t.Errorf("max %d: %d characters", max, n)
		}
		if !utf8.ValidString(out) {
			t.Errorf("max %d: invalid UTF-8", max)
		}
	}
	out := ReplyExcerpt(&Node{ID: "T1"}, prev, ReplyContextChars, nil)
	if !strings.Contains(out, "characters omitted]") {
		t.Error("a cut excerpt must say so")
	}
	if ReplyExcerpt(&Node{ID: "T1"}, prev, 0, nil) != "" {
		t.Error("a zero budget gives nothing")
	}
	// The last message is cut to 1000 characters.
	msg := ReplyExcerpt(&Node{ID: "T1"}, Attempt{Activity: []runner.Activity{{ID: "m", Kind: runner.ActMessage, Detail: long}}}, ReplyContextChars, nil)
	if utf8.RuneCountInString(msg) > len("Last message: ")+replyMessageChars {
		t.Errorf("last message not capped: %d", utf8.RuneCountInString(msg))
	}
}

func FuzzReplyExcerpt(f *testing.F) {
	f.Add("sum", "err", "out", "msg", 300)
	f.Add(strings.Repeat("é", 4000), "", strings.Repeat("\n", 100), "x", 7)
	f.Fuzz(func(t *testing.T, sum, errText, out, msg string, max int) {
		if max > 20000 {
			max = 20000
		}
		prev := Attempt{Summary: sum, Error: errText, Activity: []runner.Activity{
			{ID: "1", Kind: runner.ActCommand, Title: out, State: runner.ActFailed, Detail: out},
			{ID: "2", Kind: runner.ActMessage, Detail: msg},
		}}
		got := ReplyExcerpt(&Node{ID: "T1"}, prev, max, nil)
		if max <= 0 {
			if got != "" {
				t.Fatalf("max %d gave %q", max, got)
			}
			return
		}
		if utf8.ValidString(sum) && utf8.ValidString(errText) && utf8.ValidString(out) && utf8.ValidString(msg) && !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8 out of valid input")
		}
		if n := utf8.RuneCountInString(got); n > max {
			t.Fatalf("%d characters, max %d", n, max)
		}
	})
}

// ── Context and isolation ────────────────────────────────────────────────────

func TestWorkContextHasPreviousAttemptAndReply(t *testing.T) {
	x := &executor{projectContext: "PROJ"}
	n := &Node{ID: "T1", Title: "t", Kind: KindWork, Attempt: 2, Reply: "please use tabs",
		Attempts: []Attempt{{N: 1, Status: StatusFailed, Summary: "did a thing", Activity: []runner.Activity{
			{ID: "c", Kind: runner.ActCommand, Title: "go vet", State: runner.ActFailed, Detail: "vet complaint"},
			{ID: "t", Kind: runner.ActThinking, Detail: "PRIVATE"},
		}}}}
	g := &Graph{Nodes: []*Node{n}}
	ctx := x.workContext(g, n)
	for _, want := range []string{"Previous attempt (attempt 1, failed):", "did a thing", "vet complaint", "The user replied:\nplease use tabs"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context lacks %q:\n%s", want, ctx)
		}
	}
	if strings.Contains(ctx, "PRIVATE") {
		t.Error("reasoning leaked into the context")
	}
	if strings.Index(ctx, "Previous attempt") > strings.Index(ctx, "The user replied") {
		t.Error("the previous attempt comes first")
	}

	first := &Node{ID: "T2", Kind: KindWork, Reply: "only this"}
	ctx = x.workContext(&Graph{Nodes: []*Node{first}}, first)
	if strings.Contains(ctx, "Previous attempt") || !strings.Contains(ctx, "The user replied:\nonly this") {
		t.Errorf("first-run reply context:\n%s", ctx)
	}

	plain := &Node{ID: "T3", Kind: KindWork}
	if strings.Contains(x.workContext(&Graph{Nodes: []*Node{plain}}, plain), "The user replied") {
		t.Error("no reply, no section")
	}
}

// readsFile is an LLM that records the file it finds in its working directory
// when asked, then answers with one file block.
type readsFile struct {
	mu    sync.Mutex
	seen  []string
	reads string
	path  string
	body  string
}

func (r *readsFile) Complete(_ context.Context, req runner.CompletionRequest) (<-chan runner.Token, error) {
	data, _ := os.ReadFile(req.Dir + "/" + r.reads)
	r.mu.Lock()
	r.seen = append(r.seen, string(data))
	r.mu.Unlock()
	ch := make(chan runner.Token, 2)
	ch <- runner.Token{Text: "**" + r.path + "**\n```go\n" + r.body + "```\n===SUMMARY===\nwrote " + r.path}
	ch <- runner.Token{Done: true}
	close(ch)
	return ch, nil
}

func TestReplyIsolatedAttemptStartsFromIntegrationTip(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	llm := &readsFile{reads: "c.go", path: "c.go", body: "package c\n\nvar C = 1\n"}
	ex := &executor{
		root: repo, cfg: textModeConfig(), iso: iso,
		newRunner: func(config.AgentConfig) (runner.LLMRunner, error) { return llm, nil },
	}
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Add c", Kind: KindWork, Role: "coder", Status: StatusTodo}}}
	e := NewEngine(g, ex, 2, "")
	defer e.StopAll()
	finishRun(t, e, "T1")
	if out := gitT(t, repo, "show", iso.Branch()+":c.go"); !strings.Contains(out, "C = 1") {
		t.Fatalf("attempt 1 not merged: %q", out)
	}

	llm.mu.Lock()
	llm.body = "package c\n\nvar C = 2\n"
	llm.mu.Unlock()
	res, err := e.Reply("T1", "make C 2")
	if err != nil || res.State != ReplyStarted {
		t.Fatalf("%+v %v", res, err)
	}
	waitEvent(t, e, EventDone, "T1")
	if len(llm.seen) != 2 || llm.seen[0] != "" || !strings.Contains(llm.seen[1], "C = 1") {
		t.Fatalf("attempt 2 must start from the integration tip holding attempt 1: %q", llm.seen)
	}
	if out := gitT(t, repo, "show", iso.Branch()+":c.go"); !strings.Contains(out, "C = 2") {
		t.Errorf("attempt 2 not merged again: %q", out)
	}
	n := e.Snapshot().Node("T1")
	if n.Attempt != 2 || len(n.Attempts) != 1 || n.Attempts[0].Commit == "" || n.Commit == "" || n.Commit == n.Attempts[0].Commit {
		t.Errorf("node = %+v", n)
	}
}
