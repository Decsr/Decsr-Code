package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func TestCanvasFitAdaptsToPane(t *testing.T) {
	cases := []struct{ cols, avail, wantW, wantN int }{
		{1, 200, canvasBoxMax, 1},
		{3, 100, 29, 3},
		{3, 118, 35, 3},
		{3, 60, 24, 2},
		{3, 80, 22, 3},
		{3, 40, 30, 1},
		{0, 100, canvasBoxMax, 1},
	}
	for _, c := range cases {
		w, n := canvasFit(c.cols, c.avail)
		if w != c.wantW || n != c.wantN {
			t.Errorf("canvasFit(%d, %d) = %d,%d want %d,%d", c.cols, c.avail, w, n, c.wantW, c.wantN)
		}
	}
}

func TestCanvasCardsUseTheWidth(t *testing.T) {
	g := wsFixtureGraph()
	g.Node("T1").Title = "Parse the flags and build the output"
	cardLen := func(w int) int {
		for _, l := range strings.Split(plain(g, canvasOpts{width: w, height: 20}), "\n") {
			if i := strings.Index(l, "╭"); i >= 0 {
				if j := strings.Index(l[i:], "╮"); j >= 0 {
					return ansi.StringWidth(l[i : i+j+len("╮")])
				}
			}
		}
		return 0
	}
	narrow, wide := cardLen(70), cardLen(200)
	if narrow < canvasBoxMin || wide <= narrow || wide > canvasBoxMax {
		t.Fatalf("card width: narrow %d, wide %d", narrow, wide)
	}
	if out := plain(g, canvasOpts{width: 200, height: 20}); !strings.Contains(out, "Parse the flags and build the output") {
		t.Fatalf("the wide card still truncates the title:\n%s", out)
	}
}

func TestCanvasRunnerGlyphModes(t *testing.T) {
	g := diamond()
	for _, n := range g.Nodes {
		n.Role = "coder"
	}
	opts := canvasOpts{width: 120, height: 20, runners: map[string]string{"coder": "claude"}}
	if out := plain(g, opts); !strings.Contains(out, "✳ coder") {
		t.Fatalf("unicode glyph missing:\n%s", out)
	}
	opts.icons = "ascii"
	if out := plain(g, opts); !strings.Contains(out, "[C] coder") {
		t.Fatalf("ascii glyph missing:\n%s", out)
	}
}

// A rendered workspace has no doubled borders where panes meet.
func TestWorkspaceViewHasNoDoubleBorders(t *testing.T) {
	for _, w := range []int{160, 100} {
		h := newWSHarness(t)
		h.size(w, 40)
		h.toGraph()
		for i, sel := range []string{"T1", "T2", "CHECK"} {
			h.m.sel = sel
			v := ansi.Strip(h.view())
			for _, bad := range []string{"┐┌", "││", "╮╭", "╮┌", "┐╭"} {
				if strings.Contains(v, bad) {
					t.Fatalf("width %d, selected %s (%d): found %q:\n%s", w, sel, i, bad, v)
				}
			}
		}
	}
}

func TestWorkspaceHeaderAndPromptLook(t *testing.T) {
	h := newWSHarness(t)
	h.size(120, 36)
	h.toGraph()
	v := ansi.Strip(h.view())
	lines := strings.Split(v, "\n")
	if !strings.Contains(lines[0], "◆ decsr") || !strings.Contains(lines[0], "done") || !strings.Contains(lines[0], "tok") {
		t.Fatalf("header line: %q", lines[0])
	}
	for _, label := range []string{"ROLE", "DESCRIPTION", "FILES", "SUMMARY", "TOKENS", "GRAPH"} {
		if strings.Contains(v, label) {
			t.Errorf("all-caps label %q is still drawn:\n%s", label, v)
		}
	}
	if !strings.Contains(v, "╭") || strings.Contains(v, "┌") {
		t.Errorf("want rounded corners only:\n%s", v)
	}
}

func TestGuidancePartsJoinLikeGuidance(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.g.Node("T1").Status = workspace.StatusFailed
	h.m.g.Node("T1").Error = "boom"
	short, long, isErr := h.m.guidanceParts()
	full, fullErr := h.m.guidance()
	if !isErr || !fullErr || short == "" || long == "" || full != short+". "+long {
		t.Fatalf("parts %q + %q do not join to %q", short, long, full)
	}
}

// wholeCards fails when a rendered line has a partial card: every corner must
// have its partner on the same line.
func wholeCards(t *testing.T, out string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		for _, p := range [][2]string{{"╭", "╮"}, {"╰", "╯"}} {
			if strings.Count(l, p[0]) != strings.Count(l, p[1]) {
				t.Fatalf("partial card in %q\n%s", l, out)
			}
		}
		// A connector must end in an arrow or a card.
		tr := strings.TrimRight(l, " ")
		if strings.HasSuffix(tr, "─") || strings.HasSuffix(tr, "┬") || strings.HasSuffix(tr, "┤") {
			t.Fatalf("dangling connector in %q\n%s", l, out)
		}
	}
}

func TestCanvasWholeColumnsOnly(t *testing.T) {
	g := chainGraph(3, 3) // three columns
	for w := 18; w <= 110; w++ {
		for _, sel := range []string{"", "T1", "T2", "T3"} {
			wholeCards(t, plain(g, canvasOpts{width: w, height: 12, selected: sel}))
		}
	}
}

func TestCanvasIndicators(t *testing.T) {
	g := chainGraph(3, 3)
	out := plain(g, canvasOpts{width: 50, height: 12, selected: "T1"})
	if !strings.Contains(out, "+1 ›") || strings.Contains(out, "‹") {
		t.Fatalf("right indicator expected:\n%s", out)
	}
	if strings.Contains(out, "T3") {
		t.Fatalf("hidden column drawn:\n%s", out)
	}
	out = plain(g, canvasOpts{width: 50, height: 12, selected: "T3"})
	if !strings.Contains(out, "‹ +1") || strings.Contains(out, "›") || !strings.Contains(out, "T3") {
		t.Fatalf("selecting a hidden node must scroll it in:\n%s", out)
	}
	out = plain(g, canvasOpts{width: 38, height: 12, selected: "T2"})
	if !strings.Contains(out, "‹ +1") || !strings.Contains(out, "+1 ›") && !strings.Contains(out, "T2") {
		t.Fatalf("both indicators expected:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) != 38 {
			t.Fatalf("line width %d", ansi.StringWidth(l))
		}
	}
}

func TestSuggestedLabelKeepsArrowAndStatus(t *testing.T) {
	g := &workspace.Graph{Nodes: []*workspace.Node{cnode("T1", "Write docs")}}
	g.Nodes[0].SuggestedRole = "documentation_writer"
	for w := canvasBoxMin; w <= 44; w++ {
		out := plain(g, canvasOpts{width: w, height: 6})
		if !strings.Contains(out, "→") || !strings.Contains(out, "? unassigned") {
			t.Fatalf("width %d lost the arrow or status:\n%s", w, out)
		}
		if strings.Contains(out, "suggested") {
			t.Fatalf("old label at width %d", w)
		}
	}
	if out := plain(g, canvasOpts{width: 30, height: 6}); !strings.Contains(out, "→ docu…") && !strings.Contains(out, "→ doc") {
		t.Fatalf("role not shown:\n%s", out)
	}
}
