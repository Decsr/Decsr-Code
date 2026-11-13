package plugins

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// mkPlugin writes a plugin with the given manifest under base/.decsr/plugins/dir.
func mkPlugin(t *testing.T, base, dir, manifest string) string {
	t.Helper()
	root := filepath.Join(base, ".decsr", "plugins", dir)
	write(t, filepath.Join(root, ".claude-plugin", "plugin.json"), manifest)
	return root
}

func setup(t *testing.T) (home, proj string) {
	home, proj = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	return home, proj
}

func find(s Set, name string) *Plugin {
	for i := range s.Plugins {
		if s.Plugins[i].Name == name {
			return &s.Plugins[i]
		}
	}
	return nil
}

func TestParseFrontmatter(t *testing.T) {
	fm, body := ParseFrontmatter("---\ndescription: Hi there\nargument-hint: [msg]\n---\nBody $ARGUMENTS\n")
	if fm.String("description") != "Hi there" || fm.String("argument-hint") != "[msg]" || body != "Body $ARGUMENTS\n" {
		t.Fatalf("fm=%v body=%q", fm, body)
	}
	for _, in := range []string{"no header", "---\nunclosed: yes\n"} {
		fm, body := ParseFrontmatter(in)
		if fm != nil || body != in {
			t.Errorf("%q: fm=%v body=%q", in, fm, body)
		}
	}
	fm, body = ParseFrontmatter("---\n---\nonly body")
	if fm == nil || body != "only body" {
		t.Errorf("empty header: fm=%v body=%q", fm, body)
	}
}

func TestParseFrontmatterInvalidYAMLFallsBackToLines(t *testing.T) {
	in := "---\nargument-hint: [add | remove] [id]\ndescription: Do this: then that\ntitle: \"Quoted\"\nsingle: 'x y'\n  indented: no\n- junk\n---\nbody text"
	fm, body := ParseFrontmatter(in)
	if body != "body text" {
		t.Errorf("body = %q", body)
	}
	for k, want := range map[string]string{
		"argument-hint": "[add | remove] [id]", "description": "Do this: then that", "title": "Quoted", "single": "x y",
	} {
		if got := fm.String(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if _, ok := fm["indented"]; ok {
		t.Error("indented line should be ignored")
	}
	d, h, b := SplitCommand(in)
	if d != "Do this: then that" || h != "[add | remove] [id]" || b != "body text" {
		t.Errorf("SplitCommand = %q %q %q", d, h, b)
	}
}

func TestSplitCommand(t *testing.T) {
	d, h, b := SplitCommand("---\ndescription: From fm\nargument-hint: \"<a> [b]\"\n---\n# Heading\nbody")
	if d != "From fm" || h != "<a> [b]" || b != "# Heading\nbody" {
		t.Errorf("%q %q %q", d, h, b)
	}
	d, _, b = SplitCommand("---\nname: x\n---\n# Heading\nbody")
	if d != "Heading" || b != "body" {
		t.Errorf("%q %q", d, b)
	}
	d, _, b = SplitCommand("plain text")
	if d != "plain text" || b != "plain text" {
		t.Errorf("%q %q", d, b)
	}
	d, _, b = SplitCommand("---\ndescription: [broken\n---\nx")
	if d != "[broken" || b != "x" {
		t.Errorf("malformed header: %q %q", d, b)
	}
}

func TestLoadDiscoveryAndPrecedence(t *testing.T) {
	home, proj := setup(t)
	mkPlugin(t, home, "alpha-dir", `{"name":"alpha","version":"1.0","description":"user alpha"}`)
	mkPlugin(t, proj, "alpha", `{"name":"alpha","description":"project alpha"}`)
	mkPlugin(t, home, "beta", `{"name":"beta"}`)
	mkPlugin(t, home, "bad", `{not json`)
	mkPlugin(t, home, "badname", `{"name":"Bad Name"}`)
	if err := os.MkdirAll(filepath.Join(home, ".decsr", "plugins", "nomanifest"), 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".decsr", "plugins", "somefile"), "x")
	set := Load(proj)
	if len(set.Plugins) != 2 || set.Plugins[0].Name != "alpha" || set.Plugins[1].Name != "beta" {
		t.Fatalf("plugins = %+v", set.Plugins)
	}
	if a := set.Plugins[0]; a.Scope != "project" || a.Description != "project alpha" {
		t.Errorf("project should win: %+v", a)
	}
	if len(set.Warnings) != 2 {
		t.Errorf("warnings = %v", set.Warnings)
	}
}

func TestLoadSkipsSymlinkedPluginDir(t *testing.T) {
	home, proj := setup(t)
	real := mkPlugin(t, t.TempDir(), "real", `{"name":"real"}`)
	base := filepath.Join(home, ".decsr", "plugins")
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	if got := Load(proj); len(got.Plugins) != 0 {
		t.Errorf("symlinked plugin loaded: %+v", got.Plugins)
	}
}

func TestLoadSkipsSymlinkedManifestDir(t *testing.T) {
	home, proj := setup(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "plugin.json"), `{"name":"sneaky"}`)
	dir := filepath.Join(home, ".decsr", "plugins", "sneaky")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".claude-plugin")); err != nil {
		t.Fatal(err)
	}
	if got := Load(proj); len(got.Plugins) != 0 {
		t.Errorf("plugin with symlinked .claude-plugin loaded: %+v", got.Plugins)
	}
}

