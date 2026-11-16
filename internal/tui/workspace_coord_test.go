package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

type coordRig struct {
	*wsHarness
	notes *autoNotes
	mu    sync.Mutex
	ins   []workspace.ReviewInput
	res   workspace.ReviewResult
	err   error
}

func (r *coordRig) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ins)
}

// coordHarness is a Workspace with the coordinator on, at the given autonomy
// level, with a fake lead. The level is applied after the split so that the
// nodes do not start by themselves.
func coordHarness(t *testing.T, level int) *coordRig {
	t.Helper()
	h, notes := autoHarness(t, 0)
	r := &coordRig{wsHarness: h, notes: notes}
	h.m.cfg.Workspace.Coordinator = true
	h.m.deps.review = func(_ context.Context, in workspace.ReviewInput) (workspace.ReviewResult, runner.TokenUsage, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.ins = append(r.ins, in)
		return r.res, runner.TokenUsage{InputTokens: 10, OutputTokens: 5}, r.err
	}
	h.graph = wsSmallGraph
	h.toGraph()
	h.m.cfg.Workspace.Autonomy = level
	return r
}

// settle marks T1 done and T2 failed, adds a todo T3 and delivers one event,
// the way a batch ends. It returns the command of the model.
func (r *coordRig) settle(ev workspace.EventType, id string) tea.Cmd {
	r.t.Helper()
	if err := r.eng.Replace(func(g *workspace.Graph) {
		g.Node("T1").Status, g.Node("T1").Role, g.Node("T1").Summary = workspace.StatusDone, "coder", "indexed"
		g.Node("T2").Status, g.Node("T2").Role, g.Node("T2").Error = workspace.StatusFailed, "qa", "tests broke"
		if g.Node("T3") == nil {
			g.AddWork("Extra", "an extra node", "coder", 3)
		}
	}); err != nil {
		r.t.Fatal(err)
	}
	text := ""
	if ev == workspace.EventFailed {
		text = "tests broke"
	}
	return r.send(wsEventMsg{eng: r.eng, ev: workspace.Event{NodeID: id, Type: ev, Text: text}})
}

// deliver hands the lead's answer to the model.
func (r *coordRig) deliver(res workspace.ReviewResult) tea.Cmd {
	r.t.Helper()
	if !r.m.reviewing {
		r.t.Fatal("no review is running")
	}
	return r.send(wsReviewDoneMsg{gen: r.m.reviewGen, round: r.eng.ReviewRounds(), res: res,
		usage: runner.TokenUsage{InputTokens: 10, OutputTokens: 5}})
}

var coordProposals = []workspace.Proposal{
	{Action: "add", Title: "Write the docs", Description: "docs", Role: "coder", DependsOn: []string{"T1"}},
	{Action: "change", ID: "T2", Description: "fix the failing test", Role: "qa"},
	{Action: "cancel", ID: "T3", Reason: "not needed"},
	{Action: "change", ID: "T1", Title: "nope"},
}

func (r *coordRig) reviews() []workspace.Review { return r.m.sess.snapshot().Reviews }

