package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/artifacts"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func newTestConfig() config.Config {
	return config.Config{
		Project: config.ProjectConfig{
			Name:     "test-project",
			Language: "go",
		},
		Agents: map[string]config.AgentConfig{
			"pm":    {Runner: "codex", Model: "gpt-5.4"},
			"qa":    {Runner: "codex", Model: "gpt-5.3-codex"},
			"coder": {Runner: "claude", Model: "sonnet"},

			"ux_reviewer": {Runner: "codex", Model: "gpt-5.4"},
			"security":    {Runner: "claude", Model: "opus"},
		},
	}
}

func TestNewHomeModel_CachesProjectInfo(t *testing.T) {
	root := t.TempDir()
	cfg := newTestConfig()

	m := NewHomeModel(cfg, root)

	if m.cachedProject != "test-project" {
		t.Errorf("expected project name 'test-project', got %q", m.cachedProject)
	}
	if m.cachedLanguage != "go" {
		t.Errorf("expected language 'go', got %q", m.cachedLanguage)
	}
}

func TestNewHomeModel_DefaultProjectName(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"pm": {Runner: "codex", Model: "gpt-5"},
		},
	}

	m := NewHomeModel(cfg, root)

	expected := filepath.Base(root)
	if m.cachedProject != expected {
		t.Errorf("expected project name %q (from dir), got %q", expected, m.cachedProject)
	}
}

func TestNewHomeModel_UnnamedProject(t *testing.T) {
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"pm": {Runner: "codex", Model: "gpt-5"},
		},
	}

	m := NewHomeModel(cfg, "")

	if m.cachedProject != "(unnamed)" {
		t.Errorf("expected '(unnamed)', got %q", m.cachedProject)
	}
}

func TestNewHomeModel_RootSlash(t *testing.T) {
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"pm": {Runner: "codex", Model: "gpt-5"},
		},
	}

	m := NewHomeModel(cfg, "/")

	if m.cachedProject != "(unnamed)" {
		t.Errorf("expected '(unnamed)' for root '/', got %q", m.cachedProject)
	}
}

func TestResolveLanguageFromRoot_ConfigPriority(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		Project: config.ProjectConfig{Language: "rust"},
	}

	// Place a go.mod to verify config takes priority over filesystem detection.
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test"), 0o644)

	lang := resolveLanguageFromRoot(root, cfg)
	if lang != "rust" {
		t.Errorf("expected 'rust' from config, got %q", lang)
	}
}

func TestResolveLanguageFromRoot_DetectsFromFilesystem(t *testing.T) {
	tests := []struct {
		file     string
		expected string
	}{
		{"go.mod", "go"},
		{"package.json", "javascript"},
		{"Cargo.toml", "rust"},
		{"setup.py", "python"},
		{"pyproject.toml", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"Gemfile", "ruby"},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			root := t.TempDir()
			_ = os.WriteFile(filepath.Join(root, tt.file), []byte(""), 0o644)

			lang := resolveLanguageFromRoot(root, config.Config{})
			if lang != tt.expected {
				t.Errorf("expected %q for %s, got %q", tt.expected, tt.file, lang)
			}
		})
	}
}

func TestResolveLanguageFromRoot_EmptyWhenNoIndicator(t *testing.T) {
	root := t.TempDir()
	lang := resolveLanguageFromRoot(root, config.Config{})
	if lang != "" {
		t.Errorf("expected empty language, got %q", lang)
	}
}

func TestHomeModel_QuitConfirmation(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	// Press 'q' to trigger quit confirmation.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !m.confirmQuit {
		t.Error("expected confirmQuit to be true after pressing 'q'")
	}

	// Press any other key to cancel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.confirmQuit {
		t.Error("expected confirmQuit to be false after cancel")
	}
}

func TestHomeModel_QuitConfirmationViaMenu(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	// Navigate to Quit and press Enter.
	m.cursor = len(m.items) - 1 // last item = Quit
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.confirmQuit {
		t.Error("expected confirmQuit after selecting Quit from menu")
	}
}

func TestHomeModel_NumberShortcuts(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	// '6' should trigger quit confirmation.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	if !m.confirmQuit {
		t.Error("expected confirmQuit after pressing '6'")
	}
}

// containsText strips ANSI codes and checks for substring presence.
func containsText(s, substr string) bool {
	return strings.Contains(stripAnsi(s), substr)
}

func TestIsValidProjectRoot_RejectsEmptyRoot(t *testing.T) {
	if isValidProjectRoot("") {
		t.Error("empty root should be invalid")
	}
}

func TestIsValidProjectRoot_RejectsHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	if isValidProjectRoot(home) {
		t.Error("home directory should be invalid project root")
	}
}

func TestIsValidProjectRoot_AcceptsProjectWithGit(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	if !isValidProjectRoot(root) {
		t.Error("directory with .git should be valid")
	}
}

func TestIsValidProjectRoot_AcceptsProjectWithGoMod(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test"), 0o644)
	if !isValidProjectRoot(root) {
		t.Error("directory with go.mod should be valid")
	}
}

