package workspace

import (
	"context"
	"strings"
	"testing"
	"time"
)

func liveFixture() []LiveNode {
	return []LiveNode{
		{ID: "T1", Title: "Parser", Status: "running", Role: "coder", Started: true, Files: []string{"cmd/main.go"}},
		{ID: "T2", Title: "Docs", Status: "done", Role: "coder", Started: true},
		{ID: "T3", Title: "Flags", Status: "todo", Role: "coder", Description: "add flags", Files: nil},
		{ID: "T4", Title: "Extra", Status: "todo", Role: "coder"},
	}
}

func TestParseChangesValid(t *testing.T) {
	out := `===TASKS===
[]
===CHANGES===
[{"node":"T3","action":"update","description":"also --port","depends_on":["T1","T7"],"reason":"new task needs it"},
 {"node":"t4","action":"cancel","reason":"replaced by T7"},
 {"node":"T1","action":"note","message":"keep the alias","reason":"r"}]
===END===`
	g, err := ParseSplitOpts("task", out, testRoles(), []string{"T1", "T2", "T3", "T4"}, SplitOptions{Live: liveFixture()})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 0 || len(g.Changes) != 3 {
		t.Fatalf("nodes %d changes %+v warns %v", len(g.Nodes), g.Changes, g.SplitWarnings)
	}
	c := g.Changes[0]
	if c.Action != ChangeUpdate || !c.SetDeps || len(c.DependsOn) != 1 || c.DependsOn[0] != "T1" {
		t.Errorf("update = %+v (T7 is unknown here and must be dropped)", c)
	}
	if g.Changes[1].Node != "T4" || g.Changes[2].Action != ChangeNote || g.Changes[2].Message != "keep the alias" {
		t.Errorf("changes = %+v", g.Changes)
	}
}

func TestParseChangesKeepsNewKeyDependency(t *testing.T) {
	out := `===TASKS===
[{"key":"T7","title":"New","description":"d","role":"coder","writes":true,"depends_on":[]}]
===CHANGES===
[{"node":"T3","action":"update","depends_on":["T7"]}]
===END===`
	g, err := ParseSplitOpts("task", out, testRoles(), []string{"T1", "T2", "T3", "T4"}, SplitOptions{Live: liveFixture()})
	if err != nil || len(g.Changes) != 1 || g.Changes[0].DependsOn[0] != "T7" {
		t.Fatalf("%v %+v", err, g.Changes)
	}
}

func TestParseChangesDropsInvalid(t *testing.T) {
	out := `===TASKS===
[]
===CHANGES===
[{"node":"T3","action":"explode"},
 {"node":"T9","action":"cancel"},
 {"node":"T1","action":"update","description":"x"},
 {"node":"T2","action":"cancel"},
 {"node":"T3","action":"note","message":"hi"},
 {"node":"T1","action":"note","message":"  "},
 {"node":"T4","action":"update"},
 {"node":"T4","action":"cancel"},
 {"node":"T4","action":"cancel"}]
===END===`
	g, err := ParseSplitOpts("task", out, testRoles(), []string{"T1", "T2", "T3", "T4"}, SplitOptions{Live: liveFixture()})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Changes) != 1 || g.Changes[0].Node != "T4" || g.Changes[0].Action != ChangeCancel {
		t.Fatalf("kept = %+v", g.Changes)
	}
	for _, want := range []string{"unknown action", "no such node", "T1 is running", "T2 is done", "has not started", "is empty", "changes nothing", "more than one change"} {
		found := false
		for _, w := range g.SplitWarnings {
			found = found || strings.Contains(w, want)
		}
		if !found {
			t.Errorf("no warning with %q in %v", want, g.SplitWarnings)
		}
	}
}

func TestParseChangesOnlyAnswerStillFailsWithoutAnything(t *testing.T) {
	if _, err := ParseSplitOpts("t", "===TASKS===\n[]\n===END===", testRoles(), nil, SplitOptions{}); err == nil {
		t.Fatal("no tasks and no changes must fail")
	}
	// Without a live graph every change is dropped.
	_, err := ParseSplitOpts("t", "===TASKS===\n[]\n===CHANGES===\n[{\"node\":\"T1\",\"action\":\"cancel\"}]\n===END===", testRoles(), nil, SplitOptions{})
	if err == nil {
		t.Fatal("changes of unknown nodes must not make an answer")
	}
}