func TestCoordTriggersOnlyAtBatchEnd(t *testing.T) {
	r := coordHarness(t, 0)
	_ = r.eng.Assign("T1", "coder")
	_ = r.eng.Assign("T2", "qa")
	r.eng.RunAll(r.m.runCtx)
	r.exec.release("T1", nil)
	r.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusDone })
	if r.m.reviewing || r.eng.ReviewRounds() != 0 {
		t.Fatal("a review started while T2 and CHECK were still to run")
	}
	r.exec.release("T2", nil)
	r.pump(func(m workspaceModel) bool { return m.g.Node("T2").Status == workspace.StatusDone })
	if r.m.reviewing {
		t.Fatal("a review started while CHECK was about to run")
	}
	r.exec.release("CHECK", nil)
	var cmds []tea.Cmd
	deadline := time.After(3 * time.Second)
	for !r.m.reviewing {
		select {
		case ev := <-r.eng.Events():
			cmds = append(cmds, r.send(wsEventMsg{eng: r.eng, ev: ev}))
		case <-deadline:
			t.Fatal("no review after the batch")
		}
	}
	if r.eng.ReviewRounds() != 1 {
		t.Errorf("rounds = %d", r.eng.ReviewRounds())
	}
	msg := runForReview(t, cmds...)
	if msg.err != nil || r.calls() != 1 {
		t.Fatalf("calls %d err %v", r.calls(), msg.err)
	}
	in := r.ins[0]
	if in.Graph == nil || in.Graph.Node("T1") == nil || in.Task == "" || in.Detail == nil || len(in.Roles) == 0 {
		t.Errorf("review input = %+v", in)
	}
	r.send(msg)
	// The answer is empty: one card, nothing pending, and no second review.
	if rs := r.reviews(); len(rs) != 1 || rs[0].State != workspace.ReviewEmpty || r.m.reviewing {
		t.Fatalf("reviews = %+v", rs)
	}
	if r.m.reviewing || r.m.reviewDirty || r.eng.ReviewRounds() != 1 {
		t.Error("a second review is due with no new batch")
	}
}

// runForReview runs the commands and returns the review result they produce.
func runForReview(t *testing.T, cmds ...tea.Cmd) wsReviewDoneMsg {
	t.Helper()
	out := make(chan wsReviewDoneMsg, 1)
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch msg := c().(type) {
			case wsReviewDoneMsg:
				select {
				case out <- msg:
				default:
				}
			case tea.BatchMsg:
				for _, sub := range msg {
					run(sub)
				}
			}
		}()
	}
	for _, c := range cmds {
		run(c)
	}
	select {
	case msg := <-out:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("the review command produced no result")
	}
	return wsReviewDoneMsg{}
}

func TestCoordOffByDefaultAndSessionOverride(t *testing.T) {
	r := coordHarness(t, 0)
	r.m.cfg.Workspace.Coordinator = false
	r.settle(workspace.EventFailed, "T2")
	if r.m.reviewing {
		t.Fatal("a review started with the coordinator off")
	}
	if strings.Contains(r.m.promptChips(), "coord") {
		t.Error("the coord chip shows while off")
	}
	r.m.setFocus(wsFocusPrompt)
	r.typeCmd("/coordinator")
	r.want("Coordinator is off (set by workspace.coordinator)")
	r.typeCmd("/coordinator on")
	r.want("Coordinator of this session is on.")
	if v := r.m.sess.snapshot().Coordinator; v == nil || !*v || !strings.Contains(r.m.promptChips(), "coord") {
		t.Errorf("session override = %v, chips %q", v, r.m.promptChips())
	}
	r.typeCmd("/coordinator")
	r.want("this session, /coordinator")
	r.settle(workspace.EventFailed, "T2")
	if !r.m.reviewing {
		t.Fatal("the session override did not turn the coordinator on")
	}
	r.m.stopReview()
	r.m.cfg.Workspace.Coordinator = true
	r.typeCmd("/coordinator off")
	r.settle(workspace.EventFailed, "T2")
	if r.m.reviewing {
		t.Fatal("/coordinator off did not override the config")
	}
	r.typeCmd("/coordinator maybe")
	r.want("usage: /coordinator [on|off]")
}

