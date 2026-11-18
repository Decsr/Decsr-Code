package workspace

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestParseSplitMCP(t *testing.T) {
	out := `===TITLE===
Read issue
===TASKS===
[{"key":"T1","title":"Read issue 12","description":"read it","role":"coder","writes":false,"mcp":["github"," github ","claude.ai Google Drive"]},
 {"key":"T2","title":"Bad","description":"x","role":"coder","mcp":["", "` + strings.Repeat("a", 81) + `","ok\u0007bad","plugin:open-design:open-design"]},
 {"key":"T3","title":"One name","description":"x","role":"coder","mcp":"google-workspace"},
 {"key":"T4","title":"None","description":"x","role":"coder"}]
===END===`
	g, err := ParseSplit("t", out, testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Nodes[0].MCP; !reflect.DeepEqual(got, []string{"github", "claude.ai Google Drive"}) {
		t.Errorf("T1 mcp = %q", got)
	}
	if got := g.Nodes[1].MCP; !reflect.DeepEqual(got, []string{"plugin:open-design:open-design"}) {
		t.Errorf("T2 mcp = %q", got)
	}
	if got := g.Nodes[2].MCP; !reflect.DeepEqual(got, []string{"google-workspace"}) {
		t.Errorf("T3 mcp = %q", got)
	}
	if g.Nodes[3].MCP != nil {
		t.Errorf("T4 mcp = %q, want none", g.Nodes[3].MCP)
	}
	warned := 0
	for _, w := range g.SplitWarnings {
		if strings.HasPrefix(w, "T2: MCP server name") {
			warned++
		}
	}
	if warned != 1 {
		t.Errorf("warnings = %q", g.SplitWarnings)
	}
}

func TestNodeMCPIsCloned(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Kind: KindWork, MCP: []string{"github"}}}}
	c := g.Clone()
	c.Nodes[0].MCP[0] = "other"
	if g.Nodes[0].MCP[0] != "github" {
		t.Error("Clone shares the MCP list")
	}
}

func TestLiveNodesAndCompactShowMCP(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "Read issue", Kind: KindWork, Status: StatusDone, Role: "coder", MCP: []string{"github", "google-workspace"}, Summary: "read it"},
		{ID: "T2", Title: "Plain", Kind: KindWork, Status: StatusTodo, Role: "coder"},
	}}
	sec := liveSection(LiveNodes(g))
	if !strings.Contains(sec, "| mcp: github, google-workspace") || strings.Count(sec, "mcp:") != 1 {
		t.Errorf("live section = %q", sec)
	}
	if nodes := compactNodes(g); !strings.Contains(nodes, " mcp: github, google-workspace") || strings.Count(nodes, "mcp:") != 1 {
		t.Errorf("compact nodes = %q", nodes)
	}
}

func TestExecPassesMCPOnlyToClaude(t *testing.T) {
	run := func(runnerName string) *execFake {
		cfg := config.DefaultConfig()
		ac := cfg.Agents["coder"]
		ac.Runner, ac.Mode = runnerName, config.ModeAgent
		cfg.Agents["coder"] = ac
		f := &execFake{text: "x"}
		ex := newTestExec(t, cfg, f, nil)
		g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder", MCP: []string{"claude.ai Google Drive", "github"}}}}
		if _, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {}); err != nil {
			t.Fatal(err)
		}
		return f
	}
	has := func(f *execFake, rule string) bool {
		for _, a := range f.reqs[0].AllowedTools {
			if a == rule {
				return true
			}
		}
		return false
	}
	f := run("claude")
	if !f.reqs[0].MCP || !has(f, "mcp__claude_ai_Google_Drive") || !has(f, "mcp__github") {
		t.Errorf("claude request: mcp=%v tools=%q", f.reqs[0].MCP, f.reqs[0].AllowedTools)
	}
	f = run("codex")
	if f.reqs[0].MCP || has(f, "mcp__github") {
		t.Errorf("codex request: mcp=%v tools=%q", f.reqs[0].MCP, f.reqs[0].AllowedTools)
	}
}

func TestReadOnlyAnswerStopsAtTheSummaryMarker(t *testing.T) {
	f := &execFake{text: "I made no changes to any files.\n\n===SUMMARY===\nCalled the guide tool and it returned the index.\n\nREPORT\nstatus: done\nsummary: read the index\n"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "reviewer", ReadOnlyTask: true}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "I made no changes to any files." {
		t.Errorf("answer = %q", res.Answer)
	}
	if got := answerBody("plain answer"); got != "plain answer" {
		t.Errorf("no marker: %q", got)
	}
}