func TestFollowUpPromptListsLiveGraph(t *testing.T) {
	live := liveFixture()
	live[0].Files = []string{"a.go", "b.go"}
	p := buildSplitPrompt("task", testRoles(), "ctx", []string{"T1", "T2", "T3", "T4"}, 5, SplitOptions{Live: live})
	for _, want := range []string{`T1 "Parser" [running] role coder`, "files: a.go, b.go", `T3 "Flags" [todo]`, "===CHANGES===", "MUST list it in `depends_on`", "`T5`"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(BuildSplitPrompt("x", testRoles(), "c"), "CHANGES") {
		t.Error("a fresh split must not mention changes")
	}
}

func TestLiveNodes(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "A", Kind: KindWork, Status: StatusRunning, Role: "coder", StartedAt: time.Now(), Files: []string{"x.go"}, Observed: []string{"y.go"}},
		{ID: "T2", Title: "B", Kind: KindWork, Status: StatusTodo, SuggestedRole: "qa"},
		{ID: "CHECK", Kind: KindCheck},
	}}
	live := LiveNodes(g)
	if len(live) != 2 || !live[0].Started || live[1].Started || live[1].Role != "qa" || len(live[0].Files) != 2 {
		t.Fatalf("%+v", live)
	}
}

func TestUpdateAndCancelNode(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1"), work("T2", "T1"), work("T3", "T2"), {ID: "CHECK", Kind: KindCheck, Status: StatusTodo, DependsOn: []string{"T1", "T2", "T3"}}}}
	e := replyEngine(g, &seenExec{})
	defer e.StopAll()
	deps := []string{"T1"}
	if err := e.UpdateNode("T3", "new text", &deps); err != nil {
		t.Fatal(err)
	}
	if n := e.Snapshot().Node("T3"); n.Description != "new text" || len(n.DependsOn) != 1 || n.DependsOn[0] != "T1" {
		t.Fatalf("T3 = %+v", n)
	}
	cyc := []string{"T3"}
	if err := e.UpdateNode("T1", "", &cyc); err == nil {
		t.Fatal("a dependency cycle must be refused")
	}
	if err := e.UpdateNode("T2", "", &[]string{"T9"}); err == nil {
		t.Fatal("an unknown dependency must be refused")
	}
	if err := e.CancelNode("T2"); err != nil {
		t.Fatal(err)
	}
	snap := e.Snapshot()
	if snap.Node("T2") != nil || len(snap.Node("CHECK").DependsOn) != 2 {
		t.Fatalf("graph after cancel: %+v", snap.Nodes)
	}
	finishRun(t, e, "T1")
	if err := e.CancelNode("T1"); err == nil || e.UpdateNode("T1", "x", nil) == nil {
		t.Fatal("a node that ran cannot be updated or cancelled")
	}
}

func TestNoteReachesRunningNodeAndAppendWhileRunning(t *testing.T) {
	ex := &seenExec{start: make(chan string, 4)}
	release := make(chan struct{})
	ex.run = func(n *Node, _ func(Event)) (Result, error) {
		if n.AttemptNumber() == 1 {
			<-release
		}
		return Result{Summary: "ok"}, nil
	}
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := replyEngine(g, ex)
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	<-ex.start
	if err := e.Replace(func(*Graph) {}); err == nil {
		t.Fatal("Replace is expected to refuse while a node runs")
	}
	add := &Graph{Nodes: []*Node{{ID: "T2", Title: "n", Kind: KindWork, Status: StatusTodo, DependsOn: []string{"T1"}}}}
	next, idMap, err := e.AppendNodes(add, 2)
	if err != nil || next != 3 || idMap["T2"] != "T2" {
		t.Fatalf("append while running: %d %v %v", next, idMap, err)
	}
	snap := e.Snapshot()
	if snap.Node("T2") == nil || snap.Node("T2").DependsOn[0] != "T1" || snap.Node("CHECK") == nil {
		t.Fatalf("graph = %+v", snap.Nodes)
	}
	res, err := e.Reply("T1", "keep the alias")
	if err != nil || res.State != ReplyQueued {
		t.Fatalf("%+v %v", res, err)
	}
	if got := e.Snapshot().Node("T1").PendingReply; got != "keep the alias" {
		t.Fatalf("PendingReply = %q", got)
	}
	close(release)
}
