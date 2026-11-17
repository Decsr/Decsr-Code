package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestProviderStrings_Dsh(t *testing.T) {
	if got := providerFromString("dsh"); got != providerDsh {
		t.Fatalf("providerFromString(dsh) = %v", got)
	}
	if got := providerToString(providerDsh); got != "dsh" {
		t.Fatalf("providerToString(providerDsh) = %q", got)
	}
	if providerLabels[providerDsh] != "DeepSeek Harness" {
		t.Fatalf("label = %q", providerLabels[providerDsh])
	}
}

func TestSetup_DshRunnerRoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewSetupModel("dsh", "deepseek-pro", "high", map[string]config.AgentConfig{
		"coder": {Runner: "dsh", Model: "deepseek-pro", Effort: "high"},
	})
	m.globalOnly = true

	if m.provider != providerDsh {
		t.Fatalf("provider = %v, want dsh", m.provider)
	}
	provider, model := m.selectedProviderModel()
	if provider != "dsh" || model != "deepseek-pro" {
		t.Fatalf("selection = %q/%q", provider, model)
	}
	got := m.currentPayload()
	if got.provider != "dsh" || got.model != "deepseek-pro" || got.effort != "high" {
		t.Fatalf("payload = %+v", got)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.pendingSave == nil {
		t.Fatal("expected pending save")
	}
	finishSave(t, m)
	loaded, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ac := loaded.Agents["coder"]
	if ac.Runner != "dsh" || ac.Model != "deepseek-pro" || ac.Effort != "high" {
		t.Fatalf("coder after save = %+v", ac)
	}
}
