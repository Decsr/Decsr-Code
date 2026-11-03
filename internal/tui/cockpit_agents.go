package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// spinnerFrame returns the pixelSpinner frame for the current time.
func spinnerFrame() string {
	f := pixelSpinner.Frames
	return f[int(time.Now().UnixNano()/int64(pixelSpinner.FPS))%len(f)]
}

// agentStripGlyph returns the status glyph for an agent chip.
func agentStripGlyph(a *cockpitAgent) string {
	switch a.status {
	case AgentRunning, AgentFixing:
		return spinnerFrame()
	case AgentDone:
		return "✓"
	case AgentError:
		return "!"
	default:
		return "○"
	}
}

// renderAgentStrip renders one line with a chip per agent. Wide (>=110)
// chips carry call counts; narrow ones show glyph, short name and tokens.
func (c *CockpitModel) renderAgentStrip(w int) string {
	var parts []string
	for _, role := range agentOrder {
		a := c.agents[role]
		if a == nil {
			continue
		}
		tokens := "—"
		if a.tokens > 0 {
			tokens = formatTokensCompact(a.tokens)
		}
		running := a.status == AgentRunning || a.status == AgentFixing
		style := lipgloss.NewStyle().Foreground(roleColor(string(role)))
		var text string
		if w >= 110 {
			text = fmt.Sprintf("[%s %s %s ×%d]", agentStripGlyph(a), roleShort(role), tokens, a.calls)
		} else {
			text = fmt.Sprintf("%s %s %s", agentStripGlyph(a), roleShort(role), tokens)
		}
		if running {
			style = style.Background(surfaceRaised).Bold(true)
		}
		parts = append(parts, style.Render(text))
	}
	sep := " "
	if w >= 110 {
		sep = "  "
	}
	line := strings.Join(parts, sep)
	if ansi.StringWidth(line) > w {
		line = ansi.Truncate(line, w, "…")
	}
	return line
}

// phaseStrip renders one segment per phase, colored by status.
func (c *CockpitModel) phaseStrip() string {
	var b strings.Builder
	for _, p := range c.phases {
		st := lipgloss.NewStyle()
		seg := "▰"
		switch p.status {
		case "done":
			st = st.Foreground(galaxyCyan)
		case "active":
			switch {
			case c.failed:
				st = st.Foreground(galaxyRed).Bold(true)
			case c.gate != "":
				st = st.Foreground(galaxyGold).Bold(true)
			default:
				st = st.Foreground(galaxyAzure).Bold(true)
			}
		default:
			st = st.Foreground(crt.dim)
			seg = "▱"
		}
		b.WriteString(st.Render(seg))
	}
	return b.String()
}
