package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	cardActivityRows = 5               // activity lines of a running card
	stallAfter       = time.Minute     // "no activity for 1m" shows from here
	stallNotifyAfter = 5 * time.Minute // one NeedsYou note per stall episode
	thinkingPeek     = 60              // first characters of a thought on its line
	actDetailLines   = 3               // wrapped detail lines of an expanded item in a card
)

// ── Formatting ───────────────────────────────────────────────────────────────

// actGlyph is the kind glyph of an activity item.
func actGlyph(k runner.ActivityKind) string {
	switch k {
	case runner.ActCommand:
		return "▸"
	case runner.ActEdit, runner.ActWrite:
		return "✎"
	case runner.ActRead:
		return "◦"
	case runner.ActSearch:
		return "⌕"
	case runner.ActTool:
		return "⚙"
	case runner.ActWaiting:
		return "◷"
	case runner.ActError:
		return "!"
	case runner.ActMessage:
		return "›"
	}
	return "·"
}

// actDuration is "3.2s" under ten seconds, else a compact duration.
func actDuration(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return feedDuration(d)
}

// ctxK is a token count as "18k" (one decimal under 10k, "512" under 1k).
func ctxK(n int) string {
	switch {
	case n >= 10_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	case n >= 1000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
	}
	return fmt.Sprintf("%d", n)
}

// ctxLabel is "ctx 18k" or "ctx 18k / 128k"; "" when the size is unknown.
func ctxLabel(n *workspace.Node) string {
	if n.ContextTokens <= 0 {
		return ""
	}
	s := "ctx " + ctxK(n.ContextTokens)
	if n.ContextLimit > 0 {
		s += " / " + ctxK(n.ContextLimit)
	}
	return s
}

// midPath shortens a path to w cells from the left at a separator when it can:
// "…/internal/x.go".
func midPath(p string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(p) <= w {
		return p
	}
	if w <= 2 {
		return tailPath(p, w)
	}
	tail := ansi.TruncateLeft(p, ansi.StringWidth(p)-(w-2), "")
	if i := strings.IndexByte(tail, '/'); i >= 0 && i+1 < len(tail) {
		return "…/" + tail[i+1:]
	}
	return tailPath(p, w)
}

// clipEnd cuts s to w cells with "…" at the end.
func clipEnd(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return oneLine(workspace.SanitizeText(s))
}

// actText is the text of an item without glyph and meta, cut to w cells.
// compact drops the verbs ("Edited") and quotes.
func actText(a runner.Activity, w int, compact bool) string {
	title := oneLine(workspace.SanitizeText(a.Title))
	switch a.Kind {
	case runner.ActEdit, runner.ActWrite:
		verb := "Edited "
		if a.Kind == runner.ActWrite {
			verb = "Added "
		}
		if compact {
			verb = ""
		}
		return verb + midPath(title, w-len(verb))
	case runner.ActRead:
		verb := "Read "
		if compact {
			verb = ""
		}
		return verb + midPath(title, w-len(verb))
	case runner.ActSearch:
		if compact {
			return clipEnd(title, w)
		}
		return clipEnd(`Searched "`+title+`"`, w)
	case runner.ActWaiting:
		if compact {
			return clipEnd("waiting", w)
		}
		return clipEnd("Waiting · "+title, w)
	case runner.ActThinking:
		peek := firstLine(a.Detail)
		if peek == "" {
			peek = title
		}
		if compact || peek == "" {
			return clipEnd("thinking…", w)
		}
		if utf8.RuneCountInString(peek) > thinkingPeek {
			peek = string([]rune(peek)[:thinkingPeek-1]) + "…"
		}
		return clipEnd(`thinking… "`+peek+`"`, w)
	case runner.ActMessage:
		t := title
		if t == "" {
			t = firstLine(a.Detail)
		}
		return clipEnd(t, w)
	}
	return clipEnd(title, w)
}

