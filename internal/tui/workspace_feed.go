package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── Feed items ───────────────────────────────────────────────────────────────

type feedKind int

const (
	feedMsg    feedKind = iota // a transcript line
	feedNode                   // the card of a node that has started
	feedReview                 // the card of a lead review
)

// feedItem is one entry of the activity feed.
type feedItem struct {
	Kind feedKind
	At   time.Time
	Who  string // feedMsg: you | lead | system | error
	Text string // feedMsg
	Node string // feedNode: the node ID
	Rev  int    // feedReview: index into the session's reviews
}

// buildFeed merges the transcript and the nodes that have started into one
// list ordered by time. A node is listed once, at its start; nodes that never
// started are left out. Items with the same time keep the transcript first.
func buildFeed(lines []workspace.Line, g *workspace.Graph) []feedItem {
	return buildFeedWith(lines, g, nil)
}

// buildFeedWith is buildFeed with the lead reviews of the session, each at its
// own time.
func buildFeedWith(lines []workspace.Line, g *workspace.Graph, reviews []workspace.Review) []feedItem {
	items := make([]feedItem, 0, len(lines)+8)
	for _, l := range lines {
		items = append(items, feedItem{Kind: feedMsg, At: l.At, Who: l.Who, Text: l.Text})
	}
	if g != nil {
		for _, n := range g.Nodes {
			if !n.StartedAt.IsZero() {
				items = append(items, feedItem{Kind: feedNode, At: n.StartedAt, Node: n.ID})
			}
		}
	}
	for i, r := range reviews {
		items = append(items, feedItem{Kind: feedReview, At: r.At, Rev: i})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].At.Before(items[j].At) })
	return items
}

// feedSelectable is the IDs the arrow keys visit in the feed: nodes and review
// cards, in feed order.
func feedSelectable(items []feedItem, reviews []workspace.Review) []string {
	var ids []string
	for _, it := range items {
		switch it.Kind {
		case feedNode:
			ids = append(ids, it.Node)
		case feedReview:
			if it.Rev < len(reviews) {
				ids = append(ids, reviewKey(reviews[it.Rev]))
			}
		}
	}
	return ids
}

// feedNodeOrder is the IDs of the nodes in the feed, in feed order.
func feedNodeOrder(items []feedItem) []string {
	var ids []string
	for _, it := range items {
		if it.Kind == feedNode {
			ids = append(ids, it.Node)
		}
	}
	return ids
}

// feedDuration is a compact duration: "37s", "2m", "1m 10s", "1h 5m".
func feedDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0 && s == 0:
		return fmt.Sprintf("%dm", m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// feedClock is the local wall-clock time of t as HH:MM.
func feedClock(t time.Time) string { return t.Local().Format("15:04") }

// hardWrap wraps s into lines of at most w cells; a word longer than w is
// split.
func hardWrap(s string, w int) []string {
	if w < 1 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		wrapped := wrapText(para, w)
		if len(wrapped) == 0 {
			out = append(out, "")
			continue
		}
		for _, l := range wrapped {
			if ansi.StringWidth(l) <= w {
				out = append(out, l)
				continue
			}
			out = append(out, strings.Split(ansi.Hardwrap(l, w, true), "\n")...)
		}
	}
	return out
}

// ── Cache ────────────────────────────────────────────────────────────────────

// feedCache keeps the wrapped lines of messages, which never change once
// written, by width. It is shared by the copies of the model.
type feedCache struct{ lines map[string][]string }

const feedCacheMax = 2000

func (c *feedCache) get(key string) ([]string, bool) {
	if c == nil {
		return nil, false
	}
	l, ok := c.lines[key]
	return l, ok
}

func (c *feedCache) put(key string, l []string) {
	if c == nil {
		return
	}
	if c.lines == nil || len(c.lines) >= feedCacheMax {
		c.lines = map[string][]string{}
	}
	c.lines[key] = l
}

// ── Messages ─────────────────────────────────────────────────────────────────

