package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// AutomationOptions turns the Workspace into an unattended automation run: it
// sends the task (or the issue given in WorkspaceOptions.Issue), runs at the
// given level and budget, and exits by itself when the run is done.
type AutomationOptions struct {
	Name      string
	Timeout   time.Duration
	Task      string  // task text; "" when WorkspaceOptions.Issue is used
	Autonomy  int     // 2 or 3
	BudgetUSD float64 // budget of the session
	// AllowSubscription accepts the claude runner on a subscription login;
	// without it routing never picks claude in this run.
	AllowSubscription bool
}

// AutomationResult is how an automation run ended.
type AutomationResult struct {
	Name     string
	Finished bool   // false: the user left before the run was done
	Result   string // "pr #12 checks passed", "nothing to do", "needs you", ...
	CostUSD  float64
}

// autoNoChecksGrace is how long a pull request may report no checks at all
// before the run ends with "pr #N opened (no checks)".
const autoNoChecksGrace = 10 * time.Minute

// wsAutoTaskMsg sends the task of an automation to the lead.
type wsAutoTaskMsg struct{}

// wsAutoTimeoutMsg fires when the timeout of the automation is over.
type wsAutoTimeoutMsg struct{}

// wsAuto is the state of an automation run; nil in a normal Workspace.
type wsAuto struct {
	opts   AutomationOptions
	merged func(root, session string) bool // did the session change anything

	mergedKnown bool
	mergedVal   bool

	prNone      bool   // the pull request found nothing merged
	prErr       string // pushing or opening the pull request failed
	prPassed    bool   // checks of the pull request passed
	prExhausted bool   // CI keeps failing and the fix rounds are used up
	// prNoChecksSince is when polls first reported no checks; zero while
	// checks exist.
	prNoChecksSince time.Time
	startErr        string // the task or issue could not be started

	finished bool
	result   AutomationResult
}

// initAutomation sets up the automation of the options, if any: the level and
// the budget of the session.
func (m *workspaceModel) initAutomation(o AutomationOptions) {
	if o.Name == "" {
		return
	}
	m.auto = &wsAuto{opts: o, merged: workspace.HasMergedNode, result: AutomationResult{Name: o.Name}}
	m.sess.update(func(s *workspace.Session) {
		s.Autonomy = o.Autonomy
		s.BudgetUSD = o.BudgetUSD
	})
}

// autoStartCmd is what Init adds for an automation: the task and the timeout.
func (m workspaceModel) autoStartCmd() tea.Cmd {
	if m.auto == nil {
		return nil
	}
	cmds := []tea.Cmd{}
	if m.auto.opts.Task != "" {
		cmds = append(cmds, func() tea.Msg { return wsAutoTaskMsg{} })
	}
	if t := m.auto.opts.Timeout; t > 0 {
		cmds = append(cmds, tea.Tick(t, func(time.Time) tea.Msg { return wsAutoTimeoutMsg{} }))
	}
	return tea.Batch(cmds...)
}

// autoTaskPrefix makes the task of an automation an assignment, so the lead
// creates nodes for it instead of only answering in the chat.
const autoTaskPrefix = "This is an assigned task: create the nodes for it.\n\n"

// Update runs the Workspace update and, for an automation, checks afterwards
// whether the run is done.
func (m workspaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.auto == nil || m.auto.finished {
		return m.update(msg)
	}
	switch msg.(type) {
	case wsAutoTaskMsg:
		return m.sendTask(autoTaskPrefix+m.auto.opts.Task, m.auto.opts.Task)
	case wsAutoTimeoutMsg:
		return m.autoFinish("timed out", notify.Failed, fmt.Sprintf("automation %s timed out", m.auto.opts.Name))
	}
	m.autoObserve(msg)
	next, cmd := m.update(msg)
	wm, ok := next.(workspaceModel)
	if !ok || wm.auto == nil {
		return next, cmd
	}
	if res, level, note, done := wm.autoVerdict(); done {
		quit, qcmd := wm.autoFinish(res, level, note)
		return quit, tea.Batch(cmd, qcmd)
	}
	return wm, cmd
}

// autoObserve records what the messages say about the pull request and about
// a start that failed. It runs before the message is handled.
func (m workspaceModel) autoObserve(msg tea.Msg) {
	a := m.auto
	switch msg := msg.(type) {
	case wsSplitDoneMsg:
		if msg.gen == m.splitGen && m.splitting {
			switch {
			case msg.err != nil:
				a.startErr = oneLine(msg.err.Error())
			case msg.g != nil && msg.g.Ask != nil:
				// Nobody can answer the question. Nothing else would end the run.
				a.startErr = "the lead asked a question and created no nodes: " + truncateOneLine(oneLine(msg.g.Ask.Question), 120)
			case msg.g != nil && msg.g.Answer != "":
				// The reply is in the chat. Nothing else would end the run.
				a.startErr = "the lead only answered in the chat and created no nodes: " + truncateOneLine(oneLine(msg.g.Answer), 120)
			}
		}
	case wsIssueMsg:
		switch {
		case msg.err != nil:
			a.startErr = oneLine(msg.err.Error())
		case msg.issue.Closed && !msg.force:
			a.startErr = msg.issue.Summary() + " is closed"
		}
	case wsPRMsg:
		if m.stale(msg) {
			return
		}
		switch msg.kind {
		case "ensured":
			switch {
			case errors.Is(msg.err, workspace.ErrPRNothingDone):
				a.prNone = true
			case msg.err != nil:
				a.prErr = oneLine(msg.err.Error())
			}
		case "poll":
			if msg.err != nil {
				return
			}
			a.prPassed = msg.poll.Passed
			switch {
			case !msg.poll.NoChecks:
				a.prNoChecksSince = time.Time{}
			case a.prNoChecksSince.IsZero():
				a.prNoChecksSince = m.now()
			}
			if msg.poll.Passed {
				a.prExhausted = false
			} else if msg.poll.Exhausted {
				a.prExhausted = true
			}
		}
	}
}

