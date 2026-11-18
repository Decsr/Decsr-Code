package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	palEmptyPerGroup = 5 // rows per group while the query is empty
	palMaxPerGroup   = 6 // rows per group while searching
	palBoxW          = 72
)

// fuzzyScore matches query against text as a case-insensitive subsequence. It
// returns false when the letters do not appear in order. A higher score is a
// better match: a contiguous match beats a scattered one, a prefix or a word
// start beats a match in the middle, and an earlier match beats a later one.
func fuzzyScore(query, text string) (int, bool) {
	q, t := []rune(strings.ToLower(query)), []rune(strings.ToLower(text))
	q = []rune(strings.Join(strings.Fields(string(q)), ""))
	if len(q) == 0 {
		return 0, true
	}
	score, ti, run, prev := 0, 0, 0, -2
	for _, qc := range q {
		found := false
		for ; ti < len(t); ti++ {
			if t[ti] != qc {
				continue
			}
			found = true
			if ti == prev+1 {
				run++
				score += 10 + 2*run
			} else {
				run = 0
				score += 1
			}
			if ti == 0 {
				score += 15
			} else if !unicode.IsLetter(t[ti-1]) && !unicode.IsDigit(t[ti-1]) {
				score += 8
			}
			prev = ti
			ti++
			break
		}
		if !found {
			return 0, false
		}
	}
	if i := strings.Index(string(t), string(q)); i >= 0 { // the whole query in one piece
		score += 40
		if i == 0 {
			score += 30
		}
	}
	return score*10 - len(t), true
}

// palKind is the group of a palette row.
type palKind int

const (
	palInbox palKind = iota
	palCommand
	palNode
	palSession
	palProject
)

var palTitles = [...]string{"Needs you", "Commands", "Nodes", "Sessions", "Projects"}

// palRow is one selectable row of the palette.
type palRow struct {
	kind        palKind
	left, right string
	score       int
	inbox       inboxItem
	cmd         wsCommand
	ref         string // node ID, session ID or project path
}

// palSources are the things the palette searches besides the inbox.
type palSources struct {
	commands []wsCommand
	nodes    []*workspace.Node
	g        *workspace.Graph
	sessions []workspace.SessionInfo
	projects []wsProject
	inbox    []inboxItem
}

// buildPalette returns the rows of each group, in the fixed group order, for a
// query. An empty query lists the items that need the user and the first rows
// of the other groups; a query keeps the matches, best first.
func buildPalette(q string, s palSources, nowAge func(workspace.SessionInfo) string) [][]palRow {
	searching := strings.TrimSpace(q) != ""
	groups := make([][]palRow, len(palTitles))
	// add scores a row: the first text is matched fuzzily, the others (a
	// description, a path) only when they contain the query, so a long text does
	// not match by accident.
	add := func(k palKind, r palRow, texts ...string) {
		best, ok := 0, false
		for i, t := range texts {
			sc, hit := 0, false
			if i == 0 || strings.Contains(strings.ToLower(t), strings.ToLower(strings.TrimSpace(q))) {
				sc, hit = fuzzyScore(q, t)
			}
			if !hit {
				continue
			}
			if i > 0 {
				sc /= 2 // a hit besides the name counts less
			}
			if !ok || sc > best {
				best, ok = sc, true
			}
		}
		if !ok {
			return
		}
		r.kind, r.score = k, best
		groups[k] = append(groups[k], r)
	}
	for _, it := range s.inbox {
		if !searching && it.info {
			continue
		}
		add(palInbox, palRow{inbox: it, left: it.glyph + " " + it.label + " · " + it.text, right: it.right}, it.label+" "+it.text, it.text)
	}
	for _, c := range s.commands {
		left := padRight(c.name, 13) + " " + c.desc
		if c.tag != "" {
			left += " " + c.tag
		}
		add(palCommand, palRow{cmd: c, left: left}, c.name, c.desc)
	}
	for _, n := range s.nodes {
		glyph, word := nodeStatusGlyph(s.g, n)
		add(palNode, palRow{ref: n.ID, left: padRight(n.ID, 5) + " " + workspace.Sanitize(n.Title), right: glyph + " " + word}, n.ID+" "+n.Title)
	}
	for _, si := range s.sessions {
		title := workspace.Sanitize(si.Title)
		if title == "" {
			title = "untitled"
		}
		total := si.Todo + si.Running + si.Done + si.Failed + si.Waiting
		add(palSession, palRow{ref: si.ID, left: title, right: fmt.Sprintf("%d/%d · %s", si.Done, total, nowAge(si))}, title)
	}
	for _, p := range s.projects {
		add(palProject, palRow{ref: p.path, left: p.name, right: ui.TruncateMiddle(p.path, 28)}, p.name, p.path)
	}
	for k := range groups {
		if searching {
			sort.SliceStable(groups[k], func(i, j int) bool { return groups[k][i].score > groups[k][j].score })
		}
		limit := palMaxPerGroup
		if !searching {
			limit = palEmptyPerGroup
		}
		if palKind(k) == palInbox && !searching {
			limit = len(groups[k])
		}
		if len(groups[k]) > limit {
			groups[k] = groups[k][:limit]
		}
	}
	return groups
}

