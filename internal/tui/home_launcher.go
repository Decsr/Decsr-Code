package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// homeWideMin is the width from which the launcher shows two columns; below
// it the columns are stacked, actions first.
const homeWideMin = 100

// homeMaxWidth is the widest the launcher gets; a wider terminal centres it.
const homeMaxWidth = 150

// homeLeftRow is one selectable row of the left column: a recent project or a
// session of the current project.
type homeLeftRow struct {
	label   string
	path    string // recent project directory
	session string // session ID; empty for a project
	info    workspace.SessionInfo
}

// leftRows lists the selectable rows of the left column: recent projects, then
// the sessions of the current project.
func (m HomeModel) leftRows() []homeLeftRow {
	rows := make([]homeLeftRow, 0, len(m.recentProjects)+len(m.sessions))
	for _, p := range m.recentProjects {
		name := p.Name
		if name == "" {
			name = filepath.Base(p.Path)
		}
		rows = append(rows, homeLeftRow{label: name, path: p.Path})
	}
	for _, s := range m.sessions {
		title := s.Title
		if title == "" {
			title = "untitled"
		}
		rows = append(rows, homeLeftRow{label: title, session: s.ID, info: s})
	}
	return rows
}

// sessionKind is the ui.Status kind that sums up a session.
func sessionKind(s workspace.SessionInfo) string {
	total := s.Todo + s.Running + s.Done + s.Failed
	switch {
	case s.Failed > 0:
		return "failed"
	case s.Waiting > 0:
		return "waiting"
	case s.Running > 0:
		return "running"
	case total > 0 && s.Done == total:
		return "done"
	}
	return "todo"
}

// sessionMeta is "2/5 · 1 waiting" or "5/5 · 2d": progress, then what needs
// attention, else the age.
func sessionMeta(s workspace.SessionInfo, now time.Time) string {
	total := s.Todo + s.Running + s.Done + s.Failed
	if total == 0 {
		return sbAge(now, s.UpdatedAt)
	}
	out := fmt.Sprintf("%d/%d", s.Done, total)
	switch {
	case s.Waiting > 0:
		out += fmt.Sprintf(" · %d waiting", s.Waiting)
	case s.Failed > 0:
		out += fmt.Sprintf(" · %d failed", s.Failed)
	case s.Running > 0:
		out += fmt.Sprintf(" · %d running", s.Running)
	default:
		out += " · " + sbAge(now, s.UpdatedAt)
	}
	return out
}

// tildePath shortens the user's home directory to "~".
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + strings.TrimPrefix(p, home)
		}
	}
	return p
}

// row renders one list row. A selected row in the column without focus keeps
// only a dim cursor, so the focus is always clear.
func homeRow(left, right string, w int, selected, focused bool) string {
	r := ui.ListRow(ui.RowOpts{Left: left, Right: right, Width: w, Selected: selected && focused, Indent: 1})
	if selected && !focused && w > 0 {
		r = lipgloss.NewStyle().Foreground(ui.Muted).Render("▌") + ansi.TruncateLeft(r, 1, "")
	}
	return r
}

func dim(s string) string  { return lipgloss.NewStyle().Foreground(ui.Muted).Render(s) }
func text(s string) string { return lipgloss.NewStyle().Foreground(ui.Text).Render(s) }

// homeBox is one titled box of the launcher: its content lines, the line of
// the selected row inside them (-1 when none) and whether it holds the cursor.
type homeBox struct {
	title string
	lines []string
	sel   int
	focus bool
}

// stackBoxes draws boxes one above the other, width cells wide, and gives the
// line of the selected row. The box holding the cursor has the accent border
// when its column is focused.
func stackBoxes(boxes []homeBox, width int, focused bool) (lines []string, sel int) {
	for _, b := range boxes {
		if b.sel >= 0 {
			sel = len(lines) + 1 + b.sel
		}
		lines = append(lines, strings.Split(ui.Box(b.title, b.lines, width, focused && b.focus), "\n")...)
	}
	return lines, sel
}

