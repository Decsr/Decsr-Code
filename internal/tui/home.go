package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/decsr"
	"github.com/Decsr/Decsr-Code/internal/lang"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
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
	homeActionConnect       // handled inside Home: opens the connect overlay
	homeActionSwitchProject // switch to the project in homeSelectedMsg.path
)

// homeSelectedMsg reports a launcher choice. path is set for
// homeActionSwitchProject and session for a homeActionWorkspace that opens a
// saved session.
type homeSelectedMsg struct {
	action  homeAction
	path    string
	session string
}

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

const (
	homeMaxRecent   = 5 // recent projects listed on Home
	homeMaxSessions = 6 // sessions of the project listed on Home
)

// The two lists Home can focus: the actions, or the sessions and recent
// projects.
const (
	homeColLeft = iota
	homeColRight
)

// HomeModel is the first screen shown when DECSR launches: the project and its
// sessions on the left, the actions on the right.
type HomeModel struct {
	cfg    config.Config
	root   string
	wsPath string
	width  int
	height int

	col     int // focused column: homeColLeft (sessions, projects) or homeColRight (actions)
	cursor  int // selected action, an index into items
	leftCur int // selected row of the left lists (recent projects, then sessions)
	items   []homeMenuItem

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

	recentProjects []RecentProject         // from ~/.decsr/projects.json
	sessions       []workspace.SessionInfo // saved sessions of root, newest first

	showHelp      bool        // true while the keyboard help overlay is open
	confirmQuit   bool        // true when showing quit confirmation
	confirmAction *homeAction // destructive action awaiting y/Enter confirmation
	conn          *connectModel
	status        string // transient message, cleared on the next keypress
	ready         bool   // set by the first size
}

