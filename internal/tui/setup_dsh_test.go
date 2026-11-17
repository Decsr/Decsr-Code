package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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

func TestSetupView_DshInGlobalSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewSetupModel("dsh", "", "high", nil)
	m.globalOnly = true
	m.width, m.height = 140, 40
	m.syncViewport()

	view := ansi.Strip(m.renderContent())
	for _, want := range []string{"DeepSeek Harness", "type a model id, blank for dsh's default", "Reasoning effort for dsh / (default)"} {
		if !strings.Contains(view, want) {
			t.Errorf("global settings lack %q:\n%s", want, view)
		}
	}
}

func TestSetupView_DshCanBeChosenInGlobalSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewSetupModel("opencode", "", "default", nil)
	m.globalOnly = true
	m.width, m.height = 140, 40
	m.syncViewport()
	m.section = sectionProvider
	for i := 0; i < len(m.entries) && m.provider != providerDsh; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.provider != providerDsh {
		t.Fatalf("provider = %v, DeepSeek Harness is not reachable with the arrow keys", m.provider)
	}
	if !m.typedMode(providerDsh) {
		t.Error("dsh should take a typed model")
	}
	if !strings.Contains(ansi.Strip(m.renderContent()), "▶ DeepSeek Harness") && !strings.Contains(ansi.Strip(m.renderContent()), "[ DeepSeek Harness ]") {
		t.Error("the provider row does not mark DeepSeek Harness as selected")
	}
}
