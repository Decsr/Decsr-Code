package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ranking"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

var allConnected = map[string]bool{"claude": true, "codex": true, "dsh": true, "deepseek": true, "openai": true}

var (
	opusMedium = config.AgentConfig{Runner: "claude", Model: "claude-opus-5-5", Effort: "medium"}
	proHigh    = config.AgentConfig{Runner: "dsh", Model: "deepseek-v4-pro", Effort: "high"}
)

func lookup(t *testing.T, tbl ranking.Table, a AgentAssign) ranking.Candidate {
	t.Helper()
	c, ok := tbl.Lookup(a.Runner, a.Model, a.Effort)
	if !ok {
		t.Fatalf("assignment %+v is not ranked", a)
	}
	return c
}

func TestPickAgentCapAndFloors(t *testing.T) {
	tbl := ranking.Table{}
	ref, _ := tbl.Lookup("claude", "claude-opus-5-5", "medium")
	for _, grade := range []string{GradeSmall, GradeMedium} {
		for _, mode := range []string{config.RoutingAutoRole, config.RoutingAutoConnected} {
			a, _ := PickAgent(tbl, opusMedium, grade, mode, allConnected)
			c := lookup(t, tbl, a)
			if c.CostPerTask > ref.CostPerTask {
				t.Errorf("%s/%s: cost %.2f is above the role pair's %.2f", grade, mode, c.CostPerTask, ref.CostPerTask)
			}
			if grade == GradeSmall && c.Intelligence < 30 {
				t.Errorf("small/%s: intelligence %.1f below 30", mode, c.Intelligence)
			}
			if grade == GradeMedium && c.Intelligence < 0.85*ref.Intelligence {
				t.Errorf("medium/%s: intelligence %.1f below 85%% of %.1f", mode, c.Intelligence, ref.Intelligence)
			}
		}
	}
}

func TestPickAgentLargeKeepsRolePair(t *testing.T) {
	a, why := PickAgent(ranking.Table{}, opusMedium, GradeLarge, config.RoutingAutoConnected, allConnected)
	if a.Runner != "claude" || a.Model != "claude-opus-5-5" || a.Effort != "medium" || a.Source != SourceRole || a.Grade != GradeLarge || why == "" {
		t.Fatalf("%+v %q", a, why)
	}
	if a.EstCostUSD <= 0 {
		t.Errorf("the estimate of the role pair is missing: %+v", a)
	}
}

func TestPickAgentMissingGradeIsMedium(t *testing.T) {
	a, _ := PickAgent(ranking.Table{}, opusMedium, "", config.RoutingAutoRole, allConnected)
	if a.Grade != GradeMedium {
		t.Fatalf("grade = %q", a.Grade)
	}
}

// A coder on opus medium can get sonnet or haiku in its own runner.
func TestPickAgentOpusCoderGetsSonnetOrHaiku(t *testing.T) {
	a, _ := PickAgent(ranking.Table{}, opusMedium, GradeMedium, config.RoutingAutoRole, allConnected)
	if a.Source != SourceAuto || a.Runner != "claude" || !strings.Contains(a.Model, "sonnet") {
		t.Fatalf("medium: %+v", a)
	}
	// With the whole weight on cost, a small node goes to the cheapest model
	// above the floor: haiku.
	cheap := ranking.Table{}.With(ranking.Settings{Weights: ranking.Weights{Cost: 100}})
	a, _ = PickAgent(cheap, opusMedium, GradeSmall, config.RoutingAutoRole, allConnected)
	if a.Runner != "claude" || !strings.Contains(a.Model, "haiku") {
		t.Fatalf("small, cost first: %+v", a)
	}
}

// A coder on deepseek-v4-pro can get deepseek-flash.
func TestPickAgentDeepseekProGetsFlash(t *testing.T) {
	for _, mode := range []string{config.RoutingAutoRole, config.RoutingAutoConnected} {
		a, _ := PickAgent(ranking.Table{}, proHigh, GradeMedium, mode, allConnected)
		if a.Model != "deepseek-flash" || a.Source != SourceAuto {
			t.Errorf("%s: %+v", mode, a)
		}
	}
}

func TestPickAgentAutoConnectedCrossesRunners(t *testing.T) {
	a, _ := PickAgent(ranking.Table{}, opusMedium, GradeMedium, config.RoutingAutoConnected, allConnected)
	if a.Runner == "claude" {
		t.Fatalf("an opus lead may give a node to another connected runner: %+v", a)
	}
	// Only the role's runner is connected: nothing else is allowed.
	a, _ = PickAgent(ranking.Table{}, opusMedium, GradeMedium, config.RoutingAutoConnected, map[string]bool{"claude": true})
	if a.Runner != "claude" {
		t.Fatalf("disconnected runner picked: %+v", a)
	}
}