func TestCoordSkips(t *testing.T) {
	t.Run("paused", func(t *testing.T) {
		r := coordHarness(t, 2)
		r.m.autoPaused = true
		r.settle(workspace.EventFailed, "T2")
		if r.m.reviewing || r.eng.ReviewRounds() != 0 {
			t.Error("a review started while paused")
		}
	})
	t.Run("budget", func(t *testing.T) {
		r := coordHarness(t, 0)
		r.m.cfg.Workspace.BudgetUSD = 1
		if err := r.eng.Replace(func(g *workspace.Graph) {
			g.Node("T1").CostUSD, g.Node("T1").CostKnown, g.Node("T1").InputTokens = 2, true, 1000
		}); err != nil {
			t.Fatal(err)
		}
		r.settle(workspace.EventFailed, "T2")
		if r.m.reviewing {
			t.Error("a review started with the budget reached")
		}
		r.wantAuto("budget reached, review skipped")
	})
	t.Run("stopped", func(t *testing.T) {
		r := coordHarness(t, 0)
		if err := r.eng.Replace(func(g *workspace.Graph) { g.Node("T1").Status, g.Node("T1").Error = workspace.StatusFailed, "stopped" }); err != nil {
			t.Fatal(err)
		}
		r.send(wsEventMsg{eng: r.eng, ev: workspace.Event{NodeID: "T1", Type: workspace.EventFailed, Text: "stopped"}})
		if r.m.reviewing || r.m.reviewDirty {
			t.Error("a node stopped by the user counts as a finished batch")
		}
	})
	t.Run("no lead", func(t *testing.T) {
		r := coordHarness(t, 0)
		r.m.deps.review = nil
		r.settle(workspace.EventFailed, "T2")
		if r.m.reviewing {
			t.Error("reviewing without a lead")
		}
	})
	t.Run("answer pending", func(t *testing.T) {
		r := coordHarness(t, 0)
		r.settle(workspace.EventFailed, "T2")
		r.deliver(workspace.ReviewResult{Proposals: coordProposals})
		if r.m.pendingReview() < 0 {
			t.Fatal("no pending review")
		}
		r.send(wsEventMsg{eng: r.eng, ev: workspace.Event{NodeID: "T2", Type: workspace.EventFailed, Text: "again"}})
		if r.m.reviewing {
			t.Error("a new review started over a pending one")
		}
	})
}

func TestCoordRoundLimitAndManualReview(t *testing.T) {
	r := coordHarness(t, 0)
	for i := 0; i < workspace.MaxReviewRounds; i++ {
		r.eng.StartReview()
	}
	r.settle(workspace.EventFailed, "T2")
	if r.m.reviewing || r.eng.ReviewRounds() != 5 {
		t.Fatal("a sixth review started by itself")
	}
	if !r.notes.has(notify.NeedsYou, "5 lead reviews done for this task — /review to continue") {
		t.Errorf("no NeedsYou note: %+v", r.notes.notes)
	}
	r.want("5 lead reviews done for this task — /review to continue")
	// /review runs one anyway and counts it.
	r.m.setFocus(wsFocusPrompt)
	r.m.input.SetValue("/review")
	cmd := r.key("enter")
	if !r.m.reviewing || r.eng.ReviewRounds() != 6 {
		t.Fatalf("/review did not start (reviewing %v, rounds %d)", r.m.reviewing, r.eng.ReviewRounds())
	}
	msg := runForReview(t, cmd)
	if r.calls() != 1 {
		t.Errorf("calls = %d", r.calls())
	}
	r.send(msg)
	r.settle(workspace.EventFailed, "T2")
	if r.m.reviewing {
		t.Error("the bypass lasted longer than one review")
	}
	// Manual review is refused while one waits, runs, or the budget is reached.
	r.typeCmd("/review y")
	r.want("No lead review is waiting")
	r.typeCmd("/review x")
	r.want("usage: /review [y|n]")
}

func TestCoordRoundsResetOnNewTask(t *testing.T) {
	r := coordHarness(t, 0)
	r.settle(workspace.EventFailed, "T2")
	r.deliver(workspace.ReviewResult{})
	if r.eng.ReviewRounds() != 1 {
		t.Fatalf("rounds = %d", r.eng.ReviewRounds())
	}
	r.graph = wsSmallGraph
	r.m.setFocus(wsFocusPrompt)
	r.sendTask("another task")
	if got := r.eng.ReviewRounds(); got != 0 {
		t.Errorf("rounds after a new task = %d", got)
	}
}

