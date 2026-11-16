package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// autoRun is a Workspace opened as the automation "nightly" at the given
// level, with a recording notifier and a fake "did anything change" answer.
func autoRun(t *testing.T, level int, merged bool) (*wsHarness, *autoNotes) {
	t.Helper()
	h, notes := autoHarness(t, 0)
	h.m.initAutomation(AutomationOptions{Name: "nightly", Timeout: time.Hour, Task: "do it", Autonomy: level, BudgetUSD: 5})
	h.m.auto.merged = func(string, string) bool { return merged }
	h.graph = wsSmallGraph
	return h, notes
}

// setStatuses stops whatever runs and sets the status of every node, so the model
// can be judged without engine timing.
func (h *wsHarness) setStatuses(statuses map[string]workspace.Status) {
	h.t.Helper()
	h.eng.StopAll()
	deadline := time.Now().Add(3 * time.Second)
	for h.eng.Running() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := h.eng.Replace(func(g *workspace.Graph) {
		for _, n := range g.Nodes {
			if st, ok := statuses[n.ID]; ok {
				n.Status = st
			}
		}
	}); err != nil {
		h.t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	if h.m.auto != nil && h.m.auto.opts.Autonomy == autoUnattended {
		h.m.iso = new(workspace.Isolator) // level 3 needs worktree isolation
	}
}

func (h *wsHarness) verdict() (string, notify.Level, bool) {
	res, lvl, _, done := h.m.autoVerdict()
	return res, lvl, done
}

var allDone = map[string]workspace.Status{"T1": workspace.StatusDone, "T2": workspace.StatusDone, "CHECK": workspace.StatusDone}

func TestAutomationInitSetsLevelAndBudget(t *testing.T) {
	h, _ := autoRun(t, 3, true)
	s := h.m.sess.snapshot()
	if s.Autonomy != 3 || s.BudgetUSD != 5 {
		t.Errorf("session autonomy %d budget %v", s.Autonomy, s.BudgetUSD)
	}
	if h.m.requestedAutonomy() != 3 {
		t.Errorf("requested autonomy %d", h.m.requestedAutonomy())
	}
	plain := newWSHarness(t)
	if plain.m.auto != nil || plain.m.autoStartCmd() != nil {
		t.Error("a normal Workspace has automation state")
	}
	var none workspaceModel
	none.initAutomation(AutomationOptions{})
	if none.auto != nil {
		t.Error("an empty name started an automation")
	}
}

func TestAutomationSendsTheTaskAndSetsTheTimeout(t *testing.T) {
	h, _ := autoRun(t, 2, true)
	cmd := h.m.autoStartCmd()
	if cmd == nil {
		t.Fatal("no start command")
	}
	h.send(wsAutoTaskMsg{})
	if !h.m.splitting {
		t.Fatal("the task did not start a split")
	}
	h.finishSplit()
	if len(h.tasks) != 1 || h.tasks[0] != "do it" || !h.m.hasNodes() {
		t.Errorf("tasks %q", h.tasks)
	}
}

func TestAutomationSplitFailureEndsTheRun(t *testing.T) {
	h, notes := autoRun(t, 2, true)
	h.splitErr = errors.New("lead unreachable")
	h.send(wsAutoTaskMsg{})
	h.finishSplit()
	a := h.m.auto
	if !a.finished || a.result.Result != "failed: lead unreachable" || !h.m.quitting {
		t.Errorf("result %+v quitting %v", a.result, h.m.quitting)
	}
	if !notes.has(notify.Failed, "automation nightly failed: lead unreachable") {
		t.Errorf("notes %+v", notes.notes)
	}
}

func TestAutomationIssueFailureEndsTheRun(t *testing.T) {
	h, _ := autoRun(t, 2, true)
	h.send(wsIssueMsg{session: h.m.sess.snapshot().ID, err: errors.New("gh: no such issue")})
	if !h.m.auto.finished || !strings.HasPrefix(h.m.auto.result.Result, "failed: gh: no such issue") {
		t.Errorf("result %+v", h.m.auto.result)
	}
}

func TestAutomationTimeout(t *testing.T) {
	h, notes := autoRun(t, 2, true)
	h.toGraph() // T1 is running
	cmd := h.send(wsAutoTimeoutMsg{})
	a := h.m.auto
	if !a.finished || a.result.Result != "timed out" || !a.result.Finished || !h.m.quitting {
		t.Errorf("result %+v", a.result)
	}
	if !notes.has(notify.Failed, "automation nightly timed out") {
		t.Errorf("notes %+v", notes.notes)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("the timeout did not quit")
	}
	h.send(wsAutoTimeoutMsg{}) // a second timeout changes nothing
}

func TestAutomationLevel2PassEndsTheRun(t *testing.T) {
	h, notes := autoRun(t, 2, true)
	h.toGraph()
	h.finish(nil, "T1", "T2")
	if h.m.auto.finished {
		t.Fatal("finished before Build & test")
	}
	h.finish(nil, "CHECK")
	a := h.m.auto
	if !a.finished || a.result.Result != "build & test passed" {
		t.Errorf("result %+v", a.result)
	}
	if !notes.has(notify.Done, "automation nightly: Build & test passed") || !h.m.quitting {
		t.Errorf("notes %+v quitting %v", notes.notes, h.m.quitting)
	}
}

func TestAutomationLevel2NothingChanged(t *testing.T) {
	h, notes := autoRun(t, 2, false)
	h.toGraph()
	h.finish(nil, "T1", "T2", "CHECK")
	if got := h.m.auto.result.Result; got != "nothing to do" {
		t.Errorf("result %q", got)
	}
	if !notes.has(notify.Info, "automation nightly: nothing to do") {
		t.Errorf("notes %+v", notes.notes)
	}
}

func TestAutomationLevel2FinalFailureAfterTheFixesAreUsed(t *testing.T) {
	h, notes := autoRun(t, 2, true)
	h.m.cfg.Workspace.MaxAutoFixes = 1
	h.toGraph()
	h.finish(nil, "T1", "T2")
	h.finish(errors.New("boom"), "CHECK")
	if h.m.auto.finished {
		t.Fatal("finished while a fix is still allowed")
	}
	h.finish(nil, "F1")
	h.finish(errors.New("boom"), "CHECK")
	a := h.m.auto
	if !a.finished || !strings.HasPrefix(a.result.Result, "failed: Build & test still failing") {
		t.Errorf("result %+v", a.result)
	}
	if !notes.has(notify.Failed, "automation nightly failed") {
		t.Errorf("notes %+v", notes.notes)
	}
}

func TestAutomationVerdictTable(t *testing.T) {
	st := func(t1, t2, c workspace.Status) map[string]workspace.Status {
		return map[string]workspace.Status{"T1": t1, "T2": t2, "CHECK": c}
	}
	const (
		done, failed, waiting, todo = workspace.StatusDone, workspace.StatusFailed, workspace.StatusWaiting, workspace.StatusTodo
	)
	cases := []struct {
		name      string
		level     int
		merged    bool
		statuses  map[string]workspace.Status
		tweak     func(h *wsHarness)
		want      string // "" = not done
		wantLevel notify.Level
	}{
		{"L2 passed", 2, true, allDone, nil, "build & test passed", notify.Done},
		{"L2 nothing changed", 2, false, allDone, nil, "nothing to do", notify.Info},
		{"L2 check failed, fixes left", 2, true, st(done, done, failed), nil, "", 0},
		{"L2 check failed, fixes used up", 2, true, st(done, done, failed), func(h *wsHarness) {
			h.m.sess.update(func(s *workspace.Session) { s.AutoFixes = 2 })
		}, "failed: Build & test still failing, no automatic fixes left", notify.Failed},
		{"L2 check failed, nothing changed", 2, false, st(done, done, failed), func(h *wsHarness) {
			h.m.sess.update(func(s *workspace.Session) { s.AutoFixes = 2 })
		}, "nothing to do", notify.Info},
		{"L2 node waits for the user", 2, true, st(done, waiting, todo), nil, "needs you", notify.NeedsYou},
		{"L2 node failed", 2, true, st(done, failed, todo), nil, "failed: T2 failed", notify.Failed},
		{"L2 node without a role", 2, true, st(done, todo, todo), func(h *wsHarness) {
			_ = h.eng.Replace(func(g *workspace.Graph) { g.Node("T2").Role = "" })
			h.m.g = h.eng.Snapshot()
		}, "needs you", notify.NeedsYou},
		{"L2 work still runnable", 2, true, st(done, todo, todo), nil, "", 0},
		{"L2 budget reached", 2, true, st(done, done, todo), func(h *wsHarness) {
			_ = h.eng.Replace(func(g *workspace.Graph) {
				g.Node("T1").CostUSD, g.Node("T1").CostKnown, g.Node("T1").InputTokens = 9, true, 1
			})
			h.m.g = h.eng.Snapshot()
		}, "budget reached", notify.Failed},
		{"L2 splitting", 2, true, allDone, func(h *wsHarness) { h.m.splitting = true }, "", 0},
		{"L2 reviewing", 2, true, allDone, func(h *wsHarness) { h.m.reviewing = true }, "", 0},
		{"L3 passed, no pull request yet", 3, true, allDone, nil, "", 0},
		{"L3 pull request working", 3, true, allDone, func(h *wsHarness) {
			h.m.pr.working = true
			h.m.auto.prPassed = true
		}, "", 0},
		{"L3 checks passed", 3, true, allDone, func(h *wsHarness) {
			h.m.auto.prPassed = true
			h.m.sess.update(func(s *workspace.Session) { s.PR = &workspace.PRState{Number: 12} })
		}, "pr #12 checks passed", notify.Done},
		{"L3 CI fixes used up", 3, true, allDone, func(h *wsHarness) {
			h.m.auto.prExhausted = true
			h.m.sess.update(func(s *workspace.Session) { s.PR = &workspace.PRState{Number: 12} })
		}, "pr #12 CI fixes used up", notify.Failed},
		{"L3 pull request failed", 3, true, allDone, func(h *wsHarness) { h.m.auto.prErr = "gh not logged in" }, "failed: pull request: gh not logged in", notify.Failed},
		{"L3 nothing merged", 3, false, allDone, func(h *wsHarness) { h.m.auto.prPassed = true }, "nothing to do", notify.Info},
		{"L3 no pull request because nothing was done", 3, true, allDone, func(h *wsHarness) { h.m.auto.prNone = true }, "nothing to do", notify.Info},
		{"L3 build failed: no pull request", 3, true, st(done, done, failed), func(h *wsHarness) {
			h.m.sess.update(func(s *workspace.Session) { s.AutoFixes = 2 })
		}, "failed: Build & test still failing, no automatic fixes left", notify.Failed},
		{"L3 lowered to L2 without isolation", 3, true, allDone, func(h *wsHarness) { h.m.iso = nil }, "build & test passed", notify.Done},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := autoRun(t, c.level, c.merged)
			h.toGraph()
			h.setStatuses(c.statuses)
			if c.tweak != nil {
				c.tweak(h)
			}
			got, lvl, done := h.verdict()
			if c.want == "" {
				if done {
					t.Fatalf("done with %q, want pending", got)
				}
				return
			}
			if !done || got != c.want || lvl != c.wantLevel {
				t.Errorf("verdict %q level %v done %v, want %q level %v", got, lvl, done, c.want, c.wantLevel)
			}
		})
	}
}

