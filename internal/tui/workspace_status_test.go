package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// statusHarness is a harness with two used nodes on two runners and prices.
func statusHarness(t *testing.T, w, hh int) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.size(w, hh)
	h.toGraph()
	h.m.cfg.Agents["coder"] = config.AgentConfig{Runner: "opencode"}
	h.m.cfg.Agents["qa"] = config.AgentConfig{Runner: "deepseek", Model: "deepseek-v4-flash"}
	h.m.cfg.Pricing = map[string]config.ModelPrice{"deepseek-*": {InputPerMTok: 1, OutputPerMTok: 2}}
	h.m.cfg.Workspace.BudgetUSD = 5
	t1, t3 := h.m.g.Node("T1"), h.m.g.Node("T3")
	t1.Role, t1.InputTokens, t1.OutputTokens = "coder", 40000, 4400
	t3.Role, t3.InputTokens, t3.OutputTokens, t3.CostUSD, t3.CostKnown = "qa", 10000, 2000, 0.38, true
	return h
}

func TestStatusBarOneLineAtWidths(t *testing.T) {
	for _, w := range []int{80, 120, 160} {
		h := statusHarness(t, w, 40)
		bar := h.m.statusBar()
		if strings.Contains(bar, "\n") {
			t.Fatalf("%d: more than one line", w)
		}
		if got := ansi.StringWidth(bar); got > w {
			t.Fatalf("%d: bar is %d wide: %q", w, got, ansi.Strip(bar))
		}
		plain := ansi.Strip(bar)
		if !strings.Contains(plain, "≥ $0.38 / $5.00") {
			t.Errorf("%d: no cost segment: %q", w, plain)
		}
		if w >= 120 && !strings.Contains(plain, "⬡ opencode 44.5k") {
			t.Errorf("%d: no runner tokens: %q", w, plain)
		}
		// The whole view stays within the terminal.
		lines := strings.Split(h.view(), "\n")
		if len(lines) > 40 {
			t.Errorf("%d: %d rows", w, len(lines))
		}
		for i, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Errorf("%d: row %d overflows", w, i)
			}
		}
	}
}

func TestStatusBarBranchAndPR(t *testing.T) {
	h := statusHarness(t, 160, 40)
	h.m.iso = isolatorOf(t, h)
	h.m.sess.update(func(s *workspace.Session) { s.PR = &workspace.PRState{Number: 12} })
	h.m.pr.passedSHA = "abc"
	plain := ansi.Strip(h.m.statusBar())
	if !strings.Contains(plain, "⎇ decsr/") || !strings.Contains(plain, "PR #12 ● checks passing") {
		t.Fatalf("bar %q", plain)
	}
}

func TestStatusBarTokensOnlyAndCostColors(t *testing.T) {
	h := newWSHarness(t)
	h.size(120, 40)
	h.toGraph()
	h.m.cfg.Agents["coder"] = config.AgentConfig{Runner: "claude"}
	h.m.g.Node("T1").Role, h.m.g.Node("T1").InputTokens = "coder", 1500
	if got := ansi.Strip(h.m.statusBar()); !strings.Contains(got, "tokens only") || strings.Contains(got, "$") {
		t.Fatalf("bar %q", got)
	}
	// Without usage and without budget: no cost at all.
	h2 := newWSHarness(t)
	h2.size(120, 40)
	if got := ansi.Strip(h2.m.statusBar()); strings.Contains(got, "$") {
		t.Fatalf("bar %q", got)
	}
	// Known cost without budget is a bare amount; no "≥" when all is priced.
	h.m.cfg.Pricing = map[string]config.ModelPrice{"*": {InputPerMTok: 2}}
	h.m.g.Node("T1").CostUSD, h.m.g.Node("T1").CostKnown = 0.003, true
	if got := ansi.Strip(h.m.statusBar()); !strings.Contains(got, "$0.00") || strings.Contains(got, "≥") || strings.Contains(got, "/") {
		t.Fatalf("bar %q", got)
	}
}

func TestStatusBarMovedTokensOutOfHeader(t *testing.T) {
	h := statusHarness(t, 120, 40)
	if strings.Contains(h.m.headerLine(), "tok") {
		t.Fatal("the header still shows tokens")
	}
}

func TestPromptChips(t *testing.T) {
	h := statusHarness(t, 120, 40)
	h.m.cfg.Agents["pm"] = config.AgentConfig{Runner: "deepseek", Model: "deepseek-v4-flash"}
	chips := ansi.Strip(h.m.promptChips())
	for _, want := range []string{"shared", "◆ deepseek:deepseek-v4-flash", "$0.38/$5"} {
		if !strings.Contains(chips, want) {
			t.Errorf("chips %q lack %q", chips, want)
		}
	}
	// The chips sit inside the box and the typing area keeps its rows.
	box := h.m.promptBox()
	if len(box) != h.m.promptRows()+3 {
		t.Fatalf("box has %d rows, want %d", len(box), h.m.promptRows()+3)
	}
	// A small terminal has one typing row and no chips.
	h.size(100, 22)
	if h.m.promptChips() != "" || len(h.m.promptBox()) != 3 {
		t.Fatalf("chips on a short terminal: %q", h.m.promptChips())
	}
	// No budget: no budget chip.
	h.size(120, 40)
	h.m.cfg.Workspace.BudgetUSD = 0
	if strings.Contains(ansi.Strip(h.m.promptChips()), "$") {
		t.Fatal("budget chip without a budget")
	}
}

