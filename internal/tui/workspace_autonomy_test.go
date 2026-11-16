package tui

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

type autoNotes struct {
	mu    sync.Mutex
	notes []notify.Note
}

func (a *autoNotes) send(_ config.NotifyConfig, n notify.Note) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notes = append(a.notes, n)
}

func (a *autoNotes) has(level notify.Level, sub string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, n := range a.notes {
		if n.Level == level && strings.Contains(n.Body, sub) {
			return true
		}
	}
	return false
}

// autoHarness is a Workspace at the given level with a recording notifier.
func autoHarness(t *testing.T, level int) (*wsHarness, *autoNotes) {
	t.Helper()
	h := newWSHarness(t)
	notes := &autoNotes{}
	h.m.deps.notify = notes.send
	h.m.cfg.Workspace.Autonomy = level
	return h, notes
}

// autoLines are the "auto ›" lines of the conversation.
func (h *wsHarness) autoLines() []string {
	var out []string
	for _, l := range h.m.sess.snapshot().Transcript {
		if strings.HasPrefix(l.Text, "auto › ") {
			out = append(out, l.Text)
		}
	}
	return out
}

func (h *wsHarness) wantAuto(sub string) {
	h.t.Helper()
	for _, l := range h.autoLines() {
		if strings.Contains(l, sub) {
			return
		}
	}
	h.t.Fatalf("no auto line contains %q: %q", sub, h.autoLines())
}

// failCheck marks Build & test failed and delivers the event for it.
func (h *wsHarness) failCheck() {
	h.t.Helper()
	if err := h.eng.Replace(func(g *workspace.Graph) {
		c := g.Node("CHECK")
		c.Status, c.Error = workspace.StatusFailed, "boom"
	}); err != nil {
		h.t.Fatal(err)
	}
	h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "CHECK", Type: workspace.EventFailed, Text: "boom"}})
}

func TestAutonomyLevel1DoesNothing(t *testing.T) {
	h, _ := autoHarness(t, 0)
	h.toGraph()
	if h.m.running() != 0 || len(h.autoLines()) != 0 {
		t.Fatalf("level 1 acted: running %d, lines %q", h.m.running(), h.autoLines())
	}
	if h.m.autoChip() != "" {
		t.Errorf("chip %q at level 1", h.m.autoChip())
	}
	h.failCheck()
	if h.m.g.Node("F1") != nil || len(h.autoLines()) != 0 {
		t.Error("level 1 added a fix node")
	}
}

func TestAutonomyLevel2AcceptsAndRunsAfterSplit(t *testing.T) {
	h, notes := autoHarness(t, 2)
	h.toGraph()
	h.wantAuto("accepted 4 roles and started the run")
	if h.status("T1") != workspace.StatusRunning {
		t.Errorf("T1 is %s, want running", h.status("T1"))
	}
	if h.m.g.Node("T2").Role != "" {
		t.Errorf("T2 has role %q: docs_writer is not defined, it must stay unassigned", h.m.g.Node("T2").Role)
	}
	if !notes.has(notify.NeedsYou, "1 nodes need a role") {
		t.Errorf("no NeedsYou note: %+v", notes.notes)
	}
	if h.m.autoChip() != "L2 assisted" {
		t.Errorf("chip %q", h.m.autoChip())
	}
}

func TestAutonomyLevel2AllDefinedRoles(t *testing.T) {
	h, notes := autoHarness(t, 2)
	h.graph = wsSmallGraph
	h.toGraph()
	h.wantAuto("accepted 2 roles and started the run")
	if notes.has(notify.NeedsYou, "need a role") {
		t.Errorf("unexpected notes %+v", notes.notes)
	}
}

// finish releases the nodes with err and waits until each has ended.
func (h *wsHarness) finish(err error, ids ...string) {
	h.t.Helper()
	for _, id := range ids {
		h.pump(func(m workspaceModel) bool {
			return m.g.Node(id) != nil && m.g.Node(id).Status == workspace.StatusRunning
		})
		h.exec.release(id, err)
		h.pump(func(m workspaceModel) bool { return m.g.Node(id).Status != workspace.StatusRunning })
		h.drain()
	}
}

