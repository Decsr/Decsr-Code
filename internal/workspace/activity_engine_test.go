package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// scriptExec runs a script against emit, then finishes the node.
type scriptExec struct {
	script func(n *Node, emit func(Event))
}

func (s scriptExec) Run(_ context.Context, _ *Graph, n *Node, emit func(Event)) (Result, error) {
	s.script(n, emit)
	return Result{Summary: "ok"}, nil
}

func actEv(a runner.Activity) Event { return Event{Type: EventActivity, Activity: &a} }

func runScript(t *testing.T, save func(*Graph) error, script func(n *Node, emit func(Event))) *Engine {
	t.Helper()
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := NewEngineWithSave(g, scriptExec{script}, 1, save)
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	return e
}

func TestActivityAppendReplaceByID(t *testing.T) {
	e := runScript(t, nil, func(_ *Node, emit func(Event)) {
		emit(actEv(runner.Activity{ID: "a", Kind: runner.ActCommand, State: runner.ActRunning, Title: "go test"}))
		emit(actEv(runner.Activity{ID: "b", Kind: runner.ActRead, State: runner.ActDone, Title: "x.go"}))
		emit(actEv(runner.Activity{ID: "a", Kind: runner.ActCommand, State: runner.ActDone, Title: "go test"}))
		emit(actEv(runner.Activity{ID: "c", Kind: runner.ActEdit, State: runner.ActDone, Title: "y.go"}))
	})
	acts := e.Snapshot().Node("T1").Activity
	var got []string
	for _, a := range acts {
		got = append(got, a.ID+"/"+string(a.State))
	}
	if want := "a/done b/done c/done"; strings.Join(got, " ") != want {
		t.Errorf("activity = %v, want %s", got, want)
	}
}

func TestActivityCapAndDetail(t *testing.T) {
	long := strings.Repeat("é", 1500)
	e := runScript(t, nil, func(_ *Node, emit func(Event)) {
		for i := 0; i < 237; i++ {
			emit(actEv(runner.Activity{ID: fmt.Sprintf("i%d", i), Kind: runner.ActRead, State: runner.ActDone, Title: "f", Detail: long}))
		}
	})
	n := e.Snapshot().Node("T1")
	if len(n.Activity) != 200 || n.ActivityDropped != 37 || n.Activity[0].ID != "i37" {
		t.Fatalf("len=%d dropped=%d first=%s", len(n.Activity), n.ActivityDropped, n.Activity[0].ID)
	}
	if got := []rune(n.Activity[0].Detail); len(got) != 500 {
		t.Errorf("stored detail = %d runes", len(got))
	}
	if d, ok := e.ActivityDetail("T1", "i199"); !ok || d != long {
		t.Errorf("full detail ok=%v len=%d", ok, len([]rune(d)))
	}
	if _, ok := e.ActivityDetail("T1", "i36"); ok {
		t.Error("dropped item still has detail")
	}
	if _, ok := e.ActivityDetail("T1", "i37"); !ok {
		t.Error("oldest kept item has no detail")
	}
	if _, ok := e.ActivityDetail("nope", "i37"); ok {
		t.Error("unknown node has detail")
	}
}

func TestActivityClearedOnRestart(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1")}}
	var runs atomic.Int32
	ex := scriptExec{func(_ *Node, emit func(Event)) {
		r := runs.Add(1)
		emit(actEv(runner.Activity{ID: fmt.Sprintf("r%d", r), Kind: runner.ActRead, State: runner.ActDone, Title: "f", Detail: "d"}))
		emit(Event{Type: EventContext, ContextTokens: 5, ContextLimit: 9})
	}}
	e := NewEngine(g, ex, 1, "")
	for i := 0; i < 2; i++ {
		if err := e.Run(context.Background(), "T1"); err != nil {
			t.Fatal(err)
		}
		waitEvent(t, e, EventDone, "T1")
		if i == 0 {
			if err := e.Replace(func(g *Graph) { g.Node("T1").Status = StatusFailed }); err != nil {
				t.Fatal(err)
			}
		}
	}
	n := e.Snapshot().Node("T1")
	if len(n.Activity) != 1 || n.Activity[0].ID != "r2" {
		t.Errorf("activity = %+v", n.Activity)
	}
	if _, ok := e.ActivityDetail("T1", "r1"); ok {
		t.Error("detail of the first attempt survived")
	}
	if _, ok := e.ActivityDetail("T1", "r2"); !ok {
		t.Error("detail of the second attempt missing")
	}
}