func TestCoordLevel1CardAndApply(t *testing.T) {
	r := coordHarness(t, 0)
	r.m.showGraph = false
	r.settle(workspace.EventFailed, "T2")
	r.res = workspace.ReviewResult{Note: "T2 needs another try", Proposals: coordProposals}
	r.deliver(r.res)
	rs := r.reviews()
	if len(rs) != 1 || rs[0].State != workspace.ReviewPending || len(rs[0].Applied) != 3 || len(rs[0].Skipped) != 1 {
		t.Fatalf("review = %+v", rs)
	}
	if r.m.g.Node("T3") == nil {
		t.Fatal("a proposal changed the graph before y")
	}
	r.want("Lead review 1 · 3 changes waiting for you")
	r.want("T2 needs another try")
	r.want("+ add T4 Write the docs [coder] after T1")
	r.want("~ change T2")
	r.want("× cancel T3 — not needed")
	r.want("skipped change T1 — T1 is done and cannot be changed")
	r.want("y apply · n reject")
	r.want("y apply review")
	if !r.notes.has(notify.NeedsYou, "Lead review: 3 changes proposed") {
		t.Errorf("no NeedsYou note: %+v", r.notes.notes)
	}
	if s := r.m.sess.snapshot(); s.In != 100+10 || s.Out != 50+5 {
		t.Errorf("lead usage = %d/%d, want the review's tokens added", s.In, s.Out)
	}

	r.key("y")
	g := r.m.g
	if g.Node("T4") == nil || g.Node("T3") != nil || g.Node("T2").Description != "fix the failing test" || g.Node("T2").Status != workspace.StatusTodo {
		t.Fatalf("graph after y: %+v", g.Nodes)
	}
	if !strings.Contains(r.m.sess.snapshot().Summary, "- T3 cancelled by the lead: not needed") {
		t.Errorf("summary = %q", r.m.sess.snapshot().Summary)
	}
	if rs := r.reviews(); rs[0].State != workspace.ReviewApplied || len(rs[0].Applied) != 3 {
		t.Errorf("review after y = %+v", rs[0])
	}
	check := g.Node("CHECK")
	if !strings.Contains(strings.Join(check.DependsOn, ","), "T4") || strings.Contains(strings.Join(check.DependsOn, ","), "T3") {
		t.Errorf("CHECK deps = %v", check.DependsOn)
	}
	if r.m.pendingReview() >= 0 || r.m.running() != 0 {
		t.Error("level 1 must not run anything by itself")
	}
	r.want("Lead review 1 · 3 changes applied ›")
	r.wantNot("× cancel T3")
	// Space on the folded card shows it again.
	r.m.sel = reviewKey(r.reviews()[0])
	r.key("space")
	r.want("× cancel T3 — not needed")
}

func TestCoordLevel1Reject(t *testing.T) {
	r := coordHarness(t, 0)
	r.m.showGraph = false
	r.settle(workspace.EventFailed, "T2")
	r.deliver(workspace.ReviewResult{Proposals: coordProposals})
	r.key("n")
	if rs := r.reviews(); rs[0].State != workspace.ReviewRejected || r.m.g.Node("T3") == nil || r.m.g.Node("T4") != nil {
		t.Fatalf("review %+v", rs[0])
	}
	r.want("Lead review 1 · rejected ›")
	r.key("y")
	if r.m.g.Node("T4") != nil {
		t.Error("y applied a rejected review")
	}
}

func TestCoordYNKeysOnlyWhilePending(t *testing.T) {
	r := coordHarness(t, 0)
	r.key("y", "n")
	if len(r.reviews()) != 0 {
		t.Error("y or n with nothing pending made a review")
	}
	r.settle(workspace.EventFailed, "T2")
	r.deliver(workspace.ReviewResult{Proposals: coordProposals})
	// From the prompt the keys are text; /review y answers.
	r.m.setFocus(wsFocusPrompt)
	r.key("y")
	if r.m.input.Value() != "y" || r.m.pendingReview() < 0 {
		t.Fatal("y in the prompt must stay text")
	}
	r.m.input.Reset()
	r.typeCmd("/review y")
	if r.m.pendingReview() >= 0 || r.m.g.Node("T4") == nil {
		t.Error("/review y did not apply")
	}
}

