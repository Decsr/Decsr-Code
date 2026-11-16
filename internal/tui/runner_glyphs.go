package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// Icon sets for ui.icons.
const (
	iconsUnicode = "unicode"
	iconsASCII   = "ascii"
	iconsNerd    = "nerd"
)

// runnerGlyphSet is one runner's glyph in each icon set.
type runnerGlyphSet struct{ unicode, ascii, nerd string }

// runnerGlyphs maps a runner name to its glyphs. Local runners share one
// glyph. A runner that is not listed gets runnerGlyphUnknown.
var runnerGlyphs = map[string]runnerGlyphSet{
	"claude":   {"✳", "[C]", ""},
	"codex":    {"◎", "[X]", ""},
	"opencode": {"⬡", "[O]", ""},
	"copilot":  {"◈", "[G]", ""},
	"deepseek": {"◆", "[D]", ""},
	"openai":   {"○", "[A]", ""},
	"ollama":   {"●", "[L]", ""},
	"lmstudio": {"●", "[L]", ""},
	"mlx":      {"●", "[L]", ""},
}

var runnerGlyphUnknown = runnerGlyphSet{"·", "[?]", ""}

// runnerKey normalises a runner name; the empty runner is the default, opencode.
func runnerKey(runner string) string {
	if runner == "" {
		return "opencode"
	}
	return runner
}

// runnerGlyph returns the glyph of a runner in the given icon set
// ("unicode", "ascii", "nerd"; anything else is unicode). An empty nerd glyph
// falls back to the unicode one.
func runnerGlyph(runner, icons string) string {
	set, ok := runnerGlyphs[runnerKey(runner)]
	if !ok {
		set = runnerGlyphUnknown
	}
	switch icons {
	case iconsASCII:
		return set.ascii
	case iconsNerd:
		if set.nerd != "" {
			return set.nerd
		}
	}
	return set.unicode
}

// runnerColor is the colour of a runner's glyph: a brand colour for the two
// that have one, green for local runners, muted for the rest.
func runnerColor(runner string) lipgloss.TerminalColor {
	switch runnerKey(runner) {
	case "claude":
		return ui.Claude
	case "deepseek":
		return ui.DeepSeek
	case "ollama", "lmstudio", "mlx":
		return ui.OK
	}
	return ui.Muted
}

// runnerBadge is the coloured glyph of a runner.
func runnerBadge(runner, icons string) string {
	return lipgloss.NewStyle().Foreground(runnerColor(runner)).Render(runnerGlyph(runner, icons))
}
