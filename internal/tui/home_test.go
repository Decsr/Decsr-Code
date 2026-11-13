package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/charmbracelet/x/ansi"

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

func TestHomeModel_ShortenProjectPath_ExactHome(t *testing.T) {
	m := HomeModel{}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	result := m.shortenProjectPath(home)
	if result != "~" {
		t.Errorf("expected '~' for home dir, got %q", result)
	}
}

func TestHomeModel_ShortenProjectPath_SimilarPrefix(t *testing.T) {
	m := HomeModel{}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	// Path that starts with home dir name but is a sibling, not a child.
	sibling := home + "extra/project"
	result := m.shortenProjectPath(sibling)
	if result != sibling {
		t.Errorf("sibling path should not be shortened, got %q", result)
	}
}

func TestNewHomeModel_MenuItems(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)

	if len(m.items) != 8 {
		t.Fatalf("expected 8 menu items, got %d", len(m.items))
	}

	expectedActions := []homeAction{
		homeActionWorkspace,
		homeActionNewTask,
		homeActionOpenProject,
		homeActionGlobalSettings,
		homeActionModelMemory,
		homeActionSetup,
		homeActionClean,
		homeActionQuit,
	}
	for i, expected := range expectedActions {
		if m.items[i].action != expected {
			t.Errorf("item[%d]: expected action %d, got %d", i, expected, m.items[i].action)
		}
	}
}

func TestResolveOverrides_AllAgents(t *testing.T) {
	cfg := newTestConfig()

	overrides := resolveOverrides(cfg.Agents, "codex", "gpt-5.3-codex", "default")

	expectedRoles := []string{"pm", "coder", "qa", "ux_reviewer", "security"}
	if len(overrides) != len(expectedRoles) {
		t.Fatalf("expected %d overrides, got %d", len(expectedRoles), len(overrides))
	}

	for i, role := range expectedRoles {
		if overrides[i].role != role {
			t.Errorf("override[%d]: expected role %q, got %q", i, role, overrides[i].role)
		}
	}
}

func TestResolveOverrides_PreservesExplicitValues(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":       {Runner: "codex", Model: "gpt-5.4"},
		"coder":    {Runner: "claude", Model: "sonnet", Effort: "high"},
		"qa":       {Runner: "codex", Model: "gpt-5.3-codex", Effort: "default"},
		"security": {Runner: "claude", Model: "opus"},
	}

	overrides := resolveOverrides(agents, "codex", "gpt-5.3-codex", "default")

	expected := map[string]struct{ runner, model, effort string }{
		"pm":       {"codex", "gpt-5.4", ""},
		"coder":    {"claude", "sonnet", "high"},
		"qa":       {"codex", "gpt-5.3-codex", ""},
		"security": {"claude", "opus", ""},
	}

	for _, ov := range overrides {
		exp, ok := expected[ov.role]
		if !ok {
			continue
		}
		if ov.runner != exp.runner {
			t.Errorf("agent %q: expected runner %q, got %q", ov.role, exp.runner, ov.runner)
		}
		if ov.model != exp.model {
			t.Errorf("agent %q: expected model %q, got %q", ov.role, exp.model, ov.model)
		}
		if ov.effort != exp.effort {
			t.Errorf("agent %q: expected effort %q, got %q", ov.role, exp.effort, ov.effort)
		}
	}
}

func TestResolveOverrides_FillsDefaultsForEmpty(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":    {Runner: "", Model: ""},
		"coder": {Runner: "claude", Model: ""},
	}

	overrides := resolveOverrides(agents, "codex", "gpt-5", "default")

	for _, ov := range overrides {
		switch ov.role {
		case "pm":
			if ov.runner != "codex" || ov.model != "gpt-5" {
				t.Errorf("pm: expected codex/gpt-5, got %s/%s", ov.runner, ov.model)
			}
		case "coder":
			if ov.runner != "claude" || ov.model != "gpt-5" {
				t.Errorf("coder: expected claude/gpt-5, got %s/%s", ov.runner, ov.model)
			}
		}
	}
}

