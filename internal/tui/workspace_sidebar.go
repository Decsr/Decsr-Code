package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	wsSidebarMin      = 100 // the sidebar is inline from this width
	wsSidebarSessions = 5   // other saved sessions listed under the nodes
	wsSidebarSessMinH = 14  // fewest rows for the sessions block to show
	wsSidebarOverlayW = 34
)

// sidebarW is the width of the inline sidebar: about 22% of the terminal,
// between 28 and 34 cells.
func (m workspaceModel) sidebarW() int { return min(max(m.width*22/100, 28), 34) }

// sidebarInline reports whether the sidebar sits left of the main pane.
func (m workspaceModel) sidebarInline() bool { return m.width >= wsSidebarMin && !m.sbFlip }

// sidebarOverlay reports whether the sidebar is a box over a narrow screen.
func (m workspaceModel) sidebarOverlay() bool { return m.width < wsSidebarMin && m.sbFlip }

// sidebarVisible reports whether the sidebar is on screen in either form.
func (m workspaceModel) sidebarVisible() bool { return m.sidebarInline() || m.sidebarOverlay() }

// mainWidth is the width of the main pane: the terminal minus the sidebar and
// its divider.
func (m workspaceModel) mainWidth() int {
	if m.sidebarInline() {
		return m.width - m.sidebarW() - 1
	}
	return m.width
}

// sbAge is the short time since t: now, 2m, 1h, 3d.
func sbAge(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// otherSessions are the saved sessions of the project besides the open one,
// at most wsSidebarSessions.
func (m workspaceModel) otherSessions() []workspace.SessionInfo {
	cur := m.sess.snapshot().ID
	var out []workspace.SessionInfo
	for _, s := range m.sbSessions {
		if s.ID != cur {
			out = append(out, s)
			if len(out) == wsSidebarSessions {
				break
			}
		}
	}
	return out
}

// sbSeg renders text with the selected-row background when on is set.
func sbSeg(text string, fg lipgloss.TerminalColor, bold, on bool) string {
	st := lipgloss.NewStyle().Foreground(fg).Bold(bold)
	if on {
		st = st.Background(ui.Sel)
	}
	return st.Render(text)
}

// sbRow joins left and right into a w-cell row; the selected row is filled with
// ui.Sel across the whole width.
func sbRow(left, right string, w int, on bool) string {
	room := w - 1 - ansi.StringWidth(right)
	if right != "" {
		room--
	}
	left = ansi.Truncate(left, max(room, 1), "…")
	gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(right), 0)
	fill := strings.Repeat(" ", gap)
	if on {
		fill = lipgloss.NewStyle().Background(ui.Sel).Render(fill)
	}
	return left + fill + right
}

// nodeRows draws one node of the sidebar as two rows of w cells.
func (m workspaceModel) nodeRows(n *workspace.Node, w int, on bool, models, runners map[string]string) [2]string {
	glyph, _ := nodeStatusGlyph(m.g, n)
	if n.Status == workspace.StatusRunning {
		glyph = pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
	}
	l1 := sbSeg(" ", ui.Text, false, on) + sbSeg(glyph, statusLabelColor(m.g, n), true, on) + sbSeg(" ", ui.Text, false, on) +
		sbSeg(n.ID, ui.Muted, false, on) + sbSeg(" ", ui.Text, false, on) + sbSeg(workspace.Sanitize(n.Title), ui.Text, false, on)
	var l2, age string
	switch {
	case n.Kind == workspace.KindCheck:
		l2 = sbSeg("    checks", ui.Muted, false, on)
	case n.Role == "":
		if n.SuggestedRole != "" {
			l2 = sbSeg("    → "+n.SuggestedRole, ui.Muted, false, on)
		} else {
			l2 = sbSeg("    unassigned", ui.Warn, false, on)
		}
	default:
		badge := lipgloss.NewStyle().Foreground(runnerColor(runners[n.Role]))
		if on {
			badge = badge.Background(ui.Sel)
		}
		l2 = sbSeg("    ", ui.Muted, false, on) + badge.Render(runnerGlyph(runners[n.Role], m.cfg.UI.Icons))
		if md := models[n.Role]; md != "" {
			l2 += sbSeg(" "+md, ui.Muted, false, on)
		}
	}
	acting := false // line 2 shows what the node is doing, in place of the age
	if n.Status == workspace.StatusRunning {
		if st := stallText(n, m.now()); st != "" {
			acting = true
			l2 = sbSeg("    "+st, ui.Warn, false, on)
		} else if a, ok := currentAction(n); ok {
			acting = true
			col := actColor(a)
			if col == ui.Text {
				col = ui.Muted
			}
			l2 = sbSeg("    "+actGlyph(a.Kind)+" "+actText(a, max(w-7, 4), true), col, false, on)
		}
	}
	if n.Status == workspace.StatusWaiting {
		acting = true
		word := "asks a question"
		if n.Report != nil && n.Report.Status == workspace.ReportBlocked {
			word = "blocked"
		}
		if n.Limit != nil {
			word = "limit · " + limitResets(m.now(), n.Limit)
		}
		l2 = sbSeg("    "+word, ui.Warn, false, on)
	}
	switch n.Status {
	case workspace.StatusRunning:
		if !acting {
			age = "now"
		}
	case workspace.StatusFailed:
		age = "failed"
	case workspace.StatusDone, workspace.StatusWaiting:
		// A limited node shows its reset time instead of an age, which would
		// crowd the reset out of the narrow sidebar.
		if !n.EndedAt.IsZero() && n.Limit == nil {
			age = sbAge(m.now(), n.EndedAt)
		}
	}
	if n.Attempt > 1 {
		mark := fmt.Sprintf("attempt %d", n.Attempt)
		if age != "" {
			mark += " · " + age
		}
		age = mark
	}
	return [2]string{sbRow(l1, "", w, on), sbRow(l2, sbSeg(age, ui.Muted, false, on), w, on)}
}

