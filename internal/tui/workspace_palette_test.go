package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func TestFuzzyScore(t *testing.T) {
	if _, ok := fuzzyScore("xyz", "review"); ok {
		t.Error("xyz is not a subsequence of review")
	}
	if _, ok := fuzzyScore("rv", "REVIEW"); !ok {
		t.Error("matching ignores case")
	}
	if s, ok := fuzzyScore("", "anything"); !ok || s != 0 {
		t.Errorf("empty query: %d %v", s, ok)
	}
	order := func(q, better, worse string) {
		t.Helper()
		a, ok1 := fuzzyScore(q, better)
		b, ok2 := fuzzyScore(q, worse)
		if !ok1 || !ok2 || a <= b {
			t.Errorf("%q: %q (%d,%v) should beat %q (%d,%v)", q, better, a, ok1, worse, b, ok2)
		}
	}
	order("rev", "review", "refactor everything")      // contiguous beats scattered
	order("rev", "review flow", "a review")            // prefix beats middle
	order("rev", "/review", "/resume everything")      // contiguous at a word start
	order("stat", "/status", "start the thing at all") // contiguous beats scattered
	order("rev", "Review", "Refactor review flow")     // shorter and earlier wins
}

func palSample() palSources {
	g := &workspace.Graph{Nodes: []*workspace.Node{
		{ID: "T1", Title: "Add search", Kind: workspace.KindWork, Status: workspace.StatusDone},
		{ID: "T6", Title: "Review error messages", Kind: workspace.KindWork, Status: workspace.StatusTodo},
	}}
	return palSources{
		inbox:    []inboxItem{{kind: inboxReview, label: "Lead review", text: "3 changes", glyph: "◆"}, {kind: inboxBudget, label: "Budget", text: "82%", glyph: "·", info: true}},
		commands: []wsCommand{{name: "/review", desc: "run a lead review now"}, {name: "/status", desc: "project"}, {name: "/resume", desc: "continue a session"}},
		g:        g, nodes: g.Nodes,
		sessions: []workspace.SessionInfo{{ID: "s1", Title: "Refactor review flow", Done: 5, Todo: 0}, {ID: "s2", Title: "Other work"}},
		projects: []wsProject{{path: "/p/one", name: "one"}},
	}
}

func TestPaletteGroupsAndOrder(t *testing.T) {
	age := func(workspace.SessionInfo) string { return "2d" }
	got := buildPalette("rev", palSample(), age)
	if len(got) != 5 {
		t.Fatalf("groups = %d", len(got))
	}
	counts := []int{len(got[0]), len(got[1]), len(got[2]), len(got[3]), len(got[4])}
	if want := []int{1, 1, 1, 1, 0}; fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Errorf("counts = %v, want %v", counts, want)
	}
	if got[1][0].cmd.name != "/review" || got[2][0].ref != "T6" || got[3][0].ref != "s1" || got[0][0].inbox.kind != inboxReview {
		t.Errorf("rows: %+v", got)
	}
	if got[3][0].right != "5/5 · 2d" {
		t.Errorf("session right = %q", got[3][0].right)
	}

	// Empty query: only the items that need the user, then the others.
	empty := buildPalette("", palSample(), age)
	if len(empty[0]) != 1 || len(empty[1]) != 3 || len(empty[2]) != 2 || len(empty[3]) != 2 || len(empty[4]) != 1 {
		t.Errorf("empty query counts: %d %d %d %d %d", len(empty[0]), len(empty[1]), len(empty[2]), len(empty[3]), len(empty[4]))
	}
	// A search also finds info items; groups past the cap are cut.
	if r := buildPalette("budget", palSample(), age); len(r[0]) != 1 || r[0][0].inbox.kind != inboxBudget {
		t.Errorf("budget search: %+v", r[0])
	}
	src := palSample()
	for i := 0; i < 10; i++ {
		src.commands = append(src.commands, wsCommand{name: "/cmd" + string(rune('a'+i))})
	}
	if r := buildPalette("", src, age); len(r[1]) != palEmptyPerGroup {
		t.Errorf("empty query shows %d commands", len(r[1]))
	}
	if r := buildPalette("cmd", src, age); len(r[1]) != palMaxPerGroup {
		t.Errorf("a query shows %d commands", len(r[1]))
	}
	// Best match first inside a group.
	src = palSample()
	src.commands = []wsCommand{{name: "/resume", desc: "review the session"}, {name: "/review"}}
	if r := buildPalette("rev", src, age); r[1][0].cmd.name != "/review" {
		t.Errorf("order: %s first", r[1][0].cmd.name)
	}
}

