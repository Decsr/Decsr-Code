package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/decsr"
	"github.com/Decsr/Decsr-Code/internal/lang"
)

// ── Actions ──────────────────────────────────────────────────────────────────

type homeAction int

const (
	homeActionNewTask homeAction = iota
	homeActionRunPipeline
	homeActionResume
	homeActionResumeProject
	homeActionDoneTasks
	homeActionOpenProject
	homeActionGlobalSettings
	homeActionModelMemory
	homeActionSetup
	homeActionClean
	homeActionQuit
	homeActionWorkspace
)

type homeSelectedMsg struct{ action homeAction }

type homeMenuItem struct {
	icon   string
	label  string
	desc   string
	action homeAction
	key    string
}

// ── Color palette (centralised) ──────────────────────────────────────────────

var homePalette = struct {
	accent, green, dim, bright, gold, cyan, red lipgloss.Color
	border, activeBg, headerBg, footerBg        lipgloss.Color
}{
	accent:   galaxyAzure, // azure accent for titles
	green:    galaxyMint,  // aurora mint
	dim:      crt.dim,
	bright:   crt.bright,
	gold:     galaxyGold, // star gold
	cyan:     galaxyCyan, // comet cyan
	red:      galaxyRed,  // pulsar red
	border:   crt.border, // galaxy border
	activeBg: surfaceRaised,
	headerBg: crt.panelBg,
	footerBg: crt.panelBg,
}

// ── HomeModel ────────────────────────────────────────────────────────────────

// HomeModel is the first screen shown when DECSR launches.
type HomeModel struct {
	cfg     config.Config
	root    string
	wsPath  string
	cursor  int
	items   []homeMenuItem
	history []string
	width   int
	height  int

	// projectValid is false when root is not a real project directory
	// (e.g. home dir or missing project markers). Disables Run, Setup, Clean.
	projectValid bool

	// Cached project info — computed once in NewHomeModel, stable across renders.
	cachedRunner    string
	cachedModel     string
	cachedEffort    string
	cachedProject   string
	cachedLanguage  string
	cachedBranch    string
	cachedOverrides []agentOverride

	// Recent projects from ~/.decsr/projects.json.
	recentProjects []RecentProject

	showHelp      bool        // true while the keyboard help overlay is open
	confirmQuit   bool        // true when showing quit confirmation
	confirmAction *homeAction // destructive action awaiting y/Enter confirmation
	status        string      // transient message, cleared on the next keypress
	scrollX       int         // horizontal scroll offset (visible chars)

	viewport viewport.Model
	ready    bool // viewport initialised after first WindowSizeMsg
}

func NewHomeModel(cfg config.Config, root string) HomeModel {
	wsPath := filepath.Join(root, ".decsr")
	history := LoadHistory(wsPath)

	// Resolve project info ONCE — no map-iteration randomness on every render.
	runnerName, modelName, effortName := detectDefaultRunnerModel(cfg.Agents)
	if runnerName == "" {
		runnerName = "(not set)"
	}
	if modelName == "" {
		modelName = "(not set)"
	}

	projectName := cfg.Project.Name
	if projectName == "" && root != "" {
		base := filepath.Base(root)
		if base != "/" && base != "." {
			projectName = base
		}
	}
	if projectName == "" {
		projectName = "(unnamed)"
	}

	language := resolveLanguageFromRoot(root, cfg)
	gitBranch := GitBranch(root)
	overrides := resolveOverrides(cfg.Agents, runnerName, modelName, effortName)

	items := []homeMenuItem{
		{icon: "◉", label: "Workspace", desc: "Chat, split into nodes, assign agents", action: homeActionWorkspace, key: "w"},
		{icon: "▶", label: "Classic Pipeline", desc: "Start a PM chat to define a task — attach existing .md with Ctrl+F", action: homeActionNewTask, key: "1"},
		{icon: "■", label: "Open Project", desc: "Switch to another project directory", action: homeActionOpenProject, key: "o"},
		{icon: "◆", label: "Global Settings", desc: "Default provider, model & effort (~/.decsr/config.yaml)", action: homeActionGlobalSettings, key: "g"},
		{icon: "◈", label: "Model Memory", desc: "Cap local-model RAM or context size (auto RAM→context per agent)", action: homeActionModelMemory, key: "m"},
		{icon: "◇", label: "Project Setup", desc: "Per-agent runner, model & effort overrides", action: homeActionSetup, key: "s"},
		{icon: "✦", label: "Reset Artifacts", desc: "Remove generated plans & reports — keeps code, config, and project memory", action: homeActionClean, key: "c"},
		{icon: "×", label: "Quit", desc: "Exit DECSR", action: homeActionQuit, key: "q"},
	}

	// Show completed-task history when there is any.
	if done := decsr.DoneTasks(context.Background(), root); len(done) > 0 {
		historyItem := homeMenuItem{
			icon:   "✓",
			label:  "Done Tasks",
			desc:   fmt.Sprintf("Review %d completed task(s)", len(done)),
			action: homeActionDoneTasks,
			key:    "d",
		}
		items = append([]homeMenuItem{historyItem}, items...)
	}

	// Offer "Resume Project" when planning artifacts exist but no strictly
	// resumable (in-progress) task is detected — the PM re-reads .decsr
	// and plans only the remaining work.
	_, strictResumable := decsr.Resumable(context.Background(), root)
	if !strictResumable && decsr.HasReviewableArtifacts(root) {
		reviewItem := homeMenuItem{
			icon:   "↺",
			label:  "Resume Project",
			desc:   "PM reviews .decsr artifacts and plans the remaining work",
			action: homeActionResumeProject,
			key:    "r",
		}
		items = append([]homeMenuItem{reviewItem}, items...)
	}

	// Offer Resume at the top when an interrupted task is detected.
	if rt, ok := decsr.Resumable(context.Background(), root); ok {
		resumeItem := homeMenuItem{
			icon:   "↻",
			label:  "Resume Task",
			desc:   "Continue the interrupted task: " + rt.Title,
			action: homeActionResume,
			key:    "Enter",
		}
		items = append([]homeMenuItem{resumeItem}, items...)
	}

	return HomeModel{
		cfg:             cfg,
		root:            root,
		wsPath:          wsPath,
		history:         history,
		projectValid:    isValidProjectRoot(root),
		cachedRunner:    runnerName,
		cachedModel:     modelName,
		cachedEffort:    effortName,
		cachedProject:   projectName,
		cachedLanguage:  language,
		cachedBranch:    gitBranch,
		cachedOverrides: overrides,
		recentProjects:  LoadRecentProjects(),
		items:           items,
		width:           80,
		height:          24,
	}
}

