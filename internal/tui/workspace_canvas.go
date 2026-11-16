package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// Canvas geometry.
const (
	canvasBoxMin = 18 // narrowest node card
	canvasBoxMax = 44 // widest node card
	canvasGutter = 6
	canvasVertX  = 2 // gutter column that carries the vertical connector run
)

// canvasFit picks the card width and the number of whole columns shown for a
// graph of cols columns drawn in avail cells. When every column fits it is the
// widest card (canvasBoxMin to canvasBoxMax) that lets them all fit. Otherwise
// it is the most whole columns that fit at the minimum width, with the cards
// widened to use the room; scroll indicators reserve canvasMarkW cells per side
// that can hide columns.
func canvasFit(cols, avail int) (boxW, shown int) {
	if cols < 1 {
		cols = 1
	}
	width := func(k, reserve int) int {
		return min((avail-reserve-(k-1)*canvasGutter)/k, canvasBoxMax)
	}
	if w := width(cols, 0); w >= canvasBoxMin {
		return w, cols
	}
	for k := cols - 1; k >= 1; k-- {
		reserve := canvasMarkW(cols)
		if cols-k >= 2 {
			reserve *= 2
		}
		if w := width(k, reserve); w >= canvasBoxMin {
			return w, k
		}
	}
	return min(max(canvasBoxMin, 1), max(avail, 1)), 1
}

// canvasMarkW is the cells one scroll indicator ("+N ›") and its gap take.
func canvasMarkW(cols int) int { return len(fmt.Sprint(cols)) + 4 }

// canvasOpts configures renderCanvas.
type canvasOpts struct {
	width, height int
	selected      string            // node ID
	models        map[string]string // role name → short model label, for the node's second line
	runners       map[string]string // role name → runner name, for the card's runner glyph
	icons         string            // ui.icons: unicode | ascii | nerd
	spinner       string            // current spinner frame for running nodes
}

type canvasRect struct{ x, y, w, h int }

type canvasLayout struct {
	boxW   int
	layers [][]*workspace.Node
	layer  map[string]int
	rect   map[string]canvasRect
	far    map[string][]string // dependencies not in the preceding column
	totalW int
	totalH int
}

func computeCanvasLayout(g *workspace.Graph, boxW int) canvasLayout {
	l := canvasLayout{
		boxW:   boxW,
		layers: g.Layers(),
		layer:  map[string]int{},
		rect:   map[string]canvasRect{},
		far:    map[string][]string{},
	}
	for i, col := range l.layers {
		for _, n := range col {
			l.layer[n.ID] = i
		}
	}
	for i, col := range l.layers {
		y := 0
		for _, n := range col {
			for _, d := range n.DependsOn {
				if dl, ok := l.layer[d]; ok && dl != i-1 {
					l.far[n.ID] = append(l.far[n.ID], d)
				}
			}
			h := 4
			if len(l.far[n.ID]) > 0 {
				h = 5
			}
			l.rect[n.ID] = canvasRect{x: i * (boxW + canvasGutter), y: y, w: boxW, h: h}
			y += h + 1
		}
		if y-1 > l.totalH {
			l.totalH = y - 1
		}
	}
	if len(l.layers) > 0 {
		l.totalW = len(l.layers)*(boxW+canvasGutter) - canvasGutter
	}
	return l
}

// nodeStatusKind maps a node to the ui.Status kind it shows.
func nodeStatusKind(g *workspace.Graph, n *workspace.Node) string {
	switch n.Status {
	case workspace.StatusDone:
		return "done"
	case workspace.StatusFailed:
		return "failed"
	case workspace.StatusWaiting:
		if n.Limit != nil {
			return "limited"
		}
		return "waiting"
	case workspace.StatusRunning:
		return "running"
	}
	if !g.Assigned(n) {
		return "unassigned"
	}
	if len(g.Blockers(n.ID)) > 0 {
		return "blocked"
	}
	return "ready"
}