// autoNoChange reports whether an automation run has nothing merged, so the
// pull request flow must not start.
func (m workspaceModel) autoNoChange() bool {
	return m.auto != nil && !m.autoMerged()
}

// autoMerged is whether the session changed anything. A positive answer is
// kept; a negative one is asked again.
func (m workspaceModel) autoMerged() bool {
	a := m.auto
	if a.mergedKnown && a.mergedVal {
		return true
	}
	a.mergedVal = a.merged(m.root, m.sess.snapshot().ID)
	a.mergedKnown = true
	return a.mergedVal
}

// autoVerdict decides whether the run is done: the engine is idle, nothing
// automatic is pending, and the result of the level is in.
func (m workspaceModel) autoVerdict() (result string, level notify.Level, note string, done bool) {
	a := m.auto
	name := a.opts.Name
	fail := func(why string) (string, notify.Level, string, bool) {
		return "failed: " + why, notify.Failed, fmt.Sprintf("automation %s failed: %s", name, why), true
	}
	if a.startErr != "" {
		return fail(a.startErr)
	}
	if m.eng == nil || m.g == nil || len(m.g.Nodes) == 0 {
		return "", 0, "", false
	}
	_, running, _, _ := m.g.Counts()
	if m.splitting || m.reviewing || m.pr.working || m.installing != "" || running > 0 || m.running() > 0 {
		return "", 0, "", false
	}
	if m.budgetReached() {
		return "budget reached", notify.Failed, fmt.Sprintf("automation %s stopped: budget reached", name), true
	}
	if len(m.g.Runnable()) > 0 {
		return "", 0, "", false
	}
	if ids := mcpNodeIDs(m.g.PendingApproval()); len(ids) > 0 {
		return "needs you: " + mcpNeedsYou(ids), notify.NeedsYou, fmt.Sprintf("automation %s needs you", name), true
	}
	needsYou := func() (string, notify.Level, string, bool) {
		return "needs you", notify.NeedsYou, fmt.Sprintf("automation %s needs you", name), true
	}
	if m.g.CountWaiting() > 0 || unassignedWork(m.g) > 0 {
		return needsYou()
	}
	for _, n := range m.g.Nodes {
		if n.Kind != workspace.KindCheck && n.Status == workspace.StatusFailed {
			return fail(n.ID + " failed")
		}
	}
	checkFailed := false
	if c := m.g.Node("CHECK"); c != nil && c.Kind == workspace.KindCheck {
		switch c.Status {
		case workspace.StatusDone:
		case workspace.StatusFailed:
			if m.sess.snapshot().AutoFixes < m.maxAutoFixes() {
				return "", 0, "", false // the fix node is on its way
			}
			checkFailed = true
		default:
			return needsYou()
		}
	}
	if !m.autoMerged() {
		return "nothing to do", notify.Info, fmt.Sprintf("automation %s: nothing to do", name), true
	}
	if checkFailed {
		return fail("Build & test still failing, no automatic fixes left")
	}
	lvl, _ := m.autonomy()
	if lvl < autoUnattended {
		return "build & test passed", notify.Done, fmt.Sprintf("automation %s: Build & test passed", name), true
	}
	num := 0
	if pr := m.sess.snapshot().PR; pr != nil {
		num = pr.Number
	}
	switch {
	case a.prErr != "":
		return fail("pull request: " + a.prErr)
	case a.prNone:
		return "nothing to do", notify.Info, fmt.Sprintf("automation %s: nothing to do", name), true
	case a.prPassed:
		r := fmt.Sprintf("pr #%d checks passed", num)
		return r, notify.Done, fmt.Sprintf("automation %s: %s", name, r), true
	case a.prExhausted:
		r := fmt.Sprintf("pr #%d CI fixes used up", num)
		return r, notify.Failed, fmt.Sprintf("automation %s: %s", name, r), true
	case !a.prNoChecksSince.IsZero() && m.now().Sub(a.prNoChecksSince) >= autoNoChecksGrace:
		r := fmt.Sprintf("pr #%d opened (no checks)", num)
		return r, notify.Info, fmt.Sprintf("automation %s: %s", name, r), true
	}
	return "", 0, "", false
}

// autoFinish records the result, tells the user and closes the Workspace.
func (m workspaceModel) autoFinish(result string, level notify.Level, note string) (tea.Model, tea.Cmd) {
	a := m.auto
	if a.finished {
		return m, nil
	}
	a.finished = true
	a.result = AutomationResult{Name: a.opts.Name, Finished: true, Result: result, CostUSD: m.use().spent}
	m.autoSay("automation " + a.opts.Name + ": " + result)
	m.notifyNote(level, note)
	return m.quit(false)
}
