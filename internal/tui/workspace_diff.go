package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	wsDiffListMin  = 100 // the file list column is shown from this width
	wsDiffListMax  = 34
	wsDiffHScroll  = 8
	wsDiffNoteMax  = 3 // wrapped lines of one note box
	wsDiffCtxWidth = 200
)

// diffRow is one row of the diff of a file: a hunk header (line -1) or a line.
type diffRow struct{ hunk, line int }

// wsDiffView is the state of the full-screen diff. It is shared by the copies
// of the model.
type wsDiffView struct {
	nodeID  string // "" for the session diff
	title   string
	diff    workspace.Diff
	err     string
	rows    [][]diffRow // per file
	file    int
	cur     int // cursor row in the file
	top     int // first visible row
	hx      int // horizontal scroll
	msg     string
	editing bool
	input   textinput.Model
	body    int // body height of the last render, for paging
}

func (v *wsDiffView) rowsOf() []diffRow {
	if v.file < 0 || v.file >= len(v.rows) {
		return nil
	}
	return v.rows[v.file]
}

func (v *wsDiffView) curFile() *workspace.DiffFile {
	if v.file < 0 || v.file >= len(v.diff.Files) {
		return nil
	}
	return &v.diff.Files[v.file]
}

// curLine is the line under the cursor, nil on a hunk header.
func (v *wsDiffView) curLine() *workspace.DiffLine {
	rows, f := v.rowsOf(), v.curFile()
	if f == nil || v.cur < 0 || v.cur >= len(rows) {
		return nil
	}
	return rowLine(f, rows[v.cur])
}

func rowLine(f *workspace.DiffFile, r diffRow) *workspace.DiffLine {
	if r.line < 0 {
		return nil
	}
	return &f.Hunks[r.hunk].Lines[r.line]
}

func diffRows(d workspace.Diff) [][]diffRow {
	out := make([][]diffRow, len(d.Files))
	for i, f := range d.Files {
		var rows []diffRow
		for h, hk := range f.Hunks {
			rows = append(rows, diffRow{hunk: h, line: -1})
			for l := range hk.Lines {
				rows = append(rows, diffRow{hunk: h, line: l})
			}
		}
		out[i] = rows
	}
	return out
}

// noteSide is the side and number a note on l is filed under.
func noteSide(l workspace.DiffLine) (string, int) {
	if l.Kind == '-' {
		return "old", l.OldNo
	}
	return "new", l.NewNo
}

// noteable reports whether a note may be put on l.
func noteable(l *workspace.DiffLine) bool { return l != nil && l.Kind != '\\' }

// findNote returns the note of the node on the given line of a file.
func findNote(notes []workspace.DiffNote, node, path, side string, line int) (int, bool) {
	for i, n := range notes {
		if n.NodeID == node && n.Path == path && n.Side == side && n.Line == line {
			return i, true
		}
	}
	return -1, false
}

// ── Opening ──────────────────────────────────────────────────────────────────

// openDiff loads the diff of node n (or of the session when n is nil) and
// shows it.
func (m workspaceModel) openDiff(n *workspace.Node) workspaceModel {
	v := &wsDiffView{}
	var d workspace.Diff
	var err error
	if n == nil {
		v.title = "Session diff"
		d, err = workspace.SessionDiff(m.root, m.sess.snapshot().ID)
	} else {
		v.nodeID = n.ID
		v.title = n.ID + " · " + oneLine(workspace.Sanitize(n.Title))
		d, err = workspace.NodeDiff(m.root, n, m.iso)
	}
	if err != nil {
		v.err = oneLine(err.Error())
	}
	v.diff = d
	v.rows = diffRows(d)
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 500
	v.input = ti
	m.diff = v
	m.screen = wsScreenDiff
	if m.sidebarOverlay() {
		m.sbFlip = false
	}
	return m
}

// hasDiff reports whether the card of n can offer a diff.
func hasDiff(n *workspace.Node) bool {
	return n != nil && n.Kind == workspace.KindWork && (n.Commit != "" || len(n.FileStats) > 0)
}

// ── Keys ─────────────────────────────────────────────────────────────────────