// actMeta is the right-aligned meta of an item and its colours: plain text
// and the rendered form.
func actMeta(a runner.Activity, now time.Time) (plain, styled string) {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	ok := lipgloss.NewStyle().Foreground(ui.OK)
	fail := lipgloss.NewStyle().Foreground(ui.Fail)
	switch a.Kind {
	case runner.ActEdit, runner.ActWrite:
		var pp, ps []string
		if a.Added > 0 {
			pp = append(pp, fmt.Sprintf("+%d", a.Added))
			ps = append(ps, ok.Render(fmt.Sprintf("+%d", a.Added)))
		}
		if a.Removed > 0 {
			pp = append(pp, fmt.Sprintf("−%d", a.Removed))
			ps = append(ps, fail.Render(fmt.Sprintf("−%d", a.Removed)))
		}
		if len(pp) > 0 {
			return strings.Join(pp, " "), strings.Join(ps, " ")
		}
		if a.State == runner.ActFailed {
			return "✗", fail.Render("✗")
		}
		return "", ""
	case runner.ActCommand:
		switch a.State {
		case runner.ActRunning:
			if a.At.IsZero() {
				return "", ""
			}
			s := feedDuration(now.Sub(a.At))
			return s, dim.Render(s)
		case runner.ActFailed:
			s := "✗"
			if a.ExitCode != nil {
				s += fmt.Sprintf(" exit %d", *a.ExitCode)
			}
			if a.Duration > 0 {
				s += " · " + actDuration(a.Duration)
			}
			return s, fail.Render(s)
		}
		s := "✓"
		if a.Duration > 0 {
			s += " " + actDuration(a.Duration)
		}
		return s, dim.Render(s)
	}
	if a.State == runner.ActFailed {
		return "✗", fail.Render("✗")
	}
	return "", ""
}

// actColor is the colour of the glyph and text of an item.
func actColor(a runner.Activity) lipgloss.TerminalColor {
	switch {
	case a.Kind == runner.ActError || a.State == runner.ActFailed:
		return ui.Fail
	case a.Kind == runner.ActThinking || a.Kind == runner.ActMessage:
		return ui.Muted
	case a.State == runner.ActRunning:
		return ui.Accent
	}
	return ui.Text
}

// activityLine renders one item as glyph, text and right-aligned dim meta in
// at most w cells (exactly w when there is meta).
func activityLine(a runner.Activity, now time.Time, w int) string {
	if w < 3 {
		return clipEnd(actGlyph(a.Kind), w)
	}
	plain, styled := actMeta(a, now)
	mw := ansi.StringWidth(plain)
	if mw > 0 && w-2-mw-2 < 8 { // no room for the text: drop the meta
		styled, mw = "", 0
	}
	room := w - 2
	if mw > 0 {
		room -= mw + 1
	}
	text := actText(a, room, false)
	col := actColor(a)
	st := lipgloss.NewStyle().Foreground(col)
	if a.Kind == runner.ActThinking || a.Kind == runner.ActMessage {
		st = lipgloss.NewStyle().Foreground(ui.Muted)
	}
	left := st.Render(actGlyph(a.Kind)) + " " + st.Render(text)
	if mw == 0 {
		return left
	}
	gap := max(w-ansi.StringWidth(left)-mw, 1)
	return left + strings.Repeat(" ", gap) + styled
}

// activityDetailLines is the wrapped detail of an item, indented by four.
func activityDetailLines(detail string, w, limit int) []string {
	detail = strings.TrimSpace(workspace.SanitizeText(detail))
	if detail == "" || w < 8 {
		return nil
	}
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	var out []string
	for _, l := range hardWrap(detail, w-4) {
		if limit > 0 && len(out) == limit {
			out[len(out)-1] = dim.Render("    " + clipEnd(strings.TrimSpace(l)+" …", w-4))
			break
		}
		out = append(out, dim.Render("    "+l))
	}
	return out
}

// activityMessageLines is the detail of a finished message item as Markdown,
// indented by four and cut to limit lines; it falls back to activityDetailLines
// when Markdown is off or the text is still running.
func activityMessageLines(detail string, w, limit int, running bool) []string {
	detail = strings.TrimSpace(workspace.SanitizeText(detail))
	if running || detail == "" {
		return activityDetailLines(detail, w, limit)
	}
	md, ok := renderMarkdown(detail, w-4)
	if !ok {
		return activityDetailLines(detail, w, limit)
	}
	if limit > 0 && len(md) > limit {
		md = md[:limit]
		md[limit-1] = ansi.Truncate(md[limit-1], max(w-6, 1), "") + lipgloss.NewStyle().Foreground(ui.Muted).Render(" …")
	}
	out := make([]string, len(md))
	for i, l := range md {
		out[i] = "    " + l
	}
	return out
}

