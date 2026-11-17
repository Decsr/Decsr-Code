package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func TestTokCompact(t *testing.T) {
	for in, want := range map[int]string{0: "0", 512: "512", 2100: "2.1k", 182000: "182k", 999_600: "1M", 1_200_000: "1.2M"} {
		if got := tokCompact(in); got != want {
			t.Errorf("tokCompact(%d) = %q, want %q", in, got, want)
		}
	}
}

func liveNode(status workspace.Status) *workspace.Node {
	n := feedTestNode("T1", status, feedT0)
	n.Model, n.Effort = "anthropic/opus-5-5", "medium"
	n.InputTokens, n.CachedTokens, n.OutputTokens = 182000, 164000, 2100
	return n
}

func TestRunningLineFormats(t *testing.T) {
	now := feedT0.Add(72 * time.Second)
	plain := func(n *workspace.Node, roleModel string) string {
		return ansi.Strip(runningLine(n, "claude", roleModel, "⠋", now))
	}
	n := liveNode(workspace.StatusRunning)
	want := "⠋ claude · opus-5-5 · effort medium · 1m 12s · ↑ 182k (164k cached) ↓ 2.1k"
	if got := plain(n, ""); got != want {
		t.Errorf("running:\n got %q\nwant %q", got, want)
	}

	n.Estimated = true
	if got := plain(n, ""); !strings.Contains(got, "↑ ~182k (~164k cached) ↓ ~2.1k") {
		t.Errorf("estimated: %q", got)
	}
	n.Estimated = false

	n.Effort, n.CachedTokens = "", 0
	got := plain(n, "")
	if strings.Contains(got, "effort") || strings.Contains(got, "cached") || !strings.Contains(got, "↑ 182k ↓ 2.1k") {
		t.Errorf("no effort, no cached: %q", got)
	}

	n.Model = ""
	if got := plain(n, "sonnet-4"); !strings.Contains(got, "claude · sonnet-4 ·") {
		t.Errorf("role model fallback: %q", got)
	}
	if got := plain(n, ""); !strings.Contains(got, "claude · (default) ·") {
		t.Errorf("default fallback: %q", got)
	}

	n = liveNode(workspace.StatusDone)
	n.EndedAt = feedT0.Add(5 * time.Minute)
	if got := plain(n, ""); !strings.HasPrefix(got, "✓ claude") || !strings.Contains(got, "· 5m ·") {
		t.Errorf("done: %q", got)
	}
	n.Status = workspace.StatusFailed
	if got := plain(n, ""); !strings.HasPrefix(got, "✗ claude") {
		t.Errorf("failed: %q", got)
	}
	n.InputTokens, n.OutputTokens = 0, 0
	if got := plain(n, ""); strings.Contains(got, "↑") {
		t.Errorf("no tokens yet: %q", got)
	}
}

func TestCardMetaShowsTotalTokens(t *testing.T) {
	n := liveNode(workspace.StatusRunning)
	n.ContextTokens = 18000
	if got := cardMeta(n); got != " · ctx 18k · 184k tok" {
		t.Errorf("cardMeta = %q", got)
	}
	n.Estimated = true
	if got := cardMeta(n); !strings.HasSuffix(got, "~184k tok") {
		t.Errorf("estimated cardMeta = %q", got)
	}
	n.InputTokens, n.OutputTokens = 0, 0
	if got := cardMeta(n); got != " · ctx 18k" {
		t.Errorf("no tokens: %q", got)
	}
	o := cardOpts{g: &workspace.Graph{Nodes: []*workspace.Node{liveNode(workspace.StatusRunning)}}, n: liveNode(workspace.StatusRunning), width: 70, now: feedT0.Add(72 * time.Second)}
	if got := actCardText(o); !strings.Contains(got, "Working for 1m 12s · 184k tok") {
		t.Errorf("card:\n%s", got)
	}
}

