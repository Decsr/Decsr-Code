package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRunnerGlyph_Unicode(t *testing.T) {
	want := map[string]string{
		"claude": "✳", "codex": "◎", "opencode": "⬡", "copilot": "◈", "deepseek": "◆",
		"openai": "○", "ollama": "●", "lmstudio": "●", "mlx": "●", "nonesuch": "·",
		"": "⬡", // the default runner is opencode
	}
	for runner, g := range want {
		if got := runnerGlyph(runner, ""); got != g {
			t.Errorf("runnerGlyph(%q, default) = %q, want %q", runner, got, g)
		}
		if got := runnerGlyph(runner, "unicode"); got != g {
			t.Errorf("runnerGlyph(%q, unicode) = %q, want %q", runner, got, g)
		}
	}
}

func TestRunnerGlyph_ASCII(t *testing.T) {
	want := map[string]string{
		"claude": "[C]", "codex": "[X]", "opencode": "[O]", "copilot": "[G]",
		"deepseek": "[D]", "openai": "[A]", "ollama": "[L]", "mlx": "[L]", "nonesuch": "[?]",
	}
	for runner, g := range want {
		if got := runnerGlyph(runner, "ascii"); got != g {
			t.Errorf("runnerGlyph(%q, ascii) = %q, want %q", runner, got, g)
		}
	}
}

func TestRunnerGlyph_NerdFallsBackToUnicode(t *testing.T) {
	if got := runnerGlyph("claude", "nerd"); got == "" || got == "✳" {
		t.Errorf("nerd glyph for claude = %q, want a Nerd Font glyph", got)
	}
	if got := runnerGlyph("nonesuch", "nerd"); got != "·" {
		t.Errorf("unknown runner in nerd mode = %q, want the unicode fallback", got)
	}
}

func TestRunnerGlyph_SingleCell(t *testing.T) {
	for runner := range runnerGlyphs {
		if w := lipgloss.Width(runnerGlyph(runner, "unicode")); w != 1 {
			t.Errorf("unicode glyph of %s is %d cells, want 1", runner, w)
		}
	}
}