func (m HomeModel) Init() tea.Cmd {
	return nil
}

// ── Update ───────────────────────────────────────────────────────────────────

func (m HomeModel) Update(msg tea.Msg) (HomeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scrollX = 0 // reset horizontal scroll on resize
		m.syncViewport()

	case tea.KeyMsg:
		// Quit confirmation mode intercepts all keys.
		if m.confirmQuit {
			switch msg.String() {
			case "y", "Y", "enter":
				return m, m.selectAction(homeActionQuit)
			default:
				m.confirmQuit = false
			}
			if m.ready {
				m.viewport.SetContent(m.renderContent())
			}
			return m, nil
		}

		// Destructive-action confirmation intercepts all keys too.
		if m.confirmAction != nil {
			action := *m.confirmAction
			m.confirmAction = nil
			switch msg.String() {
			case "y", "Y", "enter":
				return m, m.selectAction(action)
			}
			if m.ready {
				m.viewport.SetContent(m.renderContent())
			}
			return m, nil
		}
		if m.showHelp {
			switch msg.String() {
			case "?", "esc", "q", "Q":
				m.showHelp = false
			}
			return m, nil
		}
		m.status = ""

		switch msg.String() {
		case "?":
			m.showHelp = true
		case "home":
			m.cursor = 0
		case "end":
			m.cursor = len(m.items) - 1
		case "r", "R":
			for _, it := range m.items {
				if it.action == homeActionResume || it.action == homeActionResumeProject {
					return m, m.selectAction(it.action)
				}
			}
		case "d", "D":
			for _, it := range m.items {
				if it.action == homeActionDoneTasks {
					return m, m.selectAction(it.action)
				}
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter", " ":
			selected := m.items[m.cursor].action
			if !m.projectValid && requiresProject(selected) {
				return m, m.selectAction(homeActionOpenProject)
			}
			if selected == homeActionQuit {
				m.confirmQuit = true
			} else {
				return m.requestAction(selected)
			}
		case "w", "W":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m, m.selectAction(homeActionWorkspace)
		case "s", "S":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m, m.selectAction(homeActionSetup)
		case "g", "G":
			return m, m.selectAction(homeActionGlobalSettings)
		case "m", "M":
			return m, m.selectAction(homeActionModelMemory)
		case "o", "O":
			return m, m.selectAction(homeActionOpenProject)
		case "c", "C":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m.requestAction(homeActionClean)
		case "q", "Q":
			m.confirmQuit = true
		case "left":
			if m.scrollX > 0 {
				m.scrollX -= horizontalScrollStep
				if m.scrollX < 0 {
					m.scrollX = 0
				}
			}
		case "right":
			m.scrollX += horizontalScrollStep
		case "1":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m.requestAction(homeActionNewTask)
		case "2":
			return m, m.selectAction(homeActionOpenProject)
		case "3":
			return m, m.selectAction(homeActionGlobalSettings)
		case "4":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m, m.selectAction(homeActionSetup)
		case "5":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m.requestAction(homeActionClean)
		case "6":
			m.confirmQuit = true
		}
	}

	// Refresh viewport content after cursor/state change.
	if m.ready {
		m.viewport.SetContent(m.renderContent())
	}

	return m, nil
}

