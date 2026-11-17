package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_NoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Language != "" {
		t.Errorf("expected empty language, got %q", cfg.Project.Language)
	}
	if cfg.Project.TestCmd != "" {
		t.Errorf("unexpected test_cmd: %q", cfg.Project.TestCmd)
	}
	if cfg.Agents["planner"].Runner != "" {
		t.Errorf("expected planner runner empty, got %q", cfg.Agents["planner"].Runner)
	}
}

func TestLoad_LegacyFileMigration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	content := `
project:
  name: myapp
  test_cmd: "make test"
`
	// Write to legacy location (root/.decsr.yaml).
	legacyPath := filepath.Join(dir, Filename)
	if err := os.WriteFile(legacyPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project.Name != "myapp" {
		t.Errorf("expected name=myapp, got %q", cfg.Project.Name)
	}
	if cfg.Project.TestCmd != "make test" {
		t.Errorf("expected test_cmd='make test', got %q", cfg.Project.TestCmd)
	}
	if cfg.Project.Language != "" {
		t.Errorf("expected empty language (default), got %q", cfg.Project.Language)
	}

	// Legacy file should be removed after migration.
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("expected legacy file to be removed after migration")
	}

	// New file should exist.
	newPath := filepath.Join(dir, GlobalDir, ProjectFilename)
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Error("expected project.yaml to be created during migration")
	}
}

func TestLoad_ProjectYAML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Write to new location (.decsr/project.yaml).
	projectDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
project:
  name: newapp
  module_path: github.com/user/newapp
agents:
  coder:
    runner: claude
    model: sonnet
`
	if err := os.WriteFile(filepath.Join(projectDir, ProjectFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project.Name != "newapp" {
		t.Errorf("expected name=newapp, got %q", cfg.Project.Name)
	}
	if cfg.Project.ModulePath != "github.com/user/newapp" {
		t.Errorf("expected module_path, got %q", cfg.Project.ModulePath)
	}
	if cfg.Agents["coder"].Runner != "claude" {
		t.Errorf("expected coder runner=claude, got %q", cfg.Agents["coder"].Runner)
	}
	if cfg.Agents["coder"].Model != "sonnet" {
		t.Errorf("expected coder model=sonnet, got %q", cfg.Agents["coder"].Model)
	}
	// Non-overridden agents keep defaults.
	if cfg.Agents["planner"].Runner != "" {
		t.Errorf("expected planner runner empty, got %q", cfg.Agents["planner"].Runner)
	}
}

func TestLoad_AgentOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	content := `
agents:
  coder:
    runner: codex
    model: ""
`
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agents["coder"].Runner != "codex" {
		t.Errorf("expected coder runner=codex, got %q", cfg.Agents["coder"].Runner)
	}
	if cfg.Agents["planner"].Runner != "" {
		t.Errorf("expected planner runner empty, got %q", cfg.Agents["planner"].Runner)
	}
}

func TestLoad_AgentEffortOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()

	globalDir := filepath.Join(home, GlobalDir)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalContent := `
agents:
  coder:
    effort: high
  qa:
    effort: medium
`
	if err := os.WriteFile(filepath.Join(globalDir, GlobalFilename), []byte(globalContent), 0o644); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectContent := `
agents:
  coder:
    effort: default
  qa:
    effort: low
`
	if err := os.WriteFile(filepath.Join(projectDir, ProjectFilename), []byte(projectContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agents["coder"].Effort != "default" {
		t.Errorf("project default should override global high, got %q", cfg.Agents["coder"].Effort)
	}
	if cfg.Agents["qa"].Effort != "low" {
		t.Errorf("expected qa effort=low, got %q", cfg.Agents["qa"].Effort)
	}
}

func TestLoad_GlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	globalDir := filepath.Join(home, GlobalDir)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}

	globalContent := `
agents:
  planner:
    model: "llama3.2:latest"
  coder:
    model: "llama3.2:latest"
`
	if err := os.WriteFile(filepath.Join(globalDir, GlobalFilename), []byte(globalContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project config overrides only coder model (new location).
	projectDir := t.TempDir()
	wsDir := filepath.Join(projectDir, GlobalDir)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectContent := `
agents:
  coder:
    model: "deepseek-coder-v2:latest"
