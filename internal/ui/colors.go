package ui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Group names of the slash commands, in display order.
const (
	GroupNameSession = "Session"
	GroupNameRun     = "Run"
	GroupNameAgents  = "Agents"
	GroupNameUsage   = "Usage"
	GroupNameProject = "Project"
	GroupNamePlugin  = "Plugin commands"
)

// GroupColor is the colour of a command group; an unknown group is Accent.
func GroupColor(group string) lipgloss.AdaptiveColor {
	switch group {
	case GroupNameSession:
		return GroupSession
	case GroupNameRun:
		return GroupRun
	case GroupNameAgents:
		return GroupAgents
	case GroupNameUsage:
		return GroupUsage
	case GroupNameProject:
		return GroupProject
	}
	return Accent
}

// RunnerColor is the colour of a runner. The empty runner is the default one,
// opencode; a runner without a colour of its own is Muted.
func RunnerColor(name string) lipgloss.TerminalColor {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claude":
		return Claude
	case "dsh", "deepseek":
		return DeepSeek
	case "codex", "openai":
		return Codex
	case "", "opencode":
		return OpenCode
	case "ollama", "mlx", "lmstudio":
		return Local
	}
	return Muted
}

var (
	rolePM       = lipgloss.AdaptiveColor{Dark: "#b48cff", Light: "#6d3fd1"} // violet
	roleCoder    = lipgloss.AdaptiveColor{Dark: "#6ea8ff", Light: "#2f6fdb"} // blue
	roleFixer    = lipgloss.AdaptiveColor{Dark: "#ffa14a", Light: "#c2610c"} // orange
	roleReviewer = lipgloss.AdaptiveColor{Dark: "#5ee0ff", Light: "#0e7490"} // cyan
	roleQA       = lipgloss.AdaptiveColor{Dark: "#6ff2b4", Light: "#2f9e44"} // green
	roleSecurity = lipgloss.AdaptiveColor{Dark: "#ff6b8e", Light: "#d6336c"} // red-pink
	roleUX       = lipgloss.AdaptiveColor{Dark: "#ff7ac6", Light: "#c2255c"} // pink

	// roleHues colour any role without a colour of its own, by a stable hash
	// of its name.
	roleHues = []lipgloss.AdaptiveColor{
		{Dark: "#e8c468", Light: "#a16207"},
		{Dark: "#7ad7c4", Light: "#0f766e"},
		{Dark: "#c49cff", Light: "#7c3aed"},
		{Dark: "#8fd16a", Light: "#4d7c0f"},
		{Dark: "#f08fa0", Light: "#be185d"},
		{Dark: "#6fc3ff", Light: "#0369a1"},
		{Dark: "#f0a36b", Light: "#c2410c"},
		{Dark: "#b0b8d8", Light: "#475569"},
	}
)

// RoleColor is the colour of a role: fixed for the built-in roles, otherwise
// one of eight hues picked by a stable hash of the name.
func RoleColor(name string) lipgloss.TerminalColor {
	switch name {
	case "pm":
		return rolePM
	case "coder":
		return roleCoder
	case "coder_fixer":
		return roleFixer
	case "reviewer":
		return roleReviewer
	case "qa":
		return roleQA
	case "security":
		return roleSecurity
	case "ux_reviewer":
		return roleUX
	}
	if name == "" {
		return Muted
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return roleHues[int(h.Sum32()%uint32(len(roleHues)))]
}

// lerpColor blends two adaptive colours (t in 0..1), in both variants.
func lerpColor(a, b lipgloss.AdaptiveColor, t float64) lipgloss.AdaptiveColor {
	switch {
	case t <= 0:
		return a
	case t >= 1:
		return b
	}
	return lipgloss.AdaptiveColor{Dark: lerpHex(a.Dark, b.Dark, t), Light: lerpHex(a.Light, b.Light, t)}
}

// MeterColor is the colour of a meter at a fraction: green, amber at the
// middle, red at the end.
func MeterColor(frac float64) lipgloss.AdaptiveColor {
	frac = min(max(frac, 0), 1)
	if frac < 0.5 {
		return lerpColor(OK, Warn, frac*2)
	}
	return lerpColor(Warn, Fail, (frac-0.5)*2)
}

// SparkColor is the colour of a sparkline glyph at a height fraction: teal
// when low, amber when high.
func SparkColor(frac float64) lipgloss.AdaptiveColor {
	return lerpColor(GroupProject, GroupUsage, min(max(frac, 0), 1))
}
