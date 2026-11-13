package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func cnode(id, title string, deps ...string) *workspace.Node {
	return &workspace.Node{ID: id, Title: title, Kind: workspace.KindWork, Status: workspace.StatusTodo, DependsOn: deps}
}

func diamond() *workspace.Graph {
	t1 := cnode("T1", "Root task")
	t1.Role = "coder"
	return &workspace.Graph{Nodes: []*workspace.Node{t1, cnode("T2", "Left branch", "T1"), cnode("T3", "Right branch", "T1"), cnode("T4", "Join all", "T2", "T3")}}
}

func chainGraph(n, layers int) *workspace.Graph {
	g := &workspace.Graph{}
	for i := 0; i < n; i++ {
		var deps []string
		if l := i % layers; l > 0 && i >= layers {
			deps = []string{fmt.Sprintf("T%d", i-layers+1)}
		} else if i > 0 && i < layers {
			deps = []string{fmt.Sprintf("T%d", i)}
		}
		g.Nodes = append(g.Nodes, cnode(fmt.Sprintf("T%d", i+1), fmt.Sprintf("Task number %d", i+1), deps...))
	}
	return g
}

func oneLayer(n int) *workspace.Graph {
	g := &workspace.Graph{}
	for i := 1; i <= n; i++ {
		g.Nodes = append(g.Nodes, cnode(fmt.Sprintf("T%d", i), fmt.Sprintf("Task number %d", i)))
	}
	return g
}

func plain(g *workspace.Graph, o canvasOpts) string { return ansi.Strip(renderCanvas(g, o)) }

func TestCanvasSizeExact(t *testing.T) {
	graphs := map[string]*workspace.Graph{
		"one":     {Nodes: []*workspace.Node{cnode("T1", "Only")}},
		"diamond": diamond(),
		"30x8":    chainGraph(30, 8),
		"12col":   oneLayer(12),
	}
	for name, g := range graphs {
		for _, sz := range [][2]int{{60, 12}, {100, 30}, {200, 50}, {20, 5}, {10, 3}} {
			for _, sel := range []string{"", g.Nodes[len(g.Nodes)-1].ID} {
				out := renderCanvas(g, canvasOpts{width: sz[0], height: sz[1], selected: sel})
				lines := strings.Split(out, "\n")
				if len(lines) != sz[1] {
					t.Fatalf("%s %v: %d lines, want %d", name, sz, len(lines), sz[1])
				}
				for i, l := range lines {
					if w := ansi.StringWidth(l); w != sz[0] {
						t.Fatalf("%s %v line %d width %d: %q", name, sz, i, w, ansi.Strip(l))
					}
				}
			}
		}
	}
}

func TestCanvasShowsNodeText(t *testing.T) {
	a := cnode("T1", "Wire login")
	a.Role = "coder"
	a.Status = workspace.StatusDone
	b := cnode("T2", "Review it", "T1")
	b.SuggestedRole = "qa"
	c := cnode("T3", "Broken thing")
	c.Role = "coder"
	c.Status = workspace.StatusFailed
	d := cnode("T4", "Waits", "T3")
	d.Role = "coder"
	chk := &workspace.Node{ID: "CHECK", Title: "Build & test", Kind: workspace.KindCheck, Status: workspace.StatusTodo, DependsOn: []string{"T1"}}
	g := &workspace.Graph{Nodes: []*workspace.Node{a, b, c, d, chk}}
	out := plain(g, canvasOpts{width: 120, height: 30, models: map[string]string{"coder": "m"}})
	for _, want := range []string{"T1", "Wire login", "coder · m", "? suggested: qa", "build & test", "✓ done", "! failed", "⏸ blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCanvasSelectedVisible(t *testing.T) {
	g := chainGraph(30, 8)
	for _, n := range g.Nodes {
		out := plain(g, canvasOpts{width: 60, height: 12, selected: n.ID})
		if !strings.Contains(out, "▌ "+n.ID+" ") {
			t.Fatalf("selected %s not visible:\n%s", n.ID, out)
		}
		// the whole box (bottom border too) must be visible
		if !strings.Contains(out, "└") {
			t.Fatalf("no box bottom for %s", n.ID)
		}
	}
	g = oneLayer(12)
	for _, n := range g.Nodes {
		out := plain(g, canvasOpts{width: 60, height: 16, selected: n.ID})
		if !strings.Contains(out, "▌ "+n.ID+" ") || !strings.Contains(out, n.Title) {
			t.Fatalf("selected %s not visible in 12-column graph:\n%s", n.ID, out)
		}
	}
}

func TestCanvasConnectors(t *testing.T) {
	out := plain(diamond(), canvasOpts{width: 120, height: 20})
	if !strings.Contains(out, "▶") {
		t.Fatalf("no arrow:\n%s", out)
	}
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Root task") {
			row = l
		}
	}
	if !strings.Contains(row[strings.Index(row, "Root task"):], "│──") {
		t.Fatalf("no connector right of T1: %q", row)
	}
	if !strings.Contains(out, "┐") && !strings.Contains(out, "┤") {
		t.Fatalf("no junction glyphs:\n%s", out)
	}
}

