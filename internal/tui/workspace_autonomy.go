package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// Autonomy levels: how much of the loop the Workspace runs by itself.
const (
	autoManual     = 1 // nothing runs until the user presses r or R
	autoAssisted   = 2 // accept roles, run, add fix nodes after a failed Build & test
	autoUnattended = 3 // level 2 plus push, open the pull request and watch it
)

// defaultMaxAutoFixes is the number of automatic fix nodes per session.
const defaultMaxAutoFixes = 2

// autoCompactAt is the number of finished work nodes from which /compact runs
// by itself at level 2 and up.
const autoCompactAt = 10

const autoUsage = "usage: /autonomy [1|2|3]  (1 manual, 2 assisted, 3 unattended)"

func autonomyName(level int) string {
	switch level {
	case autoAssisted:
		return "assisted"
	case autoUnattended:
		return "unattended"
	}
	return "manual"
}

// parseAutonomy reads "2", "L2" or a level name.
func parseAutonomy(s string) (int, bool) {
	s = strings.ToLower(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "l"))
	switch s {
	case "manual":
		return autoManual, true
	case "assisted":
		return autoAssisted, true
	case "unattended":
		return autoUnattended, true
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < autoManual || v > autoUnattended {
		return 0, false
	}
	return v, true
}

// requestedAutonomy is the level asked for: the session's /autonomy, else
// workspace.autonomy. Anything outside 1..3 is manual.
func (m workspaceModel) requestedAutonomy() int {
	v := m.liveCfg().Workspace.Autonomy
	if s := m.sess.snapshot().Autonomy; s > 0 {
		v = s
	}
	if v < autoManual || v > autoUnattended {
		return autoManual
	}
	return v
}

// level3Blocker is why level 3 cannot run now; "" when it can.
func (m workspaceModel) level3Blocker() string {
	c, s := m.liveCfg(), m.sess.snapshot()
	if limit, _ := budgetOf(c, s); limit <= 0 {
		return "needs a budget: set workspace.budget_usd or /budget first"
	}
	if m.iso == nil && (m.eng != nil || c.Workspace.Isolation == workspace.IsolationShared) {
		return "needs worktree isolation: this session works in your checkout directly"
	}
	return ""
}

// autonomy is the level in force and, when it is lower than the one asked
// for, why.
func (m workspaceModel) autonomy() (level int, why string) {
	level = m.requestedAutonomy()
	if level == autoUnattended {
		if why = m.level3Blocker(); why != "" {
			return autoAssisted, why
		}
	}
	return level, ""
}

func (m workspaceModel) maxAutoFixes() int {
	if v := m.liveCfg().Workspace.MaxAutoFixes; v > 0 {
		return v
	}
	return defaultMaxAutoFixes
}

// budgetReached reports whether the known cost has reached the budget.
func (m workspaceModel) budgetReached() bool {
	limit, _ := budgetOf(m.liveCfg(), m.sess.snapshot())
	return limit > 0 && m.use().spent >= limit
}

// autoSay writes a line to the conversation for an automatic action.
func (m *workspaceModel) autoSay(text string) { m.say("system", "auto › "+text) }

// autoActive is the level in force when automation may act now: above manual
// and not paused. The first time a lowered level 3 is met it says why.
func (m *workspaceModel) autoActive() int {
	level, why := m.autonomy()
	if why != "" && !m.autoWarned {
		m.autoWarned = true
		m.autoSay("level 3 " + why + "; behaving as level 2")
	}
	if level < autoAssisted || m.autoPaused {
		return autoManual
	}
	return level
}

// autoPause stops automation until the user runs something or sets /autonomy.
func (m *workspaceModel) autoPause() {
	if lvl, _ := m.autonomy(); lvl > autoManual && !m.autoPaused {
		m.autoPaused = true
		m.autoSay("paused; run a node (r, R) or set /autonomy to continue")
	}
}

// autoChip is the prompt chip of the level; "" at manual.
func (m workspaceModel) autoChip() string {
	lvl, _ := m.autonomy()
	if lvl < autoAssisted {
		return ""
	}
	chip := fmt.Sprintf("L%d %s", lvl, autonomyName(lvl))
	if m.autoPaused {
		chip += " · auto paused"
	}
	return chip
}

// unassigned counts the work nodes that wait for a role.
func unassignedWork(g *workspace.Graph) int {
	n := 0
	for _, x := range g.Nodes {
		if x.Kind == workspace.KindWork && x.Role == "" && x.Status == workspace.StatusTodo {
			n++
		}
	}
	return n
}

func asWorkspace(next tea.Model, cmd tea.Cmd, fallback workspaceModel) (workspaceModel, tea.Cmd) {
	if wm, ok := next.(workspaceModel); ok {
		return wm, cmd
	}
	return fallback, cmd
}