`
	if err := os.WriteFile(filepath.Join(wsDir, ProjectFilename), []byte(projectContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agents["planner"].Model != "llama3.2:latest" {
		t.Errorf("expected planner model from global config, got %q", cfg.Agents["planner"].Model)
	}
	if cfg.Agents["coder"].Model != "deepseek-coder-v2:latest" {
		t.Errorf("expected coder model from project config, got %q", cfg.Agents["coder"].Model)
	}
	if cfg.Agents["planner"].Runner != "" {
		t.Errorf("expected planner runner empty (from defaults), got %q", cfg.Agents["planner"].Runner)
	}
}

func TestSave_And_LoadProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	cfg := Config{
		Project: ProjectConfig{
			Name:       "testproject",
			ModulePath: "github.com/test/project",
		},
		Agents: map[string]AgentConfig{
			"coder": {Runner: "claude", Model: "sonnet"},
		},
	}

	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file was created at new location.
	newPath := filepath.Join(dir, GlobalDir, ProjectFilename)
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Fatal("expected project.yaml to be created")
	}

	loaded := LoadProject(dir)
	if loaded.Project.Name != "testproject" {
		t.Errorf("expected name=testproject, got %q", loaded.Project.Name)
	}
	if loaded.Project.ModulePath != "github.com/test/project" {
		t.Errorf("expected module_path, got %q", loaded.Project.ModulePath)
	}
	if loaded.Agents["coder"].Runner != "claude" {
		t.Errorf("expected coder runner=claude, got %q", loaded.Agents["coder"].Runner)
	}
}

func TestSaveGlobal_CreatesDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := Config{
		Agents: map[string]AgentConfig{
			"planner": {Runner: "claude", Model: "sonnet"},
		},
	}

	if err := SaveGlobal(cfg); err != nil {
		t.Fatalf("SaveGlobal failed: %v", err)
	}

	globalPath := filepath.Join(home, GlobalDir, GlobalFilename)
	if _, err := os.Stat(globalPath); os.IsNotExist(err) {
		t.Fatal("expected global config to be created")
	}

	loaded, err := loadFile(filepath.Join(home, GlobalDir), GlobalFilename)
	if err != nil {
		t.Fatalf("failed to read global config: %v", err)
	}
	if loaded.Agents["planner"].Runner != "claude" {
		t.Errorf("expected planner runner=claude, got %q", loaded.Agents["planner"].Runner)
	}
}

func TestLoadProject_NoFile(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadProject(dir)
	if cfg.Agents != nil {
		t.Error("expected nil agents from missing project config")
	}
	if cfg.Project.Name != "" {
		t.Errorf("expected empty name, got %q", cfg.Project.Name)
	}
}

func TestLoad_ContextExcludePatternsAndSemanticIndex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	orchDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
project:
  context:
    exclude_patterns:
      - "*.generated.go"
      - "fixtures/**"
    semantic_index:
      enabled: true
      embedder: ollama
      model: nomic-embed-text
      top_k: 25
`
	if err := os.WriteFile(filepath.Join(orchDir, ProjectFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Project.Context.ExcludePatterns) != 2 {
		t.Errorf("expected 2 exclude patterns, got %v", cfg.Project.Context.ExcludePatterns)
	}
	si := cfg.Project.Context.SemanticIndex
	if !si.Enabled || si.Embedder != "ollama" || si.Model != "nomic-embed-text" || si.TopK != 25 {
		t.Errorf("unexpected semantic_index: %+v", si)
	}
}

func TestLoad_ScopedContextShadow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	orchDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
project:
  context:
    scoped_context_shadow: true