func (m workspaceModel) diffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	v := m.diff
	if v == nil {
		m.screen = wsScreenMain
		return m, nil
	}
	k := msg.String()
	if v.editing {
		return m.diffEditKey(msg)
	}
	v.msg = ""
	rows := v.rowsOf()
	page := max(v.body-1, 1)
	switch k {
	case "esc", "q":
		m.screen = wsScreenMain
		m.diff = nil
	case "?":
		m.showHelp = true
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
		v.cur = max(len(rows)-1, 0)
	case "n":
		v.jumpHunk(1)
	case "p":
		v.jumpHunk(-1)
	case "]", "tab":
		v.setFile(v.file + 1)
	case "[", "shift+tab":
		v.setFile(v.file - 1)
	case "right", "l":
		v.hx += wsDiffHScroll
	case "left", "h":
		v.hx = max(v.hx-wsDiffHScroll, 0)
	case "0":
		v.hx = 0
	case "c":
		return m.diffStartNote(), nil
	case "x":
		return m.diffDeleteNote(), nil
	case "S":
		return m.diffSendNotes()
	}
	v.clamp()
	return m, nil
}

func (v *wsDiffView) move(d int) { v.cur += d }

func (v *wsDiffView) clamp() {
	rows := v.rowsOf()
	v.cur = max(min(v.cur, len(rows)-1), 0)
}

func (v *wsDiffView) setFile(i int) {
	if i < 0 || i >= len(v.diff.Files) {
		return
	}
	v.file, v.cur, v.top, v.hx = i, 0, 0, 0
}

// jumpHunk moves the cursor to the header of the next or previous hunk, going
// on into the next or previous file at the ends.
func (v *wsDiffView) jumpHunk(dir int) {
	rows := v.rowsOf()
	if dir > 0 {
		for i := v.cur + 1; i < len(rows); i++ {
			if rows[i].line < 0 {
				v.cur = i
				return
			}
		}
		for f := v.file + 1; f < len(v.rows); f++ {
			if len(v.rows[f]) > 0 {
				v.setFile(f)
				return
			}
		}
		return
	}
	for i := v.cur - 1; i >= 0; i-- {
		if rows[i].line < 0 {
			v.cur = i
			return
		}
	}
	for f := v.file - 1; f >= 0; f-- {
		if n := len(v.rows[f]); n > 0 {
			v.setFile(f)
			for i := n - 1; i >= 0; i-- {
				if v.rows[f][i].line < 0 {
					v.cur = i
					break
				}
			}
			return
		}
	}
}

func (m workspaceModel) diffStartNote() workspaceModel {
	v := m.diff
	if v.nodeID == "" {
		v.msg = "Notes go on a node's diff: open it with d on the node."
		return m
	}
	l, f := v.curLine(), v.curFile()
	if f == nil || !noteable(l) {
		v.msg = "Put the cursor on a code line to add a note."
		return m
	}
	side, no := noteSide(*l)
	text := ""
	if i, ok := findNote(m.sess.snapshot().Notes, v.nodeID, f.Path, side, no); ok {
		text = m.sess.snapshot().Notes[i].Text
	}
	v.editing = true
	v.input.SetValue(text)
	v.input.CursorEnd()
	v.input.Focus()
	return m
}

func (m workspaceModel) diffEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	v := m.diff
	switch msg.String() {
	case "esc":
		v.editing = false
		v.input.Blur()
		return m, nil
	case "enter":
		v.editing = false
		v.input.Blur()
		text := strings.TrimSpace(v.input.Value())
		l, f := v.curLine(), v.curFile()
		if l == nil || f == nil {
			return m, nil
		}
		side, no := noteSide(*l)
		node, path := v.nodeID, f.Path
		m.sess.update(func(s *workspace.Session) {
			i, ok := findNote(s.Notes, node, path, side, no)
			switch {
			case text == "" && ok:
				s.Notes = append(s.Notes[:i:i], s.Notes[i+1:]...)
			case text == "":
			case ok:
				s.Notes[i].Text = text
			default:
				s.Notes = append(s.Notes, workspace.DiffNote{NodeID: node, Path: path, Line: no, Side: side, Text: text})
			}
		})
		m.saveSession()
		return m, nil
	}
	var cmd tea.Cmd
	v.input, cmd = v.input.Update(msg)
	return m, cmd
}