func TestCanvasFarDependencyFooter(t *testing.T) {
	g := &workspace.Graph{Nodes: []*workspace.Node{cnode("T1", "a"), cnode("T2", "b", "T1"), cnode("T3", "c", "T2", "T1")}}
	out := plain(g, canvasOpts{width: 120, height: 12})
	if !strings.Contains(out, "◂ T1") {
		t.Fatalf("no footer:\n%s", out)
	}
	if strings.Contains(out, "◂ T2") {
		t.Fatalf("adjacent dependency listed in footer:\n%s", out)
	}
}

func TestCanvasScrollMarks(t *testing.T) {
	out := plain(chainGraph(30, 8), canvasOpts{width: 60, height: 12, selected: "T1"})
	if !strings.Contains(out, "›") {
		t.Fatalf("no right mark:\n%s", out)
	}
	out = plain(chainGraph(30, 8), canvasOpts{width: 60, height: 12, selected: "T30"})
	if !strings.Contains(out, "‹") {
		t.Fatalf("no left mark:\n%s", out)
	}
}

func TestCanvasNeighbor(t *testing.T) {
	g := diamond()
	cases := []struct{ from, dir, want string }{
		{"T1", "right", "T2"}, {"T2", "down", "T3"}, {"T3", "right", "T4"}, {"T1", "left", "T1"},
		{"T2", "up", "T2"}, {"T3", "up", "T2"}, {"T4", "right", "T4"}, {"T4", "left", "T2"},
	}
	for _, c := range cases {
		if got := canvasNeighbor(g, c.from, c.dir); got != c.want {
			t.Errorf("%s %s = %s, want %s", c.from, c.dir, got, c.want)
		}
	}
}

func TestCanvasEmpty(t *testing.T) {
	out := plain(&workspace.Graph{}, canvasOpts{width: 40, height: 7})
	if !strings.Contains(out, "no nodes yet") {
		t.Fatalf("got:\n%s", out)
	}
	if n := len(strings.Split(out, "\n")); n != 7 {
		t.Fatalf("%d lines", n)
	}
}

func TestNodeStatusGlyph(t *testing.T) {
	root := cnode("T1", "r")
	root.Role = "coder"
	g := &workspace.Graph{Nodes: []*workspace.Node{root, cnode("T2", "x"), cnode("T3", "y", "T2"), cnode("T4", "z")}}
	g.Nodes[3].Role = "coder"
	check := func(n *workspace.Node, glyph, label string) {
		t.Helper()
		gl, lb := nodeStatusGlyph(g, n)
		if gl != glyph || lb != label {
			t.Errorf("%s: got %q %q, want %q %q", n.ID, gl, lb, glyph, label)
		}
	}
	check(g.Nodes[3], "○", "ready")
	check(g.Nodes[1], "?", "unassigned")
	g.Nodes[2].Role = "qa"
	check(g.Nodes[2], "⏸", "blocked")
	g.Nodes[0].Status = workspace.StatusDone
	check(g.Nodes[0], "✓", "done")
	g.Nodes[0].Status = workspace.StatusFailed
	check(g.Nodes[0], "!", "failed")
	g.Nodes[0].Status = workspace.StatusRunning
	gl, lb := nodeStatusGlyph(g, g.Nodes[0])
	if gl == "" || lb != "running" {
		t.Errorf("running: %q %q", gl, lb)
	}
}