// nodeStatusGlyph returns the status glyph and label of a node. Running nodes
// get a fixed frame; renderCanvas swaps in the live spinner frame.
func nodeStatusGlyph(g *workspace.Graph, n *workspace.Node) (glyph, label string) {
	glyph, label, _ = ui.Status(nodeStatusKind(g, n))
	return glyph, label
}

// canvasNeighbor returns the node to select when moving from selected in dir
// ("left", "right", "up", "down"); the same ID when there is none.
func canvasNeighbor(g *workspace.Graph, selected, dir string) string {
	l := computeCanvasLayout(g, canvasBoxMin)
	li, ok := l.layer[selected]
	if !ok {
		return selected
	}
	switch dir {
	case "up", "down":
		col := l.layers[li]
		for i, n := range col {
			if n.ID != selected {
				continue
			}
			if dir == "up" && i > 0 {
				return col[i-1].ID
			}
			if dir == "down" && i < len(col)-1 {
				return col[i+1].ID
			}
		}
	case "left", "right":
		step := 1
		if dir == "left" {
			step = -1
		}
		cy := l.rect[selected].y
		for j := li + step; j >= 0 && j < len(l.layers); j += step {
			if len(l.layers[j]) == 0 {
				continue
			}
			best, bestD := "", 1<<30
			for _, n := range l.layers[j] {
				d := l.rect[n.ID].y - cy
				if d < 0 {
					d = -d
				}
				if d < bestD {
					best, bestD = n.ID, d
				}
			}
			return best
		}
	}
	return selected
}

// cell is one character of the virtual grid.
type cell struct {
	r    rune
	fg   lipgloss.TerminalColor
	bg   lipgloss.TerminalColor // only the selected card has a background
	bold bool
	wide bool // r is double-width; the next cell is its continuation
	cont bool // second half of a double-width rune; draws nothing
}

type canvasGrid struct {
	w, h  int
	cells [][]cell
}

func newCanvasGrid(w, h int) *canvasGrid {
	g := &canvasGrid{w: w, h: h, cells: make([][]cell, h)}
	for y := range g.cells {
		row := make([]cell, w)
		for x := range row {
			row[x] = cell{r: ' '}
		}
		g.cells[y] = row
	}
	return g
}

func (g *canvasGrid) set(x, y int, c cell) {
	if x >= 0 && y >= 0 && x < g.w && y < g.h {
		g.cells[y][x] = c
	}
}

// runeCells is the display width of r in terminal cells (0, 1 or 2).
func runeCells(r rune) int { return ansi.StringWidth(string(r)) }

// text writes s at x,y by display width, clipped to maxW cells. Double-width
// runes take two cells; zero-width runes are dropped.
func (g *canvasGrid) text(x, y, maxW int, s string, fg lipgloss.TerminalColor, bold bool) {
	g.textOn(x, y, maxW, s, fg, nil, bold)
}

// textOn is text on a background (bg nil = none).
func (g *canvasGrid) textOn(x, y, maxW int, s string, fg, bg lipgloss.TerminalColor, bold bool) {
	i := 0
	for _, r := range s {
		rw := runeCells(r)
		if rw == 0 {
			continue
		}
		if i+rw > maxW {
			return
		}
		if rw == 2 {
			g.set(x+i, y, cell{r: r, fg: fg, bg: bg, bold: bold, wide: true})
			g.set(x+i+1, y, cell{fg: fg, bg: bg, bold: bold, cont: true})
		} else {
			g.set(x+i, y, cell{r: r, fg: fg, bg: bg, bold: bold})
		}
		i += rw
	}
}

const (
	dirN = 1 << iota
	dirE
	dirS
	dirW
)

