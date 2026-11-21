package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestIsDocFile(t *testing.T) {
	for p, want := range map[string]bool{
		"README.md": true, "notes/Design.MD": true, "doc/workspace.txt": true, "docs/a/b.png": true,
		"LICENSE": true, "LICENSE-MIT": true, "sub/license.txt": true,
		"main.go": false, "internal/docs/x.go": false, "docker/Dockerfile": false, "go.mod": false,
	} {
		if got := IsDocFile(p); got != want {
			t.Errorf("IsDocFile(%q) = %v, want %v", p, got, want)
		}
	}
}

func gateGraph(check *Node, ended time.Time, files []string, stats ...FileStat) *Graph {
	w := &Node{ID: "T1", Title: "t", Kind: KindWork, Status: StatusDone, EndedAt: ended, Files: files, FileStats: stats}
	return &Graph{Nodes: []*Node{w, check}}
}

func TestChangedSinceCheck(t *testing.T) {
	now := time.Now()
	check := func(last time.Time) *Node { return &Node{ID: "CHECK", Kind: KindCheck, LastRunAt: last} }
	cases := []struct {
		name string
		g    *Graph
		want bool
	}{
		{"code change", gateGraph(check(time.Time{}), now, []string{"a.go"}), true},
		{"observed change", gateGraph(check(time.Time{}), now, nil, FileStat{Path: "b.go"}), true},
		{"docs only", gateGraph(check(time.Time{}), now, []string{"README.md", "docs/a.txt", "LICENSE"}), false},
		{"nothing changed", gateGraph(check(time.Time{}), now, nil), false},
		{"ended before the last run", gateGraph(check(now.Add(time.Minute)), now, []string{"a.go"}), false},
		{"ended after the last run", gateGraph(check(now.Add(-time.Minute)), now, []string{"a.go"}), true},
	}
	for _, c := range cases {
		if got := ChangedSinceCheck(c.g, c.g.Node("CHECK")); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	g := gateGraph(check(time.Time{}), now, nil)
	g.Nodes[0].Observed = []string{"x.go"}
	if !ChangedSinceCheck(g, g.Node("CHECK")) {
		t.Error("a file seen on disk must count")
	}
}

func TestExecCheckGate(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), nil, nil)
	now := time.Now()

	g := gateGraph(&Node{ID: "CHECK", Kind: KindCheck, Commands: []string{"false"}}, now, []string{"README.md"})
	res, err := ex.Run(context.Background(), g, g.Node("CHECK"), func(Event) {})
	if err != nil || !res.Skipped || res.Summary != "skipped · nothing changed" {
		t.Fatalf("docs only: res=%+v err=%v", res, err)
	}

	g = gateGraph(&Node{ID: "CHECK", Kind: KindCheck, Commands: []string{"false"}}, now, []string{"a.go"})
	if _, err := ex.Run(context.Background(), g, g.Node("CHECK"), func(Event) {}); err == nil {
		t.Fatal("a code change must run the commands")
	}

	g = gateGraph(&Node{ID: "CHECK", Kind: KindCheck, Commands: []string{"true"}}, now, []string{"a.go"})
	res, err = ex.Run(context.Background(), g, g.Node("CHECK"), func(Event) {})
	if err != nil || res.Skipped || res.Summary != "all checks passed" {
		t.Fatalf("code change: res=%+v err=%v", res, err)
	}

	g = gateGraph(&Node{ID: "CHECK", Kind: KindCheck, Commands: []string{"true"}, ForceRun: true}, now, nil)
	res, err = ex.Run(context.Background(), g, g.Node("CHECK"), func(Event) {})
	if err != nil || res.Skipped {
		t.Fatalf("forced: res=%+v err=%v", res, err)
	}
}

func TestEngineKeepsSkippedChecksApartFromPassed(t *testing.T) {
	ex := newFakeExec()
	ex.behavior["CHECK"] = func(context.Context) (Result, error) {
		return Result{Summary: "skipped · nothing changed", Skipped: true}, nil
	}
	g := &Graph{Nodes: []*Node{work("T1")}}
	g.AddCheckDeferred()
	e := NewEngine(g, ex, 2, "")
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	if err := e.Run(context.Background(), "CHECK"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "CHECK")
	c := e.Snapshot().Node("CHECK")
	if c.Status != StatusDone || !c.Skipped || !c.LastRunAt.IsZero() {
		t.Errorf("check = %+v", c)
	}
}

