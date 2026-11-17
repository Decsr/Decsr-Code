package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// outDetailLines is the most wrapped lines the detail of one expanded item
// shows in the output view.
const outDetailLines = 60

// outKey identifies an expanded item of the output view.
func outKey(nodeID, actID string) string { return nodeID + "|" + actID }

// openOutput switches to the output view of node n: the timeline, or the raw
// text for a node without activity (an old session).
func (m workspaceModel) openOutput(n *workspace.Node) workspaceModel {
	if m.sidebarOverlay() {
		m.sbFlip = false
	}
	m.screen = wsScreenOutput
	m.outNode = n.ID
	m.outFollow = true
	m.outRaw = len(n.Activity) == 0 && len(m.outputs[n.ID]) > 0
	m.outPane = 0
	m.outDiff = nil
	m.outDiffBusy, m.outDiffDirty = false, false
	m.outAttempt = 0
	m.outCur = max(len(n.Activity)-1, 0)
	m.refreshOutput()
	return m
}

// outTimelineOn reports whether the output view shows the timeline of the node.
func (m workspaceModel) outTimelineOn() bool { return !m.outRaw }

// refreshTimeline fills the viewport with the activity of the output node, one
// line per item and the detail of the expanded ones. The cursor row has a bar;
// the viewport follows the newest item or keeps the cursor in view.
func (m *workspaceModel) refreshTimeline() {
	m.sizeOutVP()
	n := m.g.Node(m.outNode)
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	if n == nil {
		m.outVP.SetContent(dim.Render("(no activity recorded; t shows the raw output)"))
		return
	}
	w := max(m.outVP.Width, 8)
	arch := m.outArchived(n)
	acts, dropped := m.outActivity(n)
	var lines []string
	if arch != nil {
		lines = archivedHeader(arch, w)
	}
	if len(acts) == 0 {
		msg := "(no activity recorded; t shows the raw output)"
		if n.Status == workspace.StatusRunning && arch == nil {
			msg = fmt.Sprintf("waiting for %s's first output… (%s)", n.Role, formatElapsed(m.now().Sub(n.StartedAt)))
		}
		if arch != nil {
			msg = "  (the activity of this attempt was not kept)"
		}
		m.outVP.SetContent(strings.Join(append(lines, dim.Render(msg)), "\n"))
		return
	}
	last := len(acts) - 1
	if m.outFollow || m.outCur > last {
		m.outCur = last
	}
	m.outCur = max(m.outCur, 0)
	if dropped > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("  … %d earlier %s", dropped, pluralize(dropped, "step", "steps"))))
	}
	curTop, curBot := 0, 0
	for i, a := range acts {
		bar := "  "
		if i == m.outCur {
			bar = lipgloss.NewStyle().Foreground(ui.Accent).Render("▌ ")
			curTop = len(lines)
		}
		lines = append(lines, bar+activityLine(a, m.now(), w-2))
		key := outKey(n.ID, a.ID)
		if arch != nil {
			key = outKey(fmt.Sprintf("%s#%d", n.ID, arch.N), a.ID)
		}
		if m.outOpen[key] {
			detail := a.Detail
			if m.eng != nil && arch == nil {
				if full, ok := m.eng.ActivityDetail(n.ID, a.ID); ok {
					detail = full
				}
			}
			if strings.TrimSpace(detail) == "" {
				lines = append(lines, dim.Render("      (no detail)"))
			}
			detailLines := activityDetailLines(detail, w-2, outDetailLines)
			if a.Kind == runner.ActMessage {
				detailLines = activityMessageLines(detail, w-2, outDetailLines, a.State == runner.ActRunning)
			}
			for _, l := range detailLines {
				lines = append(lines, "  "+l)
			}
		}
		if i == m.outCur {
			curBot = len(lines)
		}
	}
	m.outVP.SetContent(strings.Join(lines, "\n"))
	switch {
	case m.outFollow:
		m.outVP.GotoBottom()
	case curTop < m.outVP.YOffset:
		m.outVP.SetYOffset(curTop)
	case curBot > m.outVP.YOffset+m.outVP.Height:
		m.outVP.SetYOffset(curBot - m.outVP.Height)
	}
}

// timelineKey handles the keys of the timeline: it reports whether it used k.
func (m workspaceModel) timelineKey(k string) (workspaceModel, bool) {
	n := m.g.Node(m.outNode)
	if n == nil {
		return m, false
	}
	acts, _ := m.outActivity(n)
	if len(acts) == 0 {
		return m, false
	}
	last := len(acts) - 1
	move := func(to int) workspaceModel {
		m.outCur = min(max(to, 0), last)
		m.outFollow = m.outCur == last
		m.refreshOutput()
		return m
	}
	switch k {
	case "up", "k":
		return move(m.outCur - 1), true
	case "down", "j":
		return move(m.outCur + 1), true
	case "g", "home":
		return move(0), true
	case "G", "end":
		return move(last), true
	case "enter", " ", "space":
		if m.outOpen == nil {
			m.outOpen = map[string]bool{}
		}
		key := outKey(n.ID, acts[m.outCur].ID)
		if arch := m.outArchived(n); arch != nil {
			key = outKey(fmt.Sprintf("%s#%d", n.ID, arch.N), acts[m.outCur].ID)
		}
		m.outOpen[key] = !m.outOpen[key]
		m.refreshOutput()
		return m, true
	}
	return m, false
}

// outputHeader is the line under the header: node, title and, on the right,
// the status, how long it worked and its context size.
func (m workspaceModel) outputHeader(w int) string {
	n := m.g.Node(m.outNode)
	if n == nil {
		return m.outNode
	}
	_, label := nodeStatusGlyph(m.g, n)
	meta := []string{label}
	if at := m.attemptMeta(n); at != "" {
		meta = append([]string{at}, meta...)
	}
	if a := m.outArchived(n); a != nil {
		meta[len(meta)-1] = string(a.Status)
		if !a.StartedAt.IsZero() && !a.EndedAt.IsZero() {
			meta = append(meta, "Worked for "+feedDuration(a.EndedAt.Sub(a.StartedAt)))
		}
	} else {
		if !n.StartedAt.IsZero() && !m.outCombined() {
			end, word := n.EndedAt, "Worked for "
			if n.Status == workspace.StatusRunning || end.IsZero() {
				end, word = m.now(), "Working for "
			}
			meta = append(meta, word+feedDuration(end.Sub(n.StartedAt)))
		}
		if c := ctxLabel(n); c != "" {
			meta = append(meta, c)
		}
	}
	right := strings.Join(meta, " · ")
	left := n.ID + " · " + n.Title
	room := w - 4 - len([]rune(right)) - 3
	if room < 10 {
		return lipgloss.NewStyle().Foreground(ui.Text).Bold(true).Render(truncateOneLine(left+" · "+right, w-4))
	}
	return lipgloss.NewStyle().Foreground(ui.Text).Bold(true).Render(truncateOneLine(left, room)) +
		lipgloss.NewStyle().Foreground(ui.Muted).Render(" · "+right)
}
