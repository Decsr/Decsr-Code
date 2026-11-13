package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testWait = 5 * time.Second

// fakeExec runs nodes through per-node behaviour controlled by the test.
type fakeExec struct {
	mu       sync.Mutex
	behavior map[string]func(ctx context.Context) (Result, error)
	started  chan string
	order    []string
	cur, max int32
}

func newFakeExec() *fakeExec {
	return &fakeExec{behavior: map[string]func(context.Context) (Result, error){}, started: make(chan string, 64)}
}

func (f *fakeExec) Run(ctx context.Context, _ *Graph, n *Node, emit func(Event)) (Result, error) {
	c := atomic.AddInt32(&f.cur, 1)
	for {
		m := atomic.LoadInt32(&f.max)
		if c <= m || atomic.CompareAndSwapInt32(&f.max, m, c) {
			break
		}
	}
	defer atomic.AddInt32(&f.cur, -1)
	f.mu.Lock()
	f.order = append(f.order, n.ID)
	b := f.behavior[n.ID]
	f.mu.Unlock()
	f.started <- n.ID
	emit(Event{Type: EventOutput, Text: "hi " + n.ID})
	if b == nil {
		return Result{Summary: "ok " + n.ID}, nil
	}
	return b(ctx)
}

func blockUntil(ch <-chan struct{}) func(context.Context) (Result, error) {
	return func(ctx context.Context) (Result, error) {
		select {
		case <-ch:
			return Result{Summary: "released"}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
}

func work(id string, deps ...string) *Node {
	return &Node{ID: id, Title: id, Kind: KindWork, Role: "coder", Status: StatusTodo, DependsOn: deps}
}

func waitEvent(t *testing.T, e *Engine, typ EventType, id string) Event {
	t.Helper()
	timeout := time.After(testWait)
	for {
		select {
		case ev := <-e.Events():
			if ev.Type == typ && ev.NodeID == id {
				return ev
			}
		case <-timeout:
			t.Fatalf("timeout waiting for %s %s", typ, id)
		}
	}
}

// waitTerminal waits until n terminal (done/failed) events were seen.
func waitTerminal(t *testing.T, e *Engine, n int) {
	t.Helper()
	timeout := time.After(testWait)
	for seen := 0; seen < n; {
		select {
		case ev := <-e.Events():
			if ev.Type == EventDone || ev.Type == EventFailed {
				seen++
			}
		case <-timeout:
			t.Fatalf("timeout: saw %d of %d terminal events", seen, n)
		}
	}
}

func status(e *Engine, id string) Status { return e.Snapshot().Node(id).Status }

func TestEngineRunSingle(t *testing.T) {
	f := newFakeExec()
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, f, 3, "")
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, e, EventDone, "T1")
	if ev.Text != "ok T1" {
		t.Errorf("done text = %q", ev.Text)
	}
	n := e.Snapshot().Node("T1")
	if n.Status != StatusDone || n.Summary != "ok T1" || n.EndedAt.IsZero() || n.StartedAt.IsZero() {
		t.Errorf("node = %+v", n)
	}
}

func TestEngineRunRejects(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusTodo},
		work("T2", "T3", "T4"),
		work("T3"), work("T4"),
		{ID: "T5", Kind: KindWork, Role: "coder", Status: StatusDone},
	}}
	f := newFakeExec()
	e := NewEngine(g, f, 3, "")
	defer e.StopAll()
	ctx := context.Background()
	if err := e.Run(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown: %v", err)
	}
	if err := e.Run(ctx, "T1"); err == nil || !strings.Contains(err.Error(), "no role") {
		t.Errorf("unassigned: %v", err)
	}
	if err := e.Run(ctx, "T2"); err == nil || !strings.Contains(err.Error(), "T3, T4") {
		t.Errorf("blocked: %v", err)
	}
	if err := e.Run(ctx, "T5"); err == nil || !strings.Contains(err.Error(), "already done") {
		t.Errorf("done: %v", err)
	}
}

