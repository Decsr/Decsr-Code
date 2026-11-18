package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsReviewDoneMsg is the result of a lead review call.
type wsReviewDoneMsg struct {
	gen   int
	round int
	res   workspace.ReviewResult
	usage runner.TokenUsage
	err   error
}

const reviewUsage = "usage: /review [y|n]  (no argument runs one lead review now; y applies and n rejects the waiting proposals)"

// coordinatorOn is whether the lead reviews each finished batch: the session's
// /coordinator, else workspace.coordinator.
func (m workspaceModel) coordinatorOn() bool {
	if v := m.sess.snapshot().Coordinator; v != nil {
		return *v
	}
	return m.liveCfg().Workspace.Coordinator
}

// pendingReview is the index of the review that waits for y or n; -1 for none.
func (m workspaceModel) pendingReview() int {
	rs := m.sess.snapshot().Reviews
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].State == workspace.ReviewPending {
			return i
		}
	}
	return -1
}

// coordAfterEvent marks the end of a node and starts the review when the
// batch is over.
func (m workspaceModel) coordAfterEvent(ev workspace.Event) (workspaceModel, tea.Cmd) {
	if m.eng == nil || m.g == nil || !m.coordinatorOn() {
		return m, nil
	}
	switch ev.Type {
	case workspace.EventDone, workspace.EventWaiting:
	case workspace.EventFailed:
		if ev.Text == "stopped" {
			return m, nil
		}
	default:
		return m, nil
	}
	m.reviewDirty = true
	return m.coordMaybeReview()
}

// coordMaybeReview runs one review when the batch has ended and nothing
// forbids it: nothing runs, the coordinator is on, automation is not paused,
// the budget is not reached and the round limit is not.
func (m workspaceModel) coordMaybeReview() (workspaceModel, tea.Cmd) {
	if !m.reviewDirty || m.eng == nil || m.reviewing || m.splitting || m.running() > 0 ||
		m.deps.review == nil || !m.coordinatorOn() || m.pendingReview() >= 0 {
		return m, nil
	}
	switch {
	case m.autoPaused:
		m.reviewDirty = false
		return m, nil
	case m.budgetReached():
		m.reviewDirty = false
		m.autoSay("budget reached, review skipped")
		return m, nil
	case m.eng.ReviewRounds() >= workspace.MaxReviewRounds:
		m.reviewDirty = false
		body := fmt.Sprintf("%d lead reviews done for this task — /coordinator now to continue", workspace.MaxReviewRounds)
		m.say("system", body)
		m.notifyNote(notify.NeedsYou, body)
		return m, nil
	}
	return m.startReview()
}

// lastRequest is the newest thing the user typed.
func (m workspaceModel) lastRequest() string {
	ls := m.sess.snapshot().Transcript
	for i := len(ls) - 1; i >= 0; i-- {
		if ls[i].Who == "you" {
			return ls[i].Text
		}
	}
	return ""
}

// startReview asks the lead to review the graph now.
func (m workspaceModel) startReview() (workspaceModel, tea.Cmd) {
	round := m.eng.StartReview()
	m.reviewDirty = false
	m.reviewing = true
	m.reviewGen++
	gen := m.reviewGen
	ctx, cancel := context.WithCancel(m.runCtx)
	m.cancelReview = cancel
	g := m.eng.Snapshot()
	m.g = g
	task := g.Task
	if last := m.lastRequest(); last != "" && last != task {
		task += "\n\nLatest request: " + last
	}
	in := workspace.ReviewInput{Task: task, Summary: m.sess.snapshot().Summary, Graph: g,
		Roles: workspace.Roles(m.liveCfg()), Detail: m.eng.ActivityDetail}
	review := m.deps.review
	m.feedFollow = true
	cmd := func() tea.Msg {
		res, u, err := review(ctx, in)
		cancel()
		return wsReviewDoneMsg{gen: gen, round: round, res: res, usage: u, err: err}
	}
	return m, tea.Batch(cmd, m.startTick())
}

// stopReview drops a review that is running; its answer is ignored.
func (m *workspaceModel) stopReview() {
	if m.cancelReview != nil {
		m.cancelReview()
	}
	m.cancelReview = nil
	m.reviewing = false
	m.reviewGen++
}

// applyOpts are the options for applying proposals to the live graph.
func (m workspaceModel) applyOpts() *workspace.ApplyOptions {
	return &workspace.ApplyOptions{KnownRole: m.defined, NextID: max(m.sess.snapshot().NextID, nextWorkNumber(m.g))}
}

// commitApplied records what ApplyProposals did: the next number, the summary
// lines of cancelled nodes and a fresh snapshot.
func (m *workspaceModel) commitApplied(applied []workspace.AppliedProposal, opts *workspace.ApplyOptions) {
	m.sess.update(func(s *workspace.Session) {
		s.NextID = max(s.NextID, opts.NextID)
		s.Summary = workspace.CancelSummary(s.Summary, applied)
	})
	m.g = m.eng.Snapshot()
	if m.g.Node(m.sel) == nil && !strings.HasPrefix(m.sel, reviewKeyPrefix) {
		m.sel = ""
		if len(m.g.Nodes) > 0 {
			m.sel = m.g.Nodes[0].ID
		}
	}
}

// addReview keeps a review in the session, at most MaxReviews.
func (m *workspaceModel) addReview(r workspace.Review) {
	m.sess.update(func(s *workspace.Session) {
		s.Reviews = append(s.Reviews, r)
		if over := len(s.Reviews) - workspace.MaxReviews; over > 0 {
			s.Reviews = append([]workspace.Review(nil), s.Reviews[over:]...)
		}
	})
}

