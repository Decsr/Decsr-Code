package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

func rvNode(id string, st Status, deps ...string) *Node {
	return &Node{ID: id, Title: "Title " + id, Description: "Do " + id, Kind: KindWork, Status: st, DependsOn: deps, Role: "coder"}
}

// rvGraph: T1 done, T2 todo (after T1), T3 failed, T4 running, T5 waiting, T6 todo after T2, CHECK after all.
func rvGraph() *Graph {
	g := &Graph{Task: "do it", Nodes: []*Node{
		rvNode("T1", StatusDone), rvNode("T2", StatusTodo, "T1"), rvNode("T3", StatusFailed),
		rvNode("T4", StatusRunning), rvNode("T5", StatusWaiting), rvNode("T6", StatusTodo, "T2", "T5"),
	}}
	g.Node("T3").Error = "boom\nsecond"
	g.AddCheckDeferred()
	return g
}

func rvKnown(s string) bool { return s == "coder" || s == "qa" }

func TestParseReviewShapes(t *testing.T) {
	body := `{"note":"on track","proposals":[{"action":"add","title":"New","description":"d","role":"coder","depends_on":["t1"]},{"action":"cancel","id":"t5","reason":"gone"},{"action":"change","id":"T2","depends_on":[]}]}`
	for name, raw := range map[string]string{
		"bare":   body,
		"fenced": "Here you go:\n```json\n" + body + "\n```\nDone.",
		"prose":  "I think " + body + " is right",
		"comma":  strings.Replace(body, `"depends_on":[]}]}`, `"depends_on":[],},]}`, 1),
	} {
		res := ParseReview(raw)
		if res.Note != "on track" || len(res.Proposals) != 3 {
			t.Fatalf("%s: %+v", name, res)
		}
		a, c, ch := res.Proposals[0], res.Proposals[1], res.Proposals[2]
		if a.Action != "add" || a.DependsOn[0] != "T1" || c.ID != "T5" || c.Reason != "gone" {
			t.Errorf("%s: %+v %+v", name, a, c)
		}
		if !ch.SetDeps || len(ch.DependsOn) != 0 {
			t.Errorf("%s: an empty depends_on on a change must replace the dependencies: %+v", name, ch)
		}
		if a.SetDeps != true {
			t.Errorf("%s: depends_on given", name)
		}
	}
	res := ParseReview(`{"note":"fine","proposals":[]}`)
	if res.Note != "fine" || len(res.Proposals) != 0 {
		t.Errorf("empty = %+v", res)
	}
	if ch := ParseReview(`{"action":"change","id":"T2","title":"x"}`).Proposals; len(ch) != 0 {
		t.Errorf("a bare proposal is not an answer: %+v", ch)
	}
}

func TestParseReviewGarbage(t *testing.T) {
	for _, raw := range []string{"", "I cannot do that.", "{not json", `{"proposals": 5}`, "[1,2]", `{"foo":1}`} {
		res := ParseReview(raw)
		if len(res.Proposals) != 0 || !strings.Contains(res.Note, "not valid JSON") {
			t.Errorf("%q -> %+v", raw, res)
		}
	}
}