// requestAction runs action, first asking for confirmation when it would
// remove existing plans and reports (New Task, Reset Artifacts).
func (m HomeModel) requestAction(action homeAction) (HomeModel, tea.Cmd) {
	if (action == homeActionNewTask || action == homeActionClean) && m.hasArtifactsToLose(action) {
		m.confirmAction = &action
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}
		return m, nil
	}
	return m, m.selectAction(action)
}

// hasArtifactsToLose is true when the clean run by action would delete
// something, or an interrupted task could be resumed.
func (m HomeModel) hasArtifactsToLose(action homeAction) bool {
	if _, ok := decsr.Resumable(context.Background(), m.root); ok {
		return true
	}
	// New Task cleans everything; Reset keeps requirements.md.
	return wouldClean(m.wsPath, action == homeActionClean)
}

func (m HomeModel) selectAction(action homeAction) tea.Cmd {
	return func() tea.Msg { return homeSelectedMsg{action: action} }
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m HomeModel) View() string {
	if m.showHelp {
		return renderHelp(m.width, m.height, "KEYBOARD — HOME", homeHelp())
	}
	header := screenHeader(m.width, "Home", m.cachedProjectLabel())
	footer := m.renderFooter()

	if !m.ready {
		return lipgloss.JoinVertical(lipgloss.Left, header, "", footer)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		m.viewport.View(),
		footer,
	)
}

// cachedProjectLabel is the project name shown at the right of the header
// (empty when no valid project is selected).
func (m HomeModel) cachedProjectLabel() string {
	if !m.projectValid {
		return ""
	}
	return m.cachedProject
}

// ── Viewport management ──────────────────────────────────────────────────────

func (m *HomeModel) syncViewport() {
	// Reserve 2 lines: header + footer.
	vpHeight := m.height - 2
	if vpHeight < 4 {
		vpHeight = 4
	}
	if !m.ready {
		m.viewport = viewport.New(m.width, vpHeight)
		m.viewport.Style = lipgloss.NewStyle()
		m.ready = true
	} else {
		m.viewport.Width = m.width
		m.viewport.Height = vpHeight
	}
	m.viewport.SetContent(m.renderContent())
}

// ── Content rendering (inside viewport) ──────────────────────────────────────

func (m HomeModel) renderContent() string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)

	// Use a minimum render width so content stays readable in narrow windows.
	contentWidth := m.width - 4
	minContentWidth := 60
	if contentWidth < minContentWidth {
		contentWidth = minContentWidth
	}

	// Decide layout: two-column (wide) or single-column (narrow).
	wideLayout := contentWidth >= 86

	logoBlock := m.renderLogo(contentWidth)
	pipelineBlock := m.renderPipeline(contentWidth)

	sep := lipgloss.NewStyle().Foreground(homePalette.border).Render(strings.Repeat("─", contentWidth))

	infoBlock := m.renderInfoCard(contentWidth)
	menuBlock := m.renderMenu(contentWidth)

	var mainArea string
	if wideLayout {
		mainArea = lipgloss.JoinHorizontal(lipgloss.Top, infoBlock, "  ", menuBlock)
	} else {
		mainArea = lipgloss.JoinVertical(lipgloss.Left, infoBlock, "", menuBlock)
	}

	// Bottom sections.
	var bottomParts []string
	if hist := m.renderHistory(contentWidth); hist != "" {
		bottomParts = append(bottomParts, hist)
	}
	if ws := m.workspaceStatus(); ws != "" {
		bottomParts = append(bottomParts, dimStyle.Render("  "+ws))
	}

	parts := []string{
		"",
		logoBlock,
		pipelineBlock,
		sep,
		mainArea,
		sep,
	}
	parts = append(parts, bottomParts...)
	parts = append(parts, "") // trailing space

	content := strings.Join(parts, "\n")

	vpH := m.viewport.Height
	if vpH < 4 {
		vpH = 4
	}

	// Confirmation overlay (quit, or a destructive action).
	if m.confirmQuit || m.confirmAction != nil {
		title, detail := "  QUIT DECSR?", ""
		if m.confirmAction != nil {
			if *m.confirmAction == homeActionNewTask {
				title = "  START A NEW TASK?"
				detail = "  Start a new task? This removes the current plan and\n  reports (code, config and memory are kept).\n\n"
			} else {
				title = "  RESET ARTIFACTS?"
				detail = "  Reset artifacts? This removes generated plans and\n  reports (keeps code, config, and project memory).\n\n"
			}
		}
		warnStyle := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		confirmBox := lipgloss.NewStyle().
			Border(pixelBorder).
			BorderForeground(crt.warn).
			Padding(1, 3).
			Render(
				warnStyle.Render(title) + "\n\n" +
					dimStyle.Render(detail) +
					dimStyle.Render("  Press ") +
					lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render("y") +
					dimStyle.Render(" or ") +
					lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render("Enter") +
					dimStyle.Render(" to confirm, any other key to cancel"),
			)
		placeW := m.width
		if placeW < 50 {
			placeW = 50
		}
		placed := lipgloss.Place(placeW, vpH, lipgloss.Center, lipgloss.Center, confirmBox)
		if placeW > m.width {
			return horizontalSlice(placed, m.scrollX, m.width)
		}
		return placed
	}

	// Check if content fits horizontally.
	natW := maxLineWidth(content)
	if natW <= m.width {
		// Content fits — center normally.
		return lipgloss.Place(m.width, vpH, lipgloss.Center, lipgloss.Center, content)
	}

	// Content overflows — apply horizontal scroll.
	scrollX := m.scrollX
	maxScrollX := natW - m.width
	if maxScrollX < 0 {
		maxScrollX = 0
	}
	if scrollX > maxScrollX {
		scrollX = maxScrollX
	}
	return horizontalSlice(content, scrollX, m.width)
}