// leftBoxes lists recent projects, sessions and the project block for w inner
// cells.
func (m HomeModel) leftBoxes(w int, focused bool) []homeBox {
	rows := m.leftRows()
	nProj := len(m.recentProjects)
	var boxes []homeBox

	proj := homeBox{title: "PROJECTS", sel: -1, focus: m.leftCur < nProj}
	if nProj == 0 {
		proj.lines = append(proj.lines, dim("  no recent projects"))
	}
	nameW := 8
	for _, r := range rows[:nProj] {
		nameW = max(nameW, ansi.StringWidth(r.label))
	}
	nameW = min(nameW, max((w-4)/2, 4))
	for i, r := range rows[:nProj] {
		name := ui.Truncate(r.label, nameW)
		name += strings.Repeat(" ", nameW-ansi.StringWidth(name))
		meta := ui.TruncateMiddle(tildePath(r.path), max(w-4-nameW, 0))
		if i == m.leftCur {
			proj.sel = len(proj.lines)
		}
		proj.lines = append(proj.lines, homeRow(name, meta, w, i == m.leftCur, focused))
	}
	boxes = append(boxes, proj)

	if !m.projectValid {
		return append(boxes, homeBox{title: "PROJECT", sel: -1, lines: []string{
			lipgloss.NewStyle().Foreground(ui.Warn).Render("  no project here"),
			dim("  press o to open one"),
		}})
	}

	sess := homeBox{title: "SESSIONS · " + m.cachedProject, sel: -1, focus: m.leftCur >= nProj}
	if len(m.sessions) == 0 {
		sess.lines = append(sess.lines, dim("  no saved sessions"))
	}
	now := time.Now()
	for j, r := range rows[nProj:] {
		i := nProj + j
		kind := sessionKind(r.info)
		glyph, _, col := ui.Status(kind)
		selected := i == m.leftCur
		g := glyph
		if !selected || !focused {
			g = lipgloss.NewStyle().Foreground(col).Render(glyph)
		}
		meta := sessionMeta(r.info, now)
		title := ui.Truncate(r.label, max(w-4-ansi.StringWidth(meta)-3, 4))
		if selected {
			sess.sel = len(sess.lines)
		}
		sess.lines = append(sess.lines, homeRow(g+" "+title, meta, w, selected, focused))
	}
	boxes = append(boxes, sess)

	info := homeBox{title: "PROJECT", sel: -1}
	for _, kv := range m.projectInfo() {
		v := ui.Truncate(kv[1], max(w-12, 1))
		info.lines = append(info.lines, "  "+dim(fmt.Sprintf("%-9s", kv[0]))+" "+text(v))
	}
	return append(boxes, info)
}

// projectInfo is the language, lead and autonomy of the project, as label and
// value pairs.
func (m HomeModel) projectInfo() [][2]string {
	lang := m.cachedLanguage
	switch {
	case lang == "":
		lang = "not detected"
	case m.cfg.Project.Language == "":
		lang += " (auto)"
	}
	lead := "not set"
	if pm := m.cfg.Agents["pm"]; pm.Runner != "" {
		lead = pm.Runner
		if mdl := runner.EffectiveModel(pm.Runner, pm.Model); mdl != "" {
			lead += " · " + mdl
		}
	}
	level := max(m.cfg.Workspace.Autonomy, 1)
	auto := fmt.Sprintf("%d %s", level, autonomyName(level))
	if b := m.cfg.Workspace.BudgetUSD; b > 0 {
		auto += fmt.Sprintf(" · budget $%.2f", b)
	} else {
		auto += " · no budget"
	}
	return [][2]string{{"language", lang}, {"lead", lead}, {"autonomy", auto}}
}