// palHarness is a Workspace with sessions, projects and an inbox item.
func palHarness(t *testing.T) *wsHarness {
	t.Helper()
	h := newWSHarness(t)
	h.toGraph()
	h.m.showGraph = false
	h.m.now = func() time.Time { return inboxT0 }
	other := workspace.NewSession()
	other.Title = "Refactor review flow"
	other.AddLine("you", "refactor")
	if err := other.Save(h.root); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	h.recent = []RecentProject{{Path: proj, Name: "reviewer"}, {Path: filepath.Join(proj, "missing"), Name: "gone"}}
	return h
}

func (h *wsHarness) palette(query string) {
	h.t.Helper()
	h.send(ctrlK())
	if h.m.overlay != wsOverlayPalette {
		h.t.Fatalf("Ctrl+K did not open the palette: overlay=%v", h.m.overlay)
	}
	if query != "" {
		h.key(query)
	}
}

func TestPaletteOpensFromEveryFocusAndShowsGroups(t *testing.T) {
	h := palHarness(t)
	h.m.g.Node("T2").Title = "Review error messages"
	for _, f := range []wsFocus{wsFocusPrompt, wsFocusGraph, wsFocusSidebar} {
		h.m.setFocus(f)
		h.palette("")
		h.key("esc")
		if h.m.overlay != wsOverlayNone {
			t.Fatalf("Esc closes (focus %v)", f)
		}
	}
	h.palette("rev")
	v := h.view()
	for _, want := range []string{"Go to", "› rev", "Commands", "/review", "Nodes", "Review error messages", "Sessions", "Refactor review flow", "Projects", "reviewer"} {
		if !strings.Contains(v, want) {
			t.Errorf("palette lacks %q:\n%s", want, v)
		}
	}
	last, ok := -1, true
	for _, g := range []string{"Commands", "Nodes", "Sessions", "Projects"} {
		i := strings.Index(v, g)
		ok = ok && i > last
		last = i
	}
	if !ok {
		t.Errorf("group order:\n%s", v)
	}
	if strings.Contains(v, "(none match)") {
		t.Error("empty groups are hidden")
	}
	h.key("backspace", "backspace", "backspace")
	h.key("zzzq")
	h.want("No matches")
	h.send(ctrlK()) // toggles
	if h.m.overlay != wsOverlayNone {
		t.Error("Ctrl+K closes the palette")
	}
}

func TestPaletteEnterCommand(t *testing.T) {
	h := palHarness(t)
	h.m.setFocus(wsFocusGraph)
	h.palette("/status")
	h.key("enter") // no arguments: runs now
	if h.m.overlay != wsOverlayInfo {
		t.Errorf("/status opens its info: overlay=%v\n%s", h.m.overlay, h.view())
	}
	h.key("esc")

	for _, c := range []string{"/pr", "/merge", "/clear", "/compact", "/plugin", "/review", "/issue", "/export", "/quit"} {
		h.m.input.Reset()
		h.m.setFocus(wsFocusGraph)
		h.palette(c)
		h.key("enter") // outward-facing or needs an argument: into the prompt
		if h.m.overlay != wsOverlayNone || h.m.focus != wsFocusPrompt || h.m.input.Value() != c+" " {
			t.Errorf("%s: prompt = %q focus=%v overlay=%v", c, h.m.input.Value(), h.m.focus, h.m.overlay)
		}
	}
	h.palette("/issue")
	h.key("enter")
	if h.m.focus != wsFocusPrompt || h.m.input.Value() != "/issue " {
		t.Errorf("prompt = %q focus=%v", h.m.input.Value(), h.m.focus)
	}
	h.m.input.Reset()
	h.m.custom = nil
	h.m.deps.customCommands = func() []workspace.CustomCommand {
		return []workspace.CustomCommand{{Name: "deploy", Description: "ship it", Source: "project"}}
	}
	h.m.custom = h.m.deps.customCommands()
	h.palette("/deploy")
	h.key("enter") // a custom command always goes to the prompt
	if h.m.input.Value() != "/deploy " {
		t.Errorf("custom command: %q", h.m.input.Value())
	}
}

