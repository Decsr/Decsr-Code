package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/doctor"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// frKind says how a runner is probed and what step 2 does with it.
type frKind int

const (
	frCLI   frKind = iota // a binary on PATH
	frAPI                 // a hosted API that needs a key
	frLocal               // a local server
)

// FirstRunDeps are the seams of the setup screen. Nil fields use the real
// implementations; Doctor has no default and is given by the caller.
type FirstRunDeps struct {
	LookPath  func(name string) (string, error)
	Reachable func(provider string) error
	KeySource func(credName, envName string) string
	LoggedIn  func(name string) bool // cheap local check, no network
	Probe     connectProbeFn         // the key test of step 2
	Apply     func(runnerName, model string) error
	Doctor    func() []doctor.Result
}

func (d FirstRunDeps) withDefaults() FirstRunDeps {
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.Reachable == nil {
		d.Reachable = runner.ProviderReachable
	}
	if d.KeySource == nil {
		d.KeySource = runner.KeySource
	}
	if d.LoggedIn == nil {
		d.LoggedIn = claudeLoggedIn
	}
	if d.Apply == nil {
		d.Apply = config.UseRunnerGlobalFresh
	}
	if d.Doctor == nil {
		d.Doctor = func() []doctor.Result { return nil }
	}
	return d
}

// claudeLoggedIn is true when the Claude CLI left a credentials file. A login
// kept elsewhere (a keychain) is not seen, and the screen then says "found".
func claudeLoggedIn(name string) bool {
	if name != "claude" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".claude", ".credentials.json"))
	return err == nil
}

// frEntry is one row of step 1.
type frEntry struct {
	name, label string
	kind        frKind
	status      string
	usable      bool // can run as it is: installed, key set or server running
	needsKey    bool // installed, but waiting for the DeepSeek key (dsh)
}

// frInstallHint says how to get a runner that is not installed.
func frInstallHint(name string) string {
	switch name {
	case "claude":
		return "install Claude Code (https://claude.com/claude-code), then run decsr setup again"
	case "opencode":
		return "install OpenCode (https://opencode.ai), then run decsr setup again"
	case "codex":
		return "install the Codex CLI (npm i -g @openai/codex), then run decsr setup again"
	case "copilot":
		return "install the Copilot CLI, then run decsr setup again"
	case "dsh":
		return "install the DeepSeek Harness CLI (dsh), then run decsr setup again"
	}
	return "install " + name + " and put it on PATH"
}