// ── Block-art pixel font ─────────────────────────────────────────────────────

// blockGlyphs defines each letter as a 4-wide × 5-tall pixel grid.
// '#' = filled pixel, '.' = empty pixel.
var blockGlyphs = map[rune][]string{
	'D': {"###.", "#..#", "#..#", "#..#", "###."},
	'O': {".##.", "#..#", "#..#", "#..#", ".##."},
	'R': {"###.", "#..#", "###.", "#.#.", "#..#"},
	'C': {".###", "#...", "#...", "#...", ".###"},
	'H': {"#..#", "#..#", "####", "#..#", "#..#"},
	'E': {"####", "#...", "###.", "#...", "####"},
	'S': {"####", "#...", ".##.", "...#", "####"},
	'T': {"####", ".##.", ".##.", ".##.", ".##."},
	'A': {".##.", "#..#", "####", "#..#", "#..#"},
	' ': {"....", "....", "....", "....", "...."},
}

// blockPixelRows lays text out as 5 rows of '#'/'.' pixels.
func blockPixelRows(text string) []string {
	pixelRows := make([]string, 5)
	for i, ch := range text {
		glyph := blockGlyphs[ch]
		if glyph == nil {
			glyph = blockGlyphs[' ']
		}
		if i > 0 {
			for r := range pixelRows {
				pixelRows[r] += "."
			}
		}
		for r := range pixelRows {
			pixelRows[r] += glyph[r]
		}
	}
	return pixelRows
}

// blockPairs groups the 5 pixel rows into 3 display rows (top, bottom or -1).
var blockPairs = [][2]int{{0, 1}, {2, 3}, {4, -1}}

// renderBlockText renders text as chunky pixel-art using Unicode half-block characters.
// 5 pixel rows → 3 display rows via ▀▄█ compositing.
func renderBlockText(text string) []string {
	return renderBlockTextColored(text, nil)
}

// renderBlockTextColored is renderBlockText with a per-pixel-row colour
// (len 5). With half-blocks, a cell holding two pixels of different rows uses
// foreground = top colour and background = bottom colour, giving a smooth
// vertical gradient at pixel resolution. A nil rowColors renders uncoloured.
func renderBlockTextColored(text string, rowColors []lipgloss.Color) []string {
	pixelRows := blockPixelRows(text)
	result := make([]string, len(blockPairs))
	for i, pair := range blockPairs {
		top := pixelRows[pair[0]]
		bot := strings.Repeat(".", len(top))
		if pair[1] >= 0 {
			bot = pixelRows[pair[1]]
		}
		var sb strings.Builder
		for j := 0; j < len(top); j++ {
			t, b := top[j] == '#', bot[j] == '#'
			var ch string
			var st lipgloss.Style
			switch {
			case t && b:
				ch = "█"
				if rowColors != nil {
					if rowColors[pair[0]] == rowColors[pair[1]] {
						st = lipgloss.NewStyle().Foreground(rowColors[pair[0]])
					} else {
						ch = "▀"
						st = lipgloss.NewStyle().Foreground(rowColors[pair[0]]).Background(rowColors[pair[1]])
					}
				}
			case t:
				ch = "▀"
				if rowColors != nil {
					st = lipgloss.NewStyle().Foreground(rowColors[pair[0]])
				}
			case b:
				ch = "▄"
				if rowColors != nil {
					st = lipgloss.NewStyle().Foreground(rowColors[pair[1]])
				}
			default:
				sb.WriteRune(' ')
				continue
			}
			if rowColors != nil {
				sb.WriteString(st.Render(ch))
			} else {
				sb.WriteString(ch)
			}
		}
		result[i] = sb.String()
	}
	return result
}

