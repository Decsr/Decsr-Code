package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	wsLiveMinHeight = 30  // the live pane shows from this terminal height
	wsLiveRows      = 10  // rows of the live pane
	wsLiveSideMin   = 100 // the changed-files column shows from this width
	wsLiveSideMax   = 40
	wsLivePaneMin   = 64  // the pane needs this width for a changed-files column
	wsOutTwoPane    = 120 // the output screen shows two panes from this width
	wsOutDiffEvery  = 2 * time.Second
)

// ── Running line ─────────────────────────────────────────────────────────────

// tokCompact is a token count as "512", "2.1k", "182k" or "1.2M".
func tokCompact(n int) string {
	if n >= 999_500 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	}
	return ctxK(n)
}

// tokTotalLabel is "184k tok", the tokens in and out; "" before any are known.
func tokTotalLabel(n *workspace.Node) string {
	total := n.InputTokens + n.OutputTokens
	if total <= 0 {
		return ""
	}
	est := ""
	if n.Estimated {
		est = "~"
	}
	return est + tokCompact(total) + " tok"
}

// liveModelName is the model shown for a node: its own, else the role's, else
// "(default)", without a leading "vendor/".
func liveModelName(n *workspace.Node, roleModel string) string {
	model := n.Model
	if model == "" {
		model = roleModel
	}
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	if model == "" {
		return "(default)"
	}
	return model
}

// runningLine is "⠋ claude · opus-5-5 · effort medium · 1m 12s · ↑ 182k
// (164k cached) ↓ 2.1k". The spinner shows while the node runs; a finished
// node shows ✓ or ✗ and its final duration.
func runningLine(n *workspace.Node, runnerName, roleModel, spin string, now time.Time) string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	text := lipgloss.NewStyle().Foreground(ui.Text)
	var glyph string
	end := n.EndedAt
	switch n.Status {
	case workspace.StatusRunning:
		if spin == "" {
			spin = "▖"
		}
		glyph = lipgloss.NewStyle().Foreground(ui.Accent).Render(spin)
		end = now
	case workspace.StatusDone:
		glyph = lipgloss.NewStyle().Foreground(ui.OK).Render("✓")
	case workspace.StatusFailed:
		glyph = lipgloss.NewStyle().Foreground(ui.Fail).Render("✗")
	default:
		glyph = dim.Render("·")
	}
	parts := []string{}
	if runnerName != "" {
		parts = append(parts, text.Render(runnerName))
	}
	parts = append(parts, text.Render(liveModelName(n, roleModel)))
	if n.Effort != "" {
		parts = append(parts, dim.Render("effort "+n.Effort))
	}
	if !n.StartedAt.IsZero() && !end.IsZero() && !end.Before(n.StartedAt) {
		parts = append(parts, text.Render(feedDuration(end.Sub(n.StartedAt))))
	}
	if n.InputTokens > 0 || n.OutputTokens > 0 {
		est := ""
		if n.Estimated {
			est = "~"
		}
		tok := "↑ " + est + tokCompact(n.InputTokens)
		if n.CachedTokens > 0 {
			tok += " (" + est + tokCompact(n.CachedTokens) + " cached)"
		}
		tok += " ↓ " + est + tokCompact(n.OutputTokens)
		parts = append(parts, dim.Render(tok))
	}
	return glyph + " " + strings.Join(parts, dim.Render(" · "))
}

// nodeLooks is the runner name and the effective model of the role of n.
func (m workspaceModel) nodeLooks(n *workspace.Node) (runnerName, roleModel string) {
	r, ok := workspace.FindRole(m.cfg, n.Role)
	if !ok {
		return "", ""
	}
	return runnerKey(r.Agent.Runner), effectiveModel(m.cfg, r.Agent)
}

// runLine is the running line of n with the current spinner frame, cut to w.
func (m workspaceModel) runLine(n *workspace.Node, w int) string {
	rn, rm := m.nodeLooks(n)
	spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
	return ansi.Truncate(runningLine(n, rn, rm, spin, m.now()), max(w, 1), "…")
}

// ── Changed files of a running node ──────────────────────────────────────────

type liveFile struct {
	path           string
	added, removed int
}

// liveFiles is the files the node edited so far, summed from its activity,
// the newest first.
func liveFiles(n *workspace.Node) []liveFile {
	idx := map[string]int{}
	last := map[string]int{}
	var out []liveFile
	for i, a := range n.Activity {
		if (a.Kind != runner.ActEdit && a.Kind != runner.ActWrite) || a.State == runner.ActFailed {
			continue
		}
		p := oneLine(workspace.SanitizeText(a.Title))
		if p == "" || p == "patch" {
			continue
		}
		j, ok := idx[p]
		if !ok {
			j = len(out)
			idx[p] = j
			out = append(out, liveFile{path: p})
		}
		out[j].added += a.Added
		out[j].removed += a.Removed
		last[p] = i
	}
	sort.SliceStable(out, func(a, b int) bool { return last[out[a].path] > last[out[b].path] })
	return out
}

