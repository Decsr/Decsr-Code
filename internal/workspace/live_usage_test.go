package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

// liveExec reports a model, live usage and then the final usage of a node,
// pausing between the live and the final part so a test can look at the node.
type liveExec struct {
	live    chan struct{} // closed once the live events are processed
	release chan struct{}
}

func (l liveExec) Run(ctx context.Context, _ *Graph, _ *Node, emit func(Event)) (Result, error) {
	emit(Event{Type: EventModel, Model: "configured", Effort: "high"})
	emit(Event{Type: EventModel, Model: "real-model", Effort: "high"})
	emit(Event{Type: EventUsage, Live: true, In: 100, Cached: 40, Out: 10, Estimated: true, Runner: "deepseek", Model: "deepseek-v4"})
	emit(Event{Type: EventUsage, Live: true, In: 200, Cached: 80, Out: 30, Estimated: true, Runner: "deepseek", Model: "deepseek-v4"})
	close(l.live)
	select {
	case <-l.release:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	emit(Event{Type: EventUsage, In: 1000, Cached: 600, Out: 500, Runner: "deepseek", Model: "deepseek-v4"})
	return Result{Summary: "ok"}, nil
}

func TestEngineStoresLiveUsageModelAndEffort(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1")}}
	ex := liveExec{live: make(chan struct{}), release: make(chan struct{})}
	e := NewEngine(g, ex, 1, "")
	e.SetCost(testCost)
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	<-ex.live
	// The engine handles the events in order on one goroutine; wait until the
	// second live snapshot is stored.
	deadline := time.After(testWait)
	for {
		n := e.Snapshot().Node("T1")
		if n.InputTokens == 200 {
			if n.CachedTokens != 80 || n.OutputTokens != 30 || !n.Estimated {
				t.Fatalf("live usage = %+v", n)
			}
			if n.Model != "real-model" || n.Effort != "high" {
				t.Fatalf("model/effort = %q/%q", n.Model, n.Effort)
			}
			if n.CostUSD != 0.23 || !n.CostKnown {
				t.Fatalf("live cost = %v known %v", n.CostUSD, n.CostKnown)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("live usage never reached the node")
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(ex.release)
	waitEvent(t, e, EventDone, "T1")
	n := e.Snapshot().Node("T1")
	if n.InputTokens != 1000 || n.CachedTokens != 600 || n.OutputTokens != 500 || n.Estimated {
		t.Fatalf("final usage = %+v", n)
	}
	if n.CostUSD != 1.5 || !n.CostKnown {
		t.Fatalf("final cost = %v known %v", n.CostUSD, n.CostKnown)
	}
}

func TestExecEmitsModelEffortAndLiveUsage(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents["docs_writer"] = config.AgentConfig{Runner: "claude", Model: "m1", Effort: " High ", Description: "Writes docs."}
	ex := newTestExec(t, cfg, &execFake{text: "done\n===SUMMARY===\nok"}, nil)
	ex.newRunner = func(config.AgentConfig) (runner.LLMRunner, error) {
		return progressRunner{}, nil
	}
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Docs", Kind: KindWork, Role: "docs_writer"}}}
	var evs []Event
	if _, err := ex.Run(context.Background(), g, g.Nodes[0], collect(&evs)); err != nil {
		t.Fatal(err)
	}
	var models []Event
	live, final := 0, 0
	for _, e := range evs {
		switch {
		case e.Type == EventModel:
			models = append(models, e)
		case e.Type == EventUsage && e.Live:
			live++
		case e.Type == EventUsage:
			final++
			if e.In != 9 || e.Cached != 3 || e.Out != 6 || e.Estimated {
				t.Errorf("final usage event = %+v", e)
			}
		}
	}
	if len(models) != 2 || models[0].Model != "m1" || models[0].Effort != "high" || models[1].Model != "reported" || models[1].Effort != "high" {
		t.Errorf("model events = %+v", models)
	}
	// Three progress tokens arrive back to back: the throttle lets one through.
	if live != 1 || final != 1 {
		t.Errorf("live = %d, final = %d", live, final)
	}
}

type progressRunner struct{}

func (progressRunner) Complete(context.Context, runner.CompletionRequest) (<-chan runner.Token, error) {
	ch := make(chan runner.Token, 8)
	ch <- runner.Token{Model: "reported"}
	for i := 1; i <= 3; i++ {
		ch <- runner.Token{Progress: &runner.TokenUsage{InputTokens: 5, OutputTokens: i, Estimated: true}}
	}
	ch <- runner.Token{Text: "done\n===SUMMARY===\nok"}
	ch <- runner.Token{Done: true, Usage: &runner.TokenUsage{InputTokens: 9, CachedTokens: 3, OutputTokens: 6}}
	close(ch)
	return ch, nil
}

func TestUsageThrottleAllowsOnePerInterval(t *testing.T) {
	now := time.Unix(100, 0)
	th := newUsageThrottle(func() time.Time { return now })
	allowed := 0
	for i := 0; i < 10; i++ { // 10 events, 100ms apart: 1 second
		if th.allow() {
			allowed++
		}
		now = now.Add(100 * time.Millisecond)
	}
	if allowed != 2 {
		t.Errorf("allowed = %d, want 2 (at 0ms and 500ms)", allowed)
	}
}

func TestOldSessionJSONLoadsWithoutNewFields(t *testing.T) {
	old := `{"nodes":[{"id":"T1","title":"t","description":"d","kind":"work","role":"coder","status":"done","input_tokens":12,"output_tokens":3,"estimated":true}]}`
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	n := g.Node("T1")
	if n.InputTokens != 12 || n.CachedTokens != 0 || n.Model != "" || n.Effort != "" {
		t.Errorf("node = %+v", n)
	}
	// New fields are omitted when empty, so the format stays the same.
	data, _ := json.Marshal(&Node{ID: "T2"})
	for _, k := range []string{"cached_tokens", `"model"`, `"effort"`} {
		if json.Valid(data) && contains(string(data), k) {
			t.Errorf("empty node JSON holds %s: %s", k, data)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