func liveHarness(t *testing.T, status workspace.Status, w, h int) *wsHarness {
	t.Helper()
	hh := newWSHarness(t)
	hh.toGraph()
	n := hh.m.g.Node("T1")
	ln := liveNode(status)
	n.Role = "coder"
	n.Status, n.Model, n.Effort, n.StartedAt = ln.Status, ln.Model, ln.Effort, ln.StartedAt
	n.InputTokens, n.CachedTokens, n.OutputTokens = ln.InputTokens, ln.CachedTokens, ln.OutputTokens
	n.Activity = []runner.Activity{
		mkAct("a0", runner.ActRead, runner.ActDone, "main.go"),
		func() runner.Activity {
			a := mkAct("a1", runner.ActEdit, runner.ActDone, "internal/x/util.go")
			a.Added, a.Removed = 12, 3
			return a
		}(),
		mkAct("a2", runner.ActCommand, runner.ActRunning, "go test ./..."),
	}
	hh.m.sel = n.ID
	hh.m.showGraph = true
	hh.m.setFocus(wsFocusGraph)
	hh.m.now = func() time.Time { return feedT0.Add(72 * time.Second) }
	hh.size(w, h)
	return hh
}

func TestLivePaneVisibility(t *testing.T) {
	h := liveHarness(t, workspace.StatusRunning, 160, 45)
	h.want("Live · T1")
	h.want("opencode · opus-5-5")
	h.want("go test ./...")
	h.want("internal/x/util.go") // the files column at 160 columns
	h.want("+12")

	h.key("v")
	h.wantNot("Live · T1")
	h.key("v")
	h.want("Live · T1")

	// the choice is kept when the selection leaves the running node and returns
	h.m.liveHide = true
	h.m.sel = "T1"
	h.wantNot("Live · T1")
	h.m.liveHide = false

	h.size(160, 29)
	h.wantNot("Live · T1")
	h.size(160, 30)
	if h.m.liveHeight(h.m.mainHeight()) == 0 {
		t.Errorf("30 rows: the live pane should show")
	}

	h.m.g.Node("T1").Status = workspace.StatusDone
	h.size(160, 45)
	h.wantNot("Live · T1")
}

func TestLivePaneNarrowShowsFileCount(t *testing.T) {
	h := liveHarness(t, workspace.StatusRunning, 90, 40)
	h.want("Live · T1")
	h.want("1 file changed")
}

func TestLivePaneFitsRows(t *testing.T) {
	for _, sz := range [][2]int{{160, 45}, {100, 30}, {80, 30}} {
		h := liveHarness(t, workspace.StatusRunning, sz[0], sz[1])
		lines := strings.Split(h.m.View(), "\n")
		if len(lines) > sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%v: line %d is %d wide", sz, i, ansi.StringWidth(l))
			}
		}
	}
}

func diffOf(paths ...string) workspace.Diff {
	var d workspace.Diff
	for _, p := range paths {
		d.Files = append(d.Files, workspace.DiffFile{Path: p, Added: 1, Hunks: []workspace.Hunk{{Header: "@@ -1 +1 @@", Lines: []workspace.DiffLine{
			{Kind: '+', NewNo: 1, Text: "package " + strings.TrimSuffix(p, ".go")},
			{Kind: '+', NewNo: 2, Text: "var x = 1"},
			{Kind: '+', NewNo: 3, Text: "var y = 2"},
		}}}})
	}
	return d
}

func TestOutDiffRefreshKeepsFile(t *testing.T) {
	old := mergeOutDiff(nil, "T1", diffOf("a.go", "b.go"), nil)
	old.setFile(1)
	old.cur, old.top = 2, 1
	old.hx = 8

	next := mergeOutDiff(old, "T1", diffOf("a.go", "b.go", "c.go"), nil)
	if f := next.curFile(); f == nil || f.Path != "b.go" || next.cur != 2 || next.top != 1 || next.hx != 8 {
		t.Errorf("kept file/scroll: file %v cur %d top %d hx %d", next.curFile(), next.cur, next.top, next.hx)
	}
	// a new file placed before the selected one moves the index, not the file
	next = mergeOutDiff(old, "T1", diffOf("0.go", "a.go", "b.go"), nil)
	if f := next.curFile(); f == nil || f.Path != "b.go" {
		t.Errorf("file after insert: %v", next.curFile())
	}
	// the selected file disappears: back to the first
	next = mergeOutDiff(old, "T1", diffOf("a.go"), nil)
	if next.file != 0 || next.cur != 0 {
		t.Errorf("gone file: file %d cur %d", next.file, next.cur)
	}
	// a failed reload keeps what is shown
	if kept := mergeOutDiff(old, "T1", workspace.Diff{}, fmt.Errorf("git busy")); kept != old {
		t.Errorf("a failed reload replaced the view")
	}
}