// ── Bottom pane of the graph screen ──────────────────────────────────────────

// liveHeight is the rows the live pane takes of a main area mainH rows tall;
// 0 when it is not shown: it needs the graph, a running selection, a terminal
// at least wsLiveMinHeight rows tall and room left for the graph.
func (m workspaceModel) liveHeight(mainH int) int {
	if !m.showGraph || m.liveHide || m.height < wsLiveMinHeight {
		return 0
	}
	n := m.selNode()
	if n == nil || n.Status != workspace.StatusRunning {
		return 0
	}
	h := min(wsLiveRows, mainH-5)
	if h < 6 {
		return 0
	}
	return h
}

// liveLines is the live pane: a title row, the running line, then the last
// steps on the left and the changed files on the right, in exactly h rows of w
// cells.
func (m workspaceModel) liveLines(n *workspace.Node, w, h int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	line := lipgloss.NewStyle().Foreground(ui.Border)
	title := " Live · " + n.ID + " "
	hint := " v hide "
	fill := max(w-2-ansi.StringWidth(title)-ansi.StringWidth(hint)-1, 0)
	out := []string{"  " + dim.Render(strings.TrimSpace(title)) + " " + line.Render(strings.Repeat("─", fill)) + dim.Render(hint)}
	out[0] = ansi.Truncate(out[0], w, "")
	out = append(out, "  "+m.runLine(n, w-4))
	bodyH := h - len(out)

	files := liveFiles(n)
	side := m.width >= wsLiveSideMin && w >= wsLivePaneMin
	lw, rw := w, 0
	if side {
		rw = min(wsLiveSideMax, w/3)
		lw = w - rw - 1
	}
	left := m.liveSteps(n, lw-2, bodyH)
	if !side && len(files) > 0 {
		left[bodyH-1] = dim.Render(fmt.Sprintf("%d %s changed", len(files), pluralize(len(files), "file", "files")))
	}
	var right []string
	if side {
		right = liveFileRows(files, rw-1, bodyH)
	}
	div := ui.VDivider(1)
	for i := 0; i < bodyH; i++ {
		l := "  " + left[i]
		if side {
			out = append(out, padRight(ansi.Truncate(l, lw, ""), lw)+div+" "+right[i])
		} else {
			out = append(out, ansi.Truncate(l, w, ""))
		}
	}
	return out
}