// ── Logo ─────────────────────────────────────────────────────────────────────

// logoStars is a fixed starfield around the pixel logo (46 cells wide, logo block
// centred in the middle 24). '✦' is bright, 'G' is a gold star, the rest are dim.
var (
	logoStarsTop    = "  ·       ✦        +      ⋆       G           "
	logoStarsBottom = "    ⋆       ·          ✦              G       "
	logoStarsLeft   = [3]string{"   ·       ", "  ⋆        ", "     ·     "}
	logoStarsRight  = [3]string{"  ·        ", "    G      ", "   ✦       "}
)

// renderStars colours a star pattern: ✦ bright, G gold ✦, other glyphs dim.
func renderStars(s string) string {
	dim := lipgloss.NewStyle().Foreground(crt.dim)
	bright := lipgloss.NewStyle().Foreground(crt.bright)
	gold := lipgloss.NewStyle().Foreground(homePalette.gold)
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case ' ':
			sb.WriteRune(r)
		case '✦':
			sb.WriteString(bright.Render(string(r)))
		case 'G':
			sb.WriteString(gold.Render("✦"))
		default:
			sb.WriteString(dim.Render(string(r)))
		}
	}
	return sb.String()
}

func (m HomeModel) renderLogo(maxW int) string {
	tagStyle := lipgloss.NewStyle().Foreground(homePalette.dim)

	if maxW < 62 {
		titleStyle := lipgloss.NewStyle().Foreground(homePalette.accent).Bold(true)
		return lipgloss.JoinVertical(lipgloss.Center,
			titleStyle.Render("◈ DECSR"),
			tagStyle.Render("AI multi-agent coding pipeline"),
		)
	}

	// Nebula gradient (azure → sky → ice), one colour per pixel row.
	rowColors := make([]lipgloss.Color, 5)
	for r := range rowColors {
		t := float64(r) / 4
		if t < 0.5 {
			rowColors[r] = lerpColor(galaxyAzure, galaxySky, t*2)
		} else {
			rowColors[r] = lerpColor(galaxySky, galaxyIce, (t-0.5)*2)
		}
	}
	blockLines := renderBlockTextColored("DECSR", rowColors)

	lines := []string{renderStars(logoStarsTop)}
	for i, bl := range blockLines {
		lines = append(lines, renderStars(logoStarsLeft[i])+bl+renderStars(logoStarsRight[i]))
	}
	lines = append(lines, renderStars(logoStarsBottom))
	lines = append(lines, tagStyle.Render("       AI-powered multi-agent coding pipeline"))

	block := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.PlaceHorizontal(maxW, lipgloss.Center, block)
}

// ── Pipeline visualisation ───────────────────────────────────────────────────

// pipelineStages mirrors the real stage order of the task runner
// (context → negotiate → decompose → tests → code → build/fix → review → done).
var pipelineStages = []struct {
	long, short string
	color       lipgloss.Color
}{
	{"CONTEXT", "CTX", galaxyCyan},
	{"NEGOTIATE", "NEG", galaxyAzure},
	{"DECOMPOSE", "DEC", galaxyIce},
	{"TESTS", "TEST", galaxySky},
	{"CODE", "CODE", galaxyMint},
	{"BUILD/FIX", "FIX", galaxyGold},
	{"REVIEW", "REV", galaxyOrange},
	{"DONE", "DONE", galaxyIce},
}

func (m HomeModel) renderPipeline(maxW int) string {
	arrow := lipgloss.NewStyle().Foreground(crt.dim).Render("▸")

	var parts []string
	for _, st := range pipelineStages {
		label := st.long
		if maxW < 90 {
			label = st.short
		}
		parts = append(parts, chip(label, st.color))
	}

	return lipgloss.PlaceHorizontal(maxW, lipgloss.Center, strings.Join(parts, arrow))
}

// ── Project Info Card ────────────────────────────────────────────────────────

