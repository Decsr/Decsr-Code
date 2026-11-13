package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// Canvas geometry.
const (
	canvasBoxW   = 26
	canvasGutter = 6
	canvasColW   = canvasBoxW + canvasGutter
	canvasVertX  = 2 // gutter column that carries the vertical connector run
)

// canvasOpts configures renderCanvas.
type canvasOpts struct {
	width, height int
	selected      string            // node ID
	models        map[string]string // role name → short model label, for the node's second line
	spinner       string            // current spinner frame for running nodes
}

type canvasRect struct{ x, y, w, h int }

type canvasLayout struct {
	layers [][]*workspace.Node
	layer  map[string]int
	rect   map[string]canvasRect
	far    map[string][]string // dependencies not in the preceding column
	totalW int
	totalH int
}

func computeCanvasLayout(g *workspace.Graph) canvasLayout {
	l := canvasLayout{
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
			l.rect[n.ID] = canvasRect{x: i * canvasColW, y: y, w: canvasBoxW, h: h}
			y += h + 1
		}
		if y-1 > l.totalH {
			l.totalH = y - 1
		}
	}
	if len(l.layers) > 0 {
		l.totalW = len(l.layers)*canvasColW - canvasGutter
	}
	return l
}

// nodeStatusGlyph returns the status glyph and label of a node. Running nodes
// get a fixed frame; renderCanvas swaps in the live spinner frame.
func nodeStatusGlyph(g *workspace.Graph, n *workspace.Node) (glyph, label string) {
	switch n.Status {
	case workspace.StatusDone:
		return "✓", "done"
	case workspace.StatusFailed:
		return "!", "failed"
	case workspace.StatusRunning:
		return "▖", "running"
	}
	if !g.Assigned(n) {
		return "?", "unassigned"
	}
	if len(g.Blockers(n.ID)) > 0 {
		return "⏸", "blocked"
	}
	return "○", "ready"
}

