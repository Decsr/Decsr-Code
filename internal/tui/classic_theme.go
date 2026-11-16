package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// Colour helpers of the frozen classic pipeline. They map its old
// colour set onto the shared ui tokens by meaning: blue is Accent, mint is OK,
// gold and orange are Warn, red is Fail, grey is Muted and frames are Border.

// classicRoleColor is the colour of an agent role. Only roles with a meaning
// get a colour of their own; the role name always sits next to it.
func classicRoleColor(role string) lipgloss.TerminalColor {
	switch role {
	case "pm", "architect", "planner", "qa", "tester":
		return ui.Accent
	case "coder_fixer", "security":
		return ui.Warn
	case "reviewer":
		return ui.Fail
	case "system":
		return ui.Muted
	}
	return ui.Text
}

// classicPhaseColor is the colour of an active pipeline phase label.
func classicPhaseColor(label string) lipgloss.TerminalColor {
	switch label {
	case "FIXING", "CODER_FIXER", "SEC", "SECURITY":
		return ui.Warn
	case "DONE":
		return ui.OK
	case "REVIEW", "REVIEWING":
		return ui.Fail
	case "PM", "QA_TESTS", "QA_REVIEW", "QA", "ARCHITECT", "ARCHITECTURE", "PLAN", "PROMPTS", "TEST", "TESTING":
		return ui.Accent
	}
	return ui.Text
}

// classicChipStyle renders text as a one-cell-padded bold label in one colour.
// The old pixel chips drew a coloured block behind the text; the shared look
// keeps the background for the selected row only.
type classicChipStyle struct{ fg lipgloss.TerminalColor }

// Render implements the same call shape as lipgloss.Style for chip users.
func (c classicChipStyle) Render(s string) string {
	return lipgloss.NewStyle().Foreground(c.fg).Bold(true).Render(" " + s + " ")
}

// classicPanelTop builds the top edge of a classic panel with the title in it:
// ┌─ Title ────────┐ (w is the full width including the corners).
func classicPanelTop(w int, title string, titleColor, borderColor lipgloss.TerminalColor) string {
	bs := lipgloss.NewStyle().Foreground(borderColor)
	if w < 2 {
		return ""
	}
	if title == "" || w < 8 {
		return bs.Render("┌" + strings.Repeat("─", w-2) + "┐")
	}
	label := " " + ansi.Truncate(title, w-6, "…") + " "
	fill := max(w-3-ansi.StringWidth(label), 0)
	return bs.Render("┌─") +
		lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(label) +
		bs.Render(strings.Repeat("─", fill)+"┐")
}

// classicChip renders label as a bold coloured tag.
func classicChip(label string, color lipgloss.TerminalColor) string {
	return classicChipStyle{fg: color}.Render(label)
}

// State styles of the classic agent pills.
var (
	classicWaiting = lipgloss.NewStyle().Foreground(ui.Muted)
	classicRunning = lipgloss.NewStyle().Foreground(ui.Accent).Bold(true)
	classicFixing  = lipgloss.NewStyle().Foreground(ui.Warn).Bold(true)
	classicDone    = lipgloss.NewStyle().Foreground(ui.OK).Bold(true)
	classicError   = lipgloss.NewStyle().Foreground(ui.Fail).Bold(true)
	classicGate    = lipgloss.NewStyle().Foreground(ui.Warn).Bold(true)
)

// classicGateBanner is the approval banner: bold Warn text, no fill.
var classicGateBanner = lipgloss.NewStyle().Foreground(ui.Warn).Bold(true).Padding(0, 1)