// drain delivers the engine events that are already queued.
func (h *wsHarness) drain() {
	for {
		select {
		case ev := <-h.eng.Events():
			h.send(wsEventMsg{eng: h.eng, ev: ev})
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func TestAutonomyLevel2FixesUpToTheMax(t *testing.T) {
	h, notes := autoHarness(t, 2)
	h.graph = wsSmallGraph
	h.toGraph()
	h.finish(nil, "T1", "T2")
	for i, want := range []string{"F1", "F2"} {
		h.finish(errors.New("boom"), "CHECK")
		h.wantAuto(fmt.Sprintf("Build & test failed, added %s (%d/2)", want, i+1))
		if h.m.g.Node(want) == nil || h.m.g.Node(want).Role != "coder_fixer" {
			t.Fatalf("round %d: %s missing or unassigned", i+1, want)
		}
		h.finish(nil, want) // the fix runs, then Build & test runs again
	}
	h.finish(errors.New("boom"), "CHECK")
	if h.m.g.Node("F3") != nil {
		t.Error("a third fix was added")
	}
	h.wantAuto("2 automatic fixes are used up")
	if !notes.has(notify.Failed, "no automatic fixes left") {
		t.Errorf("no Failed note: %+v", notes.notes)
	}
	if got := h.m.sess.snapshot().AutoFixes; got != 2 {
		t.Errorf("AutoFixes = %d", got)
	}
}

func TestAutonomyMaxAutoFixesFromConfig(t *testing.T) {
	h, _ := autoHarness(t, 2)
	h.m.cfg.Workspace.MaxAutoFixes = 1
	h.graph = wsSmallGraph
	h.toGraph()
	h.finish(nil, "T1", "T2")
	h.finish(errors.New("boom"), "CHECK")
	h.finish(nil, "F1")
	h.finish(errors.New("boom"), "CHECK")
	if h.m.g.Node("F2") != nil {
		t.Error("max_auto_fixes 1 allowed a second fix")
	}
	h.wantAuto("1 automatic fixes are used up")
}

func TestAutonomyBudgetReachedStopsAutomation(t *testing.T) {
	h, notes := autoHarness(t, 1)
	h.m.cfg.Workspace.BudgetUSD = 1
	h.graph = wsSmallGraph
	h.toGraph()
	h.m.cfg.Workspace.Autonomy = 2
	if err := h.eng.Replace(func(g *workspace.Graph) {
		g.Node("T1").CostUSD, g.Node("T1").CostKnown, g.Node("T1").InputTokens = 2, true, 1000
	}); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.failCheck()
	if h.m.g.Node("F1") != nil {
		t.Error("a fix was added with the budget reached")
	}
	h.wantAuto("budget is reached")
	if !notes.has(notify.Failed, "budget reached") {
		t.Errorf("no Failed note: %+v", notes.notes)
	}
	// A new split does not start either.
	h.m.screen = wsScreenMain
	h.m.setFocus(wsFocusPrompt)
	h.sendTask("more")
	h.wantAuto("budget reached, the run was not started")
}

func TestAutonomyPauseWithStopAll(t *testing.T) {
	h, _ := autoHarness(t, 1)
	h.graph = wsSmallGraph
	h.toGraph()
	h.m.cfg.Workspace.Autonomy = 2
	h.m.setFocus(wsFocusGraph)
	h.key("S")
	if !h.m.autoPaused || !strings.Contains(h.m.autoChip(), "auto paused") {
		t.Fatalf("not paused: chip %q", h.m.autoChip())
	}
	h.failCheck()
	if h.m.g.Node("F1") != nil {
		t.Error("a paused automation added a fix")
	}
	h.m.sel = "T1"
	h.key("R")
	if h.m.autoPaused {
		t.Error("R must resume the automation")
	}
	h.command("/autonomy 2")
	h.key("S")
	h.command("/autonomy 2")
	if h.m.autoPaused {
		t.Error("/autonomy must resume the automation")
	}
}

func TestAutonomyLevel3NeedsBudget(t *testing.T) {
	h, _ := autoHarness(t, 3)
	h.graph = wsSmallGraph
	h.toGraph()
	h.wantAuto("level 3 needs a budget")
	h.wantAuto("accepted 2 roles and started the run") // behaves as level 2
	if lvl, _ := h.m.autonomy(); lvl != 2 {
		t.Errorf("level %d, want 2", lvl)
	}
	n := 0
	for _, l := range h.autoLines() {
		if strings.Contains(l, "level 3 needs") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("warned %d times", n)
	}
}

func TestAutonomyLevel3NeedsIsolation(t *testing.T) {
	h, _ := autoHarness(t, 3)
	h.m.cfg.Workspace.BudgetUSD = 5
	h.graph = wsSmallGraph
	h.toGraph()
	h.wantAuto("level 3 needs worktree isolation")
	if lvl, _ := h.m.autonomy(); lvl != 2 {
		t.Errorf("level %d, want 2", lvl)
	}
	if h.m.pr.autoFix || h.m.pr.working {
		t.Error("level 3 acted without isolation")
	}
}

func TestAutonomyLevel3OpensPRAndWatchesWithAutoFix(t *testing.T) {
	h, gh, _, _ := prHarness(t)
	notes := &autoNotes{}
	h.m.deps.notify = notes.send
	h.m.cfg.Workspace.Autonomy = 3
	h.m.cfg.Workspace.BudgetUSD = 5
	if lvl, why := h.m.autonomy(); lvl != 3 {
		t.Fatalf("level %d: %s", lvl, why)
	}
	cmd := h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "CHECK", Type: workspace.EventDone}})
	h.wantAuto("Build & test passed, pushing")
	pollCmd := h.feed(cmd, "ensured")
	h.wantAuto("opened PR #9 and started watching")
	if !h.m.pr.watching || !h.m.pr.autoFix {
		t.Fatalf("watching %v autoFix %v", h.m.pr.watching, h.m.pr.autoFix)
	}
	if h.m.cfg.Workspace.PRAutoFix {
		t.Error("the config must not change")
	}
	if gh.count("pr merge") != 0 {
		t.Error("level 3 merged the pull request")
	}
	h.feed(pollCmd, "poll") // CI fails: the fix node runs without asking
	h.want("Added F1 and started it.")
	h.pump(func(m workspaceModel) bool { return m.g.Node("F1").Status == workspace.StatusRunning })
	h.exec.release("F1", nil)

	// A second pass of Build & test while watching does not start another loop.
	if cmd := h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "CHECK", Type: workspace.EventDone}}); len(prMsgs(cmd)) != 0 {
		t.Error("a second pull request command started")
	}
}