// sidebarRows is the sidebar in exactly h rows of w cells: the session title
// and progress, the node rows (scrolled to keep the selected one in view) and,
// below a divider, the other saved sessions.
func (m workspaceModel) sidebarRows(w, h int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	titleCol := ui.Text
	if m.focus == wsFocusSidebar {
		titleCol = ui.Accent
	}
	title := workspace.Sanitize(m.sess.snapshot().Title)
	if title == "" {
		title = "new session"
	}
	out := []string{" " + lipgloss.NewStyle().Foreground(titleCol).Bold(true).Render(truncateOneLine(title, w-2)), " " + dim.Render(truncateOneLine(m.sidebarProgress(), w-2))}

	sessions := m.otherSessions()
	block := 0
	if len(sessions) > 0 && h >= wsSidebarSessMinH {
		block = 2 + len(sessions)
	}
	area := max(h-len(out)-block, 0)
	models, runners := m.roleLooks()
	var rows []string
	selStart, selEnd := 0, 0
	if m.g != nil {
		for _, n := range m.g.Nodes {
			on := n.ID == m.sel && m.sbSess < 0
			r := m.nodeRows(n, w, on, models, runners)
			if on {
				selStart, selEnd = len(rows), len(rows)+2
			}
			rows = append(rows, r[0], r[1])
		}
	}
	top := 0
	if len(rows) > area {
		top = max(selEnd-area, 0)
		top = min(top, selStart)
		top = min(top, len(rows)-area)
	}
	end := min(top+area, len(rows))
	out = append(out, rows[top:end]...)
	for len(out) < h-block {
		out = append(out, "")
	}
	if block > 0 {
		out = append(out, lipgloss.NewStyle().Foreground(ui.Border).Render(strings.Repeat("─", w)), " "+dim.Render("Sessions"))
		for i, s := range sessions {
			on := m.sbSess == i && m.focus == wsFocusSidebar
			t := workspace.Sanitize(s.Title)
			if t == "" {
				t = "untitled"
			}
			total := s.Todo + s.Running + s.Done + s.Failed + s.Waiting
			right := ""
			if total > 0 {
				right = sbSeg(fmt.Sprintf("%d/%d", s.Done, total), ui.Muted, false, on)
			}
			left := sbSeg(" ○ "+t, ui.Muted, false, on)
			out = append(out, sbRow(left, right, w, on))
		}
	}
	return out[:min(len(out), h)]
}

// sidebarProgress is the dim "2/5 · 1 failed" line under the title.
func (m workspaceModel) sidebarProgress() string {
	if m.g == nil || len(m.g.Nodes) == 0 {
		return "no nodes yet"
	}
	_, running, done, failed := m.g.Counts()
	s := fmt.Sprintf("%d/%d", done, len(m.g.Nodes))
	if running > 0 {
		s += fmt.Sprintf(" · %d running", running)
	}
	if w := m.g.CountWaiting(); w > 0 {
		s += fmt.Sprintf(" · %d waiting", w)
	}
	if failed > 0 {
		s += fmt.Sprintf(" · %d failed", failed)
	}
	return s
}

// sidebarOverlayBox is the sidebar as a rounded box for narrow screens.
func (m workspaceModel) sidebarOverlayBox(mainH int) string {
	bw := min(wsSidebarOverlayW, m.width-4)
	bh := max(mainH, 5)
	return ui.Overlay("Nodes", m.sidebarRows(bw-4, bh-2), nil, bw)
}

// sbMove moves the sidebar cursor one row: through the nodes in order, then
// the other sessions. dir is -1 or 1.
func (m workspaceModel) sbMove(dir int) workspaceModel {
	sessions := m.otherSessions()
	nodes := []*workspace.Node(nil)
	if m.g != nil {
		nodes = m.g.Nodes
	}
	idx := -1
	for i, n := range nodes {
		if n.ID == m.sel {
			idx = i
		}
	}
	switch {
	case m.sbSess >= 0:
		next := m.sbSess + dir
		switch {
		case next < 0 && len(nodes) > 0:
			m.sbSess = -1
			m.sel = nodes[len(nodes)-1].ID
		case next < 0:
		default:
			m.sbSess = min(next, len(sessions)-1)
		}
	case dir > 0 && idx == len(nodes)-1 && len(sessions) > 0 && m.sbSessionsShown():
		m.sbSess = 0
	case len(nodes) > 0:
		if idx < 0 {
			idx = 0
		} else {
			idx = min(max(idx+dir, 0), len(nodes)-1)
		}
		m.sel = nodes[idx].ID
	}
	if m.sbSess < 0 && m.sel != "" && !m.showGraph {
		m = m.feedReveal(m.sel)
	}
	return m
}

// sbSessionsShown reports whether the sessions block fits in the sidebar.
func (m workspaceModel) sbSessionsShown() bool {
	h := m.mainHeight()
	if m.sidebarOverlay() {
		h = max(h, 5) - 2
	}
	return h >= wsSidebarSessMinH
}