func TestIsValidProjectRoot_AcceptsProjectWithDECSR(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".decsr"), 0o755)
	if !isValidProjectRoot(root) {
		t.Error("directory with .decsr should be valid")
	}
}

func TestIsValidProjectRoot_RejectsBareDir(t *testing.T) {
	root := t.TempDir()
	if isValidProjectRoot(root) {
		t.Error("bare temp directory should be invalid")
	}
}

func TestHomeModel_ProjectValid_WithMarker(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	m := NewHomeModel(newTestConfig(), root)
	if !m.projectValid {
		t.Error("projectValid should be true for directory with .git")
	}
}

func TestHomeModel_ProjectValid_WithoutMarker(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	if m.projectValid {
		t.Error("projectValid should be false for bare temp directory")
	}
}

func TestHomeModel_DisabledAction_RedirectsToOpenProject(t *testing.T) {
	root := t.TempDir() // no project markers → projectValid=false
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	// Press '1' (Run Pipeline shortcut) — should redirect to Open Project.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if cmd == nil {
		t.Fatal("expected a command from disabled Run action")
	}
	msg := cmd()
	selected, ok := msg.(homeSelectedMsg)
	if !ok {
		t.Fatal("expected homeSelectedMsg")
	}
	if selected.action != homeActionOpenProject {
		t.Errorf("expected redirect to homeActionOpenProject, got %d", selected.action)
	}
}

func TestHomeModel_EnabledAction_RunsPipeline(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	// Press '1' (New Task shortcut) — should fire homeActionNewTask.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if cmd == nil {
		t.Fatal("expected a command from enabled Run action")
	}
	msg := cmd()
	selected, ok := msg.(homeSelectedMsg)
	if !ok {
		t.Fatal("expected homeSelectedMsg")
	}
	if selected.action != homeActionNewTask {
		t.Errorf("expected homeActionNewTask, got %d", selected.action)
	}
}

// stripAnsi removes ANSI escape sequences for easier text assertions.
func stripAnsi(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 'A' || s[j] > 'Z') && (s[j] < 'a' || s[j] > 'z') {
				j++
			}
			if j < len(s) {
				j++ // skip the final letter
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func TestNewHomeModel_OffersResumeProjectWhenArtifactsExist(t *testing.T) {
	root := t.TempDir()
	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteFile(artifacts.SummaryFile, []byte("partial progress")); err != nil {
		t.Fatal(err)
	}

	m := NewHomeModel(newTestConfig(), root)

	found := false
	for _, it := range m.items {
		if it.action == homeActionResumeProject {
			found = true
		}
	}
	if !found {
		t.Error("expected a Resume Project menu item when .decsr artifacts exist")
	}
}

func TestNewHomeModel_NoResumeProjectWithoutArtifacts(t *testing.T) {
	m := NewHomeModel(newTestConfig(), t.TempDir())
	for _, it := range m.items {
		if it.action == homeActionResumeProject {
			t.Error("did not expect Resume Project without any artifacts")
		}
	}
}

func TestBlockGlyphs_IncludesLogoWord(t *testing.T) {
	for _, ch := range "DECSR" {
		if _, ok := blockGlyphs[ch]; !ok {
			t.Errorf("blockGlyphs is missing %q; the TUI logo would drop a letter", ch)
		}
	}
}

func TestResolveLanguageFromRootDetects(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"CMakeLists.txt", "cpp"},
		{"App.sln", "csharp"},
		{"", ""},
	}
	for _, tc := range tests {
		root := t.TempDir()
		if tc.file != "" {
			if err := os.WriteFile(filepath.Join(root, tc.file), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := resolveLanguageFromRoot(root, config.DefaultConfig()); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.file, got, tc.want)
		}
	}
}

func TestStartupHomeWorkspaceSetsResult(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { _ = RemoveRecentProject(root) })
	m := newStartupModel(root, filepath.Join(root, ".decsr", "requirements.md"), newTestConfig())
	updated, cmd := m.Update(homeSelectedMsg{action: homeActionWorkspace})
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	s := updated.(startupModel)
	if !s.workspace || s.chatMode || s.reqPath != "" {
		t.Fatalf("workspace=%v chat=%v req=%q", s.workspace, s.chatMode, s.reqPath)
	}
}

func TestHomeWorkspaceKey(t *testing.T) {
	root := t.TempDir()
	_ = createProjectMarkers(root)
	m := NewHomeModel(newTestConfig(), root)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if cmd == nil {
		t.Fatal("expected a command")
	}
	if msg, ok := cmd().(homeSelectedMsg); !ok || msg.action != homeActionWorkspace {
		t.Fatalf("msg = %#v", cmd())
	}
}

func TestHomeHelpListsWorkspaceKey(t *testing.T) {
	var found bool
	for _, s := range homeHelp() {
		for _, k := range s.keys {
			if k.key == "w" && strings.Contains(strings.ToLower(k.label), "workspace") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("home help does not list the w key for the workspace")
	}
}