func TestPickAgentUnrankedAndEmpty(t *testing.T) {
	local := config.AgentConfig{Runner: "ollama", Model: "llama3", Effort: ""}
	a, _ := PickAgent(ranking.Table{}, local, GradeSmall, config.RoutingAutoConnected, allConnected)
	if a.Runner != "ollama" || a.Model != "llama3" || a.Source != SourceRole {
		t.Fatalf("unranked role model must keep its pair: %+v", a)
	}
	// A table with no usable pair (fallback emptied by the user's overrides is
	// not possible), so a role the table does not know is the empty case.
	a, _ = PickAgent(ranking.Table{}, config.AgentConfig{Runner: "claude", Model: "claude-unknown-9"}, GradeSmall, config.RoutingAutoRole, allConnected)
	if a.Model != "claude-unknown-9" || a.Source != SourceRole {
		t.Fatalf("%+v", a)
	}
}

func TestPickAgentManualKeepsRole(t *testing.T) {
	a, _ := PickAgent(ranking.Table{}, opusMedium, GradeSmall, config.RoutingManual, allConnected)
	if a.Source != SourceRole || a.Model != "claude-opus-5-5" {
		t.Fatalf("%+v", a)
	}
	a, _ = PickAgent(ranking.Table{}, opusMedium, GradeSmall, "garbage", allConnected)
	if a.Source != SourceRole {
		t.Fatalf("an unknown mode is manual: %+v", a)
	}
}

func TestPickAgentBestBalanceWins(t *testing.T) {
	tbl := ranking.Table{}
	a, _ := PickAgent(tbl, opusMedium, GradeSmall, config.RoutingAutoConnected, allConnected)
	got := lookup(t, tbl, a)
	ref, _ := tbl.Lookup("claude", "claude-opus-5-5", "medium")
	for _, c := range tbl.Candidates(ranking.RankableRunners) {
		if allConnected[c.Runner] && c.CostPerTask > 0 && c.CostPerTask <= ref.CostPerTask && c.Intelligence >= 30 && c.Balance > got.Balance {
			t.Errorf("%s %s %s (%.1f) beats the pick %s %s %s (%.1f)", c.Runner, c.Model, c.Effort, c.Balance, got.Runner, got.Model, got.Effort, got.Balance)
		}
	}
}

// --- split parsing ---

func TestParseSplitDifficulty(t *testing.T) {
	out := `===TITLE===
T
===TASKS===
[{"key":"T1","title":"a","description":"d","role":"coder","depends_on":[],"difficulty":"Small","reason":"one line"},
 {"key":"T2","title":"b","description":"d","role":"coder","depends_on":[],"difficulty":"huge"},
 {"key":"T3","title":"c","description":"d","role":"coder","depends_on":[]}]
===NEW_ROLES===
[]
===END===`
	roles := []Role{{Name: "coder"}}
	g, err := ParseSplitOpts("task", out, roles, nil, SplitOptions{Routing: true})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{g.Nodes[0].Difficulty, g.Nodes[1].Difficulty, g.Nodes[2].Difficulty}
	if got[0] != "small" || got[1] != "medium" || got[2] != "medium" || g.Nodes[0].DifficultyReason != "one line" {
		t.Fatalf("difficulty = %v reason %q", got, g.Nodes[0].DifficultyReason)
	}
	g, err = ParseSplitOpts("task", out, roles, nil, SplitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[0].Difficulty != "" {
		t.Fatalf("manual mode must not grade: %q", g.Nodes[0].Difficulty)
	}
}

func TestSplitPromptRoutingSectionKeepsPrefix(t *testing.T) {
	roles := []Role{{Name: "coder", Description: "Codes"}}
	off := buildSplitPrompt(roles, SplitOptions{})
	on := buildSplitPrompt(roles, SplitOptions{Routing: true})
	if strings.Contains(off, `"difficulty"`) || !strings.Contains(on, `"difficulty"`) {
		t.Fatal("the grading instruction belongs to the routing modes only")
	}
	cut := strings.Index(on, "## Grading")
	if cut < 0 || off[:cut] != on[:cut] {
		t.Fatal("the part before the routing section must be the same in every mode")
	}
	if strings.Contains(on, "claude-opus") {
		t.Fatal("no model list in the prompt")
	}
}

// --- engine: approval ---

func routedEngine(t *testing.T, mode string) (*Engine, *fakeExec) {
	t.Helper()
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "a", Kind: KindWork, Status: StatusTodo, Role: "coder", Difficulty: GradeMedium},
		{ID: "T2", Title: "b", Kind: KindWork, Status: StatusTodo, SuggestedRole: "coder", Difficulty: GradeSmall, DependsOn: []string{"T1"}},
		{ID: "T3", Title: "c", Kind: KindWork, Status: StatusTodo, SuggestedRole: "ghost"},
	}}
	g.AddCheckDeferred()
	fx := newFakeExec()
	e := NewEngine(g, fx, 3, "")
	e.SetRouter(&Router{
		Mode: mode, Connected: allConnected,
		Role: func(name string) (config.AgentConfig, bool) { return opusMedium, name == "coder" },
	})
	return e, fx
}

