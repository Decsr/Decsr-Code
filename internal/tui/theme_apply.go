package tui

import (
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// ApplyTheme selects the colour theme from ui.theme and whether messages are
// rendered as Markdown from ui.markdown. Call it once at startup, before the
// first render; an invalid theme counts as auto.
func ApplyTheme(cfg config.Config) {
	ui.Apply(cfg.UI.Theme, nil)
	markdownOff.Store(!cfg.UI.MarkdownOn())
}
