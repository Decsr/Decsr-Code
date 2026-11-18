package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/doctor"
	"github.com/Decsr/Decsr-Code/internal/lang"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// statusRunner is one runner line of /status.
type statusRunner struct {
	name, state string
	ok          bool // usable: installed, key saved or server running
}

// statusIn is what the lines of /status are made of.
type statusIn struct {
	version, project, language, title, isolation string
	runners                                      []statusRunner
	runnersLoaded                                bool
	providers                                    []provRow
	pluginsOn, pluginsOff                        int
	routing, autonomy, coordinator, budget       string
	parallel                                     int
	today                                        *usageSpan // nil while the sessions are read
	warnings                                     int
	doctorDone                                   bool
}

// runnerStatuses turns the probe of the runners into /status lines. claudeAuth
// is how the claude CLI logs in; only the installed claude is described by it.
func runnerStatuses(entries []frEntry, claudeAuth string) []statusRunner {
	var out []statusRunner
	for _, e := range entries {
		if !e.usable && e.kind != frCLI {
			continue // API presets and local servers show only when usable
		}
		r := statusRunner{name: e.name, ok: e.usable}
		switch {
		case e.name == "claude" && e.usable && claudeAuth == runner.AuthAPIKey:
			r.state = "API key"
		case e.name == "claude" && e.usable:
			r.state = "subscription (unattended runs need a key)"
		case e.needsKey:
			r.state = "installed, needs a DeepSeek key"
		case e.usable && e.kind == frAPI:
			r.state = e.status
		case e.usable && e.kind == frLocal:
			r.state = "running"
		case e.usable:
			r.state = "installed"
		default:
			r.state = "not installed"
		}
		out = append(out, r)
	}
	return out
}

// statusLines are the body of /status, styled.
func statusLines(in statusIn) []string {
	kv := func(k, v string) string { return dimText(padRight(k, 12)) + plainText(v) }
	proj := in.project
	if in.language != "" {
		proj += " (" + in.language + ")"
	}
	lines := []string{
		kv("Version", in.version),
		kv("Project", proj),
		kv("Session", in.title+" · "+in.isolation),
		"",
		sectionHead(ui.GroupNameSession, "RUNNERS"),
	}
	if !in.runnersLoaded {
		lines = append(lines, "  "+dimText("checking…"))
	}
	for _, r := range in.runners {
		name := lipgloss.NewStyle().Foreground(ui.RunnerColor(r.name)).Render(padRight(r.name, 10))
		if r.ok {
			lines = append(lines, "  "+name+ui.ResultLine(ui.ResultOK, r.state))
		} else {
			lines = append(lines, "  "+name+dimText("· "+r.state))
		}
	}
	lines = append(lines, "", sectionHead(ui.GroupNameAgents, "PROVIDERS"))
	if len(in.providers) == 0 {
		lines = append(lines, "  "+dimText("none connected · /provider add deepseek"))
	}
	for _, p := range in.providers {
		lines = append(lines, "  "+plainText(padRight(p.name, 12))+dimText(padRight(p.masked, 12)+p.source))
	}
	lines = append(lines, "", sectionHead(ui.GroupNameProject, "PLUGINS"),
		"  "+plainText(fmt.Sprintf("%d on · %d off", in.pluginsOn, in.pluginsOff)),
		"", sectionHead(ui.GroupNameRun, "RUN"),
		"  "+kv("Routing", in.routing),
		"  "+kv("Autonomy", in.autonomy),
		"  "+kv("Coordinator", in.coordinator),
		"  "+kv("Parallel", fmt.Sprintf("%d nodes at once", in.parallel)),
		"  "+kv("Budget", in.budget),
		"", sectionHead(ui.GroupNameUsage, "TODAY"))
	if in.today == nil {
		lines = append(lines, "  "+dimText("reading the saved sessions…"))
	} else {
		lines = append(lines, "  "+plainText(fmt.Sprintf("%d %s · %s tokens · %s", in.today.sessions, pluralize(in.today.sessions, "session", "sessions"), ui.Num(in.today.tokens), in.today.costCell())))
	}
	lines = append(lines, "")
	switch {
	case !in.doctorDone:
		lines = append(lines, dimText("running the doctor checks…"))
	case in.warnings == 0:
		lines = append(lines, ui.ResultLine(ui.ResultOK, "no warnings"))
	default:
		lines = append(lines, ui.ResultLine(ui.ResultWarn, fmt.Sprintf("%d %s · /doctor for details", in.warnings, pluralize(in.warnings, "warning", "warnings"))))
	}
	return lines
}

// countProblems is the number of checks that are not OK.
func countProblems(rs []doctor.Result) int {
	n := 0
	for _, r := range rs {
		if r.Status != doctor.OK {
			n++
		}
	}
	return n
}