func TestProposeMarksPendingAndGatesStart(t *testing.T) {
	for _, mode := range []string{config.RoutingManual, config.RoutingAutoRole} {
		e, _ := routedEngine(t, mode)
		if n := e.Propose(); n != 2 {
			t.Fatalf("%s: pending = %d", mode, n)
		}
		snap := e.Snapshot()
		if snap.Node("T3").Assign != nil {
			t.Errorf("%s: a node with an undefined role gets no assignment", mode)
		}
		a := snap.Node("T1").Assign
		if a == nil || !a.Pending || a.Grade != GradeMedium {
			t.Fatalf("%s: T1 assign = %+v", mode, a)
		}
		if mode == config.RoutingManual && (a.Source != SourceRole || a.Model != "claude-opus-5-5") {
			t.Errorf("manual proposes the role's agent: %+v", a)
		}
		if mode == config.RoutingAutoRole && a.Source != SourceAuto {
			t.Errorf("auto-role source = %q", a.Source)
		}
		if err := e.Run(context.Background(), "T1"); err == nil || !strings.Contains(err.Error(), "approval") {
			t.Errorf("%s: Run before approval: %v", mode, err)
		}
		if got := e.PendingApproval(); len(got) != 2 {
			t.Errorf("%s: PendingApproval = %d", mode, len(got))
		}
		e.RunAll(context.Background())
		if e.Running() != 0 {
			t.Errorf("%s: a pending node started", mode)
		}
		if n := e.ApproveAssignments(); n != 2 {
			t.Fatalf("%s: approved %d", mode, n)
		}
		e.RunAll(context.Background())
		waitEvent(t, e, EventStarted, "T1")
		e.Close()
	}
}

func TestApproveWithoutAutoStartsOnRun(t *testing.T) {
	e, fx := routedEngine(t, config.RoutingAutoRole)
	e.Propose()
	e.ApproveAssignments()
	if len(e.PendingApproval()) != 0 {
		t.Fatal("still pending")
	}
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, e, 1)
	_ = fx
}

func TestRejectSplitRemovesPendingNodes(t *testing.T) {
	e, _ := routedEngine(t, config.RoutingAutoRole)
	e.Propose()
	ids := e.RejectSplit()
	if strings.Join(ids, ",") != "T1,T2" {
		t.Fatalf("removed %v", ids)
	}
	g := e.Snapshot()
	if g.Node("T1") != nil || g.Node("T2") != nil || g.Node("T3") == nil {
		t.Fatalf("nodes after reject: %d", len(g.Nodes))
	}
	if c := g.Node("CHECK"); c != nil {
		for _, d := range c.DependsOn {
			if d == "T1" || d == "T2" {
				t.Errorf("CHECK still depends on %s", d)
			}
		}
	}
	if e.RejectSplit() != nil {
		t.Error("nothing is left to reject")
	}
}

