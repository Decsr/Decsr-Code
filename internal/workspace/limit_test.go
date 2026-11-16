package workspace

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// limitedRun fails every run with a rate-limit error wrapped like the worker
// wraps stream errors.
func limitedRun(reset time.Time) func(context.Context) (Result, error) {
	return func(context.Context) (Result, error) {
		rl := &runner.RateLimitError{Provider: "claude", Detail: "5-hour limit reached", ResetAt: reset}
		return Result{}, fmt.Errorf("worker: stream: %w", rl)
	}
}

func TestEngineRateLimitMakesNodeWaitingWithLimit(t *testing.T) {
	reset := time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)
	ex := newFakeExec()
	ex.behavior["T1"] = limitedRun(reset)
	g := &Graph{Nodes: []*Node{work("T1"), work("T2", "T1")}}
	e := NewEngine(g, ex, 2, "")
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, e, EventLimited, "T1")
	if ev.Text != "5-hour limit reached" || !ev.ResetAt.Equal(reset) {
		t.Fatalf("event = %+v", ev)
	}
	n := e.Snapshot().Node("T1")
	if n.Status != StatusWaiting || n.Limit == nil || n.Error != "" {
		t.Fatalf("node = %+v", n)
	}
	if n.Limit.Provider != "claude" || !n.Limit.ResetAt.Equal(reset) || n.Limit.Retries != 0 {
		t.Fatalf("limit = %+v", n.Limit)
	}
	if !n.Limited() || e.Snapshot().CountLimited() != 1 || len(e.Limited()) != 1 {
		t.Error("node must count as limited")
	}
	// A dependent stays blocked and Run points at retry.
	if b := e.Snapshot().Blockers("T2"); len(b) != 1 {
		t.Errorf("blockers = %v", b)
	}
	if err := e.Run(context.Background(), "T1"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("run limited: %v", err)
	}
	e.RunAll(context.Background())
	time.Sleep(50 * time.Millisecond)
	if status(e, "T2") != StatusTodo {
		t.Error("dependent of a limited node must stay todo")
	}
}

func TestRetryLimitedCountsAndFailsAfterThree(t *testing.T) {
	ex := newFakeExec()
	ex.behavior["T1"] = limitedRun(time.Time{})
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, ex, 1, "")
	defer e.StopAll()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventLimited, "T1")
	for i := 1; i <= MaxLimitRetries; i++ {
		if err := e.RetryLimited("T1"); err != nil {
			t.Fatal(err)
		}
		waitEvent(t, e, EventLimited, "T1")
		n := e.Snapshot().Node("T1")
		if n.Limit == nil || n.Limit.Retries != i {
			t.Fatalf("retry %d: limit = %+v", i, n.Limit)
		}
		if n.AttemptNumber() != 1 || len(n.Attempts) != 0 {
			t.Fatalf("a retry must not archive an attempt: %+v", n)
		}
	}
	if err := e.RetryLimited("T1"); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, e, EventFailed, "T1")
	want := "limit reached on claude: 5-hour limit reached (3 automatic retries used)"
	if ev.Text != want {
		t.Fatalf("text = %q, want %q", ev.Text, want)
	}
	n := e.Snapshot().Node("T1")
	if n.Status != StatusFailed || n.Error != want || n.Limit != nil {
		t.Fatalf("node = %+v", n)
	}
	if err := e.RetryLimited("T1"); err == nil {
		t.Error("retrying a failed node must be an error")
	}
}

func TestRetryLimitedNowDoesNotCount(t *testing.T) {
	ex := newFakeExec()
	ex.behavior["T1"] = limitedRun(time.Time{})
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, ex, 1, "")
	defer e.StopAll()
	_ = e.Run(context.Background(), "T1")
	waitEvent(t, e, EventLimited, "T1")
	for i := 0; i < 5; i++ {
		if err := e.RetryLimitedNow("T1"); err != nil {
			t.Fatal(err)
		}
		waitEvent(t, e, EventLimited, "T1")
	}
	if n := e.Snapshot().Node("T1"); n.Limit == nil || n.Limit.Retries != 0 {
		t.Fatalf("user retries must not count: %+v", n.Limit)
	}
}

func TestRetrySuccessClearsLimit(t *testing.T) {
	var calls int32
	ex := newFakeExec()
	ex.behavior["T1"] = func(ctx context.Context) (Result, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return limitedRun(time.Time{})(ctx)
		}
		return Result{Summary: "fine"}, nil
	}
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, ex, 1, "")
	defer e.StopAll()
	_ = e.Run(context.Background(), "T1")
	waitEvent(t, e, EventLimited, "T1")
	if err := e.RetryLimited("T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	n := e.Snapshot().Node("T1")
	if n.Status != StatusDone || n.Limit != nil {
		t.Fatalf("node = %+v", n)
	}
}

func TestRetryLimitedNeedsLimitedNodeAndBudget(t *testing.T) {
	ex := newFakeExec()
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, ex, 1, "")
	defer e.StopAll()
	if err := e.RetryLimited("T1"); err == nil {
		t.Error("a todo node is not limited")
	}
	if err := e.RetryLimited("nope"); err == nil {
		t.Error("unknown node")
	}
}

func TestReplyToLimitedNodeRetriesWithMessage(t *testing.T) {
	var calls int32
	ex := newFakeExec()
	ex.behavior["T1"] = func(ctx context.Context) (Result, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return limitedRun(time.Time{})(ctx)
		}
		return Result{Summary: "fine"}, nil
	}
	e := NewEngine(&Graph{Nodes: []*Node{work("T1")}}, ex, 1, "")
	defer e.StopAll()
	_ = e.Run(context.Background(), "T1")
	waitEvent(t, e, EventLimited, "T1")
	res, err := e.Reply("T1", "use the smaller model")
	if err != nil || res.State != ReplyStarted {
		t.Fatalf("reply = %+v, %v", res, err)
	}
	waitEvent(t, e, EventDone, "T1")
	n := e.Snapshot().Node("T1")
	if n.Status != StatusDone || n.Limit != nil || n.Reply != "use the smaller model" {
		t.Fatalf("node = %+v", n)
	}
}

func TestLimitInfoCloneAndRoundTrip(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Status: StatusWaiting, Limit: &LimitInfo{Provider: "codex", Retries: 2}}}}
	c := g.Clone()
	c.Nodes[0].Limit.Retries = 9
	if g.Nodes[0].Limit.Retries != 2 {
		t.Error("Clone must copy Limit")
	}
}

func TestRetryLimitedResumesRunAll(t *testing.T) {
	var calls int32
	ex := newFakeExec()
	ex.behavior["T1"] = func(ctx context.Context) (Result, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return limitedRun(time.Time{})(ctx)
		}
		return Result{Summary: "fine"}, nil
	}
	e := NewEngine(&Graph{Nodes: []*Node{work("T1"), work("T2", "T1")}}, ex, 2, "")
	defer e.StopAll()
	e.RunAll(context.Background())
	waitEvent(t, e, EventLimited, "T1")
	if err := e.RetryLimited("T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T2")
}