// statusInput gathers /status from the model and the probes read so far.
func (m workspaceModel) statusInput() statusIn {
	c, s := m.liveCfg(), m.sess.snapshot()
	title := s.Title
	if title == "" {
		title = "(untitled)"
	}
	iso := "shared checkout"
	if m.iso != nil {
		iso = "worktree ⎇ " + m.iso.Branch()
	}
	in := statusIn{
		version: Version, project: m.project(), language: lang.Resolve(c.Project.Language, m.root),
		title: title, isolation: iso, providers: providerRows(),
		routing: m.routingMode(), parallel: workspace.MaxParallel(c),
	}
	lvl, _ := m.autonomy()
	in.autonomy = fmt.Sprintf("%d %s", lvl, autonomyName(lvl))
	in.coordinator = "off"
	if m.coordinatorOn() {
		in.coordinator = "on"
	}
	for _, p := range m.plug.Plugins {
		if p.Enabled {
			in.pluginsOn++
		} else {
			in.pluginsOff++
		}
	}
	limit, _ := budgetOf(c, s)
	in.budget = "none"
	if limit > 0 {
		spent := m.use().spent
		in.budget = fmt.Sprintf("%s of %s (%d%%)", ui.Money(spent), ui.Money(limit), int(spent/limit*100+0.5))
	}
	if m.panel.probe != nil {
		in.runnersLoaded = true
		in.runners = runnerStatuses(m.panel.probe, claudeAuthMode())
	}
	if m.panel.hist != nil {
		t := m.panel.hist.today
		in.today = &t
	}
	if m.panel.docsDone {
		in.doctorDone = true
		in.warnings = countProblems(m.panel.docs)
	}
	return in
}

func (m workspaceModel) statusOpts(o *ui.PanelOpts) {
	o.Title = "Status"
	o.Keys = hintsFor(false, ui.Hint{Key: "↑↓", Label: "scroll"})
	o.Body = statusLines(m.statusInput())
}

// wsProbeMsg is the probe of the runners, read off the UI thread.
type wsProbeMsg struct{ entries []frEntry }

// wsDoctorMsg is the result of the doctor checks.
type wsDoctorMsg struct{ results []doctor.Result }

// probeRunnersCmd looks for the runners. It never reads the login files of a
// runner: whether claude is on a subscription comes from its own settings.
func (m workspaceModel) probeRunnersCmd() tea.Cmd {
	probe := m.deps.probeRunners
	if probe == nil {
		probe = func() []frEntry {
			return firstRunEntries(FirstRunDeps{LoggedIn: func(string) bool { return false }}.withDefaults())
		}
	}
	return func() tea.Msg { return wsProbeMsg{entries: probe()} }
}

// doctorCmd runs the doctor checks off the UI thread.
func (m workspaceModel) doctorRunCmd() tea.Cmd {
	run := m.deps.doctor
	if run == nil {
		return func() tea.Msg { return wsDoctorMsg{} }
	}
	return func() tea.Msg { return wsDoctorMsg{results: run()} }
}

func (m workspaceModel) openStatus() (tea.Model, tea.Cmd) {
	m = m.openPanel(panelStatus, 0)
	return m, tea.Batch(m.loadHistoryCmd(), m.probeRunnersCmd(), m.doctorRunCmd())
}

func (m workspaceModel) onProbe(msg wsProbeMsg) (tea.Model, tea.Cmd) {
	if m.overlay == wsOverlayPanel && m.panel.kind == panelStatus {
		m.panel.probe = append([]frEntry{}, msg.entries...)
	}
	return m, nil
}

func (m workspaceModel) onDoctor(msg wsDoctorMsg) (tea.Model, tea.Cmd) {
	if m.overlay == wsOverlayPanel && (m.panel.kind == panelStatus || m.panel.kind == panelDoctor) {
		m.panel.docs, m.panel.docsDone = msg.results, true
	}
	return m, nil
}

// doctorOpts is the /doctor panel: one line per check with the fix hint.
func (m workspaceModel) doctorOpts(o *ui.PanelOpts) {
	o.Title = "Doctor"
	o.Keys = hintsFor(false, ui.Hint{Key: "↑↓", Label: "scroll"})
	if !m.panel.docsDone {
		o.Body = []string{dimText("running the checks…")}
		return
	}
	o.Body = doctorLines(m.panel.docs, ui.PanelInner(m.width))
}

// doctorLines draws the checks: ✓ ok, ⚠ warning, ✗ failure, with the detail
// and, for a problem, the fix hint.
func doctorLines(rs []doctor.Result, width int) []string {
	if len(rs) == 0 {
		return []string{dimText("no checks are available here; run decsr doctor in a terminal")}
	}
	var lines []string
	nw := 0
	for _, r := range rs {
		nw = max(nw, len([]rune(r.Name)))
	}
	nw = min(nw, 18)
	for _, r := range rs {
		kind := ui.ResultOK
		switch r.Status {
		case doctor.Warn:
			kind = ui.ResultWarn
		case doctor.Fail:
			kind = ui.ResultError
		}
		text := padRight(r.Name, nw) + "  " + oneLine(r.Detail)
		lines = append(lines, clipRow(ui.ResultLine(kind, text), width))
		if r.Status != doctor.OK && r.Hint != "" {
			lines = append(lines, "    "+dimText(truncateOneLine("→ "+oneLine(r.Hint), width-4)))
		}
	}
	bad := countProblems(rs)
	lines = append(lines, "")
	if bad == 0 {
		return append(lines, ui.ResultLine(ui.ResultOK, "all checks passed"))
	}
	return append(lines, dimText(fmt.Sprintf("%d of %d %s need attention", bad, len(rs), pluralize(len(rs), "check", "checks"))))
}

func (m workspaceModel) cmdDoctor(string) (tea.Model, tea.Cmd) {
	m = m.openPanel(panelDoctor, 0)
	return m, m.doctorRunCmd()
}