func TestSetAssignmentManualEdit(t *testing.T) {
	e, _ := routedEngine(t, config.RoutingAutoRole)
	e.Propose()
	err := e.SetAssignment("T1", AgentAssign{Runner: "dsh", Model: "deepseek-flash", Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	a := e.Snapshot().Node("T1").Assign
	if a.Source != SourceManual || a.Runner != "dsh" || !a.Pending || a.Grade != GradeMedium {
		t.Fatalf("%+v", a)
	}
	for _, bad := range []AgentAssign{{}, {Runner: "nope", Model: "x"}} {
		if e.SetAssignment("T1", bad) == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
	if e.SetAssignment("CHECK", AgentAssign{Runner: "claude"}) == nil || e.SetAssignment("ZZ", AgentAssign{Runner: "claude"}) == nil {
		t.Error("a check or unknown node takes no agent")
	}
	// A manual pick survives a role change; an automatic one is dropped.
	e.Assign("T1", "coder")
	if e.Snapshot().Node("T1").Assign.Source != SourceManual {
		t.Error("manual pick lost")
	}
	e.Assign("T2", "other") // no router role "other": proposal dropped
	if e.Snapshot().Node("T2").Assign != nil {
		t.Error("stale automatic proposal kept")
	}
}

func TestAssignmentPersistsAndOldSessionsLoad(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	s.RoutingMode = config.RoutingAutoRole
	s.Graph = &Graph{Task: "t", Nodes: []*Node{{ID: "T1", Kind: KindWork, Status: StatusTodo, Role: "coder",
		Difficulty: GradeSmall, Assign: &AgentAssign{Runner: "claude", Model: "m", Source: SourceAuto, Reason: "r", Pending: true, EstCostUSD: 0.5}}}}
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(root, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := got.Graph.Node("T1").Assign
	if got.RoutingMode != config.RoutingAutoRole || a == nil || !a.Pending || a.Model != "m" || a.EstCostUSD != 0.5 || got.Graph.Node("T1").Difficulty != GradeSmall {
		t.Fatalf("round trip: %q %+v", got.RoutingMode, a)
	}
	// A session written before routing existed has none of the fields.
	old := `{"id":"20260101-000000-abcd","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","graph":{"task":"t","title":"t","nodes":[{"id":"T1","title":"a","description":"d","kind":"work","role":"coder","status":"todo"}]}}`
	var os Session
	if err := json.Unmarshal([]byte(old), &os); err != nil {
		t.Fatal(err)
	}
	if n := os.Graph.Node("T1"); n.Assign != nil || n.AwaitingApproval() || os.RoutingMode != "" {
		t.Fatalf("old session: %+v", n)
	}
	if len(os.Graph.Runnable()) != 1 {
		t.Fatal("an old node must stay runnable")
	}
}

func TestCloneCopiesAssign(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Kind: KindWork, Assign: &AgentAssign{Model: "a"}}}}
	c := g.Clone()
	c.Nodes[0].Assign.Model = "b"
	if g.Nodes[0].Assign.Model != "a" {
		t.Fatal("Clone shares the assignment")
	}
	into := &Graph{}
	into.Append(g, 1)
	into.Nodes[0].Assign.Model = "c"
	if g.Nodes[0].Assign.Model != "a" {
		t.Fatal("Append shares the assignment")
	}
}

// --- exec ---

type limitFake struct {
	execFake
	err error
}

func (f *limitFake) Complete(ctx context.Context, req runner.CompletionRequest) (<-chan runner.Token, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.execFake.Complete(ctx, req)
}

func assignedExec(t *testing.T, role config.AgentConfig, rolesFake, pickFake runner.LLMRunner, built *[]config.AgentConfig, conn map[string]bool) (*executor, *Graph, *Node) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents["coder"] = role
	ex := &executor{
		root: t.TempDir(), cfg: cfg,
		connected: func() map[string]bool { return conn },
		newRunner: func(ac config.AgentConfig) (runner.LLMRunner, error) {
			*built = append(*built, ac)
			if ac.Runner == role.Runner && ac.Model == role.Model {
				return rolesFake, nil
			}
			return pickFake, nil
		},
	}
	n := &Node{ID: "T1", Title: "a", Description: "d", Kind: KindWork, Role: "coder",
		Assign: &AgentAssign{Runner: "dsh", Model: "deepseek-flash", Effort: "high", Source: SourceAuto}}
	return ex, &Graph{Nodes: []*Node{n}}, n
}

func modelEvents(evs []Event) (models []string, usage []string, fb string) {
	for _, e := range evs {
		switch e.Type {
		case EventModel:
			models = append(models, e.Model)
		case EventUsage:
			usage = append(usage, e.Runner+"/"+e.Model)
		case EventFallback:
			fb = e.Text
		}
	}
	return
}