func (m HomeModel) renderInfoCard(contentWidth int) string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	valueStyle := lipgloss.NewStyle().Foreground(p.bright)
	labelStyle := lipgloss.NewStyle().Foreground(p.dim).Width(10)

	cardW := contentWidth/2 - 2
	if contentWidth < 90 {
		cardW = contentWidth - 4
	}
	card := lipgloss.NewStyle().Padding(0, 1).Width(cardW)

	if !m.projectValid {
		var lines []string
		warnStyle := lipgloss.NewStyle().Foreground(p.gold)
		lines = append(lines, warnStyle.Render("⚠ No project selected"))
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("Press ")+
			lipgloss.NewStyle().Foreground(p.accent).Bold(true).Render("o")+
			dimStyle.Render(" to open a project directory"))
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("Current directory is not a"))
		lines = append(lines, dimStyle.Render("recognized project root."))
		return pixelPanel("◆ PROJECT", p.gold, false, card, strings.Join(lines, "\n"))
	}

	var lines []string
	valueW := cardW - 2 - 11
	row := func(label, value string) {
		lines = append(lines, fmt.Sprintf("%s %s", labelStyle.Render(label), truncateOneLine(value, valueW)))
	}
	inherits := dimStyle.Render("inherits global")
	const unset = "(not set)"

	row("NAME", valueStyle.Render(m.cachedProject))
	switch {
	case m.cachedLanguage == "":
		row("LANG", dimStyle.Render("not detected"))
	case m.cfg.Project.Language == "":
		row("LANG", valueStyle.Render(m.cachedLanguage)+dimStyle.Render(" auto"))
	default:
		row("LANG", valueStyle.Render(m.cachedLanguage))
	}
	if m.cachedBranch != "" {
		row("BRANCH", valueStyle.Render(m.cachedBranch))
	}
	switch {
	case m.cachedRunner == unset && m.cachedModel == unset:
		row("DEFAULT", inherits)
	default:
		if m.cachedRunner == unset {
			row("RUNNER", inherits)
		} else {
			row("RUNNER", valueStyle.Render(m.cachedRunner)+dimStyle.Render(" (global)"))
		}
		if m.cachedModel == unset {
			row("MODEL", inherits)
		} else {
			row("MODEL", valueStyle.Render(m.cachedModel)+dimStyle.Render(" (global)"))
		}
		if m.cachedEffort != "" && m.cachedEffort != "default" {
			row("EFFORT", valueStyle.Render(m.cachedEffort)+dimStyle.Render(" (global)"))
		}
	}

	if len(m.cachedOverrides) > 0 {
		lines = append(lines, "")
		if m.agentsUniform() {
			first := m.cachedOverrides[0]
			if first.runner == unset && first.model == unset {
				row("AGENTS", fmt.Sprintf("all %d · inherits global", len(m.cachedOverrides)))
			} else {
				row("AGENTS", fmt.Sprintf("all %d · %s / %s", len(m.cachedOverrides), first.runner, first.model))
			}
			shortRole := map[string]string{"pm": "PM", "coder": "Coder", "coder_fixer": "Fixer", "qa": "QA", "ux_reviewer": "UX", "security": "Sec"}
			var legend []string
			for _, ov := range m.cachedOverrides {
				label := shortRole[ov.role]
				if label == "" {
					label = strings.ToUpper(ov.role)
				}
				legend = append(legend, lipgloss.NewStyle().Foreground(roleColor(ov.role)).Render("■ "+label))
			}
			lines = append(lines, truncateOneLine(strings.Repeat(" ", 11)+strings.Join(legend, "  "), cardW-2))
		} else {
			lines = append(lines, dimStyle.Render("AGENTS"))
			for _, ov := range m.cachedOverrides {
				var info string
				switch {
				case ov.runner == unset && ov.model == unset:
					info = inherits
				default:
					r, mdl := valueStyle.Render(ov.runner), valueStyle.Render(ov.model)
					if ov.runner == unset {
						r = inherits
					}
					if ov.model == unset {
						mdl = inherits
					}
					info = r + dimStyle.Render(" / ") + mdl
					if ov.effort != "" && ov.effort != "default" {
						info += dimStyle.Render(" · ") + valueStyle.Render(ov.effort)
					}
				}
				rc := lipgloss.NewStyle().Foreground(roleColor(ov.role))
				lines = append(lines, truncateOneLine(fmt.Sprintf("%s %s %s",
					rc.Render("■"),
					rc.Render(fmt.Sprintf("%-12s", strings.ToUpper(ov.role))),
					info,
				), cardW-2))
			}
		}
	}

	return pixelPanel("◆ PROJECT", p.accent, false, card, strings.Join(lines, "\n"))
}

// agentsUniform reports whether every agent shares one runner, model and effort.
func (m HomeModel) agentsUniform() bool {
	if len(m.cachedOverrides) == 0 {
		return false
	}
	for _, ov := range m.cachedOverrides[1:] {
		first := m.cachedOverrides[0]
		if ov.runner != first.runner || ov.model != first.model || ov.effort != first.effort {
			return false
		}
	}
	return true
}

// ── Menu ─────────────────────────────────────────────────────────────────────

