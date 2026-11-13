package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func inspectorGraph() (*workspace.Graph, *workspace.Node) {
	g := wsFixtureGraph()
	g.Nodes[0].Status = workspace.StatusDone
	g.Nodes[0].Role = "coder"
	n := g.Node("T3")
	n.Role = "qa"
	n.Status = workspace.StatusFailed
	n.Error = "go test failed: TestStorage expected 3 got 2"
	n.Files = []string{"store_test.go"}
	n.InputTokens, n.OutputTokens = 1200, 800
	n.StartedAt = time.Now().Add(-65 * time.Second)
	n.EndedAt = time.Now()
	return g, n
}

func TestInspectorShowsDepsAndError(t *testing.T) {
	g, n := inspectorGraph()
	out := ansi.Strip(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, 36, 30, []string{"tail one", "tail two"}))
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"T3 · Storage tests", "failed", "ROLE", "qa", "DEPENDS ON", "✓ T1 Data model",
		"DESCRIPTION", "FILES", "store_test.go", "ERROR", "TestStorage expected 3 got 2", "TOKENS", "in 1.2k", "OUTPUT", "tail two"} {
		if !strings.Contains(flat, want) {
			t.Errorf("inspector lacks %q:\n%s", want, out)
		}
	}
}

func TestInspectorShowsBlockers(t *testing.T) {
	g := wsFixtureGraph()
	g.Node("T4").Role = "coder"
	out := ansi.Strip(renderInspector(g, g.Node("T4"), config.DefaultConfig(), plugins.Set{}, 36, 20, nil))
	if !strings.Contains(out, "blocked by T2, T3") {
		t.Fatalf("no blockers:\n%s", out)
	}
}

func TestInspectorSuggestedUndefinedRole(t *testing.T) {
	g := wsFixtureGraph()
	out := ansi.Strip(renderInspector(g, g.Node("T2"), config.DefaultConfig(), plugins.Set{}, 46, 20, nil))
	if !strings.Contains(out, "suggested: docs_writer (not defined yet)") {
		t.Fatalf("missing suggestion:\n%s", out)
	}
}

func TestInspectorFitsEveryBox(t *testing.T) {
	g, n := inspectorGraph()
	n.Description = strings.Repeat("a long description ", 40)
	tail := make([]string, 50)
	for i := range tail {
		tail[i] = strings.Repeat("output ", 10)
	}
	for _, sz := range [][2]int{{8, 1}, {20, 3}, {36, 8}, {36, 12}, {36, 30}, {80, 40}} {
		out := renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, sz[0], sz[1], tail)
		assertFits(t, "inspector", out, sz[0], sz[1])
	}
	if out := renderInspector(g, nil, config.DefaultConfig(), plugins.Set{}, 30, 5, nil); !strings.Contains(ansi.Strip(out), "No node selected") {
		t.Fatalf("nil node: %q", out)
	}
}

func TestPickItemsOrder(t *testing.T) {
	g := wsFixtureGraph()
	cfg := config.DefaultConfig()
	items := buildPickItems(g, g.Node("T3"), cfg)
	if items[0].name != "qa" || !items[0].suggested {
		t.Fatalf("suggested role must be first: %+v", items[0])
	}
	kinds := []string{}
	for _, it := range items {
		kinds = append(kinds, it.kind)
	}
	joined := strings.Join(kinds, ",")
	if !strings.HasSuffix(joined, "create,new") {
		t.Fatalf("order = %s", joined)
	}
	g.Node("T3").Role = "qa"
	items = buildPickItems(g, g.Node("T3"), cfg)
	if items[len(items)-1].kind != "unassign" {
		t.Fatal("assigned node must offer unassign")
	}
	cfg.Agents["docs_writer"] = config.AgentConfig{Description: "x"}
	for _, it := range buildPickItems(g, g.Node("T2"), cfg) {
		if it.kind == "create" {
			t.Fatal("a defined role must not be offered for creation")
		}
	}
}

func inspectorLongNode() (*workspace.Graph, *workspace.Node, []string) {
	g := wsFixtureGraph()
	n := g.Node("T1")
	n.Role, n.Status = "coder", workspace.StatusDone
	n.Description = strings.Repeat("describe the whole job in great detail. ", 40)
	n.Summary = strings.Repeat("summary sentence that goes on. ", 40)
	for i := 0; i < 8; i++ {
		n.Files = append(n.Files, fmt.Sprintf("src/file%d.go", i))
	}
	n.InputTokens, n.OutputTokens = 100, 50
	n.StartedAt, n.EndedAt = time.Now().Add(-time.Minute), time.Now()
	tail := []string{"o1", "o2", "o3", "o4", "o5", "o6", "o7", "o8"}
	return g, n, tail
}