var junctionGlyph = map[int]rune{
	dirN | dirS:               '│',
	dirE | dirW:               '─',
	dirS | dirE:               '╭',
	dirS | dirW:               '╮',
	dirN | dirE:               '╰',
	dirN | dirW:               '╯',
	dirN | dirS | dirE:        '├',
	dirN | dirS | dirW:        '┤',
	dirE | dirW | dirS:        '┬',
	dirE | dirW | dirN:        '┴',
	dirN | dirE | dirS | dirW: '┼',
	dirN:                      '│',
	dirS:                      '│',
	dirE:                      '─',
	dirW:                      '─',
}

func nodeBorderColor(g *workspace.Graph, n *workspace.Node, selected bool) lipgloss.TerminalColor {
	switch {
	case selected:
		return ui.Accent
	case n.Status == workspace.StatusFailed:
		return ui.Fail
	}
	return ui.Border
}

func statusLabelColor(g *workspace.Graph, n *workspace.Node) lipgloss.TerminalColor {
	_, _, c := ui.Status(nodeStatusKind(g, n))
	return c
}

// drawBox draws one node card: a rounded border, the ID and bold title on the
// first row, the runner glyph, role and model on the second with the status
// right-aligned. The selected card is filled with ui.Sel.
func (c *canvasGrid) drawBox(g *workspace.Graph, n *workspace.Node, r canvasRect, far []string, o canvasOpts) {
	sel := n.ID == o.selected
	bc := nodeBorderColor(g, n, sel)
	var bg lipgloss.TerminalColor
	if sel {
		bg = ui.Sel
	}
	inner := r.w - 2
	tw := inner - 2 // 1 cell of padding on each side

	for y := 0; y < r.h; y++ {
		for x := 0; x < r.w; x++ {
			ch := ' '
			switch {
			case y == 0 && x == 0:
				ch = '╭'
			case y == 0 && x == r.w-1:
				ch = '╮'
			case y == r.h-1 && x == 0:
				ch = '╰'
			case y == r.h-1 && x == r.w-1:
				ch = '╯'
			case y == 0 || y == r.h-1:
				ch = '─'
			case x == 0 || x == r.w-1:
				ch = '│'
			}
			cl := cell{r: ch, bg: bg}
			if ch != ' ' {
				cl.fg = bc
			}
			c.set(r.x+x, r.y+y, cl)
		}
	}

	// Row 1: ID (dim) and title (bold).
	idw := ansi.StringWidth(n.ID)
	c.textOn(r.x+2, r.y+1, tw, truncateOneLine(n.ID, tw), ui.Muted, bg, false)
	if tw-idw-2 >= 3 {
		c.textOn(r.x+2+idw+2, r.y+1, tw-idw-2, truncateOneLine(n.Title, tw-idw-2), ui.Text, bg, true)
	}

	// Row 2, right: status glyph and word (and a warning mark).
	glyph, label := nodeStatusGlyph(g, n)
	if n.Status == workspace.StatusRunning && o.spinner != "" {
		glyph = o.spinner
	}
	status := glyph + " " + label
	if len(n.Warnings) > 0 && ansi.StringWidth(status)+2 <= tw-10 {
		status += " ⚠"
	}
	sw := min(ansi.StringWidth(status), tw)
	c.textOn(r.x+r.w-2-sw, r.y+2, sw, truncateOneLine(status, sw), statusLabelColor(g, n), bg, false)
	if len(n.Warnings) > 0 && strings.HasSuffix(status, "⚠") {
		c.textOn(r.x+r.w-3, r.y+2, 1, "⚠", ui.Warn, bg, false)
	}

	// Row 2, left: runner glyph, role, model.
	x, room := r.x+2, tw-sw-2
	switch {
	case n.Kind == workspace.KindCheck:
		c.textOn(x, r.y+2, room, truncateOneLine("build & test", room), ui.Muted, bg, false)
	case n.Role != "":
		runner := o.runners[n.Role]
		gl := runnerGlyph(runner, o.icons)
		c.textOn(x, r.y+2, room, gl, runnerColor(runner), bg, false)
		gw := ansi.StringWidth(gl) + 1
		x, room = x+gw, room-gw
		role := truncateOneLine(n.Role, room)
		c.textOn(x, r.y+2, room, role, ui.Text, bg, false)
		rw := ansi.StringWidth(role)
		x, room = x+rw, room-rw
		model := strings.TrimPrefix(o.models[n.Role], runner+":")
		if model != "" && model != runner && room > 5 {
			c.textOn(x, r.y+2, room, truncateOneLine(" · "+model, room), ui.Muted, bg, false)
		}
	case n.SuggestedRole != "":
		// The arrow and the status never truncate; only the role name does.
		room = tw - sw - 1
		label := "→"
		if room >= 3 {
			label = truncateOneLine("→ "+n.SuggestedRole, room)
		}
		c.textOn(x, r.y+2, room, label, ui.Muted, bg, false)
	}
	if len(far) > 0 {
		c.textOn(r.x+2, r.y+3, tw, truncateOneLine("◂ "+strings.Join(far, ","), tw), ui.Muted, bg, false)
	}
}