func TestEngineParallelLimit(t *testing.T) {
	f := newFakeExec()
	gate := make(chan struct{})
	g := &Graph{}
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		g.Nodes = append(g.Nodes, work(id))
		f.behavior[id] = blockUntil(gate)
	}
	e := NewEngine(g, f, 2, "")
	defer e.StopAll()
	e.RunAll(context.Background())
	<-f.started
	<-f.started
	if e.Running() != 2 {
		t.Fatalf("running = %d", e.Running())
	}
	if err := e.Run(context.Background(), "T3"); err == nil || !strings.Contains(err.Error(), "parallel limit reached (2 running)") {
		t.Errorf("limit error = %v", err)
	}
	close(gate)
	waitTerminal(t, e, 4)
	if m := atomic.LoadInt32(&f.max); m > 2 {
		t.Errorf("max concurrency = %d", m)
	}
	if !e.Snapshot().Node("T4").EndedAt.After(time.Time{}) || status(e, "T4") != StatusDone {
		t.Errorf("T4 not done")
	}
}

func TestEngineAutoFollowsDependencies(t *testing.T) {
	f := newFakeExec()
	g := &Graph{Nodes: []*Node{work("T3", "T2"), work("T2", "T1"), work("T1")}}
	e := NewEngine(g, f, 3, "")
	defer e.StopAll()
	e.RunAll(context.Background())
	waitTerminal(t, e, 3)
	f.mu.Lock()
	got := strings.Join(f.order, ",")
	f.mu.Unlock()
	if got != "T1,T2,T3" {
		t.Errorf("order = %s", got)
	}
	if e.Auto() {
		t.Error("auto should be off")
	}
}

func TestEngineFailureIsolation(t *testing.T) {
	f := newFakeExec()
	f.behavior["T1"] = func(context.Context) (Result, error) { return Result{}, errors.New("kaput") }
	g := &Graph{Nodes: []*Node{work("T1"), work("T2"), work("T3", "T1")}}
	e := NewEngine(g, f, 3, "")
	defer e.StopAll()
	e.RunAll(context.Background())
	waitTerminal(t, e, 2)
	snap := e.Snapshot()
	if snap.Node("T1").Status != StatusFailed || snap.Node("T1").Error != "kaput" {
		t.Errorf("T1 = %+v", snap.Node("T1"))
	}
	if snap.Node("T2").Status != StatusDone {
		t.Errorf("T2 = %s", snap.Node("T2").Status)
	}
	if snap.Node("T3").Status != StatusTodo {
		t.Errorf("T3 = %s", snap.Node("T3").Status)
	}
	if e.Auto() {
		t.Error("auto should be off")
	}
}

func TestEngineStop(t *testing.T) {
	f := newFakeExec()
	f.behavior["T1"] = blockUntil(make(chan struct{}))
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, f, 3, "")
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	<-f.started
	e.Stop("T1")
	ev := waitEvent(t, e, EventFailed, "T1")
	if ev.Text != "stopped" {
		t.Errorf("text = %q", ev.Text)
	}
	if n := e.Snapshot().Node("T1"); n.Status != StatusFailed || n.Error != "stopped" {
		t.Errorf("node = %+v", n)
	}
}

func TestEngineStopAllWaits(t *testing.T) {
	f := newFakeExec()
	g := &Graph{Nodes: []*Node{work("T1"), work("T2")}}
	f.behavior["T1"] = blockUntil(make(chan struct{}))
	f.behavior["T2"] = blockUntil(make(chan struct{}))
	e := NewEngine(g, f, 3, "")
	e.RunAll(context.Background())
	<-f.started
	<-f.started
	e.StopAll()
	if e.Running() != 0 || atomic.LoadInt32(&f.cur) != 0 {
		t.Errorf("running=%d cur=%d", e.Running(), f.cur)
	}
	if e.Auto() {
		t.Error("auto should be off")
	}
	snap := e.Snapshot()
	for _, id := range []string{"T1", "T2"} {
		if snap.Node(id).Status != StatusFailed || snap.Node(id).Error != "stopped" {
			t.Errorf("%s = %+v", id, snap.Node(id))
		}
	}
}

