package workspace

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// MaxLimitRetries is how many automatic retries a limited node gets before it
// fails with the limit message.
const MaxLimitRetries = 3

// LimitInfo says that a waiting node stopped because its provider limited
// usage. The node is retried after ResetAt (see Engine.RetryLimited).
type LimitInfo struct {
	Provider string    `json:"provider"`
	ResetAt  time.Time `json:"reset_at,omitempty"` // zero when the provider gave no reset time
	Message  string    `json:"message,omitempty"`
	// Retries counts the automatic retries used so far. It survives the
	// attempts of the limit loop and is cleared when a run succeeds or fails
	// for another reason, or when the user replies to the node.
	Retries int `json:"retries,omitempty"`
}

// Limited reports whether n is a waiting node that hit a usage limit (as
// opposed to one that waits for an answer).
func (n *Node) Limited() bool { return n.Status == StatusWaiting && n.Limit != nil }

// CountLimited returns how many nodes wait for a usage limit to lift.
func (g *Graph) CountLimited() int {
	k := 0
	for _, n := range g.Nodes {
		if n.Limited() {
			k++
		}
	}
	return k
}

// limitMessage is the text shown for a rate-limit error.
func limitMessage(rl *runner.RateLimitError) string {
	if rl.Detail != "" {
		return rl.Detail
	}
	return rl.Error()
}

// limitFailureText is the Error of a node that used up its automatic retries.
func limitFailureText(l *LimitInfo) string {
	return fmt.Sprintf("limit reached on %s: %s (%d automatic retries used)", l.Provider, l.Message, MaxLimitRetries)
}

// asRateLimit finds a rate-limit error in err, however it is wrapped.
func asRateLimit(err error) *runner.RateLimitError {
	var rl *runner.RateLimitError
	if errors.As(err, &rl) {
		return rl
	}
	return nil
}

// Limited returns a snapshot of the nodes that wait for a usage limit.
func (e *Engine) Limited() []*Node {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*Node
	for _, n := range e.g.Clone().Nodes {
		if n.Limited() {
			out = append(out, n)
		}
	}
	return out
}

// RetryLimited starts a limited node again after its limit lifted. It is the
// automatic retry: it counts toward MaxLimitRetries, and the node fails with
// the limit message instead of starting once those are used up. It is a retry,
// not a reply: the attempt number does not change.
func (e *Engine) RetryLimited(id string) error { return e.retryLimited(id, true) }

// RetryLimitedNow is the retry the user asks for (r). It does not count toward
// the automatic retries.
func (e *Engine) RetryLimitedNow(id string) error { return e.retryLimited(id, false) }

func (e *Engine) retryLimited(id string, auto bool) error {
	e.mu.Lock()
	n := e.g.Node(id)
	switch {
	case n == nil:
		e.mu.Unlock()
		return fmt.Errorf("unknown node %s", id)
	case !n.Limited():
		e.mu.Unlock()
		return fmt.Errorf("node %s is not waiting for a usage limit", id)
	}
	if auto && n.Limit.Retries >= MaxLimitRetries {
		n.Status = StatusFailed
		n.Error = limitFailureText(n.Limit)
		n.Limit = nil
		e.pending = append(e.pending, Event{NodeID: id, Type: EventFailed, Text: n.Error})
		err := e.saveLocked()
		e.mu.Unlock()
		e.reportSave(err)
		e.flush()
		return nil
	}
	if reason := e.cannotStartLocked(n); reason != "" {
		e.mu.Unlock()
		return fmt.Errorf("node %s cannot retry yet: %s", id, reason)
	}
	if auto {
		n.Limit.Retries++
	}
	if e.resumeAuto {
		e.auto, e.autoCtx, e.resumeAuto = true, e.baseContext(), false
	}
	s := e.startLocked(e.baseContext(), n)
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	e.launch(s)
	return nil
}

// limitedText is the first line of a limited node for lists and the PR body.
func limitedText(n *Node) string {
	return strings.TrimSpace(fmt.Sprintf("%s limit: %s", n.Limit.Provider, n.Limit.Message))
}
