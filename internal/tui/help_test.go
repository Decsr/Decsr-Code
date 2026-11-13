package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderHelpFitsSize(t *testing.T) {
	for _, sz := range [][2]int{{60, 12}, {80, 24}, {120, 40}} {
		out := renderHelp(sz[0], sz[1], "KEYBOARD — RUN", cockpitHelp())
		lines := strings.Split(out, "\n")
		if len(lines) > sz[1] {
			t.Errorf("%dx%d: %d lines", sz[0], sz[1], len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%dx%d: line width %d: %q", sz[0], sz[1], ansi.StringWidth(l), stripANSI(l))
			}
		}
	}
}

func TestRenderHelpListsKeys(t *testing.T) {
	out := stripANSI(renderHelp(120, 40, "KEYBOARD — RUN", cockpitHelp()))
	for _, want := range []string{"Ctrl+A", "approve", "Tab", "inspector", "close"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestCockpitHelpToggle(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !c.showHelp {
		t.Fatal("? should open help")
	}
	if !strings.Contains(stripANSI(c.View()), "KEYBOARD") {
		t.Fatal("view should show the help overlay")
	}
	before := c.tab
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if !c.showHelp {
		t.Error("other keys are swallowed while help is open")
	}
	if c.tab != before {
		t.Error("a swallowed key must not change the tab")
	}
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if c.showHelp {
		t.Error("esc should close help")
	}
}

func TestHomeHelpToggle(t *testing.T) {
	m := NewHomeModel(newTestConfig(), t.TempDir())
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !m.showHelp {
		t.Fatal("? should open help")
	}
	if !strings.Contains(stripANSI(m.View()), "KEYBOARD") {
		t.Fatal("view should show the help overlay")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("enter must be swallowed while help is open")
	}
}

var helpHint = regexp.MustCompile(`\?\s+help`)

func TestFootersAdvertiseHelp(t *testing.T) {
	m := NewHomeModel(newTestConfig(), t.TempDir())
	m.width = 120
	if f := stripANSI(m.renderFooter()); !helpHint.MatchString(f) {
		t.Errorf("home footer lacks help hint: %q", f)
	}
	c, _ := cockpitFixture(t)
	for _, w := range []int{80, 120} {
		c.SetSize(w, 30)
		if f := stripANSI(c.renderFooter()); !helpHint.MatchString(f) {
			t.Errorf("cockpit footer at %d lacks help hint: %q", w, f)
		}
	}
}

func TestRenderHelpPaneSingleTitle(t *testing.T) {
	for _, sz := range [][2]int{{60, 12}, {80, 24}, {120, 40}} {
		out := renderHelpPane(sz[0], sz[1], "KEYBOARD — RUN", cockpitHelp())
		lines := strings.Split(out, "\n")
		if len(lines) > sz[1] {
			t.Errorf("%dx%d: %d lines", sz[0], sz[1], len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%dx%d: line width %d", sz[0], sz[1], ansi.StringWidth(l))
			}
		}
		plain := stripANSI(out)
		if strings.Contains(plain, "DECSR") {
			t.Errorf("%dx%d: pane help must not draw a DECSR header", sz[0], sz[1])
		}
		if !strings.HasPrefix(strings.TrimSpace(stripANSI(lines[0])), "KEYBOARD — RUN") {
			t.Errorf("%dx%d: first line = %q", sz[0], sz[1], stripANSI(lines[0]))
		}
	}
}

func TestCockpitHelpViewHasNoDecsrHeader(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(100, 30)
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	out := c.View()
	if strings.Contains(stripANSI(out), "DECSR") {
		t.Error("cockpit help must not draw a second DECSR header")
	}
	if n := len(strings.Split(out, "\n")); n > 30 {
		t.Errorf("%d lines > cockpit height 30", n)
	}
}