`
	if err := os.WriteFile(filepath.Join(orchDir, ProjectFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Project.Context.ScopedContextShadow {
		t.Error("expected scoped_context_shadow to be true after load")
	}
}

func TestLoad_ScopedContextCoderFix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	orchDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
project:
  context:
    scoped_context_coder_fix: true
`
	if err := os.WriteFile(filepath.Join(orchDir, ProjectFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Project.Context.ScopedContextCoderFix {
		t.Error("expected scoped_context_coder_fix to be true after load")
	}
}

func TestMerge_ModelMemory(t *testing.T) {
	dst := DefaultConfig()
	merge(&dst, Config{ModelMemory: ModelMemoryConfig{Mode: "ram", MaxRAMGB: 16}})
	if dst.ModelMemory.Mode != "ram" || dst.ModelMemory.MaxRAMGB != 16 {
		t.Fatalf("ram merge: got %+v", dst.ModelMemory)
	}

	// A later layer switching to context mode overrides the mode and value;
	// the empty-mode case must NOT clobber an existing setting.
	merge(&dst, Config{ModelMemory: ModelMemoryConfig{Mode: "context", MaxContextTokens: 8192}})
	if dst.ModelMemory.Mode != "context" || dst.ModelMemory.MaxContextTokens != 8192 {
		t.Fatalf("context merge: got %+v", dst.ModelMemory)
	}

	merge(&dst, Config{}) // empty layer
	if dst.ModelMemory.Mode != "context" {
		t.Errorf("empty layer should not clear mode, got %q", dst.ModelMemory.Mode)
	}
}

func TestMerge_NotificationsDisableCMux(t *testing.T) {
	dst := DefaultConfig()
	if dst.Notifications.DisableCMux {
		t.Fatal("cmux notifications should be enabled by default")
	}
	merge(&dst, Config{Notifications: NotifyConfig{DisableCMux: true}})
	if !dst.Notifications.DisableCMux {
		t.Error("disable_cmux=true should override")
	}
	// An empty later layer must not re-enable it.
	merge(&dst, Config{})
	if !dst.Notifications.DisableCMux {
		t.Error("empty layer should not clear disable_cmux")
	}
}

func TestMerge_NotificationsChannels(t *testing.T) {
	dst := DefaultConfig()
	merge(&dst, Config{Notifications: NotifyConfig{DisableBell: true, DisableDesktop: true, Webhook: "https://ntfy.sh/a", WebhookFormat: "slack"}})
	n := dst.Notifications
	if !n.DisableBell || !n.DisableDesktop || n.Webhook != "https://ntfy.sh/a" || n.WebhookFormat != "slack" {
		t.Fatalf("first layer not applied: %+v", n)
	}
	merge(&dst, Config{}) // empty layer keeps everything
	if dst.Notifications != n {
		t.Errorf("empty layer changed notifications: %+v", dst.Notifications)
	}
	merge(&dst, Config{Notifications: NotifyConfig{Webhook: "https://ntfy.sh/b"}})
	if dst.Notifications.Webhook != "https://ntfy.sh/b" || dst.Notifications.WebhookFormat != "slack" {
		t.Errorf("non-empty string should override only itself: %+v", dst.Notifications)
	}
}

func TestMerge_WorkspaceIsolation(t *testing.T) {
	dst := DefaultConfig()
	merge(&dst, Config{Workspace: WorkspaceConfig{Isolation: "shared"}})
	if dst.Workspace.Isolation != "shared" {
		t.Errorf("isolation = %q, want shared", dst.Workspace.Isolation)
	}
	merge(&dst, Config{})
	if dst.Workspace.Isolation != "shared" {
		t.Error("empty layer should not clear isolation")
	}
	merge(&dst, Config{Workspace: WorkspaceConfig{Isolation: "worktree"}})
	if dst.Workspace.Isolation != "worktree" {
		t.Errorf("isolation = %q, want worktree", dst.Workspace.Isolation)
	}
}

func TestDefaultConfig_ContextPackEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Project.Context.Pack.Disable {
		t.Error("context pack should be enabled by default")
	}
	if cfg.Project.Context.Pack.MaxOutputTokens != 6000 {
		t.Errorf("max_output_tokens = %d, want 6000", cfg.Project.Context.Pack.MaxOutputTokens)
	}
}

func TestMerge_ContextPack(t *testing.T) {
	dst := DefaultConfig()
	merge(&dst, Config{Project: ProjectConfig{Context: ContextConfig{
		Pack: ContextPackConfig{Disable: true, MaxOutputTokens: 3000},
	}}})
	if !dst.Project.Context.Pack.Disable {
		t.Error("pack.disable=true should override")
	}
	if dst.Project.Context.Pack.MaxOutputTokens != 3000 {
		t.Errorf("max_output_tokens = %d, want 3000", dst.Project.Context.Pack.MaxOutputTokens)
	}
	// Empty later layers must not re-enable it.
	merge(&dst, Config{})
	if !dst.Project.Context.Pack.Disable {
		t.Error("empty layer should not clear pack.disable")
	}
}

