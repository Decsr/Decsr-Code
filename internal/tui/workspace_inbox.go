package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/automation"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// inboxKind is the type of an inbox item.
type inboxKind int

const (
	inboxQuestion   inboxKind = iota // a node waits for an answer or is blocked
	inboxLimit                       // a node waits for a usage limit to lift
	inboxReview                      // a lead review waits for y or n
	inboxFailed                      // a node failed
	inboxStall                       // a running node is silent
	inboxPlugin                      // a plugin question is unanswered
	inboxBudget                      // the budget warning (info)
	inboxAutomation                  // an automation run that failed or needs you (info)
)

// inboxItem is one line of the inbox. Items of the first six kinds need the
// user and clear themselves when the state changes; the others are info and
// are cleared with x.
type inboxItem struct {
	kind  inboxKind
	id    string // stable key; for info items it includes the value that made it
	ref   string // node ID, review key, plugin name or automation name
	glyph string
	label string
	text  string
	right string // age, or the retry count of a limit
	info  bool
}

// inboxAutoRun is the last run of an automation, read from its log.
type inboxAutoRun struct {
	name  string
	entry automation.Entry
}

// inboxInput is everything buildInbox reads.
type inboxInput struct {
	g         *workspace.Graph
	reviews   []workspace.Review
	plugins   []workspace.PluginRequest // pending and askable
	now       time.Time
	spent     float64
	limit     float64
	warnPct   int
	autos     []inboxAutoRun
	dismissed []string
	needsOnly bool
}

// inboxAge is the short age of an item.
func inboxAge(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return sbAge(now, t)
}

// buildInbox derives the items from the state: the ones that need the user,
// then the info ones that were not dismissed. It has no side effects.
func buildInbox(in inboxInput) (need, info []inboxItem) {
	if g := in.g; g != nil {
		for _, n := range g.Nodes {
			title := workspace.Sanitize(n.Title)
			switch {
			case n.Limited():
				l := n.Limit
				who := l.Provider
				if who == "" {
					who = "limit"
				}
				need = append(need, inboxItem{kind: inboxLimit, id: "limit:" + n.ID, ref: n.ID, glyph: "◷",
					label: n.ID + " limit", text: who + " · " + limitResets(in.now, l),
					right: fmt.Sprintf("retry %d/%d", l.Retries, workspace.MaxLimitRetries)})
			case n.Status == workspace.StatusWaiting:
				verb, what := "asks", ""
				if n.Report != nil {
					what = n.Report.Ask()
					if n.Report.Status == workspace.ReportBlocked {
						verb = "blocked"
					}
				}
				if what == "" {
					what = title
				}
				need = append(need, inboxItem{kind: inboxQuestion, id: "ask:" + n.ID, ref: n.ID, glyph: "?",
					label: n.ID + " " + verb, text: oneLine(workspace.SanitizeText(what)), right: inboxAge(in.now, n.EndedAt)})
			}
		}
	}
	for i := len(in.reviews) - 1; i >= 0; i-- {
		r := in.reviews[i]
		if r.State != workspace.ReviewPending {
			continue
		}
		need = append(need, inboxItem{kind: inboxReview, id: reviewKey(r), ref: reviewKey(r), glyph: "◆", label: "Lead review",
			text:  fmt.Sprintf("%d %s waiting for y / n", len(r.Applied), pluralize(len(r.Applied), "change", "changes")),
			right: inboxAge(in.now, r.At)})
		break
	}
	if g := in.g; g != nil {
		for _, n := range g.Nodes {
			if n.Status != workspace.StatusFailed || n.Error == "stopped" {
				continue
			}
			label := n.ID + " failed"
			if n.Kind == workspace.KindCheck {
				label = "Build & test"
			}
			need = append(need, inboxItem{kind: inboxFailed, id: "failed:" + n.ID, ref: n.ID, glyph: "!",
				label: label, text: oneLine(workspace.SanitizeText(n.Error)), right: inboxAge(in.now, n.EndedAt)})
		}
		for _, n := range g.Nodes {
			if d, ok := stalledFor(n, in.now); ok && d >= stallNotifyAfter {
				need = append(need, inboxItem{kind: inboxStall, id: "stall:" + n.ID, ref: n.ID, glyph: "◌",
					label: n.ID + " stalled", text: "no activity for " + feedDuration(d.Truncate(time.Minute)), right: ""})
			}
		}
	}
	for _, p := range in.plugins {
		need = append(need, inboxItem{kind: inboxPlugin, id: "plugin:" + p.Name, ref: p.Name, glyph: "+",
			label: "Plugin " + workspace.Sanitize(p.Name), text: oneLine(workspace.Sanitize(p.Reason))})
	}
	if in.needsOnly {
		return need, nil
	}

	gone := map[string]bool{}
	for _, d := range in.dismissed {
		gone[d] = true
	}
	if in.limit > 0 && in.warnPct > 0 {
		pct := int(in.spent / in.limit * 100)
		if pct >= in.warnPct {
			it := inboxItem{kind: inboxBudget, id: fmt.Sprintf("budget:%d", pct/10*10), glyph: "·", label: "Budget", info: true,
				text: fmt.Sprintf("$%.2f of $%.2f (%d%%)", in.spent, in.limit, pct)}
			if !gone[it.id] {
				info = append(info, it)
			}
		}
	}
	for _, a := range in.autos {
		res := strings.ToLower(a.entry.Result)
		if !strings.HasPrefix(res, "failed") && res != "needs you" && res != "timed out" && res != "budget reached" {
			continue
		}
		it := inboxItem{kind: inboxAutomation, id: "auto:" + a.name + ":" + a.entry.Start.Format(time.RFC3339), ref: a.name, glyph: "·",
			label: a.name, info: true, text: "automation " + oneLine(workspace.SanitizeText(a.entry.Result)), right: inboxAge(in.now, a.entry.End)}
		if !gone[it.id] {
			info = append(info, it)
		}
	}
	return need, info
}

