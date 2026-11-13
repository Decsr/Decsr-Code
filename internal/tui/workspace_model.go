package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/cursor"
	bkey "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/config"
	dctx "github.com/Decsr/Decsr-Code/internal/context"
	"github.com/Decsr/Decsr-Code/internal/lang"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	wsMinW         = 60
	wsMinH         = 16
	wsInspectorW   = 38
	wsInspectorMin = 100 // inspector is shown by default from this width
	wsMaxOutput    = 2000
)

const (
	wsInputHint     = "Type a task or / for commands"
	wsSplittingHint = "The lead is splitting your task…"
)

// WorkspaceResult tells the caller why the Workspace closed.
type WorkspaceResult struct {
	OpenMenu      bool
	SwitchProject string // directory to reopen the Workspace in; "" = stay
	Cfg           config.Config
}

// WorkspaceOptions configures how the Workspace opens.
type WorkspaceOptions struct {
	Resume bool // open with the session picker showing
}

// wsDeps are the seams between the model and the outside world. The optional
// ones fall back to the real implementations when nil.
type wsDeps struct {
	// split asks the lead to split task; earlier is the session summary and
	// the current nodes ("" for a fresh session). existing are the node IDs
	// already in the graph and next the first number for new sub-tasks.
	split func(ctx context.Context, task, earlier string, existing []string, next int, onToken func(string)) (*workspace.Graph, runner.TokenUsage, error)
	// newEngine builds the engine; summary gives the workers the session summary.
	newEngine func(g *workspace.Graph, save func(*workspace.Graph) error, summary func() string) *workspace.Engine
	saveRole  func(name string, ac config.AgentConfig) (config.Config, error) // persists and returns the reloaded config
	checkCmds func() []string

	listProfiles   func() []string
	saveProfile    func(name string, agents map[string]config.AgentConfig) error
	applyProfile   func(name string) (config.Config, error) // applies and returns the reloaded config
	deleteProfile  func(name string) error
	recent         func() []RecentProject
	customCommands func() []workspace.CustomCommand
}

func (d *wsDeps) fillDefaults(root string) {
	if d.listProfiles == nil {
		d.listProfiles = config.ListProfiles
	}
	if d.saveProfile == nil {
		d.saveProfile = config.SaveProfile
	}
	if d.applyProfile == nil {
		d.applyProfile = func(name string) (config.Config, error) {
			if err := config.ApplyProfile(root, name); err != nil {
				return config.Config{}, err
			}
			return config.Load(root)
		}
	}
	if d.deleteProfile == nil {
		d.deleteProfile = config.DeleteProfile
	}
	if d.recent == nil {
		d.recent = LoadRecentProjects
	}
	if d.customCommands == nil {
		d.customCommands = func() []workspace.CustomCommand { return workspace.LoadCustomCommands(root) }
	}
}

// wsLiveExec builds the real executor per node from the current config, so a
// role created while the Workspace is open can run straight away.
type wsLiveExec struct {
	root    string
	cfg     func() config.Config
	pctx    func() string
	checks  func() []string
	summary func() string
}

func (e wsLiveExec) Run(ctx context.Context, g *workspace.Graph, n *workspace.Node, emit func(workspace.Event)) (workspace.Result, error) {
	c := e.cfg()
	agents := make(map[string]config.AgentConfig, len(c.Agents))
	for k, v := range c.Agents {
		agents[k] = v
	}
	c.Agents = agents
	runner.ApplyModelMemory(&c)
	return workspace.NewExecutorWithSummary(e.root, c, e.pctx(), e.checks, e.summary).Run(ctx, g, n, emit)
}

// writeSplitRaw saves the lead's full raw answer of the last split to
// .decsr/workspace_split.txt so the user can inspect it. Errors are ignored.
func writeSplitRaw(root, raw string) {
	dir := filepath.Join(root, artifacts.DirName)
	if os.MkdirAll(dir, 0o750) != nil {
		return
	}
	p := filepath.Join(dir, "workspace_split.txt")
	if os.WriteFile(p, []byte(raw), 0o600) == nil {
		_ = os.Chmod(p, 0o600)
	}
}

// RunWorkspace runs the Workspace program until the user leaves it.
func RunWorkspace(root string, cfg config.Config, opts WorkspaceOptions) (WorkspaceResult, error) {
	var mu sync.Mutex
	cur := cfg
	getCfg := func() config.Config { mu.Lock(); defer mu.Unlock(); return cur }
	setCfg := func(c config.Config) { mu.Lock(); cur = c; mu.Unlock() }

	var once sync.Once
	var pctx string
	projectContext := func() string {
		once.Do(func() {
			c := getCfg()
			if pc, err := dctx.Collect(root, c); err == nil {
				pctx = pc.SystemPromptFragment()
			}
			if data, err := os.ReadFile(filepath.Join(root, artifacts.DirName, artifacts.ContextPackFile)); err == nil && len(data) > 0 { // #nosec G304 -- fixed path under the project root
				pctx += "\n\n## Context pack\n" + string(data)
			}
		})
		return pctx
	}

	// checkCmds resolves the Build & test commands. It is used at split time
	// and again when the check node runs, so a project that had no files at
	// split time gets its language detected late.
	checkCmds := func() []string {
		c := getCfg()
		cmds := lang.CommandsFor(lang.Resolve(c.Project.Language, root), root)
		var out []string
		if cmds.Build != "" {
			out = append(out, cmds.Build)
		}
		if c.Project.TestCmd != "" {
			out = append(out, c.Project.TestCmd)
		} else {
			out = append(out, cmds.Test...)
		}
		return out
	}

	var engMu sync.Mutex
	var engines []*workspace.Engine
	deps := wsDeps{
		split: func(ctx context.Context, task, earlier string, existing []string, next int, onToken func(string)) (*workspace.Graph, runner.TokenUsage, error) {
			c := getCfg()
			agents := make(map[string]config.AgentConfig, len(c.Agents))
			for k, v := range c.Agents {
				agents[k] = v
			}
			c.Agents = agents
			runner.ApplyModelMemory(&c)
			pm := c.Agents["pm"]
			r, err := runner.New(pm, nil, c.PromptLanguage)
			if err != nil {
				return nil, runner.TokenUsage{}, fmt.Errorf("lead runner: %w", err)
			}
			pc := projectContext()
			if earlier != "" {
				pc += "\n\n## Earlier in this session\n" + earlier
			}
			var raw strings.Builder
			g, u, err := workspace.SplitNext(ctx, r, pm.Model, task, workspace.Roles(c), pc, existing, next, func(s string) {
				raw.WriteString(s)
				if onToken != nil {
					onToken(s)
				}
			})
			writeSplitRaw(root, raw.String())
			return g, u, err
		},
		newEngine: func(g *workspace.Graph, save func(*workspace.Graph) error, summary func() string) *workspace.Engine {
			e := workspace.NewEngineWithSave(g, wsLiveExec{root: root, cfg: getCfg, pctx: projectContext, checks: checkCmds, summary: summary},
				workspace.MaxParallel(getCfg()), save)
			engMu.Lock()
			engines = append(engines, e)
			engMu.Unlock()
			return e
		},
		saveRole: func(name string, ac config.AgentConfig) (config.Config, error) {
			if err := config.SaveProjectRole(root, name, ac); err != nil {
				return config.Config{}, err
			}
			c, err := config.Load(root)
			if err != nil {
				return config.Config{}, err
			}
			setCfg(c)
			return c, nil
		},
		applyProfile: func(name string) (config.Config, error) {
			if err := config.ApplyProfile(root, name); err != nil {
				return config.Config{}, err
			}
			c, err := config.Load(root)
			if err != nil {
				return config.Config{}, err
			}
			setCfg(c)
			return c, nil
		},
		checkCmds: checkCmds,
	}

	prog := tea.NewProgram(newWorkspaceModel(root, cfg, deps, opts), tea.WithAltScreen())
	res, err := prog.Run()
	engMu.Lock()
	for _, e := range engines {
		e.Close()
	}
	engMu.Unlock()
	if err != nil {
		return WorkspaceResult{Cfg: getCfg()}, err
	}
	if final, ok := res.(workspaceModel); ok {
		return WorkspaceResult{OpenMenu: final.openMenu, SwitchProject: final.switchTo, Cfg: final.cfg}, nil
	}
	return WorkspaceResult{Cfg: getCfg()}, nil
}

// ── Model ────────────────────────────────────────────────────────────────────

type wsScreen int

const (
	wsScreenMain wsScreen = iota
	wsScreenOutput
)

type wsFocus int

const (
	wsFocusPrompt wsFocus = iota
	wsFocusGraph
)

type wsOverlay int