// acceptRoles is the A key: it assigns every suggested role that is defined.
func (m workspaceModel) acceptRoles() workspaceModel {
	next, cmd := m.acceptAll()
	m, _ = asWorkspace(next, cmd, m)
	return m
}

// autoAfterGraph runs when new nodes become available (a split, an import):
// at level 2 and up it accepts the suggested roles that are defined and starts
// the run.
func (m workspaceModel) autoAfterGraph() (workspaceModel, tea.Cmd) {
	if m.eng == nil || m.autoActive() == autoManual || m.overlay == wsOverlayConfirm {
		return m, nil
	}
	if m.budgetReached() {
		m.autoSay("budget reached, the run was not started")
		m.notifyNote(notify.NeedsYou, "Budget reached: the run was not started")
		return m, nil
	}
	m.g = m.eng.Snapshot()
	var wait bool
	if m, wait = m.autoApproveAgents(); wait {
		return m, nil
	}
	before := unassignedWork(m.g)
	m = m.acceptRoles()
	left := unassignedWork(m.g)
	accepted := before - left
	if len(m.g.Runnable()) == 0 {
		m.autoSay(fmt.Sprintf("accepted %d roles, nothing can run yet: %d nodes need a role", accepted, left))
		m.notifyNote(notify.NeedsYou, fmt.Sprintf("%d nodes need a role", left))
		return m, nil
	}
	next, cmd := m.runAll()
	m, cmd = asWorkspace(next, cmd, m)
	note := fmt.Sprintf("accepted %d roles and started the run", accepted)
	if left > 0 {
		note += fmt.Sprintf("; %d nodes have no defined role and wait for you (a)", left)
		m.notifyNote(notify.NeedsYou, fmt.Sprintf("%d nodes need a role", left))
	}
	m.autoSay(note)
	return m, cmd
}

// autoAfterEvent reacts to the Build & test node: a failure adds a fix node
// and runs it (level 2), a pass opens the pull request (level 3).
func (m workspaceModel) autoAfterEvent(ev workspace.Event) (workspaceModel, tea.Cmd) {
	if m.eng == nil || m.g == nil {
		return m, nil
	}
	if ev.Type == workspace.EventBudgetReached {
		if lvl, _ := m.autonomy(); lvl > autoManual {
			m.autoSay("budget reached, automation stops")
		}
		return m, nil
	}
	m = m.autoCompact(ev)
	n := m.g.Node(ev.NodeID)
	if n == nil || n.Kind != workspace.KindCheck || (ev.Type != workspace.EventDone && ev.Type != workspace.EventFailed) {
		return m, nil
	}
	if ev.Type == workspace.EventFailed && ev.Text == "stopped" {
		return m, nil
	}
	lvl := m.autoActive()
	if lvl == autoManual {
		return m, nil
	}
	if ev.Type == workspace.EventFailed {
		return m.autoFix(n.ID)
	}
	if n.Skipped {
		return m, nil // nothing changed: nothing to open a pull request for
	}
	if lvl == autoUnattended {
		return m.autoPR()
	}
	return m, nil
}

// doneWork counts the finished work and fix nodes.
func doneWork(g *workspace.Graph) int {
	n := 0
	for _, x := range g.Nodes {
		if x.Kind == workspace.KindWork && x.Status == workspace.StatusDone {
			n++
		}
	}
	return n
}

// autoCompact folds the finished nodes once their number reaches
// autoCompactAt, at level 2 and up, when nothing runs. It fires once per
// crossing: it is armed again when the count drops below the threshold.
func (m workspaceModel) autoCompact(ev workspace.Event) workspaceModel {
	if ev.Type != workspace.EventDone && ev.Type != workspace.EventFailed {
		return m
	}
	if doneWork(m.g) < autoCompactAt {
		m.autoCompactFired = false
		return m
	}
	if m.autoCompactFired || m.running() > 0 || m.splitting || m.autoActive() == autoManual || m.budgetReached() {
		return m
	}
	m.autoCompactFired = true
	folded, _, err := m.applyCompact()
	if err != nil {
		m.autoSay("could not compact: " + oneLine(err.Error()))
		return m
	}
	if folded > 0 {
		m.autoSay(fmt.Sprintf("compacted %d finished %s", folded, pluralize(folded, "node", "nodes")))
	}
	return m
}