func (m HomeModel) renderMenu(contentWidth int) string {
	p := homePalette

	// Each menu item gets its own accent color, keyed by action so dynamic
	// items (Resume, Done Tasks) don't shift colors onto the wrong rows or onto
	// the dim description color.
	itemColorFor := func(action homeAction) lipgloss.Color {
		switch action {
		case homeActionNewTask, homeActionResume, homeActionWorkspace:
			return p.green
		case homeActionOpenProject, homeActionResumeProject:
			return p.accent
		case homeActionGlobalSettings:
			return p.cyan
		case homeActionModelMemory:
			return p.gold
		case homeActionSetup:
			return p.red
		case homeActionClean:
			return p.gold
		case homeActionDoneTasks:
			return p.bright
		case homeActionQuit:
			return p.dim
		default:
			return p.bright
		}
	}

	cardW := contentWidth/2 - 2
	if contentWidth < 90 {
		cardW = contentWidth - 4
	}
	rowW := cardW - 2 // inside horizontal padding
	if rowW < 24 {
		rowW = 24
	}

	var lines []string
	for i, item := range m.items {
		disabled := !m.projectValid && requiresProject(item.action)

		itemColor := itemColorFor(item.action)
		if disabled {
			itemColor = p.dim
		}
		desc := item.desc
		if disabled {
			desc = "Open a project first (o)"
		}

		selected := i == m.cursor
		bg := lipgloss.NewStyle()
		bar := " "
		capStyle := lipgloss.NewStyle().Background(crt.muted).Foreground(p.dim)
		if selected {
			bg = bg.Background(p.activeBg)
			bar = lipgloss.NewStyle().Foreground(focusBorder).Bold(true).Render("▌")
			capStyle = lipgloss.NewStyle().Background(focusBorder).Foreground(p.bright).Bold(true)
		}
		keycapStr := capStyle.Render(" " + item.key + " ")

		labelStyle := bg.Foreground(itemColor)
		if selected {
			labelStyle = labelStyle.Bold(true)
		}
		left := labelStyle.Render(" " + item.icon + " " + strings.ToUpper(item.label))
		gap := rowW - 1 - ansi.StringWidth(left) - ansi.StringWidth(keycapStr) - 1
		if gap < 1 {
			gap = 1
		}
		line1 := bar + left + bg.Render(strings.Repeat(" ", gap)) + keycapStr + bg.Render(" ")

		descText := ansi.Truncate("   "+desc, rowW-1, "…")
		descPad := rowW - 1 - ansi.StringWidth(descText)
		if descPad < 0 {
			descPad = 0
		}
		line2 := bar + bg.Foreground(p.dim).Render(descText) + bg.Render(strings.Repeat(" ", descPad))

		lines = append(lines, line1, line2)
		if i < len(m.items)-1 {
			lines = append(lines, "")
		}
	}

	card := lipgloss.NewStyle().Padding(0, 1).Width(cardW)
	return pixelPanel("◆ ACTIONS", p.accent, true, card, strings.Join(lines, "\n"))
}

// ── Recent Projects ──────────────────────────────────────────────────────────

func (m HomeModel) renderHistory(width int) string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	titleStyle := lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(p.bright)
	pathStyle := lipgloss.NewStyle().Foreground(p.dim)
	activeStyle := lipgloss.NewStyle().Foreground(p.green)

	// Show recent projects (excluding current).
	var filtered []RecentProject
	for _, proj := range m.recentProjects {
		if proj.Path != m.root {
			filtered = append(filtered, proj)
		}
	}

	if len(filtered) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, titleStyle.Render("  ◆ RECENT PROJECTS")+"  "+dimStyle.Render("(o to switch)"))

	maxShow := 5
	if len(filtered) < maxShow {
		maxShow = len(filtered)
	}
	for i := 0; i < maxShow; i++ {
		proj := filtered[i]
		exists := dirExists(proj.Path)
		marker := activeStyle.Render("●")
		if !exists {
			marker = dimStyle.Render("○")
		}
		name := nameStyle.Render(proj.Name)
		short := m.shortenProjectPath(proj.Path)
		line := fmt.Sprintf("    %s %s  %s", marker, name, pathStyle.Render(short))
		lines = append(lines, truncateOneLine(line, width))
	}
	if len(filtered) > maxShow {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("    … and %d more", len(filtered)-maxShow)))
	}
	return strings.Join(lines, "\n")
}

func (m HomeModel) shortenProjectPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	// Ensure home ends with separator so "/home/user" doesn't match "/home/username".
	homePrefix := home
	if !strings.HasSuffix(homePrefix, string(filepath.Separator)) {
		homePrefix += string(filepath.Separator)
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, homePrefix) {
		return "~" + string(filepath.Separator) + path[len(homePrefix):]
	}
	return path
}

// ── Footer ───────────────────────────────────────────────────────────────────

