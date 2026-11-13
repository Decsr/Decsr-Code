package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	wsInputHint     = "Type a task…  (/ for commands)"
	wsSplittingHint = "The lead is splitting your task…"
)

// WorkspaceResult tells the caller why the Workspace closed.
type WorkspaceResult struct {
	OpenMenu bool
	Cfg      config.Config
}

// wsDeps are the seams between the model and the outside world.
type wsDeps struct {
	split     func(ctx context.Context, task string, onToken func(string)) (*workspace.Graph, runner.TokenUsage, error)
	newEngine func(g *workspace.Graph) *workspace.Engine
	saveRole  func(name string, ac config.AgentConfig) (config.Config, error) // persists and returns the reloaded config
	checkCmds func() []string
}

// wsLiveExec builds the real executor per node from the current config, so a
// role created while the Workspace is open can run straight away.
type wsLiveExec struct {
	root   string
	cfg    func() config.Config
	pctx   func() string
	checks func() []string
}

func (e wsLiveExec) Run(ctx context.Context, g *workspace.Graph, n *workspace.Node, emit func(workspace.Event)) (workspace.Result, error) {
	c := e.cfg()
	agents := make(map[string]config.AgentConfig, len(c.Agents))
	for k, v := range c.Agents {
		agents[k] = v
	}
	c.Agents = agents
	runner.ApplyModelMemory(&c)
	return workspace.NewExecutorWithChecks(e.root, c, e.pctx(), e.checks).Run(ctx, g, n, emit)
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
func RunWorkspace(root string, cfg config.Config) (WorkspaceResult, error) {
	var mu sync.Mutex
	cur := cfg
	getCfg := func() config.Config { mu.Lock(); defer mu.Unlock(); return cur }

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
	var lastEng *workspace.Engine
	deps := wsDeps{
		split: func(ctx context.Context, task string, onToken func(string)) (*workspace.Graph, runner.TokenUsage, error) {
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
			var raw strings.Builder
			g, u, err := workspace.Split(ctx, r, pm.Model, task, workspace.Roles(c), projectContext(), func(s string) {
				raw.WriteString(s)
				if onToken != nil {
					onToken(s)
				}
			})
			writeSplitRaw(root, raw.String())
			return g, u, err
		},
		newEngine: func(g *workspace.Graph) *workspace.Engine {
			e := workspace.NewEngine(g, wsLiveExec{root: root, cfg: getCfg, pctx: projectContext, checks: checkCmds},
				workspace.MaxParallel(getCfg()), filepath.Join(root, artifacts.DirName, workspace.FileName))
			engMu.Lock()
			lastEng = e
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
			mu.Lock()
			cur = c
			mu.Unlock()
			return c, nil
		},
		checkCmds: checkCmds,
	}

	prog := tea.NewProgram(newWorkspaceModel(root, cfg, deps), tea.WithAltScreen())
	res, err := prog.Run()
	engMu.Lock()
	if lastEng != nil {
		lastEng.Close()
	}
	engMu.Unlock()
	if err != nil {
		return WorkspaceResult{Cfg: getCfg()}, err
	}
	if final, ok := res.(workspaceModel); ok {
		return WorkspaceResult{OpenMenu: final.openMenu, Cfg: final.cfg}, nil
	}
	return WorkspaceResult{Cfg: getCfg()}, nil
}

// ── Model ────────────────────────────────────────────────────────────────────

type wsScreen int

const (
	wsScreenStart wsScreen = iota
	wsScreenGraph
	wsScreenOutput
)

type wsOverlay int

const (
	wsOverlayNone wsOverlay = iota
	wsOverlayPicker
	wsOverlayRoles
	wsOverlayForm
	wsOverlayCmds
	wsOverlayConfirm
)

type wsLine struct{ kind, text string } // kind: you | lead | err

type wsConfirm struct{ kind, text string } // kind: quit | menu | discard

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

type workspaceModel struct {
	root string
	cfg  config.Config
	deps wsDeps
	now  func() time.Time

	width, height int
	screen        wsScreen
	showHelp      bool
	overlay       wsOverlay
	confirm       wsConfirm
	openMenu      bool
	quitting      bool
	quitAt        time.Time
	notice        string
	noticeErr     bool

	// start screen
	input       textarea.Model
	transcript  []wsLine
	scroll      int // lines scrolled up from the newest
	cmdIdx      int
	saved       *workspace.Graph
	splitting   bool
	splitGen    int
	splitTask   string
	cancelSplit context.CancelFunc
	stream      *wsStream

	// graph screen
	eng        *workspace.Engine
	g          *workspace.Graph
	sel        string
	inspFlip   bool
	leadTokens int
	outputs    map[string][]string
	partial    map[string]string
	outSize    map[string]int // bytes stored per node, partial line excluded
	runCtx     context.Context
	runCancel  context.CancelFunc
	ticking    bool
	spin       int

	// output screen
	outVP     viewport.Model
	outNode   string
	outFollow bool

	// overlays
	pickNode      string
	pickIdx       int
	cmdFilter     string // graph-screen command overlay
	formName      textinput.Model
	formDesc      textinput.Model
	formFocus     int
	formErr       string
	formNode      string
	formCanChange bool // "Can change files"; false saves a read-only role

	pendingTask string // task waiting for the "replace the unfinished graph" answer
}

func newWorkspaceModel(root string, cfg config.Config, deps wsDeps) workspaceModel {
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
		outputs:   map[string][]string{},
		partial:   map[string]string{},
		outSize:   map[string]int{},
		runCtx:    runCtx,
		runCancel: runCancel,
		formName:  newField("snake_case_name"),
		formDesc:  newField("what this role does, in one line"),
		outVP:     viewport.New(78, 20),
	}
	m.resize()
	if g, err := workspace.Load(m.savePath()); err == nil && len(g.Nodes) > 0 && g.Validate() == nil {
		workspace.Reconcile(g, cfg)
		if _, _, done, _ := g.Counts(); done < len(g.Nodes) {
			m.saved = g
		}
	}
	return m
}