func TestEngineRecordsWhenTheCheckLastRan(t *testing.T) {
	ex := newFakeExec()
	g := &Graph{Nodes: []*Node{work("T1")}}
	g.AddCheckDeferred()
	e := NewEngine(g, ex, 2, "")
	_ = e.Run(context.Background(), "T1")
	waitEvent(t, e, EventDone, "T1")
	_ = e.Run(context.Background(), "CHECK")
	waitEvent(t, e, EventDone, "CHECK")
	c := e.Snapshot().Node("CHECK")
	if c.Skipped || c.LastRunAt.IsZero() {
		t.Errorf("check = %+v", c)
	}
}

func TestQueueCheckAddsOrResetsAndForces(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := NewEngine(g, newFakeExec(), 2, "")
	if err := e.QueueCheck([]string{"go test ./..."}); err != nil {
		t.Fatal(err)
	}
	c := e.Snapshot().Node("CHECK")
	if c == nil || !c.ForceRun || c.Status != StatusTodo || len(c.Commands) != 1 {
		t.Fatalf("added check = %+v", c)
	}
	e.mu.Lock()
	e.g.Node("CHECK").Status = StatusDone
	e.g.Node("CHECK").Skipped = true
	e.mu.Unlock()
	if err := e.QueueCheck(nil); err != nil {
		t.Fatal(err)
	}
	c = e.Snapshot().Node("CHECK")
	if c.Status != StatusTodo || c.Skipped || !c.ForceRun {
		t.Errorf("reset check = %+v", c)
	}
	e.mu.Lock()
	e.g.Node("CHECK").Status = StatusRunning
	e.mu.Unlock()
	if err := e.QueueCheck(nil); err == nil {
		t.Error("a running check cannot be queued")
	}
}

func TestAppendAddsTheCheckOnlyForWritingNodes(t *testing.T) {
	readOnly := &Node{ID: "T1", Title: "review", Kind: KindWork, ReadOnlyTask: true}
	writer := &Node{ID: "T1", Title: "fix", Kind: KindWork}

	g := &Graph{}
	g.Append(&Graph{Nodes: []*Node{readOnly}}, 1)
	if g.Node("CHECK") != nil {
		t.Error("a read-only append must not add Build & test")
	}

	g.Append(&Graph{Nodes: []*Node{writer}}, 2)
	c := g.Node("CHECK")
	if c == nil || len(c.DependsOn) != 2 {
		t.Fatalf("check = %+v", c)
	}

	c.Status = StatusDone
	g.Append(&Graph{Nodes: []*Node{{ID: "T1", Title: "more review", Kind: KindWork, ReadOnlyTask: true}}}, 3)
	if g.Node("CHECK").Status != StatusDone || len(g.Node("CHECK").DependsOn) != 2 {
		t.Error("a read-only append must leave a finished Build & test alone")
	}
}

func TestEngineCarriesTheAnswerOfAReadOnlyTask(t *testing.T) {
	ex := newFakeExec()
	ex.behavior["T1"] = func(context.Context) (Result, error) {
		return Result{Summary: "s", Answer: "Everything is fine."}, nil
	}
	ex.behavior["T2"] = func(context.Context) (Result, error) {
		return Result{Summary: "s"}, nil
	}
	e := NewEngine(&Graph{Nodes: []*Node{work("T1"), work("T2")}}, ex, 2, "")
	_ = e.Run(context.Background(), "T1")
	if ev := waitEvent(t, e, EventDone, "T1"); ev.Answer != "Everything is fine." {
		t.Errorf("answer = %q", ev.Answer)
	}
	_ = e.Run(context.Background(), "T2")
	if ev := waitEvent(t, e, EventDone, "T2"); ev.Answer != "" {
		t.Errorf("a node without an answer has none, got %q", ev.Answer)
	}
	if g := e.Snapshot(); !g.Node("T1").Answered || g.Node("T2").Answered {
		t.Errorf("answered flags: %v %v", g.Node("T1").Answered, g.Node("T2").Answered)
	}
}

func TestExecReadOnlyTaskResultCarriesTheBody(t *testing.T) {
	f := &execFake{text: "The code is fine.\n\nREPORT\nstatus: done\nsummary: fine\n"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "reviewer", ReadOnlyTask: true}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil || res.Answer != "The code is fine." {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	g.Nodes[0].ReadOnlyTask = false
	if res, _ = ex.Run(context.Background(), g, g.Nodes[0], func(Event) {}); res.Answer != "The code is fine." {
		t.Errorf("answer %q for a read-only role", res.Answer)
	}
}

func TestExecWritingNodeHasNoAnswer(t *testing.T) {
	f := &execFake{text: "Done.\n\nREPORT\nstatus: done\nsummary: fine\n"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	if res, _ := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {}); res.Answer != "" {
		t.Errorf("answer %q for a node that writes", res.Answer)
	}
}
