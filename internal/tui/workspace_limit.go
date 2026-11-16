package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// limitRetryDelay is how long after the reset time a limited node is retried,
// so the provider has surely lifted the limit.
const limitRetryDelay = 30 * time.Second

// limitBusyDelay is the pause before an automatic retry is tried again after
// the engine could not start the node (parallel limit, budget).
const limitBusyDelay = 5 * time.Second

// limitCardLines is the most rows of the provider message on a card.
const limitCardLines = 2

// limitClock is the local reset time: "14:30", with the date ("Nov 17 14:30")
// when it is not today.
func limitClock(now, t time.Time) string {
	t = t.In(now.Location())
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// limitResets is "resets 14:30", or "reset time unknown".
func limitResets(now time.Time, l *workspace.LimitInfo) string {
	if l.ResetAt.IsZero() {
		return "reset time unknown"
	}
	return "resets " + limitClock(now, l.ResetAt)
}

// limitCardRows is the body of a limited node's card: the status line, the
// provider message and the retry hint.
func limitCardRows(n *workspace.Node, iw int, now time.Time, line, arrow string) []string {
	dim := lipgloss.NewStyle().Foreground(colMuted)
	warn := lipgloss.NewStyle().Foreground(colWarn)
	l := n.Limit
	rows := []string{warn.Render(truncateOneLine("◷ waiting · "+limitResets(now, l), iw)) + dim.Render(line+arrow)}
	if msg := strings.TrimSpace(oneLine(workspace.SanitizeText(l.Message))); msg != "" {
		lines := hardWrap(msg, iw)
		if len(lines) > limitCardLines {
			lines = lines[:limitCardLines]
			lines[limitCardLines-1] = truncateOneLine(lines[limitCardLines-1]+" …", iw)
		}
		for _, s := range lines {
			rows = append(rows, warn.Render(s))
		}
	}
	hint := "r retry · m reply"
	if l.Retries > 0 {
		hint = fmt.Sprintf("retry %d/%d · %s", l.Retries, workspace.MaxLimitRetries, hint)
	}
	return append(rows, dim.Render(truncateOneLine(hint, iw)))
}

// limitedRunners is, per runner, the latest reset time of its limited nodes
// (zero when unknown), sorted by runner name. Only limits that nodes hit are
// listed; nothing is asked from the providers.
func (m workspaceModel) limitedRunners() []limitedRunner {
	if m.g == nil {
		return nil
	}
	c := m.liveCfg()
	by := map[string]*limitedRunner{}
	for _, n := range m.g.Nodes {
		if !n.Limited() {
			continue
		}
		name := n.Limit.Provider
		if role, ok := workspace.FindRole(c, n.Role); ok && role.Agent.Runner != "" {
			name = runnerKey(role.Agent.Runner)
		}
		r := by[name]
		if r == nil {
			r = &limitedRunner{name: name}
			by[name] = r
		}
		if n.Limit.ResetAt.IsZero() {
			r.unknown = true
		} else if n.Limit.ResetAt.After(r.resetAt) {
			r.resetAt = n.Limit.ResetAt
		}
	}
	out := make([]limitedRunner, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

type limitedRunner struct {
	name    string
	resetAt time.Time
	unknown bool
}

// statusText is "limited until 14:30" ("limited" when a reset time is unknown).
func (r limitedRunner) statusText(now time.Time) string {
	if r.unknown || r.resetAt.IsZero() {
		return "limited"
	}
	return "limited until " + limitClock(now, r.resetAt)
}

// limitAutoRetries says whether a limited node is retried by itself: level 2
// or 3, not paused, budget left and a reset time known.
func (m workspaceModel) limitAutoRetries(n *workspace.Node) bool {
	if n.Limit == nil || n.Limit.ResetAt.IsZero() || m.autoPaused || m.budgetReached() {
		return false
	}
	lvl, _ := m.autonomy()
	return lvl >= autoAssisted
}

// notifyLimited tells the user a node hit a usage limit: NeedsYou when they
// have to retry, Info when decsr retries after the reset.
func (m workspaceModel) notifyLimited(ev workspace.Event) {
	if m.g == nil {
		return
	}
	n := m.g.Node(ev.NodeID)
	if n == nil || n.Limit == nil {
		return
	}
	now := m.now()
	l := n.Limit
	if m.limitAutoRetries(n) {
		m.notifyNote(notify.Info, fmt.Sprintf("%s waits for the %s limit — retrying after %s", n.ID, l.Provider, limitClock(now, l.ResetAt)))
		return
	}
	if l.ResetAt.IsZero() {
		m.notifyNote(notify.NeedsYou, fmt.Sprintf("%s hit the %s limit — reset time unknown, press r to retry", n.ID, l.Provider))
		return
	}
	m.notifyNote(notify.NeedsYou, fmt.Sprintf("%s hit the %s limit — resets %s, press r to retry", n.ID, l.Provider, limitClock(now, l.ResetAt)))
}

// limitNotice is the one-line notice shown when a node becomes limited.
func (m *workspaceModel) limitNotice(ev workspace.Event) {
	m.setNotice("◷ "+ev.NodeID+" hit a usage limit: "+oneLine(ev.Text), false)
}

// hasLimitedAuto reports whether some limited node will be retried by itself,
// which keeps the tick running.
func (m workspaceModel) hasLimitedAuto() bool {
	if m.g == nil {
		return false
	}
	for _, n := range m.g.Nodes {
		if n.Limited() && m.limitAutoRetries(n) {
			return true
		}
	}
	return false
}

// checkLimitRetries starts the automatic retry of every limited node whose
// reset time (plus limitRetryDelay) has passed. It does nothing at level 1,
// when paused, when the budget is reached or when the reset time is unknown.
func (m *workspaceModel) checkLimitRetries() {
	if m.eng == nil || m.g == nil || m.g.CountLimited() == 0 {
		return
	}
	now := m.now()
	var started bool
	for _, n := range m.g.Nodes {
		if !n.Limited() || n.Limit.ResetAt.IsZero() {
			continue
		}
		due := n.Limit.ResetAt.Add(limitRetryDelay)
		if now.Before(due) || now.Before(m.limitBackoff[n.ID]) {
			continue
		}
		if m.autoActive() == autoManual || m.budgetReached() {
			return
		}
		if n.Limit.Retries >= workspace.MaxLimitRetries {
			m.autoSay(fmt.Sprintf("%s used its %d retries after the %s limit; it fails", n.ID, workspace.MaxLimitRetries, n.Limit.Provider))
		} else {
			m.autoSay(fmt.Sprintf("%s retry %d/%d after the %s limit reset", n.ID, n.Limit.Retries+1, workspace.MaxLimitRetries, n.Limit.Provider))
		}
		if err := m.eng.RetryLimited(n.ID); err != nil {
			if m.limitBackoff == nil {
				m.limitBackoff = map[string]time.Time{}
			}
			m.limitBackoff[n.ID] = now.Add(limitBusyDelay)
			m.setNotice("! "+err.Error(), true)
			continue
		}
		started = true
	}
	if started {
		m.g = m.eng.Snapshot()
	}
}

// retryLimitedKey is r on a limited node: the user retries now. It does not
// count toward the automatic retries.
func (m *workspaceModel) retryLimitedKey(n *workspace.Node) {
	if err := m.eng.RetryLimitedNow(n.ID); err != nil {
		m.setNotice("! "+err.Error(), true)
		return
	}
	m.g = m.eng.Snapshot()
	m.setNotice("Retrying "+n.ID, false)
}

// limitStatusSegment renders "⬡ claude · limited until 14:30".
func (m workspaceModel) limitStatusSegment(r limitedRunner) string {
	text := runnerGlyph(r.name, m.cfg.UI.Icons) + " " + r.name + " · " + r.statusText(m.now())
	return lipgloss.NewStyle().Foreground(colWarn).Render(ansi.Truncate(text, max(m.width-4, 1), "…"))
}

func findLimited(list []limitedRunner, name string) (limitedRunner, bool) {
	for _, r := range list {
		if r.name == name {
			return r, true
		}
	}
	return limitedRunner{}, false
}

func usedRunner(u sessionUse, name string) bool {
	for _, r := range u.runners {
		if r.name == name {
			return true
		}
	}
	return false
}