// firstRunEntries lists the runners with their status. The probes run
// together so a slow one does not hold up the others.
func firstRunEntries(d FirstRunDeps) []frEntry {
	type spec struct {
		name, label string
		kind        frKind
	}
	specs := []spec{
		{"claude", "Claude Code", frCLI},
		{"opencode", "OpenCode", frCLI},
		{"codex", "Codex", frCLI},
		{"copilot", "GitHub Copilot", frCLI},
		{"dsh", "DeepSeek Harness", frCLI},
	}
	for _, p := range runner.Presets() {
		specs = append(specs, spec{p.Name, p.Label + " API", frAPI})
	}
	specs = append(specs, spec{"ollama", "Local (Ollama)", frLocal}, spec{"lmstudio", "Local (LM Studio)", frLocal}, spec{"mlx", "Local (MLX)", frLocal})

	out := make([]frEntry, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = firstRunEntry(d, s.name, s.label, s.kind)
		}()
	}
	wg.Wait()
	// Copilot is only offered when it is installed.
	kept := out[:0]
	for _, e := range out {
		if e.name == "copilot" && !e.usable {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

func firstRunEntry(d FirstRunDeps, name, label string, kind frKind) frEntry {
	e := frEntry{name: name, label: label, kind: kind}
	switch kind {
	case frCLI:
		if _, err := d.LookPath(name); err != nil {
			e.status = "not installed"
			return e
		}
		e.usable = true
		e.status = name + " found"
		if name == "dsh" && d.KeySource("deepseek", "DEEPSEEK_API_KEY") == "" {
			e.usable, e.needsKey = false, true
			e.status = "dsh found · needs a DeepSeek key"
		}
		if name == "claude" {
			e.status = "claude CLI found"
			if d.LoggedIn(name) {
				e.status += " · logged in"
			}
		}
	case frAPI:
		env := ""
		if p, ok := runner.PresetByName(name); ok {
			env = p.KeyEnv
		}
		switch src := d.KeySource(name, env); {
		case strings.HasPrefix(src, "env "):
			e.usable, e.status = true, "key from env"
		case src != "":
			e.usable, e.status = true, "key saved"
		default:
			e.status = "needs an API key"
		}
	case frLocal:
		if err := d.Reachable(name); err != nil {
			e.status = "not running"
		} else {
			e.usable, e.status = true, "running"
		}
	}
	return e
}

// frModelFor is the model written for a runner: the preset default, the Claude
// default, or "" to leave the choice to the runner.
func frModelFor(name string) string {
	if name == "claude" {
		return runner.ClaudeDefaultModel
	}
	if p, ok := runner.PresetByName(name); ok {
		return p.DefaultModel
	}
	return ""
}

type frStep int

const (
	frPick   frStep = iota + 1 // step 1: choose a runner
	frKey                      // step 2: connect a key
	frDoctor                   // step 3: the doctor result
)

type frDoctorMsg struct{ results []doctor.Result }

// FirstRunResult is how the setup screen ended.
type FirstRunResult struct {
	Done  bool // finished with Enter on step 3; false when skipped
	Saved bool // a runner was saved as the global default
	Cfg   config.Config
}

// firstRunModel is the three-step setup screen.
type firstRunModel struct {
	deps    FirstRunDeps
	entries []frEntry
	idx     int
	step    frStep
	conn    *connectModel
	chosen  frEntry
	results []doctor.Result
	checked bool
	notice  string // a hint or an error under the list
	saved   bool
	done    bool
	width   int
	height  int
}

func newFirstRunModel(d FirstRunDeps) firstRunModel {
	d = d.withDefaults()
	return firstRunModel{deps: d, entries: firstRunEntries(d), step: frPick, width: 80, height: 24}
}

func (m firstRunModel) Init() tea.Cmd { return nil }

func (m firstRunModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case frDoctorMsg:
		m.results, m.checked = msg.results, true
	case connectClosedMsg:
		m.conn, m.step = nil, frPick
	case connectSavedMsg:
		m.conn = nil
		if msg.err != nil {
			m.step, m.notice = frPick, oneLine(msg.err.Error())
			return m, nil
		}
		return m.save()
	case connectProbeMsg, connectTickMsg:
		if m.conn != nil {
			c, cmd := m.conn.update(msg)
			m.conn = &c
			return m, cmd
		}
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m.onKey(msg)
	}
	return m, nil
}

func (m firstRunModel) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.step {
	case frKey:
		if m.conn == nil {
			return m, nil
		}
		c, cmd := m.conn.update(msg)
		m.conn = &c
		return m, cmd
	case frDoctor:
		switch msg.String() {
		case "enter":
			m.done = true
			return m, tea.Quit
		case "esc":
			return m, tea.Quit
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m, tea.Quit
	case "up", "k":
		m.idx = max(m.idx-1, 0)
		m.notice = ""
	case "down", "j":
		m.idx = min(m.idx+1, len(m.entries)-1)
		m.notice = ""
	case "enter":
		return m.choose()
	}
	return m, nil
}

// choose leaves step 1 for step 2 (an API runner without a key) or saves and
// moves to step 3.
func (m firstRunModel) choose() (tea.Model, tea.Cmd) {
	e := m.entries[m.idx]
	m.chosen, m.notice = e, ""
	if e.needsKey {
		c := newConnectModel("deepseek", false, m.deps.Probe, nil)
		m.conn, m.step = &c, frKey
		return m, nil
	}
	if e.kind == frCLI && !e.usable {
		m.notice = frInstallHint(e.name)
		return m, nil
	}
	if e.kind == frAPI && !e.usable {
		c := newConnectModel(e.name, false, m.deps.Probe, nil)
		m.conn, m.step = &c, frKey
		return m, nil
	}
	return m.save()
}

// save writes the chosen runner for all roles and starts the doctor run.
func (m firstRunModel) save() (tea.Model, tea.Cmd) {
	if err := m.deps.Apply(m.chosen.name, frModelFor(m.chosen.name)); err != nil {
		m.step, m.notice = frPick, "could not save: "+oneLine(err.Error())
		return m, nil
	}
	m.saved, m.step, m.checked = true, frDoctor, false
	doc := m.deps.Doctor
	return m, func() tea.Msg { return frDoctorMsg{results: doc()} }
}

func (m firstRunModel) View() string {
	w, h := max(m.width, 20), max(m.height, 8)
	var lines []string
	switch m.step {
	case frKey:
		lines = m.pickLines(w, "2 of 3", "Connecting "+m.chosen.label+".")
	case frDoctor:
		lines = m.doctorLines(w, h)
	default:
		lines = m.pickLines(w, "1 of 3", "No agent runner is ready yet. Pick one to start:")
	}
	hints := []ui.Hint{{Key: "↑↓", Label: "choose"}, {Key: "Enter", Label: "next"}, {Key: "Esc", Label: "skip setup"}}
	switch m.step {
	case frKey:
		hints = []ui.Hint{{Key: "Enter", Label: "next"}, {Key: "Esc", Label: "back"}}
	case frDoctor:
		hints = []ui.Hint{{Key: "Enter", Label: "open the Workspace"}, {Key: "Esc", Label: "leave"}}
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines[:h-1], ui.HintBar(hints, w))
	out := strings.Join(lines, "\n")
	if m.step == frKey && m.conn != nil {
		out = ui.PlaceOverlay(out, m.conn.box(w), w, h)
	}
	return out
}

func (m firstRunModel) pickLines(w int, count, intro string) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	warn := lipgloss.NewStyle().Foreground(ui.Warn)
	lines := []string{ui.Header("decsr · Setup", count, w), "", "  " + ui.Truncate(intro, w-2), ""}
	for i, e := range m.entries {
		lines = append(lines, "  "+ui.ListRow(ui.RowOpts{
			Left:     padRight(e.label, 20) + " " + e.status,
			Width:    w - 4,
			Selected: i == m.idx && m.step == frPick,
			Indent:   1,
		}))
	}
	lines = append(lines, "")
	if m.notice != "" {
		lines = append(lines, "  "+warn.Render(ui.Truncate(m.notice, w-2)))
	} else {
		lines = append(lines, "  "+dim.Render(ui.Truncate("Step 2 asks for a key if needed, step 3 runs a quick doctor check.", w-2)))
	}
	return lines
}

