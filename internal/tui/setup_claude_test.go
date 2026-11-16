package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func claudeSetup(t *testing.T, model string) SetupModel {
	t.Helper()
	m := prep(globalSetupFor(t, "claude", model, nil))
	m.claudeModelsLoading = false
	m.section = sectionModel
	return m
}

func typeInto(m SetupModel, s string) SetupModel {
	m.typed.SetValue(s)
	return m
}

func TestClaudeRowsOrderAndHints(t *testing.T) {
	m := claudeSetup(t, "sonnet")
	want := []string{"fable", "opus", "sonnet", "haiku", "claude-fable-5-2", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", claudeTypeRow}
	if got := m.claudeRows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v", got)
	}
	_, hints := m.claudeLabels()
	if hints[0] != "latest Fable" || hints[1] != "latest Opus" || hints[4] != "" || hints[8] != "" {
		t.Fatalf("hints = %q", hints)
	}
}

func TestClaudeGlobalSelectFable(t *testing.T) {
	m := claudeSetup(t, "sonnet")
	for m.modelIdx > 0 {
		m, _ = press(m, "k")
	}
	if _, model := m.selectedProviderModel(); model != "fable" {
		t.Fatalf("model = %q", model)
	}
	if p := m.currentPayload(); p.model != "fable" {
		t.Fatalf("payload model = %q", p.model)
	}
}

func TestClaudeAgentSelectFable(t *testing.T) {
	m := prep(globalSetupFor(t, "claude", "sonnet", nil))
	m.claudeModelsLoading = false
	m.agentRoles = []string{"coder"}
	m.agentCursor = 0
	m.section = sectionAgentSetup
	m.agentEditing, m.agentEditStep, m.agentEditProv = true, 1, providerClaude
	m.agentEditIdx = 0
	m, _ = press(m, "enter")
	m.agentEffortLoading = false
	m, _ = press(m, "enter")
	if ov := m.agentOverrides["coder"]; ov.model != "fable" || ov.runner != "claude" {
		t.Fatalf("override = %+v", ov)
	}
}

func TestClaudeTypedIdGlobal(t *testing.T) {
	m := claudeSetup(t, "sonnet")
	m.modelIdx = len(m.claudeRows()) - 1
	m, _ = press(m, "enter")
	if !m.claudeTyping || m.section != sectionModel {
		t.Fatalf("typing = %v section = %v", m.claudeTyping, m.section)
	}
	// Empty and spaces are rejected and keep the input open.
	m, _ = press(m, "enter")
	if !m.claudeTyping || m.claudeTypeErr == "" {
		t.Fatal("empty id accepted")
	}
	m = typeInto(m, "my model")
	m, _ = press(m, "enter")
	if !m.claudeTyping || m.claudeTypeErr == "" {
		t.Fatal("id with a space accepted")
	}
	if !strings.Contains(prep(m).renderContent(), "no spaces") {
		t.Error("error not shown")
	}
	// Esc returns to the list without closing the setup.
	m, cmd := press(m, "esc")
	if m.claudeTyping || cmd != nil {
		t.Fatalf("esc: typing = %v cmd = %v", m.claudeTyping, cmd != nil)
	}
	m.modelIdx = len(m.claudeRows()) - 1
	m, _ = press(m, "enter")
	m = typeInto(m, " claude-opus-4-8 ")
	m, _ = press(m, "enter")
	if m.claudeTyping || m.section != sectionEffort {
		t.Fatalf("typing = %v section = %v", m.claudeTyping, m.section)
	}
	if _, model := m.selectedProviderModel(); model != "claude-opus-4-8" {
		t.Fatalf("model = %q", model)
	}
}

func TestClaudeTypedIdAgent(t *testing.T) {
	m := prep(globalSetupFor(t, "claude", "sonnet", nil))
	m.claudeModelsLoading = false
	m.agentRoles = []string{"coder"}
	m.section = sectionAgentSetup
	m.agentEditing, m.agentEditStep, m.agentEditProv = true, 1, providerClaude
	m.agentEditIdx = len(m.claudeRows()) - 1
	m, _ = press(m, "enter")
	if !m.claudeTyping || m.agentEditStep != 1 {
		t.Fatalf("typing = %v step = %d", m.claudeTyping, m.agentEditStep)
	}
	m, _ = press(m, "esc")
	if m.claudeTyping || !m.agentEditing || m.agentEditStep != 1 {
		t.Fatalf("after esc: typing = %v editing = %v step = %d", m.claudeTyping, m.agentEditing, m.agentEditStep)
	}
	m.agentEditIdx = len(m.claudeRows()) - 1
	m, _ = press(m, "enter")
	m = typeInto(m, "claude-custom-1")
	m, _ = press(m, "enter")
	if m.agentEditStep != 2 {
		t.Fatalf("step = %d", m.agentEditStep)
	}
	m.agentEffortLoading = false
	m, _ = press(m, "enter")
	if ov := m.agentOverrides["coder"]; ov.model != "claude-custom-1" {
		t.Fatalf("override = %+v", ov)
	}
}