func TestResolveOverrides_SkipsMissingRoles(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":    {Runner: "codex", Model: "gpt-5"},
		"coder": {Runner: "claude", Model: "sonnet"},
	}

	overrides := resolveOverrides(agents, "codex", "gpt-5", "default")

	if len(overrides) != 2 {
		t.Fatalf("expected 2 overrides, got %d", len(overrides))
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

func TestHomeModel_CursorNavigation(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	if m.cursor != 0 {
		t.Fatalf("initial cursor should be 0, got %d", m.cursor)
	}

	// Move down.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 after 'j', got %d", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("expected cursor 2 after down, got %d", m.cursor)
	}

	// Move up.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 after 'k', got %d", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("expected cursor 0 after up, got %d", m.cursor)
	}

	// Don't go below 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("cursor should stay at 0, got %d", m.cursor)
	}

	// Don't go past last item.
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != len(m.items)-1 {
		t.Errorf("cursor should be at last item (%d), got %d", len(m.items)-1, m.cursor)
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

func TestHomeModel_WindowResize(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)

	m, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	if m.width != 200 || m.height != 50 {
		t.Errorf("expected 200x50, got %dx%d", m.width, m.height)
	}
	if !m.ready {
		t.Error("viewport should be ready after WindowSizeMsg")
	}
	if m.scrollX != 0 {
		t.Error("scrollX should reset on resize")
	}
}

func TestHomeModel_HorizontalScroll(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	// Scroll right.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.scrollX != horizontalScrollStep {
		t.Errorf("expected scrollX=%d after right, got %d", horizontalScrollStep, m.scrollX)
	}

	// Scroll left.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.scrollX != 0 {
		t.Errorf("expected scrollX=0 after left, got %d", m.scrollX)
	}

	// Don't go negative.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.scrollX != 0 {
		t.Errorf("scrollX should not go negative, got %d", m.scrollX)
	}
}

func TestHomeModel_RenderInfoCard_ShowsAllOverrides(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	cfg := newTestConfig()
	m := NewHomeModel(cfg, root)
	m.width, m.height = 120, 40
	m.syncViewport()

	content := m.renderInfoCard(100)

	expectedEntries := []struct {
		role   string
		runner string
		model  string
	}{
		{"pm", "codex", "gpt-5.4"},
		{"qa", "codex", "gpt-5.3-codex"},
		{"coder", "claude", "sonnet"},

		{"ux_reviewer", "codex", "gpt-5.4"},
		{"security", "claude", "opus"},
	}

	for _, entry := range expectedEntries {
		display := entry.runner + " / " + entry.model
		if !containsText(content, display) {
			t.Errorf("info card missing display %q for agent %s", display, entry.role)
		}
	}
}

func TestHomeModel_WorkspaceStatus_NoWorkspace(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)

	status := m.workspaceStatus()
	if status != "⊘ no workspace" {
		t.Errorf("expected '⊘ no workspace', got %q", status)
	}
}

func TestHomeModel_WorkspaceStatus_EmptyWorkspace(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".decsr"), 0o755)
	m := NewHomeModel(newTestConfig(), root)

	status := m.workspaceStatus()
	if status != "◇ workspace ready" {
		t.Errorf("expected '◇ workspace ready', got %q", status)
	}
}

func TestHomeModel_WorkspaceStatus_WithArtifacts(t *testing.T) {
	root := t.TempDir()
	wsDir := filepath.Join(root, ".decsr")
	_ = os.MkdirAll(wsDir, 0o755)
	_ = os.WriteFile(filepath.Join(wsDir, "requirements.md"), []byte("# req"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "implementation_plan.md"), []byte("# plan"), 0o644)

	m := NewHomeModel(newTestConfig(), root)
	status := m.workspaceStatus()

	if !containsText(status, "requirements") {
		t.Error("expected status to mention 'requirements'")
	}
	if !containsText(status, "plan") {
		t.Error("expected status to mention 'plan'")
	}
}

