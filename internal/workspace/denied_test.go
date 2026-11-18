package workspace

import (
	"context"
	"reflect"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

func TestEngineKeepsDeniedUntilNextAttempt(t *testing.T) {
	ex := &seenExec{}
	ex.run = func(n *Node, _ func(Event)) (Result, error) {
		if n.Attempt < 2 {
			return Result{Summary: "first", Denied: []runner.Denial{{Tool: "Bash", Command: "cmake --build build"}}}, nil
		}
		return Result{Summary: "second"}, nil
	}
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := replyEngine(g, ex)
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	want := []runner.Denial{{Tool: "Bash", Command: "cmake --build build"}}
	if got := e.Snapshot().Node("T1").Denied; !reflect.DeepEqual(got, want) {
		t.Fatalf("denied = %+v", got)
	}
	if err := e.AllowCommands("T1", []string{"cmake", "cmake", " "}); err != nil {
		t.Fatal(err)
	}
	if got := e.Snapshot().Node("T1").ExtraAllowed; !reflect.DeepEqual(got, []string{"cmake"}) {
		t.Fatalf("extra allowed = %v", got)
	}
	if _, err := e.Reply("T1", "go on"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	n := e.Snapshot().Node("T1")
	if len(n.Denied) != 0 || !reflect.DeepEqual(n.ExtraAllowed, []string{"cmake"}) {
		t.Errorf("after the retry: denied %+v, extra %v", n.Denied, n.ExtraAllowed)
	}
	if err := e.AllowCommands("nope", []string{"x"}); err == nil {
		t.Error("an unknown node must fail")
	}
}
