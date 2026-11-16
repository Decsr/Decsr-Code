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
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	wsMinW         = 60
	wsMinH         = 16
	wsInspectorW   = 38
	wsInspectorMin = 100 // the inspector is shown beside the graph from this main-pane width
	wsMaxOutput    = 2000
)

const (
	wsInputHint     = "Type a task, / for commands"
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
	Resume bool   // open with the session picker showing
	Issue  string // /issue arguments to run after start ("42", "<URL> --force")
}

// wsDeps are the seams between the model and the outside world. The optional
// ones fall back to the real implementations when nil.
type wsDeps struct {
	// split asks the lead to split task; earlier is the session summary and
	// the current nodes ("" for a fresh session). existing are the node IDs
	// already in the graph and next the first number for new sub-tasks.
	split func(ctx context.Context, task, earlier string, existing []string, next int, onToken func(string)) (*workspace.Graph, runner.TokenUsage, error)
	// newEngine builds the engine; summary gives the workers the session summary.
	newEngine func(g *workspace.Graph, save func(*workspace.Graph) error, summary func() string, iso *workspace.Isolator) *workspace.Engine
	// isolator returns the worktree isolator of a session, nil when its nodes
	// share the checkout. Nil means no isolation.
	isolator  func(session string) *workspace.Isolator
	saveRole  func(name string, ac config.AgentConfig) (config.Config, error) // persists and returns the reloaded config
	checkCmds func() []string

	listProfiles   func() []string
	saveProfile    func(name string, agents map[string]config.AgentConfig) error
	applyProfile   func(name string) (config.Config, error) // applies and returns the reloaded config
	deleteProfile  func(name string) error
	recent         func() []RecentProject
	customCommands func() []workspace.CustomCommand
	loadPlugins    func() plugins.Set
	// installPlugin installs a catalog plugin (plugins.Install by default).
	installPlugin func(ctx context.Context, e plugins.CatalogEntry) error
	// ghRun runs gh and git for /pr; nil runs the real programs.
	ghRun workspace.CmdRunner
	// notify sends a "needs you" note; nil sends nothing.
	notify wsNotifyFunc
	// cfg returns the live config for pricing and the budget; nil uses the
	// config the engine was attached with.
	cfg func() config.Config
	// probeKey tests an API key with GET <base>/models (/connect); nil calls
	// the real endpoint.
	probeKey connectProbeFn
	// useProvider writes runner/model for the lead or all roles into the
	// global config and returns the reloaded config.
	useProvider connectApplyFn
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
	if d.loadPlugins == nil {
		d.loadPlugins = func() plugins.Set { return plugins.Load(root) }
	}
	if d.installPlugin == nil {
		d.installPlugin = plugins.Install
	}
	if d.useProvider == nil {
		d.useProvider = func(runnerName, provider, model string, all bool) (config.Config, error) {
			if err := config.UseProviderGlobal(runnerName, provider, model, all); err != nil {
				return config.Config{}, err
			}
			return config.Load(root)
		}
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
	iso     *workspace.Isolator
}

func (e wsLiveExec) Run(ctx context.Context, g *workspace.Graph, n *workspace.Node, emit func(workspace.Event)) (workspace.Result, error) {
	c := e.cfg()
	agents := make(map[string]config.AgentConfig, len(c.Agents))
	for k, v := range c.Agents {
		agents[k] = v
	}
	c.Agents = agents
	runner.ApplyModelMemory(&c)
	return workspace.NewExecutorIsolated(e.root, c, e.pctx(), e.checks, e.summary, e.iso).Run(ctx, g, n, emit)
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
	var pluginAgents map[string]config.PluginAgent // set by deps.loadPlugins
	getCfg := func() config.Config {
		mu.Lock()
		defer mu.Unlock()
		c := cur
		c.PluginAgents = pluginAgents
		return c
	}
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
		notify: wsSendNote,
		cfg:    getCfg,
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
			opts := workspace.SplitOptions{Plugins: plugins.Load(root), Catalog: plugins.Catalog()}
			g, u, err := workspace.SplitOpts(ctx, r, pm.Model, task, workspace.Roles(c), pc, existing, next, opts, func(s string) {
				raw.WriteString(s)
				if onToken != nil {
					onToken(s)
				}
			})
			writeSplitRaw(root, raw.String())
			return g, u, err
		},
		isolator: func(session string) *workspace.Isolator {
			return workspace.NewIsolator(root, session, getCfg().Workspace.Isolation)
		},
		newEngine: func(g *workspace.Graph, save func(*workspace.Graph) error, summary func() string, iso *workspace.Isolator) *workspace.Engine {
			e := workspace.NewEngineWithSave(g, wsLiveExec{root: root, cfg: getCfg, pctx: projectContext, checks: checkCmds, summary: summary, iso: iso},
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
		useProvider: func(runnerName, provider, model string, all bool) (config.Config, error) {
			if err := config.UseProviderGlobal(runnerName, provider, model, all); err != nil {
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
		loadPlugins: func() plugins.Set {
			set := plugins.Load(root)
			mu.Lock()
			pluginAgents = set.AgentRoles()
			mu.Unlock()
			return set
		},
	}

	defer wsNotifyWait()
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
	wsScreenDiff
)

type wsFocus int

const (
	wsFocusPrompt wsFocus = iota
	wsFocusGraph          // the main pane: the activity feed or the graph
	wsFocusSidebar
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
	wsOverlaySkills
	wsOverlayConnect
)

// wsConfirm is a pending y/n question. kind: quit | menu | clear | delsession
// | delprofile | plugin. back is the list overlay to return to on "no". A
// plugin question (arg is the plugin name) is answered y, n or a; verb is
// "install" or "enable".
type wsConfirm struct {
	kind, text, arg, verb string
	back                  wsOverlay
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
	conn          *connectModel // the /connect overlay
	confirm       wsConfirm
	openMenu      bool
	switchTo      string
	quitting      bool
	quitAt        time.Time
	notice        string
	noticeErr     bool

	// session and prompt
	sess         *wsSess
	nSaved       int                    // saved sessions of this project, for the empty state
	sugDismissed string                 // suggestionKey of the suggestion hidden by Esc or accepting
	spec         *workspace.SpecFeature // newest specs/*/tasks.md with an open task; nil = none
	custom       []workspace.CustomCommand
	plug         plugins.Set
	input        textarea.Model
	scroll       int // chat lines scrolled up from the newest
	cmdIdx       int
	cmdPicked    bool // the highlight was moved with the arrows
	splitting    bool
	startIssue   string // /issue arguments to run at start
	splitGen     int
	splitTask    string
	cancelSplit  context.CancelFunc
	stream       *wsStream
	iso          *workspace.Isolator // worktree isolation of this session; nil = shared checkout
	isoAnnounced bool                // the isolation line was shown
	pr           wsPRState           // pull request loop (/pr)
	autoPaused   bool                // S paused the automation of /autonomy
	autoWarned   bool                // the lowered level 3 was announced

	// plugin install started from the dialog
	installing    string // plugin being installed; "" for none
	installGen    int
	cancelInstall context.CancelFunc

	// layout: sidebar and activity feed
	showGraph    bool                    // the main pane shows the graph; false = the activity feed
	sbFlip       bool                    // hides the sidebar when wide, shows it as a box when narrow
	sbSess       int                     // sidebar cursor on a saved session; -1 = on a node
	sbSessions   []workspace.SessionInfo // saved sessions of the project, for the sidebar
	feedFollow   bool                    // the feed shows its newest rows
	feedTop      int                     // first visible feed row while not following
	feedExpanded map[string]bool         // cards that show their output
	feedCache    *feedCache

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

	// diff screen (d, D)
	diff *wsDiffView

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

	// skill picker (K)
	skillNode  string
	skillIdx   int
	skillItems []wsSkillItem
	skillSel   map[string]bool
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
		st.Text = lipgloss.NewStyle().Foreground(colText)
		st.Placeholder = lipgloss.NewStyle().Foreground(colMuted)
	}
	ta.Cursor.SetMode(cursor.CursorStatic)
	ta.Focus()

	newField := func(placeholder string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholder
		ti.CharLimit = 200
		ti.Cursor.SetMode(cursor.CursorStatic)
		ti.PromptStyle = lipgloss.NewStyle().Foreground(colMuted)
		ti.TextStyle = lipgloss.NewStyle().Foreground(colText)
		ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(colMuted)
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

		sbSess:       -1,
		feedFollow:   true,
		feedExpanded: map[string]bool{},
		feedCache:    &feedCache{},
	}
	m.resize()
	if _, err := workspace.MigrateLegacy(root); err != nil {
		m.setNotice("old workspace graph: "+oneLine(err.Error()), true)
	}
	m.custom = deps.customCommands()
	m.plug = deps.loadPlugins()
	m.cfg = m.withPluginAgents(m.cfg)
	m.refreshSaved()
	m.rescanSpec()
	if len(opts) > 0 {
		m.startIssue = opts[0].Issue
	}
	if len(opts) > 0 && opts[0].Resume {
		m = m.openSessions()
	}
	return m
}

func (m workspaceModel) Init() tea.Cmd { return m.startIssueCmd() }

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
	m.input.SetWidth(max(m.width-6, 10))
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
	if f != wsFocusSidebar {
		m.sbSess = -1
	}
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

// rescanSpec looks for a Spec Kit tasks.md with open tasks once; it is not
// part of rendering.
func (m *workspaceModel) rescanSpec() {
	m.spec = nil
	if f, ok := workspace.DetectSpec(m.root); ok {
		m.spec = &f
	}
}

func (m *workspaceModel) refreshSaved() {
	m.nSaved = workspace.CountSessions(m.root)
	m.sbSessions = workspace.ListSessions(m.root)
}

// say adds a chat line to the session. who: lead | system | error.
func (m *workspaceModel) say(who, text string) {
	m.scroll = 0
	if who == "you" {
		m.feedFollow = true
	}
	m.sess.addLine(who, text)
}

// ── Update ───────────────────────────────────────────────────────────────────

func (m workspaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		was := m.inspDefault()
		sbWas := m.width >= wsSidebarMin
		m.width, m.height = msg.Width, msg.Height
		if was != m.inspDefault() {
			m.inspFlip = false // the flip means "hide" when shown by default and "open" otherwise
		}
		if sbWas != (m.width >= wsSidebarMin) {
			m.sbFlip = false // the flip means "hide" when wide and "open" when narrow
		}
		if m.focus == wsFocusSidebar && !m.sidebarVisible() {
			m.setFocus(wsFocusGraph)
		}
		m.resize()
		if m.screen == wsScreenOutput {
			m.refreshOutput()
		}
		return m, nil
	case wsSplitDoneMsg:
		return m.onSplitDone(msg)
	case wsEventMsg:
		return m.onEvent(msg)
	case wsInstallMsg:
		return m.onInstalled(msg)
	case connectProbeMsg, connectTickMsg, connectSavedMsg, connectClosedMsg:
		return m.onConnectMsg(msg)
	case wsPRMsg:
		return m.onPRMsg(msg)
	case wsIssueMsg:
		return m.onIssueMsg(msg)
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
		if m.splitting || m.running() > 0 || m.installing != "" {
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
	if m.screen == wsScreenDiff {
		return m.diffKey(msg)
	}
	if m.splitting {
		if k == "esc" {
			return m.cancelSplitting(), nil
		}
		return m, nil
	}
	if m.focus != wsFocusPrompt {
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
	if sug := m.suggestion(); sug != "" {
		switch k {
		case "right":
			m.dismissSuggestion(sug)
			m.input.SetValue(sug)
			return m, nil
		case "esc":
			m.dismissSuggestion(sug)
			return m, nil
		}
	}
	switch k {
	case "pgup", "pgdown":
		if !m.showGraph {
			_, h := m.feedPaneSize()
			page := max(h-2, 1)
			if k == "pgup" {
				page = -page
			}
			return m.feedScrollBy(page), nil
		}
		if k == "pgup" {
			m.scroll += 3
		} else {
			m.scroll = max(m.scroll-3, 0)
		}
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
		return m.cycleFocus(1), nil
	case "shift+tab":
		return m.cycleFocus(-1), nil
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
	var next tea.Model
	var cmd tea.Cmd
	if m.eng == nil || !m.hasNodes() {
		next, cmd = m.openGraph(msg.g)
	} else {
		next, cmd = m.appendGraph(msg.g)
	}
	m, ok := next.(workspaceModel)
	if !ok {
		return next, cmd
	}
	for _, w := range msg.g.SplitWarnings {
		m.say("system", workspace.Sanitize(w))
	}
	if m.autoActive() == autoManual {
		m.notifySplitDone()
	}
	m = m.askPlugins()
	m, autoCmd := m.autoAfterGraph()
	return m, tea.Batch(cmd, autoCmd)
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
	m.iso, m.isoAnnounced = nil, false
	if m.deps.isolator != nil {
		m.iso = m.deps.isolator(sess.snapshot().ID)
	}
	m.eng = m.deps.newEngine(g, sess.saveGraph, func() string { return sess.snapshot().Summary }, m.iso)
	m.wireBudget(sess)
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
	m.sbSess = -1
	m.feedFollow = true
	return m, tea.Batch(wsWaitEvent(m.eng), m.startTick())
}

// resetSession drops the engine and the graph and starts an empty session.
func (m workspaceModel) resetSession(s *workspace.Session) workspaceModel {
	closeEngine(m.eng)
	m.eng = nil
	m.g = &workspace.Graph{}
	m.sel = ""
	m.sess = newWsSess(m.root, s)
	m.iso, m.isoAnnounced = nil, false
	m.autoPaused, m.autoWarned = false, false
	m.prStop()
	m.outputs = map[string][]string{}
	m.partial = map[string]string{}
	m.outSize = map[string]int{}
	m.screen = wsScreenMain
	m.overlay = wsOverlayNone
	m.scroll = 0
	m.sbSess, m.sbFlip = -1, false
	m.feedFollow, m.feedExpanded = true, map[string]bool{}
	m.input.Reset()
	m.input.Placeholder = wsInputHint
	m.setFocus(wsFocusPrompt)
	m.rescanSpec()
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
		if m.iso != nil && !m.isoAnnounced {
			m.isoAnnounced = true
			m.say("system", fmt.Sprintf("Nodes run in isolated worktrees on branch %s. Use /merge to bring the result into your checkout.", m.iso.Branch()))
		}
	case workspace.EventOutput:
		if ev.NodeID == "" {
			m.setNotice(oneLine(ev.Text), true)
		} else {
			m.appendOutput(ev.NodeID, ev.Text)
		}
	case workspace.EventDone:
		m.setNotice("✓ "+ev.NodeID+" done", false)
		if n := m.g.Node(ev.NodeID); m.iso != nil && n != nil && n.Kind == workspace.KindCheck {
			m.say("system", "Build & test passed. Use /merge to bring the result into your checkout.")
		}
	case workspace.EventBudgetWarn, workspace.EventBudgetReached:
		m.onBudgetEvent(ev)
	case workspace.EventFailed:
		if ev.Text == "stopped" {
			m.setNotice(ev.NodeID+" stopped", false)
		} else {
			m.setNotice(ev.NodeID+" failed: "+oneLine(ev.Text), true)
		}
	}
	m.g = m.eng.Snapshot()
	m.notifyEvent(ev)
	if m.screen == wsScreenOutput {
		m.refreshOutput()
	}
	m, autoCmd := m.autoAfterEvent(ev)
	return m, tea.Batch(wsWaitEvent(m.eng), m.startTick(), m.prAfterEvent(ev), autoCmd)
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
		content = lipgloss.NewStyle().Foreground(colMuted).Render(msg)
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
	case "tab":
		return m.cycleFocus(1), nil
	case "shift+tab":
		return m.cycleFocus(-1), nil
	case "c":
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
		m.inspFlip = m.inspFlip && m.inspDefault() // closes the inspector box
		if m.sidebarOverlay() {
			m.sbFlip = false
			if m.focus == wsFocusSidebar {
				m.setFocus(wsFocusGraph)
			}
		}
		return m, nil
	case "g":
		m.showGraph = !m.showGraph
		m.inspFlip = false
		return m, nil
	case "b":
		m.sbFlip = !m.sbFlip
		switch {
		case m.sidebarVisible():
			m.setFocus(wsFocusSidebar)
		case m.focus == wsFocusSidebar:
			m.setFocus(wsFocusGraph)
		}
		return m, nil
	case "G":
		if !m.showGraph {
			m.feedFollow = true
		}
		return m, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		if !m.showGraph && m.focus == wsFocusGraph {
			_, h := m.feedPaneSize()
			page := max(h-2, 1)
			if k == "pgup" || k == "ctrl+u" {
				page = -page
			}
			return m.feedScrollBy(page), nil
		}
		return m, nil
	}
	dirs := map[string]string{"up": "up", "k": "up", "down": "down", "j": "down", "left": "left", "h": "left", "right": "right", "l": "right"}
	if m.focus == wsFocusSidebar {
		switch d := dirs[k]; {
		case d == "up":
			return m.sbMove(-1), nil
		case d == "down":
			return m.sbMove(1), nil
		case k == "enter" && m.sbSess >= 0:
			return m.sbOpenSession()
		}
	}
	if m.eng == nil || !m.hasNodes() {
		return m, nil
	}
	if d, ok := dirs[k]; ok {
		switch {
		case m.focus == wsFocusSidebar:
		case !m.showGraph:
			switch d {
			case "up":
				return m.feedSelect(-1), nil
			case "down":
				return m.feedSelect(1), nil
			}
		default:
			m.sel = canvasNeighbor(m.g, m.sel, d)
		}
		return m, nil
	}
	n := m.selNode()
	switch k {
	case " ", "space":
		if !m.showGraph && n != nil {
			m.feedExpanded[n.ID] = !m.feedExpanded[n.ID]
			m = m.feedReveal(n.ID)
		}
	case "a":
		return m.openPicker(n)
	case "A":
		return m.acceptAll()
	case "K":
		return m.openSkillPicker(n)
	case "r":
		if n == nil {
			return m, nil
		}
		m.autoPaused = false
		if m.installing != "" {
			m.setNotice("Wait for the plugin install to finish, then run the node", true)
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
		m.autoPaused = false
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
		m.autoPause()
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
	case "d":
		if m.focus == wsFocusSidebar && m.sbSess >= 0 {
			return m, nil
		}
		switch {
		case n == nil:
		case n.Kind != workspace.KindWork:
			m.setNotice("Build & test has no diff", false)
		default:
			m = m.openDiff(n)
		}
	case "D":
		m = m.openDiff(nil)
	case "o", "enter":
		if n != nil {
			if m.sidebarOverlay() {
				m.sbFlip = false
			}
			m.screen = wsScreenOutput
			m.outNode = n.ID
			m.outFollow = true
			m.refreshOutput()
		}
	}
	return m, nil
}

// cycleFocus moves the focus along prompt, sidebar (when shown) and main.
func (m workspaceModel) cycleFocus(dir int) workspaceModel {
	order := []wsFocus{wsFocusPrompt}
	if m.sidebarVisible() {
		order = append(order, wsFocusSidebar)
	}
	order = append(order, wsFocusGraph)
	i := 0
	for j, f := range order {
		if f == m.focus {
			i = j
		}
	}
	next := order[(i+dir+len(order))%len(order)]
	if m.focus == wsFocusSidebar && m.sidebarOverlay() {
		m.sbFlip = false // leaving the box closes it
	}
	m.setFocus(next)
	return m
}

// sbOpenSession loads the saved session under the sidebar cursor, as /resume
// does.
func (m workspaceModel) sbOpenSession() (tea.Model, tea.Cmd) {
	sessions := m.otherSessions()
	if m.sbSess < 0 || m.sbSess >= len(sessions) {
		return m, nil
	}
	if m.busy() {
		return m.refuseBusy()
	}
	id := sessions[m.sbSess].ID
	m.sbSess, m.sbFlip = -1, false
	return m.loadSession(id)
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
	if m.installing != "" {
		m.setNotice("Wait for the plugin install to finish, then run the nodes", true)
		return m, nil
	}
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

	case wsOverlaySkills:
		return m.skillKey(k)

	case wsOverlaySessions, wsOverlayProjects, wsOverlayProfiles:
		return m.listKey(msg)

	case wsOverlayText:
		return m.textKey(msg)

	case wsOverlayConnect:
		return m.connectKey(msg)

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
	if c.kind == "plugin" {
		return m.pluginAnswer(k)
	}
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
	m.cfg = m.withPluginAgents(cfg)
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
	case m.screen == wsScreenDiff:
		v = m.diffView()
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
		warn := lipgloss.NewStyle().Foreground(colWarn).Bold(true)
		var body []string
		for _, l := range wrapText(m.confirm.text, boxW-4) {
			body = append(body, warn.Render(l))
		}
		switch {
		case m.confirm.kind == "plugin":
			body = append(body, "", wsKey("y")+" "+m.confirm.verb+"   "+wsKey("n")+" skip   "+wsKey("a")+" skip all")
		case !strings.HasSuffix(m.confirm.text, "y/n"): // otherwise the text already asks
			body = append(body, "", wsKey("y")+" yes   "+wsKey("n")+" no")
		}
		return wsBox("Confirm", boxW, body)
	case wsOverlaySkills:
		n := m.g.Node(m.skillNode)
		if n == nil {
			return ""
		}
		return renderSkillPicker("Skills for "+n.ID+" · "+n.Title, m.skillItems, m.skillSel, m.skillIdx, w, h)
	case wsOverlayConnect:
		if m.conn == nil {
			return ""
		}
		return m.conn.box(w)
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

// wsKey renders a key name in normal weight, to stand out from dim labels.
func wsKey(k string) string { return lipgloss.NewStyle().Foreground(colText).Render(k) }

// wsHintBar renders footer hints as "key label" pairs, the key in normal
// weight and the label dim, separated by a dim " · ".
func wsHintBar(width int, hints []keyHint) string {
	hints = detachHints(hints)
	dim := lipgloss.NewStyle().Foreground(colMuted)
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = wsKey(h.key) + dim.Render(" "+h.label)
	}
	return ansi.Truncate("  "+strings.Join(parts, dim.Render(" · ")), width, "")
}

// fitHints keeps the first hint and the last (help) and drops the ones in
// between, from the back, until the bar fits width.
func fitHints(width int, hints []keyHint) []keyHint {
	size := func(hs []keyHint) int {
		total := 2 + 3*max(len(hs)-1, 0)
		for _, h := range hs {
			total += ansi.StringWidth(h.key) + 1 + ansi.StringWidth(h.label)
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
	long := ""
	if text == "" && m.installing != "" {
		text = m.installLine()
	}
	if text == "" {
		text, long, isErr = m.guidanceParts()
	}
	if text == "" {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(colMuted)
	if !isErr {
		return "  " + dim.Render(truncateOneLine(text+orSep(long), w-4))
	}
	// An error is one line: a red glyph, the short text, then the dim advice.
	text = strings.TrimPrefix(text, "! ")
	glyph := lipgloss.NewStyle().Foreground(colFail).Bold(true).Render("!") + " "
	short := truncateOneLine(text, max(w-6, 4))
	line := glyph + lipgloss.NewStyle().Foreground(colText).Render(short)
	if room := w - 4 - 2 - ansi.StringWidth(short) - 2; long != "" && room >= 8 {
		line += dim.Render("  " + truncateOneLine(long, room))
	}
	return "  " + line
}

// orSep prefixes a non-empty tail with ". " for joining.
func orSep(s string) string {
	if s == "" {
		return ""
	}
	return ". " + s
}

// guidance says what is going on and which key does the next sensible thing.
func (m workspaceModel) guidance() (string, bool) {
	short, long, isErr := m.guidanceParts()
	if short == "" {
		return "", false
	}
	return short + orSep(long), isErr
}

// guidanceParts is guidance split into the short statement and the advice
// that follows it ("" when there is none).
func (m workspaceModel) guidanceParts() (short, long string, isErr bool) {
	g := m.g
	if g == nil || len(g.Nodes) == 0 {
		return "", "", false
	}
	via := ""
	if m.focus == wsFocusPrompt {
		via = "Tab to the nodes, then "
	}
	_, running, done, failed := g.Counts()
	if failed > 0 {
		for _, n := range g.Nodes {
			if n.Status != workspace.StatusFailed {
				continue
			}
			if n.Kind == workspace.KindCheck {
				return fmt.Sprintf("! %s failed", n.Title), fmt.Sprintf("%sselect it: f adds a fix node, r retries", via), true
			}
			return fmt.Sprintf("! %s failed: %s", n.ID, truncateOneLine(oneLine(n.Error), 50)), fmt.Sprintf("%sselect it: r retries, a changes the agent", via), true
		}
	}
	if running > 0 {
		return fmt.Sprintf("%d running", running), fmt.Sprintf("%so shows output, s stops the selected node", via), false
	}
	unassigned := 0
	for _, n := range g.Nodes {
		if n.Kind == workspace.KindWork && n.Role == "" && n.Status == workspace.StatusTodo {
			unassigned++
		}
	}
	if unassigned > 0 {
		return fmt.Sprintf("%d nodes need an agent", unassigned), fmt.Sprintf("%sA accepts the suggestions, a picks one", via), false
	}
	if len(g.Runnable()) > 0 {
		return "Ready", fmt.Sprintf("%sr runs the selected node, R runs every assigned node", via), false
	}
	if done == len(g.Nodes) {
		return "All nodes are done", "Type a follow-up task, or /compact to fold them into the summary", false
	}
	return "Nothing can run: nodes are waiting on unfinished dependencies", "", false
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
			prefix = lipgloss.NewStyle().Foreground(colText).Bold(true).Render("you ›") + " "
			body = lipgloss.NewStyle().Foreground(colText)
		case "lead":
			prefix = lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render("lead ›") + " "
			body = lipgloss.NewStyle().Foreground(colText)
		case "system":
			prefix = lipgloss.NewStyle().Foreground(colMuted).Bold(true).Render("· ")
			body = lipgloss.NewStyle().Foreground(colMuted)
		default:
			prefix = lipgloss.NewStyle().Foreground(colFail).Bold(true).Render("! ")
			body = lipgloss.NewStyle().Foreground(colFail)
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
				out = append(out, "  "+lead+body.Render(wl))
			}
		}
	}
	if m.splitting {
		spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
		out = append(out, "  "+lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render("lead ›")+" "+
			lipgloss.NewStyle().Foreground(colText).Render(spin+" splitting the task…  (Esc cancels)"))
		if withStream && m.stream != nil {
			if last := m.stream.lastLine(); last != "" {
				out = append(out, "    "+lipgloss.NewStyle().Foreground(colMuted).Render(truncateOneLine(last, w-4)))
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
			if hasDiff(n) {
				hs = append(hs, keyHint{"d", "diff"})
			}
		case !m.g.Assigned(n):
			hs = append(hs, keyHint{"a", "assign"}, keyHint{"A", "accept all"})
		case len(m.g.Blockers(n.ID)) == 0:
			hs = append(hs, keyHint{"r", "run"}, keyHint{"R", "run all"})
		default:
			hs = append(hs, keyHint{"R", "run all"}, keyHint{"a", "reassign"})
		}
	}
	if n := m.selNode(); n != nil && n.Kind == workspace.KindWork && (n.Status == workspace.StatusTodo || n.Status == workspace.StatusFailed) {
		hs = append(hs, keyHint{"K", "skills"})
	}
	switch {
	case m.focus == wsFocusSidebar && m.sbSess >= 0:
		hs = []keyHint{{"Enter", "resume"}}
		hs = append(hs, keyHint{"↑↓", "move"}, keyHint{"Tab", "main"}, keyHint{"b", "hide"}, keyHint{"?", "help"})
		return hs
	case m.focus == wsFocusSidebar:
		close := keyHint{"b", "hide"}
		if m.sidebarOverlay() {
			close = keyHint{"Esc", "close"}
		}
		hs = append(hs, keyHint{"↑↓", "move"}, keyHint{"Tab", "main"}, close)
	case m.feedMode():
		if n := m.selNode(); n != nil {
			hs = append(hs, keyHint{"Space", "expand"})
		}
		hs = append(hs, keyHint{"↑↓", "cards"}, keyHint{"PgUp/PgDn", "scroll"}, keyHint{"g", "graph"}, keyHint{"Tab", "prompt"})
	default:
		hs = append(hs, keyHint{"g", "activity"}, keyHint{"←↑↓→", "move"}, keyHint{"Tab", "prompt"})
	}
	return append(hs, keyHint{"?", "help"})
}

func (m workspaceModel) hintLine() string {
	w := m.width
	var hs []keyHint
	switch {
	case m.splitting:
		hs = []keyHint{{"Esc", "cancel"}, {"Ctrl+C", "quit"}}
	case m.focus != wsFocusPrompt:
		hs = m.graphHints()
	case m.paletteOpen():
		hs = []keyHint{{"↑↓", "choose"}, {"Tab", "complete"}, {"Enter", "run"}, {"Esc", "close"}}
	default:
		next := "main"
		if m.sidebarVisible() {
			next = "sidebar"
		}
		hs = []keyHint{{"Enter", "send"}, {"/", "commands"}, {"Tab", next}}
		if m.suggestion() != "" {
			hs = append([]keyHint{{"Enter", "send"}, {"→", "accept suggestion"}}, hs[1:]...)
		}
		if m.input.Value() == "" {
			hs = append(hs, keyHint{"?", "help"})
		}
		hs = append(hs, keyHint{"Ctrl+C", "quit"})
	}
	return wsHintBar(w, fitHints(w, hs))
}

// progressStats is the dim "2/5 done · 1 failed" text.
func (m workspaceModel) progressStats() string {
	g := m.g
	warns := 0
	for _, n := range g.Nodes {
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
	return stats
}

// headerLine is the calm top line: the product mark and project on the left,
// the progress stats dim on the right. The branch is in the bottom bar.
func (m workspaceModel) headerLine() string {
	w := m.width
	dim := lipgloss.NewStyle().Foreground(colMuted)
	right := dim.Render(m.progressStats())
	fits := func(r string) bool { return ansi.StringWidth(r)+2+12 <= w }
	if m.eng != nil && m.eng.Auto() {
		if r := lipgloss.NewStyle().Foreground(colWarn).Render("AUTO") + dim.Render(" · ") + right; fits(r) {
			right = r
		}
	}
	if p := m.cfg.Project.Profile; p != "" {
		if r := dim.Render("profile: "+p+" · ") + right; fits(r) {
			right = r
		}
	}
	right += "  "
	left := "  " + lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render("◆ decsr")
	if p := m.project(); p != "" {
		left += dim.Render(" · ") + lipgloss.NewStyle().Foreground(colText).Render(truncateOneLine(p, max(w/4, 8)))
	}
	if ansi.StringWidth(left)+ansi.StringWidth(right) > w {
		right = ""
	}
	gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// titleLine is the session title in bold.
func (m workspaceModel) titleLine() string {
	title := workspace.Sanitize(m.sess.snapshot().Title)
	if title == "" {
		title = "new session"
	}
	return "  " + lipgloss.NewStyle().Foreground(colText).Bold(true).Render(truncateOneLine(title, max(m.width-4, 4)))
}

// emptyPane is the body of the graph pane before the first task.
func (m workspaceModel) emptyPane(iw, ih int) []string {
	dim := lipgloss.NewStyle().Foreground(colMuted)
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
		body = append(body, "", centerLine(dim.Render(truncateOneLine(line, iw-2)), iw))
	}
	if m.spec != nil {
		pre := fmt.Sprintf("Spec Kit: %s has %d open %s — ", m.spec.Dir, m.spec.Open, pluralize(m.spec.Open, "task", "tasks"))
		const cmd = "/import speckit"
		if ansi.StringWidth(pre+cmd) <= iw-2 {
			line := dim.Render(pre) + lipgloss.NewStyle().Foreground(colText).Render(cmd)
			body = append(body, "", centerLine(line, iw))
		}
	}
	// The logo is one accent colour, without the stars; a narrow pane gets none.
	var logo []string
	if iw >= 62 {
		accent := []lipgloss.Color{colAccent, colAccent, colAccent, colAccent, colAccent}
		for _, l := range renderBlockTextColored("DECSR", accent) {
			logo = append(logo, centerLine(l, iw))
		}
	}
	lines := body
	if len(logo) > 0 && len(logo)+1+len(body) <= ih {
		lines = append(append(append([]string(nil), logo...), ""), body...)
	}
	top := max((ih-len(lines))/2, 0)
	return append(make([]string, top), lines...)
}

// Workspace layout.
const (
	wsGraphMinH      = 12 // fewest rows kept for the graph pane when the chat grows
	wsGraphMinHInsp  = 16 // the same with the inspector beside it
	wsChatExtraLimit = 16 // most rows the chat takes over from the graph
)

// paneLabel is the dim line that names a pane; a focused pane gets normal weight.
func paneLabel(text string, focused bool) string {
	st := lipgloss.NewStyle().Foreground(colMuted)
	if focused {
		st = lipgloss.NewStyle().Foreground(colText).Bold(true)
	}
	return "  " + st.Render(text)
}

// inspDefault reports whether the inspector sits beside the graph unless the
// user hides it: only in graph mode with room for both.
func (m workspaceModel) inspDefault() bool {
	return m.showGraph && m.mainWidth() >= wsInspectorMin
}

// showInspector reports whether the inspector sits beside the graph.
func (m workspaceModel) showInspector() bool {
	return m.inspDefault() && !m.inspFlip
}

// graphRows is the height the graph pane wants: its label, the cards and a
// blank line, but at least a sensible minimum.
func (m workspaceModel) graphRows() int {
	minH := wsGraphMinH
	if m.showInspector() {
		minH = wsGraphMinHInsp
	}
	if m.g == nil || len(m.g.Nodes) == 0 {
		return minH
	}
	return max(computeCanvasLayout(m.g, canvasBoxMin).totalH+2, minH)
}

// mainPanes draws the graph pane and the inspector in exactly mainH lines.
// The panes have no frame; a single faint divider separates them.
func (m workspaceModel) mainPanes(mainH int) []string {
	w := m.mainWidth()
	g := m.g
	models, runners := m.roleLooks()
	spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]

	n := m.selNode()
	tail := m.outputTail(m.sel, mainH)
	graphPane := func(cw int) []string {
		if !m.showGraph {
			return m.feedPane(cw, mainH)
		}
		out := []string{paneLabel("Graph", m.focus == wsFocusGraph) + lipgloss.NewStyle().Foreground(colMuted).Render(" · g activity")}
		var body []string
		if len(g.Nodes) == 0 {
			body = m.emptyPane(cw-4, mainH-1)
		} else {
			body = strings.Split(renderCanvas(g, canvasOpts{width: cw - 4, height: mainH - 1, selected: m.sel, models: models, runners: runners, icons: m.cfg.UI.Icons, spinner: spin}), "\n")
		}
		for _, l := range body {
			out = append(out, "  "+l)
		}
		return out
	}
	inspBody := func(iw, ih int) []string {
		return strings.Split(renderInspector(g, n, m.cfg, m.plug, iw, ih, tail), "\n")
	}
	var main []string
	switch {
	case m.showInspector():
		cw := w - wsInspectorW
		iw := wsInspectorW - 3 // divider, one cell of padding on each side
		cl := graphPane(cw)
		il := append([]string{lipgloss.NewStyle().Foreground(colMuted).Render("Inspector")}, inspBody(iw, mainH-1)...)
		div := lipgloss.NewStyle().Foreground(colBorder).Render("│")
		for i := 0; i < mainH; i++ {
			c, p := "", ""
			if i < len(cl) {
				c = cl[i]
			}
			if i < len(il) {
				p = il[i]
			}
			main = append(main, padRight(ansi.Truncate(c, cw, ""), cw)+div+" "+ansi.Truncate(p, iw, ""))
		}
	case !m.inspDefault() && m.inspFlip:
		// No room beside the graph, or the activity feed: the inspector is a
		// box over the pane.
		bw := min(w, 60)
		bh := max(mainH-2, min(mainH, 5))
		body := inspBody(bw-4, bh-2)
		for len(body) < bh-2 {
			body = append(body, "")
		}
		title := "Inspector"
		if n != nil {
			title = n.ID
		}
		box := wsBox(title, bw, body[:bh-2])
		main = strings.Split(overlayCenter(strings.Join(graphPane(w), "\n"), box, w, mainH), "\n")
	default:
		main = graphPane(w)
	}
	for len(main) < mainH {
		main = append(main, "")
	}
	return main[:mainH]
}

func (m workspaceModel) promptBox() []string {
	bc := colBorder
	if m.focus == wsFocusPrompt && !m.splitting {
		bc = colAccent
	}
	boxStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(bc).MarginLeft(2)
	in := m.input
	if sug := m.suggestion(); sug != "" {
		in.Placeholder = sug
		in.FocusedStyle.Placeholder, in.BlurredStyle.Placeholder = styleWsGhost, styleWsGhost
		// The textarea draws with the style its pointer was set to by
		// Focus or Blur; on this copy that pointer still names the original.
		if in.Focused() {
			in.Focus()
		} else {
			in.Blur()
		}
	}
	body := in.View()
	if chips := m.promptChips(); chips != "" {
		body += "\n" + chips
	}
	return strings.Split(boxStyle.Render(body), "\n")
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
		pal = []string{lipgloss.NewStyle().Foreground(colFail).Render("! no such command")}
	} else {
		pal = renderCmdList(matches, m.cmdIdx, m.width-2, maxRows)
	}
	for i := range pal {
		pal[i] = " " + pal[i]
	}
	return pal
}

// feedMode reports whether the main pane shows the activity feed.
func (m workspaceModel) feedMode() bool { return !m.showGraph }

// mainLayout returns the rows around the main area: the chat strip under the
// graph (none in the feed, where the chat is part of it) and the rows the
// header, title, status, prompt box and hints take together.
func (m workspaceModel) mainLayout(box []string) (chatRows, fixed int) {
	if !m.feedMode() {
		chatRows = m.chatRows()
	}
	fixed = 1 + 1 + chatRows + len(box) + 1 + 1 // header, status, chat, prompt, hints, status bar
	if !m.sidebarInline() {
		fixed++ // the title line; the sidebar carries it otherwise
	}
	return chatRows, fixed
}

// mainHeight is the height of the main area (the sidebar and the pane).
func (m workspaceModel) mainHeight() int {
	_, fixed := m.mainLayout(m.promptBox())
	return max(m.height-fixed, 1)
}

// mainArea draws the sidebar, a divider and the main pane in mainH rows.
func (m workspaceModel) mainArea(mainH int) []string {
	main := m.mainPanes(mainH)
	switch {
	case m.sidebarInline():
		sw := m.sidebarW()
		side := m.sidebarRows(sw, mainH)
		div := lipgloss.NewStyle().Foreground(colBorder).Render("│")
		out := make([]string, mainH)
		for i := range out {
			out[i] = padRight(ansi.Truncate(side[i], sw, ""), sw) + div + main[i]
		}
		return out
	case m.sidebarOverlay():
		box := m.sidebarOverlayBox(mainH)
		return strings.Split(overlayAt(strings.Join(main, "\n"), box, 1, 0, m.width, mainH), "\n")[:mainH]
	}
	return main
}

func (m workspaceModel) mainView() string {
	w, h := m.width, m.height
	box := m.promptBox()
	chatRows, fixed := m.mainLayout(box)
	pal := m.paletteLines(h - fixed - 6)
	mainH := h - fixed - len(pal)
	// Rows the graph does not need go to the chat, as far as it has lines.
	if spare := mainH - m.graphRows(); spare > 0 && m.hasNodes() && !m.feedMode() {
		need := len(m.transcriptLines(w-2, true)) - chatRows
		grow := min(min(spare, need), wsChatExtraLimit)
		if grow > 0 {
			chatRows += grow
			mainH -= grow
		}
	}

	parts := []string{m.headerLine()}
	if !m.sidebarInline() {
		parts = append(parts, m.titleLine())
	}
	parts = append(parts, m.mainArea(mainH)...)
	parts = append(parts, m.statusLine())
	if !m.feedMode() {
		parts = append(parts, m.chatStrip(chatRows)...)
	}
	parts = append(parts, pal...) // directly above the prompt box
	parts = append(parts, box...)
	parts = append(parts, m.hintLine(), m.statusBar())
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
		m.headerLine(),
		"  " + lipgloss.NewStyle().Foreground(colText).Bold(true).Render(truncateOneLine(title, w-4)),
		vp.View(),
		wsHintBar(w, fitHints(w, hints)),
	}
	return strings.Join(parts, "\n")
}
