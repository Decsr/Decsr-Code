package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// replyRe is a prompt that replies to a node: "@T3 message", the message may
// span several lines.
var replyRe = regexp.MustCompile(`(?s)^@([A-Za-z]+[0-9]*)\s+(.+)$`)

// parseReply splits "@T3 message" into the node ID as typed and the message.
func parseReply(text string) (id, msg string, ok bool) {
	g := replyRe.FindStringSubmatch(strings.TrimSpace(text))
	if g == nil {
		return "", "", false
	}
	msg = strings.TrimSpace(g[2])
	return g[1], msg, msg != ""
}

// replyNode finds the node an ID typed in a reply names, ignoring case.
func (m workspaceModel) replyNode(id string) *workspace.Node {
	if m.g == nil {
		return nil
	}
	for _, n := range m.g.Nodes {
		if strings.EqualFold(n.ID, id) {
			return n
		}
	}
	return nil
}

// sendReply sends msg to a node as a reply. Whatever the outcome, an error
// leaves the text in the prompt.
func (m workspaceModel) sendReply(id, msg string) (tea.Model, tea.Cmd) {
	n := m.replyNode(id)
	if m.eng == nil || n == nil {
		m.say("error", fmt.Sprintf("unknown node %s — type @ to pick one", id))
		return m, nil
	}
	res, err := m.eng.Reply(n.ID, msg)
	if err != nil {
		m.say("error", "reply to "+n.ID+": "+err.Error())
		return m, nil
	}
	first, rest, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	if rest != "" {
		first += " …"
	}
	m.say("you", "→ "+n.ID+": "+first)
	switch res.State {
	case workspace.ReplyQueued:
		m.say("system", "queued for "+n.ID+": it starts attempt "+fmt.Sprint(res.Attempt)+" when the run ends")
	case workspace.ReplyWaiting:
		m.say("system", n.ID+" waiting: "+res.Reason)
	case workspace.ReplyStarted:
		if res.Attempt > 1 {
			m.say("system", fmt.Sprintf("%s attempt %d started", n.ID, res.Attempt))
		} else {
			m.say("system", n.ID+" started")
		}
	}
	m.input.Reset()
	m.cmdIdx = 0
	m.g = m.eng.Snapshot()
	m.sel = n.ID
	return m, m.startTick()
}

// replyKey is "m": it focuses the prompt filled with "@T3 ".
func (m workspaceModel) replyKey(n *workspace.Node) workspaceModel {
	switch {
	case n == nil:
	case n.Kind != workspace.KindWork:
		m.setNotice("Build & test takes no reply. Reply to a work or fix node.", true)
	default:
		prefix := "@" + n.ID + " "
		v := m.input.Value()
		if v != "" && !strings.HasPrefix(v, "@") {
			m.setNotice("The prompt holds a draft. Clear it first, then press m.", true)
			return m
		}
		if cur, _ := parseMention(v); cur != "" {
			v = strings.TrimLeft(v[len(cur)+1:], " \t")
		} else {
			v = ""
		}
		m.setFocus(wsFocusPrompt)
		m.input.SetValue(prefix + v)
		m.cmdIdx = 0
	}
	return m
}

// parseMention returns the leading "@ID" word of v (without the "@") and the
// rest; "" when v does not start with a mention.
func parseMention(v string) (id, rest string) {
	if !strings.HasPrefix(v, "@") {
		return "", v
	}
	end := strings.IndexAny(v, " \t\n")
	if end < 0 {
		end = len(v)
	}
	return v[1:end], v[end:]
}

// mentionOpen is true while a node ID is being typed after "@": the text
// starts with "@" and holds no space or line break yet.
func (m workspaceModel) mentionOpen() bool {
	v := m.input.Value()
	return m.hasNodes() && strings.HasPrefix(v, "@") && !strings.ContainsAny(v, " \t\n")
}