func TestCoordStaleProposalsAreSkippedOnApply(t *testing.T) {
	r := coordHarness(t, 0)
	r.settle(workspace.EventFailed, "T2")
	r.deliver(workspace.ReviewResult{Proposals: []workspace.Proposal{{Action: "cancel", ID: "T3"}}})
	if err := r.eng.Replace(func(g *workspace.Graph) { g.Node("T3").Status = workspace.StatusDone }); err != nil {
		t.Fatal(err)
	}
	r.m.setFocus(wsFocusGraph)
	r.key("y")
	if r.m.g.Node("T3") == nil || r.reviews()[0].State != workspace.ReviewEmpty {
		t.Errorf("a stale cancel was applied: %+v", r.reviews()[0])
	}
	r.want("nothing could be applied any more")
}

func TestCoordLevel2AppliesWithAutoLines(t *testing.T) {
	r := coordHarness(t, 2)
	r.settle(workspace.EventFailed, "T2")
	if !r.m.reviewing {
		t.Fatal("no review at level 2")
	}
	r.deliver(workspace.ReviewResult{Note: "go on", Proposals: coordProposals})
	r.wantAuto("lead: added T4 Write the docs")
	r.wantAuto("lead: changed T2 (description, role)")
	r.wantAuto("lead: cancelled T3: not needed")
	if n := len(r.autoLines()); n < 3 {
		t.Fatalf("auto lines = %q", r.autoLines())
	}
	g := r.m.g
	if g.Node("T3") != nil || g.Node("T4") == nil {
		t.Fatalf("not applied: %+v", g.Nodes)
	}
	if rs := r.reviews(); len(rs) != 1 || rs[0].State != workspace.ReviewApplied || len(rs[0].Applied) != 3 || len(rs[0].Skipped) != 1 {
		t.Fatalf("review = %+v", rs)
	}
	if r.m.pendingReview() >= 0 {
		t.Error("level 2 left a card waiting")
	}
	// autoAfterGraph ran: the new work started.
	r.wantAuto("started the run")
	if r.m.running() == 0 {
		t.Error("the new nodes did not start")
	}
	if !strings.Contains(r.m.sess.snapshot().Summary, "- T3 cancelled by the lead: not needed") {
		t.Errorf("summary = %q", r.m.sess.snapshot().Summary)
	}
}

func TestCoordNoProposalsAndGarbage(t *testing.T) {
	r := coordHarness(t, 0)
	r.m.showGraph = false
	r.settle(workspace.EventFailed, "T2")
	r.deliver(workspace.ParseReview("I refuse to answer in JSON."))
	rs := r.reviews()
	if len(rs) != 1 || rs[0].State != workspace.ReviewEmpty || len(rs[0].Proposals) != 0 {
		t.Fatalf("review = %+v", rs)
	}
	if strings.Contains(r.m.View(), "y apply") {
		t.Error("an empty review asks for y/n")
	}
	r.m.sel = reviewKey(rs[0])
	r.key("space")
	r.want("not valid JSON")
	if r.notes.has(notify.NeedsYou, "Lead review") {
		t.Error("an empty review notified")
	}
}

func TestCoordReviewErrorAndCancel(t *testing.T) {
	r := coordHarness(t, 0)
	r.settle(workspace.EventFailed, "T2")
	r.send(wsReviewDoneMsg{gen: r.m.reviewGen, err: errors.New("lead is down")})
	if r.m.reviewing || len(r.reviews()) != 0 {
		t.Error("an error left a review")
	}
	r.want("lead review failed: lead is down")

	if r.m.reviewDirty {
		t.Fatal("a failed review must not start again by itself")
	}
	r.m.setFocus(wsFocusPrompt)
	r.typeCmd("/review")
	if !r.m.reviewing {
		t.Fatal("no review")
	}
	gen := r.m.reviewGen
	r.m.input.SetValue("a task")
	r.key("enter")
	if r.m.splitting {
		t.Fatal("a task was sent during a review")
	}
	r.m.input.Reset()
	r.key("esc")
	if r.m.reviewing || r.m.reviewGen == gen {
		t.Fatal("esc did not cancel the review")
	}
	r.send(wsReviewDoneMsg{gen: gen, res: workspace.ReviewResult{Proposals: coordProposals}})
	if len(r.reviews()) != 0 {
		t.Error("the answer of a cancelled review was used")
	}
}

