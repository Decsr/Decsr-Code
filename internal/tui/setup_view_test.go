package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func pressKey(m SetupModel, t tea.KeyType) SetupModel {
	m, _ = m.Update(tea.KeyMsg{Type: t})
	return m
}

func sizedSetup(m SetupModel, w, h int) SetupModel {
	m.width, m.height = w, h
	m.claudeModelsLoading = false
	m.syncViewport()
	return m
}

func projectSetupFor(t *testing.T) (SetupModel, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "go", "proj", map[string]config.AgentConfig{
		"pm": {Runner: "claude", Model: "claude-opus-5-5", Effort: "high"},
	})
	m.root = root
	m.agentRoles = []string{"pm", "coder"}
	m.claudeModelsLoading = false
	m.section = sectionAgentSetup
	return m, root
}

func checkLook(t *testing.T, name, out string, w int) {
	t.Helper()
	for i, l := range strings.Split(out, "\n") {
		if lw := ansi.StringWidth(l); lw > w {
			t.Errorf("%s: line %d is %d wide (max %d): %q", name, i, lw, w, ansi.Strip(l))
		}
	}
	// Boxes are titled in capitals and sit side by side without a gap; every
	// box line must keep its right border at the same column as its top line.
	for i, l := range strings.Split(ansi.Strip(out), "\n") {
		if strings.HasPrefix(l, "┌") && !strings.Contains(l, "─ ◆ ") {
			t.Errorf("%s: line %d has a box top without a title: %q", name, i, l)
		}
	}
}

func TestSetupScreensLook(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {110, 34}, {140, 40}} {
		w, h := sz[0], sz[1]
		g := sizedSetup(prep(globalSetupFor(t, "claude", "sonnet", nil)), w, h)
		g.providerAvailability[providerOllama] = errors.New("down")
		g.syncViewport()
		checkLook(t, "global", g.View(), w)
		g.section = sectionEffort
		g.syncViewport()
		checkLook(t, "global effort", g.View(), w)

		p, _ := projectSetupFor(t)
		p = sizedSetup(p, w, h)
		checkLook(t, "project", p.View(), w)
		p, _ = press(p, "enter")
		checkLook(t, "project editor", p.View(), w)

		mm := newModelMemoryModel(config.Config{})
		mm.width, mm.height = w, h
		for i := 0; i < 3; i++ {
			checkLook(t, "memory", mm.View(), w)
			mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRight})
		}
	}
}

func TestSetupGlobalOfflineLine(t *testing.T) {
	m := sizedSetup(prep(globalSetupFor(t, "claude", "sonnet", nil)), 140, 40)
	m.providerAvailability[providerOllama] = errors.New("down")
	m.providerAvailability[providerMLX] = errors.New("down")
	m.syncViewport()
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "offline: Ollama · oMLX") {
		t.Errorf("no offline line:\n%s", view)
	}
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "Ollama") && !strings.Contains(l, "offline:") {
			t.Errorf("offline provider shown as a tab: %q", l)
		}
	}
	if !strings.Contains(view, "▌Claude▐") {
		t.Errorf("selected provider not marked:\n%s", view)
	}
}

func TestSetupModelEffortSideBySideOrStacked(t *testing.T) {
	both := func(w int) bool {
		m := sizedSetup(prep(globalSetupFor(t, "claude", "sonnet", nil)), w, 40)
		for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
			if strings.Contains(l, "DEFAULT MODEL") && strings.Contains(l, "EFFORT") {
				return true
			}
		}
		return false
	}
	if !both(140) {
		t.Error("model and effort should sit side by side at 140 columns")
	}
	if both(100) {
		t.Error("model and effort should be stacked at 100 columns")
	}
}

func TestAgentEditorFieldsAndSave(t *testing.T) {
	m, root := projectSetupFor(t)
	m = sizedSetup(m, 140, 40)
	m, _ = press(m, "enter")
	view := ansi.Strip(m.View())
	for _, want := range []string{"Runner", "Model", "Effort", "Mode", "╭─ pm"} {
		if !strings.Contains(view, want) {
			t.Fatalf("editor lacks %q:\n%s", want, view)
		}
	}
	if m.agentField != agentFieldRunner {
		t.Fatalf("field = %d", m.agentField)
	}
	m = pressKey(m, tea.KeyDown)
	m = pressKey(m, tea.KeyDown)
	m = pressKey(m, tea.KeyDown)
	if m.agentField != agentFieldMode {
		t.Fatalf("field after 3 downs = %d", m.agentField)
	}
	m = pressKey(m, tea.KeyUp)
	if m.agentField != agentFieldEffort {
		t.Fatalf("field after up = %d", m.agentField)
	}
	m = pressKey(m, tea.KeyDown)
	m = pressKey(m, tea.KeyRight) // default -> text
	if m.agentEditMode != config.ModeText {
		t.Fatalf("mode = %q", m.agentEditMode)
	}
	m = pressKey(m, tea.KeyRight) // text -> agent
	if m.agentEditMode != config.ModeAgent {
		t.Fatalf("mode = %q", m.agentEditMode)
	}
	m.agentEffortLoading = false
	m, _ = press(m, "enter")
	if m.agentEditing {
		t.Fatalf("editor still open: %q", m.agentEditErr)
	}
	ov := m.agentOverrides["pm"]
	if ov.runner != "claude" || ov.mode != config.ModeAgent || ov.model != "claude-opus-5-5" {
		t.Fatalf("override = %+v", ov)
	}
	ac := config.LoadProject(root).Agents["pm"]
	if ac.Mode != config.ModeAgent || ac.Runner != "claude" || ac.Model != "claude-opus-5-5" {
		t.Fatalf("saved = %+v", ac)
	}
	if !strings.Contains(m.saveStatus, "Saved") {
		t.Errorf("status = %q", m.saveStatus)
	}
}