// palRows is the palette of the model for its current query, flattened.
func (m workspaceModel) palRows() [][]palRow {
	cur := m.sess.snapshot().ID
	var sessions []workspace.SessionInfo
	for _, s := range m.sbSessions {
		if s.ID != cur {
			sessions = append(sessions, s)
		}
	}
	src := palSources{inbox: m.inboxAll(), commands: m.commandList(), g: m.g, sessions: sessions, projects: m.palProjects}
	if m.g != nil {
		src.nodes = m.g.Nodes
	}
	return buildPalette(m.palIn.Value(), src, func(s workspace.SessionInfo) string { return sbAge(m.now(), s.UpdatedAt) })
}

func flattenPalette(groups [][]palRow) []palRow {
	var out []palRow
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// openPalette opens Ctrl+K with an empty query. The inbox logs, saved
// sessions and recent projects are read now, not on every key.
func (m workspaceModel) openPalette() workspaceModel {
	m.inboxAutos = readAutoRuns(m.root)
	m.refreshSaved()
	m.palProjects = nil
	cur := filepath.Clean(m.root)
	for _, p := range m.deps.recent() {
		path := filepath.Clean(p.Path)
		if path == cur {
			continue
		}
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			continue
		}
		name := p.Name
		if name == "" {
			name = filepath.Base(path)
		}
		m.palProjects = append(m.palProjects, wsProject{path: path, name: name})
	}
	m.palIn.SetValue("")
	m.palIn.Focus()
	m.palIdx = 0
	m.overlay = wsOverlayPalette
	return m
}

func (m workspaceModel) paletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := flattenPalette(m.palRows())
	m.palIdx = min(m.palIdx, max(len(rows)-1, 0))
	switch msg.String() {
	case "esc", "ctrl+k":
		m.overlay = wsOverlayNone
		m.palIn.Blur()
		return m, nil
	case "up", "ctrl+p":
		m.palIdx = max(m.palIdx-1, 0)
		return m, nil
	case "down", "ctrl+n":
		m.palIdx = min(m.palIdx+1, max(len(rows)-1, 0))
		return m, nil
	case "enter":
		if len(rows) == 0 {
			return m, nil
		}
		m.overlay = wsOverlayNone
		m.palIn.Blur()
		return m.paletteChoose(rows[m.palIdx])
	}
	var cmd tea.Cmd
	before := m.palIn.Value()
	m.palIn, cmd = m.palIn.Update(msg)
	if m.palIn.Value() != before {
		m.palIdx = 0
	}
	return m, cmd
}

// palRunNow lists the commands the palette runs at once: they only show
// information or open a list. Every other command, including all that push,
// merge, install, delete, clear, write or call a model, is put in the prompt.
var palRunNow = map[string]bool{
	"/help": true, "/status": true, "/usage": true,
	"/roles": true, "/resume": true, "/project": true,
}

// runsNow reports whether the palette may run c without the user pressing
// Enter in the prompt. Custom and plugin commands never do.
func runsNow(c wsCommand) bool { return c.tag == "" && palRunNow[c.name] }

// paletteChoose does what Enter on a row does. The overlay is already closed.
func (m workspaceModel) paletteChoose(r palRow) (tea.Model, tea.Cmd) {
	switch r.kind {
	case palInbox:
		return m.inboxJump(r.inbox)
	case palCommand:
		if runsNow(r.cmd) {
			return r.cmd.run(m, "")
		}
		m.setFocus(wsFocusPrompt)
		m.input.SetValue(r.cmd.name + " ")
		m.input.CursorEnd()
		m.cmdIdx, m.cmdPicked = 0, false
		return m, nil
	case palNode:
		return m.selectNode(r.ref), nil
	case palSession:
		if m.busy() {
			return m.refuseBusy()
		}
		return m.loadSession(r.ref)
	case palProject:
		if m.busy() {
			return m.refuseBusy()
		}
		return m.switchProject(r.ref)
	}
	return m, nil
}

// paletteBox draws the palette: the query, the groups that have rows and a
// hint line; "No matches" when every group is empty.
func (m workspaceModel) paletteBox() string {
	boxW := min(palBoxW, m.width-2)
	inner := boxW - 4
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	groups := m.palRows()
	idx := min(m.palIdx, max(len(flattenPalette(groups))-1, 0))
	prompt := lipgloss.NewStyle().Foreground(ui.Accent).Render("›") + " " + m.palIn.View()
	body := []string{prompt}
	var lines []string
	sel, n := 0, 0
	for k, g := range groups {
		if len(g) == 0 {
			continue
		}
		lines = append(lines, dim.Render(palTitles[k]))
		for _, r := range g {
			if n == idx {
				sel = len(lines)
			}
			lines = append(lines, ui.ListRow(ui.RowOpts{Left: r.left, Right: r.right, Width: inner, Selected: n == idx, Indent: 1}))
			n++
		}
	}
	if n == 0 {
		lines = []string{dim.Render("No matches")}
	}
	body = append(body, listWindow(lines, sel, max(m.height-10, 3))...)
	hints := []ui.Hint{{Key: "↑↓", Label: "move"}, {Key: "Enter", Label: "go"}, {Key: "Esc", Label: "close"}}
	return ui.Overlay("Go to", body, hints, boxW)
}
