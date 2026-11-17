package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func agentEditorSetup(t *testing.T) SetupModel {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := NewSetupModelWithOverrides("opencode", "", "default", "go", "proj", nil)
	m.width, m.height = 140, 50
	m.syncViewport()
	m.agentRoles = []string{"coder"}
	m.section = sectionAgentSetup
	m, _ = press(m, "enter")
	if !m.agentEditing {
		t.Fatal("the editor did not open")
	}
	return m
}

func TestSetupView_AgentEditorListsAllFields(t *testing.T) {
	m := agentEditorSetup(t)
	view := ansi.Strip(m.renderContent())
	for _, want := range []string{"Edit coder", "Runner", "Model", "Effort", "Mode", "↑↓ field"} {
		if !strings.Contains(view, want) {
			t.Errorf("agent editor lacks %q:\n%s", want, view)
		}
	}
}

func TestSetupView_AgentEditorModeField(t *testing.T) {
	m := agentEditorSetup(t)
	for i := 0; i < agentFieldMode; i++ {
		m, _ = press(m, "down")
	}
	if m.agentField != agentFieldMode {
		t.Fatalf("field = %d, want the mode field", m.agentField)
	}
	view := ansi.Strip(m.renderContent())
	for _, want := range []string{"runner default", "text", "agent"} {
		if !strings.Contains(view, want) {
			t.Errorf("mode field lacks %q:\n%s", want, view)
		}
	}
	m, _ = press(m, "right")
	if m.agentEditMode != "text" {
		t.Fatalf("mode = %q after right, want text", m.agentEditMode)
	}
	if v := ansi.Strip(m.renderContent()); !strings.Contains(v, "[ text ]") {
		t.Errorf("the chosen mode is not marked:\n%s", v)
	}
}

func TestSetupView_AgentListShowsMode(t *testing.T) {
	m := agentEditorSetup(t)
	m.agentEditing = false
	m.agentOverrides["coder"] = agentSetupOverride{runner: "opencode", model: "x/y", mode: "text", modeSet: true}
	if v := ansi.Strip(m.renderContent()); !strings.Contains(v, "mode text") {
		t.Errorf("the agent list does not show the mode:\n%s", v)
	}
}

func TestSetupView_BracketTypedInProjectName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewSetupModelWithOverrides("opencode", "", "default", "go", "proj", nil)
	m.width, m.height = 100, 40
	m.syncViewport()
	m.section = sectionProjectName
	m.syncProjectNameFocus()
	m, _ = press(m, "]")
	if m.scrollX != 0 {
		t.Errorf("scrollX = %d, the bracket should be typed into the name", m.scrollX)
	}
	m.section = sectionProgLang
	m, _ = press(m, "]")
	if m.scrollX != horizontalScrollStep {
		t.Errorf("scrollX = %d, want %d", m.scrollX, horizontalScrollStep)
	}
}