// rightBoxes lists the actions, one box per group, for w inner cells. The
// selected action shows what it does.
func (m HomeModel) rightBoxes(w int, focused bool) []homeBox {
	var boxes []homeBox
	for i, it := range m.items {
		if len(boxes) == 0 || it.group != m.items[i-1].group {
			boxes = append(boxes, homeBox{title: strings.ToUpper(it.group), sel: -1})
		}
		b := &boxes[len(boxes)-1]
		selected := i == m.cursor
		if selected {
			b.sel, b.focus = len(b.lines), true
		}
		b.lines = append(b.lines, homeRow(it.label, it.key, w, selected, focused))
		if selected {
			desc := it.desc
			if !m.projectValid && requiresProject(it.action) {
				desc = "open a project first"
			}
			b.lines = append(b.lines, "  "+dim(ui.Truncate(desc, max(w-3, 1))))
		}
	}
	return boxes
}

// The banner is the DECSR block letters in a sparse starfield (46 cells wide,
// the 24-cell logo in the middle). In the star rows "✦" is bright and "G" a
// warm star; the other glyphs are dim.
const (
	homeStarsTop    = "  ·       ✦        +      ⋆       G           "
	homeStarsBottom = "    ⋆       ·          ✦              G       "
	homeBannerWidth = 46
)

var (
	homeStarsLeft  = [3]string{"   ·       ", "  ⋆        ", "     ·     "}
	homeStarsRight = [3]string{"  ·        ", "    G      ", "   ✦       "}
)

func renderStars(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case ' ':
			sb.WriteRune(r)
		case '✦':
			sb.WriteString(lipgloss.NewStyle().Foreground(ui.Text).Render("✦"))
		case 'G':
			sb.WriteString(lipgloss.NewStyle().Foreground(ui.Warn).Render("✦"))
		default:
			sb.WriteString(lipgloss.NewStyle().Foreground(ui.Muted).Render(string(r)))
		}
	}
	return sb.String()
}

// banner is the logo with its tagline, centred in w cells, followed by an
// empty line. It is empty when w is too narrow.
func (m HomeModel) banner(w int) []string {
	if w < homeBannerWidth+2 {
		return nil
	}
	rowColors := make([]lipgloss.TerminalColor, 5)
	for i := range rowColors {
		rowColors[i] = ui.Accent
	}
	logo := renderBlockTextColored("DECSR", rowColors)
	lines := []string{renderStars(homeStarsTop)}
	for i, l := range logo {
		lines = append(lines, renderStars(homeStarsLeft[i])+l+renderStars(homeStarsRight[i]))
	}
	lines = append(lines, renderStars(homeStarsBottom))
	tag := "AI-powered multi-agent coding pipeline"
	lines = append(lines, strings.Repeat(" ", (homeBannerWidth-ansi.StringWidth(tag))/2)+dim(tag))
	for i, l := range lines {
		lines[i] = strings.Repeat(" ", (w-homeBannerWidth)/2) + l
	}
	return append(lines, "")
}

// renderFooter is the hint bar, or the status message when there is one.
func (m HomeModel) renderFooter() string {
	if m.status != "" {
		col := ui.OK
		if strings.HasPrefix(m.status, "!") {
			col = ui.Fail
		}
		return "  " + ui.Truncate(lipgloss.NewStyle().Foreground(col).Render(m.status), max(m.width-2, 1))
	}
	hints := []ui.Hint{{Key: "↑↓", Label: "move"}, {Key: "Tab", Label: "column"}, {Key: "Enter", Label: "open"},
		{Key: "w", Label: "workspace"}, {Key: "?", Label: "help"}, {Key: "q", Label: "quit"}}
	for _, it := range m.items {
		if it.action == homeActionResume || it.action == homeActionResumeProject {
			hints = append(hints, ui.Hint{Key: "r", Label: "resume"})
			break
		}
	}
	return ui.HintBar(hints, m.width)
}