const (
	wsOverlayNone wsOverlay = iota
	wsOverlayPicker
	wsOverlayRoles
	wsOverlayForm
	wsOverlayConfirm
	wsOverlaySessions
	wsOverlayProjects
	wsOverlayProfiles
	wsOverlayText
	wsOverlayInfo
)

// wsConfirm is a pending y/n question. kind: quit | menu | clear | delsession
// | delprofile. back is the list overlay to return to on "no".
type wsConfirm struct {
	kind, text, arg string
	back            wsOverlay
}

// wsStream carries streamed split text from the split goroutine to the UI. It
// keeps only the current line and the last finished non-empty line, so reading
// the last line never re-splits the whole text.
type wsStream struct {
	mu   sync.Mutex
	cur  string // text after the last newline
	done string // last non-empty finished line
}

const wsStreamCur = 8192

func (s *wsStream) add(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur += t
	if i := strings.LastIndex(s.cur, "\n"); i >= 0 {
		finished := s.cur[:i]
		s.cur = s.cur[i+1:]
		for len(finished) > 0 {
			line := finished
			if k := strings.LastIndex(finished, "\n"); k >= 0 {
				line, finished = finished[k+1:], finished[:k]
			} else {
				finished = ""
			}
			if l := strings.TrimSpace(line); l != "" {
				s.done = l
				break
			}
		}
	}
	if len(s.cur) > wsStreamCur {
		cut := len(s.cur) - wsStreamCur
		for cut < len(s.cur) && !utf8.RuneStart(s.cur[cut]) {
			cut++
		}
		s.cur = s.cur[cut:]
	}
}

func (s *wsStream) lastLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l := strings.TrimSpace(s.cur); l != "" {
		return l
	}
	return s.done
}

type (
	wsSplitDoneMsg struct {
		gen   int
		g     *workspace.Graph
		usage runner.TokenUsage
		err   error
	}
	wsEventMsg struct {
		eng *workspace.Engine
		ev  workspace.Event
	}
	wsTickMsg    struct{}
	wsStoppedMsg struct{}
)

func wsTick() tea.Cmd {
	return tea.Tick(125*time.Millisecond, func(time.Time) tea.Msg { return wsTickMsg{} })
}

// wsWaitEvent reads one engine event; the model re-arms it after each event.
// It also returns when the engine is closed, so no goroutine stays blocked on
// a discarded engine.
func wsWaitEvent(e *workspace.Engine) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-e.Events():
			return wsEventMsg{eng: e, ev: ev}
		case <-e.Done():
			return nil
		}
	}
}

// closeEngine closes e in the background: Close waits for running nodes.
func closeEngine(e *workspace.Engine) {
	if e != nil {
		go e.Close()
	}
}

// wsSess shares the session between the model and the engine's save callback,
// which runs on engine goroutines.
type wsSess struct {
	mu   sync.Mutex
	root string
	s    *workspace.Session
}

func newWsSess(root string, s *workspace.Session) *wsSess { return &wsSess{root: root, s: s} }

// snapshot returns a shallow copy that is safe to read while the engine saves.
func (p *wsSess) snapshot() workspace.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	return *p.s
}

func (p *wsSess) update(f func(s *workspace.Session)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p.s)
}

func (p *wsSess) addLine(who, text string) {
	p.update(func(s *workspace.Session) { s.AddLine(who, text) })
}

// hasContent is true once the session holds a task or a graph; empty sessions
// are never written.
func (p *wsSess) hasContent() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hasContentLocked()
}

func (p *wsSess) hasContentLocked() bool {
	if p.s.Graph != nil && len(p.s.Graph.Nodes) > 0 {
		return true
	}
	for _, l := range p.s.Transcript {
		if l.Who == "you" {
			return true
		}
	}
	return false
}

func (p *wsSess) save() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.hasContentLocked() {
		return nil
	}
	return p.s.Save(p.root)
}

// saveGraph is the engine's save callback: it copies g into the session.
func (p *wsSess) saveGraph(g *workspace.Graph) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.s.Graph = g.Clone()
	return p.s.Save(p.root)
}

type workspaceModel struct {
	root string
	cfg  config.Config
	deps wsDeps
	now  func() time.Time

	width, height int
	screen        wsScreen
	focus         wsFocus
	showHelp      bool
	overlay       wsOverlay
	confirm       wsConfirm
	openMenu      bool
	switchTo      string
	quitting      bool
	quitAt        time.Time
	notice        string
	noticeErr     bool

	// session and prompt
	sess        *wsSess
	nSaved      int // saved sessions of this project, for the empty state
	custom      []workspace.CustomCommand
	input       textarea.Model
	scroll      int // chat lines scrolled up from the newest
	cmdIdx      int
	cmdPicked   bool // the highlight was moved with the arrows
	splitting   bool
	splitGen    int
	splitTask   string
	cancelSplit context.CancelFunc
	stream      *wsStream

	// graph
	eng       *workspace.Engine
	g         *workspace.Graph
	sel       string
	inspFlip  bool
	outputs   map[string][]string
	partial   map[string]string
	outSize   map[string]int // bytes stored per node, partial line excluded
	runCtx    context.Context
	runCancel context.CancelFunc
	ticking   bool
	spin      int

	// output screen
	outVP     viewport.Model
	outNode   string
	outFollow bool

	// overlays
	pickNode      string
	pickIdx       int
	formName      textinput.Model
	formDesc      textinput.Model
	formFocus     int
	formErr       string
	formNode      string
	formCanChange bool // "Can change files"; false saves a read-only role

	listIdx  int // cursor of the sessions, projects and profiles lists
	sessions []workspace.SessionInfo
	projects []wsProject
	profiles []string
	textIn   textinput.Model
	textKind string // "path" | "profile"
	textErr  string
	infoName string
	infoText []string
	infoTop  int
}

