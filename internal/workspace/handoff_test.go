package workspace

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

func failedCmd(id, title, detail string, code int) runner.Activity {
	return runner.Activity{ID: id, Kind: runner.ActCommand, State: runner.ActFailed, Title: title, Detail: detail, ExitCode: &code}
}

func TestFixExcerptPriorityAndExclusions(t *testing.T) {
	n := &Node{
		ID: "CHECK", Kind: KindCheck, Status: StatusFailed, Error: "node error text",
		Files: []string{"a.go"}, Observed: []string{"b.go"}, FileStats: []FileStat{{Path: "c.go"}},
		Activity: []runner.Activity{
			{ID: "t", Kind: runner.ActThinking, State: runner.ActDone, Title: "Thinking", Detail: "SECRET-THOUGHT"},
			{ID: "m", Kind: runner.ActMessage, State: runner.ActFailed, Title: "chat", Detail: "SECRET-MESSAGE"},
			{ID: "e", Kind: runner.ActError, State: runner.ActFailed, Title: "rate limited", Detail: "retry later"},
			failedCmd("c", "go test ./...", "FAIL pkg 0.1s", 1),
			{ID: "ok", Kind: runner.ActCommand, State: runner.ActDone, Title: "go build", Detail: "fine"},
		},
	}
	got := FixExcerpt([]*Node{n}, 8000)
	for _, bad := range []string{"SECRET", "go build", "node error text"} {
		if strings.Contains(got, bad) {
			t.Errorf("excerpt contains %q:\n%s", bad, got)
		}
	}
	ic, ie, ifl := strings.Index(got, "go test"), strings.Index(got, "rate limited"), strings.Index(got, "Files touched: a.go, b.go, c.go")
	if ic < 0 || (ic >= ie || ie >= ifl) {
		t.Errorf("order wrong (cmd %d, error %d, files %d):\n%s", ic, ie, ifl, got)
	}
	if !strings.Contains(got, "(exit 1)") || !strings.Contains(got, "FAIL pkg") {
		t.Errorf("missing command detail:\n%s", got)
	}
}

func TestFixExcerptNodeErrorWithoutCommand(t *testing.T) {
	got := FixExcerpt([]*Node{{ID: "T1", Error: "boom"}}, 1000)
	if !strings.Contains(got, "[T1] boom") {
		t.Errorf("got %q", got)
	}
	if FixExcerpt(nil, 1000) != "" || FixExcerpt([]*Node{{ID: "T1"}}, 1000) != "" {
		t.Error("nothing failed should give an empty excerpt")
	}
}

func TestFixExcerptPrefersFullDetail(t *testing.T) {
	n := &Node{ID: "CHECK", Activity: []runner.Activity{failedCmd("c", "make", "short", 2)}}
	got := FixExcerptWith([]*Node{n}, 8000, func(node, act string) (string, bool) {
		if node == "CHECK" && act == "c" {
			return "the full output", true
		}
		return "", false
	})
	if !strings.Contains(got, "the full output") || strings.Contains(got, "short") {
		t.Errorf("got %q", got)
	}
}

func TestFixExcerptMultipleNodes(t *testing.T) {
	a := &Node{ID: "T1", Activity: []runner.Activity{failedCmd("c", "lint", "lint failed", 1)}, Files: []string{"x.go"}}
	b := &Node{ID: "CHECK", Activity: []runner.Activity{failedCmd("c", "go test", "test failed", 1)}, Files: []string{"x.go", "y.go"}}
	got := FixExcerpt([]*Node{a, b}, 8000)
	if !strings.Contains(got, "[T1] $ lint") || !strings.Contains(got, "[CHECK] $ go test") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(got, "Files touched: x.go, y.go") {
		t.Errorf("files not merged: %q", got)
	}
}