func outScreen(t *testing.T, w, hgt int) *wsHarness {
	h := liveHarness(t, workspace.StatusRunning, w, hgt)
	h.m = h.m.openOutput(h.m.g.Node("T1"))
	next, _ := h.m.onOutDiff(wsOutDiffMsg{node: "T1", d: diffOf("a.go", "b.go")})
	h.m = next
	h.m.refreshOutput()
	return h
}

func TestOutputScreenTwoPanes(t *testing.T) {
	h := outScreen(t, 160, 40)
	v := h.view()
	for _, want := range []string{"opencode · opus-5-5", "Timeline", "Changes · 2 files", "go test ./...", "a.go", "package a"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	for i, l := range strings.Split(h.m.View(), "\n") {
		if ansi.StringWidth(l) > 160 {
			t.Errorf("line %d is %d wide", i, ansi.StringWidth(l))
		}
	}
	if h.m.outPane != 0 {
		t.Fatalf("focus starts on the timeline")
	}
	h.key("tab")
	if h.m.outPane != 1 {
		t.Fatalf("Tab moves the focus to the changes")
	}
	h.key("n")
	if f := h.m.outDiff.curFile(); f == nil || f.Path != "b.go" {
		t.Errorf("n picks the next file: %v", f)
	}
	h.key("tab")
	if h.m.outPane != 0 {
		t.Errorf("Tab moves the focus back")
	}
	h.key("t")
	if !h.m.outRaw {
		t.Errorf("t shows the raw output")
	}
	h.wantNot("Changes")
}

func TestOutputScreenOnePaneAt100(t *testing.T) {
	h := outScreen(t, 100, 30)
	v := h.view()
	if !strings.Contains(v, "Timeline") || strings.Contains(v, "Changes") {
		t.Errorf("only the timeline shows first:\n%s", v)
	}
	h.key("tab")
	v = h.view()
	if strings.Contains(v, "Timeline") || !strings.Contains(v, "Changes · 2 files") || !strings.Contains(v, "package a") {
		t.Errorf("Tab shows only the changes:\n%s", v)
	}
	h.key("tab")
	h.want("Timeline")
}

func TestOutputScreenFinishedNodeIsFrozen(t *testing.T) {
	h := liveHarness(t, workspace.StatusDone, 160, 40)
	n := h.m.g.Node("T1")
	n.EndedAt = feedT0.Add(5 * time.Minute)
	h.m.now = func() time.Time { return feedT0.Add(time.Hour) }
	h.m = h.m.openOutput(n)
	h.want("✓ opencode")
	h.want("· 5m ·")
	if _, cmd := h.m.outDiffTick(); cmd != nil {
		t.Errorf("a finished node is not refreshed on a tick")
	}
}

func TestOutputScreenReloadsOnEdit(t *testing.T) {
	h := outScreen(t, 160, 40)
	if h.m.outDiffBusy {
		t.Fatalf("no request in flight yet")
	}
	h.m.now = func() time.Time { return feedT0.Add(80 * time.Second) }
	m, cmd := h.m.startOutDiff()
	if cmd == nil || !m.outDiffBusy {
		t.Fatalf("a running node starts a reload")
	}
	if m2, cmd2 := m.startOutDiff(); cmd2 != nil || !m2.outDiffDirty {
		t.Errorf("a second request waits for the first")
	}
	if _, cmd := h.m.outDiffTick(); cmd == nil {
		t.Errorf("the fallback reloads a stale diff")
	}
}