func TestExecUsesAssignment(t *testing.T) {
	roleF := &execFake{text: "ROLE\n===SUMMARY===\nr"}
	pickF := &execFake{text: "PICK\n===SUMMARY===\np"}
	role := config.AgentConfig{Runner: "claude", Model: "claude-opus-5-5", Effort: "medium", BaseURL: "http://x", Mode: config.ModeText}
	var built []config.AgentConfig
	ex, g, n := assignedExec(t, role, roleF, pickF, &built, map[string]bool{"dsh": true})
	var evs []Event
	if _, err := ex.Run(context.Background(), g, n, collect(&evs)); err != nil {
		t.Fatal(err)
	}
	if len(built) != 1 || built[0].Runner != "dsh" || built[0].Model != "deepseek-flash" || built[0].Effort != "high" || built[0].BaseURL != "" || built[0].Mode != config.ModeText {
		t.Fatalf("built %+v", built)
	}
	if len(pickF.reqs) != 1 || len(roleF.reqs) != 0 || pickF.reqs[0].Model != "deepseek-flash" {
		t.Fatalf("the assigned runner must run: pick=%d role=%d", len(pickF.reqs), len(roleF.reqs))
	}
	models, usage, fb := modelEvents(evs)
	if models[0] != "deepseek-flash" || fb != "" {
		t.Fatalf("models %v fallback %q", models, fb)
	}
	if len(usage) == 0 || usage[len(usage)-1] != "dsh/deepseek-flash" {
		t.Fatalf("pricing must use the model that ran: %v", usage)
	}
}

func TestExecFallsBackWhenRunnerNotConnected(t *testing.T) {
	roleF := &execFake{text: "ROLE\n===SUMMARY===\nr"}
	pickF := &execFake{text: "PICK"}
	role := config.AgentConfig{Runner: "claude", Model: "claude-opus-5-5", Mode: config.ModeText}
	var built []config.AgentConfig
	ex, g, n := assignedExec(t, role, roleF, pickF, &built, map[string]bool{"dsh": false})
	var evs []Event
	if _, err := ex.Run(context.Background(), g, n, collect(&evs)); err != nil {
		t.Fatal(err)
	}
	if len(roleF.reqs) != 1 || len(pickF.reqs) != 0 {
		t.Fatalf("the role must run: role=%d pick=%d", len(roleF.reqs), len(pickF.reqs))
	}
	models, usage, fb := modelEvents(evs)
	if !strings.Contains(fb, "not connected") || !strings.Contains(fb, "coder") || models[0] != "claude-opus-5-5" || usage[len(usage)-1] != "claude/claude-opus-5-5" {
		t.Fatalf("fallback %q models %v usage %v", fb, models, usage)
	}
}

func TestExecFallsBackOnRateLimit(t *testing.T) {
	roleF := &execFake{text: "ROLE\n===SUMMARY===\nr"}
	pickF := &limitFake{err: &runner.RateLimitError{Provider: "deepseek"}}
	role := config.AgentConfig{Runner: "claude", Model: "claude-opus-5-5", Mode: config.ModeText}
	var built []config.AgentConfig
	ex, g, n := assignedExec(t, role, roleF, pickF, &built, map[string]bool{"dsh": true})
	var evs []Event
	if _, err := ex.Run(context.Background(), g, n, collect(&evs)); err != nil {
		t.Fatalf("a rate limit on the assigned agent must fall back: %v", err)
	}
	if len(roleF.reqs) != 1 {
		t.Fatalf("role ran %d times", len(roleF.reqs))
	}
	_, usage, fb := modelEvents(evs)
	if !strings.Contains(fb, "rate limited") || usage[len(usage)-1] != "claude/claude-opus-5-5" {
		t.Fatalf("fallback %q usage %v", fb, usage)
	}
	// The role's own rate limit is not retried.
	role2 := &limitFake{err: &runner.RateLimitError{Provider: "claude"}}
	built = nil
	ex, g, n = assignedExec(t, role, role2, &limitFake{err: errors.New("x")}, &built, map[string]bool{"dsh": false})
	if _, err := ex.Run(context.Background(), g, n, collect(&evs)); asRateLimit(err) == nil {
		t.Fatalf("the role's rate limit must surface: %v", err)
	}
}

func TestEngineStoresFallbackNote(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Kind: KindWork, Status: StatusTodo, Role: "coder", Assign: &AgentAssign{Runner: "dsh", Model: "m"}}}}
	fx := newFakeExec()
	fx.behavior["T1"] = nil
	e := NewEngine(g, &noteExec{fakeExec: fx}, 1, "")
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, e, 1)
	if got := e.Snapshot().Node("T1").Assign.FallbackNote; got != "used the role" {
		t.Fatalf("note = %q", got)
	}
}

type noteExec struct{ *fakeExec }

func (x *noteExec) Run(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error) {
	emit(Event{Type: EventFallback, Text: "used the role"})
	return x.fakeExec.Run(ctx, g, n, emit)
}