func TestParseFrontmatterInvalidYAMLWithoutPairsIsBody(t *testing.T) {
	in := "---\n- junk\n  [: bad\n---\nbody"
	fm, body := ParseFrontmatter(in)
	if fm != nil || body != in {
		t.Errorf("fm=%v body=%q", fm, body)
	}
}

func TestCommands(t *testing.T) {
	home, proj := setup(t)
	root := mkPlugin(t, home, "p", `{"name":"p","commands":["extra", "/abs", "../escape"]}`)
	write(t, filepath.Join(root, "commands", "review.md"), "---\ndescription: Review it\nargument-hint: \"<pr>\"\n---\nDo $ARGUMENTS")
	write(t, filepath.Join(root, "commands", "Bad Name.md"), "x")
	write(t, filepath.Join(root, "commands", "empty.md"), "---\ndescription: x\n---\n")
	write(t, filepath.Join(root, "commands", "sub", "nested.md"), "nested")
	write(t, filepath.Join(root, "extra", "more.md"), "# More\nbody")
	write(t, filepath.Join(home, ".decsr", "plugins", "escape", "outside.md"), "no")
	cmds := Load(proj).Commands()
	if len(cmds) != 2 {
		t.Fatalf("cmds = %+v", cmds)
	}
	if c := cmds[1]; c.Plugin != "p" || c.Name != "review" || c.Description != "Review it" || c.ArgHint != "<pr>" || c.Body != "Do $ARGUMENTS" {
		t.Errorf("review = %+v", c)
	}
	if c := cmds[0]; c.Name != "more" || c.Description != "More" {
		t.Errorf("more = %+v", c)
	}
}

func TestSkills(t *testing.T) {
	home, proj := setup(t)
	root := mkPlugin(t, home, "ui", `{"name":"ui","skills":"./.claude/skills/"}`)
	write(t, filepath.Join(root, ".claude", "skills", "design", "SKILL.md"), "---\nname: design\ndescription: Design things\n---\n# Design\nUse grids.")
	write(t, filepath.Join(root, "skills", "plain", "SKILL.md"), "Plain body")
	write(t, filepath.Join(root, "skills", "Bad Dir", "SKILL.md"), "x")
	write(t, filepath.Join(root, "skills", "nofile", "other.md"), "x")
	set := Load(proj)
	p := find(set, "ui")
	if p == nil || len(p.Skills) != 2 || p.Skills[0].ID != "ui:design" || p.Skills[1].ID != "ui:plain" {
		t.Fatalf("skills = %+v", p)
	}
	if p.Skills[0].Description != "Design things" {
		t.Errorf("desc = %q", p.Skills[0].Description)
	}
	body, err := LoadSkill(proj, "ui:design")
	if err != nil || body != "# Design\nUse grids." {
		t.Errorf("body = %q err=%v", body, err)
	}
	for _, id := range []string{"ui:missing", "nope:design"} {
		if _, err := LoadSkill(proj, id); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	if _, err := LoadSkill(proj, "design"); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("plain name should be invalid, got %v", err)
	}
}