// renderFeedMsg draws a chat message in at most w cells: a bold label with the
// time for you and the lead, then the wrapped text indented by two. System
// lines are dim, errors red.
func renderFeedMsg(who, text string, at time.Time, w int) []string {
	text = workspace.SanitizeText(text)
	dim := lipgloss.NewStyle().Foreground(colMuted)
	clock := ""
	if !at.IsZero() {
		clock = dim.Render("  " + feedClock(at))
	}
	var out []string
	switch who {
	case "you", "lead":
		label, col := "You", colText
		if who == "lead" {
			label, col = "Lead", colAccent
		}
		out = append(out, lipgloss.NewStyle().Foreground(col).Bold(true).Render(label)+clock)
		for _, l := range hardWrap(text, w-2) {
			out = append(out, "  "+lipgloss.NewStyle().Foreground(colText).Render(l))
		}
	case "system":
		for i, l := range hardWrap(text, w-2) {
			p := "  "
			if i == 0 {
				p = "· "
			}
			out = append(out, dim.Render(p+l))
		}
	default:
		for i, l := range hardWrap(text, w-2) {
			p := "  "
			if i == 0 {
				p = "! "
			}
			out = append(out, lipgloss.NewStyle().Foreground(colFail).Render(p+l))
		}
	}
	return out
}

// ── Node card ────────────────────────────────────────────────────────────────

const (
	cardSummaryLines = 3
	cardWaitingLines = 4
	cardFileRows     = 5
	cardErrorLines   = 3
	cardOutputLines  = 15
)

// cardOpts is everything a node card shows.
type cardOpts struct {
	g        *workspace.Graph
	n        *workspace.Node
	width    int // total width, borders included
	selected bool
	expanded bool
	now      time.Time
	spin     string // current spinner frame
	icons    string
	runner   string // of the node's role; "" when unassigned
	model    string
	out      []string // stored output lines
	iso      bool     // the session runs in worktrees
}

// lastNonEmpty is the last line of lines that is not blank.
func lastNonEmpty(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// tailPath shortens a path from the left to w cells: "…ts/version.go".
func tailPath(p string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(p) <= w {
		return p
	}
	if w == 1 {
		return "…"
	}
	return "…" + ansi.TruncateLeft(p, ansi.StringWidth(p)-(w-1), "")
}

// fileRow is one "Edited path   +18 −2" row of w cells.
func fileRow(st workspace.FileStat, hasCounts bool, w int) string {
	dim := lipgloss.NewStyle().Foreground(colMuted)
	verb := "Edited"
	if st.New {
		verb = "Added"
	}
	counts, cw := "", 0
	switch {
	case !hasCounts:
	case st.Binary:
		counts, cw = dim.Render("binary"), 6
	default:
		var parts []string
		var plain []string
		if st.Added > 0 || st.Removed == 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(colOK).Render(fmt.Sprintf("+%d", st.Added)))
			plain = append(plain, fmt.Sprintf("+%d", st.Added))
		}
		if st.Removed > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(colFail).Render(fmt.Sprintf("−%d", st.Removed)))
			plain = append(plain, fmt.Sprintf("−%d", st.Removed))
		}
		counts, cw = strings.Join(parts, " "), ansi.StringWidth(strings.Join(plain, " "))
	}
	room := w - len(verb) - 1
	if cw > 0 {
		room -= cw + 2
	}
	path := tailPath(st.Path, room)
	left := dim.Render(verb) + " " + lipgloss.NewStyle().Foreground(colText).Render(path)
	if cw == 0 {
		return left
	}
	gap := max(w-ansi.StringWidth(left)-cw, 2)
	return left + strings.Repeat(" ", gap) + counts
}

// cardFiles lists the file rows of a finished node, at most cardFileRows and a
// "+N more files" line.
func cardFiles(n *workspace.Node, w int) []string {
	stats := n.FileStats
	hasCounts := len(stats) > 0
	if !hasCounts {
		for _, p := range n.AllFiles() {
			stats = append(stats, workspace.FileStat{Path: p})
		}
	}
	var rows []string
	for i, st := range stats {
		if i == cardFileRows {
			more := len(stats) - cardFileRows
			rows = append(rows, lipgloss.NewStyle().Foreground(colMuted).Render(fmt.Sprintf("+%d more %s", more, pluralize(more, "file", "files"))))
			break
		}
		rows = append(rows, fileRow(st, hasCounts, w))
	}
	return rows
}