func TestPaletteEnterNodeAndInbox(t *testing.T) {
	h := palHarness(t)
	h.m.setFocus(wsFocusPrompt)
	h.palette("T3")
	h.key("enter")
	if h.m.sel != "T3" || h.m.focus != wsFocusGraph || h.m.overlay != wsOverlayNone {
		t.Errorf("node: sel=%q focus=%v", h.m.sel, h.m.focus)
	}
	h.m.g = inboxGraph()
	h.palette("")
	if rows := flattenPalette(h.m.palRows()); len(rows) == 0 || rows[0].kind != palInbox {
		t.Fatalf("needs-you comes first: %+v", rows)
	}
	h.key("down", "down", "enter") // T3 limit
	if h.m.sel != "T3" {
		t.Errorf("inbox row selects its node: %q", h.m.sel)
	}
	h.palette("T1 asks")
	h.key("enter")
	if h.m.sel != "T1" {
		t.Errorf("sel = %q", h.m.sel)
	}
}

func TestPaletteEnterSessionAndProject(t *testing.T) {
	h := palHarness(t)
	h.palette("refactor review")
	h.key("enter")
	if h.m.sess.snapshot().Title != "Refactor review flow" {
		t.Errorf("session not loaded: %q", h.m.sess.snapshot().Title)
	}
	h.palette("reviewer")
	groups := h.m.palRows()
	if len(groups[palProject]) != 1 {
		t.Fatalf("missing projects are not listed: %+v", groups[palProject])
	}
	h.m.palIdx = len(flattenPalette(groups)) - 1
	h.key("enter")
	if h.m.switchTo != h.recent[0].Path || !h.m.quitting {
		t.Errorf("switchTo = %q quitting=%v", h.m.switchTo, h.m.quitting)
	}
}

func TestPaletteSessionAndProjectRefuseWhileRunning(t *testing.T) {
	h := palHarness(t)
	_ = h.m.eng.Assign("T1", "coder")
	h.m.eng.RunAll(h.m.runCtx)
	h.pump(func(m workspaceModel) bool { return m.running() > 0 })
	h.m.now = func() time.Time { return inboxT0 }
	h.palette("refactor review")
	h.key("enter")
	if h.m.sess.snapshot().Title == "Refactor review flow" {
		t.Error("a running session is not left")
	}
	h.palette("reviewer")
	h.key("enter")
	if h.m.switchTo != "" || h.m.quitting {
		t.Error("no project switch while nodes run")
	}
	if !strings.Contains(h.view(), msgStopFirst) && !strings.Contains(h.flat(), msgStopFirst) {
		t.Logf("view: %s", h.view())
	}
	h.exec.release("T1", nil)
}

func TestPaletteDoesNotChangeSlashAndAtPalettes(t *testing.T) {
	h := newWSHarness(t)
	h.key("/")
	if !h.m.paletteOpen() || h.m.overlay != wsOverlayNone {
		t.Error("/ still opens the command list")
	}
	h.key("esc")
	h.toGraph()
	h.m.setFocus(wsFocusPrompt)
	h.key("@")
	if !h.m.mentionOpen() {
		t.Error("@ still opens the mention list")
	}
}

func TestPaletteRenderSample(t *testing.T) {
	h := palHarness(t)
	h.size(120, 34)
	h.m.g.Node("T2").Title = "Review error messages"
	h.m.sess.update(func(s *workspace.Session) {
		s.Reviews = []workspace.Review{{Round: 1, At: inboxT0.Add(-5 * time.Minute), State: workspace.ReviewPending, Applied: make([]workspace.AppliedProposal, 3)}}
	})
	h.palette("rev")
	t.Log("\n" + h.view())
}
