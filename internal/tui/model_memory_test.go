package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// Toggling away from a mode and back must preserve the value the user typed,
// not reset it to the default. Reproduces the reported bug where editing the
// context limit, switching to RAM, and switching back lost the context value.
func TestModelMemory_TogglePreservesPerModeValue(t *testing.T) {
	m := newModelMemoryModel(config.Config{})
	// Start in context mode.
	for m.mode != mmModeContext {
		m, _ = m.Update(key("l"))
	}

	// Clear and type a custom context value.
	m.value.SetValue("")
	for _, r := range "12345" {
		m, _ = m.Update(key(string(r)))
	}
	if got := m.value.Value(); got != "12345" {
		t.Fatalf("context input: want 12345, got %q", got)
	}

	// Toggle to RAM and back to context.
	m, _ = m.Update(key("l")) // context -> off (or ram, order-dependent)
	m, _ = m.Update(key("h")) // back

	// Find context mode again and assert the value survived.
	startMode := m.mode
	for m.mode != mmModeContext {
		m, _ = m.Update(key("l"))
		if m.mode == startMode {
			t.Fatal("could not return to context mode")
		}
	}
	if got := m.value.Value(); got != "12345" {
		t.Errorf("context value not preserved across toggle: want 12345, got %q", got)
	}
}

func TestModelMemory_ResultUsesActiveMode(t *testing.T) {
	m := newModelMemoryModel(config.Config{})
	for m.mode != mmModeContext {
		m, _ = m.Update(key("l"))
	}
	m.value.SetValue("")
	for _, r := range "16384" {
		m, _ = m.Update(key(string(r)))
	}
	res := m.result()
	if res.Mode != "context" || res.MaxContextTokens != 16384 {
		t.Errorf("result: want context/16384, got %s/%d", res.Mode, res.MaxContextTokens)
	}
}

// The prefilled value is a suggestion: the first typed digit replaces it.
func TestModelMemory_FirstDigitReplacesPrefill(t *testing.T) {
	m := newModelMemoryModel(config.Config{ModelMemory: config.ModelMemoryConfig{Mode: "ram", MaxRAMGB: 10.5}})
	if got := m.value.Value(); got != "10.5" {
		t.Fatalf("prefill: want 10.5, got %q", got)
	}
	m, _ = m.Update(key("8"))
	if got := m.value.Value(); got != "8" {
		t.Fatalf("after first digit: want 8, got %q", got)
	}
	m, _ = m.Update(key("4"))
	if got := m.value.Value(); got != "84" {
		t.Fatalf("second digit must append: want 84, got %q", got)
	}
}

func TestModelMemory_BackspaceEditsPrefill(t *testing.T) {
	m := newModelMemoryModel(config.Config{ModelMemory: config.ModelMemoryConfig{Mode: "ram", MaxRAMGB: 10.5}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = m.Update(key("7"))
	if got := m.value.Value(); got != "10.7" {
		t.Fatalf("backspace then digit: want 10.7, got %q", got)
	}
}

func TestModelMemory_ModeSwitchReplacesPrefill(t *testing.T) {
	m := newModelMemoryModel(config.Config{ModelMemory: config.ModelMemoryConfig{Mode: "ram", MaxRAMGB: 10.5}})
	m, _ = m.Update(key("5")) // typed, no longer fresh
	m, _ = m.Update(key("l"))
	m, _ = m.Update(key("h"))
	m, _ = m.Update(key("9"))
	if got := m.value.Value(); got != "9" {
		t.Fatalf("digit after mode switch: want 9, got %q", got)
	}
}

func TestModelMemoryViewIsPanelled(t *testing.T) {
	for _, sz := range [][2]int{{100, 30}, {60, 20}} {
		m := newModelMemoryModel(config.Config{})
		m, _ = m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		out := m.View()
		plain := stripANSI(out)
		for _, want := range []string{"MODEL MEMORY", "┌", "└", mmModeLabels[0], mmModeLabels[1], mmModeLabels[2]} {
			if !strings.Contains(plain, want) {
				t.Errorf("%dx%d: view missing %q", sz[0], sz[1], want)
			}
		}
		for _, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%dx%d: line width %d", sz[0], sz[1], ansi.StringWidth(l))
			}
		}
	}
}

func TestModelMemory_ActiveModeMarkedWithoutColor(t *testing.T) {
	m := newModelMemoryModel(config.Config{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	var row string
	for _, l := range strings.Split(stripANSI(m.View()), "\n") {
		if strings.Contains(l, mmModeLabels[0]) && strings.Contains(l, mmModeLabels[1]) {
			row = l
		}
	}
	if row == "" {
		t.Fatal("mode row not found")
	}
	if got := strings.Count(row, "▸"); got != 1 {
		t.Fatalf("want exactly one marker, got %d in %q", got, row)
	}
	if !strings.Contains(row, "▸  "+mmModeLabels[m.mode]) {
		t.Fatalf("marker not on active mode %q: %q", mmModeLabels[m.mode], row)
	}
}