// cardHeader is the first row of a card: status glyph, ID and bold title on the
// left; runner glyph, model, start time and ↗ dim on the right.
func cardHeader(o cardOpts, iw int) string {
	n := o.n
	dim := lipgloss.NewStyle().Foreground(colMuted)
	glyph, _ := nodeStatusGlyph(o.g, n)
	if n.Status == workspace.StatusRunning && o.spin != "" {
		glyph = o.spin
	}
	left := lipgloss.NewStyle().Foreground(statusLabelColor(o.g, n)).Bold(true).Render(glyph) + " " + dim.Render(n.ID) + " "
	clock := attemptTag(n) + feedClock(n.StartedAt) + " ↗"
	var cands []string
	if o.runner != "" || o.model != "" {
		badge := runnerBadge(o.runner, o.icons)
		if o.model != "" {
			cands = append(cands, badge+dim.Render(" "+o.model+" · "+clock))
		}
		cands = append(cands, badge+dim.Render(" "+clock))
	}
	cands = append(cands, dim.Render(clock))
	right := cands[len(cands)-1]
	for _, c := range cands {
		if iw-ansi.StringWidth(left)-ansi.StringWidth(c)-1 >= 10 {
			right = c
			break
		}
	}
	room := max(iw-ansi.StringWidth(left)-ansi.StringWidth(right)-1, 1)
	title := lipgloss.NewStyle().Foreground(colText).Bold(true).Render(truncateOneLine(n.Title, room))
	line := left + title
	gap := max(iw-ansi.StringWidth(line)-ansi.StringWidth(right), 1)
	return line + strings.Repeat(" ", gap) + right
}

// cardBody is the rows under the header.
func cardBody(o cardOpts, iw int) []string {
	n := o.n
	dim := lipgloss.NewStyle().Foreground(colMuted)
	text := lipgloss.NewStyle().Foreground(colText)
	fail := lipgloss.NewStyle().Foreground(colFail)
	var rows []string
	took := func() time.Duration {
		if n.EndedAt.IsZero() || n.EndedAt.Before(n.StartedAt) {
			return -1
		}
		return n.EndedAt.Sub(n.StartedAt)
	}
	cmds := func() {
		for i, c := range n.Commands {
			if i == 3 {
				rows = append(rows, dim.Render(fmt.Sprintf("+%d more", len(n.Commands)-3)))
				break
			}
			rows = append(rows, dim.Render(truncateOneLine("$ "+c, iw)))
		}
	}
	check := n.Kind == workspace.KindCheck
	hasAct := len(n.Activity) > 0
	hasOut := len(o.out) > 0 || hasAct
	arrow := ""
	if hasOut {
		arrow = " ›"
		if o.expanded {
			arrow = " ⌄"
		}
	}

	switch n.Status {
	case workspace.StatusRunning:
		sp := o.spin
		if sp == "" {
			sp = "▖"
		}
		work := text.Render("Working for "+feedDuration(o.now.Sub(n.StartedAt))) + dim.Render(cardMeta(n))
		if s := stallText(n, o.now); s != "" {
			work += dim.Render(" · ") + lipgloss.NewStyle().Foreground(colWarn).Render(s)
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(colAccent).Render(sp)+" "+work+dim.Render(arrow))
		if check && !hasAct {
			cmds()
		}
		switch {
		case hasAct && !o.expanded:
			from := max(len(n.Activity)-cardActivityRows, 0)
			for _, a := range n.Activity[from:] {
				rows = append(rows, activityLine(a, o.now, iw))
			}
		case !hasAct:
			if last := lastNonEmpty(o.out); last != "" {
				rows = append(rows, dim.Render(truncateOneLine(cleanOutputLine(last), iw)))
			}
		}
	case workspace.StatusDone:
		word := "Worked for "
		if check {
			word = "Passed in "
		}
		line := "Done"
		if d := took(); d >= 0 {
			line = word + feedDuration(d)
		}
		if check {
			rows = append(rows, lipgloss.NewStyle().Foreground(colOK).Render(line)+dim.Render(cardMeta(n)+arrow))
			cmds()
		} else {
			rows = append(rows, dim.Render(line+cardMeta(n)+arrow))
			rows = append(rows, summaryRows(n.Summary, iw)...)
			rows = append(rows, cardFiles(n, iw)...)
			if hasDiff(n) {
				rows = append(rows, dim.Render("d diff"))
			}
		}
		if check && o.iso {
			rows = append(rows, dim.Render("/merge to apply"))
		}
	case workspace.StatusWaiting:
		line := ""
		if d := took(); d >= 0 {
			line = " · Worked for " + feedDuration(d)
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(colWarn).Render("◇ waiting")+dim.Render(line+cardMeta(n)+arrow))
		rows = append(rows, waitingRows(n, iw)...)
		rows = append(rows, cardFiles(n, iw)...)
		hint := "m reply"
		if hasDiff(n) {
			hint += " · d diff"
		}
		rows = append(rows, dim.Render(hint))
	case workspace.StatusFailed:
		word := "Failed after "
		if n.Error == "stopped" {
			word = "Stopped after "
		}
		line := "Failed"
		if n.Error == "stopped" {
			line = "Stopped"
		}
		if d := took(); d >= 0 {
			line = word + feedDuration(d)
		}
		rows = append(rows, fail.Render(line)+dim.Render(cardMeta(n)+arrow))
		if n.Error != "" && n.Error != "stopped" {
			errLines := hardWrap(workspace.SanitizeText(n.Error), iw)
			for i, l := range errLines {
				if i == cardErrorLines {
					rows[len(rows)-1] = fail.Render(truncateOneLine(errLines[cardErrorLines-1]+" …", iw))
					break
				}
				rows = append(rows, fail.Render(l))
			}
		}
		hint := "r retry · a change agent"
		if check {
			hint = "r retry · f add fix"
		}
		rows = append(rows, dim.Render(truncateOneLine(hint, iw)))
	default:
		_, label := nodeStatusGlyph(o.g, n)
		rows = append(rows, dim.Render(label))
	}

	if len(n.Warnings) > 0 && n.Status != workspace.StatusRunning {
		rows = append(rows, dim.Render("⚠ "+fmt.Sprintf("%d %s", len(n.Warnings), pluralize(len(n.Warnings), "warning", "warnings"))))
	}
	rows = append(rows, replyLines(n, iw)...)
	if o.expanded && hasAct {
		rows = append(rows, activityTimelineRows(n, o.now, iw)...)
	} else if o.expanded {
		rows = append(rows, dim.Render(strings.Repeat("─", min(iw, 24))))
		if !hasOut {
			rows = append(rows, dim.Render("(no output kept)"))
		}
		tail := o.out
		if len(tail) > cardOutputLines {
			tail = tail[len(tail)-cardOutputLines:]
		}
		for _, l := range tail {
			rows = append(rows, dim.Render(truncateOneLine(cleanOutputLine(l), iw)))
		}
	}
	if o.expanded {
		rows = append(rows, attemptRows(n, o.now, iw)...)
	}
	return rows
}

