package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func globalSetupWithEffort(t *testing.T, effort string) SetupModel {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	base := map[string]config.AgentConfig{
		"coder": {Runner: "claude", Model: "sonnet", Effort: effort},
		"qa":    {Runner: "claude", Model: "sonnet", Effort: effort},
	}
	m := NewSetupModel("claude", "sonnet", effort, base)
	m.globalOnly = true
	m.width, m.height = 100, 40
	m.section = sectionModel
	return m
}

// finishSave runs the pending save as if the reachability check passed and
// returns the message the screen emits.
func finishSave(t *testing.T, m SetupModel) tea.Msg {
	t.Helper()
	m, cmd := m.Update(pingResultMsg{})
	if cmd == nil {
		t.Fatal("expected a command after a successful save")
	}
	return cmd()
}

func TestGlobalSetup_ModelChangeKeepsAgentEffort(t *testing.T) {
	m := globalSetupWithEffort(t, "max")

	m, _ = m.handleModelKeys(tea.KeyMsg{Type: tea.KeyDown}) // sonnet -> opus
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.pendingSave == nil {
		t.Fatal("expected pending save")
	}
	finishSave(t, m)

	loaded, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"coder", "qa"} {
		ac := loaded.Agents[role]
		if ac.Model != "opus" {
			t.Errorf("%s model = %q, want opus", role, ac.Model)
		}
		if ac.Effort != "max" {
			t.Errorf("%s effort = %q, want max", role, ac.Effort)
		}
	}
}

func TestGlobalSetup_EffortSectionDefaultsToCurrent(t *testing.T) {
	m := globalSetupWithEffort(t, "max")
	m, _ = m.handleModelKeys(tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := m.handleModelKeys(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(cmd())
	choices := effortChoices(m.effortOptions)
	if choices[m.effortIdx] != "max" {
		t.Fatalf("effort cursor on %q, want max", choices[m.effortIdx])
	}
	// Enter without touching the list keeps max.
	m, _ = m.handleEffortKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pendingSave == nil || m.pendingSave.msg.effort != "max" {
		t.Fatalf("pending save = %+v, want effort max", m.pendingSave)
	}
}

func TestGlobalSetup_ExplicitEffortStillSaved(t *testing.T) {
	m := globalSetupWithEffort(t, "max")
	m, _ = m.handleModelKeys(tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := m.handleModelKeys(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(cmd())
	m, _ = m.handleEffortKeys(tea.KeyMsg{Type: tea.KeyUp}) // one level below max
	want := effortChoices(m.effortOptions)[m.effortIdx]
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	finishSave(t, m)

	loaded, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"coder", "qa"} {
		ac := loaded.Agents[role]
		if ac.Effort != want || ac.Model != "opus" {
			t.Errorf("%s = %q/%q, want opus/%q", role, ac.Model, ac.Effort, want)
		}
	}
}