func (m workspaceModel) diffDeleteNote() workspaceModel {
	v := m.diff
	l, f := v.curLine(), v.curFile()
	if !noteable(l) || f == nil {
		return m
	}
	side, no := noteSide(*l)
	node, path := v.nodeID, f.Path
	removed := false
	m.sess.update(func(s *workspace.Session) {
		if i, ok := findNote(s.Notes, node, path, side, no); ok {
			s.Notes = append(s.Notes[:i:i], s.Notes[i+1:]...)
			removed = true
		}
	})
	if removed {
		m.saveSession()
		v.msg = "Note deleted."
	}
	return m
}

// ── Sending the notes ────────────────────────────────────────────────────────

// diffSendNotes turns the notes of the open node into one follow-up node.
func (m workspaceModel) diffSendNotes() (tea.Model, tea.Cmd) {
	v := m.diff
	if v.nodeID == "" {
		v.msg = "Open a node's diff (d on the node) to send its notes."
		return m, nil
	}
	notes := m.nodeNotesFor(v.nodeID)
	if len(notes) == 0 {
		v.msg = "No notes to send: press c on a line to add one."
		return m, nil
	}
	src := m.g.Node(v.nodeID)
	if src == nil || m.eng == nil {
		v.msg = "The node is gone."
		return m, nil
	}
	role := ""
	if m.defined(src.Role) {
		role = src.Role
	}
	title := "Address review notes on " + src.ID
	desc := reviewDescription(src, notes, v.diff)
	next := m.sess.snapshot().NextID
	id, next := m.eng.AddWork(title, desc, role, next)
	m.sess.update(func(s *workspace.Session) {
		s.NextID = next
		kept := s.Notes[:0:0]
		for _, n := range s.Notes {
			if n.NodeID != src.ID {
				kept = append(kept, n)
			}
		}
		s.Notes = kept
	})
	m.g = m.eng.Snapshot()
	m.sel = id
	m.screen = wsScreenMain
	m.diff = nil
	m.setFocus(wsFocusGraph)
	count := fmt.Sprintf("%d review %s", len(notes), pluralize(len(notes), "note", "notes"))
	if role == "" {
		m.say("system", fmt.Sprintf("Added %s from %s — assign a role (a), then press r to run", id, count))
	} else {
		m.say("system", fmt.Sprintf("Added %s from %s — press r to run", id, count))
	}
	m.saveSession()
	return m, nil
}

