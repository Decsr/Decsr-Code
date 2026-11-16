package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsPRState is the pull request loop of the open session. It is replaced as
// a whole (with a higher gen) whenever the loop ends, so messages of an
// earlier loop are recognised and dropped.
type wsPRState struct {
	watching  bool
	working   bool // a /pr command or a poll is in flight
	gen       int
	passedSHA string // head commit whose passing checks were announced
}

// wsPRMsg is every asynchronous result of the pull request loop.
type wsPRMsg struct {
	kind    string // "ensured" | "tick" | "poll" | "pushed"
	gen     int
	session string
	watch   bool // ensured: start watching afterwards
	res     workspace.PRResult
	st      workspace.PRState
	poll    workspace.PollResult
	node    string // pushed: the fix node that triggered the push
	err     error
}

func (m workspaceModel) prClient() *workspace.PRClient {
	w := m.cfg.Workspace
	return &workspace.PRClient{
		Root: m.root, Session: m.sess.snapshot().ID, Run: m.deps.ghRun,
		Cfg: workspace.PRConfig{Base: w.PRBase, Remote: w.PRRemote, PollSeconds: w.PRPollSeconds, MaxCIFixes: w.MaxCIFixes},
	}
}

// prStop ends the loop and invalidates every message still on its way.
func (m *workspaceModel) prStop() { m.pr = wsPRState{gen: m.pr.gen + 1} }

func (m workspaceModel) stale(msg wsPRMsg) bool {
	return msg.gen != m.pr.gen || msg.session != m.sess.snapshot().ID
}

// cmdPR is /pr, /pr watch and /pr stop.
func (m workspaceModel) cmdPR(args string) (tea.Model, tea.Cmd) {
	switch strings.TrimSpace(args) {
	case "stop":
		if !m.pr.watching {
			m.say("system", "Not watching a pull request.")
			return m, nil
		}
		m.prStop()
		m.say("system", "Stopped watching the pull request.")
		return m, nil
	case "watch":
		if m.pr.watching {
			m.prStop()
			m.say("system", "Stopped watching the pull request.")
			return m, nil
		}
		return m.startPR(true)
	case "":
		return m.startPR(false)
	}
	m.say("error", "usage: /pr [watch|stop]")
	return m, nil
}

// startPR pushes the session branch and creates or finds its pull request.
func (m workspaceModel) startPR(watch bool) (tea.Model, tea.Cmd) {
	switch {
	case m.iso == nil:
		m.say("error", "/pr needs worktree isolation: this session works in your checkout directly (not a git repository with a commit, or workspace.isolation is \"shared\")")
		return m, nil
	case m.busy():
		return m.refuseBusy()
	case m.pr.working:
		m.say("error", "already working on the pull request")
		return m, nil
	}
	m.pr.working = true
	m.setNotice("Pushing "+m.iso.Branch()+"…", false)
	c, sess, gen := m.prClient(), m.sess.snapshot(), m.pr.gen
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		t, err := c.Prepare(ctx)
		if err != nil {
			return wsPRMsg{kind: "ensured", gen: gen, session: sess.ID, err: err}
		}
		res, err := c.EnsurePR(ctx, t, sess)
		return wsPRMsg{kind: "ensured", gen: gen, session: sess.ID, watch: watch, res: res, err: err}
	}
}

func (m workspaceModel) onPRMsg(msg wsPRMsg) (tea.Model, tea.Cmd) {
	if m.stale(msg) {
		return m, nil
	}
	switch msg.kind {
	case "ensured":
		m.pr.working = false
		if msg.err != nil {
			m.say("error", oneLine(msg.err.Error()))
			return m, nil
		}
		st := msg.res.State
		m.sess.update(func(s *workspace.Session) { s.PR = &st })
		m.saveSession()
		if msg.res.Created {
			m.say("system", fmt.Sprintf("Pull request #%d created: %s", st.Number, st.URL))
		} else {
			m.say("system", fmt.Sprintf("Pushed %s to pull request #%d: %s", m.iso.Branch(), st.Number, st.URL))
		}
		if !msg.watch {
			return m, nil
		}
		m.pr.watching = true
		m.pr.gen++
		m.say("system", fmt.Sprintf("Watching the pull request every %d s: failing CI and review comments become fix nodes. /pr stop ends it.", int(m.prClient().Cfg.PollInterval()/time.Second)))
		return m, m.prPoll()
	case "tick":
		if !m.pr.watching {
			return m, nil
		}
		return m, m.prPoll()
	case "poll":
		m.pr.working = false
		if !m.pr.watching {
			return m, nil
		}
		next := m.prTick()
		if msg.err != nil {
			m.setNotice("pull request poll failed: "+oneLine(msg.err.Error()), true)
			return m, next
		}
		m.sess.update(func(s *workspace.Session) { s.PR = &msg.st })
		m, cmd := m.applyPoll(msg.st, msg.poll)
		m.saveSession()
		return m, tea.Batch(next, cmd)
	case "pushed":
		if msg.err != nil {
			m.say("error", "could not push the fix of "+msg.node+": "+oneLine(msg.err.Error()))
			return m, nil
		}
		m.say("system", fmt.Sprintf("Pushed the fix of %s to %s: CI runs again.", msg.node, m.iso.Branch()))
	}
	return m, nil
}