func TestClaudeSavedUnknownIdShowsCustom(t *testing.T) {
	m := claudeSetup(t, "claude-opus-4-8")
	rows := m.claudeRows()
	if rows[m.modelIdx] != "claude-opus-4-8" || m.modelIdx != len(rows)-2 {
		t.Fatalf("idx %d rows %v", m.modelIdx, rows)
	}
	labels, _ := m.claudeLabels()
	if labels[m.modelIdx] != "claude-opus-4-8 (custom)" {
		t.Fatalf("label = %q", labels[m.modelIdx])
	}
	if _, model := m.selectedProviderModel(); model != "claude-opus-4-8" {
		t.Fatalf("model = %q", model)
	}
	if !strings.Contains(ansi.Strip(m.renderContent()), "claude-opus-4-8 (custom)") {
		t.Error("custom row not rendered")
	}
}

func TestClaudeDefaultLine(t *testing.T) {
	m := claudeSetup(t, "")
	const want = "default → claude-opus-5-5 · high effort"
	if got := m.claudeDefaultLine(true); got != want {
		t.Errorf("line = %q", got)
	}
	if !strings.Contains(ansi.Strip(m.renderContent()), want) {
		t.Error("default line not rendered for an empty model")
	}
	if claudeSetup(t, "sonnet").claudeDefaultLine(false) != "" {
		t.Error("default line shown with a model set")
	}
}

func TestClaudeUnsetModelCursorAndHint(t *testing.T) {
	m := claudeSetup(t, "")
	rows := m.claudeRows()
	if rows[m.modelIdx] != "claude-opus-5-5" {
		t.Fatalf("cursor on %q", rows[m.modelIdx])
	}
	_, hints := m.claudeLabels()
	if hints[m.modelIdx] != "default" {
		t.Errorf("hint = %q", hints[m.modelIdx])
	}
	if m := claudeSetup(t, "haiku"); m.claudeRows()[m.modelIdx] != "haiku" {
		t.Error("explicit model lost its cursor")
	}
}

func TestClaudeEffortDefaultLabel(t *testing.T) {
	choices := effortChoices([]string{"low", "high"})
	if got := effortLabels(choices, true)[0]; got != "default (high)" {
		t.Errorf("claude label = %q", got)
	}
	if got := effortLabels(choices, false)[0]; got != "(provider default)" {
		t.Errorf("other label = %q", got)
	}
}

func TestClaudeAgentStepShowsDefaultLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "go", "proj", nil)
	m.width, m.height = 140, 40
	m.claudeModelsLoading = false
	m.agentRoles = []string{"coder"}
	m.section = sectionAgentSetup
	m.agentEditing, m.agentEditStep, m.agentEditProv = true, 1, providerClaude
	if !strings.Contains(ansi.Strip(m.renderContent()), "default → claude-opus-5-5 · high effort") {
		t.Error("agent step lacks the default line")
	}
	m.agentEditing = false
	m.agentOverrides["coder"] = agentSetupOverride{runner: "claude"}
	if !strings.Contains(ansi.Strip(m.renderContent()), "claude / default → claude-opus-5-5 · high") {
		t.Error("agent list lacks the default text for a claude role without a model")
	}
	m.agentEditing = true
	m.agentOverrides["coder"] = agentSetupOverride{runner: "claude", model: "haiku"}
	if strings.Contains(ansi.Strip(m.renderContent()), "default → ") {
		t.Error("default line shown for a role with a model")
	}
}

func TestClaudeSetupViewWidths(t *testing.T) {
	for _, w := range []int{100, 140} {
		for _, typing := range []bool{false, true} {
			m := claudeSetup(t, "claude-opus-4-8")
			m.width, m.height = w, 40
			m.syncViewport()
			m.claudeTyping = typing
			for i, line := range strings.Split(m.View(), "\n") {
				if lw := ansi.StringWidth(line); lw > w {
					t.Fatalf("width %d typing %v: line %d is %d wide: %q", w, typing, i, lw, ansi.Strip(line))
				}
			}
		}
	}
}

func TestClaudeSetupViewWidthsUnsetModel(t *testing.T) {
	for _, w := range []int{100, 140} {
		for _, typing := range []bool{false, true} {
			m := claudeSetup(t, "")
			m.width, m.height = w, 40
			m.syncViewport()
			m.claudeTyping = typing
			for i, line := range strings.Split(m.View(), "\n") {
				if lw := ansi.StringWidth(line); lw > w {
					t.Fatalf("width %d typing %v: line %d is %d wide: %q", w, typing, i, lw, ansi.Strip(line))
				}
			}
		}
	}
}