func TestApplyValidation(t *testing.T) {
	cases := []struct {
		name string
		p    Proposal
		want string // reason substring; "" = applied
	}{
		{"add ok", Proposal{Action: "add", Title: "N", DependsOn: []string{"T1"}}, ""},
		{"add no title", Proposal{Action: "add"}, "needs a title"},
		{"add missing dep", Proposal{Action: "add", Title: "N", DependsOn: []string{"T99"}}, "does not exist"},
		{"add on CHECK", Proposal{Action: "add", Title: "N", DependsOn: []string{"CHECK"}}, "CHECK"},
		{"change todo", Proposal{Action: "change", ID: "T2", Title: "x"}, ""},
		{"change failed", Proposal{Action: "change", ID: "T3", Description: "again"}, ""},
		{"change done", Proposal{Action: "change", ID: "T1", Title: "x"}, "done"},
		{"change running", Proposal{Action: "change", ID: "T4", Title: "x"}, "running"},
		{"change waiting", Proposal{Action: "change", ID: "T5", Title: "x"}, "waiting"},
		{"change CHECK", Proposal{Action: "change", ID: "CHECK", Title: "x"}, "Build & test"},
		{"change unknown", Proposal{Action: "change", ID: "T9", Title: "x"}, "does not exist"},
		{"change nothing", Proposal{Action: "change", ID: "T2"}, "nothing to change"},
		{"change self dep", Proposal{Action: "change", ID: "T2", DependsOn: []string{"T2"}, SetDeps: true}, "itself"},
		{"change cycle", Proposal{Action: "change", ID: "T2", DependsOn: []string{"T6"}, SetDeps: true}, "cycle"},
		{"cancel todo", Proposal{Action: "cancel", ID: "T2"}, ""},
		{"cancel failed", Proposal{Action: "cancel", ID: "T3"}, ""},
		{"cancel waiting", Proposal{Action: "cancel", ID: "T5"}, ""},
		{"cancel done", Proposal{Action: "cancel", ID: "T1"}, "done"},
		{"cancel running", Proposal{Action: "cancel", ID: "T4"}, "running"},
		{"cancel CHECK", Proposal{Action: "cancel", ID: "CHECK"}, "Build & test"},
		{"unknown action", Proposal{Action: "merge", ID: "T2"}, "unknown action"},
	}
	for _, tc := range cases {
		g := rvGraph()
		applied, skipped := g.ApplyProposals([]Proposal{tc.p}, &ApplyOptions{KnownRole: rvKnown})
		if tc.want == "" {
			if len(applied) != 1 || len(skipped) != 0 {
				t.Errorf("%s: applied %v skipped %v", tc.name, applied, skipped)
			}
		} else if len(skipped) != 1 || !strings.Contains(skipped[0].Reason, tc.want) || len(applied) != 0 {
			t.Errorf("%s: applied %v skipped %+v, want reason %q", tc.name, applied, skipped, tc.want)
		}
		if err := g.Validate(); err != nil {
			t.Errorf("%s: graph invalid after: %v", tc.name, err)
		}
	}
}

func TestApplyAddJoinsGraphAndCheck(t *testing.T) {
	g := rvGraph()
	g.Node("CHECK").Status = StatusDone
	opts := &ApplyOptions{KnownRole: rvKnown, NextID: 7}
	applied, _ := g.ApplyProposals([]Proposal{
		{Action: "add", Title: "  Add   docs ", Role: "qa", DependsOn: []string{"T1", "T1"}},
		{Action: "add", Title: "Odd role", Role: "Data Wizard"},
	}, opts)
	if len(applied) != 2 || applied[0].NodeID != "T7" || applied[1].NodeID != "T8" || opts.NextID != 9 {
		t.Fatalf("applied %+v next %d", applied, opts.NextID)
	}
	n := g.Node("T7")
	if n.Title != "Add docs" || n.Role != "qa" || n.Description != "Add docs" || n.Status != StatusTodo || len(n.DependsOn) != 1 {
		t.Errorf("T7 = %+v", n)
	}
	o := g.Node("T8")
	if o.Role != "" || o.SuggestedRole != "data_wizard" {
		t.Errorf("an unknown role is only suggested: %+v", o)
	}
	c := g.Node("CHECK")
	if c.Status != StatusTodo || !strings.Contains(strings.Join(c.DependsOn, ","), "T7") || !strings.Contains(strings.Join(c.DependsOn, ","), "T8") {
		t.Errorf("CHECK = %+v", c)
	}
}

func TestApplyChange(t *testing.T) {
	g := rvGraph()
	applied, _ := g.ApplyProposals([]Proposal{
		{Action: "change", ID: "T3", Title: "Retry it", Role: "Mystery", Description: "new text"},
		{Action: "change", ID: "T6", DependsOn: []string{"T1"}, SetDeps: true},
	}, &ApplyOptions{KnownRole: rvKnown})
	if len(applied) != 2 {
		t.Fatalf("applied %+v", applied)
	}
	n := g.Node("T3")
	if n.Title != "Retry it" || n.Description != "new text" || n.Status != StatusTodo || n.Error != "" || n.Role != "" || n.SuggestedRole != "mystery" {
		t.Errorf("T3 = %+v", n)
	}
	if d := g.Node("T6").DependsOn; len(d) != 1 || d[0] != "T1" {
		t.Errorf("T6 deps = %v", d)
	}
	if g.Node("T2").Title != "Title T2" {
		t.Error("an unrelated node changed")
	}
}