func TestEngineRetryFailed(t *testing.T) {
	f := newFakeExec()
	var calls int32
	f.behavior["T1"] = func(context.Context) (Result, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return Result{}, errors.New("first")
		}
		return Result{Summary: "second"}, nil
	}
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, f, 3, "")
	defer e.StopAll()
	ctx := context.Background()
	if err := e.Run(ctx, "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventFailed, "T1")
	if err := e.Run(ctx, "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	if n := e.Snapshot().Node("T1"); n.Status != StatusDone || n.Error != "" || n.Summary != "second" {
		t.Errorf("node = %+v", n)
	}
}

func TestEngineAssignRules(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusTodo},
		{ID: "T2", Kind: KindWork, Status: StatusDone},
		{ID: "T3", Kind: KindWork, Status: StatusRunning},
		{ID: "T4", Kind: KindWork, Status: StatusFailed},
		{ID: "CHECK", Kind: KindCheck, Status: StatusTodo},
	}}
	e := NewEngine(g, newFakeExec(), 3, "")
	if err := e.Assign("T1", "coder"); err != nil || e.Snapshot().Node("T1").Role != "coder" {
		t.Errorf("assign T1: %v", err)
	}
	if err := e.Assign("T1", ""); err != nil || e.Snapshot().Node("T1").Role != "" {
		t.Errorf("unassign T1: %v", err)
	}
	if err := e.Assign("T4", "qa"); err != nil {
		t.Errorf("failed node should be assignable: %v", err)
	}
	for _, id := range []string{"T2", "T3", "CHECK", "nope"} {
		if err := e.Assign(id, "coder"); err == nil {
			t.Errorf("assign %s should fail", id)
		}
	}
}

func TestEngineAcceptSuggestions(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusTodo, SuggestedRole: "coder"},
		{ID: "T2", Kind: KindWork, Status: StatusTodo, SuggestedRole: "ghost"},
		{ID: "T3", Kind: KindWork, Status: StatusTodo, SuggestedRole: "qa", Role: "coder"},
		{ID: "T4", Kind: KindWork, Status: StatusTodo},
	}}
	e := NewEngine(g, newFakeExec(), 3, "")
	n := e.AcceptSuggestions(func(s string) bool { return s != "ghost" })
	if n != 1 {
		t.Fatalf("count = %d", n)
	}
	snap := e.Snapshot()
	if snap.Node("T1").Role != "coder" || snap.Node("T2").Role != "" || snap.Node("T3").Role != "coder" || snap.Node("T4").Role != "" {
		t.Errorf("roles: %q %q %q %q", snap.Node("T1").Role, snap.Node("T2").Role, snap.Node("T3").Role, snap.Node("T4").Role)
	}
}

func TestEngineAddFix(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusDone},
		{ID: "CHECK", Title: "Build & test", Kind: KindCheck, Status: StatusFailed, Error: "boom", DependsOn: []string{"T1"}},
	}}
	e := NewEngine(g, newFakeExec(), 3, "")
	id, err := e.AddFix("CHECK")
	if err != nil || id != "F1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	snap := e.Snapshot()
	f := snap.Node("F1")
	if f == nil || !strings.Contains(f.Description, "boom") {
		t.Fatalf("F1 = %+v", f)
	}
	c := snap.Node("CHECK")
	if c.Status != StatusTodo || len(snap.Blockers("CHECK")) != 1 || snap.Blockers("CHECK")[0] != "F1" {
		t.Errorf("check = %+v blockers=%v", c, snap.Blockers("CHECK"))
	}
	if _, err := e.AddFix("CHECK"); err == nil {
		t.Error("check is no longer failed; AddFix should error")
	}
}

func TestEnginePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws", "workspace.json")
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, newFakeExec(), 3, path)
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Node("T1").Status != StatusDone {
		t.Errorf("persisted status = %s", g.Node("T1").Status)
	}
}

func TestEngineSnapshotIsCopy(t *testing.T) {
	e := NewEngine(&Graph{Nodes: []*Node{work("T1", "T0")}}, newFakeExec(), 3, "")
	s := e.Snapshot()
	s.Node("T1").Status = StatusDone
	s.Node("T1").DependsOn[0] = "X"
	s2 := e.Snapshot()
	if s2.Node("T1").Status != StatusTodo || s2.Node("T1").DependsOn[0] != "T0" {
		t.Errorf("engine state leaked: %+v", s2.Node("T1"))
	}
}

func filesResult(files ...string) func(context.Context) (Result, error) {
	return func(context.Context) (Result, error) { return Result{Summary: "ok", Files: files}, nil }
}

func gate(ch <-chan struct{}, files ...string) func(context.Context) (Result, error) {
	return func(ctx context.Context) (Result, error) {
		select {
		case <-ch:
			return Result{Summary: "ok", Files: files}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
}

func TestEngineParallelOverwriteWarnsBoth(t *testing.T) {
	f := newFakeExec()
	r1, r2 := make(chan struct{}), make(chan struct{})
	f.behavior["T1"] = gate(r1, "a.go", "b.go")
	f.behavior["T2"] = gate(r2, "a.go")
	e := NewEngine(&Graph{Nodes: []*Node{work("T1"), work("T2")}}, f, 3, "")
	defer e.StopAll()
	e.RunAll(context.Background())
	<-f.started
	<-f.started
	close(r1)
	waitEvent(t, e, EventDone, "T1")
	close(r2)
	lines := map[string][]string{}
	timeout := time.After(testWait)
	done := false
	for !done {
		select {
		case ev := <-e.Events():
			if ev.Type == EventOutput && strings.HasPrefix(ev.Text, "⚠ ") {
				lines[ev.NodeID] = append(lines[ev.NodeID], strings.TrimSpace(strings.TrimPrefix(ev.Text, "⚠ ")))
			}
			if ev.Type == EventDone && ev.NodeID == "T2" {
				done = true
			}
		case <-timeout:
			t.Fatal("timeout")
		}
	}
	s := e.Snapshot()
	if got := s.Node("T1").Warnings; len(got) != 1 || got[0] != "also written by T2 in parallel: a.go" {
		t.Errorf("T1 warnings = %v", got)
	}
	if got := s.Node("T2").Warnings; len(got) != 1 || got[0] != "also written by T1 in parallel: a.go" {
		t.Errorf("T2 warnings = %v", got)
	}
	if len(lines["T1"]) != 1 || len(lines["T2"]) != 1 {
		t.Errorf("output lines = %v", lines)
	}
}

func TestEngineSequentialSharedFileNoWarning(t *testing.T) {
	f := newFakeExec()
	f.behavior["T1"] = filesResult("a.go")
	f.behavior["T2"] = filesResult("a.go")
	e := NewEngine(&Graph{Nodes: []*Node{work("T1"), work("T2", "T1")}}, f, 3, "")
	defer e.StopAll()
	e.RunAll(context.Background())
	waitTerminal(t, e, 2)
	s := e.Snapshot()
	if len(s.Node("T1").Warnings)+len(s.Node("T2").Warnings) != 0 {
		t.Errorf("unexpected warnings: %v %v", s.Node("T1").Warnings, s.Node("T2").Warnings)
	}
}

func TestEngineStoresResultWarningsAndClearsOnRerun(t *testing.T) {
	f := newFakeExec()
	calls := 0
	f.behavior["T1"] = func(context.Context) (Result, error) {
		calls++
		if calls == 1 {
			return Result{Summary: "ok", Warnings: []string{"not written: x: bad"}}, nil
		}
		return Result{Summary: "ok"}, nil
	}
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, f, 3, "")
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	if w := e.Snapshot().Node("T1").Warnings; len(w) != 1 || w[0] != "not written: x: bad" {
		t.Fatalf("warnings = %v", w)
	}
	// Mark failed to allow a re-run.
	e.mu.Lock()
	e.g.Node("T1").Status = StatusFailed
	e.mu.Unlock()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	if w := e.Snapshot().Node("T1").Warnings; len(w) != 0 {
		t.Errorf("warnings not cleared: %v", w)
	}
}

func TestEngineStopAllReturnsWhenNobodyReads(t *testing.T) {
	f := newFakeExec()
	f.behavior["T1"] = blockUntil(make(chan struct{}))
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, f, 3, "")
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	<-f.started
	for i := 0; i < eventBuffer+10; i++ {
		e.emitOutput(Event{NodeID: "T1", Type: EventOutput, Text: "x"})
	}
	fin := make(chan struct{})
	go func() { e.StopAll(); close(fin) }()
	select {
	case <-fin:
	case <-time.After(3 * time.Second):
		t.Fatal("StopAll blocked on a full event buffer")
	}
	if status(e, "T1") != StatusFailed {
		t.Errorf("status = %s", status(e, "T1"))
	}
}