// readAutoRuns reads the last run of every automation log of the project.
func readAutoRuns(root string) []inboxAutoRun {
	paths, _ := filepath.Glob(filepath.Join(automation.Dir(root), "*.log"))
	sort.Strings(paths)
	var out []inboxAutoRun
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".log")
		if e, ok := automation.LastEntry(root, name); ok {
			out = append(out, inboxAutoRun{name: name, entry: e})
		}
	}
	return out
}

// readLogTail is the last n lines of a file; the lines are cleaned for display.
func readLogTail(path string, n int) []string {
	data, err := os.ReadFile(path) // #nosec G304 -- path under the project root, validated name
	if err != nil {
		return []string{"(no log)"}
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		lines[i] = cleanOutputLine(l)
	}
	return lines
}

// inboxInputOf collects the state buildInbox reads. needsOnly skips the info
// items, which cost a price lookup and the log files.
func (m workspaceModel) inboxInputOf(needsOnly bool) inboxInput {
	s := m.sess.snapshot()
	in := inboxInput{g: m.g, reviews: s.Reviews, now: m.now(), dismissed: s.InboxDismissed, needsOnly: needsOnly}
	if m.g != nil {
		for _, r := range m.g.PendingPlugins() {
			if _, ok := plugins.FindCatalog(r.Name); ok && m.plug.State(r.Name) != plugins.StateEnabled {
				in.plugins = append(in.plugins, r)
			}
		}
	}
	if !needsOnly {
		in.limit, in.warnPct = budgetOf(m.liveCfg(), s)
		in.spent = m.use().spent
		in.autos = m.inboxAutos
	}
	return in
}

// inboxNeeds are the items that need the user right now.
func (m workspaceModel) inboxNeeds() []inboxItem {
	need, _ := buildInbox(m.inboxInputOf(true))
	return need
}

// inboxAll is every item, the ones that need the user first.
func (m workspaceModel) inboxAll() []inboxItem {
	need, info := buildInbox(m.inboxInputOf(false))
	return append(need, info...)
}

// inboxSidebarLine is "● 3 need you" in the warning colour; "" when nothing
// needs the user.
func (m workspaceModel) inboxSidebarLine() string {
	n := len(m.inboxNeeds())
	if n == 0 {
		return ""
	}
	verb := "need"
	if n == 1 {
		verb = "needs"
	}
	return " " + lipgloss.NewStyle().Foreground(ui.Warn).Bold(true).Render(fmt.Sprintf("● %d %s you", n, verb))
}

// ── Overlay ──────────────────────────────────────────────────────────────────

const wsInboxW = 72