func TestPromptChipsWorktree(t *testing.T) {
	h := statusHarness(t, 120, 40)
	h.m.iso = isolatorOf(t, h)
	if !strings.Contains(ansi.Strip(h.m.promptChips()), "⚠ worktree") {
		t.Fatal("no worktree chip")
	}
}

func TestParseBudget(t *testing.T) {
	ok := map[string]float64{"10": 10, "$7.5": 7.5, "0": 0, "off": 0, "OFF": 0, "0.25": 0.25}
	for in, want := range ok {
		got, err := parseBudget(in)
		if err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"abc", "-3", "NaN", "inf", "1e12", "$"} {
		if _, err := parseBudget(in); err == nil {
			t.Errorf("%q must be rejected", in)
		}
	}
}

func TestBudgetCommandSetsOverrideAndLists(t *testing.T) {
	h := statusHarness(t, 120, 40)
	h.typeCmd("/budget 12")
	if got := h.m.sess.snapshot().BudgetUSD; got != 12 {
		t.Fatalf("override %v", got)
	}
	if limit, _ := budgetOf(h.m.cfg, h.m.sess.snapshot()); limit != 12 {
		t.Fatalf("limit %v", limit)
	}
	h.typeCmd("/budget nonsense")
	h.want("usage: /budget")
	h.typeCmd("/budget")
	h.want("this session")
	h.want("By runner")
	h.want("opencode")
	h.want("No price for")
	h.want("Only known cost counts")
}

func TestBudgetOffRemovesOverride(t *testing.T) {
	h := statusHarness(t, 120, 40)
	h.m.sess.update(func(s *workspace.Session) { s.BudgetUSD = 9 })
	h.typeCmd("/budget off")
	if h.m.sess.snapshot().BudgetUSD != 0 {
		t.Fatal("override kept")
	}
}

func TestCardHeaderShowsRunnerWhenModelIsDefault(t *testing.T) {
	h := statusHarness(t, 140, 40)
	h.m.sel = "T1"
	n := h.m.g.Node("T1")
	n.Status = workspace.StatusDone
	n.StartedAt = h.m.now()
	out := ansi.Strip(strings.Join(h.m.feedLayoutLines(), "\n"))
	if !strings.Contains(out, "⬡ opencode · ") {
		t.Fatalf("card header lacks the runner:\n%s", out)
	}
	models, runners := h.m.roleLooks()
	if models["coder"] != "opencode" || runners["coder"] != "opencode" {
		t.Fatalf("looks %v %v", models, runners)
	}
	// The lead's model is the effective one for a runner without its own.
	h.m.cfg.Agents["pm"] = config.AgentConfig{Runner: "opencode", Model: "deepseek/v4"}
	models, _ = h.m.roleLooks()
	if models["coder"] != "v4" {
		t.Fatalf("model %q", models["coder"])
	}
}

func (m workspaceModel) feedLayoutLines() []string {
	lines, _ := m.feedLayout(m.mainWidth())
	return lines
}

// isolatorOf turns the harness project into a git repository with one commit
// and returns the isolator of its session.
func isolatorOf(t *testing.T, h *wsHarness) *workspace.Isolator {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	prGit(t, h.root, "init", "-q")
	prGit(t, h.root, "config", "user.name", "tester")
	prGit(t, h.root, "config", "user.email", "t@example.com")
	if err := os.WriteFile(filepath.Join(h.root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prGit(t, h.root, "add", "a.go")
	prGit(t, h.root, "commit", "-q", "-m", "init")
	iso := workspace.NewIsolator(h.root, h.m.sess.snapshot().ID, "worktree")
	if iso == nil {
		t.Fatal("no isolator")
	}
	return iso
}

func TestBudgetEventSaysAndNotifies(t *testing.T) {
	h := statusHarness(t, 120, 40)
	var got []notify.Note
	h.m.deps.notify = func(_ config.NotifyConfig, n notify.Note) { got = append(got, n) }
	next, _ := h.m.onEvent(wsEventMsg{eng: h.m.eng, ev: workspace.Event{Type: workspace.EventBudgetWarn, Text: "Budget warning: $4.00 of $5.00 spent (80%)"}})
	h.m = next.(workspaceModel)
	h.want("Budget warning: $4.00 of $5.00")
	if len(got) != 1 || got[0].Level != notify.NeedsYou {
		t.Fatalf("notes %+v", got)
	}
}

func TestWiredEngineStopsAtSessionBudget(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.sess.update(func(s *workspace.Session) { s.BudgetUSD = 1 })
	if err := h.m.eng.Replace(func(g *workspace.Graph) {
		g.Nodes[0].Status, g.Nodes[0].CostUSD, g.Nodes[0].CostKnown = workspace.StatusDone, 1.5, true
		g.Nodes[1].Role = "coder"
	}); err != nil {
		t.Fatal(err)
	}
	err := h.m.eng.Run(context.Background(), "T2")
	if err == nil || !strings.Contains(err.Error(), "Budget reached: $1.50 of $1.00") {
		t.Fatalf("err %v", err)
	}
	h.m.sess.update(func(s *workspace.Session) { s.BudgetUSD = 0 })
	if err := h.m.eng.Run(context.Background(), "T2"); err != nil {
		t.Fatalf("no budget set: %v", err)
	}
}