func TestAutomationLevel3PullRequestFlow(t *testing.T) {
	h, notes := autoRun(t, 3, true)
	h.toGraph()
	h.setStatuses(allDone)
	h.m.sess.update(func(s *workspace.Session) { s.PR = &workspace.PRState{Number: 9} })
	sid := h.m.sess.snapshot().ID
	h.m.pr.watching = true

	// A poll that fails keeps the run going.
	h.send(wsPRMsg{kind: "poll", gen: h.m.pr.gen, session: sid, st: workspace.PRState{Number: 9}, poll: workspace.PollResult{Failed: []string{"ci"}}})
	if h.m.auto.finished {
		t.Fatal("finished with failing checks")
	}
	// A stale message is ignored.
	h.send(wsPRMsg{kind: "poll", gen: h.m.pr.gen + 5, session: sid, poll: workspace.PollResult{Passed: true}})
	if h.m.auto.finished || h.m.auto.prPassed {
		t.Fatal("a stale poll counted")
	}
	h.send(wsPRMsg{kind: "poll", gen: h.m.pr.gen, session: sid, st: workspace.PRState{Number: 9}, poll: workspace.PollResult{Passed: true, HeadSHA: "abc"}})
	if !h.m.auto.finished || h.m.auto.result.Result != "pr #9 checks passed" {
		t.Fatalf("result %+v", h.m.auto.result)
	}
	if !notes.has(notify.Done, "automation nightly: pr #9 checks passed") {
		t.Errorf("notes %+v", notes.notes)
	}
}