// liveSteps is the last steps of n, rows of them, the newest at the bottom;
// the step in progress shows the spinner.
func (m workspaceModel) liveSteps(n *workspace.Node, w, rows int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	acts := n.Activity
	if len(acts) > rows {
		acts = acts[len(acts)-rows:]
	}
	out := make([]string, 0, rows)
	if len(acts) == 0 {
		out = append(out, dim.Render("waiting for the first step…"))
	}
	spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
	for i, a := range acts {
		l := activityLine(a, m.now(), w)
		if a.State == runner.ActRunning && i == len(acts)-1 {
			l = strings.Replace(l, actGlyph(a.Kind), lipgloss.NewStyle().Foreground(ui.Accent).Render(spin), 1)
		}
		out = append(out, l)
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

// liveFileRows is the changed files as "path +a −r" rows of w cells.
func liveFileRows(files []liveFile, w, rows int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	out := make([]string, 0, rows)
	if len(files) == 0 {
		out = append(out, dim.Render("no file changes yet"))
	}
	for i, f := range files {
		if i == rows {
			break
		}
		if i == rows-1 && len(files) > rows {
			out = append(out, dim.Render(fmt.Sprintf("+%d more", len(files)-i)))
			break
		}
		counts := ""
		if f.added+f.removed > 0 {
			counts = diffCounts(f.added, f.removed)
		}
		cw := ansi.StringWidth(diffCountsPlain(f.added, f.removed))
		if counts == "" {
			cw = 0
		}
		name := tailPath(f.path, max(w-cw-1, 4))
		gap := max(w-ansi.StringWidth(name)-cw, 1)
		out = append(out, lipgloss.NewStyle().Foreground(ui.Text).Render(name)+strings.Repeat(" ", gap)+counts)
	}
	for len(out) < rows {
		out = append(out, "")
	}
	for i := range out {
		out[i] = ansi.Truncate(out[i], w, "")
	}
	return out
}

// ── Output screen: the combined timeline and changes ─────────────────────────

// wsOutDiffMsg carries a diff loaded off the UI thread for the output screen.
type wsOutDiffMsg struct {
	node string
	d    workspace.Diff
	err  error
}

// outCombined reports whether the output screen shows the timeline and the
// changes (it shows the raw text after t).
func (m workspaceModel) outCombined() bool { return m.outTimelineOn() }

// outGeom is the layout of the combined screen: whether two panes sit side by
// side, their widths and the rows of the body (below the label row).
func (m workspaceModel) outGeom() (two bool, lw, rw, bodyH int) {
	w := m.width
	bodyH = max(m.height-4, 1)
	if w >= wsOutTwoPane {
		lw = (w - 1) * 2 / 5
		return true, lw, w - 1 - lw, bodyH
	}
	return false, w, w, bodyH
}

// sizeOutVP sizes the timeline viewport to its pane.
func (m *workspaceModel) sizeOutVP() {
	if !m.outCombined() {
		m.outVP.Width, m.outVP.Height = max(m.width, 1), max(m.height-3, 1)
		return
	}
	_, lw, _, bodyH := m.outGeom()
	m.outVP.Width, m.outVP.Height = max(lw, 1), max(bodyH-1, 1)
}

// startOutDiff loads the diff of the output node off the UI thread. A running
// node is reloaded while it works; one request at a time is in flight and a
// request that comes meanwhile is queued as one more.
func (m workspaceModel) startOutDiff() (workspaceModel, tea.Cmd) {
	n := m.g.Node(m.outNode)
	if m.screen != wsScreenOutput || !m.outCombined() || n == nil || m.outArchived(n) != nil {
		return m, nil
	}
	if m.outDiffBusy {
		m.outDiffDirty = true
		return m, nil
	}
	m.outDiffBusy, m.outDiffDirty = true, false
	m.outDiffAt = m.now()
	nc := *n
	root, iso := m.root, m.iso
	return m, func() tea.Msg {
		d, err := workspace.NodeDiff(root, &nc, iso)
		return wsOutDiffMsg{node: nc.ID, d: d, err: err}
	}
}

// onOutDiff stores a loaded diff, keeping the selected file and the scroll.
func (m workspaceModel) onOutDiff(msg wsOutDiffMsg) (workspaceModel, tea.Cmd) {
	if msg.node != m.outNode || m.screen != wsScreenOutput {
		return m, nil
	}
	m.outDiffBusy = false
	m.outDiff = mergeOutDiff(m.outDiff, m.outNode, msg.d, msg.err)
	if n := m.g.Node(m.outNode); m.outDiffDirty && n != nil && n.Status == workspace.StatusRunning {
		return m.startOutDiff()
	}
	return m, nil
}

// mergeOutDiff is the view for a new diff of node: the file under the cursor,
// the cursor and the scroll of old stay when the file is still there. A failed
// reload keeps the old view.
func mergeOutDiff(old *wsDiffView, node string, d workspace.Diff, err error) *wsDiffView {
	if err != nil && old != nil && len(old.diff.Files) > 0 {
		return old
	}
	v := &wsDiffView{nodeID: node, diff: d, rows: diffRows(d)}
	if err != nil {
		v.err = oneLine(err.Error())
	}
	if old != nil {
		v.hx = old.hx
		if f := old.curFile(); f != nil {
			for i := range d.Files {
				if d.Files[i].Path == f.Path {
					v.file, v.cur, v.top = i, old.cur, old.top
					break
				}
			}
		}
	}
	v.clamp()
	return v
}

// outDiffTick starts a reload when a running node's diff is older than the
// fallback interval.
func (m workspaceModel) outDiffTick() (workspaceModel, tea.Cmd) {
	n := m.g.Node(m.outNode)
	if m.screen != wsScreenOutput || n == nil || n.Status != workspace.StatusRunning || m.now().Sub(m.outDiffAt) < wsOutDiffEvery {
		return m, nil
	}
	return m.startOutDiff()
}

// changesKey handles the keys of the changes pane; it reports whether it used k.
func (m workspaceModel) changesKey(k string) (workspaceModel, bool) {
	v := m.outDiff
	if v == nil {
		return m, false
	}
	page := max(v.body-1, 1)
	switch k {
	case "j", "down":
		v.move(1)
	case "k", "up":
		v.move(-1)
	case "pgdown", "ctrl+d", " ", "space":
		v.move(page)
	case "pgup", "ctrl+u":
		v.move(-page)
	case "g", "home":
		v.cur = 0
	case "G", "end":
		v.cur = max(len(v.rowsOf())-1, 0)
	case "n":
		v.setFile(v.file + 1)
	case "p":
		v.setFile(v.file - 1)
	case "right", "l":
		v.hx += wsDiffHScroll
	case "left", "h":
		v.hx = max(v.hx-wsDiffHScroll, 0)
	case "0":
		v.hx = 0
	default:
		return m, false
	}
	v.clamp()
	return m, true
}

// outChangesPane is the changes pane: a label row, the file list, a rule with
// the file path and the unified diff of the selected file; rows lines of w
// cells.
func (m workspaceModel) outChangesPane(n *workspace.Node, w, rows int, focused bool) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	v := m.outDiff
	label := "Changes"
	if v != nil && len(v.diff.Files) > 0 {
		a, r := v.diff.Totals()
		label += fmt.Sprintf(" · %d %s %s", len(v.diff.Files), pluralize(len(v.diff.Files), "file", "files"), diffCountsPlain(a, r))
	}
	out := []string{ui.PaneLabel(label, focused, w)}
	msg := func(s string) []string { return append(out, "  "+dim.Render(truncateOneLine(s, max(w-4, 1)))) }
	switch {
	case n == nil:
		return msg("")
	case m.outArchived(n) != nil:
		return msg("Changes of earlier attempts are not kept.")
	case v == nil:
		return msg("loading…")
	case v.err != "":
		return msg("! " + v.err)
	case len(v.diff.Files) == 0:
		if n.Status == workspace.StatusRunning {
			return msg("No file changes yet.")
		}
		return msg("No changes in this node.")
	}
	body := rows - 1
	fl := min(len(v.diff.Files), max(body/3, 1))
	mm := m
	mm.diff = v
	for _, l := range mm.diffFileList(w, fl) {
		out = append(out, l)
	}
	if rest := body - fl - 1; rest >= 1 {
		f := v.curFile()
		rule := ""
		if f != nil {
			rule = fmt.Sprintf(" %s ", truncateOneLine(f.Path, max(w-8, 4)))
		}
		out = append(out, lipgloss.NewStyle().Foreground(ui.Border).Render("──")+dim.Render(rule)+
			lipgloss.NewStyle().Foreground(ui.Border).Render(strings.Repeat("─", max(w-2-ansi.StringWidth(rule), 0))))
		v.body = rest
		out = append(out, mm.diffPane(w, rest)...)
	}
	return out
}

// outCombinedBody is the body of the combined screen: the timeline and the
// changes side by side, or the focused one alone.
func (m workspaceModel) outCombinedBody(n *workspace.Node) []string {
	two, lw, rw, bodyH := m.outGeom()
	rows := bodyH
	timeline := func(w int, focused bool) []string {
		vp := m.outVP
		vp.Width, vp.Height = w, rows-1
		return append([]string{ui.PaneLabel("Timeline", focused, w)}, strings.Split(vp.View(), "\n")...)
	}
	fit := func(ls []string, w int) []string {
		for len(ls) < rows {
			ls = append(ls, "")
		}
		ls = ls[:rows]
		for i := range ls {
			ls[i] = padRight(ansi.Truncate(ls[i], w, ""), w)
		}
		return ls
	}
	if !two {
		if m.outPane == 1 {
			return fit(m.outChangesPane(n, lw, rows, true), lw)
		}
		return fit(timeline(lw, true), lw)
	}
	l := fit(timeline(lw, m.outPane == 0), lw)
	r := fit(m.outChangesPane(n, rw, rows, m.outPane == 1), rw)
	div := ui.VDivider(1)
	out := make([]string, rows)
	for i := range out {
		out[i] = l[i] + div + r[i]
	}
	return out
}

// combinedView is the output screen with the timeline and the changes.
func (m workspaceModel) combinedView(n *workspace.Node, title string) string {
	w := m.width
	hints := []keyHint{{"↑↓", "step"}, {"Enter", "expand"}, {"Tab", "changes"}, {"d", "diff"}, {"t", "raw"}, {"s", "stop"}, {"Esc", "back"}, {"?", "help"}}
	if m.outPane == 1 {
		hints = []keyHint{{"↑↓", "scroll"}, {"n/p", "file"}, {"Tab", "timeline"}, {"d", "diff"}, {"t", "raw"}, {"s", "stop"}, {"Esc", "back"}, {"?", "help"}}
	}
	if n != nil && len(n.Attempts) > 0 {
		hints = append([]keyHint{{"[ ]", "attempt"}}, hints...)
	}
	run := ""
	if n != nil {
		run = "  " + m.runLine(n, w-4)
	}
	parts := []string{m.headerLine(), "  " + title, run}
	parts = append(parts, m.outCombinedBody(n)...)
	parts = append(parts, wsHintBar(w, fitHints(w, hints)))
	return strings.Join(parts, "\n")
}