// openInbox shows the inbox; the automation logs are read now.
func (m workspaceModel) openInbox() workspaceModel {
	m.inboxAutos = readAutoRuns(m.root)
	m.inboxIdx = 0
	m.overlay = wsOverlayInbox
	return m
}

func (m workspaceModel) inboxKey(k string) (tea.Model, tea.Cmd) {
	items := m.inboxAll()
	m.inboxIdx = min(m.inboxIdx, max(len(items)-1, 0))
	switch k {
	case "esc", "N", "q":
		m.overlay = wsOverlayNone
	case "up", "k":
		m.inboxIdx = max(m.inboxIdx-1, 0)
	case "down", "j":
		m.inboxIdx = min(m.inboxIdx+1, max(len(items)-1, 0))
	case "enter":
		if len(items) > 0 {
			return m.inboxJump(items[m.inboxIdx])
		}
	case "x":
		if len(items) > 0 && items[m.inboxIdx].info {
			id := items[m.inboxIdx].id
			m.sess.update(func(s *workspace.Session) { s.InboxDismissed = append(s.InboxDismissed, id) })
			m.saveSession()
			m.inboxIdx = min(m.inboxIdx, max(len(m.inboxAll())-1, 0))
		}
	}
	return m, nil
}

// inboxJump closes the overlay and goes to what the item is about.
func (m workspaceModel) inboxJump(it inboxItem) (tea.Model, tea.Cmd) {
	m.overlay = wsOverlayNone
	switch it.kind {
	case inboxReview:
		m.showGraph = false
		m.sel = it.ref
		m.sbSess = -1
		m.setFocus(wsFocusGraph)
		return m.feedReveal(it.ref), nil
	case inboxPlugin:
		return m.askPlugins(), nil
	case inboxBudget:
		return m.cmdBudget("")
	case inboxAutomation:
		var lines []string
		for _, l := range readLogTail(automation.LogPath(m.root, it.ref), 20) {
			lines = append(lines, wrapText(l, 70)...)
		}
		return m.openInfo("Automation "+it.ref, lines), nil
	}
	return m.selectNode(it.ref), nil
}

// selectNode selects a node, focuses the main pane and scrolls the feed to it.
func (m workspaceModel) selectNode(id string) workspaceModel {
	if m.g == nil || m.g.Node(id) == nil {
		return m
	}
	m.sel = id
	m.sbSess = -1
	m.setFocus(wsFocusGraph)
	if !m.showGraph {
		m = m.feedReveal(id)
	}
	return m
}

// inboxRows draws the item rows of the overlay in w cells and returns them
// with the line of the selected one.
func inboxRows(items []inboxItem, idx, w int) (lines []string, sel int) {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	lastInfo := false
	for i, it := range items {
		if it.info && !lastInfo {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, dim.Render("  Info"))
		}
		lastInfo = it.info
		if i == idx {
			sel = len(lines)
		}
		left := it.glyph + " " + padRight(it.label, 12) + " " + it.text
		lines = append(lines, ui.ListRow(ui.RowOpts{Left: left, Right: it.right, Width: w, Selected: i == idx}))
	}
	return lines, sel
}

// listWindow cuts lines to the vis rows that show row sel.
func listWindow(lines []string, sel, vis int) []string {
	if len(lines) <= vis {
		return lines
	}
	top := min(max(sel-vis+1, 0), len(lines)-vis)
	if sel < top {
		top = sel
	}
	return lines[top : top+vis]
}

func (m workspaceModel) inboxBox() string {
	boxW := min(wsInboxW, m.width-2)
	inner := boxW - 4
	items := m.inboxAll()
	idx := min(m.inboxIdx, max(len(items)-1, 0))
	need := 0
	for _, it := range items {
		if !it.info {
			need++
		}
	}
	var body []string
	if len(items) == 0 {
		body = []string{lipgloss.NewStyle().Foreground(ui.Muted).Render("Nothing needs you.")}
	} else {
		lines, sel := inboxRows(items, idx, inner)
		body = listWindow(lines, sel, max(m.height-8, 3))
	}
	hints := []ui.Hint{{Key: "Enter", Label: "go to"}, {Key: "x", Label: "clear info"}, {Key: "Esc", Label: "close"}}
	return ui.Overlay(fmt.Sprintf("Needs you · %d", need), body, hints, boxW)
}