// canvasNeighbor returns the node to select when moving from selected in dir
// ("left", "right", "up", "down"); the same ID when there is none.
func canvasNeighbor(g *workspace.Graph, selected, dir string) string {
	l := computeCanvasLayout(g)
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
	fg   lipgloss.Color
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
func (g *canvasGrid) text(x, y, maxW int, s string, fg lipgloss.Color, bold bool) {
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
			g.set(x+i, y, cell{r: r, fg: fg, bold: bold, wide: true})
			g.set(x+i+1, y, cell{fg: fg, bold: bold, cont: true})
		} else {
			g.set(x+i, y, cell{r: r, fg: fg, bold: bold})
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
	dirS | dirE:               '┌',
	dirS | dirW:               '┐',
	dirN | dirE:               '└',
	dirN | dirW:               '┘',
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

func nodeBorderColor(g *workspace.Graph, n *workspace.Node, selected bool) lipgloss.Color {
	switch {
	case selected:
		return focusBorder
	case n.Status == workspace.StatusRunning:
		return roleColor(n.Role)
	case n.Status == workspace.StatusDone:
		return crt.success
	case n.Status == workspace.StatusFailed:
		return crt.warn
	}
	return crt.border
}

func statusLabelColor(g *workspace.Graph, n *workspace.Node) lipgloss.Color {
	switch n.Status {
	case workspace.StatusDone:
		return crt.success
	case workspace.StatusFailed:
		return crt.warn
	case workspace.StatusRunning:
		return roleColor(n.Role)
	}
	if !g.Assigned(n) {
		return galaxyGold
	}
	if len(g.Blockers(n.ID)) > 0 {
		return crt.dim
	}
	return crt.primary
}

func (c *canvasGrid) drawBox(g *workspace.Graph, n *workspace.Node, r canvasRect, far []string, o canvasOpts) {
	sel := n.ID == o.selected
	bc := nodeBorderColor(g, n, sel)
	inner := r.w - 2

	// Borders.
	for x := 1; x < r.w-1; x++ {
		c.set(r.x+x, r.y, cell{r: '─', fg: bc, bold: sel})
		c.set(r.x+x, r.y+r.h-1, cell{r: '─', fg: bc, bold: sel})
	}
	for y := 1; y < r.h-1; y++ {
		c.set(r.x, r.y+y, cell{r: '│', fg: bc, bold: sel})
		c.set(r.x+r.w-1, r.y+y, cell{r: '│', fg: bc, bold: sel})
		for x := 1; x < r.w-1; x++ {
			c.set(r.x+x, r.y+y, cell{r: ' '})
		}
	}
	c.set(r.x, r.y, cell{r: '┌', fg: bc, bold: sel})
	c.set(r.x+r.w-1, r.y, cell{r: '┐', fg: bc, bold: sel})
	c.set(r.x, r.y+r.h-1, cell{r: '└', fg: bc, bold: sel})
	c.set(r.x+r.w-1, r.y+r.h-1, cell{r: '┘', fg: bc, bold: sel})

	// Top border: ID on the left, status on the right.
	glyph, label := nodeStatusGlyph(g, n)
	if n.Status == workspace.StatusRunning && o.spinner != "" {
		glyph = o.spinner
	}
	status := " " + glyph + " " + label + " "
	warned := " " + glyph + " " + label + " ⚠ "
	lead := "─ "
	if sel {
		lead = "▌ "
	}
	id := truncateOneLine(n.ID, inner-ansi.StringWidth(status)-3)
	left := lead + id + " "
	// Keep at least one fill dash between ID and status; shrink the label if needed.
	room := inner - ansi.StringWidth(left) - 1
	if len(n.Warnings) > 0 && ansi.StringWidth(warned) <= room {
		status = warned
	}
	if ansi.StringWidth(status) > room {
		status = truncateOneLine(status, max(room, 0))
	}
	sw := ansi.StringWidth(status)
	c.text(r.x+1, r.y, inner, left, bc, true)
	c.text(r.x+r.w-1-sw, r.y, sw, status, statusLabelColor(g, n), false)
	if sel {
		c.set(r.x+1, r.y, cell{r: '▌', fg: focusBorder, bold: true})
	}

	// Line 1: title. Line 2: role / suggestion.
	tw := inner - 2
	c.text(r.x+2, r.y+1, tw, truncateOneLine(n.Title, tw), crt.primary, sel)

	switch {
	case n.Kind == workspace.KindCheck:
		c.text(r.x+2, r.y+2, tw, "build & test", crt.dim, false)
	case n.Role != "":
		s := n.Role
		if m := o.models[n.Role]; m != "" {
			s += " · " + m
		}
		c.text(r.x+2, r.y+2, tw, truncateOneLine(s, tw), roleColor(n.Role), false)
	case n.SuggestedRole != "":
		c.text(r.x+2, r.y+2, tw, truncateOneLine("? suggested: "+n.SuggestedRole, tw), galaxyGold, false)
	default:
		c.text(r.x+2, r.y+2, tw, "? unassigned", galaxyGold, false)
	}
	if len(far) > 0 {
		c.text(r.x+2, r.y+3, tw, truncateOneLine("◂ "+strings.Join(far, ","), tw), crt.dim, false)
	}
}

func renderCanvas(g *workspace.Graph, o canvasOpts) string {
	if o.width <= 0 || o.height <= 0 {
		return ""
	}
	out := newCanvasGrid(o.width, o.height)
	if g == nil || len(g.Nodes) == 0 {
		msg := "no nodes yet"
		out.text(max((o.width-ansi.StringWidth(msg))/2, 0), o.height/2, o.width, msg, crt.dim, false)
		return out.render()
	}

	l := computeCanvasLayout(g)
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
		fg    lipgloss.Color
	}
	conns := map[[2]int]*conn{}
	touch := func(x, y, dir int, fg lipgloss.Color) {
		k := [2]int{x, y}
		c := conns[k]
		if c == nil {
			c = &conn{fg: fg}
			conns[k] = c
		}
		c.mask |= dir
		if fg == focusBorder {
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
			fg := crt.dim
			if d == o.selected || t.ID == o.selected {
				fg = focusBorder
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

	// Offset: minimal scroll that keeps the selected box fully visible.
	ox, oy := 0, 0
	if sr, ok := l.rect[o.selected]; ok {
		if sr.x+sr.w > ox+o.width {
			ox = sr.x + sr.w - o.width
		}
		if sr.x < ox {
			ox = sr.x
		}
		if sr.y+sr.h > oy+o.height {
			oy = sr.y + sr.h - o.height
		}
		if sr.y < oy {
			oy = sr.y
		}
	}
	for y := 0; y < o.height; y++ {
		for x := 0; x < o.width; x++ {
			gx, gy := x+ox, y+oy
			if gx < grid.w && gy < grid.h {
				out.cells[y][x] = grid.cells[gy][gx]
			}
		}
	}

	// Scroll marks (never over the selection marker).
	mark := func(x, y int, r rune) {
		if x >= 0 && y >= 0 && x < o.width && y < o.height && out.cells[y][x].r != '▌' {
			out.cells[y][x] = cell{r: r, fg: crt.dim}
		}
	}
	if ox > 0 {
		mark(0, o.height/2, '‹')
	}
	if ox+o.width < grid.w {
		mark(o.width-1, o.height/2, '›')
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
			for e < len(row) && row[e].fg == row[x].fg && row[e].bold == row[x].bold {
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
			if row[x].fg == "" && !row[x].bold {
				sb.WriteString(run.String())
			} else {
				sb.WriteString(lipgloss.NewStyle().Foreground(row[x].fg).Bold(row[x].bold).Render(run.String()))
			}
			x = e
		}
		lines[y] = sb.String()
	}
	return strings.Join(lines, "\n")
}