// reviewDescription lists each note as "path:line — text" with up to three
// lines of the diff around it.
func reviewDescription(src *workspace.Node, notes []workspace.DiffNote, d workspace.Diff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review notes on %s (%s). Address each note below and change nothing else.\n", src.ID, oneLine(workspace.Sanitize(src.Title)))
	for _, n := range notes {
		where := fmt.Sprintf("%s:%d", n.Path, n.Line)
		if n.Side == "old" {
			where += " (removed line)"
		}
		fmt.Fprintf(&b, "\n%s — %s\n", where, oneLine(n.Text))
		for _, l := range noteContext(d, n) {
			b.WriteString(l + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// noteContext is the noted line with the line before and after it (inside the
// same hunk), as "    26   code" rows; the noted line is marked with ">".
func noteContext(d workspace.Diff, n workspace.DiffNote) []string {
	for _, f := range d.Files {
		if f.Path != n.Path {
			continue
		}
		for _, h := range f.Hunks {
			for i, l := range h.Lines {
				if l.Kind == '\\' {
					continue
				}
				if side, no := noteSide(l); side != n.Side || no != n.Line {
					continue
				}
				var out []string
				for j := max(i-1, 0); j <= min(i+1, len(h.Lines)-1); j++ {
					c := h.Lines[j]
					if c.Kind == '\\' {
						continue
					}
					mark := " "
					if j == i {
						mark = ">"
					}
					no := c.NewNo
					if c.Kind == '-' {
						no = c.OldNo
					}
					out = append(out, fmt.Sprintf("  %s %4d %c %s", mark, no, c.Kind, truncPlain(sanitizeCode(c.Text), wsDiffCtxWidth)))
				}
				return out
			}
		}
	}
	return nil
}

func truncPlain(s string, w int) string { return ansi.Truncate(s, w, "…") }

// sanitizeCode makes one line of code safe to print: tabs become four spaces
// and control characters are dropped.
func sanitizeCode(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ── View ─────────────────────────────────────────────────────────────────────

func diffCounts(added, removed int) string {
	ok := lipgloss.NewStyle().Foreground(colOK)
	bad := lipgloss.NewStyle().Foreground(colFail)
	return ok.Render(fmt.Sprintf("+%d", added)) + " " + bad.Render(fmt.Sprintf("−%d", removed))
}

func diffCountsPlain(added, removed int) string { return fmt.Sprintf("+%d −%d", added, removed) }

func (m workspaceModel) diffTitleLine(w int) string {
	v := m.diff
	dim := lipgloss.NewStyle().Foreground(colMuted)
	a, r := v.diff.Totals()
	nf := len(v.diff.Files)
	meta := fmt.Sprintf(" · %d %s ", nf, pluralize(nf, "file", "files"))
	notes := len(m.nodeNotesFor(v.nodeID))
	right := diffCounts(a, r)
	rightW := ansi.StringWidth(diffCountsPlain(a, r))
	noteTxt := ""
	if notes > 0 {
		noteTxt = fmt.Sprintf(" · %d %s", notes, pluralize(notes, "note", "notes"))
	}
	room := max(w-4-ansi.StringWidth(meta)-rightW-ansi.StringWidth(noteTxt), 6)
	title := lipgloss.NewStyle().Foreground(colText).Bold(true).Render(truncateOneLine(v.title, room))
	return "  " + title + dim.Render(meta) + right + dim.Render(noteTxt)
}

func (m workspaceModel) nodeNotesFor(node string) []workspace.DiffNote {
	if node == "" {
		return nil
	}
	var out []workspace.DiffNote
	for _, n := range m.sess.snapshot().Notes {
		if n.NodeID == node {
			out = append(out, n)
		}
	}
	return out
}

func (m workspaceModel) diffView() string {
	w, h := m.width, m.height
	v := m.diff
	if v == nil {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(colMuted)
	var head []string
	head = append(head, m.diffTitleLine(w))
	hints := []keyHint{{"Esc", "back"}, {"j/k", "scroll"}, {"n/p", "hunk"}, {"[ ]", "file"}, {"c", "note"}, {"S", "send notes"}}
	if v.nodeID == "" {
		hints = []keyHint{{"Esc", "back"}, {"j/k", "scroll"}, {"n/p", "hunk"}, {"[ ]", "file"}, {"←/→", "pan"}}
	}
	head = append(head, wsHintBar(w, fitHints(w, hints)))
	if v.diff.Note != "" {
		head = append(head, "  "+dim.Render(truncateOneLine(v.diff.Note, w-4)))
	}
	wide := w >= wsDiffListMin
	if !wide && len(v.diff.Files) > 0 {
		f := v.diff.Files[v.file]
		sel := fmt.Sprintf("[ file %d/%d: %s ]", v.file+1, len(v.diff.Files), f.Path)
		head = append(head, "  "+lipgloss.NewStyle().Foreground(colAccent).Render(truncateOneLine(sel, w-4)))
	}
	bodyH := max(h-len(head)-1, 1)
	v.body = bodyH
	var body []string
	switch {
	case v.err != "":
		body = []string{"  " + lipgloss.NewStyle().Foreground(colFail).Render(truncateOneLine("! "+v.err, w-4))}
	case len(v.diff.Files) == 0:
		body = []string{"  " + dim.Render("No changes in this node.")}
		if v.nodeID == "" {
			body = []string{"  " + dim.Render("No changes in the session branch.")}
		}
	case wide:
		lw := min(wsDiffListMax, w/4)
		list := m.diffFileList(lw, bodyH)
		pane := m.diffPane(w-lw-1, bodyH)
		sep := lipgloss.NewStyle().Foreground(colBorder).Render("│")
		for i := 0; i < bodyH; i++ {
			l, p := "", ""
			if i < len(list) {
				l = list[i]
			}
			if i < len(pane) {
				p = pane[i]
			}
			body = append(body, padRight(l, lw)+sep+p)
		}
	default:
		body = m.diffPane(w, bodyH)
	}
	for len(body) < bodyH {
		body = append(body, "")
	}
	out := append(head, body[:bodyH]...)
	out = append(out, m.diffStatusLine(w))
	return strings.Join(out, "\n")
}

func (m workspaceModel) diffStatusLine(w int) string {
	v := m.diff
	dim := lipgloss.NewStyle().Foreground(colMuted)
	if v.editing {
		l := v.curLine()
		label := "Note"
		if l != nil {
			_, no := noteSide(*l)
			label = fmt.Sprintf("Note · line %d", no)
		}
		v.input.Width = max(w-ansi.StringWidth(label)-8, 8)
		return "  " + lipgloss.NewStyle().Foreground(colAccent).Render(label+" › ") + v.input.View() + dim.Render("  Enter save · Esc cancel")
	}
	if v.msg != "" {
		return "  " + lipgloss.NewStyle().Foreground(colWarn).Render(truncateOneLine(v.msg, w-4))
	}
	if f := v.curFile(); f != nil && len(v.rowsOf()) > 0 {
		pos := fmt.Sprintf("%s · row %d/%d", f.Path, v.cur+1, len(v.rowsOf()))
		if v.hx > 0 {
			pos += fmt.Sprintf(" · col +%d", v.hx)
		}
		return "  " + dim.Render(truncateOneLine(pos, w-4))
	}
	return ""
}

// diffFileList is the left column: one row per file, the selected one on the
// raised background.
func (m workspaceModel) diffFileList(w, h int) []string {
	v := m.diff
	n := len(v.diff.Files)
	start := 0
	if n > h {
		start = max(min(v.file-h/2, n-h), 0)
	}
	var out []string
	for i := start; i < n && len(out) < h; i++ {
		f := v.diff.Files[i]
		counts := diffCountsPlain(f.Added, f.Removed)
		if f.Binary {
			counts = "bin"
		}
		room := max(w-ansi.StringWidth(counts)-3, 4)
		name := tailPath(f.Path, room)
		gap := max(w-2-ansi.StringWidth(name)-ansi.StringWidth(counts)-1, 1)
		sel := i == v.file
		bg := lipgloss.NewStyle()
		if sel {
			bg = bg.Background(colSel)
		}
		fg := colText
		if !sel {
			fg = colMuted
		}
		lead := "  "
		if sel {
			lead = "▌ "
		}
		row := bg.Foreground(colAccent).Render(lead) + bg.Foreground(fg).Render(name+strings.Repeat(" ", gap))
		if f.Binary {
			row += bg.Foreground(colMuted).Render(counts + " ")
		} else {
			row += bg.Foreground(colOK).Render(fmt.Sprintf("+%d", f.Added)) + bg.Render(" ") + bg.Foreground(colFail).Render(fmt.Sprintf("−%d", f.Removed)) + bg.Render(" ")
		}
		out = append(out, row)
	}
	return out
}

func digits(n int) int { return len(fmt.Sprint(n)) }

// diffPane renders the visible rows of the current file at w cells and h rows.
func (m workspaceModel) diffPane(w, h int) []string {
	v := m.diff
	f := v.curFile()
	rows := v.rowsOf()
	if f == nil {
		return nil
	}
	if len(rows) == 0 {
		msg := "No textual changes."
		switch {
		case f.Binary:
			msg = "Binary file."
		case f.OldPath != "":
			msg = "Renamed from " + f.OldPath + "."
		}
		return []string{"  " + lipgloss.NewStyle().Foreground(colMuted).Render(truncateOneLine(msg, w-2))}
	}
	maxNo := 0
	for _, hk := range f.Hunks {
		for _, l := range hk.Lines {
			maxNo = max(maxNo, max(l.OldNo, l.NewNo))
		}
	}
	nw := max(digits(maxNo), 3)
	gutter := 2*nw + 3
	notes := m.nodeNotesFor(v.nodeID)
	noteFor := func(i int) (workspace.DiffNote, bool) {
		l := rowLine(f, rows[i])
		if l == nil || len(notes) == 0 {
			return workspace.DiffNote{}, false
		}
		side, no := noteSide(*l)
		if j, ok := findNote(notes, v.nodeID, f.Path, side, no); ok {
			return notes[j], true
		}
		return workspace.DiffNote{}, false
	}
	boxW := max(w-gutter-1, 12)
	noteBox := func(n workspace.DiffNote) []string { return renderNoteBox(n, boxW, strings.Repeat(" ", gutter)) }
	heightOf := func(i int) int {
		if n, ok := noteFor(i); ok {
			return 1 + len(noteBox(n))
		}
		return 1
	}
	// Keep the cursor row (and its note) inside the window.
	v.top = max(min(v.top, v.cur), 0)
	used := 0
	for i := v.top; i <= v.cur; i++ {
		used += heightOf(i)
	}
	for used > h && v.top < v.cur {
		used -= heightOf(v.top)
		v.top++
	}
	var out []string
	for i := v.top; i < len(rows) && len(out) < h; i++ {
		out = append(out, v.renderRow(f, rows[i], i == v.cur, w, nw))
		if n, ok := noteFor(i); ok {
			out = append(out, noteBox(n)...)
		}
	}
	return out[:min(len(out), h)]
}

var (
	diffTintAdd = lerpColor(crt.bg, colOK, 0.16)
	diffTintDel = lerpColor(crt.bg, colFail, 0.18)
)

// renderRow renders one row to exactly w cells. Added and removed rows are
// tinted over the whole width in one background so no stripes show.
func (v *wsDiffView) renderRow(f *workspace.DiffFile, r diffRow, cursor bool, w, nw int) string {
	gutter := 2*nw + 3
	mark := " "
	if cursor {
		mark = lipgloss.NewStyle().Foreground(colAccent).Render("▌")
	}
	avail := max(w-gutter, 1)
	if r.line < 0 {
		hdr := f.Hunks[r.hunk].Header
		st := lipgloss.NewStyle().Foreground(colAccent).Faint(true)
		if cursor {
			st = st.Background(colSel)
		}
		return mark + st.Render(padRight(truncPlain(" "+strings.Repeat(" ", gutter-1)+sanitizeCode(hdr), w-1), w-1))
	}
	l := f.Hunks[r.hunk].Lines[r.line]
	bg := lipgloss.NewStyle()
	fg := colText
	sign := string(l.Kind)
	switch l.Kind {
	case '+':
		bg, fg = bg.Background(diffTintAdd), colOK
	case '-':
		bg, fg = bg.Background(diffTintDel), colFail
	case '\\':
		fg, sign = colMuted, " "
	}
	if cursor {
		bg = lipgloss.NewStyle().Background(colSel)
	}
	num := func(n int) string {
		if n <= 0 {
			return strings.Repeat(" ", nw)
		}
		return fmt.Sprintf("%*d", nw, n)
	}
	nums := bg.Foreground(colMuted).Render(num(l.OldNo) + " " + num(l.NewNo) + " ")
	text := ansi.TruncateLeft(sanitizeCode(l.Text), v.hx, "")
	body := sign + " " + text
	body = padRight(ansi.Truncate(body, avail, "…"), avail)
	return mark + nums + bg.Foreground(fg).Render(body)
}

// renderNoteBox is the rounded box of a note under its line, indented by pad.
func renderNoteBox(n workspace.DiffNote, w int, pad string) []string {
	border := lipgloss.NewStyle().Foreground(colAccent)
	dim := lipgloss.NewStyle().Foreground(colMuted)
	text := lipgloss.NewStyle().Foreground(colText)
	inner := max(w-4, 4)
	title := fmt.Sprintf("Note · line %d", n.Line)
	lines := hardWrap(oneLine(workspace.SanitizeText(n.Text)), inner)
	if len(lines) > wsDiffNoteMax {
		lines = lines[:wsDiffNoteMax]
		lines[wsDiffNoteMax-1] = truncPlain(lines[wsDiffNoteMax-1]+" …", inner)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	topFill := max(w-4-ansi.StringWidth(title), 0)
	out := []string{pad + border.Render("╭ ") + dim.Render(title) + border.Render(" "+strings.Repeat("─", topFill)+"╮")}
	for _, l := range lines {
		out = append(out, pad+border.Render("│ ")+text.Render(padRight(l, inner))+border.Render(" │"))
	}
	out = append(out, pad+border.Render("╰"+strings.Repeat("─", max(w-2, 0))+"╯"))
	return out
}