func writeProjectYAML(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, GlobalDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ProjectFilename), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_MemoryExplicitFalseAndZero(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeProjectYAML(t, root, "project:\n  context:\n    memory:\n      enabled: false\n      auto_promote: false\n      hybrid_alpha: 0\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Project.Context.Memory
	if m.Enabled || m.AutoPromote || m.HybridAlpha != 0 {
		t.Errorf("explicit false/0 ignored: %+v", m)
	}
	if m.TopK != 8 {
		t.Errorf("unset TopK should keep default 8, got %d", m.TopK)
	}
}

func TestLoad_MemoryUnsetKeepsDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeProjectYAML(t, root, "project:\n  name: x\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Project.Context.Memory
	if !m.Enabled || !m.AutoPromote || m.HybridAlpha != 1.0 {
		t.Errorf("defaults lost: %+v", m)
	}
}

func TestSaveGlobal_UnparseableConfigNotClobbered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, GlobalDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, GlobalFilename)
	bad := []byte("agents: [unclosed\n  : :")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveGlobal(Config{PromptLanguage: "en"}); err == nil {
		t.Fatal("expected error for unparseable global config")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(bad) {
		t.Errorf("global config was overwritten: %q", got)
	}
}

func TestDefaultConfigHasNoLanguageAssumption(t *testing.T) {
	p := DefaultConfig().Project
	if p.Language != "" || p.TestCmd != "" || p.LintCmd != "" {
		t.Errorf("defaults assume a language: %+v", p)
	}
}

func TestSupportedLanguagesOwnerStackFirst(t *testing.T) {
	langs := SupportedProgrammingLanguages
	want := []string{"python", "java", "csharp", "cpp", "c"}
	for i, w := range want {
		if langs[i] != w {
			t.Fatalf("langs[%d] = %q, want %q", i, langs[i], w)
		}
	}
	if len(langs) != 14 {
		t.Errorf("len = %d, want 14", len(langs))
	}
	seen := map[string]bool{}
	for _, l := range langs {
		if seen[l] {
			t.Errorf("duplicate %q", l)
		}
		seen[l] = true
	}
}

func TestCustomRoleSurvivesLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, GlobalDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	yml := "agents:\n  docs_writer:\n    description: Writes docs\n    model: x\n"
	if err := os.WriteFile(filepath.Join(dir, ProjectFilename), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	ac, ok := cfg.Agents["docs_writer"]
	if !ok || ac.Description != "Writes docs" || ac.Model != "x" {
		t.Fatalf("custom role lost: %+v", ac)
	}
}

