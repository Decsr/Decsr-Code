package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// usageExec reports usage for every node before it finishes.
type usageExec struct {
	*fakeExec
	in, out int
}

func (u usageExec) Run(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error) {
	emit(Event{Type: EventUsage, In: u.in, Out: u.out, Runner: "deepseek", Model: "deepseek-v4"})
	return u.fakeExec.Run(ctx, g, n, emit)
}

// $1 per 1000 tokens, in and out alike; unknown models have no price.
func testCost(runner, model string, in, out int) (float64, bool) {
	if model != "deepseek-v4" {
		return 0, false
	}
	return float64(in+out) / 1000, true
}

func drainBudget(e *Engine) (warn, reached []Event) {
	for {
		select {
		case ev := <-e.Events():
			switch ev.Type {
			case EventBudgetWarn:
				warn = append(warn, ev)
			case EventBudgetReached:
				reached = append(reached, ev)
			}
		case <-time.After(150 * time.Millisecond):
			return warn, reached
		}
	}
}

func TestNodeCostSetFromUsage(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := NewEngine(g, usageExec{newFakeExec(), 1000, 500}, 1, "")
	e.SetCost(testCost)
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	n := e.Snapshot().Node("T1")
	if !n.CostKnown || n.CostUSD != 1.5 {
		t.Fatalf("cost %v known %v", n.CostUSD, n.CostKnown)
	}
}

func TestBudgetBlocksRunAndKeepsNodeReady(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1"), work("T2")}}
	g.Nodes[0].Status, g.Nodes[0].CostUSD, g.Nodes[0].CostKnown = StatusDone, 5.02, true
	e := NewEngine(g, newFakeExec(), 2, "")
	e.SetBudget(func() Budget { return Budget{LimitUSD: 5} })
	err := e.Run(context.Background(), "T2")
	if err == nil || !strings.Contains(err.Error(), "Budget reached: $5.02 of $5.00") || !strings.Contains(err.Error(), "/usage budget <amount>") {
		t.Fatalf("err %v", err)
	}
	if e.Snapshot().Node("T2").Status != StatusTodo {
		t.Fatal("the node must stay todo")
	}
	e.SetBudget(func() Budget { return Budget{LimitUSD: 10} })
	if err := e.Run(context.Background(), "T2"); err != nil {
		t.Fatalf("raised budget: %v", err)
	}
}

func TestBudgetCountsExtraAndUnknownIsFree(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1")}}
	g.Nodes[0].InputTokens = 1000 // used tokens, no price: counts $0
	e := NewEngine(g, newFakeExec(), 1, "")
	e.SetBudget(func() Budget { return Budget{LimitUSD: 5} })
	if _, blocked := func() (string, bool) { e.mu.Lock(); defer e.mu.Unlock(); return e.budgetBlockedLocked() }(); blocked {
		t.Fatal("unknown cost must not count")
	}
	e.SetBudget(func() Budget { return Budget{LimitUSD: 5, ExtraUSD: 5} })
	if err := e.Run(context.Background(), "T1"); err == nil {
		t.Fatal("the lead's cost counts toward the budget")
	}
}

