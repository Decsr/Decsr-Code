package tui

import (
	"fmt"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// runSummary is the chat message that closes a run: a count line, one line for
// each work or fix node that ended in the run (ended holds their IDs), the
// Build & test line and what the user can do next. It reads the graph only;
// no model writes it. Lines are cut to width cells. It is "" when no listed
// node ended.
func runSummary(g *workspace.Graph, ended []string, iso bool, width int) string {
	if g == nil || len(ended) == 0 {
		return ""
	}
	in := make(map[string]bool, len(ended))
	for _, id := range ended {
		in[id] = true
	}
	_, _, done, failed := g.Counts()
	lines := []string{fmt.Sprintf("Run finished · %d done · %d waiting · %d failed", done, g.CountWaiting(), failed)}
	var waiting, failedWork []string
	var check *workspace.Node
	for _, n := range g.Nodes {
		if n.Kind == workspace.KindCheck {
			check = n
		}
		if n.Status == workspace.StatusWaiting && !n.Limited() {
			waiting = append(waiting, n.ID)
		}
		if n.Kind != workspace.KindWork || !in[n.ID] {
			continue
		}
		title := oneLine(workspace.Sanitize(n.Title))
		switch n.Status {
		case workspace.StatusDone:
			if n.Answered {
				lines = append(lines, fmt.Sprintf("✓ %s %s - answer above", n.ID, title))
				break
			}
			lines = append(lines, fmt.Sprintf("✓ %s %s - %s%s", n.ID, title, summaryText(n), summaryFiles(n)))
		case workspace.StatusWaiting:
			what := "waits for your answer"
			if n.Report != nil && n.Report.Status == workspace.ReportBlocked {
				what = "blocked: " + summaryText(n)
			}
			lines = append(lines, fmt.Sprintf("◇ %s %s - %s", n.ID, title, what))
		case workspace.StatusFailed:
			failedWork = append(failedWork, n.ID)
			lines = append(lines, fmt.Sprintf("! %s %s - %s", n.ID, title, oneLine(workspace.SanitizeText(n.Error))))
		}
	}
	checkOK := false
	if check != nil {
		switch check.Status {
		case workspace.StatusDone:
			if check.Skipped {
				lines = append(lines, "– Build & test skipped · "+workspace.SkippedText)
				break
			}
			checkOK = true
			lines = append(lines, "✓ Build & test passed")
		case workspace.StatusFailed:
			lines = append(lines, "! Build & test failed: "+oneLine(workspace.SanitizeText(check.Error)))
		default:
			lines = append(lines, "· Build & test not run")
		}
	}
	var next []string
	if len(waiting) > 0 {
		next = append(next, "answer "+strings.Join(waiting, ", "))
	}
	if iso && checkOK {
		next = append(next, "/merge to bring the result into your checkout")
	}
	if len(failedWork) > 0 {
		next = append(next, fmt.Sprintf("retry with `@%s ...`", failedWork[0]))
	}
	next = append(next, "type a follow-up task")
	last := "Next: " + strings.Join(next, ", ")
	if len(next) > 1 {
		i := strings.LastIndex(last, ", ")
		last = last[:i] + ", or" + last[i+1:]
	}
	lines = append(lines, last+".")
	for i, l := range lines {
		lines[i] = truncateOneLine(l, width)
	}
	return strings.Join(lines, "\n")
}

// summaryText is the one-line summary of a node: its report's, else its own.
func summaryText(n *workspace.Node) string {
	s := ""
	if n.Report != nil {
		s = n.Report.Summary
	}
	if strings.TrimSpace(s) == "" {
		s = n.Summary
	}
	return oneLine(workspace.SanitizeText(s))
}

// summaryFiles is " (2 files)" for the files a node changed; "" for none.
func summaryFiles(n *workspace.Node) string {
	k := len(n.FileStats)
	if k == 0 {
		k = len(n.AllFiles())
	}
	if k == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d %s)", k, pluralize(k, "file", "files"))
}

// summaryAfterEvent remembers the node that ended with ev and, when the run is
// over (nothing runs and nothing can start), says the summary in the chat.
func (m workspaceModel) summaryAfterEvent(ev workspace.Event) workspaceModel {
	switch ev.Type {
	case workspace.EventDone, workspace.EventWaiting:
	case workspace.EventFailed:
		if ev.Text == "stopped" {
			return m
		}
	default:
		return m
	}
	m.runEnded = append(append([]string(nil), m.runEnded...), ev.NodeID)
	if m.eng != nil {
		m.g = m.eng.Snapshot()
	}
	if _, running, _, _ := m.g.Counts(); running > 0 || len(m.g.Runnable()) > 0 {
		return m
	}
	if text := runSummary(m.g, m.runEnded, m.iso != nil, max(m.mainWidth()-8, 40)); text != "" {
		m.say("system", text)
	}
	m.runEnded = nil
	return m
}