func TestSaveProjectRole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := Save(root, Config{Project: ProjectConfig{Name: "demo"}}); err != nil {
		t.Fatal(err)
	}
	err := SaveProjectRole(root, "docs_writer", AgentConfig{Description: "Writes docs", Runner: "claude", Model: "m", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.Name != "demo" {
		t.Errorf("project name lost: %q", cfg.Project.Name)
	}
	ac := cfg.Agents["docs_writer"]
	if ac.Description != "Writes docs" || ac.Runner != "claude" || ac.Model != "m" || ac.Effort != "low" {
		t.Errorf("round trip: %+v", ac)
	}
}

func TestValidRoleName(t *testing.T) {
	for _, n := range []string{"docs_writer", "qa2"} {
		if !ValidRoleName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	long := "a"
	for len(long) < 33 {
		long += "b"
	}
	for _, n := range []string{"", "A", "1abc", "has space", "x", long} {
		if ValidRoleName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestValidQualifiedName(t *testing.T) {
	for _, n := range []string{"superpowers:brainstorming", "a-b:c_d", "p:1"} {
		if !ValidQualifiedName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range []string{"", "plain", ":x", "x:", "a:b:c", "A:b", "a:B", "a b:c", "a:../b"} {
		if ValidQualifiedName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestSaveProjectRoleKeepsReadOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := SaveProjectRole(root, "auditor", AgentConfig{Description: "Audits", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Agents["auditor"].ReadOnly {
		t.Errorf("read_only lost: %+v", cfg.Agents["auditor"])
	}
}

func TestSaveProjectRoleBrokenFileUntouched(t *testing.T) {
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
	if err := SaveProjectRole(root, "docs_writer", AgentConfig{Description: "x"}); err == nil {
		t.Fatal("expected an error for an unparseable project.yaml")
	}
	got, _ := os.ReadFile(path)
	if string(got) != broken {
		t.Errorf("file was modified: %q", got)
	}
}

func TestMerge_AgentAPIFields(t *testing.T) {
	dst := Config{Agents: map[string]AgentConfig{"coder": {Runner: "openai", BaseURL: "http://a", APIKeyEnv: "KEY_A"}}}
	merge(&dst, Config{Agents: map[string]AgentConfig{"coder": {Model: "m"}}})
	ac := dst.Agents["coder"]
	if ac.BaseURL != "http://a" || ac.APIKeyEnv != "KEY_A" || ac.Model != "m" {
		t.Fatalf("empty layer must keep values: %+v", ac)
	}
	merge(&dst, Config{Agents: map[string]AgentConfig{"coder": {BaseURL: "http://b", APIKeyEnv: "KEY_B"}}})
	ac = dst.Agents["coder"]
	if ac.BaseURL != "http://b" || ac.APIKeyEnv != "KEY_B" {
		t.Fatalf("override failed: %+v", ac)
	}
}

func TestProfileKeepsAPIFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	agents := map[string]AgentConfig{"coder": {Runner: "openai", Model: "m", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"}}
	if err := SaveProfile("api", agents); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProfile("api")
	if err != nil {
		t.Fatal(err)
	}
	if ac := p.Agents["coder"]; ac.BaseURL != "https://openrouter.ai/api/v1" || ac.APIKeyEnv != "OPENROUTER_API_KEY" {
		t.Fatalf("profile lost api fields: %+v", ac)
	}
	root := t.TempDir()
	if err := ApplyProfile(root, "api"); err != nil {
		t.Fatal(err)
	}
	if ac := LoadProject(root).Agents["coder"]; ac.BaseURL == "" || ac.APIKeyEnv == "" || ac.Runner != "openai" {
		t.Fatalf("project lost api fields: %+v", ac)
	}
}

func TestMerge_UIIcons(t *testing.T) {
	var dst Config
	merge(&dst, Config{UI: UIConfig{Icons: "ascii"}})
	if dst.UI.Icons != "ascii" {
		t.Fatalf("icons = %q, want ascii", dst.UI.Icons)
	}
	merge(&dst, Config{})
	if dst.UI.Icons != "ascii" {
		t.Errorf("empty layer changed icons: %q", dst.UI.Icons)
	}
	merge(&dst, Config{UI: UIConfig{Icons: "nerd"}})
	if dst.UI.Icons != "nerd" {
		t.Errorf("later layer should override: %q", dst.UI.Icons)
	}
}

func TestMerge_UITheme(t *testing.T) {
	var dst Config
	merge(&dst, Config{UI: UIConfig{Theme: "light"}})
	merge(&dst, Config{})
	if dst.UI.Theme != "light" {
		t.Fatalf("empty layer changed theme: %q", dst.UI.Theme)
	}
	merge(&dst, Config{UI: UIConfig{Theme: "dark"}})
	if dst.UI.Theme != "dark" {
		t.Errorf("later layer should override: %q", dst.UI.Theme)
	}
}

func TestLoad_PricingAndBudgetMerge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, GlobalDir)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	global := `
pricing:
  "deepseek-*": {input_per_mtok: 0.27, output_per_mtok: 1.1}
  "gpt-5": {input_per_mtok: 1.25, output_per_mtok: 10}
workspace:
  budget_usd: 5
  budget_warn_pct: 70
`
	if err := os.WriteFile(filepath.Join(globalDir, GlobalFilename), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	wsDir := filepath.Join(projectDir, GlobalDir)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := `
pricing:
  "gpt-5": {input_per_mtok: 2, output_per_mtok: 20}
workspace:
  budget_usd: 12.5
`
	if err := os.WriteFile(filepath.Join(wsDir, ProjectFilename), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Pricing["gpt-5"]; got.InputPerMTok != 2 || got.OutputPerMTok != 20 {
		t.Errorf("project must override per key, got %+v", got)
	}
	if got := cfg.Pricing["deepseek-*"]; got.OutputPerMTok != 1.1 {
		t.Errorf("global key must survive, got %+v", got)
	}
	if cfg.Workspace.BudgetUSD != 12.5 || cfg.Workspace.BudgetWarnPct != 70 {
		t.Errorf("budget %v warn %v", cfg.Workspace.BudgetUSD, cfg.Workspace.BudgetWarnPct)
	}
}

func TestMerge_UITmux(t *testing.T) {
	var dst Config
	merge(&dst, Config{UI: UIConfig{Tmux: true}})
	if !dst.UI.Tmux {
		t.Fatal("tmux not set")
	}
	merge(&dst, Config{})
	if !dst.UI.Tmux {
		t.Error("empty layer cleared tmux")
	}
}

func TestMerge_WorkspaceAutonomy(t *testing.T) {
	var dst Config
	merge(&dst, Config{Workspace: WorkspaceConfig{Autonomy: 2, MaxAutoFixes: 4}})
	if dst.Workspace.Autonomy != 2 || dst.Workspace.MaxAutoFixes != 4 {
		t.Fatalf("autonomy %d, max auto fixes %d", dst.Workspace.Autonomy, dst.Workspace.MaxAutoFixes)
	}
	merge(&dst, Config{})
	if dst.Workspace.Autonomy != 2 || dst.Workspace.MaxAutoFixes != 4 {
		t.Error("an empty layer reset the autonomy")
	}
	merge(&dst, Config{Workspace: WorkspaceConfig{Autonomy: 3}})
	if dst.Workspace.Autonomy != 3 || dst.Workspace.MaxAutoFixes != 4 {
		t.Errorf("autonomy %d, max auto fixes %d", dst.Workspace.Autonomy, dst.Workspace.MaxAutoFixes)
	}
}

func TestMerge_WorkspaceCoordinator(t *testing.T) {
	var dst Config
	if dst.Workspace.Coordinator {
		t.Fatal("the coordinator is off by default")
	}
	merge(&dst, Config{Workspace: WorkspaceConfig{Coordinator: true}})
	if !dst.Workspace.Coordinator {
		t.Fatal("the coordinator was not merged")
	}
	merge(&dst, Config{})
	if !dst.Workspace.Coordinator {
		t.Error("an empty layer reset the coordinator")
	}
}

func TestMerge_ProjectAllowedCommands(t *testing.T) {
	dst := DefaultConfig()
	merge(&dst, Config{Project: ProjectConfig{AllowedCommands: []string{"dpkg -l"}}})
	merge(&dst, Config{}) // empty layer keeps the list
	if len(dst.Project.AllowedCommands) != 1 || dst.Project.AllowedCommands[0] != "dpkg -l" {
		t.Fatalf("got %v", dst.Project.AllowedCommands)
	}
	merge(&dst, Config{Project: ProjectConfig{AllowedCommands: []string{"/opt/cmake"}}})
	if len(dst.Project.AllowedCommands) != 1 || dst.Project.AllowedCommands[0] != "/opt/cmake" {
		t.Fatalf("got %v", dst.Project.AllowedCommands)
	}
}

func TestRoutingMerge(t *testing.T) {
	dst := Config{Routing: RoutingConfig{
		Aliases:  map[string]string{"A": "claude/a", "B": "claude/b"},
		Weights:  RoutingWeights{Intelligence: 1},
		Fallback: map[string]FallbackTier{"x": {Intelligence: 1}},
	}}
	merge(&dst, Config{Routing: RoutingConfig{
		Aliases:  map[string]string{"B": "dsh/b2", "C": "dsh/c"},
		Weights:  RoutingWeights{Cost: 5},
		Fallback: map[string]FallbackTier{"y": {Intelligence: 2}},
	}})
	if dst.Routing.Aliases["A"] != "claude/a" || dst.Routing.Aliases["B"] != "dsh/b2" || dst.Routing.Aliases["C"] != "dsh/c" {
		t.Fatalf("aliases: %v", dst.Routing.Aliases)
	}
	if dst.Routing.Weights != (RoutingWeights{Cost: 5}) {
		t.Fatalf("weights: %+v", dst.Routing.Weights)
	}
	if len(dst.Routing.Fallback) != 2 {
		t.Fatalf("fallback: %v", dst.Routing.Fallback)
	}
	merge(&dst, Config{})
	if dst.Routing.Weights.Cost != 5 {
		t.Fatal("an empty layer must not reset the weights")
	}
}

func TestMerge_WorkspaceLimitAutoRetry(t *testing.T) {
	var dst Config
	merge(&dst, Config{})
	if dst.Workspace.LimitAutoRetry != nil {
		t.Fatal("unset setting became set")
	}
	off := false
	merge(&dst, Config{Workspace: WorkspaceConfig{LimitAutoRetry: &off}})
	if dst.Workspace.LimitAutoRetry == nil || *dst.Workspace.LimitAutoRetry {
		t.Fatal("false was not merged")
	}
	merge(&dst, Config{})
	if dst.Workspace.LimitAutoRetry == nil || *dst.Workspace.LimitAutoRetry {
		t.Fatal("an unset layer cleared the setting")
	}
}