// setReview changes the review at index i.
func (m *workspaceModel) setReview(i int, f func(r *workspace.Review)) {
	m.sess.update(func(s *workspace.Session) {
		if i >= 0 && i < len(s.Reviews) {
			f(&s.Reviews[i])
		}
	})
}

func (m workspaceModel) onReviewDone(msg wsReviewDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.reviewGen || !m.reviewing {
		return m, nil
	}
	m.reviewing, m.cancelReview = false, nil
	m.sess.update(func(s *workspace.Session) {
		s.In += msg.usage.InputTokens
		s.Out += msg.usage.OutputTokens
	})
	if msg.err != nil {
		m.say("error", "lead review failed: "+oneLine(workspace.SanitizeText(msg.err.Error())))
		m.saveSession()
		return m, nil
	}
	if m.eng == nil {
		return m, nil
	}
	rv := workspace.Review{Round: msg.round, At: m.now(), Note: msg.res.Note, Proposals: msg.res.Proposals, State: workspace.ReviewEmpty}
	m.g = m.eng.Snapshot()
	opts := m.applyOpts()
	auto := m.autoActive() != autoManual
	var cmd tea.Cmd
	if auto {
		rv.Applied, rv.Skipped = m.eng.ApplyProposals(rv.Proposals, opts)
		if len(rv.Applied) > 0 {
			rv.State = workspace.ReviewApplied
		}
		m.commitApplied(rv.Applied, opts)
		m.addReview(rv)
		for _, a := range rv.Applied {
			m.autoSay("lead: " + a.Text)
		}
		if len(rv.Applied) > 0 {
			m, cmd = m.autoAfterGraph()
		}
	} else {
		rv.Applied, rv.Skipped = m.eng.PreviewProposals(rv.Proposals, opts)
		if len(rv.Applied) > 0 {
			rv.State = workspace.ReviewPending
			m.say("system", fmt.Sprintf("lead review %d: %d %s proposed — y applies, n rejects",
				rv.Round, len(rv.Applied), pluralize(len(rv.Applied), "change", "changes")))
			m.notifyNote(notify.NeedsYou, fmt.Sprintf("Lead review: %d %s proposed — y applies, n rejects",
				len(rv.Applied), pluralize(len(rv.Applied), "change", "changes")))
		}
		m.addReview(rv)
	}
	m.feedFollow = true
	m.saveSession()
	return m, cmd
}

// reviewResolve is y or n on the waiting review.
func (m workspaceModel) reviewResolve(apply bool) (workspaceModel, tea.Cmd) {
	i := m.pendingReview()
	if i < 0 || m.eng == nil {
		m.setNotice("No lead review is waiting for an answer", true)
		return m, nil
	}
	rv := m.sess.snapshot().Reviews[i]
	m.reviewDirty = false
	if !apply {
		m.setReview(i, func(r *workspace.Review) { r.State = workspace.ReviewRejected })
		m.say("system", fmt.Sprintf("lead review %d rejected", rv.Round))
		m.saveSession()
		return m, nil
	}
	m.g = m.eng.Snapshot()
	opts := m.applyOpts()
	applied, skipped := m.eng.ApplyProposals(rv.Proposals, opts)
	state := workspace.ReviewApplied
	if len(applied) == 0 {
		state = workspace.ReviewEmpty
	}
	m.setReview(i, func(r *workspace.Review) { r.Applied, r.Skipped, r.State = applied, skipped, state })
	m.commitApplied(applied, opts)
	switch {
	case len(applied) == 0:
		m.say("error", fmt.Sprintf("lead review %d: nothing could be applied any more", rv.Round))
	default:
		line := fmt.Sprintf("lead review %d applied: %d %s", rv.Round, len(applied), pluralize(len(applied), "change", "changes"))
		if len(skipped) > 0 {
			line += fmt.Sprintf(", %d skipped", len(skipped))
		}
		m.say("system", line)
	}
	m.saveSession()
	return m, nil
}

// cmdReview is /coordinator now; y and n apply or reject the waiting proposals.
func (m workspaceModel) cmdReview(args string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "y", "yes", "apply":
		return m.reviewResolve(true)
	case "n", "no", "reject":
		return m.reviewResolve(false)
	case "":
	default:
		m.say("error", reviewUsage)
		return m, nil
	}
	switch {
	case m.eng == nil || !m.hasNodes():
		m.say("system", "nothing to review: there is no graph yet")
	case m.deps.review == nil:
		m.say("error", "the lead is not available for a review")
	case m.reviewing:
		m.say("error", "a lead review is already running")
	case m.splitting:
		m.say("error", "the lead is splitting a task")
	case m.busy():
		return m.refuseBusy()
	case m.pendingReview() >= 0:
		m.say("error", "answer the waiting lead review first: y applies it, n rejects it")
	case m.budgetReached():
		m.say("error", "budget reached: the review was not started")
	default:
		return m.startReview()
	}
	return m, nil
}

// cmdCoordinator is /coordinator [on|off].
func (m workspaceModel) cmdCoordinator(args string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "":
		src := "workspace.coordinator"
		if m.sess.snapshot().Coordinator != nil {
			src = "this session, /coordinator"
		}
		state := "off"
		if m.coordinatorOn() {
			state = "on"
		}
		m.say("system", fmt.Sprintf("Coordinator is %s (set by %s). When on, the lead reviews each finished batch and proposes changes. /coordinator on or off changes it for this session.", state, src))
		return m, nil
	case "on", "off":
		on := strings.EqualFold(strings.TrimSpace(args), "on")
		m.sess.update(func(s *workspace.Session) { s.Coordinator = &on })
		m.reviewDirty = false
		word := "off"
		if on {
			word = "on"
		}
		m.say("system", "Coordinator of this session is "+word+".")
		m.saveSession()
		return m, nil
	}
	m.say("error", "usage: /coordinator [on|off]")
	return m, nil
}