func TestAutonomyLevel3BudgetReachedDoesNotPush(t *testing.T) {
	h, _, _, _ := prHarness(t)
	h.m.cfg.Workspace.Autonomy = 3
	h.m.cfg.Workspace.BudgetUSD = 1
	if err := h.eng.Replace(func(g *workspace.Graph) {
		g.Node("T1").CostUSD, g.Node("T1").CostKnown, g.Node("T1").InputTokens = 2, true, 1000
	}); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	cmd := h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "CHECK", Type: workspace.EventDone}})
	if len(prMsgs(cmd)) != 0 || h.m.pr.working {
		t.Error("pushed with the budget reached")
	}
	h.wantAuto("budget is reached: not pushing")
}

func TestAutonomyLevel2DoesNotOpenPR(t *testing.T) {
	h, gh, _, _ := prHarness(t)
	h.m.cfg.Workspace.Autonomy = 2
	cmd := h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "CHECK", Type: workspace.EventDone}})
	if len(prMsgs(cmd)) != 0 || h.m.pr.working || gh.count("pr create") != 0 {
		t.Error("level 2 opened a pull request")
	}
}

func TestAutonomyCommand(t *testing.T) {
	h, _ := autoHarness(t, 0)
	h.command("/autonomy")
	h.want("1 · manual")
	h.key("esc")

	h.command("/autonomy 9")
	h.want("usage: /autonomy [1|2|3]")
	h.command("/autonomy fast")
	if h.m.sess.snapshot().Autonomy != 0 {
		t.Error("an invalid value was stored")
	}

	h.command("/autonomy assisted")
	if got := h.m.sess.snapshot().Autonomy; got != 2 {
		t.Fatalf("autonomy %d, want 2", got)
	}

	h.command("/autonomy 3")
	h.want("needs a budget")
	if got := h.m.sess.snapshot().Autonomy; got != 2 {
		t.Errorf("autonomy %d after a refused level 3, want 2", got)
	}

	h.m.cfg.Workspace.BudgetUSD = 5
	h.command("/autonomy 3") // no graph yet: isolation is not known, so it is allowed
	if got := h.m.sess.snapshot().Autonomy; got != 3 {
		t.Errorf("autonomy %d, want 3", got)
	}
	h.command("/autonomy 1")
	if got := h.m.sess.snapshot().Autonomy; got != 1 {
		t.Errorf("autonomy %d, want 1 (a session override of the config)", got)
	}
	if lvl, _ := h.m.autonomy(); lvl != 1 {
		t.Errorf("level %d", lvl)
	}

	// The override is saved with the session.
	h.toGraph()
	h.command("/autonomy 2")
	back, err := workspace.LoadSession(h.root, h.m.sess.snapshot().ID)
	if err != nil || back.Autonomy != 2 {
		t.Fatalf("saved session: %+v, %v", back, err)
	}
}

func TestAutonomyLevel3RefusedWithSharedIsolation(t *testing.T) {
	h, _ := autoHarness(t, 0)
	h.m.cfg.Workspace.BudgetUSD = 5
	h.m.cfg.Workspace.Isolation = workspace.IsolationShared
	h.command("/autonomy 3")
	h.want("needs worktree isolation")
	if got := h.m.sess.snapshot().Autonomy; got != 2 {
		t.Errorf("autonomy %d, want 2", got)
	}
}

func TestAutonomyChipShowsLevel(t *testing.T) {
	h, _ := autoHarness(t, 2)
	h.m.sess.update(func(s *workspace.Session) { s.Autonomy = 0 })
	if got := h.m.autoChip(); got != "L2 assisted" {
		t.Errorf("chip %q", got)
	}
	h.m.cfg.Workspace.Autonomy = 7
	if h.m.autoChip() != "" {
		t.Error("an invalid configured level must be manual")
	}
}

func TestSplitDoneNoteOnlyAtLevel1(t *testing.T) {
	h, notes := autoHarness(t, 1)
	h.toGraph()
	if !notes.has(notify.NeedsYou, "Assign roles") {
		t.Errorf("level 1 split sent no assign-roles note: %+v", notes.notes)
	}
	h, notes = autoHarness(t, 2)
	h.graph = wsSmallGraph
	h.toGraph()
	if notes.has(notify.NeedsYou, "Assign roles") {
		t.Errorf("level 2 split sent the assign-roles note: %+v", notes.notes)
	}
}