func TestFixExcerptBudgetAndMarker(t *testing.T) {
	long := strings.Repeat("line of output é\n", 2000)
	for _, max := range []int{1, 50, 150, 300, 1000, 8000} {
		n := &Node{ID: "CHECK", Error: long, Files: []string{strings.Repeat("f", 500)}, Activity: []runner.Activity{
			failedCmd("a", "cmd one", long, 1), failedCmd("b", "cmd two", long, 1),
			{ID: "e", Kind: runner.ActError, State: runner.ActFailed, Title: "err", Detail: long},
		}}
		got := FixExcerpt([]*Node{n}, max)
		if c := utf8.RuneCountInString(got); c > max {
			t.Errorf("max %d: %d characters", max, c)
		}
		if !utf8.ValidString(got) {
			t.Errorf("max %d: invalid UTF-8", max)
		}
		if max >= 1000 && !strings.Contains(got, "characters omitted]") {
			t.Errorf("max %d: no omission marker", max)
		}
	}
	// Nothing cut: no marker.
	small := FixExcerpt([]*Node{{ID: "T1", Error: "x"}}, 8000)
	if strings.Contains(small, "omitted") {
		t.Errorf("marker without cut: %q", small)
	}
}

func TestFixExcerptKeepsTailOfCutCommand(t *testing.T) {
	out := strings.Repeat("noise\n", 3000) + "THE REAL ERROR"
	got := FixExcerpt([]*Node{{ID: "CHECK", Activity: []runner.Activity{failedCmd("a", "go test", out, 1)}}}, 2000)
	if !strings.HasSuffix(got, "THE REAL ERROR") && !strings.Contains(got, "THE REAL ERROR") {
		t.Errorf("tail lost: %q", got[len(got)-80:])
	}
}

func TestAddFixDescriptionHasExcerpt(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusDone},
		{ID: "CHECK", Title: "Build & test", Kind: KindCheck, Status: StatusFailed, DependsOn: []string{"T1"}, Error: "exit 1",
			Activity: []runner.Activity{failedCmd("c", "go test ./...", "FAIL pkg/x", 1)}},
	}}
	f, err := g.AddFix("CHECK", "exit 1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.Description, "Failure output:") || !strings.Contains(f.Description, "What failed:\n[CHECK] $ go test ./... (exit 1)\nFAIL pkg/x") {
		t.Errorf("description = %q", f.Description)
	}
	if strings.Count(f.Description, "FAIL pkg/x") != 1 {
		t.Errorf("tail repeated: %q", f.Description)
	}
}

func TestAddFixWithoutExcerptKeepsFailureOutput(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusDone},
		{ID: "CHECK", Title: "Build & test", Kind: KindCheck, Status: StatusFailed, DependsOn: []string{"T1"}},
	}}
	f, err := g.AddFixExcerpt("CHECK", "boom", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Description, "Failure output:\nboom") || strings.Contains(f.Description, "What failed:") {
		t.Errorf("description = %q", f.Description)
	}
}

func TestEngineAddFixUsesFullDetail(t *testing.T) {
	full := strings.Repeat("x", 1500) + "FULLTAIL"
	g := &Graph{Nodes: []*Node{work("T1"), {ID: "CHECK", Title: "Build & test", Kind: KindCheck, Status: StatusTodo, DependsOn: []string{"T1"}}}}
	ex := scriptExec{func(n *Node, emit func(Event)) {
		if n.ID == "CHECK" {
			a := failedCmd("c", "go test", full, 1)
			emit(actEv(a))
		}
	}}
	e := NewEngine(g, failingCheckExec{ex}, 1, "")
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventDone, "T1")
	if err := e.Run(context.Background(), "CHECK"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, EventFailed, "CHECK")
	id, err := e.AddFix("CHECK")
	if err != nil {
		t.Fatal(err)
	}
	d := e.Snapshot().Node(id).Description
	if !strings.Contains(d, "What failed:") || !strings.Contains(d, "FULLTAIL") || !strings.Contains(d, full) {
		t.Errorf("description lacks the full detail: %.200q", d)
	}
}