// prTick schedules the next poll of the loop that is running now.
func (m workspaceModel) prTick() tea.Cmd {
	gen, sid := m.pr.gen, m.sess.snapshot().ID
	return tea.Tick(m.prClient().Cfg.PollInterval(), func(time.Time) tea.Msg {
		return wsPRMsg{kind: "tick", gen: gen, session: sid}
	})
}

// prPoll asks GitHub once; nil when a poll is already out.
func (m *workspaceModel) prPoll() tea.Cmd {
	if m.pr.working {
		return nil
	}
	s := m.sess.snapshot()
	if s.PR == nil {
		return nil
	}
	m.pr.working = true
	c, st, gen := m.prClient(), *s.PR, m.pr.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		st, res, err := c.Poll(ctx, st)
		return wsPRMsg{kind: "poll", gen: gen, session: s.ID, st: st, poll: res, err: err}
	}
}

// applyPoll reports what a poll found and adds the fix nodes it asks for.
func (m workspaceModel) applyPoll(st workspace.PRState, res workspace.PollResult) (workspaceModel, tea.Cmd) {
	var cmds []tea.Cmd
	if res.Passed && res.HeadSHA != m.pr.passedSHA {
		m.pr.passedSHA = res.HeadSHA
		m.say("system", fmt.Sprintf("Pull request #%d: all checks passed.", st.Number))
		m.notifyNote(notify.Done, "PR checks passed")
	}
	max := m.prClient().Cfg.MaxFixes()
	if f := res.CIFix; f != nil {
		var cmd tea.Cmd
		m, cmd = m.addPRFix(*f, fmt.Sprintf("CI failed (%s), fix round %d of %d.", strings.Join(res.Failed, ", "), st.CIFixes, max))
		cmds = append(cmds, cmd)
		m.notifyNote(notify.Failed, "CI failed: "+strings.Join(res.Failed, ", "))
	}
	if res.Exhausted {
		m.say("error", fmt.Sprintf("CI fails again (%s) and the %d allowed fix rounds are used up: no more fix nodes are added. Fix it by hand or raise workspace.max_ci_fixes.", strings.Join(res.Failed, ", "), max))
		m.notifyNote(notify.Failed, "CI still failing, no fix rounds left")
	}
	if f := res.ReviewFix; f != nil {
		var cmd tea.Cmd
		m, cmd = m.addPRFix(*f, "New review comments.")
		cmds = append(cmds, cmd)
		m.notifyNote(notify.NeedsYou, "Review comments on the pull request")
	}
	return m, tea.Batch(cmds...)
}

// addPRFix adds a fix node for the loop and runs it when workspace.pr_auto_fix
// is on.
func (m workspaceModel) addPRFix(f workspace.FixRequest, why string) (workspaceModel, tea.Cmd) {
	if m.eng == nil {
		m.say("error", why+" There is no graph to add a fix node to.")
		return m, nil
	}
	role := ""
	if m.defined("coder_fixer") {
		role = "coder_fixer"
	}
	id := m.eng.AddCIFix(f.Title, f.Desc, role)
	m.g = m.eng.Snapshot()
	m.sel = id
	switch {
	case role == "":
		m.say("system", fmt.Sprintf("%s Added %s; assign a role to it (a), then press r to run.", why, id))
	case m.cfg.Workspace.PRAutoFix:
		if err := m.eng.Run(m.runCtx, id); err != nil {
			m.say("system", fmt.Sprintf("%s Added %s; it could not start (%s): press r to run.", why, id, oneLine(err.Error())))
			return m, nil
		}
		m.g = m.eng.Snapshot()
		m.say("system", fmt.Sprintf("%s Added %s and started it.", why, id))
		return m, m.startTick()
	default:
		m.say("system", fmt.Sprintf("%s Added %s: press r to run it.", why, id))
	}
	return m, nil
}

// prAfterEvent pushes the branch when a fix node finishes while watching, so
// CI runs on the fix.
func (m workspaceModel) prAfterEvent(ev workspace.Event) tea.Cmd {
	if ev.Type != workspace.EventDone || !m.pr.watching || !workspace.IsFixID(ev.NodeID) {
		return nil
	}
	s := m.sess.snapshot()
	if s.PR == nil {
		return nil
	}
	c, gen, id := m.prClient(), m.pr.gen, ev.NodeID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		t, err := c.Prepare(ctx)
		if err == nil {
			err = c.Push(ctx, t)
		}
		return wsPRMsg{kind: "pushed", gen: gen, session: s.ID, node: id, err: err}
	}
}