// autoFix adds one fix node for the failed check and runs it, up to
// workspace.max_auto_fixes per session.
func (m workspaceModel) autoFix(checkID string) (workspaceModel, tea.Cmd) {
	max, used := m.maxAutoFixes(), m.sess.snapshot().AutoFixes
	switch {
	case m.budgetReached():
		m.autoSay("Build & test failed and the budget is reached: no fix added")
		m.notifyNote(notify.Failed, "Build & test failed, budget reached")
		return m, nil
	case used >= max:
		m.autoSay(fmt.Sprintf("Build & test failed again and the %d automatic fixes are used up: fix it by hand", max))
		m.notifyNote(notify.Failed, "Build & test still failing, no automatic fixes left")
		return m, nil
	}
	id, err := m.eng.AddFix(checkID)
	if err != nil {
		m.autoSay("could not add a fix node: " + oneLine(err.Error()))
		return m, nil
	}
	m.sess.update(func(s *workspace.Session) { s.AutoFixes++ })
	m.saveSession()
	m.g = m.eng.Snapshot()
	m.sel = id
	m = m.acceptRoles()
	f := m.g.Node(id)
	if f == nil || f.Role == "" {
		m.autoSay(fmt.Sprintf("Build & test failed, added %s (%d/%d); assign a role to it (a) and press R", id, used+1, max))
		m.notifyNote(notify.NeedsYou, id+" needs a role")
		return m, nil
	}
	next, cmd := m.runAll()
	m, cmd = asWorkspace(next, cmd, m)
	m.autoSay(fmt.Sprintf("Build & test failed, added %s (%d/%d)", id, used+1, max))
	return m, cmd
}

// autoPR pushes the branch, opens or updates the pull request and watches it,
// with the fix nodes of the watch running without asking. It never merges.
func (m workspaceModel) autoPR() (workspaceModel, tea.Cmd) {
	if m.pr.watching || m.pr.working || m.autoNoChange() {
		return m, nil // an automation that changed nothing opens no pull request
	}
	if m.budgetReached() {
		m.autoSay("Build & test passed but the budget is reached: not pushing")
		m.notifyNote(notify.NeedsYou, "Build & test passed, budget reached: not pushing")
		return m, nil
	}
	m.pr.autoFix = true
	m.autoSay("Build & test passed, pushing the branch and opening the pull request")
	next, cmd := m.startPR(true)
	return asWorkspace(next, cmd, m)
}

// prAutoFix reports whether the fix nodes of /pr watch run without asking.
func (m workspaceModel) prAutoFix() bool {
	return m.cfg.Workspace.PRAutoFix || (m.pr.autoFix && !m.autoPaused)
}

// autoPRNote tells that the loop started by level 3 is watching.
func (m *workspaceModel) autoPRNote(st workspace.PRState) {
	if m.pr.autoFix {
		m.autoSay(fmt.Sprintf("opened PR #%d and started watching", st.Number))
	}
}

// cmdAutonomy is /autonomy [1|2|3].
func (m workspaceModel) cmdAutonomy(args string) (tea.Model, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		return m.openInfo("Autonomy", m.autonomyLines()), nil
	}
	v, ok := parseAutonomy(args)
	if !ok {
		m.say("error", autoUsage)
		return m, nil
	}
	if v == autoUnattended {
		if why := m.level3Blocker(); why != "" {
			m.say("error", "Level 3 "+why+". Using level 2 instead.")
			v = autoAssisted
		}
	}
	m.sess.update(func(s *workspace.Session) { s.Autonomy = v })
	m.autoPaused, m.autoWarned = false, false
	m.say("system", fmt.Sprintf("Autonomy of this session set to level %d, %s.", v, autonomyName(v)))
	m.saveSession()
	return m, nil
}

// autonomyLines is the body of /autonomy.
func (m workspaceModel) autonomyLines() []string {
	lvl, why := m.autonomy()
	cur := fmt.Sprintf("%d · %s", lvl, autonomyName(lvl))
	if m.autoPaused {
		cur += " (paused)"
	}
	src := "workspace.autonomy"
	if m.sess.snapshot().Autonomy > 0 {
		src = "this session, /autonomy"
	}
	lines := []string{kv("Level", cur), kv("Set by", src)}
	if why != "" {
		lines = append(lines, kv("Lowered", "level 3 "+why))
	}
	lines = append(lines, kv("Fixes", fmt.Sprintf("%d of %d automatic fixes used (workspace.max_auto_fixes)", m.sess.snapshot().AutoFixes, m.maxAutoFixes())),
		"",
		"1 · manual      nothing runs until you press r or R",
		"2 · assisted    after a split: accept the suggested roles and run;",
		"                a failed Build & test adds a fix node and runs it",
		"3 · unattended  level 2, and a passing Build & test pushes, opens",
		"                the pull request and watches it with auto-fix.",
		"                Needs a budget and worktree isolation.",
		"",
		"It never runs /merge and never merges the pull request.",
		"S pauses it until you run a node or set the level again.",
		"Change it with /autonomy 1, 2 or 3.")
	return lines
}