func (m HomeModel) renderFooter() string {
	p := homePalette
	hints := []keyHint{{"↑↓", "navigate"}}
	if m.width < 80 {
		hints = append(hints, keyHint{"←→", "scroll"})
	}
	hints = append(hints, keyHint{"Enter", "select"})
	for _, it := range m.items {
		if it.action == homeActionResume || it.action == homeActionResumeProject {
			hints = append(hints, keyHint{"r", "resume"})
			break
		}
	}
	hints = append(hints,
		keyHint{"w", "workspace"},
		keyHint{"o", "project"},
		keyHint{"g", "global"},
		keyHint{"s", "setup"},
		keyHint{"c", "reset"},
		keyHint{"?", "help"},
		keyHint{"q", "quit"},
	)

	bar := lipgloss.NewStyle().Background(p.footerBg)
	right := ""
	if m.status != "" {
		right = bar.Foreground(p.green).Bold(true).Render(m.status) + bar.Render("  ")
	}
	right += bar.Foreground(p.gold).Bold(true).Render("v" + Version)
	if m.status != "" {
		// The status replaces the key hints so it is never truncated away.
		hints = nil
	}
	// When the hints do not fit, drop from the middle so help and quit stay.
	for _, drop := range []string{"c", "s", "g", "o", "w", "r", "←→", "Enter", "↑↓"} {
		if hintsWidth(hints)+ansi.StringWidth(right)+2 <= m.width {
			break
		}
		for i, h := range hints {
			if h.key == drop {
				hints = append(hints[:i:i], hints[i+1:]...)
				break
			}
		}
	}
	return hintBar(m.width, hints, right)
}

// hintsWidth is the width hintBar needs for the hints alone.
func hintsWidth(hints []keyHint) int {
	w := 1
	for i, h := range hints {
		if i > 0 {
			w += 2
		}
		w += ansi.StringWidth(keycap(h.key)) + 1 + ansi.StringWidth(h.label)
	}
	return w
}

// ── Data resolution helpers ──────────────────────────────────────────────────

type agentOverride struct {
	role   string
	runner string
	model  string
	effort string
}

// resolveLanguageFromRoot detects project language from config or filesystem markers.
func resolveLanguageFromRoot(root string, cfg config.Config) string {
	return lang.Resolve(cfg.Project.Language, root)
}

// resolveOverrides returns all agents with their effective runner/model and
// any effort that differs from the global default.
func resolveOverrides(agents map[string]config.AgentConfig, defaultRunner, defaultModel, defaultEffort string) []agentOverride {
	var overrides []agentOverride
	roles := []string{"pm", "coder", "coder_fixer", "qa", "ux_reviewer", "security"}
	for _, role := range roles {
		if ac, ok := agents[role]; ok {
			r := ac.Runner
			mdl := ac.Model
			if r == "" {
				r = defaultRunner
			}
			if mdl == "" {
				mdl = defaultModel
			}
			effort := ac.Effort
			if effort == "default" || effort == defaultEffort {
				effort = ""
			}
			overrides = append(overrides, agentOverride{role: role, runner: r, model: mdl, effort: effort})
		}
	}
	return overrides
}

func (m HomeModel) shortenPath(path string) string {
	rel, err := filepath.Rel(m.root, path)
	if err != nil {
		return filepath.Base(path)
	}
	if len(rel) > 50 {
		return "…" + rel[len(rel)-49:]
	}
	return rel
}

func (m HomeModel) workspaceStatus() string {
	wsDir := filepath.Join(m.root, ".decsr")
	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		return "⊘ no workspace"
	}

	artifacts := []struct {
		file  string
		label string
	}{
		{"requirements.md", "requirements"},
		{"architecture.md", "architecture"},
		{"implementation_plan.md", "plan"},
		{"prompts.md", "prompts"},
	}

	var found []string
	for _, a := range artifacts {
		if _, err := os.Stat(filepath.Join(wsDir, a.file)); err == nil {
			found = append(found, a.label)
		}
	}

	if len(found) == 0 {
		return "◇ workspace ready"
	}
	return "◆ artifacts: " + strings.Join(found, ", ")
}

// isValidProjectRoot returns true when root looks like a real project directory.
// It rejects the user's home directory and directories without any project markers.
func isValidProjectRoot(root string) bool {
	if root == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err == nil && filepath.Clean(root) == filepath.Clean(home) {
		return false
	}
	// Check project markers plus .decsr workspace.
	// Avoid append(projectMarkers, ...) — it can mutate the shared slice.
	for _, marker := range projectMarkers {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			return true
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".decsr")); err == nil {
		return true
	}
	return false
}

// requiresProject returns true for actions that need a valid project directory.
func requiresProject(action homeAction) bool {
	switch action {
	case homeActionWorkspace, homeActionNewTask, homeActionRunPipeline, homeActionResume, homeActionResumeProject, homeActionSetup, homeActionClean:
		return true
	case homeActionOpenProject, homeActionGlobalSettings, homeActionModelMemory, homeActionDoneTasks, homeActionQuit:
		return false
	}
	return false
}