func (m firstRunModel) doctorLines(w, h int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	lines := []string{ui.Header("decsr · Setup", "3 of 3", w), "",
		"  " + ui.Truncate("Saved. All roles now use "+m.chosen.name+"; change any role in /config.", w-2)}
	if m.chosen.kind == frAPI && frModelFor(m.chosen.name) == "" {
		lines = append(lines, "  "+dim.Render(ui.Truncate("No default model for "+m.chosen.label+": pick one in /config.", w-2)))
	}
	lines = append(lines, "")
	if !m.checked {
		return append(lines, "  "+dim.Render("running the doctor checks…"))
	}
	room := max(h-len(lines)-3, 1)
	shown := m.results
	more := 0
	if len(shown) > room {
		more = len(shown) - (room - 1)
		shown = shown[:room-1]
	}
	for _, r := range shown {
		kind := map[doctor.Status]string{doctor.OK: "done", doctor.Warn: "waiting", doctor.Fail: "failed"}[r.Status]
		glyph, _, col := ui.Status(kind)
		line := lipgloss.NewStyle().Foreground(col).Render(glyph) + " " + padRight(r.Name, 22) + " " + dim.Render(oneLine(r.Detail))
		lines = append(lines, "  "+ui.Truncate(line, w-2))
	}
	if more > 0 {
		lines = append(lines, "  "+dim.Render(fmt.Sprintf("+%d more: run decsr doctor", more)))
	}
	return lines
}

// RunFirstRun shows the setup screen. The result says whether it was finished
// and carries the reloaded config when a runner was saved.
func RunFirstRun(root string, cfg config.Config, deps FirstRunDeps) (FirstRunResult, error) {
	res, err := tea.NewProgram(newFirstRunModel(deps), tea.WithAltScreen()).Run()
	if err != nil {
		return FirstRunResult{Cfg: cfg}, err
	}
	final, ok := res.(firstRunModel)
	if !ok {
		return FirstRunResult{Cfg: cfg}, nil
	}
	out := FirstRunResult{Done: final.done, Saved: final.saved, Cfg: cfg}
	if final.saved {
		if reloaded, lerr := config.Load(root); lerr == nil {
			out.Cfg = reloaded
		}
	}
	return out, nil
}