func TestLastActivityAtUpdates(t *testing.T) {
	var clock atomic.Int64
	base := time.Unix(1000, 0)
	g := &Graph{Nodes: []*Node{work("T1")}}
	release := make(chan struct{})
	step := make(chan struct{})
	ex := scriptExec{func(_ *Node, emit func(Event)) {
		emit(actEv(runner.Activity{ID: "a", Kind: runner.ActRead, State: runner.ActDone, Title: "f"}))
		step <- struct{}{}
		<-release
		emit(Event{Type: EventOutput, Text: "x"})
		step <- struct{}{}
		<-release
	}}
	e := NewEngine(g, ex, 1, "")
	e.now = func() time.Time { return base.Add(time.Duration(clock.Load()) * time.Second) }
	clock.Store(10)
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	<-step
	if got := e.Snapshot().Node("T1").LastActivityAt; !got.Equal(base.Add(10 * time.Second)) {
		t.Errorf("after activity = %v", got)
	}
	clock.Store(20)
	release <- struct{}{}
	<-step
	if got := e.Snapshot().Node("T1").LastActivityAt; !got.Equal(base.Add(20 * time.Second)) {
		t.Errorf("after output = %v", got)
	}
	release <- struct{}{}
	waitEvent(t, e, EventDone, "T1")
}

func TestActivitySaveThrottled(t *testing.T) {
	var saves, midSaves atomic.Int32
	var clock atomic.Int64
	base := time.Unix(1000, 0)
	var e *Engine
	g := &Graph{Nodes: []*Node{work("T1")}}
	saveFn := func(*Graph) error { saves.Add(1); return nil }
	ex := scriptExec{func(_ *Node, emit func(Event)) {
		before := saves.Load()
		for i := 0; i < 50; i++ {
			clock.Add(int64(100 * time.Millisecond)) // 5 s in total
			emit(actEv(runner.Activity{ID: fmt.Sprintf("i%d", i), Kind: runner.ActRead, State: runner.ActDone, Title: "f"}))
		}
		midSaves.Store(saves.Load() - before)
	}}
	e = NewEngineWithSave(g, ex, 1, saveFn)
	e.now = func() time.Time { return base.Add(time.Duration(clock.Load())) }
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	if m := midSaves.Load(); m < 2 || m > 3 {
		t.Errorf("saves while 50 events came in over 5 s = %d, want 2 or 3", m)
	}
}

func TestSessionJSONRoundTripAndOldFile(t *testing.T) {
	code := 1
	g := &Graph{Nodes: []*Node{{
		ID: "T1", Kind: KindWork, Status: StatusDone, Role: "coder",
		Activity:        []runner.Activity{{ID: "a", Kind: runner.ActCommand, State: runner.ActFailed, Title: "go test", Detail: "boom", ExitCode: &code, Duration: time.Second}},
		ActivityDropped: 3, LastActivityAt: time.Unix(5, 0).UTC(), ContextTokens: 1200, ContextLimit: 8000,
	}}}
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var back Graph
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&back, g) {
		t.Errorf("round trip: got %+v", back.Nodes[0])
	}
	c := g.Clone()
	c.Nodes[0].Activity[0].Title = "changed"
	if g.Nodes[0].Activity[0].Title != "go test" {
		t.Error("Clone shares the activity slice")
	}
	var old Graph
	if err := json.Unmarshal([]byte(`{"nodes":[{"id":"T1","title":"x","kind":"work","status":"done"}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if n := old.Nodes[0]; len(n.Activity) != 0 || n.ContextTokens != 0 || !n.LastActivityAt.IsZero() {
		t.Errorf("old file node = %+v", n)
	}
}

func TestAppendCopiesActivity(t *testing.T) {
	add := &Graph{Nodes: []*Node{{ID: "T1", Kind: KindWork, Activity: []runner.Activity{{ID: "a"}}}}}
	g := &Graph{}
	g.Append(add, 1)
	g.Nodes[0].Activity[0].ID = "z"
	if add.Nodes[0].Activity[0].ID != "a" {
		t.Error("Append shares the activity slice")
	}
}

func TestEventContextStoredAndForwarded(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1")}}
	ex := scriptExec{func(_ *Node, emit func(Event)) {
		emit(Event{Type: EventContext, ContextTokens: 1234, ContextLimit: 8000})
	}}
	e := NewEngine(g, ex, 1, "")
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, e, EventContext, "T1")
	if ev.ContextTokens != 1234 || ev.ContextLimit != 8000 {
		t.Errorf("event = %+v", ev)
	}
	waitEvent(t, e, EventDone, "T1")
	if n := e.Snapshot().Node("T1"); n.ContextTokens != 1234 || n.ContextLimit != 8000 {
		t.Errorf("node = %+v", n)
	}
}