// sectionRows returns the number of rows under each upper-case header.
func sectionRows(out string) map[string]int {
	rows := map[string]int{}
	cur := ""
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		switch t {
		case "ROLE", "DEPENDS ON", "DESCRIPTION", "FILES", "WARNINGS", "ERROR", "SUMMARY", "TOKENS", "OUTPUT":
			cur = t
			rows[cur] = 0
			continue
		}
		if cur != "" && t != "" {
			rows[cur]++
		}
	}
	return rows
}

func TestInspectorLayoutByHeight(t *testing.T) {
	g, n, tail := inspectorLongNode()
	cfg := config.DefaultConfig()

	out14 := ansi.Strip(renderInspector(g, n, cfg, plugins.Set{}, 40, 14, tail))
	r := sectionRows(out14)
	assertFits(t, "h14", out14, 40, 14)
	if r["DESCRIPTION"] < 3 {
		t.Errorf("h14 description rows = %d:\n%s", r["DESCRIPTION"], out14)
	}
	for _, gone := range []string{"TOKENS", "OUTPUT", "FILES"} {
		if _, ok := r[gone]; ok {
			t.Errorf("h14 should drop %s:\n%s", gone, out14)
		}
	}

	out22 := ansi.Strip(renderInspector(g, n, cfg, plugins.Set{}, 40, 22, tail))
	r = sectionRows(out22)
	assertFits(t, "h22", out22, 40, 22)
	if r["DESCRIPTION"] < 4 {
		t.Errorf("h22 description rows = %d:\n%s", r["DESCRIPTION"], out22)
	}
	if _, ok := r["TOKENS"]; ok {
		t.Errorf("h22 should drop TOKENS first:\n%s", out22)
	}

	out32 := ansi.Strip(renderInspector(g, n, cfg, plugins.Set{}, 40, 32, tail))
	r = sectionRows(out32)
	assertFits(t, "h32", out32, 40, 32)
	for _, sec := range []string{"DESCRIPTION", "FILES", "SUMMARY", "OUTPUT"} {
		if _, ok := r[sec]; !ok {
			t.Errorf("h32 lacks %s:\n%s", sec, out32)
		}
	}
	if _, ok := r["TOKENS"]; ok {
		t.Errorf("h32 keeps TOKENS although the description would drop below 4 rows:\n%s", out32)
	}
	out40 := ansi.Strip(renderInspector(g, n, cfg, plugins.Set{}, 40, 40, tail))
	if _, ok := sectionRows(out40)["TOKENS"]; !ok {
		t.Errorf("h40 lacks TOKENS:\n%s", out40)
	}
	if r["FILES"] != 5 || !strings.Contains(out32, "+4 more") {
		t.Errorf("h32 files rows = %d:\n%s", r["FILES"], out32)
	}
	if r["SUMMARY"] > 6 || r["SUMMARY"] < 2 || r["OUTPUT"] < 4 || r["OUTPUT"] > 6 {
		t.Errorf("h32 summary=%d output=%d:\n%s", r["SUMMARY"], r["OUTPUT"], out32)
	}
	if r["DESCRIPTION"] < 4 {
		t.Errorf("h32 description rows = %d", r["DESCRIPTION"])
	}
	// Truncated sections end with an ellipsis.
	if !strings.Contains(out32, "…") {
		t.Errorf("no ellipsis:\n%s", out32)
	}
}

func TestInspectorDescriptionAbsorbsLeftover(t *testing.T) {
	g, n, _ := inspectorLongNode()
	n.Summary, n.Files = "", nil
	n.InputTokens, n.OutputTokens = 0, 0
	out := ansi.Strip(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, 40, 30, nil))
	if r := sectionRows(out)["DESCRIPTION"]; r < 15 {
		t.Errorf("description rows = %d:\n%s", r, out)
	}
}

func TestInspectorShowsObservedFiles(t *testing.T) {
	g, n, _ := inspectorLongNode()
	n.Files = []string{"a.go"}
	n.Observed = []string{"todo.py"}
	out := ansi.Strip(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, 60, 30, nil))
	if !strings.Contains(out, "todo.py (by the agent's own tools)") || strings.Index(out, "a.go") > strings.Index(out, "todo.py") {
		t.Errorf("observed files:\n%s", out)
	}
}

func TestInspectorWarningsAndErrorShownUpToSixRows(t *testing.T) {
	g, n, _ := inspectorLongNode()
	n.Status = workspace.StatusFailed
	n.Error = strings.Repeat("boom ", 100)
	n.Warnings = []string{"first warning", "second warning"}
	out := ansi.Strip(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, 40, 50, nil))
	r := sectionRows(out)
	if r["ERROR"] != 6 || r["WARNINGS"] != 2 {
		t.Errorf("error=%d warnings=%d:\n%s", r["ERROR"], r["WARNINGS"], out)
	}
}