func TestAutomationLevel3NothingChangedOpensNoPullRequest(t *testing.T) {
	h, notes := autoRun(t, 3, false)
	var mu sync.Mutex
	var gh []string
	h.m.deps.ghRun = func(_ context.Context, _ string, name string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		gh = append(gh, name+" "+strings.Join(args, " "))
		return "", errors.New("must not be called")
	}
	h.toGraph()
	h.setStatuses(allDone)
	m, cmd := h.m.autoPR()
	if cmd != nil || m.pr.working {
		t.Fatalf("autoPR started the pull request flow: working %v", m.pr.working)
	}
	// The check event of an unchanged run ends it as "nothing to do".
	h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "CHECK", Type: workspace.EventDone}})
	if got := h.m.auto.result.Result; got != "nothing to do" {
		t.Errorf("result %q", got)
	}
	if !notes.has(notify.Info, "automation nightly: nothing to do") {
		t.Errorf("notes %+v", notes.notes)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gh) != 0 {
		t.Errorf("gh was called: %q", gh)
	}
}

func TestAutomationLevel3ChangedRunStartsThePullRequest(t *testing.T) {
	h, _ := autoRun(t, 3, true)
	h.toGraph()
	h.setStatuses(allDone)
	m, cmd := h.m.autoPR()
	if cmd == nil || !m.pr.working {
		t.Fatalf("autoPR did not start: working %v", m.pr.working)
	}
}

