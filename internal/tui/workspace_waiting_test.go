package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const longQuestion = "Should the storage layer use PostgreSQL with a connection pool or the embedded SQLite file, and which migration tool do you want me to wire in for the schema changes?"

func waitingNode(kind string) *workspace.Node {
	n := feedTestNode("T3", workspace.StatusWaiting, feedT0)
	n.EndedAt = feedT0.Add(2 * time.Minute)
	n.Summary = "wrote the models"
	n.Files = []string{"models.go"}
	n.Report = &workspace.Report{Status: kind, Summary: "stuck on the database", Question: longQuestion}
	return n
}

func waitingGraph(n *workspace.Node) *workspace.Graph {
	return &workspace.Graph{Nodes: []*workspace.Node{n}}
}

func TestWaitingGlyphAndColour(t *testing.T) {
	n := waitingNode(workspace.ReportQuestion)
	g := waitingGraph(n)
	if glyph, label := nodeStatusGlyph(g, n); glyph != "◇" || label != "waiting" {
		t.Errorf("glyph = %q %q", glyph, label)
	}
	if statusLabelColor(g, n) != colWarn {
		t.Error("waiting uses the warning colour")
	}
}

func TestWaitingCardQuestionAndBlocked(t *testing.T) {
	for _, w := range []int{80, 120} {
		n := waitingNode(workspace.ReportQuestion)
		lines := renderNodeCard(cardOpts{g: waitingGraph(n), n: n, width: w - 4, now: feedT0.Add(time.Hour)})
		out := actPlain(strings.Join(lines, "\n"))
		for _, want := range []string{"◇ T3", "◇ waiting · Worked for 2m", "? Should the storage layer", "m reply"} {
			if !strings.Contains(out, want) {
				t.Errorf("w=%d card lacks %q:\n%s", w, want, out)
			}
		}
		if strings.Contains(out, "wrote the models") {
			t.Errorf("the summary does not replace the question:\n%s", out)
		}
		q := 0
		for _, l := range lines {
			if ansi.StringWidth(l) > w-4 {
				t.Errorf("w=%d line of %d cells", w, ansi.StringWidth(l))
			}
			if s := actPlain(l); strings.Contains(s, "?") || q > 0 && !strings.Contains(s, "m reply") && !strings.Contains(s, "╰") && !strings.Contains(s, "models.go") && !strings.Contains(s, "◇") {
				q++
			}
		}
		if q > cardWaitingLines {
			t.Errorf("w=%d question takes %d rows, max %d", w, q, cardWaitingLines)
		}

		b := waitingNode(workspace.ReportBlocked)
		out = actPlain(strings.Join(renderNodeCard(cardOpts{g: waitingGraph(b), n: b, width: w - 4, now: feedT0.Add(time.Hour)}), "\n"))
		if !strings.Contains(out, "blocked: stuck on the database") || !strings.Contains(out, "m reply") {
			t.Errorf("w=%d blocked card:\n%s", w, out)
		}
	}
}

func TestWaitingSidebarRows(t *testing.T) {
	h := feedHarness(t)
	g := h.m.g
	g.Node("T1").Status, g.Node("T1").Role = workspace.StatusDone, "coder"
	g.Node("T2").Status, g.Node("T2").Role = workspace.StatusWaiting, "coder"
	g.Node("T2").Report = &workspace.Report{Status: "question", Question: "q?"}
	g.Node("T3").Status, g.Node("T3").Role = workspace.StatusWaiting, "qa"
	g.Node("T3").Report = &workspace.Report{Status: "blocked", Summary: "s"}
	for _, w := range []int{28, 34} {
		rows := h.m.sidebarRows(w, 30)
		plain := ansi.Strip(strings.Join(rows, "\n"))
		for _, want := range []string{"1/6 · 2 waiting", "◇ T2", "asks a question", "◇ T3", "blocked"} {
			if !strings.Contains(plain, want) {
				t.Errorf("w=%d sidebar lacks %q:\n%s", w, want, plain)
			}
		}
		for i, l := range rows {
			if ansi.StringWidth(l) > w {
				t.Errorf("w=%d row %d is %d cells", w, i, ansi.StringWidth(l))
			}
		}
	}
	if got := h.m.progressStats(); !strings.Contains(got, "1/6 done · 2 waiting") {
		t.Errorf("progress = %q", got)
	}
}

func TestWaitingInspector(t *testing.T) {
	for _, w := range []int{30, 38} {
		n := waitingNode(workspace.ReportQuestion)
		out := renderInspector(waitingGraph(n), n, config.DefaultConfig(), plugins.Set{}, w, 40, nil)
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "◇ waiting") || !strings.Contains(plain, "Report  question") || !strings.Contains(plain, "Should the storage") {
			t.Errorf("w=%d inspector:\n%s", w, plain)
		}
		for _, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("w=%d line of %d cells", w, ansi.StringWidth(l))
			}
		}
	}
	d := waitingNode(workspace.ReportDone)
	d.Status = workspace.StatusDone
	d.Report.Summary = "all of it works"
	plain := ansi.Strip(renderInspector(waitingGraph(d), d, config.DefaultConfig(), plugins.Set{}, 38, 40, nil))
	if !strings.Contains(plain, "Report  done") || !strings.Contains(plain, "all of it works") {
		t.Errorf("done inspector:\n%s", plain)
	}
}