func TestBudgetRunAllStopsStartingButKeepsRunning(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1"), work("T2", "T1"), work("T3", "T2")}}
	ex := newFakeExec()
	release := make(chan struct{})
	ex.behavior["T1"] = blockUntil(release)
	e := NewEngine(g, usageExec{ex, 6000, 0}, 1, "")
	e.SetCost(testCost)
	e.SetBudget(func() Budget { return Budget{LimitUSD: 5} })
	e.RunAll(context.Background())
	<-ex.started
	// T1 runs and has used $6: the budget is over, T1 is not killed.
	deadline := time.After(testWait)
	for e.Snapshot().Node("T1").CostUSD == 0 {
		select {
		case <-deadline:
			t.Fatal("usage not recorded")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if e.Snapshot().Node("T1").Status != StatusRunning {
		t.Fatal("a running node must not be killed")
	}
	close(release)
	waitEvent(t, e, EventDone, "T1")
	time.Sleep(100 * time.Millisecond)
	snap := e.Snapshot()
	if snap.Node("T2").Status != StatusTodo || snap.Node("T3").Status != StatusTodo {
		t.Fatalf("T2 %s T3 %s", snap.Node("T2").Status, snap.Node("T3").Status)
	}
}

func TestBudgetWarnsOnceThenReachedOnce(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := NewEngine(g, newFakeExec(), 1, "")
	spent := 0.0
	e.SetBudget(func() Budget { return Budget{LimitUSD: 10, WarnPct: 50, ExtraUSD: spent} })
	spent = 4
	e.CheckBudget()
	spent = 6
	e.CheckBudget()
	e.CheckBudget()
	spent = 7
	e.CheckBudget()
	spent = 10.5
	e.CheckBudget()
	e.CheckBudget()
	warn, reached := drainBudget(e)
	if len(warn) != 1 || len(reached) != 1 {
		t.Fatalf("warn %d reached %d", len(warn), len(reached))
	}
	if !strings.Contains(warn[0].Text, "$6.00 of $10.00") {
		t.Fatalf("warn text %q", warn[0].Text)
	}
	if !strings.HasPrefix(reached[0].Text, "Budget reached: $10.50 of $10.00") {
		t.Fatalf("reached text %q", reached[0].Text)
	}
}

func TestBudgetWarnDefaultsTo80(t *testing.T) {
	if (Budget{}).WarnShare() != 80 || (Budget{WarnPct: 150}).WarnShare() != 80 || (Budget{WarnPct: 60}).WarnShare() != 60 {
		t.Fatal("warn share")
	}
}

func TestUnpricedNodes(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", InputTokens: 5},
		{ID: "T2", InputTokens: 5, CostKnown: true},
		{ID: "T3"},
	}}
	if got := g.UnpricedNodes(); len(got) != 1 || got[0] != "T1" {
		t.Fatalf("%v", got)
	}
}

func TestSessionJSONBackwardCompat(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	s.Graph = &Graph{Title: "t", Nodes: []*Node{work("T1")}}
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(root, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := got.Graph.Node("T1")
	if got.BudgetUSD != 0 || n.CostUSD != 0 || n.CostKnown {
		t.Fatal("an old session has no cost and no budget override")
	}
	data, _ := os.ReadFile(sessionFile(root, s.ID))
	for _, key := range []string{"budget_usd", "cost_usd", "cost_known"} {
		if strings.Contains(string(data), key) {
			t.Errorf("%s must be omitted when empty", key)
		}
	}
	got.BudgetUSD = 7.5
	got.Graph.Node("T1").CostUSD, got.Graph.Node("T1").CostKnown = 1.25, true
	if err := got.Save(root); err != nil {
		t.Fatal(err)
	}
	again, err := LoadSession(root, s.ID)
	if err != nil || again.BudgetUSD != 7.5 || again.Graph.Node("T1").CostUSD != 1.25 || !again.Graph.Node("T1").CostKnown {
		t.Fatalf("round trip: %v %+v", err, again)
	}
}

func sessionFile(root, id string) string { p, _ := sessionPath(root, id); return p }

func TestRetryAddsCostAcrossAttempts(t *testing.T) {
	f := newFakeExec()
	var calls int32
	f.behavior["T1"] = func(context.Context) (Result, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return Result{}, errors.New("first")
		}
		return Result{Summary: "second"}, nil
	}
	g := &Graph{Nodes: []*Node{work("T1"), work("T2")}}
	e := NewEngine(g, usageExec{f, 3000, 0}, 2, "") // $3 per attempt
	defer e.StopAll()
	e.SetCost(testCost)
	e.SetBudget(func() Budget { return Budget{LimitUSD: 5} })
	ctx := context.Background()
	if err := e.Run(ctx, "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventFailed, "T1")
	if n := e.Snapshot().Node("T1"); n.CostUSD != 3 || !n.CostKnown {
		t.Fatalf("after the first attempt: cost %v known %v", n.CostUSD, n.CostKnown)
	}
	if err := e.Run(ctx, "T1"); err != nil { // $3 of $5: the gate lets the retry start
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	snap := e.Snapshot()
	if n := snap.Node("T1"); n.CostUSD != 6 || !n.CostKnown || n.InputTokens != 3000 {
		t.Fatalf("after the retry: cost %v known %v in %d", n.CostUSD, n.CostKnown, n.InputTokens)
	}
	if got := snap.SpentUSD(); got != 6 {
		t.Fatalf("spent %v, want 6", got)
	}
	err := e.Run(ctx, "T2")
	if err == nil || !strings.Contains(err.Error(), "Budget reached: $6.00 of $5.00") {
		t.Fatalf("the budget gate must see both attempts: %v", err)
	}
}
