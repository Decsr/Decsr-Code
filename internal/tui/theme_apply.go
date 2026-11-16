package tui

import (
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// ApplyTheme selects the colour theme from ui.theme. Call it once at startup,
// before the first render; an invalid value counts as auto.
func ApplyTheme(cfg config.Config) { ui.Apply(cfg.UI.Theme, nil) }
