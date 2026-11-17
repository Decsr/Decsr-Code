package tui

import (
	"errors"
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
	m, _ = m.Update(dshModelsListMsg{err: errors.New("no key")})
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
	m, _ = m.Update(dshModelsListMsg{err: errors.New("no key")})
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

func dshListed(t *testing.T, model string) SetupModel {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := NewSetupModel("dsh", model, "high", nil)
	m.globalOnly = true
	m.width, m.height = 140, 40
	m, _ = m.Update(dshModelsListMsg{models: []string{"deepseek-flash", "deepseek-v4-pro"}})
	return m
}

func TestSetup_DshModelList(t *testing.T) {
	m := dshListed(t, "")
	if m.typedMode(providerDsh) {
		t.Fatal("a fetched list should replace the typed input")
	}
	want := []string{dshDefaultRow, "deepseek-flash", "deepseek-v4-pro"}
	if got := m.modelsForProvider(providerDsh); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if _, model := m.selectedProviderModel(); model != "" || m.modelIdx != 0 {
		t.Fatalf("blank model should select (default): idx %d, model %q", m.modelIdx, model)
	}
	m.modelIdx = 2
	if provider, model := m.selectedProviderModel(); provider != "dsh" || model != "deepseek-v4-pro" {
		t.Fatalf("selection = %q/%q", provider, model)
	}
	m.syncViewport()
	if view := ansi.Strip(m.renderContent()); !strings.Contains(view, "deepseek-v4-pro") || !strings.Contains(view, dshDefaultRow) {
		t.Errorf("list not rendered:\n%s", view)
	}
}

func TestSetup_DshSavedModelStaysSelected(t *testing.T) {
	m := dshListed(t, "deepseek-flash")
	if _, model := m.selectedProviderModel(); model != "deepseek-flash" {
		t.Fatalf("listed saved model lost: %q", model)
	}
	m = dshListed(t, "my-custom-model")
	rows := m.modelsForProvider(providerDsh)
	if rows[len(rows)-1] != "my-custom-model" {
		t.Fatalf("saved model not kept in rows: %v", rows)
	}
	if _, model := m.selectedProviderModel(); model != "my-custom-model" {
		t.Fatalf("saved model not selected: %q", model)
	}
}

func TestSetup_DshNoKeyFallsBackToTyping(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := NewSetupModel("dsh", "saved-model", "high", nil)
	m.globalOnly = true
	m.width, m.height = 140, 40
	m, _ = m.Update(dshModelsListMsg{err: errors.New("no key")})
	if !m.typedMode(providerDsh) {
		t.Fatal("a failed fetch should leave the typed input")
	}
	if _, model := m.selectedProviderModel(); model != "saved-model" {
		t.Fatalf("typed model = %q", model)
	}
	m.syncViewport()
	if view := ansi.Strip(m.renderContent()); !strings.Contains(view, "decsr connect deepseek") {
		t.Errorf("no key hint:\n%s", view)
	}
}

func TestSetup_DshEffortWithBlankModel(t *testing.T) {
	m := dshListed(t, "")
	m.syncViewport()
	if view := ansi.Strip(m.renderContent()); !strings.Contains(view, "Reasoning effort for dsh / (default)") {
		t.Errorf("effort section lost for a blank model:\n%s", view)
	}
	if got := m.currentPayload(); got.provider != "dsh" || got.model != "" || got.effort != "high" {
		t.Fatalf("payload = %+v", got)
	}
}
