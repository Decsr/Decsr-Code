package workspace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ReplyState says what a reply did.
type ReplyState int

const (
	// ReplyStarted: a new attempt of the node is running.
	ReplyStarted ReplyState = iota
	// ReplyQueued: the node is running; the reply starts a new attempt when
	// the run ends.
	ReplyQueued
	// ReplyWaiting: the node is ready for its new attempt but cannot start yet
	// (see ReplyResult.Reason); it starts like any todo node.
	ReplyWaiting
)

// ReplyResult is the outcome of Engine.Reply.
type ReplyResult struct {
	State   ReplyState
	Attempt int    // number of the attempt the reply starts (or will start)
	Reason  string // why a waiting node does not run yet
}

// joinReply appends msg to an earlier message with a blank line between.
func joinReply(prev, msg string) string {
	if prev == "" {
		return msg
	}
	return prev + "\n\n" + msg
}

// Reply sends msg to a work or fix node. A node that is running gets the reply
// queued: it starts the next attempt as soon as the run ends, however it ends
// (a stop keeps the reply). A node that is done, failed or waiting keeps its history as
// an earlier attempt and starts attempt N+1 now, or waits when its
// dependencies, role, the parallel limit or the budget do not allow it yet. A
// node that never ran keeps msg as the user's message for its first run. A
// Build & test node that was done or failed is reset to todo, as AddFix does.
func (e *Engine) Reply(id, msg string) (ReplyResult, error) {
	msg = strings.TrimSpace(msg)
	e.mu.Lock()
	n := e.g.Node(id)
	switch {
	case n == nil:
		e.mu.Unlock()
		return ReplyResult{}, fmt.Errorf("unknown node %s", id)
	case n.Kind != KindWork:
		e.mu.Unlock()
		return ReplyResult{}, errors.New("reply to a work or fix node")
	case msg == "":
		e.mu.Unlock()
		return ReplyResult{}, errors.New("the reply is empty")
	}
	var res ReplyResult
	var s *started
	var notes []Event
	switch n.Status {
	case StatusRunning:
		n.PendingReply = joinReply(n.PendingReply, msg)
		res = ReplyResult{State: ReplyQueued, Attempt: n.AttemptNumber() + 1}
	case StatusTodo:
		n.Reply = joinReply(n.Reply, msg)
		res, s = e.startReplyLocked(n)
	default:
		res, s, notes = e.applyReplyLocked(n, joinReply(n.PendingReply, msg))
	}
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	e.flush()
	for _, ev := range notes {
		e.emitOutput(ev)
	}
	if s != nil {
		e.launch(*s)
	}
	return res, nil
}

// applyReplyLocked turns a done or failed node into the next attempt with
// message msg and starts it when it can. It returns the notes to send as
// output events once the lock is released.
func (e *Engine) applyReplyLocked(n *Node, msg string) (ReplyResult, *started, []Event) {
	var notes []Event
	if n.Status == StatusDone {
		var stale []string
		for _, o := range e.g.Nodes {
			if o.Kind != KindWork || o.Status == StatusTodo {
				continue
			}
			for _, d := range o.DependsOn {
				if d == n.ID {
					stale = append(stale, o.ID)
					break
				}
			}
		}
		if len(stale) > 0 {
			sort.Strings(stale)
			notes = append(notes, Event{NodeID: n.ID, Type: EventOutput,
				Text: fmt.Sprintf("%s depend on %s and were not re-run\n", strings.Join(stale, ", "), n.ID)})
		}
	}
	e.archiveLocked(n)
	n.PendingReply = ""
	n.Reply = msg
	n.Limit = nil
	n.Status = StatusTodo
	n.EndedAt = time.Time{}
	n.Error, n.Summary, n.Report = "", "", nil
	n.Files, n.Observed, n.FileStats, n.Commit, n.Warnings = nil, nil, nil, "", nil
	n.Activity, n.ActivityDropped = nil, 0
	n.ContextTokens, n.ContextLimit = 0, 0
	if c := e.g.Node("CHECK"); c != nil && c.Kind == KindCheck && (c.Status == StatusDone || c.Status == StatusFailed) {
		c.Status = StatusTodo
		c.Error = ""
	}
	res, s := e.startReplyLocked(n)
	if s != nil && len(notes) > 0 {
		// The start resets the output of the node: the notes follow it.
		e.startNotes[n.ID] = append(e.startNotes[n.ID], notes...)
		notes = nil
	}
	return res, s, notes
}

// archiveLocked moves the finished attempt of n into Attempts and numbers the
// next one. Only the most recent attempts keep their activity.
func (e *Engine) archiveLocked(n *Node) {
	cur := n.AttemptNumber()
	n.Attempts = append(n.Attempts, Attempt{
		N: cur, Reply: n.Reply, Status: n.Status, StartedAt: n.StartedAt, EndedAt: n.EndedAt,
		Summary: n.Summary, Report: cloneReport(n.Report), Error: n.Error,
		FileStats: append([]FileStat(nil), n.FileStats...), Commit: n.Commit,
		Activity: append(n.Activity[:0:0], n.Activity...), ActivityDropped: n.ActivityDropped,
	})
	for i := 0; i < len(n.Attempts)-maxAttemptActivity; i++ {
		n.Attempts[i].Activity, n.Attempts[i].ActivityDropped = nil, 0
	}
	n.Attempt = cur + 1
}

// startReplyLocked starts the todo node n when nothing prevents it. Otherwise it
// stays todo and the result says why.
func (e *Engine) startReplyLocked(n *Node) (ReplyResult, *started) {
	res := ReplyResult{Attempt: n.AttemptNumber()}
	if reason := e.cannotStartLocked(n); reason != "" {
		res.State, res.Reason = ReplyWaiting, reason
		return res, nil
	}
	s := e.startLocked(e.baseContext(), n)
	res.State = ReplyStarted
	return res, &s
}

// cannotStartLocked says why the todo node n cannot start now; "" when it can.
func (e *Engine) cannotStartLocked(n *Node) string {
	if !e.g.Assigned(n) {
		return "no role assigned"
	}
	if b := e.g.Blockers(n.ID); len(b) > 0 {
		return "blocked by " + strings.Join(b, ", ")
	}
	if e.stopping.Load() {
		return "the workspace is closing"
	}
	if len(e.cancels) >= e.max {
		return fmt.Sprintf("parallel limit reached (%d running)", len(e.cancels))
	}
	if msg, blocked := e.budgetBlockedLocked(); blocked {
		return msg
	}
	return ""
}

// baseContext is the context nodes started by a reply run under: the one of
// the latest start, else Background.
func (e *Engine) baseContext() context.Context {
	if e.parent != nil {
		return e.parent
	}
	return context.Background()
}