// waitingRows is what a waiting node needs from the user, in the warning
// colour: "? question" or "blocked: summary", wrapped to at most
// cardWaitingLines rows.
func waitingRows(n *workspace.Node, iw int) []string {
	text := workspace.SanitizeText(n.Summary)
	prefix := "blocked: "
	if r := n.Report; r != nil {
		if r.Status == workspace.ReportQuestion {
			prefix = "? "
			text = r.Ask()
		} else if s := strings.TrimSpace(r.Summary); s != "" {
			text = s
		}
		text = workspace.SanitizeText(text)
	}
	text = strings.TrimSpace(text)
	lines := hardWrap(oneLine(prefix+text), iw)
	if len(lines) > cardWaitingLines {
		lines = lines[:cardWaitingLines]
		lines[cardWaitingLines-1] = truncateOneLine(lines[cardWaitingLines-1]+" …", iw)
	}
	st := lipgloss.NewStyle().Foreground(colWarn)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = st.Render(l)
	}
	return out
}

// summaryRows is the node summary in the text colour, wrapped to at most
// cardSummaryLines lines, the last ending in "…" when there is more.
func summaryRows(s string, iw int) []string {
	s = oneLine(workspace.SanitizeText(s))
	if s == "" {
		return nil
	}
	lines := hardWrap(s, iw)
	if len(lines) > cardSummaryLines {
		lines = lines[:cardSummaryLines]
		lines[cardSummaryLines-1] = truncateOneLine(lines[cardSummaryLines-1]+" …", iw)
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = lipgloss.NewStyle().Foreground(colText).Render(l)
	}
	return out
}