func TestHomeModel_ShortenPath(t *testing.T) {
	root := "/home/user/project"
	m := HomeModel{root: root}

	// Relative path.
	short := m.shortenPath("/home/user/project/src/main.go")
	if short != "src/main.go" {
		t.Errorf("expected 'src/main.go', got %q", short)
	}

	// Very long path should be truncated.
	longPath := root + "/a/very/deeply/nested/directory/structure/that/exceeds/fifty/characters/file.go"
	short = m.shortenPath(longPath)
	if len(short) > 53 { // ~50 visible chars + leading '…' (multi-byte)
		t.Errorf("expected shortened path, got %q (len=%d)", short, len(short))
	}
}

func TestHomeModel_ViewNotReady(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	// Don't trigger WindowSizeMsg — viewport not ready.

	view := m.View()
	if view == "" {
		t.Error("View should return non-empty string even when not ready")
	}
}

func TestHomeModel_ViewReady(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	view := m.View()
	if view == "" {
		t.Error("View should return non-empty string when ready")
	}
	if !containsText(view, "DECSR") {
		t.Error("View should contain 'D E C S R'")
	}
}

func TestHomeModel_RenderContent_QuitOverlay(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()
	m.confirmQuit = true

	content := m.renderContent()
	if !containsText(content, "QUIT") {
		t.Error("quit overlay should contain 'QUIT'")
	}
}

func TestHomeModel_RenderLogo_Wide(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)

	logo := m.renderLogo(80)
	if !containsText(logo, "multi-agent") {
		t.Error("wide logo should contain tagline")
	}
}

func TestHomeModel_RenderLogo_Compact(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)

	logo := m.renderLogo(40)
	if !containsText(logo, "DECSR") {
		t.Error("compact logo should contain 'DECSR'")
	}
}

func TestHomeModel_RenderHistory_Empty(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.recentProjects = nil

	hist := m.renderHistory(100)
	if hist != "" {
		t.Errorf("expected empty history, got %q", hist)
	}
}