func renderCanvas(g *workspace.Graph, o canvasOpts) string {
	if o.width <= 0 || o.height <= 0 {
		return ""
	}
	out := newCanvasGrid(o.width, o.height)
	if g == nil || len(g.Nodes) == 0 {
		msg := "no nodes yet"
		out.text(max((o.width-ansi.StringWidth(msg))/2, 0), o.height/2, o.width, msg, ui.Muted, false)
		return out.render()
	}

	nCols := len(g.Layers())
	boxW, shown := canvasFit(nCols, o.width)
	l := computeCanvasLayout(g, boxW)
	grid := newCanvasGrid(l.totalW, l.totalH)
	for _, n := range g.Nodes {
		if r, ok := l.rect[n.ID]; ok {
			grid.drawBox(g, n, r, l.far[n.ID], o)
		}
	}

	// Connectors: collect direction bits per gutter cell, then merge to glyphs.
	type conn struct {
		mask  int
		arrow bool
		fg    lipgloss.TerminalColor
	}
	conns := map[[2]int]*conn{}
	touch := func(x, y, dir int, fg lipgloss.TerminalColor) {
		k := [2]int{x, y}
		c := conns[k]
		if c == nil {
			c = &conn{fg: fg}
			conns[k] = c
		}
		c.mask |= dir
		if fg == ui.Accent {
			c.fg = fg
		}
	}
	for _, t := range g.Nodes {
		tr, ok := l.rect[t.ID]
		if !ok {
			continue
		}
		for _, d := range t.DependsOn {
			sl, sok := l.layer[d]
			if !sok || sl != l.layer[t.ID]-1 {
				continue
			}
			sr := l.rect[d]
			fg := ui.Border
			if d == o.selected || t.ID == o.selected {
				fg = ui.Accent
			}
			gx := sr.x + sr.w
			ys, yt := sr.y+1, tr.y+1
			// Path points (gutter-relative x): (0,ys) -> (vx,ys) -> (vx,yt) -> (5,yt).
			path := [][2]int{}
			for x := 0; x <= canvasVertX; x++ {
				path = append(path, [2]int{gx + x, ys})
			}
			for y := ys; y != yt; {
				if yt > ys {
					y++
				} else {
					y--
				}
				path = append(path, [2]int{gx + canvasVertX, y})
			}
			for x := canvasVertX + 1; x < canvasGutter; x++ {
				path = append(path, [2]int{gx + x, yt})
			}
			for i := range path {
				if i > 0 {
					a, b := path[i-1], path[i]
					switch {
					case b[0] > a[0]:
						touch(a[0], a[1], dirE, fg)
						touch(b[0], b[1], dirW, fg)
					case b[1] > a[1]:
						touch(a[0], a[1], dirS, fg)
						touch(b[0], b[1], dirN, fg)
					case b[1] < a[1]:
						touch(a[0], a[1], dirN, fg)
						touch(b[0], b[1], dirS, fg)
					}
				}
			}
			// First cell touches the source box on its west side.
			touch(path[0][0], path[0][1], dirW, fg)
			last := path[len(path)-1]
			conns[[2]int{last[0], last[1]}].arrow = true
		}
	}
	for k, c := range conns {
		r := junctionGlyph[c.mask]
		if c.arrow {
			r = '▶'
		}
		if r == 0 {
			r = '─'
		}
		grid.set(k[0], k[1], cell{r: r, fg: c.fg})
	}

	// Window: whole columns only, scrolled minimally to keep the selected
	// column visible.
	first := 0
	if _, ok := l.rect[o.selected]; ok {
		first = max(l.layer[o.selected]-shown+1, 0)
	}
	hiddenR := nCols - first - shown
	markW := canvasMarkW(nCols)
	ox := first * (boxW + canvasGutter)
	winW := shown*boxW + (shown-1)*canvasGutter
	x0 := 0
	if first > 0 && markW+winW <= o.width {
		x0 = markW
	}
	room := x0 > 0 || first == 0 && winW+markW <= o.width // margins fit the indicators
	oy := 0
	if sr, ok := l.rect[o.selected]; ok {
		if sr.y+sr.h > oy+o.height {
			oy = sr.y + sr.h - o.height
		}
		if sr.y < oy {
			oy = sr.y
		}
	}
	for y := 0; y < o.height; y++ {
		for x := 0; x < winW && x0+x < o.width; x++ {
			gx, gy := x+ox, y+oy
			if gx < grid.w && gy < grid.h {
				out.cells[y][x0+x] = grid.cells[gy][gx]
			}
		}
	}

	// Scroll indicators sit in the reserved side margins; the vertical marks
	// never cover the selected card.
	note := func(x, y int, text string) {
		for i, r := range []rune(text) {
			if x+i >= 0 && x+i < o.width && y >= 0 && y < o.height {
				out.cells[y][x+i] = cell{r: r, fg: ui.Muted}
			}
		}
	}
	if first > 0 && room {
		note(0, o.height/2, fmt.Sprintf("‹ +%d", first))
	}
	if hiddenR > 0 && room {
		t := fmt.Sprintf("+%d ›", hiddenR)
		note(o.width-len([]rune(t)), o.height/2, t)
	}
	mark := func(x, y int, r rune) {
		if x >= 0 && y >= 0 && x < o.width && y < o.height && out.cells[y][x].bg == nil {
			out.cells[y][x] = cell{r: r, fg: ui.Muted}
		}
	}
	if oy > 0 {
		mark(o.width/2, 0, '▴')
	}
	if oy+o.height < grid.h {
		mark(o.width/2, o.height-1, '▾')
	}
	return out.render()
}

// render converts the grid to styled lines, grouping runs of equal style.
func (g *canvasGrid) render() string {
	lines := make([]string, g.h)
	for y, row := range g.cells {
		var sb strings.Builder
		for x := 0; x < len(row); {
			e := x
			var run strings.Builder
			for e < len(row) && row[e].fg == row[x].fg && row[e].bg == row[x].bg && row[e].bold == row[x].bold {
				c := row[e]
				switch {
				case c.cont:
					// Drawn by its wide lead; an orphaned half becomes a space.
					if e == 0 || !row[e-1].wide {
						run.WriteRune(' ')
					}
				case c.wide:
					if e+1 < len(row) && row[e+1].cont {
						run.WriteRune(c.r)
					} else {
						run.WriteRune(' ')
					}
				default:
					run.WriteRune(c.r)
				}
				e++
			}
			if row[x].fg == nil && row[x].bg == nil && !row[x].bold {
				sb.WriteString(run.String())
			} else {
				sb.WriteString(lipgloss.NewStyle().Foreground(row[x].fg).Background(row[x].bg).Bold(row[x].bold).Render(run.String()))
			}
			x = e
		}
		lines[y] = sb.String()
	}
	return strings.Join(lines, "\n")
}
