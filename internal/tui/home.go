package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/decsr"
	"github.com/Decsr/Decsr-Code/internal/lang"
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
	group  string // section label of the launcher
}

// ── HomeModel ────────────────────────────────────────────────────────────────

const (
	homeMaxRecent   = 5 // recent projects listed in the launcher
	homeMaxSessions = 6 // sessions of the project listed in the launcher
)

// homeLeft and homeRight are the two columns of the launcher.
const (
	homeColLeft = iota
	homeColRight
)

// HomeModel is the launcher: recent projects and sessions on the left,
// actions on the right.
type HomeModel struct {
	cfg    config.Config
	root   string
	wsPath string
	width  int
	height int

	col     int // focused column: homeColLeft or homeColRight
	cursor  int // selected action, an index into items
	leftCur int // selected row of the left column (recent projects, then sessions)
	items   []homeMenuItem

	// projectValid is false when root is not a real project directory
	// (e.g. home dir or missing project markers). Disables Run, Setup, Clean.
	projectValid bool

	// Cached project info — computed once in NewHomeModel, stable across renders.
	cachedProject  string
	cachedLanguage string
	cachedBranch   string

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

	const (
		gStart    = "Start"
		gSettings = "Settings"
		gMore     = "More"
	)
	items := []homeMenuItem{
		{icon: "◉", label: "Workspace", desc: "chat, split into nodes, run agents", action: homeActionWorkspace, key: "w", group: gStart},
		{icon: "■", label: "Open project…", desc: "switch to another project directory", action: homeActionOpenProject, key: "o", group: gStart},
		{icon: "◆", label: "Global settings", desc: "default provider, model and effort (~/.decsr/config.yaml)", action: homeActionGlobalSettings, key: "g", group: gSettings},
		{icon: "◇", label: "Project setup", desc: "per-agent runner, model and effort overrides", action: homeActionSetup, key: "s", group: gSettings},
		{icon: "◈", label: "Connect a provider", desc: "save an API key and pick a model", action: homeActionConnect, key: "k", group: gSettings},
		{icon: "◈", label: "Model memory", desc: "cap local-model RAM or context size", action: homeActionModelMemory, key: "m", group: gSettings},
		{icon: "▶", label: "Classic pipeline (frozen)", desc: "start a PM chat to define a task; attach a .md with Ctrl+F", action: homeActionNewTask, key: "1", group: gMore},
		{icon: "✦", label: "Reset artifacts", desc: "remove generated plans and reports; keeps code, config and memory", action: homeActionClean, key: "c", group: gMore},
		{icon: "×", label: "Quit", desc: "exit decsr", action: homeActionQuit, key: "q", group: gMore},
	}
	insertBefore := func(action homeAction, it homeMenuItem) {
		for i, x := range items {
			if x.action == action {
				items = append(items[:i], append([]homeMenuItem{it}, items[i:]...)...)
				return
			}
		}
	}

	// Show completed-task history when there is any.
	if done := decsr.DoneTasks(context.Background(), root); len(done) > 0 {
		insertBefore(homeActionNewTask, homeMenuItem{icon: "✓", label: "Done tasks", desc: "review completed tasks", action: homeActionDoneTasks, key: "d", group: gMore})
	}

	// Offer "Resume project" when planning artifacts exist but no strictly
	// resumable (in-progress) task is detected — the PM re-reads .decsr
	// and plans only the remaining work.
	rt, resumable := decsr.Resumable(context.Background(), root)
	if !resumable && decsr.HasReviewableArtifacts(root) {
		insertBefore(homeActionOpenProject, homeMenuItem{icon: "↺", label: "Resume project", desc: "the PM reviews .decsr artifacts and plans the remaining work", action: homeActionResumeProject, key: "r", group: gStart})
	}
	// Offer Resume when an interrupted task is detected.
	if resumable {
		insertBefore(homeActionOpenProject, homeMenuItem{icon: "↻", label: "Resume task", desc: "continue the interrupted task: " + rt.Title, action: homeActionResume, key: "r", group: gStart})
	}

	valid := isValidProjectRoot(root)
	var sessions []workspace.SessionInfo
	if valid {
		sessions = workspace.ListSessions(root)
		if len(sessions) > homeMaxSessions {
			sessions = sessions[:homeMaxSessions]
		}
	}
	recent := LoadRecentProjects()
	if len(recent) > homeMaxRecent {
		recent = recent[:homeMaxRecent]
	}

	return HomeModel{
		cfg:            cfg,
		root:           root,
		wsPath:         filepath.Join(root, ".decsr"),
		col:            homeColRight,
		projectValid:   valid,
		cachedProject:  projectName,
		cachedLanguage: resolveLanguageFromRoot(root, cfg),
		cachedBranch:   GitBranch(root),
		recentProjects: recent,
		sessions:       sessions,
		items:          items,
		width:          80,
		height:         24,
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

// syncViewport marks the model as sized; the launcher itself is laid out on
// every View from width and height.
func (m *HomeModel) syncViewport() { m.ready = true }

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