// failingCheckExec fails the CHECK node after running the script.
type failingCheckExec struct{ s scriptExec }

func (f failingCheckExec) Run(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error) {
	r, err := f.s.Run(ctx, g, n, emit)
	if n.ID == "CHECK" {
		return r, errCheck
	}
	return r, err
}

var errCheck = &checkErr{}

type checkErr struct{}

func (*checkErr) Error() string { return "go test failed" }

func TestEngineAddCIFixAppendsExcerptWithinBudget(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Kind: KindWork, Status: StatusFailed, Error: "worker broke"}}}
	e := NewEngine(g, newFakeExec(), 1, "")
	id := e.AddCIFix("CI", "ci log", "coder")
	d := e.Snapshot().Node(id).Description
	if !strings.HasPrefix(d, "ci log\n\nWhat failed:\n[T1] worker broke") {
		t.Errorf("description = %q", d)
	}
	big := strings.Repeat("x", FixExcerptChars-100)
	id = e.AddCIFix("CI2", big, "coder")
	if d := e.Snapshot().Node(id).Description; strings.Contains(d, "What failed") || len(d) > FixExcerptChars {
		t.Errorf("no budget left but description has %d chars", len(d))
	}
}

func TestFixExcerptKeepsErrorLinesOfCutPart(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		switch i {
		case 40:
			sb.WriteString("FAIL: TestEarly\n")
		case 150:
			sb.WriteString("panic: late problem\n")
		default:
			fmt.Fprintf(&sb, "ok line %d\n", i)
		}
	}
	sb.WriteString("TAIL END")
	n := &Node{ID: "CHECK", Activity: []runner.Activity{failedCmd("a", "go test", sb.String(), 1)}}
	got := FixExcerpt([]*Node{n}, 1500)
	for _, want := range []string{"FAIL: TestEarly", "panic: late problem", "ok line 38\n", "ok line 42", "ok line 148", "ok line 152", "TAIL END", "characters omitted]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ok line 37\n") || strings.Contains(got, "ok line 43\n") {
		t.Errorf("context is wider than two lines:\n%s", got)
	}
	if strings.Index(got, "FAIL: TestEarly") > strings.Index(got, "panic: late problem") || strings.Index(got, "panic: late problem") > strings.Index(got, "TAIL END") {
		t.Errorf("pieces are out of order:\n%s", got)
	}
	if c := utf8.RuneCountInString(got); c > 1500 {
		t.Errorf("%d characters, budget 1500", c)
	}
}

func TestCutKeepingErrorsMarkersAreAccurate(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		if i == 20 || i == 22 {
			sb.WriteString("an Exception here\n")
			continue
		}
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	text := []rune(sb.String())
	got := cutKeepingErrors(text, 600)
	// The two matches are two lines apart: one merged window, so two markers.
	if c := strings.Count(got, "characters omitted]"); c != 2 {
		t.Fatalf("%d markers, want 2:\n%s", c, got)
	}
	// Markers plus kept text account for the whole input.
	omitted := 0
	kept := 0
	for _, l := range strings.Split(got, "\n") {
		var n int
		if _, err := fmt.Sscanf(l, "[… %d characters omitted]", &n); err == nil {
			omitted += n
			continue
		}
		kept += utf8.RuneCountInString(l) + 1
	}
	if omitted+kept-1 != len(text) {
		t.Errorf("omitted %d + kept %d does not add up to %d", omitted, kept-1, len(text))
	}
}

func TestCutKeepingErrorsWithoutMatchesKeepsOnlyTail(t *testing.T) {
	text := []rune(strings.Repeat("quiet line\n", 100))
	got := cutKeepingErrors(text, 200)
	if c := strings.Count(got, "characters omitted]"); c != 1 || !strings.HasPrefix(got, "[… 900 characters omitted]\n") {
		t.Errorf("got %q", got)
	}
}