func TestWaitingGraphCardAndViews(t *testing.T) {
	for _, w := range []int{80, 120} {
		h := feedHarness(t)
		h.size(w, 40)
		g := h.m.g
		g.Node("T1").Status, g.Node("T1").Role = workspace.StatusWaiting, "coder"
		g.Node("T1").StartedAt, g.Node("T1").EndedAt = feedT0, feedT0.Add(time.Minute)
		g.Node("T1").Report = &workspace.Report{Status: "question", Question: longQuestion}
		h.m.sel = "T1"
		for _, graph := range []bool{false, true} {
			h.m.showGraph = graph
			view := h.m.View()
			for i, l := range strings.Split(view, "\n") {
				if ansi.StringWidth(l) > w {
					t.Fatalf("w=%d graph=%v line %d is %d cells", w, graph, i, ansi.StringWidth(l))
				}
			}
			plain := ansi.Strip(view)
			if !strings.Contains(plain, "◇") || !strings.Contains(plain, "waiting") {
				t.Errorf("w=%d graph=%v view lacks the waiting status:\n%s", w, graph, plain)
			}
			if !graph && !strings.Contains(plain, "m reply") {
				t.Errorf("w=%d feed lacks the reply hint:\n%s", w, plain)
			}
		}
	}
}

func TestWaitingKeyTablesMentionReply(t *testing.T) {
	var all strings.Builder
	for _, sec := range workspaceHelp(nil) {
		for _, k := range sec.keys {
			all.WriteString(k.key + " " + k.label + "\n")
		}
	}
	if !strings.Contains(all.String(), "waiting node") || !strings.Contains(all.String(), "reply") {
		t.Error("the workspace help does not mention replying to waiting nodes")
	}
}

func TestNotify_NodeAsksAndBlocked(t *testing.T) {
	for _, tc := range []struct {
		rep  workspace.Report
		want string
	}{
		{workspace.Report{Status: "question", Summary: "s", Question: "Which db?\nSecond line"}, "T1 asks: Which db?"},
		{workspace.Report{Status: "blocked", Summary: "no token\nmore"}, "T1 is blocked: no token"},
	} {
		h, log := newNotifyHarness(t)
		h.graph = oneWorkGraph
		h.toGraph()
		h.exec.reports = map[string]workspace.Report{"T1": tc.rep}
		if err := h.m.eng.Assign("T1", "coder"); err != nil {
			t.Fatal(err)
		}
		h.m.eng.RunAll(h.m.runCtx)
		h.exec.release("T1", nil)
		pumpNote(h, log, "Run finished")
		n, ok := log.find("T1 ")
		if !ok || n.Body != tc.want || n.Level != notify.NeedsYou {
			t.Fatalf("note = %+v, want %q (%v)", n, tc.want, log.bodies())
		}
		if f, _ := log.find("Run finished"); f.Body != "Run finished: 0 done, 0 failed, 1 waiting" {
			t.Errorf("run-finished = %q", f.Body)
		}
		if h.m.g.Node("T1").Status != workspace.StatusWaiting {
			t.Errorf("status = %s", h.m.g.Node("T1").Status)
		}
	}
}

func TestAutonomyLeavesWaitingNodeAlone(t *testing.T) {
	h, _ := autoHarness(t, 3)
	h.graph = wsSmallGraph
	h.toGraph()
	h.exec.reports = map[string]workspace.Report{"T1": {Status: "question", Question: "q?"}}
	_ = h.m.eng.Assign("T1", "coder")
	_ = h.m.eng.Assign("T2", "qa")
	h.m.eng.RunAll(h.m.runCtx)
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusWaiting })
	time.Sleep(100 * time.Millisecond)
	h.pump(func(m workspaceModel) bool { return true })
	g := h.m.eng.Snapshot()
	if g.Node("T1").Status != workspace.StatusWaiting || g.Node("T2").Status != workspace.StatusTodo || g.Node("T1").Attempt > 1 {
		t.Errorf("T1=%s T2=%s attempt=%d", g.Node("T1").Status, g.Node("T2").Status, g.Node("T1").Attempt)
	}
	if got := fmt.Sprint(h.m.suggestionText()); strings.Contains(got, "review") {
		t.Errorf("no review suggestion while a node waits: %q", got)
	}
}

func TestEffectiveModelShowsClaudeDefault(t *testing.T) {
	c := config.DefaultConfig()
	if got := effectiveModel(c, config.AgentConfig{Runner: "claude"}); got != "claude-opus-5-5" {
		t.Errorf("claude without a model shows %q", got)
	}
	if got := effectiveModel(c, config.AgentConfig{Runner: "claude", Model: "sonnet"}); got != "sonnet" {
		t.Errorf("own model = %q", got)
	}
	if got := effectiveModel(c, config.AgentConfig{Runner: "codex"}); got != "" {
		t.Errorf("other runners keep their default: %q", got)
	}
}
