package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude writes a Claude Code plugin cache and index under home.
func fakeClaude(t *testing.T, home string) (superpowers, docs string) {
	t.Helper()
	cache := filepath.Join(home, ".claude", "plugins", "cache")
	superpowers = filepath.Join(cache, "official", "superpowers", "7.0")
	write(t, filepath.Join(superpowers, ".claude-plugin", "plugin.json"), `{"name":"superpowers","version":"7.0","description":"workflows"}`)
	write(t, filepath.Join(superpowers, "skills", "brainstorming", "SKILL.md"), "---\ndescription: ideas\n---\nbody")
	write(t, filepath.Join(superpowers, "agents", "reviewer.md"), "---\ndescription: reviews\n---\nyou review")
	write(t, filepath.Join(superpowers, "hooks", "hooks.json"), "{}")
	write(t, filepath.Join(superpowers, ".git", "config"), "never copied")
	// a plugin with only a marketplace.json, as the Anthropic skills repo has
	docs = filepath.Join(cache, "skills", "docs", "abc")
	write(t, filepath.Join(docs, ".claude-plugin", "marketplace.json"),
		`{"name":"skills","plugins":[{"name":"docs","description":"Documents","skills":["./skills/xlsx"]}]}`)
	write(t, filepath.Join(docs, "skills", "xlsx", "SKILL.md"), "---\ndescription: sheets\n---\nbody")
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), fmt.Sprintf(`{"version":2,"plugins":{
		"superpowers@official":[{"scope":"user","installPath":%q,"version":"7.0"}],
		"docs@skills":[{"scope":"user","installPath":%q,"version":"abc"}],
		"outside@x":[{"installPath":"/etc"}],
		"relative@x":[{"installPath":"cache/relative"}],
		"bad name@x":[{"installPath":%q}]}}`, superpowers, docs, superpowers))
	return superpowers, docs
}

func TestClaudeInstalledListsOnlyThePluginCache(t *testing.T) {
	home, _ := setup(t)
	fakeClaude(t, home)
	// a login file next to the index must never be read
	write(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"secret"}`)
	got := ClaudeInstalled()
	if len(got) != 2 || got[0].Name != "docs" || got[1].Name != "superpowers" {
		t.Fatalf("listed %+v", got)
	}
	sp := got[1]
	if sp.Marketplace != "official" || sp.Version != "7.0" || sp.Skills != 1 || sp.Agents != 1 {
		t.Errorf("superpowers = %+v", sp)
	}
	if got[0].Skills != 1 { // no plugin.json: counted by the skills folder
		t.Errorf("docs skills = %d", got[0].Skills)
	}
}

func TestClaudeInstalledDefensive(t *testing.T) {
	home, _ := setup(t)
	if got := ClaudeInstalled(); got != nil {
		t.Errorf("no files: %v", got)
	}
	idx := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	for _, body := range []string{"", "{", `[]`, `{"plugins":{"a@b":"x"}}`, `{"plugins":null}`} {
		write(t, idx, body)
		if got := ClaudeInstalled(); len(got) != 0 {
			t.Errorf("%q listed %v", body, got)
		}
	}
	// an install path that leaves the cache through a symlink is refused
	cache := filepath.Join(home, ".claude", "plugins", "cache")
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cache, "link")); err != nil {
		t.Skip(err)
	}
	write(t, idx, fmt.Sprintf(`{"plugins":{"evil@x":[{"installPath":%q}]}}`, filepath.Join(cache, "link")))
	if got := ClaudeInstalled(); len(got) != 0 {
		t.Errorf("symlink out of the cache listed: %v", got)
	}
}

func TestImportClaudeCopiesIntoTheLoaderLayout(t *testing.T) {
	home, proj := setup(t)
	sp, _ := fakeClaude(t, home)
	p, err := ImportClaude("superpowers")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skills) != 1 || len(p.Agents) != 1 {
		t.Errorf("imported %+v", p)
	}
	dst := filepath.Join(home, ".decsr", "plugins", "superpowers")
	if !isFile(filepath.Join(dst, ".claude-plugin", "plugin.json")) || !isFile(filepath.Join(dst, "skills", "brainstorming", "SKILL.md")) {
		t.Error("files not copied")
	}
	if isDir(filepath.Join(dst, ".git")) {
		t.Error(".git copied")
	}
	set := Load(proj)
	pl := find(set, "superpowers")
	if pl == nil || !pl.Enabled || len(pl.Skills) != 1 || !pl.HasHooks {
		t.Fatalf("loader sees %+v", pl)
	}
	if !isFile(filepath.Join(sp, ".claude-plugin", "plugin.json")) {
		t.Error("the Claude copy changed")
	}
	if _, err := ImportClaude("superpowers"); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("second import = %v", err)
	}
	if _, err := ImportClaude("nope"); err == nil {
		t.Error("unknown plugin imported")
	}
}

func TestImportClaudeWritesAManifestWhenThereIsNone(t *testing.T) {
	home, proj := setup(t)
	fakeClaude(t, home)
	p, err := ImportClaude("docs")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "docs" {
		t.Errorf("name = %q", p.Name)
	}
	pl := find(Load(proj), "docs")
	if pl == nil || len(pl.Skills) != 1 {
		t.Fatalf("loader sees %+v", pl)
	}
}

func TestRemoveDeletesOnlyPluginFolders(t *testing.T) {
	home, proj := setup(t)
	mkPlugin(t, home, "gone", `{"name":"gone"}`)
	mkPlugin(t, proj, "projonly", `{"name":"projonly"}`)
	if err := SetEnabled("gone", false); err != nil {
		t.Fatal(err)
	}
	if err := Remove(proj, "gone"); err != nil {
		t.Fatal(err)
	}
	if isDir(filepath.Join(home, ".decsr", "plugins", "gone")) || readDisabled()["gone"] {
		t.Error("plugin or disabled line left")
	}
	if err := Remove(proj, "projonly"); err != nil || isDir(filepath.Join(proj, ".decsr", "plugins", "projonly")) {
		t.Errorf("project plugin: %v", err)
	}
	if err := Remove(proj, "nope"); err == nil {
		t.Error("unknown plugin removed")
	}
}
