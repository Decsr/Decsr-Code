package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// restyledScreens renders the screens of the group-3 restyle at w x h and
// returns each one as plain text, keyed by name.
func restyledScreens(t *testing.T, w, h int) map[string]string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
	mustWrite(t, filepath.Join(root, "spec.md"), "# Spec\n")
	mustWrite(t, filepath.Join(root, "requirements.md"), "# Requirements\n\n- first thing\n- second thing\n")
	mustWrite(t, filepath.Join(root, "architecture.md"),
		"# Architecture\n\n## Components\n\nPriority: 1\n\n- **Parser** reads the flags and builds the options for every later stage of the run\n- Runner executes the plan\n\n### Notes\n\nA long paragraph that needs wrapping because it is longer than the narrow terminal width of eighty columns, so it wraps.\n")
	mustWrite(t, filepath.Join(root, "task_spec.json"), `{"title":"Add flags","scope":"cli"}`)

	out := map[string]string{}
	view := func(name, s string) { out[name] = ansi.Strip(s) }

	revise := func(string, string) error { return nil }
	av := newArtifactViewer(root, "architecture.md", w, h, revise)
	view("artifact", av.View())
	av2, _ := av.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	view("artifact-feedback", av2.View())
	av3, _ := av.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	view("artifact-edit", av3.View())
	js := newArtifactViewer(root, "task_spec.json", w, h, nil)
	view("artifact-json", js.View())

	fp := NewFilePreview(root, w, h)
	fp.Show("main.go", false)
	view("preview", fp.View())
	fp.SetFocused(true)
	view("preview-focused", fp.View())

	ed := NewEditor("# Task\nBuild the thing")
	ed.SetSize(w, h)
	view("editor", ed.View())
	edc, _ := ed.Update(tea.KeyMsg{Type: tea.KeyEsc})
	view("editor-confirm", edc.View())

	ch := NewChat(nil, "sys").WithFileContext(root).WithReviseFn(func(string, string) error { return nil })
	ch.SetSize(w, h)
	ch.lines = []chatLine{
		{role: "user", content: "Plan a CLI that parses flags"},
		{role: "assistant", content: "## Vision\n\nMust have\n- **Flag parser** with `--help`\n1. First step\nPriority: high\n> a quote"},
	}
	ch.input = "type a long reply here"
	ch.cursor = runeLen(ch.input)
	ch.refreshViewport()
	view("chat", ch.View())
	chp, _ := ch.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	view("chat-picker", chp.View())

	ng := NewNegotiate(func(string) {}, root)
	ng.SetSize(w, h)
	ng.SeedContext("Add a flag parser to the CLI")
	ng.AddPMMessage("## Vision\n\nShould have\n- **Parser** for flags\nWhat is the target platform?")
	ng.Prefill("Linux and macOS")
	view("negotiate", ng.View())
	ng.SetWaiting()
	view("negotiate-waiting", ng.View())
	ngp, _ := ng.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	view("negotiate-picker", ngp.View())
	return out
}

var allCapsWord = regexp.MustCompile(`\b[A-Z]{4,}\b`)

func TestRestyledScreensFitAndHaveNoAllCapsLabels(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		w, h := size[0], size[1]
		for name, v := range restyledScreens(t, w, h) {
			lines := strings.Split(v, "\n")
			if len(lines) > h {
				t.Errorf("%s at %dx%d: %d lines, want at most %d", name, w, h, len(lines), h)
			}
			for i, l := range lines {
				if lw := ansi.StringWidth(l); lw > w {
					t.Errorf("%s at %dx%d: line %d is %d wide: %q", name, w, h, i, lw, l)
				}
			}
			if m := allCapsWord.FindString(v); m != "" {
				t.Errorf("%s at %dx%d has an ALL-CAPS label %q", name, w, h, m)
			}
		}
	}
}