// renderNodeCard draws a node card in a rounded border of exactly o.width
// cells: colAccent when selected, colFail when failed.
func renderNodeCard(o cardOpts) []string {
	iw := o.width - 4
	if iw < 4 {
		iw = 4
	}
	rows := append([]string{cardHeader(o, iw)}, cardBody(o, iw)...)
	bc := colBorder
	switch {
	case o.selected:
		bc = colAccent
	case o.n.Status == workspace.StatusFailed:
		bc = colFail
	}
	b := lipgloss.NewStyle().Foreground(bc)
	out := make([]string, 0, len(rows)+2)
	out = append(out, b.Render("╭"+strings.Repeat("─", iw+2)+"╮"))
	for _, r := range rows {
		out = append(out, b.Render("│")+" "+padRight(ansi.Truncate(r, iw, "…"), iw)+" "+b.Render("│"))
	}
	return append(out, b.Render("╰"+strings.Repeat("─", iw+2)+"╯"))
}

// ── Layout ───────────────────────────────────────────────────────────────────

// feedIndent is the left gutter of the feed.
const feedIndent = 2

// feedWidth is the width of messages and cards in a pane of width pw.
func feedWidth(pw int) int { return max(pw-feedIndent-2, 12) }

// roleLooks maps role names to their short model and runner.
func (m workspaceModel) roleLooks() (models, runners map[string]string) {
	models, runners = map[string]string{}, map[string]string{}
	for _, r := range workspace.Roles(m.cfg) {
		runners[r.Name] = runnerKey(r.Agent.Runner)
		models[r.Name] = shortModel(r.Agent.Runner, effectiveModel(m.cfg, r.Agent))
		if models[r.Name] == "" {
			models[r.Name] = runners[r.Name]
		}
	}
	return models, runners
}

// feedLayout renders the whole feed for a pane of width pw: the lines, and the
// row span [start, end) of every node card.
func (m workspaceModel) feedLayout(pw int) (lines []string, spans map[string][2]int) {
	cw := feedWidth(pw)
	spans = map[string][2]int{}
	snap := m.sess.snapshot()
	items := buildFeedWith(snap.Transcript, m.g, snap.Reviews)
	models, runners := m.roleLooks()
	spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
	pad := strings.Repeat(" ", feedIndent)
	prevSystem := false
	for i, it := range items {
		var body []string
		revKey := ""
		isSystem := false
		switch it.Kind {
		case feedMsg:
			key := fmt.Sprintf("%d|%s|%d|%s", cw, it.Who, it.At.UnixNano(), it.Text)
			var ok bool
			if body, ok = m.feedCache.get(key); !ok {
				body = renderFeedMsg(it.Who, it.Text, it.At, cw)
				m.feedCache.put(key, body)
			}
			isSystem = it.Who == "system"
		case feedNode:
			n := m.g.Node(it.Node)
			if n == nil {
				continue
			}
			o := cardOpts{g: m.g, n: n, width: cw, selected: n.ID == m.sel, expanded: m.feedExpanded[n.ID],
				now: m.now(), spin: spin, icons: m.cfg.UI.Icons, iso: m.iso != nil,
				runner: runners[n.Role], model: models[n.Role]}
			if n.Status == workspace.StatusRunning || o.expanded || n.Status == workspace.StatusDone || n.Status == workspace.StatusFailed || n.Status == workspace.StatusWaiting {
				o.out = m.outputs[n.ID]
				if p := cleanOutputLine(m.partial[n.ID]); p != "" && n.Status == workspace.StatusRunning {
					o.out = append(append([]string(nil), o.out...), p)
				}
			}
			body = renderNodeCard(o)
		case feedReview:
			if it.Rev >= len(snap.Reviews) {
				continue
			}
			rv := snap.Reviews[it.Rev]
			revKey = reviewKey(rv)
			body = renderReviewCard(rv, cw, revKey == m.sel, m.feedExpanded[revKey])
		}
		if i > 0 && (!prevSystem || !isSystem) && len(lines) > 0 {
			lines = append(lines, "")
		}
		start := len(lines)
		for _, l := range body {
			lines = append(lines, pad+l)
		}
		if it.Kind == feedNode {
			spans[it.Node] = [2]int{start, len(lines)}
		}
		if revKey != "" {
			spans[revKey] = [2]int{start, len(lines)}
		}
		prevSystem = isSystem
	}
	if m.splitting {
		spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, pad+lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render("Lead")+"  "+
			lipgloss.NewStyle().Foreground(colText).Render(spin+" splitting the task…  (Esc cancels)"))
		if m.stream != nil {
			if last := m.stream.lastLine(); last != "" {
				lines = append(lines, pad+"  "+lipgloss.NewStyle().Foreground(colMuted).Render(truncateOneLine(last, cw-2)))
			}
		}
	}
	if m.reviewing {
		spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, pad+lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render("Lead")+"  "+
			lipgloss.NewStyle().Foreground(colText).Render(spin+" reviewing the batch…  (Esc cancels)"))
	}
	return lines, spans
}