func newWorkspaceModel(root string, cfg config.Config, deps wsDeps, opts ...WorkspaceOptions) workspaceModel {
	deps.fillDefaults(root)
	ta := textarea.New()
	ta.Prompt = " "
	ta.ShowLineNumbers = false
	ta.Placeholder = wsInputHint
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.KeyMap.InsertNewline = bkey.NewBinding(bkey.WithKeys("alt+enter", "ctrl+j"))
	for _, st := range []*textarea.Style{&ta.FocusedStyle, &ta.BlurredStyle} {
		st.Base = lipgloss.NewStyle()
		st.CursorLine = lipgloss.NewStyle()
		st.Text = lipgloss.NewStyle().Foreground(crt.primary)
		st.Placeholder = lipgloss.NewStyle().Foreground(crt.dim)
	}
	ta.Cursor.SetMode(cursor.CursorStatic)
	ta.Focus()

	newField := func(placeholder string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholder
		ti.CharLimit = 200
		ti.Cursor.SetMode(cursor.CursorStatic)
		ti.PromptStyle = lipgloss.NewStyle().Foreground(crt.dim)
		ti.TextStyle = lipgloss.NewStyle().Foreground(crt.primary)
		ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(crt.dim)
		return ti
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	m := workspaceModel{
		root: root, cfg: cfg, deps: deps, now: time.Now,
		width: 80, height: 24,
		input:     ta,
		g:         &workspace.Graph{},
		sess:      newWsSess(root, workspace.NewSession()),
		outputs:   map[string][]string{},
		partial:   map[string]string{},
		outSize:   map[string]int{},
		runCtx:    runCtx,
		runCancel: runCancel,
		formName:  newField("snake_case_name"),
		formDesc:  newField("what this role does, in one line"),
		textIn:    newField(""),
		outVP:     viewport.New(78, 20),
	}
	m.resize()
	if _, err := workspace.MigrateLegacy(root); err != nil {
		m.setNotice("old workspace graph: "+oneLine(err.Error()), true)
	}
	m.custom = deps.customCommands()
	m.refreshSaved()
	if len(opts) > 0 && opts[0].Resume {
		m = m.openSessions()
	}
	return m
}

func (m workspaceModel) Init() tea.Cmd { return nil }

func (m workspaceModel) project() string {
	if m.cfg.Project.Name != "" {
		return m.cfg.Project.Name
	}
	if b := filepath.Base(m.root); b != "/" && b != "." {
		return b
	}
	return ""
}

// chatRows is the height of the chat strip.
func (m workspaceModel) chatRows() int {
	switch {
	case m.height >= 24:
		return 3
	case m.height >= 20:
		return 2
	}
	return 1
}

// promptRows is the text height of the prompt box (the border adds 2).
func (m workspaceModel) promptRows() int {
	if m.height < 24 {
		return 1
	}
	return 3
}

func (m *workspaceModel) resize() {
	m.input.SetWidth(max(m.width-2, 10))
	m.input.SetHeight(m.promptRows())
	m.outVP.Width = max(m.width, 1)
	m.outVP.Height = max(m.height-3, 1)
	fw := max(min(70, m.width-2)-4-22, 10)
	m.formName.Width = fw
	m.formDesc.Width = fw
	m.textIn.Width = max(min(70, m.width-2)-6, 10)
}

func (m *workspaceModel) startTick() tea.Cmd {
	if m.ticking {
		return nil
	}
	m.ticking = true
	return wsTick()
}

func (m *workspaceModel) setNotice(s string, isErr bool) {
	m.notice, m.noticeErr = s, isErr
}

func (m *workspaceModel) setFocus(f wsFocus) {
	m.focus = f
	if f == wsFocusPrompt {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
}

func (m workspaceModel) defined(name string) bool {
	_, ok := workspace.FindRole(m.cfg, name)
	return ok
}

func (m workspaceModel) selNode() *workspace.Node {
	if m.g == nil {
		return nil
	}
	return m.g.Node(m.sel)
}

func (m workspaceModel) hasNodes() bool { return m.g != nil && len(m.g.Nodes) > 0 }

func (m workspaceModel) running() int {
	if m.eng == nil {
		return 0
	}
	return m.eng.Running()
}

// saveSession writes the session when it holds anything; a failure shows up in
// the status line.
func (m *workspaceModel) saveSession() {
	if err := m.sess.save(); err != nil {
		m.setNotice("could not save the session: "+oneLine(err.Error()), true)
		return
	}
	m.refreshSaved()
}

func (m *workspaceModel) refreshSaved() { m.nSaved = workspace.CountSessions(m.root) }

// say adds a chat line to the session. who: lead | system | error.
func (m *workspaceModel) say(who, text string) {
	m.scroll = 0
	m.sess.addLine(who, text)
}

// ── Update ───────────────────────────────────────────────────────────────────

func (m workspaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if (m.width >= wsInspectorMin) != (msg.Width >= wsInspectorMin) {
			m.inspFlip = false // the flip means "hide" when wide and "open" when narrow
		}
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		if m.screen == wsScreenOutput {
			m.refreshOutput()
		}
		return m, nil
	case wsSplitDoneMsg:
		return m.onSplitDone(msg)
	case wsEventMsg:
		return m.onEvent(msg)
	case wsStoppedMsg:
		if m.eng != nil {
			m.g = m.eng.Snapshot()
		}
		return m, nil
	case wsTickMsg:
		m.spin++
		if m.screen == wsScreenOutput && len(m.outputs[m.outNode]) == 0 {
			m.refreshOutput() // keeps the waiting timer moving
		}
		if m.splitting || m.running() > 0 {
			return m, wsTick()
		}
		m.ticking = false
		return m, nil
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	if m.screen == wsScreenMain && m.focus == wsFocusPrompt && m.overlay == wsOverlayNone && !m.splitting {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m workspaceModel) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m.ctrlC()
	}
	m.quitAt = time.Time{}
	if m.notice != "" {
		m.setNotice("", false)
	}
	if m.showHelp {
		if k == "?" || k == "esc" || k == "q" {
			m.showHelp = false
		}
		return m, nil
	}
	if m.overlay != wsOverlayNone {
		return m.overlayKey(msg)
	}
	if m.screen == wsScreenOutput {
		return m.outputKey(msg)
	}
	if m.splitting {
		if k == "esc" {
			return m.cancelSplitting(), nil
		}
		return m, nil
	}
	if m.focus == wsFocusGraph {
		if isBurst(msg) {
			return m.insertBurst(msg), nil
		}
		return m.graphKey(msg)
	}
	return m.promptKey(msg)
}

func (m workspaceModel) ctrlC() (tea.Model, tea.Cmd) {
	now := m.now()
	if !m.quitAt.IsZero() && now.Sub(m.quitAt) <= 2*time.Second {
		return m.quit(false)
	}
	m.quitAt = now
	if m.running() > 0 {
		m.setNotice("Nodes are running. Press Ctrl+C again to stop them and quit", false)
	} else {
		m.setNotice("Press Ctrl+C again to quit", false)
	}
	return m, nil
}

// quit saves the session, stops every node, then ends the program.
func (m workspaceModel) quit(openMenu bool) (tea.Model, tea.Cmd) {
	m.saveSession()
	m.openMenu = openMenu
	m.quitting = true
	eng, cancel, cancelSplit := m.eng, m.runCancel, m.cancelSplit
	return m, func() tea.Msg {
		if cancelSplit != nil {
			cancelSplit()
		}
		if eng != nil {
			eng.Close()
		}
		cancel()
		return tea.QuitMsg{}
	}
}

// requestQuit asks first while nodes are running.
func (m workspaceModel) requestQuit(openMenu bool) (tea.Model, tea.Cmd) {
	if n := m.running(); n > 0 {
		kind := "quit"
		if openMenu {
			kind = "menu"
		}
		m.confirm = wsConfirm{kind: kind, text: fmt.Sprintf("%d node(s) are running. Stop them and leave?", n)}
		m.overlay = wsOverlayConfirm
		return m, nil
	}
	return m.quit(openMenu)
}

// ── Prompt ───────────────────────────────────────────────────────────────────

// paletteOpen is true while a command name is being typed: the text starts
// with "/" and has no space or line break yet.
func (m workspaceModel) paletteOpen() bool {
	v := m.input.Value()
	return strings.HasPrefix(v, "/") && !strings.ContainsAny(v, " \t\n")
}

// isBurst is true for a paste or a burst of several characters in one message.
func isBurst(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && (msg.Paste || len(msg.Runes) > 1)
}

// insertBurst puts pasted text into the prompt as text: it is never sent and
// never run as a command, whatever line breaks it holds.
func (m workspaceModel) insertBurst(msg tea.KeyMsg) workspaceModel {
	text := strings.ReplaceAll(strings.ReplaceAll(string(msg.Runes), "\r\n", "\n"), "\r", "\n")
	m.setFocus(wsFocusPrompt)
	m.input.InsertString(workspace.SanitizeText(text))
	m.cmdIdx = 0
	m.cmdPicked = false
	m.custom = m.deps.customCommands()
	return m
}

func (m workspaceModel) promptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if isBurst(msg) {
		return m.insertBurst(msg), nil
	}
	k := msg.String()
	empty := m.input.Value() == ""
	open := m.paletteOpen()
	matches := m.cmdMatches(m.input.Value())
	switch k {
	case "pgup":
		m.scroll += 3
		return m, nil
	case "pgdown":
		m.scroll = max(m.scroll-3, 0)
		return m, nil
	case "?":
		if empty {
			m.showHelp = true
			return m, nil
		}
	case "tab":
		if open {
			if len(matches) > 0 {
				m.input.SetValue(matches[min(m.cmdIdx, len(matches)-1)].name)
				m.cmdIdx = 0
				m.cmdPicked = false
			}
			return m, nil
		}
		m.setFocus(wsFocusGraph)
		return m, nil
	case "shift+tab":
		m.setFocus(wsFocusGraph)
		return m, nil
	case "esc":
		switch {
		case open:
			m.input.Reset()
			m.cmdIdx = 0
		case m.hasNodes():
			m.setFocus(wsFocusGraph)
		}
		return m, nil
	case "up", "down":
		if open {
			if k == "up" {
				m.cmdIdx = max(m.cmdIdx-1, 0)
			} else {
				m.cmdIdx = min(m.cmdIdx+1, max(len(matches)-1, 0))
			}
			m.cmdPicked = true
			return m, nil
		}
	case "enter":
		return m.submit()
	}
	was := open
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.cmdIdx = 0
	m.cmdPicked = false
	if !was && m.paletteOpen() {
		m.custom = m.deps.customCommands() // pick up files added while open
	}
	return m, cmd
}

// submit runs the prompt text as a command or sends it as a task. Text that
// does not start with "/" is a task. So is a single line whose first word has
// a second "/" (a path), and "//text" sends "/text".
func (m workspaceModel) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return m.sendTask(text, text)
	}
	if strings.HasPrefix(text, "//") {
		return m.sendTask(text[1:], text[1:])
	}
	first, args, _ := strings.Cut(text, " ")
	if strings.Contains(first[1:], "/") {
		return m.sendTask(text, text)
	}
	m.custom = m.deps.customCommands()
	name := strings.ToLower(first)
	c, ok := m.lookup(name)
	if !ok && m.paletteOpen() && args == "" {
		matches := m.cmdMatches(name)
		switch {
		case len(matches) == 1 || (len(matches) > 1 && m.cmdPicked):
			c, ok = matches[min(m.cmdIdx, len(matches)-1)], true
		case len(matches) > 1:
			names := make([]string, len(matches))
			for i, mc := range matches {
				names[i] = mc.name
			}
			sort.Strings(names)
			list := idList(names)
			m.say("error", fmt.Sprintf("%s matches several commands: %s — keep typing or press Tab", name, list))
			return m, nil
		}
	}
	if !ok {
		m.say("error", fmt.Sprintf("unknown command %s — /help lists them", name))
		return m, nil
	}
	m.input.Reset()
	m.cmdIdx = 0
	m.cmdPicked = false
	return c.run(m, strings.TrimSpace(args))
}