// mentionMatches lists the work and fix nodes whose ID starts with what is typed
// after "@", as palette rows: "@T3  title · status".
func (m workspaceModel) mentionMatches() []wsCommand {
	if !m.mentionOpen() {
		return nil
	}
	prefix := strings.ToUpper(m.input.Value()[1:])
	var out []wsCommand
	for _, n := range m.g.Nodes {
		if n.Kind != workspace.KindWork || !strings.HasPrefix(strings.ToUpper(n.ID), prefix) {
			continue
		}
		_, label := nodeStatusGlyph(m.g, n)
		out = append(out, wsCommand{name: "@" + n.ID, desc: oneLine(workspace.Sanitize(n.Title)) + " · " + label})
	}
	return out
}

// completeMention puts "@T3 " in the prompt.
func (m workspaceModel) completeMention(c wsCommand) workspaceModel {
	m.input.SetValue(c.name + " ")
	m.cmdIdx = 0
	m.cmdPicked = false
	return m
}

// mentionLines is the "@" node list drawn above the prompt, at most maxRows.
func (m workspaceModel) mentionLines(maxRows int) []string {
	if !m.mentionOpen() || maxRows <= 0 {
		return nil
	}
	matches := m.mentionMatches()
	var pal []string
	if len(matches) == 0 {
		pal = []string{lipgloss.NewStyle().Foreground(ui.Fail).Render("! no such node")}
	} else {
		pal = renderCmdList(matches, m.cmdIdx, m.width-2, maxRows)
	}
	for i := range pal {
		pal[i] = " " + pal[i]
	}
	return pal
}

// ── Cards ────────────────────────────────────────────────────────────────────

// replySnippet is the first line of a reply in at most 60 characters, quoted.
func replySnippet(s string) string {
	s = oneLine(workspace.SanitizeText(s))
	if r := []rune(s); len(r) > 60 {
		s = string(r[:59]) + "…"
	}
	return `"` + s + `"`
}

// replyLines are the dim lines of a card about a reply that is queued or
// waits for its node to start.
func replyLines(n *workspace.Node, iw int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	var rows []string
	if n.PendingReply != "" {
		rows = append(rows, dim.Render(truncateOneLine("reply queued: "+replySnippet(n.PendingReply), iw)))
	}
	if n.Status == workspace.StatusTodo && n.Reply != "" {
		rows = append(rows, dim.Render(truncateOneLine("reply waiting: "+replySnippet(n.Reply), iw)))
	}
	return rows
}

// attemptLine is "Attempt 1 · failed · Worked for 2m 10s ›".
func attemptLine(a workspace.Attempt) string {
	s := fmt.Sprintf("Attempt %d · %s", a.N, a.Status)
	if !a.StartedAt.IsZero() && !a.EndedAt.IsZero() && !a.EndedAt.Before(a.StartedAt) {
		s += " · Worked for " + feedDuration(a.EndedAt.Sub(a.StartedAt))
	}
	return s + " ›"
}

// attemptRows are the earlier attempts of a node for an expanded card: a folded
// line each, then what started it, its summary or error and the tail of its
// activity when that was kept.
func attemptRows(n *workspace.Node, now time.Time, iw int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	fail := lipgloss.NewStyle().Foreground(ui.Fail)
	var rows []string
	for _, a := range n.Attempts {
		head := dim
		if a.Status == workspace.StatusFailed {
			head = fail
		}
		rows = append(rows, head.Render(truncateOneLine(attemptLine(a), iw)))
		if a.Reply != "" {
			rows = append(rows, dim.Render(truncateOneLine("  reply: "+replySnippet(a.Reply), iw)))
		}
		rows = append(rows, summaryRows(a.Summary, iw)...)
		if a.Error != "" && a.Error != "stopped" {
			errLines := hardWrap(workspace.SanitizeText(a.Error), iw)
			for i, l := range errLines {
				if i == cardErrorLines {
					rows[len(rows)-1] = fail.Render(truncateOneLine(errLines[cardErrorLines-1]+" …", iw))
					break
				}
				rows = append(rows, fail.Render(l))
			}
		}
		if len(a.Activity) > 0 {
			from := max(len(a.Activity)-cardActivityRows, 0)
			if earlier := from + a.ActivityDropped; earlier > 0 {
				rows = append(rows, dim.Render(fmt.Sprintf("… %d earlier %s", earlier, pluralize(earlier, "step", "steps"))))
			}
			for _, act := range a.Activity[from:] {
				rows = append(rows, activityLine(act, now, iw))
			}
		}
	}
	return rows
}