// currentAction is the item a node is busy with: the latest running one, else
// the latest. ok is false for a node without activity.
func currentAction(n *workspace.Node) (runner.Activity, bool) {
	for i := len(n.Activity) - 1; i >= 0; i-- {
		if n.Activity[i].State == runner.ActRunning {
			return n.Activity[i], true
		}
	}
	if len(n.Activity) > 0 {
		return n.Activity[len(n.Activity)-1], true
	}
	return runner.Activity{}, false
}

// activityCounts is the number of items and how many of them failed.
func activityCounts(n *workspace.Node) (total, failed int) {
	for _, a := range n.Activity {
		total++
		if a.State == runner.ActFailed {
			failed++
		}
	}
	return total, failed
}

// ── Stall ────────────────────────────────────────────────────────────────────

// stallBase is when a running node last showed life.
func stallBase(n *workspace.Node) time.Time {
	if !n.LastActivityAt.IsZero() {
		return n.LastActivityAt
	}
	return n.StartedAt
}

// stalledFor is how long a running node has been silent once that reaches
// stallAfter; ok is false otherwise.
func stalledFor(n *workspace.Node, now time.Time) (time.Duration, bool) {
	if n.Status != workspace.StatusRunning {
		return 0, false
	}
	base := stallBase(n)
	if base.IsZero() {
		return 0, false
	}
	d := now.Sub(base)
	return d, d >= stallAfter
}

// stallText is "no activity for 1m" (minutes rounded down); "" when not stalled.
func stallText(n *workspace.Node, now time.Time) string {
	d, ok := stalledFor(n, now)
	if !ok {
		return ""
	}
	return "no activity for " + feedDuration(d.Truncate(time.Minute))
}

// checkStalls sends one NeedsYou note per stall episode of a running node. An
// episode ends when the node shows activity again, which moves its base time.
func (m *workspaceModel) checkStalls() {
	if m.g == nil {
		return
	}
	now := m.now()
	for _, n := range m.g.Nodes {
		if n.Status != workspace.StatusRunning {
			delete(m.stallNoted, n.ID)
			continue
		}
		d, ok := stalledFor(n, now)
		if !ok || d < stallNotifyAfter {
			continue
		}
		base := stallBase(n)
		if m.stallNoted[n.ID].Equal(base) {
			continue
		}
		if m.stallNoted == nil {
			m.stallNoted = map[string]time.Time{}
		}
		m.stallNoted[n.ID] = base
		m.notifyNote(notify.NeedsYou, fmt.Sprintf("%s has had no activity for %s", n.ID, feedDuration(stallNotifyAfter)))
	}
}

// ── Card rows ────────────────────────────────────────────────────────────────

// activityTimelineRows is the whole saved timeline of a node for a card: a
// divider, "… N earlier steps" and every item; the detail of thinking items is
// shown wrapped under them.
func activityTimelineRows(n *workspace.Node, now time.Time, iw int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	rows := []string{dim.Render(strings.Repeat("─", min(iw, 24)))}
	if n.ActivityDropped > 0 {
		rows = append(rows, dim.Render(fmt.Sprintf("… %d earlier %s", n.ActivityDropped, pluralize(n.ActivityDropped, "step", "steps"))))
	}
	for _, a := range n.Activity {
		rows = append(rows, activityLine(a, now, iw))
		if a.Kind == runner.ActThinking {
			rows = append(rows, activityDetailLines(a.Detail, iw, actDetailLines)...)
		}
	}
	return rows
}

// cardMeta is the dim text after the duration: " · ctx 18k".
func cardMeta(n *workspace.Node) string {
	if c := ctxLabel(n); c != "" {
		return " · " + c
	}
	return ""
}
