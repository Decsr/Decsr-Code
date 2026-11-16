package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// pixelSpinner is a quadrant-block spinner (the braille spinner.Dot frames are
// missing from Monaspace Krypton).
var pixelSpinner = spinner.Spinner{
	Frames: []string{"▘", "▝", "▗", "▖"},
	FPS:    time.Second / 8,
}

// keyHint is one entry of a key list: the key and what it does.
type keyHint struct{ key, label string }

// Suggested prompt shown as ghost text in the empty Workspace prompt; italic
// so it also reads without color.
var styleWsGhost = lipgloss.NewStyle().Foreground(ui.Muted).Italic(true)

// Skills of a node (inspector, picker) and the plugin dialogs. The state is
// always spelled out in the text; the style only supports it.
var (
	styleWsSkill     = lipgloss.NewStyle().Foreground(ui.Text)
	styleWsSkillNote = lipgloss.NewStyle().Foreground(ui.Muted)
	styleWsSkillWarn = lipgloss.NewStyle().Foreground(ui.Fail)
	styleWsPickRow   = lipgloss.NewStyle().Background(ui.Sel)
)
