package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ── Galaxy × Pixel Theme ─────────────────────────────────────────────────────
// Dark-grey base with starlight text and nebula / aurora / pulsar accents.

// galaxyTheme defines the base palette of the TUI.
type galaxyTheme struct {
	primary  lipgloss.Color // main text
	dim      lipgloss.Color // subdued / secondary text
	bright   lipgloss.Color // high-intensity highlights
	bg       lipgloss.Color // deep background
	border   lipgloss.Color // panel borders
	panelBg  lipgloss.Color // panel background tint
	warn     lipgloss.Color // warning / danger accent
	success  lipgloss.Color // success indicator
	muted    lipgloss.Color // very dim, chrome-level
	scanline lipgloss.Color // scanline stripe tint
}

// themeAmber is the previous amber CRT palette, kept so it can be switched back.
var themeAmber = galaxyTheme{
	primary:  lipgloss.Color("#ffc66d"),
	dim:      lipgloss.Color("#6b553f"),
	bright:   lipgloss.Color("#ffe4a8"),
	bg:       lipgloss.Color("#0b0c0b"),
	border:   lipgloss.Color("#3a3020"),
	panelBg:  lipgloss.Color("#0d1117"),
	warn:     lipgloss.Color("#ff6633"),
	success:  lipgloss.Color("#c8b040"),
	muted:    lipgloss.Color("#3a3025"),
	scanline: lipgloss.Color("#111111"),
}

var themeGalaxy = galaxyTheme{
	primary:  lipgloss.Color("#c8c6d8"), // starlight
	dim:      lipgloss.Color("#77758a"),
	bright:   lipgloss.Color("#f4f2ff"),
	bg:       lipgloss.Color("#1b1b1f"),
	border:   lipgloss.Color("#3a3a44"),
	panelBg:  lipgloss.Color("#232329"),
	warn:     lipgloss.Color("#ff5c7a"), // pulsar red
	success:  lipgloss.Color("#6ff2b4"), // aurora mint
	muted:    lipgloss.Color("#2c2c33"),
	scanline: lipgloss.Color("#202026"),
}

// crt is the active theme used across the TUI.
var crt = themeGalaxy

// Role colors — each agent gets a distinct accent against the dark base.
var roleColors = map[string]lipgloss.Color{
	"pm":          lipgloss.Color("#9d7cff"), // nebula violet — project manager
	"coder":       lipgloss.Color("#6ff2b4"), // aurora mint
	"coder_fixer": lipgloss.Color("#4fcf95"), // deeper mint — fixer variant
	"qa":          lipgloss.Color("#ff7ac6"), // cosmic pink — QA (tests + review)
	"ux_reviewer": lipgloss.Color("#ffd166"), // star gold
	"security":    lipgloss.Color("#ff5c7a"), // pulsar red
	"pr":          lipgloss.Color("#5ce1e6"), // comet cyan
	"system":      crt.dim,
	// Deprecated roles — kept for legacy Pipeline.
	"architect": lipgloss.Color("#5ce1e6"),
	"planner":   lipgloss.Color("#9d7cff"),
	"tester":    lipgloss.Color("#ff7ac6"),
	"reviewer":  lipgloss.Color("#ff5c7a"),
}

func roleColor(role string) lipgloss.Color {
	if c, ok := roleColors[role]; ok {
		return c
	}
	return crt.dim
}

// pipelineColors maps pipeline step labels to distinct colors for the phase bar.
var pipelineColors = map[string]lipgloss.Color{
	"PM":           roleColors["pm"],
	"CODE":         roleColors["coder"],
	"CODING":       roleColors["coder"],
	"CODER":        roleColors["coder"],
	"QA_TESTS":     roleColors["qa"],
	"QA_REVIEW":    roleColors["qa"],
	"QA":           roleColors["qa"],
	"UX":           roleColors["ux_reviewer"],
	"UX_REVIEWING": roleColors["ux_reviewer"],
	"UX_REVIEWER":  roleColors["ux_reviewer"],
	"SEC":          roleColors["security"],
	"SECURITY":     roleColors["security"],
	"FIXING":       lipgloss.Color("#ffd166"),
	"DONE":         lipgloss.Color("#5ce1e6"),
	// Legacy entries.
	"ARCHITECT":    roleColors["architect"],
	"ARCHITECTURE": roleColors["architect"],
	"PLAN":         roleColors["planner"],
	"PROMPTS":      roleColors["planner"],
	"TEST":         roleColors["tester"],
	"TESTING":      roleColors["tester"],
	"REVIEW":       roleColors["reviewer"],
	"REVIEWING":    roleColors["reviewer"],
}

// Panel state styles — phosphor monochrome.
var (
	styleWaiting = lipgloss.NewStyle().Foreground(crt.dim)
	styleRunning = lipgloss.NewStyle().Foreground(crt.primary).Bold(true)
	styleFixing  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd166")).Bold(true)
	styleDone    = lipgloss.NewStyle().Foreground(lipgloss.Color("#5ce1e6")).Bold(true)
	styleError   = lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
	styleGate    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff7ac6")).Bold(true)
)

// Gate banner — phosphor highlight.
var styleGateBanner = lipgloss.NewStyle().
	Background(crt.border).
	Foreground(crt.bright).
	Bold(true).
	Padding(0, 2)

// Conversation line.
var styleConvTimestamp = lipgloss.NewStyle().Foreground(crt.dim)

// Status bar — CRT terminal footer.
var styleStatusBar = lipgloss.NewStyle().
	Background(crt.panelBg).
	Foreground(crt.dim).
	Padding(0, 1)

var styleStatusKey = lipgloss.NewStyle().
	Background(crt.panelBg).
	Foreground(crt.primary).
	Bold(true).
	Padding(0, 1)

// paintBackground fills the whole width x height screen with crt.bg. Inner
// styled segments end with an SGR reset that would drop the terminal back to
// its default background, so the background is re-applied after every reset.
// Returns view unchanged when the size is unknown or the profile has no colour.
func paintBackground(view string, width, height int) string {
	if width <= 0 || height <= 0 {
		return view
	}
	// Render a marker to obtain the profile-aware background sequence.
	seq, _, ok := strings.Cut(lipgloss.NewStyle().Background(crt.bg).Render("x"), "x")
	if !ok || seq == "" {
		return view
	}
	const reset = "\x1b[0m"
	lines := strings.Split(view, "\n")
	var b strings.Builder
	for i := 0; i < height || i < len(lines); i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(seq)
		for _, r := range []string{"\x1b[0m", "\x1b[m", "\x1b[49m"} {
			line = strings.ReplaceAll(line, r, r+seq)
		}
		b.WriteString(line)
		if pad := width - ansi.StringWidth(line); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString(reset)
	}
	return b.String()
}