func TestAutomationPullRequestMessagesAreObserved(t *testing.T) {
	h, _ := autoRun(t, 3, true)
	h.toGraph()
	sid := h.m.sess.snapshot().ID
	h.m.auto.prPassed = true
	h.send(wsPRMsg{kind: "ensured", gen: h.m.pr.gen, session: sid, err: workspace.ErrPRNothingDone})
	if !h.m.auto.prNone {
		t.Error("ErrPRNothingDone not recorded")
	}
	h.send(wsPRMsg{kind: "ensured", gen: h.m.pr.gen, session: sid, err: errors.New("push rejected\nreason")})
	if h.m.auto.prErr != "push rejected reason" {
		t.Errorf("prErr %q", h.m.auto.prErr)
	}
	h.m.auto.prPassed = false
	h.send(wsPRMsg{kind: "poll", gen: h.m.pr.gen, session: sid, poll: workspace.PollResult{Exhausted: true}})
	if !h.m.auto.prExhausted {
		t.Error("Exhausted not recorded")
	}
	h.send(wsPRMsg{kind: "poll", gen: h.m.pr.gen, session: sid, poll: workspace.PollResult{Passed: true}})
	if h.m.auto.prExhausted || !h.m.auto.prPassed {
		t.Error("a passing poll must clear the exhausted flag")
	}
}

func TestAutomationLeavesNormalWorkspaceAlone(t *testing.T) {
	h, _ := autoHarness(t, 2)
	h.toGraph()
	h.finish(nil, "T1")
	if h.m.auto != nil || h.m.quitting {
		t.Error("a normal Workspace behaved like an automation")
	}
}

func TestAutomationNoChecksGracePeriod(t *testing.T) {
	h, notes := autoRun(t, 3, true)
	h.toGraph()
	h.setStatuses(allDone)
	h.m.sess.update(func(s *workspace.Session) { s.PR = &workspace.PRState{Number: 9} })
	sid := h.m.sess.snapshot().ID
	h.m.pr.watching = true
	clock := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	h.m.now = func() time.Time { return clock }
	poll := func(r workspace.PollResult) {
		h.send(wsPRMsg{kind: "poll", gen: h.m.pr.gen, session: sid, st: workspace.PRState{Number: 9}, poll: r})
	}
	poll(workspace.PollResult{NoChecks: true})
	clock = clock.Add(9 * time.Minute)
	poll(workspace.PollResult{NoChecks: true})
	if h.m.auto.finished {
		t.Fatal("finished inside the grace period")
	}
	clock = clock.Add(time.Minute)
	poll(workspace.PollResult{NoChecks: true})
	if !h.m.auto.finished || h.m.auto.result.Result != "pr #9 opened (no checks)" {
		t.Fatalf("result %+v", h.m.auto.result)
	}
	if !notes.has(notify.Info, "automation nightly: pr #9 opened (no checks)") {
		t.Errorf("notes %+v", notes.notes)
	}
}

func TestAutomationChecksAppearInsideTheGracePeriod(t *testing.T) {
	h, _ := autoRun(t, 3, true)
	h.toGraph()
	h.setStatuses(allDone)
	h.m.sess.update(func(s *workspace.Session) { s.PR = &workspace.PRState{Number: 9} })
	sid := h.m.sess.snapshot().ID
	h.m.pr.watching = true
	clock := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	h.m.now = func() time.Time { return clock }
	poll := func(r workspace.PollResult) {
		h.send(wsPRMsg{kind: "poll", gen: h.m.pr.gen, session: sid, st: workspace.PRState{Number: 9}, poll: r})
	}
	poll(workspace.PollResult{NoChecks: true})
	clock = clock.Add(5 * time.Minute)
	poll(workspace.PollResult{}) // checks exist and are pending
	clock = clock.Add(8 * time.Minute)
	poll(workspace.PollResult{})
	if h.m.auto.finished {
		t.Fatal("the grace period was not reset by checks")
	}
	poll(workspace.PollResult{Passed: true, HeadSHA: "abc"})
	if h.m.auto.result.Result != "pr #9 checks passed" {
		t.Errorf("result %+v", h.m.auto.result)
	}
}