// feedWindow picks the visible rows: h rows from the bottom when following,
// else from row top. It returns the rows and the clamped top.
func feedWindow(lines []string, h, top int, follow bool) ([]string, int) {
	maxTop := max(len(lines)-h, 0)
	if follow || top > maxTop {
		top = maxTop
	}
	if top < 0 {
		top = 0
	}
	end := min(top+h, len(lines))
	return lines[top:end], top
}

// feedPaneSize is the size of the feed area without its label row.
func (m workspaceModel) feedPaneSize() (pw, h int) {
	pw = m.mainWidth()
	h = m.mainHeight() - 1
	return pw, max(h, 1)
}

// feedTopNow is the first visible row of the feed.
func (m workspaceModel) feedTopNow(lines []string, h int) int {
	_, top := feedWindow(lines, h, m.feedTop, m.feedFollow)
	return top
}

// feedScrollBy moves the feed by delta rows (negative is up). Reaching the
// bottom resumes following.
func (m workspaceModel) feedScrollBy(delta int) workspaceModel {
	pw, h := m.feedPaneSize()
	lines, _ := m.feedLayout(pw)
	top := m.feedTopNow(lines, h) + delta
	return m.feedSetTop(lines, h, top)
}

func (m workspaceModel) feedSetTop(lines []string, h, top int) workspaceModel {
	maxTop := max(len(lines)-h, 0)
	top = min(max(top, 0), maxTop)
	m.feedTop = top
	m.feedFollow = top >= maxTop
	return m
}

// feedReveal scrolls the card of node id into view; a card taller than the
// pane shows its top.
func (m workspaceModel) feedReveal(id string) workspaceModel {
	pw, h := m.feedPaneSize()
	lines, spans := m.feedLayout(pw)
	sp, ok := spans[id]
	if !ok {
		return m
	}
	top := m.feedTopNow(lines, h)
	switch {
	case sp[0] < top || sp[1]-sp[0] >= h:
		top = sp[0]
	case sp[1] > top+h:
		top = sp[1] - h
	default:
		return m
	}
	return m.feedSetTop(lines, h, top)
}

// feedSelect moves the selection to the previous (dir < 0) or next card of the
// feed and scrolls it into view.
func (m workspaceModel) feedSelect(dir int) workspaceModel {
	snap := m.sess.snapshot()
	ids := feedSelectable(buildFeedWith(snap.Transcript, m.g, snap.Reviews), snap.Reviews)
	if len(ids) == 0 {
		return m
	}
	idx := -1
	for i, id := range ids {
		if id == m.sel {
			idx = i
		}
	}
	switch {
	case idx < 0 && dir < 0:
		idx = len(ids) - 1
	case idx < 0:
		idx = 0
	default:
		idx = min(max(idx+dir, 0), len(ids)-1)
	}
	m.sel = ids[idx]
	return m.feedReveal(m.sel)
}

// feedPane draws the activity pane in exactly h rows of w cells: the label
// row, then the feed or the empty state.
func (m workspaceModel) feedPane(w, h int) []string {
	dim := lipgloss.NewStyle().Foreground(colMuted)
	label := paneLabel("Activity", m.focus != wsFocusPrompt) + dim.Render(" · g graph")
	if !m.feedFollow {
		tag := "G latest ↓"
		if gap := w - ansi.StringWidth(label) - ansi.StringWidth(tag) - 2; gap > 0 {
			label += strings.Repeat(" ", gap) + dim.Render(tag)
		}
	}
	out := []string{label}
	if m.feedEmpty() {
		for _, l := range m.emptyPane(w-4, h-1) {
			out = append(out, "  "+l)
		}
	} else {
		lines, _ := m.feedLayout(w)
		vis, _ := feedWindow(lines, h-1, m.feedTop, m.feedFollow)
		out = append(out, vis...)
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}

// feedEmpty is true before the first message and node: the pane then shows the
// start screen.
func (m workspaceModel) feedEmpty() bool {
	return len(m.sess.snapshot().Transcript) == 0 && !m.hasNodes() && !m.splitting
}