func TestCoordHistoryCapAndPersistence(t *testing.T) {
	r := coordHarness(t, 0)
	for i := 1; i <= workspace.MaxReviews+5; i++ {
		r.m.addReview(workspace.Review{Round: i, At: time.Unix(int64(1000+i), 0), State: workspace.ReviewEmpty})
	}
	rs := r.reviews()
	if len(rs) != workspace.MaxReviews || rs[0].Round != 6 || rs[len(rs)-1].Round != workspace.MaxReviews+5 {
		t.Fatalf("kept %d reviews from round %d", len(rs), rs[0].Round)
	}
	r.m.sess.update(func(s *workspace.Session) { s.Reviews = nil })
	r.settle(workspace.EventFailed, "T2")
	r.deliver(workspace.ReviewResult{Note: "n", Proposals: coordProposals})
	on := true
	r.m.sess.update(func(s *workspace.Session) { s.Coordinator = &on })
	r.m.saveSession()
	got, err := workspace.LoadSession(r.root, r.m.sess.snapshot().ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Reviews) != 1 || got.Reviews[0].State != workspace.ReviewPending || len(got.Reviews[0].Applied) != 3 ||
		got.Reviews[0].Note != "n" || got.Coordinator == nil || !*got.Coordinator || got.Graph == nil || got.Graph.ReviewRounds != 1 {
		t.Errorf("reloaded: %+v coordinator %v graph %+v", got.Reviews, got.Coordinator, got.Graph)
	}
}

func TestCoordRenderingFitsWidth(t *testing.T) {
	long := strings.Repeat("a very long reason ", 12)
	for _, w := range []int{80, 120} {
		r := coordHarness(t, 0)
		r.m.showGraph = false
		r.size(w, 44)
		r.settle(workspace.EventFailed, "T2")
		r.deliver(workspace.ReviewResult{Note: long, Proposals: []workspace.Proposal{
			{Action: "add", Title: "Title " + long, Role: "coder", DependsOn: []string{"T1"}},
			{Action: "change", ID: "T2", Title: "x " + long, DependsOn: []string{"T1"}},
			{Action: "cancel", ID: "T3", Reason: long},
			{Action: "change", ID: "T1", Title: "nope"},
			{Action: "frobnicate", ID: "T9"},
		}})
		check := func(stage string) {
			for i, l := range strings.Split(r.m.View(), "\n") {
				if ansi.StringWidth(l) > w {
					t.Fatalf("w=%d %s: line %d is %d cells", w, stage, i, ansi.StringWidth(l))
				}
			}
		}
		check("pending")
		r.want("Lead review 1")
		r.key("y")
		check("folded")
		r.m.sel = reviewKey(r.reviews()[0])
		r.key("space")
		check("expanded")
		r.m.showGraph = true
		check("graph view")
	}
}

func TestRemovedCostCountsInSpendAndBudget(t *testing.T) {
	r := coordHarness(t, 0)
	r.m.cfg.Workspace.BudgetUSD = 1
	if err := r.eng.Replace(func(g *workspace.Graph) {
		g.Node("T1").CostUSD, g.Node("T1").CostKnown, g.Node("T1").InputTokens = 2, true, 1000
		g.Node("T1").Status = workspace.StatusDone
		g.Node("T2").DependsOn = nil
	}); err != nil {
		t.Fatal(err)
	}
	r.m.g = r.eng.Snapshot()
	if !r.m.budgetReached() {
		t.Fatal("budget not reached before compact")
	}
	r.m.setFocus(wsFocusPrompt)
	r.typeCmd("/compact")
	if r.m.g.Node("T1") != nil {
		t.Fatal("T1 was not folded")
	}
	if r.m.use().spent < 2 || !r.m.budgetReached() {
		t.Errorf("spent after compact = %v", r.m.use().spent)
	}
}
