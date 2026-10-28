package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
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

// Galaxy accent palette shared by roles, pipeline steps and widgets.
var (
	galaxyViolet   = lipgloss.Color("#9d7cff") // nebula violet
	galaxyPurple   = lipgloss.Color("#c77dff") // nebula purple
	galaxyBlue     = lipgloss.Color("#7c9cff") // nebula blue
	galaxyMint     = lipgloss.Color("#6ff2b4") // aurora mint
	galaxyMintDeep = lipgloss.Color("#4fcf95") // deeper aurora mint
	galaxyPink     = lipgloss.Color("#ff7ac6") // cosmic pink
	galaxyGold     = lipgloss.Color("#ffd166") // star gold
	galaxyOrange   = lipgloss.Color("#ffa36b") // solar orange
	galaxyRed      = lipgloss.Color("#ff5c7a") // pulsar red
	galaxyCyan     = lipgloss.Color("#5ce1e6") // comet cyan
	galaxyIce      = lipgloss.Color("#9ee8ff") // ice
)

// Derived surface shades (galaxy theme).
var (
	surfaceRaised = lipgloss.Color("#34343f") // raised surface: keycaps, selected rows
	focusBorder   = lipgloss.Color("#7a5fd9") // focused-panel border (deep nebula violet)
)

// pixelBorder is the shared square-cornered panel border (┌┐└┘─│).
var pixelBorder = lipgloss.NormalBorder()

// pixelSpinner is a quadrant-block spinner (the braille spinner.Dot frames are
// missing from Monaspace Krypton).
var pixelSpinner = spinner.Spinner{
	Frames: []string{"▘", "▝", "▗", "▖"},
	FPS:    time.Second / 8,
}

// Role colors — each agent gets a distinct accent against the dark base.
var roleColors = map[string]lipgloss.Color{
	"pm":          galaxyViolet,   // nebula violet — project manager
	"coder":       galaxyMint,     // aurora mint
	"coder_fixer": galaxyMintDeep, // deeper mint — fixer variant
	"qa":          galaxyPink,     // cosmic pink — QA (tests + review)
	"ux_reviewer": galaxyGold,     // star gold
	"security":    galaxyRed,      // pulsar red
	"pr":          galaxyCyan,     // comet cyan
	"system":      crt.dim,
	// Deprecated roles — kept for legacy Pipeline.
	"architect": galaxyCyan,
	"planner":   galaxyViolet,
	"tester":    galaxyPink,
	"reviewer":  galaxyRed,
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
	"FIXING":       galaxyGold,
	"DONE":         galaxyCyan,
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
	styleFixing  = lipgloss.NewStyle().Foreground(galaxyGold).Bold(true)
	styleDone    = lipgloss.NewStyle().Foreground(galaxyCyan).Bold(true)
	styleError   = lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
	styleGate    = lipgloss.NewStyle().Foreground(galaxyPink).Bold(true)
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

// ── Shared pixel chrome helpers ──────────────────────────────────────────────

// lerpColor linearly blends two "#rrggbb" colours (t in 0..1).
func lerpColor(a, b lipgloss.Color, t float64) lipgloss.Color {
	parse := func(c lipgloss.Color) (int, int, int) {
		var r, g, bl int
		_, _ = fmt.Sscanf(string(c), "#%02x%02x%02x", &r, &g, &bl)
		return r, g, bl
	}
	ar, ag, ab := parse(a)
	br, bg, bb := parse(b)
	mix := func(x, y int) int { return x + int(float64(y-x)*t+0.5) }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb)))
}

// screenHeader renders the slim header bar shared by every screen:
// pixel mark + DECSR + screen name on the left, project name on the right.
func screenHeader(width int, screen, project string) string {
	bar := lipgloss.NewStyle().Background(crt.panelBg)
	left := bar.Foreground(galaxyViolet).Bold(true).Render(" ◆ ") +
		bar.Foreground(crt.bright).Bold(true).Render("DECSR") +
		bar.Foreground(crt.border).Render("  ▌ ") +
		bar.Foreground(galaxyViolet).Bold(true).Render(strings.ToUpper(screen))
	right := ""
	if project != "" {
		right = bar.Foreground(crt.dim).Render(project + " ")
	}
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
		right = ""
	}
	out := left + bar.Render(strings.Repeat(" ", gap)) + right
	if width > 0 && ansi.StringWidth(out) > width {
		out = ansi.Truncate(out, width, "")
	}
	return out
}

// keycap renders a key on a slightly raised background.
func keycap(k string) string {
	return lipgloss.NewStyle().Background(surfaceRaised).Foreground(crt.bright).Bold(true).Render(" " + k + " ")
}

// keyHint is one footer entry: a keycap plus a dim label.
type keyHint struct{ key, label string }

// hintBar renders footer key hints (keycap + dim label) on the panel bar,
// padded to width, with an optional right-aligned segment (already styled).
func hintBar(width int, hints []keyHint, right string) string {
	bar := lipgloss.NewStyle().Background(crt.panelBg)
	dim := bar.Foreground(crt.dim)
	var parts []string
	for _, h := range hints {
		parts = append(parts, keycap(h.key)+dim.Render(" "+h.label))
	}
	left := bar.Render(" ") + strings.Join(parts, bar.Render("  "))
	if right != "" {
		right += bar.Render(" ")
	}
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
	}
	out := left + bar.Render(strings.Repeat(" ", gap)) + right
	if width > 0 && ansi.StringWidth(out) > width {
		out = ansi.Truncate(out, width, "")
	}
	return out
}

// chip renders a small coloured pixel chip: dark text on an accent block.
func chip(label string, color lipgloss.Color) string {
	return lipgloss.NewStyle().Background(color).Foreground(crt.bg).Bold(true).Render(" " + label + " ")
}

// panelTop builds the top border line of a pixel panel with the title embedded:
// ┌─ ◆ TITLE ────────┐ (w is the full panel width including corners).
func panelTop(w int, title string, titleColor, borderColor lipgloss.Color) string {
	bs := lipgloss.NewStyle().Foreground(borderColor)
	if w < 2 {
		return ""
	}
	if title == "" || w < 8 {
		return bs.Render("┌" + strings.Repeat("─", w-2) + "┐")
	}
	t := ansi.Truncate(title, w-6, "…")
	label := " " + t + " "
	fill := w - 3 - ansi.StringWidth(label)
	if fill < 0 {
		fill = 0
	}
	return bs.Render("┌─") +
		lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(label) +
		bs.Render(strings.Repeat("─", fill)+"┐")
}

// pixelPanel renders body inside a square-cornered border (padding/width taken
// from st) with the title embedded in the top edge. A focused panel uses the
// violet focus border; otherwise crt.border.
func pixelPanel(title string, titleColor lipgloss.Color, focused bool, st lipgloss.Style, body string) string {
	bc := crt.border
	if focused {
		bc = focusBorder
	}
	out := st.Border(pixelBorder, false, true, true, true).BorderForeground(bc).Render(body)
	return panelTop(lipgloss.Width(out), title, titleColor, bc) + "\n" + out
}

// chipStyle renders text as a one-cell-padded bold chip; an empty bg draws
// plain padded text (used for pending phases).
type chipStyle struct{ fg, bg lipgloss.Color }

// Render implements the same call shape as lipgloss.Style for chip users.
func (c chipStyle) Render(s string) string {
	st := lipgloss.NewStyle().Foreground(c.fg).Bold(true)
	if c.bg != "" {
		st = st.Background(c.bg)
	}
	return st.Render(" " + s + " ")
}