func TestAgents(t *testing.T) {
	home, proj := setup(t)
	root := mkPlugin(t, home, "fd", `{"name":"fd"}`)
	write(t, filepath.Join(root, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: |\n  Reviews\n  code\ntools: Glob, Grep, Read\n---\nYou review.")
	write(t, filepath.Join(root, "agents", "writer.md"), "---\ndescription: Writes\ntools: [Read, Edit]\n---\nYou write.")
	write(t, filepath.Join(root, "agents", "free.md"), "---\ndescription: Free\n---\nAnything.")
	write(t, filepath.Join(root, "agents", "big.md"), strings.Repeat("x", 64*1024+1))
	roles := Load(proj).AgentRoles()
	if len(roles) != 3 {
		t.Fatalf("got %d roles", len(roles))
	}
	if r := roles["fd:reviewer"]; !r.ReadOnly || r.Description != "Reviews code" || r.Prompt != "You review." {
		t.Errorf("reviewer = %+v", r)
	}
	if roles["fd:writer"].ReadOnly || roles["fd:free"].ReadOnly {
		t.Errorf("writer/free must not be read-only")
	}
	if _, ok := roles["fd:big"]; ok {
		t.Errorf("file over 64 KB should be skipped")
	}
}

func TestAgentPromptCap(t *testing.T) {
	home, proj := setup(t)
	root := mkPlugin(t, home, "fd", `{"name":"fd"}`)
	write(t, filepath.Join(root, "agents", "long.md"), strings.Repeat("y", 30*1024))
	r := Load(proj).AgentRoles()["fd:long"]
	if len(r.Prompt) != 16*1024 {
		t.Errorf("prompt len = %d", len(r.Prompt))
	}
}

func TestDisabledAndSetEnabled(t *testing.T) {
	home, proj := setup(t)
	root := mkPlugin(t, home, "p", `{"name":"p"}`)
	write(t, filepath.Join(root, "commands", "c.md"), "body")
	write(t, filepath.Join(root, "skills", "s", "SKILL.md"), "skill")
	if err := SetEnabled("p", false); err != nil {
		t.Fatal(err)
	}
	set := Load(proj)
	p := find(set, "p")
	if p == nil || p.Enabled || len(p.Commands) != 0 || len(p.Skills) != 0 {
		t.Fatalf("disabled plugin = %+v", p)
	}
	if _, err := LoadSkill(proj, "p:s"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("disabled skill err = %v", err)
	}
	if err := SetEnabled("p", true); err != nil {
		t.Fatal(err)
	}
	if p := find(Load(proj), "p"); !p.Enabled || len(p.Commands) != 1 {
		t.Errorf("re-enabled plugin = %+v", p)
	}
	if err := SetEnabled("../x", false); err == nil {
		t.Error("invalid name accepted")
	}
}

func TestHooksAndMCPDetected(t *testing.T) {
	home, proj := setup(t)
	a := mkPlugin(t, home, "a", `{"name":"a"}`)
	write(t, filepath.Join(a, "hooks", "hooks.json"), "{}")
	b := mkPlugin(t, home, "b", `{"name":"b"}`)
	write(t, filepath.Join(b, ".mcp.json"), "{}")
	mkPlugin(t, home, "c", `{"name":"c","mcpServers":{"x":{}}}`)
	mkPlugin(t, home, "d", `{"name":"d"}`)
	set := Load(proj)
	if !find(set, "a").HasHooks || find(set, "a").HasMCP {
		t.Errorf("a = %+v", find(set, "a"))
	}
	if !find(set, "b").HasMCP || !find(set, "c").HasMCP {
		t.Errorf("b/c should have MCP")
	}
	if d := find(set, "d"); d.HasHooks || d.HasMCP {
		t.Errorf("d = %+v", d)
	}
}

func TestUnsafeFilesSkipped(t *testing.T) {
	home, proj := setup(t)
	root := mkPlugin(t, home, "p", `{"name":"p"}`)
	write(t, filepath.Join(root, "commands", "ok.md"), "fine")
	write(t, filepath.Join(root, "commands", "nul.md"), "a\x00b")
	write(t, filepath.Join(root, "commands", "big.md"), strings.Repeat("x", 64*1024+1))
	outside := filepath.Join(t.TempDir(), "secret.md")
	write(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(root, "commands", "link.md")); err != nil {
		t.Fatal(err)
	}
	cmds := Load(proj).Commands()
	if len(cmds) != 1 || cmds[0].Name != "ok" {
		t.Errorf("cmds = %+v", cmds)
	}
}

func TestDescriptionSanitized(t *testing.T) {
	home, proj := setup(t)
	root := mkPlugin(t, home, "p", `{"name":"p","description":"x\u001b[31my"}`)
	write(t, filepath.Join(root, "commands", "c.md"), "---\ndescription: \"a\\u001b[2Jb\"\n---\nbody")
	set := Load(proj)
	if d := set.Plugins[0].Description; d != "xy" {
		t.Errorf("plugin desc = %q", d)
	}
	if d := set.Commands()[0].Description; d != "ab" {
		t.Errorf("command desc = %q", d)
	}
}
