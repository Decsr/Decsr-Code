package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestSetupRender_IncludesEffortCard(t *testing.T) {
	m := NewSetupModel("claude", "sonnet", "high", map[string]config.AgentConfig{
		"coder": {Runner: "claude", Model: "sonnet"},
	})
	m.globalOnly = true
	m.width, m.height = 100, 40

	content := m.renderContent()
	if !strings.Contains(content, "EFFORT") {
		t.Fatalf("expected effort card in global setup content:\n%s", content)
	}
	if !strings.Contains(content, "Reasoning effort") {
		t.Fatalf("expected effort explanation in setup content:\n%s", content)
	}
}

func TestSetupRender_ProjectHidesGlobalDefaults(t *testing.T) {
	m := NewSetupModelWithOverrides("claude", "sonnet", "high", "go", "proj", nil)
	m.width, m.height = 100, 40

	content := m.renderContent()
	if strings.Contains(content, "◆ EFFORT") {
		t.Fatalf("project setup must not render the global effort card:\n%s", content)
	}
	if !strings.Contains(content, "configured in Global Settings") {
		t.Fatalf("expected a pointer to Global Settings:\n%s", content)
	}
}

func TestSetupRender_EffortStepDoesNotPanic(t *testing.T) {
	m := NewSetupModelWithOverrides("claude", "sonnet", "high", "go", "proj", nil)
	m.width, m.height = 100, 40
	m.agentRoles = []string{"coder"}
	m.agentCursor = 0
	m.agentEditing = true
	m.agentEditStep = 2
	m.agentEditProv = providerClaude
	m.claudeModels = []string{"sonnet"}
	m.agentEditIdx = 0
	m.agentEffortOptions = []string{"low", "medium", "high"}
	m.agentEffortFor = "claude/sonnet"
	m.agentEditEffortIdx = 2

	content := m.renderContent()
	if !strings.Contains(content, "Select effort for coder") {
		t.Fatalf("expected effort picker in setup content:\n%s", content)
	}
}

func TestSetupEffortFlow_GlobalSavesAndStays(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	baseAgents := map[string]config.AgentConfig{
		"coder": {Runner: "claude", Model: "sonnet"},
	}
	m := NewSetupModel("claude", "sonnet", "high", baseAgents)
	m.globalOnly = true
	m.width, m.height = 100, 40
	m.section = sectionModel

	m, cmd := m.handleModelKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if m.section != sectionEffort {
		t.Fatalf("section = %v, want sectionEffort", m.section)
	}
	if cmd == nil {
		t.Fatal("expected an effort-options command")
	}

	msg := cmd()
	if msg == nil {
		t.Fatal("expected an effort-options message")
	}
	m, _ = m.Update(msg)
	if m.effortLoading {
		t.Fatal("effort loading should be finished")
	}
	if !containsEffort(m.effortOptions, "high") {
		t.Fatalf("options = %v, want high", m.effortOptions)
	}
	if m.effortIdx != 3 { // default, low, medium, high
		t.Fatalf("effortIdx = %d, want 3", m.effortIdx)
	}

	m, _ = m.handleEffortKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pendingSave == nil {
		t.Fatal("expected pending global save")
	}
	if m.pendingSave.msg.effort != "high" {
		t.Fatalf("pending effort = %q, want high", m.pendingSave.msg.effort)
	}
	if m.pendingSave.close {
		t.Fatal("incremental effort save must not close the screen")
	}
	if !m.saving {
		t.Fatal("expected saving state while ping is in flight")
	}

	// Simulate a successful reachability check.
	m, cmd = m.Update(pingResultMsg{})
	if m.saving {
		t.Fatal("saving should be finished")
	}
	if m.section != sectionProvider {
		t.Fatalf("section = %v, want sectionProvider after save", m.section)
	}
	if !strings.Contains(m.saveStatus, "Saved") {
		t.Fatalf("saveStatus = %q, want Saved", m.saveStatus)
	}
	if cmd == nil {
		t.Fatal("expected a save-status clear command")
	}

	loaded, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agents["coder"].Effort != "high" {
		t.Fatalf("persisted effort = %q, want high", loaded.Agents["coder"].Effort)
	}
}

func TestProjectSetup_LanguageSavesAndAdvances(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "go", "proj", nil)
	m.root = root
	m.width, m.height = 100, 40
	m.progLanguages = []string{"go", "cpp"}
	m.progLangIdx = 1
	m.section = sectionProgLang

	m, cmd := m.handleProgLangKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if m.section != sectionProjectName {
		t.Fatalf("section = %v, want sectionProjectName", m.section)
	}
	if !strings.Contains(m.saveStatus, "Saved") {
		t.Fatalf("saveStatus = %q, want Saved", m.saveStatus)
	}
	if cmd == nil {
		t.Fatal("expected a save-status clear command")
	}

	proj := config.LoadProject(root)
	if proj.Project.Language != "cpp" {
		t.Fatalf("persisted language = %q, want cpp", proj.Project.Language)
	}
}

func TestProjectSetup_NameSavesAndAdvances(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "go", "old", nil)
	m.root = root
	m.projectNameInput.SetValue("new-name")
	m.section = sectionProjectName

	m, _ = m.handleProjectNameKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if m.section != sectionAgentSetup {
		t.Fatalf("section = %v, want sectionAgentSetup", m.section)
	}
	if !strings.Contains(m.saveStatus, "Saved") {
		t.Fatalf("saveStatus = %q, want Saved", m.saveStatus)
	}

	proj := config.LoadProject(root)
	if proj.Project.Name != "new-name" {
		t.Fatalf("persisted name = %q, want new-name", proj.Project.Name)
	}
}

func TestAgentEffortSavesAndStays(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "go", "proj", nil)
	m.root = root
	m.width, m.height = 100, 40
	m.agentRoles = []string{"coder"}
	m.agentCursor = 0
	m.agentEditing = true
	m.agentEditStep = 2
	m.agentEditProv = providerClaude
	m.claudeModels = []string{"sonnet"}
	m.agentEditIdx = 0
	m.agentEffortOptions = []string{"low", "medium", "high"}
	m.agentEffortFor = "claude/sonnet"
	m.agentEffortLoading = false
	m.agentEditEffort = "high"
	m.agentEditEffortIdx = 3 // default, low, medium, high

	m, _ = m.handleAgentEditKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if m.agentEditing {
		t.Fatal("agent editor should close after saving")
	}
	if m.section != sectionAgentSetup {
		t.Fatalf("section = %v, want sectionAgentSetup", m.section)
	}
	if !strings.Contains(m.saveStatus, "Saved") {
		t.Fatalf("saveStatus = %q, want Saved", m.saveStatus)
	}

	proj := config.LoadProject(root)
	if proj.Agents["coder"].Effort != "high" {
		t.Fatalf("persisted effort = %q, want high", proj.Agents["coder"].Effort)
	}
}

func TestEscEmitsClosedMsg(t *testing.T) {
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "go", "proj", nil)
	m.section = sectionProvider
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected a close command")
	}
	if _, ok := cmd().(setupClosedMsg); !ok {
		t.Fatalf("expected setupClosedMsg, got %T", cmd())
	}
}

func containsEffort(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