func TestApplyCancelDropsEdgesAndSummary(t *testing.T) {
	g := rvGraph()
	applied, _ := g.ApplyProposals([]Proposal{{Action: "cancel", ID: "T2", Reason: "not needed"}, {Action: "cancel", ID: "T5"}}, nil)
	if len(applied) != 2 || g.Node("T2") != nil || g.Node("T5") != nil {
		t.Fatalf("applied %+v", applied)
	}
	if d := g.Node("T6").DependsOn; len(d) != 0 {
		t.Errorf("T6 still depends on %v", d)
	}
	for _, d := range g.Node("CHECK").DependsOn {
		if d == "T2" || d == "T5" {
			t.Errorf("CHECK depends on cancelled %s", d)
		}
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	sum := CancelSummary("- T1 Title T1 [coder]", applied)
	want := "- T1 Title T1 [coder]\n- T2 cancelled by the lead: not needed\n- T5 cancelled by the lead"
	if sum != want {
		t.Errorf("summary = %q", sum)
	}
	if CancelSummary("x", []AppliedProposal{{Proposal: Proposal{Action: "add"}}}) != "x" {
		t.Error("only cancels add a line")
	}
}

func TestApplyCapsAtEight(t *testing.T) {
	g := rvGraph()
	var ps []Proposal
	for i := 0; i < 10; i++ {
		ps = append(ps, Proposal{Action: "add", Title: "N"})
	}
	applied, skipped := g.ApplyProposals(ps, &ApplyOptions{})
	if len(applied) != MaxProposals || len(skipped) != 2 || !strings.Contains(skipped[0].Reason, "more than 8") {
		t.Errorf("applied %d skipped %+v", len(applied), skipped)
	}
}

func TestEngineProposalsAndRounds(t *testing.T) {
	g := rvGraph()
	g.Node("T4").Status = StatusDone
	var saved int
	e := NewEngineWithSave(g, nil, 2, func(*Graph) error { saved++; return nil })
	ps := []Proposal{{Action: "add", Title: "Extra"}}
	pre, _ := e.PreviewProposals(ps, &ApplyOptions{NextID: 7})
	if len(pre) != 1 || e.Snapshot().Node("T7") != nil || saved != 0 {
		t.Fatal("a preview must not change or save the graph")
	}
	applied, _ := e.ApplyProposals(ps, &ApplyOptions{NextID: 7})
	if len(applied) != 1 || e.Snapshot().Node("T7") == nil || saved != 1 {
		t.Fatalf("applied %v saved %d", applied, saved)
	}
	if e.StartReview() != 1 || e.StartReview() != 2 || e.ReviewRounds() != 2 {
		t.Error("rounds do not count")
	}
	add := &Graph{Task: "t2", Nodes: []*Node{rvNode("T1", StatusTodo)}}
	if err := e.Replace(func(g *Graph) { g.Append(add, 20) }); err != nil {
		t.Fatal(err)
	}
	if e.ReviewRounds() != 0 {
		t.Error("a new task must reset the rounds")
	}
}

func TestReviewPromptHasTableAndBudgetedExcerpt(t *testing.T) {
	g := rvGraph()
	g.Node("T5").Report = &Report{Status: "question", Summary: "stuck", Question: "Which db?"}
	g.Node("T1").Report = &Report{Status: "done", Summary: "all good"}
	g.Node("T1").Files = []string{"a.go", "b.go"}
	huge := strings.Repeat("compile error line\n", 2000)
	n := g.Node("T3")
	n.Activity = []runner.Activity{{ID: "a1", Kind: runner.ActCommand, Title: "go build", State: runner.ActFailed, Detail: huge}}
	p := BuildReviewPrompt(ReviewInput{Task: "Build the thing", Summary: "- T0 earlier", Graph: g, Roles: []Role{{Name: "coder", Description: "Writes code"}}})
	for _, want := range []string{"Build the thing", "- T0 earlier", "- coder: Writes code",
		"T1 | Title T1 | work | coder | done | report done: all good | files 2",
		"T3 | Title T3 | work | coder | failed | error: boom | files 0",
		"T5 | Title T5 | work | coder | waiting | report question: stuck | question: Which db?",
		"CHECK | Build & test | check", "characters omitted", "never answer it"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "second") {
		t.Error("only the first line of an error goes in the table")
	}
	start := strings.Index(p, "## What failed")
	end := strings.Index(p, "## Rules")
	if got := utf8.RuneCountInString(p[start:end]); got > ReviewExcerptChars+100 {
		t.Errorf("failure section is %d chars, budget %d", got, ReviewExcerptChars)
	}
	if strings.Contains(p, "{{") {
		t.Error("unreplaced placeholder")
	}
	g.Node("T3").Status = StatusDone
	if p := BuildReviewPrompt(ReviewInput{Graph: g}); !strings.Contains(p, "(nothing failed)") || !strings.Contains(p, "(none)") {
		t.Error("empty sections have a placeholder")
	}
}

func TestSessionReviewsRoundTripAndCompat(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	yes := true
	s.Coordinator = &yes
	s.Reviews = []Review{{Round: 1, At: time.Now().UTC().Truncate(time.Second), Note: "n", State: ReviewPending,
		Proposals: []Proposal{{Action: "cancel", ID: "T2", Reason: "r"}},
		Applied:   []AppliedProposal{{Proposal: Proposal{Action: "cancel", ID: "T2", Reason: "r"}, NodeID: "T2", Text: "cancelled T2: r"}},
		Skipped:   []SkippedProposal{{Proposal: Proposal{Action: "change", ID: "T1"}, Reason: "T1 is done"}}}}
	s.AddLine("you", "hi")
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(root, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Coordinator == nil || !*got.Coordinator || len(got.Reviews) != 1 || got.Reviews[0].Skipped[0].Reason != "T1 is done" ||
		got.Reviews[0].Applied[0].Text != "cancelled T2: r" || got.Reviews[0].Proposals[0].Reason != "r" {
		t.Errorf("round trip lost data: %+v", got.Reviews)
	}
	// An old session file has neither field.
	old := NewSession()
	if err := old.Save(root); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(old)
	if strings.Contains(string(raw), "reviews") || strings.Contains(string(raw), "coordinator") {
		t.Errorf("empty fields must be omitted: %s", raw)
	}
	back, err := LoadSession(root, old.ID)
	if err != nil || back.Coordinator != nil || back.Reviews != nil {
		t.Errorf("old session: %+v %v", back, err)
	}
}

func TestRemovedNodesKeepTheirCost(t *testing.T) {
	g := rvGraph()
	g.Node("T1").CostUSD = 1.5
	g.Node("T3").CostUSD = 0.25
	g.Node("T2").CostUSD = 0.5
	if got := g.SpentUSD(); got != 2.25 {
		t.Fatalf("spent = %v", got)
	}
	// Cancel through a proposal.
	g.ApplyProposals([]Proposal{{Action: "cancel", ID: "T3"}}, nil)
	if g.Node("T3") != nil || g.SpentUSD() != 2.25 || g.RemovedCostUSD != 0.25 {
		t.Errorf("after cancel: spent %v removed %v", g.SpentUSD(), g.RemovedCostUSD)
	}
	// Compact folds the done T1 only when nothing unfinished needs it.
	g.Node("T2").DependsOn = nil
	g.Node("T6").DependsOn = nil
	if len(g.Compact()) == 0 {
		t.Fatal("nothing compacted")
	}
	if g.Node("T1") != nil || g.SpentUSD() != 2.25 || g.RemovedCostUSD != 1.75 {
		t.Errorf("after compact: spent %v removed %v", g.SpentUSD(), g.RemovedCostUSD)
	}
	// The budget gate sees the full spend.
	e := NewEngineWithSave(g, nil, 1, nil)
	e.SetBudget(func() Budget { return Budget{LimitUSD: 2} })
	e.mu.Lock()
	_, blocked := e.budgetBlockedLocked()
	e.mu.Unlock()
	if !blocked {
		t.Error("the budget gate lost the cost of removed nodes")
	}
	raw, _ := json.Marshal(g)
	var back Graph
	if err := json.Unmarshal(raw, &back); err != nil || back.RemovedCostUSD != 1.75 {
		t.Errorf("round trip: %v %v", back.RemovedCostUSD, err)
	}
	var old Graph
	if err := json.Unmarshal([]byte(`{"task":"x","nodes":[]}`), &old); err != nil || old.RemovedCostUSD != 0 {
		t.Errorf("old file: %v %v", old.RemovedCostUSD, err)
	}
	if strings.Contains(string((func() []byte { b, _ := json.Marshal(&Graph{}); return b })()), "removed_cost_usd") {
		t.Error("zero must be omitted")
	}
}