func TestEngineCloseIdempotent(t *testing.T) {
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, newFakeExec(), 1, "")
	select {
	case <-e.Done():
		t.Fatal("Done closed too early")
	default:
	}
	e.Close()
	e.Close()
	select {
	case <-e.Done():
	default:
		t.Fatal("Done not closed")
	}
}

func TestEngineWillAutoRun(t *testing.T) {
	f := newFakeExec()
	rel := make(chan struct{})
	f.behavior["T1"] = blockUntil(rel)
	unassigned := &Node{ID: "T3", Kind: KindWork, Status: StatusTodo}
	e := NewEngine(&Graph{Nodes: []*Node{work("T1"), work("T2", "T1"), unassigned}}, f, 1, "")
	defer e.StopAll()
	if e.WillAutoRun("T2") {
		t.Error("auto is off")
	}
	e.RunAll(context.Background())
	<-f.started
	if !e.WillAutoRun("T2") {
		t.Error("T2 should auto-run")
	}
	if e.WillAutoRun("T1") {
		t.Error("running node")
	}
	if e.WillAutoRun("T3") || e.WillAutoRun("nope") {
		t.Error("unassigned or unknown")
	}
	close(rel)
}

func TestEngineObservedFilesDoNotRaiseOverlapWarning(t *testing.T) {
	f := newFakeExec()
	r1, r2 := make(chan struct{}), make(chan struct{})
	obs := func(ch <-chan struct{}) func(context.Context) (Result, error) {
		return func(ctx context.Context) (Result, error) {
			select {
			case <-ch:
				return Result{Summary: "ok", Observed: []string{"shared.py"}}, nil
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		}
	}
	f.behavior["T1"] = obs(r1)
	f.behavior["T2"] = obs(r2)
	e := NewEngine(&Graph{Nodes: []*Node{work("T1"), work("T2")}}, f, 3, "")
	defer e.StopAll()
	e.RunAll(context.Background())
	<-f.started
	<-f.started
	close(r1)
	waitEvent(t, e, EventDone, "T1")
	close(r2)
	waitEvent(t, e, EventDone, "T2")
	s := e.Snapshot()
	for _, id := range []string{"T1", "T2"} {
		if len(s.Node(id).Warnings) != 0 || len(s.Node(id).Observed) != 1 {
			t.Errorf("%s = %+v", id, s.Node(id))
		}
	}
	if c := s.Clone(); len(c.Node("T1").Observed) != 1 {
		t.Error("Clone drops Observed")
	}
}