func NewHomeModel(cfg config.Config, root string) HomeModel {
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

	items := []homeMenuItem{
		{icon: "◉", label: "Workspace", desc: "Chat, split into nodes, assign agents", action: homeActionWorkspace, key: "w"},
		{icon: "▶", label: "Classic Pipeline", desc: "Start a PM chat to define a task — attach existing .md with Ctrl+F", action: homeActionNewTask, key: "1"},
		{icon: "■", label: "Open Project", desc: "Switch to another project directory", action: homeActionOpenProject, key: "o"},
		{icon: "◆", label: "Global Settings", desc: "Default provider, model & effort (~/.decsr/config.yaml)", action: homeActionGlobalSettings, key: "g"},
		{icon: "◈", label: "Connect a Provider", desc: "Save an API key and pick a model", action: homeActionConnect, key: "k"},
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
	rt, resumable := decsr.Resumable(context.Background(), root)
	if !resumable && decsr.HasReviewableArtifacts(root) {
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
	if resumable {
		resumeItem := homeMenuItem{
			icon:   "↻",
			label:  "Resume Task",
			desc:   "Continue the interrupted task: " + rt.Title,
			action: homeActionResume,
			key:    "r",
		}
		items = append([]homeMenuItem{resumeItem}, items...)
	}

	valid := isValidProjectRoot(root)
	var sessions []workspace.SessionInfo
	if valid {
		sessions = workspace.ListSessions(root)
		if len(sessions) > homeMaxSessions {
			sessions = sessions[:homeMaxSessions]
		}
	}
	var recent []RecentProject
	for _, p := range LoadRecentProjects() {
		if filepath.Clean(p.Path) != filepath.Clean(root) {
			recent = append(recent, p)
		}
	}
	if len(recent) > homeMaxRecent {
		recent = recent[:homeMaxRecent]
	}

	return HomeModel{
		cfg:             cfg,
		root:            root,
		wsPath:          filepath.Join(root, ".decsr"),
		col:             homeColRight,
		projectValid:    valid,
		cachedRunner:    runnerName,
		cachedModel:     modelName,
		cachedEffort:    effortName,
		cachedProject:   projectName,
		cachedLanguage:  resolveLanguageFromRoot(root, cfg),
		cachedBranch:    GitBranch(root),
		cachedOverrides: resolveOverrides(cfg.Agents, runnerName, modelName, effortName),
		recentProjects:  recent,
		sessions:        sessions,
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
		m.syncViewport()

	case connectClosedMsg:
		m.conn = nil
	case connectSavedMsg:
		m.conn = nil
		if msg.err != nil {
			m.status = "! " + oneLine(msg.err.Error())
		} else {
			m.status = "✓ " + connectSummary(msg)
		}
	case connectProbeMsg, connectTickMsg:
		if m.conn != nil {
			c, cmd := m.conn.update(msg)
			m.conn = &c
			return m, cmd
		}

	case tea.KeyMsg:
		if m.conn != nil {
			c, cmd := m.conn.update(msg)
			m.conn = &c
			return m, cmd
		}
		// Quit confirmation mode intercepts all keys.
		if m.confirmQuit {
			switch msg.String() {
			case "y", "Y", "enter":
				return m, m.selectAction(homeActionQuit)
			default:
				m.confirmQuit = false
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
		return m.onKey(msg.String())
	}
	return m, nil
}

// onKey handles a key on the launcher itself.
func (m HomeModel) onKey(key string) (HomeModel, tea.Cmd) {
	switch key {
	case "?":
		m.showHelp = true
	case "tab", "shift+tab":
		m.col = 1 - m.col
	case "left":
		m.col = homeColLeft
	case "right":
		m.col = homeColRight
	case "home":
		m.setCursor(0)
	case "end":
		m.setCursor(m.rowCount() - 1)
	case "up":
		m.setCursor(m.curRow() - 1)
	case "down":
		m.setCursor(m.curRow() + 1)
	case "enter", " ":
		return m.activate()
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
	case "w", "W":
		return m.runAction(homeActionWorkspace)
	case "s", "S", "4":
		return m.runAction(homeActionSetup)
	case "g", "G", "3":
		return m.runAction(homeActionGlobalSettings)
	case "m", "M":
		return m.runAction(homeActionModelMemory)
	case "o", "O", "2":
		return m.runAction(homeActionOpenProject)
	case "k", "K":
		return m.runAction(homeActionConnect)
	case "c", "C", "5":
		return m.runAction(homeActionClean)
	case "1":
		return m.runAction(homeActionNewTask)
	case "q", "Q", "6":
		m.confirmQuit = true
	}
	return m, nil
}

// runAction runs an action chosen by its key or by Enter on its row: the ones
// that need a project fall back to Open project when there is none.
func (m HomeModel) runAction(action homeAction) (HomeModel, tea.Cmd) {
	if !m.projectValid && requiresProject(action) {
		return m, m.selectAction(homeActionOpenProject)
	}
	switch action {
	case homeActionQuit:
		m.confirmQuit = true
		return m, nil
	case homeActionConnect:
		c := newConnectModel("", false, nil, nil)
		m.conn = &c
		return m, nil
	case homeActionNewTask, homeActionClean:
		return m.requestAction(action)
	}
	return m, m.selectAction(action)
}

// activate is Enter on the selected row of the focused column.
func (m HomeModel) activate() (HomeModel, tea.Cmd) {
	if m.col == homeColRight {
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		return m.runAction(m.items[m.cursor].action)
	}
	left := m.leftRows()
	if m.leftCur < 0 || m.leftCur >= len(left) {
		return m, nil
	}
	row := left[m.leftCur]
	if row.session != "" {
		return m, func() tea.Msg { return homeSelectedMsg{action: homeActionWorkspace, session: row.session} }
	}
	if filepath.Clean(row.path) == filepath.Clean(m.root) {
		return m.runAction(homeActionWorkspace)
	}
	if !dirExists(row.path) {
		m.status = "! project not found: " + row.path
		return m, nil
	}
	return m, func() tea.Msg { return homeSelectedMsg{action: homeActionSwitchProject, path: row.path} }
}

// rowCount is the number of selectable rows of the focused column.
func (m HomeModel) rowCount() int {
	if m.col == homeColRight {
		return len(m.items)
	}
	return len(m.leftRows())
}

func (m HomeModel) curRow() int {
	if m.col == homeColRight {
		return m.cursor
	}
	return m.leftCur
}

// setCursor moves the selection of the focused column, clamped to its rows.
func (m *HomeModel) setCursor(i int) {
	i = max(min(i, m.rowCount()-1), 0)
	if m.col == homeColRight {
		m.cursor = i
	} else {
		m.leftCur = i
	}
}

// requestAction runs action, first asking for confirmation when it would
// remove existing plans and reports (New Task, Reset Artifacts).
func (m HomeModel) requestAction(action homeAction) (HomeModel, tea.Cmd) {
	if (action == homeActionNewTask || action == homeActionClean) && m.hasArtifactsToLose(action) {
		m.confirmAction = &action
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

// syncViewport marks the model as sized; the screen is laid out on every View
// from width and height.
func (m *HomeModel) syncViewport() { m.ready = true }

// ── Left lists: sessions and recent projects ─────────────────────────────────

// homeLeftRow is one selectable row of the left lists: a recent project or a
// session of the current project.
type homeLeftRow struct {
	label   string
	path    string // recent project directory
	session string // session ID; empty for a project
	info    workspace.SessionInfo
}

// leftRows lists the selectable rows of the left lists: recent projects, then
// the sessions of the current project.
func (m HomeModel) leftRows() []homeLeftRow {
	rows := make([]homeLeftRow, 0, len(m.recentProjects)+len(m.sessions))
	for _, p := range m.recentProjects {
		name := p.Name
		if name == "" {
			name = filepath.Base(p.Path)
		}
		rows = append(rows, homeLeftRow{label: name, path: p.Path})
	}
	for _, s := range m.sessions {
		title := s.Title
		if title == "" {
			title = "untitled"
		}
		rows = append(rows, homeLeftRow{label: title, session: s.ID, info: s})
	}
	return rows
}

// sessionKind is the ui.Status kind that sums up a session.
func sessionKind(s workspace.SessionInfo) string {
	total := s.Todo + s.Running + s.Done + s.Failed
	switch {
	case s.Failed > 0:
		return "failed"
	case s.Waiting > 0:
		return "waiting"
	case s.Running > 0:
		return "running"
	case total > 0 && s.Done == total:
		return "done"
	}
	return "todo"
}

// sessionMeta is "2/5 · 1 waiting" or "5/5 · 2d": progress, then what needs
// attention, else the age.
func sessionMeta(s workspace.SessionInfo, now time.Time) string {
	total := s.Todo + s.Running + s.Done + s.Failed
	if total == 0 {
		return sbAge(now, s.UpdatedAt)
	}
	out := fmt.Sprintf("%d/%d", s.Done, total)
	switch {
	case s.Waiting > 0:
		out += fmt.Sprintf(" · %d waiting", s.Waiting)
	case s.Failed > 0:
		out += fmt.Sprintf(" · %d failed", s.Failed)
	case s.Running > 0:
		out += fmt.Sprintf(" · %d running", s.Running)
	default:
		out += " · " + sbAge(now, s.UpdatedAt)
	}
	return out
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
		m.renderContent(),
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

// bodyHeight is the height between the header and the footer.
func (m HomeModel) bodyHeight() int { return max(m.height-2, 4) }

// ── Content rendering ────────────────────────────────────────────────────────

// renderContent lays out the body: logo, pipeline, the project and its lists
// next to the actions, and the workspace status. A body taller than the
// screen scrolls so the selected row stays in view.
func (m HomeModel) renderContent() string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)

	// Use a minimum render width so content stays readable in narrow windows.
	contentWidth := m.width - 4
	minContentWidth := 60
	if contentWidth < minContentWidth {
		contentWidth = minContentWidth
	}

	vpH := m.bodyHeight()

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
		return lipgloss.Place(max(m.width, 50), vpH, lipgloss.Center, lipgloss.Center, confirmBox)
	}
	if m.conn != nil {
		return lipgloss.Place(m.width, vpH, lipgloss.Center, lipgloss.Center, m.conn.box(m.width))
	}

	// Decide layout: two-column (wide) or single-column (narrow).
	wideLayout := contentWidth >= 86

	logoBlock := m.renderLogo(contentWidth)
	pipelineBlock := m.renderPipeline(contentWidth)

	sep := lipgloss.NewStyle().Foreground(homePalette.border).Render(strings.Repeat("─", contentWidth))

	infoBlock := m.renderInfoCard(contentWidth)
	listsBlock, listsSel := m.renderLeftLists(contentWidth)
	menuBlock, menuSel := m.renderMenu(contentWidth)

	var mainArea string
	var sel int // line of the selected row inside mainArea
	if wideLayout {
		leftBlock := infoBlock
		if listsBlock != "" {
			leftBlock = lipgloss.JoinVertical(lipgloss.Left, infoBlock, listsBlock)
		}
		mainArea = lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, "  ", menuBlock)
		sel = menuSel
		if m.col == homeColLeft && listsSel >= 0 {
			sel = lipgloss.Height(infoBlock) + listsSel
		}
	} else {
		mainArea = lipgloss.JoinVertical(lipgloss.Left, infoBlock, "", menuBlock)
		sel = lipgloss.Height(infoBlock) + 1 + menuSel
		if listsBlock != "" {
			if m.col == homeColLeft && listsSel >= 0 {
				sel = lipgloss.Height(mainArea) + 1 + listsSel
			}
			mainArea = lipgloss.JoinVertical(lipgloss.Left, mainArea, "", listsBlock)
		}
	}

	parts := []string{
		"",
		logoBlock,
		pipelineBlock,
		sep,
	}
	sel += strings.Count(strings.Join(parts, "\n"), "\n") + 1
	parts = append(parts, mainArea, sep)
	if ws := m.workspaceStatus(); ws != "" {
		parts = append(parts, dimStyle.Render("  "+ws))
	}
	parts = append(parts, "") // trailing space

	content := strings.Join(parts, "\n")
	if lines := strings.Split(content, "\n"); len(lines) > vpH {
		start := 0
		if sel >= vpH-2 {
			start = sel - vpH + 3
		}
		start = max(min(start, len(lines)-vpH), 0)
		content = strings.Join(lines[start:start+vpH], "\n")
	}

	// Content that is wider than the screen is cut at the right edge.
	if maxLineWidth(content) > m.width {
		return horizontalSlice(content, 0, m.width)
	}
	return lipgloss.Place(m.width, vpH, lipgloss.Center, lipgloss.Center, content)
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
	rowColors := make([]lipgloss.TerminalColor, 5)
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

// homeCardWidth is the width of the project, list and action cards.
func homeCardWidth(contentWidth int) int {
	if contentWidth < 90 {
		return contentWidth - 4
	}
	return contentWidth/2 - 2
}

func (m HomeModel) renderInfoCard(contentWidth int) string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	valueStyle := lipgloss.NewStyle().Foreground(p.bright)
	labelStyle := lipgloss.NewStyle().Foreground(p.dim).Width(10)

	cardW := homeCardWidth(contentWidth)
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

// ── Sessions and recent projects ─────────────────────────────────────────────

// leftRow renders one selectable row of the left lists inside rowW cells. The
// selected row has the raised background when its list has the focus; without
// the focus it keeps only a dim bar.
func (m HomeModel) leftRow(glyph, label, meta string, rowW int, selected bool) string {
	p := homePalette
	focused := m.col == homeColLeft
	bg := lipgloss.NewStyle()
	bar := " "
	if selected {
		if focused {
			bg = bg.Background(p.activeBg)
			bar = lipgloss.NewStyle().Foreground(focusBorder).Bold(true).Render("▌")
		} else {
			bar = lipgloss.NewStyle().Foreground(p.dim).Render("▌")
		}
	}
	labelStyle := bg.Foreground(p.bright)
	if selected && focused {
		labelStyle = labelStyle.Bold(true)
	}
	left := glyph
	if left != "" {
		left = bg.Render(" ") + left
	}
	left += labelStyle.Render(" " + label)
	right := bg.Foreground(p.dim).Render(meta + " ")
	room := rowW - 1 - ansi.StringWidth(right)
	left = ansi.Truncate(left, max(room, 1), "…")
	gap := rowW - 1 - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
	}
	return bar + left + bg.Render(strings.Repeat(" ", gap)) + right
}

// sessionGlyphColor is the galaxy colour of a session status.
func sessionGlyphColor(kind string) lipgloss.Color {
	switch kind {
	case "failed":
		return galaxyRed
	case "waiting":
		return galaxyGold
	case "running":
		return galaxyAzure
	case "done":
		return galaxyMint
	}
	return crt.dim
}

// renderLeftLists draws the sessions of the project and the recent projects as
// cards, and gives the line of the selected row inside the block (-1 when the
// selected row is not there). It is empty when there is nothing to list.
func (m HomeModel) renderLeftLists(contentWidth int) (string, int) {
	p := homePalette
	cardW := homeCardWidth(contentWidth)
	rowW := max(cardW-2, 24)
	card := lipgloss.NewStyle().Padding(0, 1).Width(cardW)
	focused := m.col == homeColLeft
	nProj := len(m.recentProjects)
	rows := m.leftRows()

	var blocks []string
	offset, sel := 0, -1

	if m.projectValid && len(m.sessions) > 0 {
		var lines []string
		now := time.Now()
		for j, r := range rows[nProj:] {
			i := nProj + j
			glyph, _, _ := ui.Status(sessionKind(r.info))
			g := lipgloss.NewStyle().Foreground(sessionGlyphColor(sessionKind(r.info))).Render(glyph)
			if i == m.leftCur {
				sel = offset + 1 + len(lines)
			}
			lines = append(lines, m.leftRow(g, r.label, sessionMeta(r.info, now), rowW, i == m.leftCur))
		}
		block := pixelPanel("◆ SESSIONS", p.accent, focused && m.leftCur >= nProj, card, strings.Join(lines, "\n"))
		blocks = append(blocks, block)
		offset += lipgloss.Height(block)
	}

	if nProj > 0 {
		var lines []string
		nameW := 8
		for _, r := range rows[:nProj] {
			nameW = max(nameW, ansi.StringWidth(r.label))
		}
		nameW = min(nameW, max(rowW/3, 8))
		for i, r := range rows[:nProj] {
			name := ansi.Truncate(r.label, nameW, "…")
			name += strings.Repeat(" ", nameW-ansi.StringWidth(name))
			meta := ""
			if !dirExists(r.path) {
				meta = "missing "
			}
			meta += m.shortenProjectPath(r.path)
			if i == m.leftCur {
				sel = offset + 1 + len(lines)
			}
			metaW := max(rowW-nameW-6, 4)
			if ansi.StringWidth(meta) > metaW {
				meta = "…" + ansi.TruncateLeft(meta, ansi.StringWidth(meta)-metaW+1, "")
			}
			lines = append(lines, m.leftRow("", name, meta, rowW, i == m.leftCur))
		}
		blocks = append(blocks, pixelPanel("◆ RECENT PROJECTS", p.accent, focused && m.leftCur < nProj, card, strings.Join(lines, "\n")))
	}
	if len(blocks) == 0 {
		return "", -1
	}
	return lipgloss.JoinVertical(lipgloss.Left, blocks...), sel
}

// ── Menu ─────────────────────────────────────────────────────────────────────

// renderMenu draws the actions card and gives the line of the selected action
// inside it.
func (m HomeModel) renderMenu(contentWidth int) (string, int) {
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
		case homeActionGlobalSettings, homeActionConnect:
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

	cardW := homeCardWidth(contentWidth)
	rowW := cardW - 2 // inside horizontal padding
	if rowW < 24 {
		rowW = 24
	}

	focused := m.col == homeColRight
	sel := 0
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
		if selected {
			sel = 1 + len(lines)
		}
		bg := lipgloss.NewStyle()
		bar := " "
		capStyle := lipgloss.NewStyle().Background(crt.muted).Foreground(p.dim)
		if selected {
			if focused {
				bg = bg.Background(p.activeBg)
				bar = lipgloss.NewStyle().Foreground(focusBorder).Bold(true).Render("▌")
				capStyle = lipgloss.NewStyle().Background(focusBorder).Foreground(p.bright).Bold(true)
			} else {
				bar = lipgloss.NewStyle().Foreground(p.dim).Render("▌")
			}
		}
		keycapStr := capStyle.Render(" " + item.key + " ")

		labelStyle := bg.Foreground(itemColor)
		if selected && focused {
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
	return pixelPanel("◆ ACTIONS", p.accent, focused, card, strings.Join(lines, "\n")), sel
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
	hints := []keyHint{{"↑↓", "navigate"}, {"Tab", "lists"}, {"Enter", "select"}}
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
		col := p.green
		if strings.HasPrefix(m.status, "!") {
			col = p.red
		}
		right = bar.Foreground(col).Bold(true).Render(m.status) + bar.Render("  ")
	}
	right += bar.Foreground(p.gold).Bold(true).Render("v" + Version)
	if m.status != "" {
		// The status replaces the key hints so it is never truncated away.
		hints = nil
	}
	// When the hints do not fit, drop from the middle so help and quit stay.
	for _, drop := range []string{"c", "s", "g", "o", "w", "r", "Tab", "Enter", "↑↓"} {
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

// renderBlockTextColored is renderBlockText with a per-pixel-row colour
// (len 5). With half-blocks, a cell holding two pixels of different rows uses
// foreground = top colour and background = bottom colour, giving a smooth
// vertical gradient at pixel resolution. A nil rowColors renders uncoloured.
func renderBlockTextColored(text string, rowColors []lipgloss.TerminalColor) []string {
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

// resolveLanguageFromRoot detects project language from config or filesystem markers.
func resolveLanguageFromRoot(root string, cfg config.Config) string {
	return lang.Resolve(cfg.Project.Language, root)
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