func TestAgentEditorRefusesInvalidMode(t *testing.T) {
	m, root := projectSetupFor(t)
	m = sizedSetup(m, 140, 40)
	m.agentCursor = 1
	m, _ = press(m, "enter")
	m.agentEditProv = m.kindOf("opencode", "")
	m.agentEditMode = config.ModeText
	m.agentEffortLoading = false
	m, _ = press(m, "enter")
	if !m.agentEditing || m.agentEditErr == "" {
		t.Fatalf("invalid mode accepted: editing=%v err=%q", m.agentEditing, m.agentEditErr)
	}
	want := (config.AgentConfig{Runner: "opencode", Mode: config.ModeText}).ModeError().Error()
	if m.agentEditErr != want {
		t.Errorf("err = %q, want %q", m.agentEditErr, want)
	}
	if !strings.Contains(ansi.Strip(m.View()), "text mode is not supported") {
		t.Error("error not shown in the editor")
	}
	if _, ok := m.agentOverrides["coder"]; ok {
		t.Error("override stored")
	}
	if _, ok := config.LoadProject(root).Agents["coder"]; ok {
		t.Error("config saved")
	}
}

func TestAgentEditorEscCancelsAndRunnerChanges(t *testing.T) {
	m, _ := projectSetupFor(t)
	m = sizedSetup(m, 140, 40)
	m, _ = press(m, "enter")
	before := m.agentEditProv
	m = pressKey(m, tea.KeyRight)
	if m.agentEditProv == before {
		t.Error("right did not change the runner")
	}
	m, _ = press(m, "esc")
	if m.agentEditing {
		t.Fatal("esc did not close the editor")
	}
	if ov := m.agentOverrides["pm"]; ov.runner != "claude" {
		t.Errorf("override changed: %+v", ov)
	}
}

func TestAgentModeOnlyKeptOnReset(t *testing.T) {
	m, _ := projectSetupFor(t)
	m.agentOverrides["pm"] = agentSetupOverride{runner: "claude", model: "x", mode: config.ModeAgent, modeSet: true}
	m, _ = press(m, "\x7f")
	m.resetAgentOverride("pm")
	if ov := m.agentOverrides["pm"]; ov.runner != "" || ov.mode != config.ModeAgent {
		t.Errorf("reset = %+v", ov)
	}
	m = sizedSetup(m, 140, 40)
	if s := m.agentSummary("pm"); !strings.Contains(s, "inherits global") || !strings.Contains(s, "agent") {
		t.Errorf("summary = %q", s)
	}
}

func TestSetupRenderSamples(t *testing.T) {
	g := sizedSetup(prep(globalSetupFor(t, "claude", "", nil)), 140, 40)
	g.providerAvailability[providerOllama] = errors.New("down")
	g.providerAvailability[providerMLX] = errors.New("down")
	g.syncViewport()
	t.Logf("GLOBAL 140x40\n%s", ansi.Strip(g.View()))
	g2 := sizedSetup(prep(globalSetupFor(t, "claude", "", nil)), 100, 30)
	t.Logf("GLOBAL 100x30\n%s", ansi.Strip(g2.View()))
	p, _ := projectSetupFor(t)
	p = sizedSetup(p, 140, 40)
	p, _ = press(p, "enter")
	t.Logf("PROJECT 140x40\n%s", ansi.Strip(p.View()))
	mm := newModelMemoryModel(config.Config{ModelMemory: config.ModelMemoryConfig{Mode: "ram", MaxRAMGB: 16}})
	mm.width, mm.height = 120, 34
	mm.localLoading = false
	t.Logf("MEMORY 120x34\n%s", ansi.Strip(mm.View()))
}

func TestGlobalModelListShownInFullWhenItFits(t *testing.T) {
	g := sizedSetup(prep(globalSetupFor(t, "claude", "", nil)), 140, 40)
	out := ansi.Strip(g.View())
	if strings.Contains(out, "more") || !strings.Contains(out, "fable") || !strings.Contains(out, "opus") {
		t.Fatalf("model list was windowed although it fits:\n%s", out)
	}
	// Too little room: windowed, with the cursor visible.
	small := sizedSetup(prep(globalSetupFor(t, "claude", "", nil)), 140, 14)
	small.modelIdx = 6
	small.syncViewport()
	out = ansi.Strip(small.View())
	if !strings.Contains(out, "more") {
		t.Fatalf("a list that does not fit must be windowed:\n%s", out)
	}
}

func TestOfflineProvidersOnlyOnTheOfflineLine(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {100, 30}, {70, 24}} {
		g := prep(globalSetupFor(t, "claude", "", nil))
		g.providerAvailability[providerOllama] = errors.New("down")
		g.providerAvailability[providerMLX] = errors.New("down")
		g = sizedSetup(g, size[0], size[1])
		var tabs, offline string
		for _, l := range strings.Split(ansi.Strip(g.View()), "\n") {
			if strings.Contains(l, "offline:") {
				offline = l
			} else if strings.Contains(l, "Claude") || strings.Contains(l, "Codex") {
				tabs += l
			}
		}
		if !strings.Contains(offline, "Ollama") || !strings.Contains(offline, "oMLX") {
			t.Errorf("%v: offline line = %q", size, offline)
		}
		if strings.Contains(tabs, "Ollama") || strings.Contains(tabs, "oMLX") {
			t.Errorf("%v: offline provider shown as a tab: %q", size, tabs)
		}
	}
}
