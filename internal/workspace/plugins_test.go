package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/plugins"
)

// mkHomePlugin writes a plugin named name under home/.decsr/plugins with the
// given files (path relative to the plugin root -> content).
func mkHomePlugin(t *testing.T, home, name string, files map[string]string) {
	t.Helper()
	root := filepath.Join(home, ".decsr", "plugins", name)
	files[".claude-plugin/plugin.json"] = `{"name":"` + name + `"}`
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func pluginCfg(t *testing.T, home, root string) config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.PluginAgents = plugins.Load(root).AgentRoles()
	return cfg
}

func TestPluginRolesInRolesAndFindRole(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	mkHomePlugin(t, home, "fd", map[string]string{
		"agents/reviewer.md": "---\ndescription: Reviews code\ntools: Read, Grep\n---\nYou review.",
	})
	cfg := pluginCfg(t, home, root)
	cfg.Agents["pm"] = config.AgentConfig{Runner: "claude", Model: "m1", Effort: "high"}
	r, ok := FindRole(cfg, "fd:reviewer")
	if !ok || !r.Plugin || !r.ReadOnly || r.Description != "Reviews code" || r.Prompt != "You review." {
		t.Fatalf("role = %+v ok=%v", r, ok)
	}
	if r.Agent.Runner != "claude" || r.Agent.Model != "m1" || r.Agent.Effort != "high" {
		t.Errorf("should inherit pm: %+v", r.Agent)
	}
	roles := Roles(cfg)
	if last := roles[len(roles)-1]; last.Name != "fd:reviewer" {
		t.Errorf("plugin roles should come last, got %s", last.Name)
	}
	// Not part of the persisted agents map.
	if _, in := cfg.Agents["fd:reviewer"]; in {
		t.Error("plugin role leaked into cfg.Agents")
	}
}

func TestPluginRoleSurvivesReconcileAndUnknownIsCleared(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	mkHomePlugin(t, home, "fd", map[string]string{"agents/reviewer.md": "You review."})
	cfg := pluginCfg(t, home, root)
	a, b := n("A"), n("B")
	a.Role, b.Role = "fd:reviewer", "fd:gone"
	g := &Graph{Nodes: []*Node{a, b}}
	Reconcile(g, cfg)
	if a.Role != "fd:reviewer" || b.Role != "" {
		t.Errorf("roles = %q %q", a.Role, b.Role)
	}
}

func TestPluginRoleDoesNotShadowConfiguredRole(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PluginAgents = map[string]config.PluginAgent{"coder": {Prompt: "x"}, "bad name:x": {Prompt: "x"}}
	for _, r := range Roles(cfg) {
		if r.Plugin {
			t.Errorf("unexpected plugin role %q", r.Name)
		}
	}
}