// attemptTag is "attempt 2 · " for the card header of a replied node.
func attemptTag(n *workspace.Node) string {
	if n.Attempt > 1 {
		return fmt.Sprintf("attempt %d · ", n.Attempt)
	}
	return ""
}

// ── Output view ──────────────────────────────────────────────────────────────

// outArchived is the earlier attempt the output view shows; nil for the current one.
func (m workspaceModel) outArchived(n *workspace.Node) *workspace.Attempt {
	if m.outAttempt == 0 || n == nil {
		return nil
	}
	for i := range n.Attempts {
		if n.Attempts[i].N == m.outAttempt {
			return &n.Attempts[i]
		}
	}
	return nil
}

// outActivity is the activity the output view lists and how many older items
// were cut off.
func (m workspaceModel) outActivity(n *workspace.Node) (act []runner.Activity, dropped int) {
	if a := m.outArchived(n); a != nil {
		return a.Activity, a.ActivityDropped
	}
	return n.Activity, n.ActivityDropped
}

// outAttemptStep moves the output view to the previous (dir -1) or next (dir 1)
// attempt: the earlier ones, then the current one.
func (m workspaceModel) outAttemptStep(dir int) workspaceModel {
	n := m.g.Node(m.outNode)
	if n == nil || len(n.Attempts) == 0 {
		return m
	}
	nums := make([]int, 0, len(n.Attempts)+1)
	for _, a := range n.Attempts {
		nums = append(nums, a.N)
	}
	nums = append(nums, 0) // 0 is the current attempt
	at := len(nums) - 1
	for i, v := range nums {
		if v == m.outAttempt {
			at = i
		}
	}
	at = min(max(at+dir, 0), len(nums)-1)
	m.outAttempt = nums[at]
	m.outFollow = true
	m.outCur = 1 << 30
	m.refreshOutput()
	return m
}

// archivedHeader is the lines that open the output view of an earlier attempt.
func archivedHeader(a *workspace.Attempt, w int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	text := lipgloss.NewStyle().Foreground(ui.Text)
	fail := lipgloss.NewStyle().Foreground(ui.Fail)
	lines := []string{dim.Render(truncateOneLine("  "+strings.TrimSuffix(attemptLine(*a), " ›"), w))}
	if a.Reply != "" {
		for _, l := range hardWrap("reply: "+workspace.SanitizeText(a.Reply), w-4) {
			lines = append(lines, dim.Render("  "+l))
		}
	}
	for _, l := range hardWrap(oneLine(workspace.SanitizeText(a.Summary)), w-4) {
		if l != "" {
			lines = append(lines, "  "+text.Render(l))
		}
	}
	if a.Error != "" && a.Error != "stopped" {
		for _, l := range hardWrap(workspace.SanitizeText(a.Error), w-4) {
			lines = append(lines, "  "+fail.Render(l))
		}
	}
	return append(lines, dim.Render("  "+strings.Repeat("─", min(max(w-4, 1), 24))))
}

// attemptMeta is "attempt 2 of 3" for the output header; "" for a node with one.
func (m workspaceModel) attemptMeta(n *workspace.Node) string {
	if n.AttemptNumber() < 2 {
		return ""
	}
	at := n.AttemptNumber()
	if a := m.outArchived(n); a != nil {
		at = a.N
	}
	return fmt.Sprintf("attempt %d of %d", at, n.AttemptNumber())
}

// sayQueuedReplyStarted announces the attempt a queued reply started when the
// run of the node ended: the node is already running again.
func (m *workspaceModel) sayQueuedReplyStarted(id string) {
	if m.eng == nil {
		return
	}
	n := m.eng.Snapshot().Node(id)
	if n != nil && n.Status == workspace.StatusRunning && n.Attempt > 1 {
		m.say("system", fmt.Sprintf("%s attempt %d started with your queued reply", n.ID, n.Attempt))
	}
}