func TestRestyledScreensUseSharedChrome(t *testing.T) {
	s := restyledScreens(t, 120, 34)
	for _, name := range []string{"artifact", "editor", "chat", "negotiate", "chat-picker"} {
		if !strings.Contains(strings.Split(s[name], "\n")[0], "◆") {
			t.Errorf("%s: first line is not the shared header: %q", name, strings.Split(s[name], "\n")[0])
		}
	}
	for name, v := range s {
		if strings.ContainsAny(v, "┌┐└┘├┤┬┴") {
			t.Errorf("%s still draws a frame:\n%s", name, v)
		}
	}
	if !strings.Contains(s["artifact"], "[2/") || !strings.Contains(s["artifact"], "Ctrl+A approve") {
		t.Errorf("artifact viewer lost its position or hints:\n%s", s["artifact"])
	}
}

// TestRestyledScreensRenderLight proves the screens render with the light
// palette: the light Text colour is emitted and the dark one is not.
func TestRestyledScreensRenderLight(t *testing.T) {
	withColor(t)
	ui.Apply("light", nil)
	t.Cleanup(func() { ui.Apply("dark", nil) })

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.md"), "# Title\n\n- item\n")
	av := newArtifactViewer(root, "a.md", 100, 30, nil)
	ed := NewEditor("hello")
	ed.SetSize(100, 30)
	fp := NewFilePreview(root, 100, 30)
	fp.Show("a.md", false)
	ch := NewChat(nil, "").WithFileContext(root)
	ch.SetSize(100, 30)
	ch.lines = []chatLine{{role: "assistant", content: "## Heading\n- bullet `code`"}}
	ch.refreshViewport()
	ng := NewNegotiate(func(string) {}, root)
	ng.SetSize(100, 30)
	ng.AddPMMessage("Must have\n- thing")

	lightText := termenv.TrueColor.Color(ui.Text.Light).Sequence(false)
	darkText := termenv.TrueColor.Color(ui.Text.Dark).Sequence(false)
	for name, v := range map[string]string{"artifact": av.View(), "editor": ed.View(), "preview": fp.View(), "chat": ch.View(), "negotiate": ng.View()} {
		if !strings.Contains(v, lightText) {
			t.Errorf("%s: light Text colour missing", name)
		}
		if strings.Contains(v, darkText) {
			t.Errorf("%s: dark Text colour used in the light theme", name)
		}
	}
}

func TestCockpitUsesSharedHeaderAndHintBar(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 34)
	lines := strings.Split(ansi.Strip(c.View()), "\n")
	if len(lines) > 34 {
		t.Errorf("cockpit is %d lines, want at most 34", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 120 {
			t.Errorf("line %d is %d wide", i, w)
		}
	}
	if !strings.HasPrefix(lines[0], "◆ ") {
		t.Errorf("first line is not the shared header: %q", lines[0])
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "Tab focus") || !strings.Contains(last, "q quit") {
		t.Errorf("last line is not the shared hint bar: %q", last)
	}
}

// TestNoGalaxyColours keeps the old palette out of the package: no hex
// literals and no galaxy identifiers in any non-test file.
func TestNoGalaxyColours(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	bad := regexp.MustCompile(`#[0-9a-fA-F]{6}\b|galaxy|\bcrt\.|paintBackground|screenHeader|lipgloss\.Color`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := bad.Find(data); m != nil {
			t.Errorf("%s still uses the old palette: %q", f, m)
		}
	}
}

// TestRenderSamplesGroup3 logs plain-text samples of the restyled screens
// (go test -v -run TestRenderSamplesGroup3).
func TestRenderSamplesGroup3(t *testing.T) {
	s := restyledScreens(t, 120, 34)
	for _, name := range []string{"artifact", "chat", "negotiate"} {
		t.Logf("\n--- %s 120x34\n%s", name, s[name])
	}
	e := restyledScreens(t, 100, 30)
	t.Logf("\n--- editor 100x30\n%s", e["editor"])
	c, _ := cockpitFixture(t)
	c.SetSize(120, 34)
	t.Logf("\n--- cockpit 120x34\n%s", ansi.Strip(c.View()))
}
