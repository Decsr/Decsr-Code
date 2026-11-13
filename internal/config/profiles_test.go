package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	long := "a" + strings.Repeat("b", 31) // 32 chars
	for _, n := range []string{"a", "0", "my-profile", "x_1", long} {
		if !ValidName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range []string{"", "-a", "_a", "A", "a b", "../x", "a/b", "a.b", ".hidden", long + "c", "é"} {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func testAgents() map[string]AgentConfig {
	return map[string]AgentConfig{
		"coder":       {Runner: "claude", Model: "m1", Effort: "high", MaxContextTokens: 99, Skills: []string{"embedded"}},
		"docs_writer": {Runner: "ollama", Model: "m2", Description: "Writes docs", ReadOnly: true, UserSkills: []string{"style"}},
	}
}

func TestProfileRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := ListProfiles(); len(got) != 0 {
		t.Fatalf("list = %v", got)
	}
	if ProfilesDir() != filepath.Join(home, ".decsr", "profiles") {
		t.Fatalf("dir = %s", ProfilesDir())
	}
	if err := SaveProfile("zeta", testAgents()); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile("alpha", testAgents()); err != nil {
		t.Fatal(err)
	}
	if got := ListProfiles(); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("list = %v", got)
	}
	// Non-profile entries are ignored.
	_ = os.WriteFile(filepath.Join(ProfilesDir(), "Bad Name.yaml"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(ProfilesDir(), "notes.txt"), []byte("x"), 0o600)
	_ = os.Mkdir(filepath.Join(ProfilesDir(), "dir.yaml"), 0o750)
	if got := ListProfiles(); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("list with junk = %v", got)
	}
	p, err := LoadProfile("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "alpha" {
		t.Errorf("name = %q", p.Name)
	}
	c := p.Agents["coder"]
	if c.Runner != "claude" || c.Model != "m1" || c.Effort != "high" || c.MaxContextTokens != 0 || c.Skills != nil {
		t.Errorf("coder = %+v (only runner, model, effort, description, read_only, skills are stored)", c)
	}
	d := p.Agents["docs_writer"]
	if d.Description != "Writes docs" || !d.ReadOnly || !reflect.DeepEqual(d.UserSkills, []string{"style"}) {
		t.Errorf("docs_writer = %+v", d)
	}
	st, err := os.Stat(filepath.Join(ProfilesDir(), "alpha.yaml"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("stat %v %v", st, err)
	}
	if err := DeleteProfile("alpha"); err != nil {
		t.Fatal(err)
	}
	if got := ListProfiles(); !reflect.DeepEqual(got, []string{"zeta"}) {
		t.Fatalf("after delete = %v", got)
	}
	if _, err := LoadProfile("alpha"); err == nil {
		t.Error("deleted profile still loads")
	}
}

func TestProfileInvalidNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	for _, n := range []string{"", "../x", "A", "a/b", "a b"} {
		if _, err := LoadProfile(n); err == nil {
			t.Errorf("LoadProfile(%q)", n)
		}
		if err := SaveProfile(n, testAgents()); err == nil {
			t.Errorf("SaveProfile(%q)", n)
		}
		if err := DeleteProfile(n); err == nil {
			t.Errorf("DeleteProfile(%q)", n)
		}
		if err := ApplyProfile(root, n); err == nil {
			t.Errorf("ApplyProfile(%q)", n)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".decsr")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(home, ".decsr"))
		if len(entries) > 0 {
			t.Errorf("files created for invalid names: %v", entries)
		}
	}
}

func TestApplyProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := Save(root, Config{
		Project: ProjectConfig{Name: "demo", TestCmd: "make test"},
		Agents: map[string]AgentConfig{
			"coder":  {Model: "old", MaxContextTokens: 77},
			"keeper": {Description: "stays", Runner: "claude"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile("team", testAgents()); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProfile(root, "team"); err != nil {
		t.Fatal(err)
	}
	proj := LoadProject(root)
	if proj.Project.Name != "demo" || proj.Project.TestCmd != "make test" || proj.Project.Profile != "team" {
		t.Errorf("project = %+v", proj.Project)
	}
	if c := proj.Agents["coder"]; c.Runner != "claude" || c.Model != "m1" || c.Effort != "high" || c.MaxContextTokens != 77 {
		t.Errorf("coder = %+v", c)
	}
	if k := proj.Agents["keeper"]; k.Description != "stays" || k.Runner != "claude" {
		t.Errorf("keeper = %+v", k)
	}
	d := proj.Agents["docs_writer"]
	if d.Runner != "ollama" || d.Description != "Writes docs" || !d.ReadOnly || !reflect.DeepEqual(d.UserSkills, []string{"style"}) {
		t.Errorf("docs_writer = %+v", d)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.Profile != "team" || !reflect.DeepEqual(cfg.Agents["docs_writer"].UserSkills, []string{"style"}) {
		t.Errorf("Load merge lost profile/skills: %q %+v", cfg.Project.Profile, cfg.Agents["docs_writer"])
	}
}

func TestApplyProfileUnknownRunner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := Save(root, Config{Project: ProjectConfig{Name: "demo"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".decsr", ProjectFilename)
	before, _ := os.ReadFile(path)
	if err := SaveProfile("bad", map[string]AgentConfig{"coder": {Runner: "nope"}}); err != nil {
		t.Fatal(err)
	}
	err := ApplyProfile(root, "bad")
	want := `profile "bad": role "coder" uses unknown runner "nope"`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Errorf("project.yaml changed")
	}
}

func TestApplyProfileMissingAndHostileRole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := ApplyProfile(root, "ghost"); err == nil {
		t.Error("missing profile should fail")
	}
	if err := os.MkdirAll(ProfilesDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	yml := "agents:\n  \"../evil\":\n    runner: claude\n"
	if err := os.WriteFile(filepath.Join(ProfilesDir(), "evil.yaml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProfile(root, "evil"); err == nil {
		t.Error("invalid role name should fail")
	}
	if _, err := os.Stat(filepath.Join(root, ".decsr", ProjectFilename)); err == nil {
		t.Error("project.yaml written")
	}
}

func TestApplyProfileBrokenProjectUntouched(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, GlobalDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ProjectFilename)
	broken := "project:\n  name: [unterminated\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile("team", testAgents()); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProfile(root, "team"); err == nil {
		t.Fatal("expected error")
	}
	got, _ := os.ReadFile(path)
	if string(got) != broken {
		t.Errorf("file modified: %q", got)
	}
}

func TestUserSkillsSurviveLoadAndSaveProjectRole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Global layer gives skills to one role, project layer to another.
	if err := os.MkdirAll(filepath.Join(home, ".decsr"), 0o750); err != nil {
		t.Fatal(err)
	}
	g := "agents:\n  coder:\n    skills: [g1]\n"
	if err := os.WriteFile(filepath.Join(home, ".decsr", GlobalFilename), []byte(g), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := Save(root, Config{Agents: map[string]AgentConfig{"qa": {UserSkills: []string{"q1", "q2"}}}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Agents["coder"].UserSkills, []string{"g1"}) || !reflect.DeepEqual(cfg.Agents["qa"].UserSkills, []string{"q1", "q2"}) {
		t.Fatalf("merge: coder=%+v qa=%+v", cfg.Agents["coder"], cfg.Agents["qa"])
	}
	// Embedded skills stay defaults.
	if !reflect.DeepEqual(cfg.Agents["coder"].Skills, []string{"coder"}) {
		t.Errorf("embedded skills = %v", cfg.Agents["coder"].Skills)
	}
	if err := SaveProjectRole(root, "qa", AgentConfig{Model: "new"}); err != nil {
		t.Fatal(err)
	}
	qa := LoadProject(root).Agents["qa"]
	if qa.Model != "new" || !reflect.DeepEqual(qa.UserSkills, []string{"q1", "q2"}) {
		t.Errorf("SaveProjectRole lost skills: %+v", qa)
	}
}

func TestLoadUserSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pdir := filepath.Join(root, ".decsr", "skills")
	hdir := filepath.Join(home, ".decsr", "skills")
	write(hdir, "style", "home style")
	write(hdir, "only-home", "from home")
	write(pdir, "style", "project style")
	write(pdir, "big", strings.Repeat("x", 64*1024+1))
	write(pdir, "exact", strings.Repeat("x", 64*1024))

	if got, err := LoadUserSkill(root, "style"); err != nil || got != "project style" {
		t.Errorf("precedence: %q %v", got, err)
	}
	if got, err := LoadUserSkill(root, "only-home"); err != nil || got != "from home" {
		t.Errorf("home fallback: %q %v", got, err)
	}
	if _, err := LoadUserSkill(root, "big"); err == nil {
		t.Error("oversized skill should fail")
	}
	if got, err := LoadUserSkill(root, "exact"); err != nil || len(got) != 64*1024 {
		t.Errorf("exact size: %d %v", len(got), err)
	}
	if _, err := LoadUserSkill(root, "missing"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	for _, n := range []string{"", "../style", "Style", "a/b"} {
		if _, err := LoadUserSkill(root, n); err == nil {
			t.Errorf("name %q should be rejected", n)
		}
	}
}