func TestParseSplitKeepsKnownPluginRole(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PluginAgents = map[string]config.PluginAgent{"fd:code-reviewer": {Description: "Reviews", Prompt: "p"}}
	out := `===TASKS===
[{"key":"T1","title":"a","description":"d","role":"fd:code-reviewer","depends_on":[]},
 {"key":"T2","title":"b","description":"d","role":"fd:unknown","depends_on":[]}]
===END===`
	g, err := ParseSplit("t", out, Roles(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[0].SuggestedRole != "fd:code-reviewer" {
		t.Errorf("suggested = %q", g.Nodes[0].SuggestedRole)
	}
	if got := g.Nodes[1].SuggestedRole; strings.Contains(got, ":") || len(g.NewRoles) != 1 || g.NewRoles[0].Name != got {
		t.Errorf("unknown plugin role should be normalized as a new role: %q %+v", got, g.NewRoles)
	}
	if p := BuildSplitPrompt("t", Roles(cfg), ""); !strings.Contains(p, "fd:code-reviewer: Reviews") {
		t.Error("split prompt does not offer the plugin role")
	}
}

func TestParseSplitNewRolesKeepsKnownPluginRole(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PluginAgents = map[string]config.PluginAgent{"fd:code-reviewer": {Description: "Reviews", Prompt: "p"}}
	out := `===NEW_ROLES===
[{"name":"fd:code-reviewer","description":"x"}]
===TASKS===
[{"key":"T1","title":"a","description":"d","role":"fd:code-reviewer","depends_on":[]}]
===END===`
	g, err := ParseSplit("t", out, Roles(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.NewRoles) != 0 {
		t.Errorf("known plugin role became a proposal: %+v", g.NewRoles)
	}
}

func TestExecPluginRoleAndPluginSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkHomePlugin(t, home, "sp", map[string]string{
		"skills/brainstorm/SKILL.md": "---\nname: brainstorm\ndescription: d\n---\nPLUGIN-SKILL-BODY",
		"agents/helper.md":           "---\ndescription: Helps\n---\nYOU-ARE-HELPER",
	})
	cfg := config.DefaultConfig()
	cfg.Agents["docs_writer"] = config.AgentConfig{Description: "Writes docs.", UserSkills: []string{"sp:brainstorm", "sp:ghost", "off:thing"}}
	f := &execFake{text: "done\n===SUMMARY===\nok"}
	ex := newTestExec(t, cfg, f, nil)
	cfg.PluginAgents = plugins.Load(ex.root).AgentRoles()
	ex.cfg = cfg
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "Docs", Kind: KindWork, Role: "docs_writer"},
		{ID: "T2", Title: "Help", Kind: KindWork, Role: "sp:helper"},
	}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if p := f.reqs[0].SystemPrompt; !strings.Contains(p, "PLUGIN-SKILL-BODY") || !strings.Contains(p, "### sp:brainstorm") || strings.Contains(p, "name: brainstorm") {
		t.Errorf("prompt = %q", p)
	}
	want := map[string]bool{`skill "sp:ghost" not found`: false, `skill "off:thing" not found`: false}
	for _, w := range res.Warnings {
		if _, ok := want[w]; ok {
			want[w] = true
		}
	}
	for w, seen := range want {
		if !seen {
			t.Errorf("missing warning %q in %v", w, res.Warnings)
		}
	}
	if _, err := ex.Run(context.Background(), g, g.Nodes[1], func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if p := f.reqs[1].SystemPrompt; !strings.Contains(p, "YOU-ARE-HELPER") {
		t.Errorf("plugin role prompt missing: %q", p)
	}
}

func TestLoadCustomCommandsFrontmatter(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, root, "fm.md", "---\ndescription: From frontmatter\nargument-hint: \"<file>\"\n---\n# Title\nDo $ARGUMENTS")
	writeCmd(t, root, "plain.md", "# Heading\nbody")
	got := LoadCustomCommands(root)
	if len(got) != 2 {
		t.Fatalf("got %s", cmdNames(got))
	}
	if c := got[0]; c.Name != "fm" || c.Description != "From frontmatter" || c.ArgHint != "<file>" || c.Body != "# Title\nDo $ARGUMENTS" {
		t.Errorf("fm = %+v", c)
	}
	if c := got[1]; c.Description != "Heading" || c.Body != "body" {
		t.Errorf("plain = %+v", c)
	}
}

func TestPluginCommands(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	mkHomePlugin(t, home, "tb", map[string]string{
		"commands/diff-review.md": "---\ndescription: Review a diff\nargument-hint: \"<ref>\"\n---\nReview $ARGUMENTS",
	})
	cs := PluginCommands(plugins.Load(root))
	if len(cs) != 1 {
		t.Fatalf("cs = %+v", cs)
	}
	c := cs[0]
	if c.Name != "tb:diff-review" || c.Source != "plugin:tb" || c.ArgHint != "<ref>" || c.Expand("main") != "Review main" {
		t.Errorf("c = %+v", c)
	}
}