func TestHomeModel_RenderHistory_WithEntries(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.recentProjects = []RecentProject{
		{Path: "/home/user/project-a", Name: "project-a"},
		{Path: "/home/user/project-b", Name: "project-b"},
		{Path: "/home/user/project-c", Name: "project-c"},
		{Path: "/home/user/project-d", Name: "project-d"},
		{Path: "/home/user/project-e", Name: "project-e"},
		{Path: "/home/user/project-f", Name: "project-f"},
		{Path: "/home/user/project-g", Name: "project-g"},
	}

	hist := m.renderHistory(100)
	if !containsText(hist, "project-a") {
		t.Error("history should contain first entry")
	}
	if !containsText(hist, "project-c") {
		t.Error("history should contain third entry")
	}
	if containsText(hist, "project-f") {
		t.Error("history should not show more than 5 entries")
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

func TestHomeModel_InfoCard_NoProject(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	card := m.renderInfoCard(100)
	if !containsText(card, "No project selected") {
		t.Error("info card should show 'No project selected' for invalid project")
	}
}

func TestHomeModel_Menu_DisabledDescription(t *testing.T) {
	root := t.TempDir()
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()

	menu := m.renderMenu(100)
	if !containsText(menu, "Open a project first") {
		t.Error("disabled menu items should show 'Open a project first'")
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

// uniformAgentsConfig puts all six agents on one runner, model and effort.
func uniformAgentsConfig(model string) config.Config {
	agents := map[string]config.AgentConfig{}
	for _, role := range []string{"pm", "coder", "coder_fixer", "qa", "ux_reviewer", "security"} {
		agents[role] = config.AgentConfig{Runner: "opencode", Model: model, Effort: "max"}
	}
	return config.Config{Project: config.ProjectConfig{Name: "card"}, Agents: agents}
}

func cardTestModel(t *testing.T, cfg config.Config) HomeModel {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewHomeModel(cfg, root)
	m.width, m.height = 120, 40
	return m
}

func TestInfoCardCollapsesUniformAgents(t *testing.T) {
	m := cardTestModel(t, uniformAgentsConfig("opencode-go/deepseek-v4.1-flash"))
	card := stripAnsi(m.renderInfoCard(100))
	if strings.Contains(card, "CODER_FIXER") {
		t.Error("uniform agents should not be listed one per row")
	}
	found := false
	for _, l := range strings.Split(card, "\n") {
		if strings.Contains(l, "AGENTS") && strings.Contains(l, "all 6") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected one AGENTS row with 'all 6':\n%s", card)
	}
}

func TestInfoCardListsDifferingAgents(t *testing.T) {
	cfg := uniformAgentsConfig("opencode-go/deepseek-v4.1-flash")
	cfg.Agents["coder"] = config.AgentConfig{Runner: "claude", Model: "sonnet", Effort: "max"}
	m := cardTestModel(t, cfg)
	card := stripAnsi(m.renderInfoCard(100))
	if strings.Contains(card, "all 6") {
		t.Error("differing agents must not be collapsed")
	}
	for _, role := range []string{"PM", "CODER ", "CODER_FIXER", "QA", "UX_REVIEWER", "SECURITY"} {
		n := 0
		for _, l := range strings.Split(card, "\n") {
			if strings.Contains(l, "■ "+role) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("role %q: want one row, got %d", role, n)
		}
	}
}

func TestInfoCardNeverWraps(t *testing.T) {
	long := strings.Repeat("m", 60)
	cfg := uniformAgentsConfig(long)
	cfg.Agents["coder"] = config.AgentConfig{Runner: "claude", Model: long, Effort: "max"}
	for _, uniform := range []bool{false, true} {
		c := cfg
		if uniform {
			c = uniformAgentsConfig(long)
		}
		m := cardTestModel(t, c)
		rows := len(strings.Split(m.renderInfoCard(1000), "\n")) // nothing wraps this wide
		for _, cw := range []int{60, 80, 100, 120} {
			cardW := cw/2 - 2
			if cw < 90 {
				cardW = cw - 4
			}
			lines := strings.Split(m.renderInfoCard(cw), "\n")
			if len(lines) != rows {
				t.Errorf("uniform=%v cw=%d: %d lines, want %d", uniform, cw, len(lines), rows)
			}
			for _, l := range lines {
				if ansi.StringWidth(l) > cardW+2 {
					t.Errorf("uniform=%v cw=%d: line width %d > %d", uniform, cw, ansi.StringWidth(l), cardW+2)
				}
			}
			if cw == 60 && !strings.Contains(stripAnsi(m.renderInfoCard(cw)), "…") {
				t.Errorf("uniform=%v: long values should end with an ellipsis", uniform)
			}
		}
	}
}

func TestInfoCardLanguageLabel(t *testing.T) {
	langRow := func(m HomeModel) string {
		for _, l := range strings.Split(stripAnsi(m.renderInfoCard(100)), "\n") {
			if strings.Contains(l, "LANG") {
				return l
			}
		}
		return ""
	}
	cfg := uniformAgentsConfig("x")

	m := cardTestModel(t, cfg)
	if err := os.WriteFile(filepath.Join(m.root, "CMakeLists.txt"), []byte("project(x)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = NewHomeModel(cfg, m.root)
	if l := langRow(m); !strings.Contains(l, "cpp") || !strings.Contains(l, "auto") {
		t.Errorf("detected: %q", l)
	}

	cfg.Project.Language = "java"
	m = NewHomeModel(cfg, m.root)
	if l := langRow(m); !strings.Contains(l, "java") || strings.Contains(l, "auto") {
		t.Errorf("configured: %q", l)
	}

	cfg.Project.Language = ""
	m = cardTestModel(t, cfg)
	if l := langRow(m); !strings.Contains(l, "not detected") {
		t.Errorf("empty dir: %q", l)
	}
}

func TestHomeFooterHasNoScrollPercent(t *testing.T) {
	m := cardTestModel(t, uniformAgentsConfig("x"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 10})
	if pct := regexp.MustCompile(`\d+%`); pct.MatchString(stripAnsi(m.renderFooter())) {
		t.Errorf("footer shows a scroll percent: %q", stripAnsi(m.renderFooter()))
	}
}

func TestHomeModel_RenderHistory_TruncatesToWidth(t *testing.T) {
	m := NewHomeModel(newTestConfig(), t.TempDir())
	m.recentProjects = []RecentProject{
		{Path: "/very/long/" + strings.Repeat("segment/", 30), Name: "long-project"},
	}
	for _, w := range []int{60, 80} {
		for _, l := range strings.Split(m.renderHistory(w), "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d: line is %d wide", w, ansi.StringWidth(l))
			}
		}
	}
	m.width, m.height = 100, 40
	m.syncViewport()
	for _, l := range strings.Split(m.renderContent(), "\n") {
		if ansi.StringWidth(l) > m.width {
			t.Fatalf("content line is %d wide, wider than the %d terminal", ansi.StringWidth(l), m.width)
		}
	}
}

func TestHomeModel_RenderLogo_CenteredAndAligned(t *testing.T) {
	m := NewHomeModel(newTestConfig(), t.TempDir())
	lines := strings.Split(m.renderLogo(100), "\n")
	for _, l := range lines {
		if got := ansi.StringWidth(l); got != 100 {
			t.Fatalf("logo line is %d wide, want 100: %q", got, stripAnsi(l))
		}
	}
	first := stripAnsi(lines[0])
	if strings.TrimLeft(first, " ") == first {
		t.Error("logo block should be indented to the centre")
	}
}

func TestHomeModel_RenderFooter_KeepsHelpAndQuit(t *testing.T) {
	for _, w := range []int{60, 80, 120} {
		m := NewHomeModel(newTestConfig(), t.TempDir())
		m.width = w
		out := stripAnsi(m.renderFooter())
		if ansi.StringWidth(out) > w {
			t.Errorf("width %d: footer is %d wide", w, ansi.StringWidth(out))
		}
		for _, want := range []string{"help", "quit"} {
			if !strings.Contains(out, want) {
				t.Errorf("width %d: footer lost %q: %q", w, want, out)
			}
		}
	}
	m := NewHomeModel(newTestConfig(), t.TempDir())
	m.width = 120
	if out := stripAnsi(m.renderFooter()); !strings.Contains(out, "reset") || !strings.Contains(out, "setup") {
		t.Errorf("wide footer should keep every hint: %q", out)
	}
	m.width = 60
	if out := stripAnsi(m.renderFooter()); strings.Contains(out, "reset") {
		t.Errorf("narrow footer should drop reset first: %q", out)
	}
}

func TestHomeModel_InfoCard_UniformUnsetAgents(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".decsr"), 0o755)
	m := NewHomeModel(config.Config{}, root)
	m.cachedOverrides = []agentOverride{{role: "pm", runner: "(not set)", model: "(not set)"}, {role: "coder", runner: "(not set)", model: "(not set)"}}
	out := stripAnsi(m.renderInfoCard(60))
	if !strings.Contains(out, "all 2 · inherits global") || strings.Contains(out, "(not set) / (not set)") {
		t.Errorf("unexpected AGENTS row:\n%s", out)
	}
}

func TestHomeMenuHasWorkspaceFirst(t *testing.T) {
	m := NewHomeModel(newTestConfig(), t.TempDir())
	if len(m.items) < 2 {
		t.Fatalf("too few items: %d", len(m.items))
	}
	first, second := m.items[0], m.items[1]
	if first.label != "Workspace" || first.key != "w" || first.desc != "Chat, split into nodes, assign agents" {
		t.Fatalf("first item = %+v", first)
	}
	if first.action != homeActionWorkspace {
		t.Fatalf("first action = %d", first.action)
	}
	if strings.ToUpper(second.label) != "CLASSIC PIPELINE" || second.action != homeActionNewTask {
		t.Fatalf("second item = %+v", second)
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