func (m workspaceModel) Init() tea.Cmd { return nil }

func (m workspaceModel) savePath() string {
	return filepath.Join(m.root, artifacts.DirName, workspace.FileName)
}

func (m workspaceModel) project() string {
	if m.cfg.Project.Name != "" {
		return m.cfg.Project.Name
	}
	if b := filepath.Base(m.root); b != "/" && b != "." {
		return b
	}
	return ""
}

func (m *workspaceModel) resize() {
	m.input.SetWidth(max(m.width-2, 10))
	m.outVP.Width = max(m.width, 1)
	m.outVP.Height = max(m.height-3, 1)
	fw := max(min(70, m.width-2)-4-22, 10)
	m.formName.Width = fw
	m.formDesc.Width = fw
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

// ── Update ───────────────────────────────────────────────────────────────────

func (m workspaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
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
		if m.splitting || (m.eng != nil && m.eng.Running() > 0) {
			return m, wsTick()
		}
		m.ticking = false
		return m, nil
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	if m.screen == wsScreenStart && m.overlay == wsOverlayNone && !m.splitting {
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
	switch m.screen {
	case wsScreenOutput:
		return m.outputKey(msg)
	case wsScreenGraph:
		return m.graphKey(msg)
	}
	return m.startKey(msg)
}

func (m workspaceModel) ctrlC() (tea.Model, tea.Cmd) {
	now := m.now()
	if !m.quitAt.IsZero() && now.Sub(m.quitAt) <= 2*time.Second {
		return m.quit(false)
	}
	m.quitAt = now
	if m.eng != nil && m.eng.Running() > 0 {
		m.setNotice("Nodes are running. Press Ctrl+C again to stop them and quit", false)
	} else {
		m.setNotice("Press Ctrl+C again to quit", false)
	}
	return m, nil
}

// quit stops every node, then ends the program.
func (m workspaceModel) quit(openMenu bool) (tea.Model, tea.Cmd) {
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
	if m.eng != nil {
		if n := m.eng.Running(); n > 0 {
			kind := "quit"
			if openMenu {
				kind = "menu"
			}
			m.confirm = wsConfirm{kind: kind, text: fmt.Sprintf("%d node(s) are running. Stop them and leave?", n)}
			m.overlay = wsOverlayConfirm
			return m, nil
		}
	}
	return m.quit(openMenu)
}

// ── Start screen ─────────────────────────────────────────────────────────────

func (m workspaceModel) paletteOpen() bool {
	v := m.input.Value()
	return strings.HasPrefix(v, "/") && !strings.Contains(v, "\n")
}

func (m workspaceModel) startKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.splitting {
		if k == "esc" {
			return m.cancelSplitting(), nil
		}
		return m, nil
	}
	switch k {
	case "pgup":
		m.scroll += 5
		return m, nil
	case "pgdown":
		m.scroll = max(m.scroll-5, 0)
		return m, nil
	case "?":
		if m.input.Value() == "" {
			m.showHelp = true
			return m, nil
		}
	}
	if m.paletteOpen() {
		matches := wsCmdMatches(strings.TrimSpace(m.input.Value()))
		switch k {
		case "up":
			m.cmdIdx = max(m.cmdIdx-1, 0)
			return m, nil
		case "down":
			m.cmdIdx = min(m.cmdIdx+1, max(len(matches)-1, 0))
			return m, nil
		case "tab":
			if len(matches) > 0 {
				m.input.SetValue(matches[min(m.cmdIdx, len(matches)-1)].name)
			}
			return m, nil
		case "esc":
			m.input.Reset()
			m.cmdIdx = 0
			return m, nil
		case "enter":
			if len(matches) == 0 {
				m.transcript = append(m.transcript, wsLine{"err", fmt.Sprintf("unknown command %s", strings.TrimSpace(m.input.Value()))})
				return m, nil
			}
			name := matches[min(m.cmdIdx, len(matches)-1)].name
			m.input.Reset()
			m.cmdIdx = 0
			return m.runCommand(name)
		}
	}
	if k == "enter" {
		task := strings.TrimSpace(m.input.Value())
		if task == "" {
			return m, nil
		}
		if m.saved != nil {
			m.pendingTask = task
			title := m.saved.Title
			if title == "" {
				title = m.saved.Task
			}
			m.confirm = wsConfirm{kind: "replace", text: fmt.Sprintf("Replace the unfinished graph %q? y/n", title)}
			m.overlay = wsOverlayConfirm
			return m, nil
		}
		return m.startSplit(task)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.cmdIdx = 0
	return m, cmd
}

func (m workspaceModel) startSplit(task string) (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.splitGen++
	gen := m.splitGen
	m.splitting = true
	m.splitTask = task
	m.cancelSplit = cancel
	m.stream = &wsStream{}
	m.scroll = 0
	m.transcript = append(m.transcript, wsLine{"you", task})
	m.input.Reset()
	m.input.Placeholder = wsSplittingHint
	split, stream := m.deps.split, m.stream
	cmd := func() tea.Msg {
		g, u, err := split(ctx, task, stream.add)
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
	m.transcript = append(m.transcript, wsLine{"err", "split cancelled. Your text is still in the box"})
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
		m.transcript = append(m.transcript, wsLine{"err", oneLine(msg.err.Error())})
		return m, nil
	}
	if msg.g == nil || len(msg.g.Nodes) == 0 {
		m.restoreTask()
		m.transcript = append(m.transcript, wsLine{"err", "the lead returned no sub-tasks. Try describing the task differently"})
		return m, nil
	}
	m.leadTokens = msg.usage.InputTokens + msg.usage.OutputTokens
	m.transcript = append(m.transcript, wsLine{"lead", fmt.Sprintf("split into %d sub-tasks", len(msg.g.Nodes))})
	m.input.Reset()
	return m.openGraph(msg.g, true)
}

// restoreTask puts the task being split back into the input for editing.
func (m *workspaceModel) restoreTask() {
	m.input.Placeholder = wsInputHint
	m.input.SetValue(m.splitTask)
}

// openGraph hands g to a new engine and switches to the graph screen.
func (m workspaceModel) openGraph(g *workspace.Graph, fresh bool) (tea.Model, tea.Cmd) {
	if fresh {
		// The Build & test node is always there; without known commands it
		// asks for them when it runs.
		var cmds []string
		if m.deps.checkCmds != nil {
			cmds = m.deps.checkCmds()
		}
		if len(cmds) > 0 {
			g.AddCheck(cmds)
		} else {
			g.AddCheckDeferred()
		}
	}
	closeEngine(m.eng)
	if err := workspace.Save(m.savePath(), g); err != nil {
		m.setNotice("could not save the graph: "+err.Error(), true)
	}
	first := g.Nodes[0].ID
	m.eng = m.deps.newEngine(g)
	m.g = m.eng.Snapshot()
	m.sel = first
	m.outputs = map[string][]string{}
	m.partial = map[string]string{}
	m.outSize = map[string]int{}
	m.screen = wsScreenGraph
	m.overlay = wsOverlayNone
	m.scroll = 0
	m.saved = nil
	return m, tea.Batch(wsWaitEvent(m.eng), m.startTick())
}

// ── Commands ─────────────────────────────────────────────────────────────────

func (m workspaceModel) runCommand(name string) (tea.Model, tea.Cmd) {
	m.overlay = wsOverlayNone
	switch name {
	case "/help":
		m.showHelp = true
	case "/quit":
		return m.requestQuit(false)
	case "/menu":
		return m.requestQuit(true)
	case "/roles":
		m.overlay = wsOverlayRoles
		m.pickIdx = 0
		m.pickNode = ""
	case "/resume":
		if m.eng != nil {
			m.setNotice("This graph is already open", false)
			return m, nil
		}
		if m.saved == nil {
			m.report("nothing to resume: there is no unfinished graph", true)
			return m, nil
		}
		return m.openGraph(m.saved, false)
	}
	return m, nil
}

// report shows a message in the status line (graph) or the chat (start).
func (m *workspaceModel) report(s string, isErr bool) {
	if m.screen == wsScreenStart {
		kind := "lead"
		if isErr {
			kind = "err"
		}
		m.transcript = append(m.transcript, wsLine{kind, s})
		return
	}
	m.setNotice(s, isErr)
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
	if m.g == nil || m.eng == nil {
		return m, nil
	}
	dirs := map[string]string{"up": "up", "k": "up", "down": "down", "j": "down", "left": "left", "h": "left", "right": "right", "l": "right"}
	if d, ok := dirs[k]; ok {
		m.sel = canvasNeighbor(m.g, m.sel, d)
		return m, nil
	}
	n := m.selNode()
	switch k {
	case "?":
		m.showHelp = true
	case "/":
		m.overlay = wsOverlayCmds
		m.cmdFilter = "/"
		m.cmdIdx = 0
	case "q":
		return m.requestQuit(false)
	case "i":
		m.inspFlip = !m.inspFlip
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
	case "n":
		return m.newTask()
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

func (m workspaceModel) newTask() (tea.Model, tea.Cmd) {
	if m.eng.Running() > 0 {
		m.setNotice("Stop the running nodes first (S), then press n", true)
		return m, nil
	}
	_, _, done, _ := m.g.Counts()
	if done < len(m.g.Nodes) {
		m.confirm = wsConfirm{kind: "discard", text: "Discard this graph?"}
		m.overlay = wsOverlayConfirm
		return m, nil
	}
	return m.toStart(false), nil
}

func (m workspaceModel) toStart(discard bool) workspaceModel {
	if discard {
		_ = os.Remove(m.savePath())
	}
	closeEngine(m.eng)
	m.eng = nil
	m.g = nil
	m.sel = ""
	m.screen = wsScreenStart
	m.overlay = wsOverlayNone
	m.saved = nil
	m.input.Reset()
	return m
}

// ── Output screen ────────────────────────────────────────────────────────────

func (m workspaceModel) outputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.screen = wsScreenGraph
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
		switch k {
		case "y", "Y":
			c := m.confirm
			m.overlay = wsOverlayNone
			switch c.kind {
			case "quit":
				return m.quit(false)
			case "menu":
				return m.quit(true)
			case "discard":
				return m.toStart(true), nil
			case "replace":
				task := m.pendingTask
				m.pendingTask = ""
				return m.startSplit(task)
			}
		case "n", "N", "esc":
			m.overlay = wsOverlayNone
			m.pendingTask = ""
		}
		return m, nil

	case wsOverlayCmds:
		matches := wsCmdMatches(m.cmdFilter)
		switch k {
		case "esc":
			m.overlay = wsOverlayNone
		case "up":
			m.cmdIdx = max(m.cmdIdx-1, 0)
		case "down":
			m.cmdIdx = min(m.cmdIdx+1, max(len(matches)-1, 0))
		case "enter":
			if len(matches) > 0 {
				return m.runCommand(matches[min(m.cmdIdx, len(matches)-1)].name)
			}
		case "backspace":
			if len(m.cmdFilter) <= 1 {
				m.overlay = wsOverlayNone
			} else {
				m.cmdFilter = m.cmdFilter[:len(m.cmdFilter)-1]
				m.cmdIdx = 0
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.cmdFilter += string(msg.Runes)
				m.cmdIdx = 0
			}
		}
		return m, nil

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
	}
	return m, nil
}

func (m workspaceModel) rolesItems() []wsPickItem {
	var items []wsPickItem
	for _, r := range workspace.Roles(m.cfg) {
		items = append(items, wsPickItem{kind: "role", name: r.Name, desc: r.Description, meta: roleMetaShort(r.Agent)})
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
		v = renderHelp(w, h, "KEYBOARD — WORKSPACE", workspaceHelp())
	case m.screen == wsScreenOutput:
		v = m.outputView()
	case m.screen == wsScreenGraph:
		v = m.graphView()
	default:
		v = m.startView()
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
		inherits := fmt.Sprintf("Runner / model: inherits %s / %s — change it in /menu → Project Setup", orDash(pm.Runner), orDash(pm.Model))
		return renderRoleForm(m.formName.View(), m.formDesc.View(), m.formFocus, m.formCanChange, inherits, m.formErr, w)
	case wsOverlayCmds:
		matches := wsCmdMatches(m.cmdFilter)
		boxW := min(60, w-2)
		body := []string{lipgloss.NewStyle().Foreground(crt.bright).Render(m.cmdFilter + "█")}
		if len(matches) == 0 {
			body = append(body, lipgloss.NewStyle().Foreground(crt.warn).Render("! no such command"))
		}
		body = append(body, renderCmdList(matches, m.cmdIdx, boxW-4)...)
		body = append(body, "", lipgloss.NewStyle().Foreground(crt.dim).Render("↑↓ move · Enter run · Esc close"))
		return wsBox("Commands", boxW, body)
	case wsOverlayConfirm:
		boxW := min(56, w-2)
		warn := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		var body []string
		for _, l := range wrapText(m.confirm.text, boxW-4) {
			body = append(body, warn.Render(l))
		}
		if m.confirm.kind != "replace" { // its text already asks y/n
			body = append(body, "", keycap("y")+" yes   "+keycap("n")+" no")
		}
		return wsBox("Confirm", boxW, body)
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
	if text == "" && m.screen == wsScreenGraph {
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
	if g == nil {
		return "", false
	}
	_, running, done, failed := g.Counts()
	if failed > 0 {
		for _, n := range g.Nodes {
			if n.Status != workspace.StatusFailed {
				continue
			}
			if n.Kind == workspace.KindCheck {
				return fmt.Sprintf("! %s failed. Select it: f adds a fix node, r retries", n.Title), true
			}
			return fmt.Sprintf("! %s failed: %s. Select it: r retries, a changes the agent", n.ID, truncateOneLine(oneLine(n.Error), 60)), true
		}
	}
	if running > 0 {
		return fmt.Sprintf("%d running. o shows output, s stops the selected node", running), false
	}
	unassigned := 0
	for _, n := range g.Nodes {
		if n.Kind == workspace.KindWork && n.Role == "" && n.Status == workspace.StatusTodo {
			unassigned++
		}
	}
	if unassigned > 0 {
		return fmt.Sprintf("%d nodes need an agent. A accepts the suggestions, a picks one", unassigned), false
	}
	if len(g.Runnable()) > 0 {
		return "Ready. r runs the selected node, R runs every assigned node", false
	}
	if done == len(g.Nodes) {
		return "All nodes are done. n starts a new task", false
	}
	return "Nothing can run: nodes are waiting on unfinished dependencies", false
}

func (m workspaceModel) startView() string {
	w, h := m.width, m.height
	head := screenHeader(w, "Workspace", m.project())
	hints := hintBar(w, fitHints(w, []keyHint{
		{"Enter", "send"}, {"Alt+Enter", "newline"}, {"/", "commands"}, {"Ctrl+C", "quit"}, {"?", "help"},
	}), "")

	boxStyle := lipgloss.NewStyle().Border(pixelBorder).BorderForeground(focusBorder)
	if m.splitting {
		boxStyle = boxStyle.BorderForeground(crt.border)
	}
	inputBox := strings.Split(boxStyle.Render(m.input.View()), "\n")

	pal := m.startPalette()
	bodyH := h - 1 - 1 - 1 - len(inputBox) - len(pal) // header, status, hints
	body := m.startBody(bodyH)

	parts := []string{head}
	parts = append(parts, body...)
	parts = append(parts, m.statusLine())
	parts = append(parts, pal...) // directly above the input box
	parts = append(parts, inputBox...)
	parts = append(parts, hints)
	return strings.Join(parts, "\n")
}

// startPalette is the "/" command list drawn right above the input box.
func (m workspaceModel) startPalette() []string {
	if !m.paletteOpen() {
		return nil
	}
	pal := renderCmdList(wsCmdMatches(strings.TrimSpace(m.input.Value())), m.cmdIdx, m.width-2)
	for i := range pal {
		pal[i] = " " + pal[i]
	}
	if len(pal) == 0 {
		pal = []string{" " + lipgloss.NewStyle().Foreground(crt.warn).Render("! no such command")}
	}
	return pal
}

// startBody lays out the intro and the transcript in exactly bodyH lines.
func (m workspaceModel) startBody(bodyH int) []string {
	w := m.width
	dim := lipgloss.NewStyle().Foreground(crt.dim)

	var tag []string
	for _, l := range wrapText("Describe a task. The lead splits it into sub-tasks; you assign the agents.", w-4) {
		tag = append(tag, centerLine(dim.Render(l), w))
	}
	if m.saved != nil {
		_, _, done, _ := m.saved.Counts()
		line := fmt.Sprintf("↺ unfinished: %q (%d/%d done) — /resume", m.saved.Title, done, len(m.saved.Nodes))
		tag = append(tag, centerLine(lipgloss.NewStyle().Foreground(galaxyGold).Render(truncateOneLine(line, w-2)), w))
	}
	var logo []string
	for _, l := range strings.Split(HomeModel{}.renderLogo(w), "\n") {
		logo = append(logo, centerLine(l, w))
	}

	trans := m.transcriptLines(w - 2)
	avail := bodyH

	full := append(append(append([]string(nil), logo...), ""), tag...)
	var intro []string
	switch {
	case len(trans) == 0 && len(full) <= avail:
		intro = full
	case len(full)+1+len(trans) <= avail:
		intro = full
	case len(tag)+1+len(trans) <= avail:
		intro = tag
	}

	var out []string
	if len(trans) == 0 {
		top := max((avail-len(intro))/2, 0)
		out = append(out, make([]string, top)...)
		out = append(out, intro...)
	} else {
		room := avail - len(intro)
		if len(intro) > 0 {
			room--
		}
		end := len(trans) - min(m.scroll, max(len(trans)-1, 0))
		start := max(end-room, 0)
		out = append(out, intro...)
		if len(intro) > 0 {
			out = append(out, "")
		}
		if gap := avail - len(out) - (end - start); gap > 0 {
			out = append(out, make([]string, gap)...)
		}
		out = append(out, trans[start:end]...)
	}
	for len(out) < bodyH {
		out = append(out, "")
	}
	return out[:bodyH]
}

func (m workspaceModel) transcriptLines(w int) []string {
	if w < 8 {
		return nil
	}
	var out []string
	for _, l := range m.transcript {
		var prefix string
		var body lipgloss.Style
		switch l.kind {
		case "you":
			prefix = lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render("you ›") + " "
			body = lipgloss.NewStyle().Foreground(crt.primary)
		case "lead":
			prefix = lipgloss.NewStyle().Foreground(roleColor("pm")).Bold(true).Render("lead ›") + " "
			body = lipgloss.NewStyle().Foreground(crt.primary)
		default:
			prefix = lipgloss.NewStyle().Foreground(crt.warn).Bold(true).Render("! ")
			body = lipgloss.NewStyle().Foreground(crt.warn)
		}
		pw := ansi.StringWidth(prefix)
		first := true
		for _, para := range strings.Split(l.text, "\n") {
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
		if m.stream != nil {
			if last := m.stream.lastLine(); last != "" {
				out = append(out, "   "+lipgloss.NewStyle().Foreground(crt.dim).Render(truncateOneLine(last, w-4)))
			}
		}
	}
	return out
}

// graphHints returns the context-sensitive footer hints.
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
	return append(hs, keyHint{"←↑↓→", "move"}, keyHint{"n", "new task"}, keyHint{"?", "help"})
}

func (m workspaceModel) summaryLine() string {
	w := m.width
	g := m.g
	title := g.Title
	if title == "" {
		title = g.Task
	}
	_, running, done, failed := g.Counts()
	tokens := m.leadTokens
	for _, n := range g.Nodes {
		tokens += n.InputTokens + n.OutputTokens
	}
	stats := fmt.Sprintf("%d/%d done", done, len(g.Nodes))
	if running > 0 {
		stats += fmt.Sprintf(" · %d running", running)
	}
	if failed > 0 {
		stats += fmt.Sprintf(" · %d failed", failed)
	}
	warns := 0
	for _, n := range g.Nodes {
		warns += len(n.Warnings)
	}
	switch {
	case warns == 1:
		stats += " · 1 warning"
	case warns > 1:
		stats += fmt.Sprintf(" · %d warnings", warns)
	}
	stats += " · " + formatTokensCompact(tokens) + " tok"
	right := lipgloss.NewStyle().Foreground(crt.dim).Render(stats)
	if m.eng != nil && m.eng.Auto() {
		right = chip("AUTO", galaxyGold) + " " + right
	}
	right += " "
	left := " " + lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render(truncateOneLine(title, max(w-ansi.StringWidth(right)-3, 4)))
	gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func (m workspaceModel) graphView() string {
	w, h := m.width, m.height
	g := m.g
	mainH := h - 4
	models := map[string]string{}
	for _, r := range workspace.Roles(m.cfg) {
		models[r.Name] = shortModel(r.Agent.Runner, r.Agent.Model)
	}
	spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]

	showInsp := (w >= wsInspectorMin) != m.inspFlip
	n := m.selNode()
	tail := m.outputTail(m.sel, mainH)
	inspTitle := "INSPECTOR"
	if n != nil {
		inspTitle = n.ID
	}
	canvasFrame := func(cw int) string {
		canvas := strings.Split(renderCanvas(g, canvasOpts{width: cw - 2, height: mainH - 2, selected: m.sel, models: models, spinner: spin}), "\n")
		return wsFrame("GRAPH", galaxyAzure, true, cw, mainH, 0, canvas)
	}
	inspFrame := func(iw int) string {
		insp := strings.Split(renderInspector(g, n, m.cfg, iw-4, mainH-2, tail), "\n")
		return wsFrame(inspTitle, galaxyAzure, false, iw, mainH, 1, insp)
	}
	var main []string
	switch {
	case showInsp && w >= wsInspectorMin:
		cw := w - wsInspectorW
		cl := strings.Split(canvasFrame(cw), "\n")
		il := strings.Split(inspFrame(wsInspectorW), "\n")
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
	case showInsp:
		main = strings.Split(inspFrame(w), "\n")
	default:
		main = strings.Split(canvasFrame(w), "\n")
	}
	for len(main) < mainH {
		main = append(main, "")
	}
	main = main[:mainH]

	parts := []string{screenHeader(w, "Workspace", m.project()), m.summaryLine()}
	parts = append(parts, main...)
	parts = append(parts, m.statusLine(), hintBar(w, fitHints(w, m.graphHints()), ""))
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