// earlierContext is what the lead is told about the session so far.
func (m workspaceModel) earlierContext() string {
	var parts []string
	if s := strings.TrimSpace(m.sess.snapshot().Summary); s != "" {
		parts = append(parts, "Finished and folded into this summary:\n"+s)
	}
	var nodes []string
	for _, n := range m.g.Nodes {
		if n.Kind != workspace.KindWork {
			continue
		}
		line := fmt.Sprintf("- %s %s [%s]", n.ID, n.Title, n.Status)
		if n.Role != "" {
			line += " by " + n.Role
		}
		nodes = append(nodes, line)
	}
	if len(nodes) > 0 {
		parts = append(parts, "Nodes already in the graph:\n"+strings.Join(nodes, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// sendTask sends task to the lead; shown is what the chat displays.
func (m workspaceModel) sendTask(task, shown string) (tea.Model, tea.Cmd) {
	if m.running() > 0 {
		m.setNotice("Stop the running nodes first (Tab to the graph, then S), then send the task", true)
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.splitGen++
	gen := m.splitGen
	m.splitting = true
	m.splitTask = shown
	m.cancelSplit = cancel
	m.stream = &wsStream{}
	m.say("you", shown)
	m.input.Reset()
	m.input.Placeholder = wsSplittingHint
	earlier := m.earlierContext()
	var existing []string
	next := 0
	if m.hasNodes() {
		for _, n := range m.g.Nodes {
			existing = append(existing, n.ID)
		}
		next = max(m.sess.snapshot().NextID, nextWorkNumber(m.g))
	}
	split, stream := m.deps.split, m.stream
	cmd := func() tea.Msg {
		g, u, err := split(ctx, task, earlier, existing, next, stream.add)
		return wsSplitDoneMsg{gen: gen, g: g, usage: u, err: err}
	}
	return m, tea.Batch(cmd, m.startTick())
}

func (m workspaceModel) cancelSplitting() workspaceModel {
	if m.cancelSplit != nil {
		m.cancelSplit()
	}
	m.cancelSplit = nil
	m.splitting = false
	m.splitGen++
	m.restoreTask()
	m.say("error", "split cancelled. Your text is still in the box")
	return m
}

func (m workspaceModel) onSplitDone(msg wsSplitDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.splitGen || !m.splitting {
		return m, nil
	}
	m.splitting = false
	m.cancelSplit = nil
	m.input.Placeholder = wsInputHint
	if msg.err != nil {
		m.restoreTask()
		m.say("error", oneLine(msg.err.Error()))
		return m, nil
	}
	if msg.g == nil || len(msg.g.Nodes) == 0 {
		m.restoreTask()
		m.say("error", "the lead returned no sub-tasks. Try describing the task differently")
		return m, nil
	}
	m.sess.update(func(s *workspace.Session) {
		s.In += msg.usage.InputTokens
		s.Out += msg.usage.OutputTokens
	})
	m.input.Reset()
	if m.eng == nil || !m.hasNodes() {
		return m.openGraph(msg.g)
	}
	return m.appendGraph(msg.g)
}

// restoreTask puts the task being split back into the input for editing.
func (m *workspaceModel) restoreTask() {
	m.input.Placeholder = wsInputHint
	m.input.SetValue(m.splitTask)
}

// nextWorkNumber is one more than the highest T<number> node ID (1 for none).
func nextWorkNumber(g *workspace.Graph) int {
	top := 0
	for _, n := range g.Nodes {
		var v int
		if _, err := fmt.Sscanf(n.ID, "T%d", &v); err == nil && v > top {
			top = v
		}
	}
	return top + 1
}

// splitNotice is the chat line after a split: "split into 1 node: T1".
func splitNotice(ids []string) string {
	return fmt.Sprintf("split into %d %s: %s", len(ids), pluralize(len(ids), "node", "nodes"), idList(ids))
}

// idList is "T1, T2, T3" for at most six IDs, then "…".
func idList(ids []string) string {
	if len(ids) > 6 {
		return strings.Join(ids[:6], ", ") + ", …"
	}
	return strings.Join(ids, ", ")
}

// openGraph starts the engine on the first graph of a session.
func (m workspaceModel) openGraph(g *workspace.Graph) (tea.Model, tea.Cmd) {
	// The Build & test node is always there; without known commands it asks
	// for them when it runs.
	var cmds []string
	if m.deps.checkCmds != nil {
		cmds = m.deps.checkCmds()
	}
	if len(cmds) > 0 {
		g.AddCheck(cmds)
	} else {
		g.AddCheckDeferred()
	}
	var ids []string
	for _, n := range g.Nodes {
		if n.Kind == workspace.KindWork {
			ids = append(ids, n.ID)
		}
	}
	m.sess.update(func(s *workspace.Session) {
		if s.Title == "" {
			s.Title = g.Title
		}
		s.NextID = nextWorkNumber(g)
	})
	m.say("lead", splitNotice(ids))
	m, cmd := m.attachGraph(g, true)
	m.setFocus(wsFocusGraph)
	m.saveSession()
	return m, cmd
}

// appendGraph adds the nodes of a follow-up split to the live graph.
func (m workspaceModel) appendGraph(add *workspace.Graph) (tea.Model, tea.Cmd) {
	add.Sanitize()
	next := m.sess.snapshot().NextID
	var idMap map[string]string
	err := m.eng.Replace(func(g *workspace.Graph) { next, idMap = g.Append(add, next) })
	if err != nil {
		m.restoreTask()
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	var ids []string
	for _, n := range add.Nodes {
		if id, ok := idMap[n.ID]; ok {
			ids = append(ids, id)
		}
	}
	m.sess.update(func(s *workspace.Session) { s.NextID = next })
	m.say("lead", splitNotice(ids))
	m.g = m.eng.Snapshot()
	if len(ids) > 0 {
		m.sel = ids[0]
	}
	m.setFocus(wsFocusGraph)
	m.saveSession()
	return m, nil
}

// attachGraph hands g to a new engine, which saves through the session.
func (m workspaceModel) attachGraph(g *workspace.Graph, fresh bool) (workspaceModel, tea.Cmd) {
	closeEngine(m.eng)
	g.Sanitize()
	if err := m.sess.saveGraph(g); err != nil {
		m.setNotice("could not save the session: "+oneLine(err.Error()), true)
	}
	sess := m.sess
	m.eng = m.deps.newEngine(g, sess.saveGraph, func() string { return sess.snapshot().Summary })
	m.g = m.eng.Snapshot()
	m.sel = ""
	if len(m.g.Nodes) > 0 {
		m.sel = m.g.Nodes[0].ID
	}
	m.outputs = map[string][]string{}
	m.partial = map[string]string{}
	m.outSize = map[string]int{}
	m.screen = wsScreenMain
	m.overlay = wsOverlayNone
	m.scroll = 0
	return m, tea.Batch(wsWaitEvent(m.eng), m.startTick())
}

// resetSession drops the engine and the graph and starts an empty session.
func (m workspaceModel) resetSession(s *workspace.Session) workspaceModel {
	closeEngine(m.eng)
	m.eng = nil
	m.g = &workspace.Graph{}
	m.sel = ""
	m.sess = newWsSess(m.root, s)
	m.outputs = map[string][]string{}
	m.partial = map[string]string{}
	m.outSize = map[string]int{}
	m.screen = wsScreenMain
	m.overlay = wsOverlayNone
	m.scroll = 0
	m.input.Reset()
	m.input.Placeholder = wsInputHint
	m.setFocus(wsFocusPrompt)
	return m
}

// report shows a message in the chat. isErr marks it with "! ".
func (m *workspaceModel) report(s string, isErr bool) {
	who := "system"
	if isErr {
		who = "error"
	}
	m.say(who, s)
}

// ── Engine events ────────────────────────────────────────────────────────────
func (m workspaceModel) onEvent(msg wsEventMsg) (tea.Model, tea.Cmd) {
	if msg.eng != m.eng || m.eng == nil {
		return m, nil
	}
	ev := msg.ev
	switch ev.Type {
	case workspace.EventStarted:
		m.outputs[ev.NodeID] = nil
		m.partial[ev.NodeID] = ""
		m.outSize[ev.NodeID] = 0
	case workspace.EventOutput:
		if ev.NodeID == "" {
			m.setNotice(oneLine(ev.Text), true)
		} else {
			m.appendOutput(ev.NodeID, ev.Text)
		}
	case workspace.EventDone:
		m.setNotice("✓ "+ev.NodeID+" done", false)
	case workspace.EventFailed:
		if ev.Text == "stopped" {
			m.setNotice(ev.NodeID+" stopped", false)
		} else {
			m.setNotice(ev.NodeID+" failed: "+oneLine(ev.Text), true)
		}
	}
	m.g = m.eng.Snapshot()
	if m.screen == wsScreenOutput {
		m.refreshOutput()
	}
	return m, tea.Batch(wsWaitEvent(m.eng), m.startTick())
}

// wsMaxOutputBytes caps the stored output of one node.
const wsMaxOutputBytes = 1 << 20

// wsMaxPartial is the longest unfinished line kept before it is stored as is.
const wsMaxPartial = 64 << 10

// cleanOutputLine removes terminal escape sequences and control characters
// and expands tabs, so stored output cannot move the cursor or recolor the UI.
func cleanOutputLine(s string) string {
	s = strings.TrimRight(s, "\r")
	if i := strings.LastIndex(s, "\r"); i >= 0 { // a carriage return overwrites the line
		s = s[i+1:]
	}
	if strings.ContainsRune(s, '\x1b') {
		s = ansi.Strip(s)
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func (m *workspaceModel) appendOutput(id, text string) {
	buf := m.partial[id] + text
	parts := strings.Split(buf, "\n")
	rest := parts[len(parts)-1]
	complete := parts[:len(parts)-1]
	if len(rest) > wsMaxPartial {
		complete = append(complete, rest)
		rest = ""
	}
	m.partial[id] = rest
	lines := m.outputs[id]
	size := m.outSize[id]
	for _, l := range complete {
		l = cleanOutputLine(l)
		lines = append(lines, l)
		size += len(l) + 1
	}
	drop := 0
	for len(lines)-drop > wsMaxOutput || (size > wsMaxOutputBytes && len(lines)-drop > 1) {
		size -= len(lines[drop]) + 1
		drop++
	}
	if drop > 0 {
		lines = append([]string(nil), lines[drop:]...)
	}
	m.outputs[id] = lines
	m.outSize[id] = size
}

// outputLines returns every stored line of a node plus its unfinished line.
func (m workspaceModel) outputLines(id string) []string {
	lines := m.outputs[id]
	if p := cleanOutputLine(m.partial[id]); p != "" {
		lines = append(append([]string(nil), lines...), p)
	}
	return lines
}

// outputTail returns at most n of the newest lines.
func (m workspaceModel) outputTail(id string, n int) []string {
	lines := m.outputs[id]
	p := cleanOutputLine(m.partial[id])
	if p != "" {
		n--
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := append([]string(nil), lines...)
	if p != "" {
		out = append(out, p)
	}
	return out
}

func (m *workspaceModel) refreshOutput() {
	lines := m.outputLines(m.outNode)
	content := strings.Join(lines, "\n")
	if len(lines) == 0 {
		msg := "(no output yet)"
		if n := m.g.Node(m.outNode); n != nil && n.Status == workspace.StatusRunning {
			msg = fmt.Sprintf("waiting for %s's first output… (%s)", n.Role, formatElapsed(m.now().Sub(n.StartedAt)))
		}
		content = lipgloss.NewStyle().Foreground(crt.dim).Render(msg)
	}
	m.outVP.SetContent(content)
	if m.outFollow {
		m.outVP.GotoBottom()
	}
}

// ── Graph screen ─────────────────────────────────────────────────────────────

func (m workspaceModel) graphKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch k {
	case "tab", "shift+tab", "c":
		m.setFocus(wsFocusPrompt)
		return m, nil
	case "/":
		m.setFocus(wsFocusPrompt)
		m.input.InsertString("/")
		m.custom = m.deps.customCommands()
		m.cmdIdx = 0
		return m, nil
	case "?":
		m.showHelp = true
		return m, nil
	case "q":
		return m.requestQuit(false)
	case "i":
		m.inspFlip = !m.inspFlip
		return m, nil
	case "esc":
		m.inspFlip = m.inspFlip && m.width >= wsInspectorMin // closes the narrow inspector box
		return m, nil
	}
	if m.eng == nil || !m.hasNodes() {
		return m, nil
	}
	dirs := map[string]string{"up": "up", "k": "up", "down": "down", "j": "down", "left": "left", "h": "left", "right": "right", "l": "right"}
	if d, ok := dirs[k]; ok {
		m.sel = canvasNeighbor(m.g, m.sel, d)
		return m, nil
	}
	n := m.selNode()
	switch k {
	case "a":
		return m.openPicker(n)
	case "A":
		return m.acceptAll()
	case "r":
		if n == nil {
			return m, nil
		}
		if err := m.eng.Run(m.runCtx, n.ID); err != nil {
			m.setNotice("! "+err.Error(), true)
			return m, nil
		}
		m.g = m.eng.Snapshot()
		m.setNotice("Started "+n.ID, false)
		return m, m.startTick()
	case "R":
		return m.runAll()
	case "s":
		if n != nil && n.Status == workspace.StatusRunning {
			m.eng.Stop(n.ID)
			m.setNotice("Stopping "+n.ID+"…", false)
		} else if n != nil {
			m.setNotice(n.ID+" is not running", false)
		}
	case "S":
		eng := m.eng
		m.setNotice("Stopping all nodes…", false)
		return m, func() tea.Msg { eng.StopAll(); return wsStoppedMsg{} }
	case "f":
		if n == nil || n.Kind != workspace.KindCheck || n.Status != workspace.StatusFailed {
			m.setNotice("f adds a fix node, and only after Build & test has failed", true)
			return m, nil
		}
		id, err := m.eng.AddFix(n.ID)
		if err != nil {
			m.setNotice("! "+err.Error(), true)
			return m, nil
		}
		m.g = m.eng.Snapshot()
		m.sel = id
		m.setNotice("Added "+id+". Assign it, run it, then run Build & test again (or press R).", false)
	case "o", "enter":
		if n != nil {
			m.screen = wsScreenOutput
			m.outNode = n.ID
			m.outFollow = true
			m.refreshOutput()
		}
	}
	return m, nil
}

func (m workspaceModel) openPicker(n *workspace.Node) (tea.Model, tea.Cmd) {
	switch {
	case n == nil:
	case n.Kind == workspace.KindCheck:
		m.setNotice(n.ID+" needs no agent: it runs your build and test commands", false)
	case n.Status != workspace.StatusTodo && n.Status != workspace.StatusFailed:
		m.setNotice(fmt.Sprintf("%s is %s and cannot be reassigned", n.ID, n.Status), true)
	default:
		m.pickNode = n.ID
		m.overlay = wsOverlayPicker
		m.pickIdx = pickDefaultIndex(buildPickItems(m.g, n, m.cfg))
	}
	return m, nil
}

func (m workspaceModel) acceptAll() (tea.Model, tea.Cmd) {
	count := m.eng.AcceptSuggestions(m.defined)
	m.g = m.eng.Snapshot()
	need := 0
	for _, n := range m.g.Nodes {
		if n.Kind == workspace.KindWork && n.Role == "" && (n.Status == workspace.StatusTodo || n.Status == workspace.StatusFailed) {
			need++
		}
	}
	note := fmt.Sprintf("assigned %d nodes; %d need a new role", count, need)
	var joined []string
	for _, n := range m.g.Nodes {
		if n.Role != "" && m.eng.WillAutoRun(n.ID) && n.Kind == workspace.KindWork {
			joined = append(joined, n.ID)
		}
	}
	switch {
	case len(joined) == 1:
		note += "; " + joined[0] + " joins the running batch and starts when its dependencies finish"
	case len(joined) > 1:
		note += fmt.Sprintf("; %d nodes join the running batch and start when their dependencies finish", len(joined))
	}
	m.setNotice(note, false)
	return m, nil
}

func (m workspaceModel) runAll() (tea.Model, tea.Cmd) {
	g := m.eng.Snapshot()
	unassigned := 0
	for _, n := range g.Nodes {
		if n.Kind == workspace.KindWork && n.Role == "" && n.Status == workspace.StatusTodo {
			unassigned++
		}
	}
	_, running, _, _ := g.Counts()
	if len(g.Runnable()) == 0 && running == 0 {
		if unassigned > 0 {
			m.setNotice(fmt.Sprintf("Nothing can run yet: %d nodes are unassigned (A accepts suggestions, a picks an agent)", unassigned), true)
		} else {
			m.setNotice("Nothing can run: the remaining nodes wait on unfinished dependencies", true)
		}
		return m, nil
	}
	m.eng.RunAll(m.runCtx)
	m.g = m.eng.Snapshot()
	if unassigned > 0 {
		m.setNotice(fmt.Sprintf("%d nodes are unassigned and will wait", unassigned), false)
	} else {
		m.setNotice("Running every assigned node", false)
	}
	return m, m.startTick()
}

// ── Output screen ────────────────────────────────────────────────────────────

func (m workspaceModel) outputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.screen = wsScreenMain
		return m, nil
	case "?":
		m.showHelp = true
		return m, nil
	case "s":
		if m.eng != nil {
			m.eng.Stop(m.outNode)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.outVP, cmd = m.outVP.Update(msg)
	m.outFollow = m.outVP.AtBottom()
	return m, cmd
}

// ── Overlays ─────────────────────────────────────────────────────────────────

func (m workspaceModel) overlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch m.overlay {
	case wsOverlayConfirm:
		return m.confirmKey(k)

	case wsOverlayPicker, wsOverlayRoles:
		var items []wsPickItem
		if m.overlay == wsOverlayPicker {
			items = buildPickItems(m.g, m.g.Node(m.pickNode), m.cfg)
		} else {
			items = m.rolesItems()
		}
		switch k {
		case "esc":
			m.overlay = wsOverlayNone
		case "up", "k":
			m.pickIdx = max(m.pickIdx-1, 0)
		case "down", "j":
			m.pickIdx = min(m.pickIdx+1, len(items)-1)
		case "enter":
			return m.pickChoose(items[min(m.pickIdx, len(items)-1)])
		}
		return m, nil

	case wsOverlayForm:
		return m.formKey(msg)

	case wsOverlaySessions, wsOverlayProjects, wsOverlayProfiles:
		return m.listKey(msg)

	case wsOverlayText:
		return m.textKey(msg)

	case wsOverlayInfo:
		switch k {
		case "esc", "q", "enter":
			m.overlay = wsOverlayNone
		case "up", "k":
			m.infoTop = max(m.infoTop-1, 0)
		case "down", "j":
			m.infoTop = min(m.infoTop+1, max(len(m.infoText)-1, 0))
		case "pgup":
			m.infoTop = max(m.infoTop-5, 0)
		case "pgdown":
			m.infoTop = min(m.infoTop+5, max(len(m.infoText)-1, 0))
		}
		return m, nil
	}
	return m, nil
}

// confirmKey answers the y/n question in m.confirm.
func (m workspaceModel) confirmKey(k string) (tea.Model, tea.Cmd) {
	c := m.confirm
	switch k {
	case "y", "Y":
		m.overlay = wsOverlayNone
		switch c.kind {
		case "quit":
			return m.quit(false)
		case "menu":
			return m.quit(true)
		case "clear":
			return m.doClear(), nil
		case "delsession":
			return m.doDeleteSession(c.arg), nil
		case "delprofile":
			return m.doDeleteProfile(c.arg), nil
		}
	case "n", "N", "esc":
		m.overlay = c.back
	}
	return m, nil
}

func (m workspaceModel) rolesItems() []wsPickItem {
	var items []wsPickItem
	for _, r := range workspace.Roles(m.cfg) {
		items = append(items, wsPickItem{kind: "role", name: r.Name, desc: r.Description, meta: roleMetaShort(r.Agent), skills: strings.Join(r.UserSkills, ", ")})
	}
	return append(items, wsPickItem{kind: "new"})
}

func (m workspaceModel) pickChoose(it wsPickItem) (tea.Model, tea.Cmd) {
	switch it.kind {
	case "role":
		if m.overlay == wsOverlayRoles {
			return m, nil // read-only list
		}
		return m.assign(m.pickNode, it.name)
	case "unassign":
		return m.assign(m.pickNode, "")
	case "create":
		return m.openForm(it.name, it.desc), nil
	case "new":
		return m.openForm("", ""), nil
	}
	return m, nil
}

func (m workspaceModel) assign(id, role string) (tea.Model, tea.Cmd) {
	m.overlay = wsOverlayNone
	if err := m.eng.Assign(id, role); err != nil {
		m.setNotice("! "+err.Error(), true)
		return m, nil
	}
	m.g = m.eng.Snapshot()
	if role == "" {
		m.setNotice(id+" is unassigned", false)
	} else if extra := m.assignExtras(id, role); extra != "" {
		m.setNotice(extra, false)
	} else {
		m.setNotice(id+" assigned to "+role, false)
	}
	return m, nil
}

// assignExtras explains what is special about an assignment: a read-only role,
// or a node that joins a batch that is already running. "" when nothing is.
func (m workspaceModel) assignExtras(id, role string) string {
	var parts []string
	if r, ok := workspace.FindRole(m.cfg, role); ok && r.ReadOnly {
		parts = append(parts, role+" is read-only: it will report, not change files")
	}
	if m.eng != nil && m.eng.WillAutoRun(id) {
		parts = append(parts, id+" joins the running batch and starts when its dependencies finish")
	}
	return strings.Join(parts, "; ")
}

func (m workspaceModel) openForm(name, desc string) workspaceModel {
	m.formNode = ""
	if m.overlay == wsOverlayPicker {
		m.formNode = m.pickNode
	}
	m.overlay = wsOverlayForm
	m.formName.SetValue(name)
	m.formDesc.SetValue(desc)
	m.formErr = ""
	m.formCanChange = true
	m.formFocus = 0
	if name != "" {
		m.formFocus = 1
	}
	m.focusForm()
	return m
}

func (m *workspaceModel) focusForm() {
	m.formName.Blur()
	m.formDesc.Blur()
	switch m.formFocus {
	case 0:
		m.formName.Focus()
	case 1:
		m.formDesc.Focus()
	}
}

func (m workspaceModel) formKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = wsOverlayNone
		return m, nil
	case "tab", "down":
		m.formFocus = (m.formFocus + 1) % 3
		m.focusForm()
		return m, nil
	case "shift+tab", "up":
		m.formFocus = (m.formFocus + 2) % 3
		m.focusForm()
		return m, nil
	case "enter":
		return m.saveForm()
	}
	var cmd tea.Cmd
	switch m.formFocus {
	case 0:
		m.formName, cmd = m.formName.Update(msg)
	case 1:
		m.formDesc, cmd = m.formDesc.Update(msg)
	default:
		switch msg.String() {
		case "left", "right", " ", "space", "h", "l":
			m.formCanChange = !m.formCanChange
		}
	}
	m.formErr = ""
	return m, cmd
}

func (m workspaceModel) saveForm() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.formName.Value())
	desc := strings.TrimSpace(m.formDesc.Value())
	switch {
	case !config.ValidRoleName(name):
		m.formErr = "Name must be 2-32 characters: lowercase letters, digits and _, starting with a letter"
	case m.defined(name) || name == "pr":
		m.formErr = fmt.Sprintf("A role named %q already exists", name)
	case desc == "":
		m.formErr = "Describe what this role does (one line)"
	}
	if m.formErr != "" {
		return m, nil
	}
	cfg, err := m.deps.saveRole(name, config.AgentConfig{Description: desc, ReadOnly: !m.formCanChange})
	if err != nil {
		m.formErr = err.Error()
		return m, nil
	}
	m.cfg = cfg
	m.overlay = wsOverlayNone
	if m.formNode != "" && m.eng != nil {
		if err := m.eng.Assign(m.formNode, name); err != nil {
			m.setNotice("! "+err.Error(), true)
		} else if extra := m.assignExtras(m.formNode, name); extra != "" {
			m.setNotice(extra, false)
		} else {
			m.setNotice(fmt.Sprintf("role %q created", name), false)
		}
		m.g = m.eng.Snapshot()
	} else {
		m.report(fmt.Sprintf("role %q created", name), false)
	}
	return m, nil
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m workspaceModel) View() string {
	w, h := m.width, m.height
	if w < wsMinW || h < wsMinH {
		msg := fmt.Sprintf("Terminal too small (need %d×%d)", wsMinW, wsMinH)
		return paintBackground(ansi.Truncate(msg, max(w, 1), ""), w, h)
	}
	var v string
	switch {
	case m.showHelp:
		v = renderHelp(w, h, "KEYBOARD — WORKSPACE", workspaceHelp(m.commandList()))
	case m.screen == wsScreenOutput:
		v = m.outputView()
	default:
		v = m.mainView()
	}
	if !m.showHelp {
		if box := m.overlayBox(); box != "" {
			v = overlayCenter(v, box, w, h)
		}
	}
	return paintBackground(fitView(v, w, h), w, h)
}

// fitView guarantees at most h lines of at most w cells.
func fitView(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			lines[i] = ansi.Truncate(l, w, "")
		}
	}
	return strings.Join(lines, "\n")
}

func (m workspaceModel) overlayBox() string {
	w, h := m.width, m.height
	switch m.overlay {
	case wsOverlayPicker:
		n := m.g.Node(m.pickNode)
		if n == nil {
			return ""
		}
		return renderPicker("Assign "+n.ID+" · "+n.Title, buildPickItems(m.g, n, m.cfg), m.pickIdx,
			"↑↓ move · Enter choose · Esc close", w, h)
	case wsOverlayRoles:
		return renderPicker("Roles", m.rolesItems(), m.pickIdx,
			"Read-only list. Enter on ＋ new role creates one · Esc close", w, h)
	case wsOverlayForm:
		pm := m.cfg.Agents["pm"]
		inherits := fmt.Sprintf("Runner / model: inherits %s / %s — change it in /config → Project Setup", orDash(pm.Runner), orDash(pm.Model))
		return renderRoleForm(m.formName.View(), m.formDesc.View(), m.formFocus, m.formCanChange, inherits, m.formErr, w)
	case wsOverlayConfirm:
		boxW := min(56, w-2)
		warn := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		var body []string
		for _, l := range wrapText(m.confirm.text, boxW-4) {
			body = append(body, warn.Render(l))
		}
		if !strings.HasSuffix(m.confirm.text, "y/n") { // otherwise the text already asks
			body = append(body, "", keycap("y")+" yes   "+keycap("n")+" no")
		}
		return wsBox("Confirm", boxW, body)
	case wsOverlaySessions, wsOverlayProjects, wsOverlayProfiles, wsOverlayText, wsOverlayInfo:
		return m.listBox()
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// fitHints keeps the first hint and the last (help) and drops the ones in
// between, from the back, until the bar fits width.
func fitHints(width int, hints []keyHint) []keyHint {
	size := func(hs []keyHint) int {
		total := 1 + 2*max(len(hs)-1, 0)
		for _, h := range hs {
			total += ansi.StringWidth(keycap(h.key)) + 1 + ansi.StringWidth(h.label)
		}
		return total
	}
	for len(hints) > 2 && size(hints) > width {
		hints = append(hints[:len(hints)-2], hints[len(hints)-1])
	}
	return hints
}

func centerLine(s string, w int) string {
	pad := (w - ansi.StringWidth(s)) / 2
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

func (m workspaceModel) statusLine() string {
	w := m.width
	text, isErr := m.notice, m.noticeErr
	if text == "" {
		text, isErr = m.guidance()
	}
	if text == "" {
		return ""
	}
	st := lipgloss.NewStyle().Foreground(crt.dim)
	if isErr {
		st = lipgloss.NewStyle().Foreground(crt.warn)
		if !strings.HasPrefix(text, "!") {
			text = "! " + text
		}
	}
	return " " + st.Render(truncateOneLine(text, w-2))
}

// guidance says what is going on and which key does the next sensible thing.
func (m workspaceModel) guidance() (string, bool) {
	g := m.g
	if g == nil || len(g.Nodes) == 0 {
		return "", false
	}
	via := ""
	if m.focus == wsFocusPrompt {
		via = "Tab to the graph, then "
	}
	_, running, done, failed := g.Counts()
	if failed > 0 {
		for _, n := range g.Nodes {
			if n.Status != workspace.StatusFailed {
				continue
			}
			if n.Kind == workspace.KindCheck {
				return fmt.Sprintf("! %s failed. %sselect it: f adds a fix node, r retries", n.Title, via), true
			}
			return fmt.Sprintf("! %s failed: %s. %sselect it: r retries, a changes the agent", n.ID, truncateOneLine(oneLine(n.Error), 50), via), true
		}
	}
	if running > 0 {
		return fmt.Sprintf("%d running. %so shows output, s stops the selected node", running, via), false
	}
	unassigned := 0
	for _, n := range g.Nodes {
		if n.Kind == workspace.KindWork && n.Role == "" && n.Status == workspace.StatusTodo {
			unassigned++
		}
	}
	if unassigned > 0 {
		return fmt.Sprintf("%d nodes need an agent. %sA accepts the suggestions, a picks one", unassigned, via), false
	}
	if len(g.Runnable()) > 0 {
		return fmt.Sprintf("Ready. %sr runs the selected node, R runs every assigned node", via), false
	}
	if done == len(g.Nodes) {
		return "All nodes are done. Type a follow-up task, or /compact to fold them into the summary", false
	}
	return "Nothing can run: nodes are waiting on unfinished dependencies", false
}

// transcriptLines wraps the session chat into styled lines of at most w cells,
// followed by the spinner while the lead is splitting.
func (m workspaceModel) transcriptLines(w int, withStream bool) []string {
	if w < 8 {
		return nil
	}
	var out []string
	for _, l := range m.sess.snapshot().Transcript {
		var prefix string
		var body lipgloss.Style
		switch l.Who {
		case "you":
			prefix = lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render("you ›") + " "
			body = lipgloss.NewStyle().Foreground(crt.primary)
		case "lead":
			prefix = lipgloss.NewStyle().Foreground(roleColor("pm")).Bold(true).Render("lead ›") + " "
			body = lipgloss.NewStyle().Foreground(crt.primary)
		case "system":
			prefix = lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("· ")
			body = lipgloss.NewStyle().Foreground(crt.dim)
		default:
			prefix = lipgloss.NewStyle().Foreground(crt.warn).Bold(true).Render("! ")
			body = lipgloss.NewStyle().Foreground(crt.warn)
		}
		pw := ansi.StringWidth(prefix)
		first := true
		for _, para := range strings.Split(workspace.SanitizeText(l.Text), "\n") {
			wrapped := wrapText(para, w-pw)
			if len(wrapped) == 0 {
				wrapped = []string{""}
			}
			for _, wl := range wrapped {
				lead := strings.Repeat(" ", pw)
				if first {
					lead = prefix
					first = false
				}
				out = append(out, " "+lead+body.Render(wl))
			}
		}
	}
	if m.splitting {
		spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
		out = append(out, " "+lipgloss.NewStyle().Foreground(roleColor("pm")).Bold(true).Render("lead ›")+" "+
			lipgloss.NewStyle().Foreground(crt.primary).Render(spin+" splitting the task…  (Esc cancels)"))
		if withStream && m.stream != nil {
			if last := m.stream.lastLine(); last != "" {
				out = append(out, "   "+lipgloss.NewStyle().Foreground(crt.dim).Render(truncateOneLine(last, w-4)))
			}
		}
	}
	return out
}

// chatStrip is the last rows lines of the chat, newest at the bottom.
func (m workspaceModel) chatStrip(rows int) []string {
	lines := m.transcriptLines(m.width-2, rows >= 2)
	end := len(lines) - min(m.scroll, max(len(lines)-1, 0))
	start := max(end-rows, 0)
	out := append([]string(nil), lines[start:end]...)
	if m.splitting && rows == 1 && len(out) > 0 { // keep the spinner, not the stream
		out = out[len(out)-1:]
	}
	for len(out) < rows {
		out = append([]string{""}, out...)
	}
	return out
}

// graphHints returns the context-sensitive footer hints for graph focus.
func (m workspaceModel) graphHints() []keyHint {
	var hs []keyHint
	if n := m.selNode(); n != nil && m.g != nil {
		switch {
		case n.Status == workspace.StatusRunning:
			hs = append(hs, keyHint{"s", "stop"}, keyHint{"o", "output"})
		case n.Status == workspace.StatusFailed && n.Kind == workspace.KindCheck:
			hs = append(hs, keyHint{"f", "add fix"}, keyHint{"r", "retry"})
		case n.Status == workspace.StatusFailed:
			hs = append(hs, keyHint{"r", "retry"}, keyHint{"a", "reassign"})
		case n.Status == workspace.StatusDone:
			hs = append(hs, keyHint{"o", "output"})
		case !m.g.Assigned(n):
			hs = append(hs, keyHint{"a", "assign"}, keyHint{"A", "accept all"})
		case len(m.g.Blockers(n.ID)) == 0:
			hs = append(hs, keyHint{"r", "run"}, keyHint{"R", "run all"})
		default:
			hs = append(hs, keyHint{"R", "run all"}, keyHint{"a", "reassign"})
		}
	}
	return append(hs, keyHint{"Tab", "prompt"}, keyHint{"←↑↓→", "move"}, keyHint{"?", "help"})
}

func (m workspaceModel) hintLine() string {
	w := m.width
	var hs []keyHint
	switch {
	case m.splitting:
		hs = []keyHint{{"Esc", "cancel"}, {"Ctrl+C", "quit"}}
	case m.focus == wsFocusGraph:
		hs = m.graphHints()
	case m.paletteOpen():
		hs = []keyHint{{"↑↓", "choose"}, {"Tab", "complete"}, {"Enter", "run"}, {"Esc", "close"}}
	default:
		hs = []keyHint{{"Enter", "send"}, {"/", "commands"}, {"Tab", "graph"}}
		if m.input.Value() == "" {
			hs = append(hs, keyHint{"?", "help"})
		}
		hs = append(hs, keyHint{"Ctrl+C", "quit"})
	}
	return hintBar(w, fitHints(w, hs), "")
}

func (m workspaceModel) summaryLine() string {
	w := m.width
	g := m.g
	title := workspace.Sanitize(m.sess.snapshot().Title)
	if title == "" {
		title = "new session"
	}
	s := m.sess.snapshot()
	tokens := s.In + s.Out
	warns := 0
	for _, n := range g.Nodes {
		tokens += n.InputTokens + n.OutputTokens
		warns += len(n.Warnings)
	}
	var stats string
	if len(g.Nodes) == 0 {
		stats = "no nodes"
	} else {
		_, running, done, failed := g.Counts()
		stats = fmt.Sprintf("%d/%d done", done, len(g.Nodes))
		if running > 0 {
			stats += fmt.Sprintf(" · %d running", running)
		}
		if failed > 0 {
			stats += fmt.Sprintf(" · %d failed", failed)
		}
		switch {
		case warns == 1:
			stats += " · 1 warning"
		case warns > 1:
			stats += fmt.Sprintf(" · %d warnings", warns)
		}
	}
	stats += " · " + formatTokensCompact(tokens) + " tok"
	right := lipgloss.NewStyle().Foreground(crt.dim).Render(stats)
	fits := func(r string) bool { return ansi.StringWidth(r)+1+12 <= w }
	if m.eng != nil && m.eng.Auto() {
		if r := chip("AUTO", galaxyGold) + " " + right; fits(r) {
			right = r
		}
	}
	if p := m.cfg.Project.Profile; p != "" {
		if r := chip("profile: "+p, galaxyAzure) + " " + right; fits(r) {
			right = r
		}
	}
	right += " "
	left := " " + lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render(truncateOneLine(title, max(w-ansi.StringWidth(right)-3, 4)))
	gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// emptyPane is the body of the GRAPH pane before the first task.
func (m workspaceModel) emptyPane(iw, ih int) []string {
	dim := lipgloss.NewStyle().Foreground(crt.dim)
	var body []string
	for _, l := range wrapText("No nodes yet. Describe a task below — the lead splits it into nodes and you assign the agents.", max(iw-4, 8)) {
		body = append(body, centerLine(dim.Render(l), iw))
	}
	if m.nSaved > 0 {
		plural := "s"
		if m.nSaved == 1 {
			plural = ""
		}
		line := fmt.Sprintf("↺ %d saved session%s — /resume", m.nSaved, plural)
		body = append(body, "", centerLine(lipgloss.NewStyle().Foreground(galaxyGold).Render(truncateOneLine(line, iw-2)), iw))
	}
	var logo []string
	for _, l := range strings.Split(HomeModel{}.renderLogo(iw), "\n") {
		logo = append(logo, centerLine(l, iw))
	}
	lines := body
	if len(logo)+1+len(body) <= ih {
		lines = append(append(append([]string(nil), logo...), ""), body...)
	}
	top := max((ih-len(lines))/2, 0)
	return append(make([]string, top), lines...)
}

// mainPanes draws the GRAPH pane and the inspector in exactly mainH lines.
func (m workspaceModel) mainPanes(mainH int) []string {
	w := m.width
	g := m.g
	models := map[string]string{}
	for _, r := range workspace.Roles(m.cfg) {
		models[r.Name] = shortModel(r.Agent.Runner, r.Agent.Model)
	}
	spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]

	wide := w >= wsInspectorMin
	showInsp := wide && !m.inspFlip
	n := m.selNode()
	tail := m.outputTail(m.sel, mainH)
	inspTitle := "INSPECTOR"
	if n != nil {
		inspTitle = n.ID
	}
	canvasFrame := func(cw int) string {
		var canvas []string
		if len(g.Nodes) == 0 {
			canvas = m.emptyPane(cw-2, mainH-2)
		} else {
			canvas = strings.Split(renderCanvas(g, canvasOpts{width: cw - 2, height: mainH - 2, selected: m.sel, models: models, spinner: spin}), "\n")
		}
		return wsFrame("GRAPH", galaxyAzure, m.focus == wsFocusGraph, cw, mainH, 0, canvas)
	}
	inspFrame := func(iw, ih int) string {
		insp := strings.Split(renderInspector(g, n, m.cfg, iw-4, ih-2, tail), "\n")
		return wsFrame(inspTitle, galaxyAzure, false, iw, ih, 1, insp)
	}
	var main []string
	switch {
	case showInsp:
		cw := w - wsInspectorW
		cl := strings.Split(canvasFrame(cw), "\n")
		il := strings.Split(inspFrame(wsInspectorW, mainH), "\n")
		for i := 0; i < mainH; i++ {
			c, p := "", ""
			if i < len(cl) {
				c = cl[i]
			}
			if i < len(il) {
				p = il[i]
			}
			main = append(main, padRight(c, cw)+padRight(p, wsInspectorW))
		}
	case !wide && m.inspFlip:
		// Narrow terminal: the inspector is a box over the graph.
		bh := max(mainH-2, min(mainH, 5))
		box := inspFrame(min(w, 60), bh)
		main = strings.Split(overlayCenter(canvasFrame(w), box, w, mainH), "\n")
	default:
		main = strings.Split(canvasFrame(w), "\n")
	}
	for len(main) < mainH {
		main = append(main, "")
	}
	return main[:mainH]
}

func (m workspaceModel) promptBox() []string {
	boxStyle := lipgloss.NewStyle().Border(pixelBorder).BorderForeground(crt.border)
	if m.focus == wsFocusPrompt && !m.splitting {
		boxStyle = boxStyle.BorderForeground(focusBorder)
	}
	return strings.Split(boxStyle.Render(m.input.View()), "\n")
}

// paletteLines is the "/" command list drawn right above the prompt box, at
// most maxRows lines.
func (m workspaceModel) paletteLines(maxRows int) []string {
	if !m.paletteOpen() || maxRows <= 0 {
		return nil
	}
	matches := m.cmdMatches(m.input.Value())
	var pal []string
	if len(matches) == 0 {
		pal = []string{lipgloss.NewStyle().Foreground(crt.warn).Render("! no such command")}
	} else {
		pal = renderCmdList(matches, m.cmdIdx, m.width-2, maxRows)
	}
	for i := range pal {
		pal[i] = " " + pal[i]
	}
	return pal
}

func (m workspaceModel) mainView() string {
	w, h := m.width, m.height
	box := m.promptBox()
	chatRows := m.chatRows()
	fixed := 1 + 1 + 1 + chatRows + len(box) + 1 // header, summary, status, chat, prompt, hints
	pal := m.paletteLines(h - fixed - 6)
	mainH := h - fixed - len(pal)

	parts := []string{screenHeader(w, "Workspace", m.project()), m.summaryLine()}
	parts = append(parts, m.mainPanes(mainH)...)
	parts = append(parts, m.statusLine())
	parts = append(parts, m.chatStrip(chatRows)...)
	parts = append(parts, pal...) // directly above the prompt box
	parts = append(parts, box...)
	parts = append(parts, m.hintLine())
	return strings.Join(parts, "\n")
}

func (m workspaceModel) outputView() string {
	w, h := m.width, m.height
	n := m.g.Node(m.outNode)
	title := m.outNode
	if n != nil {
		_, label := nodeStatusGlyph(m.g, n)
		title = fmt.Sprintf("%s · %s · %s", n.ID, n.Title, label)
	}
	vp := m.outVP
	vp.Width, vp.Height = w, h-3
	hints := []keyHint{{"↑↓", "scroll"}, {"s", "stop"}, {"Esc", "back"}, {"?", "help"}}
	parts := []string{
		screenHeader(w, "Workspace", m.project()),
		" " + lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render(truncateOneLine(title, w-2)),
		vp.View(),
		hintBar(w, fitHints(w, hints), ""),
	}
	return strings.Join(parts, "\n")
}