// renderBase is the launcher without overlays: header, columns, hint bar.
func (m HomeModel) renderBase() string {
	w, h := max(m.width, 1), max(m.height, 4)
	left := "decsr · no project"
	if m.projectValid {
		left = "decsr · " + m.cachedProject
		if m.cachedBranch != "" {
			left += " · " + m.cachedBranch
		}
	}
	header := ui.Header(left, "v"+Version+" · ? help", w)

	bodyH := max(h-3, 1)
	cw := min(w, homeMaxWidth)
	margin := strings.Repeat(" ", (w-cw)/2)
	banner := m.banner(cw)
	var body []string
	var sel int
	if w >= homeWideMin {
		lw := (cw - 1) / 2
		rw := cw - 1 - lw
		l, ls := stackBoxes(m.leftBoxes(lw-4, m.col == homeColLeft), lw, m.col == homeColLeft)
		r, rs := stackBoxes(m.rightBoxes(rw-4, m.col == homeColRight), rw, m.col == homeColRight)
		sel = rs
		if m.col == homeColLeft {
			sel = ls
		}
		body = sideBySide(l, r, lw, 1)
	} else {
		r, rs := stackBoxes(m.rightBoxes(cw-4, m.col == homeColRight), cw, m.col == homeColRight)
		l, ls := stackBoxes(m.leftBoxes(cw-4, m.col == homeColLeft), cw, m.col == homeColLeft)
		body = append(append([]string(nil), r...), l...)
		sel = rs
		if m.col == homeColLeft {
			sel = len(r) + ls
		}
		if len(body) > bodyH {
			// Both columns do not fit: show the focused one.
			body, sel = r, rs
			if m.col == homeColLeft {
				body, sel = l, ls
			}
		}
	}
	if len(banner)+len(body) <= bodyH {
		body = append(banner, body...)
		sel += len(banner)
	}
	if top := (bodyH - len(body)) / 2; top > 0 {
		body = append(make([]string, top), body...)
		sel += top
	}
	if margin != "" {
		for i, l := range body {
			if l != "" {
				body[i] = margin + l
			}
		}
	}

	for i, l := range body {
		body[i] = ansi.Truncate(l, w, "")
	}
	start := 0
	if sel >= bodyH {
		start = sel - bodyH + 2
	}
	start = max(min(start, len(body)-bodyH), 0)
	body = body[start:min(start+bodyH, len(body))]
	for len(body) < bodyH {
		body = append(body, "")
	}
	out := append([]string{header, ""}, body...)
	return strings.Join(append(out, m.renderFooter()), "\n")
}

// View renders the launcher, the help overlay, a confirmation or the connect
// overlay.
func (m HomeModel) View() string {
	if m.showHelp {
		return renderHelp(m.width, m.height, "Keyboard — home", homeHelp())
	}
	base := m.renderBase()
	w, h := max(m.width, 1), max(m.height, 4)
	switch {
	case m.conn != nil:
		return ui.PlaceOverlay(base, m.conn.box(w), w, h)
	case m.confirmQuit:
		box := ui.Overlay("Quit decsr?", nil, []ui.Hint{{Key: "y / Enter", Label: "quit"}, {Key: "any other key", Label: "cancel"}}, min(60, w))
		return ui.PlaceOverlay(base, box, w, h)
	case m.confirmAction != nil:
		title, detail := "Reset artifacts?", "Reset artifacts? This removes generated plans and reports (keeps code, config, and project memory)."
		if *m.confirmAction == homeActionNewTask {
			title, detail = "Start a new task?", "Start a new task? This removes the current plan and reports (code, config and memory are kept)."
		}
		bw := min(60, w)
		box := ui.Overlay(title, wrapText(detail, max(bw-4, 1)), []ui.Hint{{Key: "y / Enter", Label: "confirm"}, {Key: "any other key", Label: "cancel"}}, bw)
		return ui.PlaceOverlay(base, box, w, h)
	}
	return base
}
