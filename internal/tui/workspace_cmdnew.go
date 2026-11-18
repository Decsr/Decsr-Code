package tui

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── result lines ─────────────────────────────────────────────────────────────

// sayUsage prints a usage line of a command.
func (m *workspaceModel) sayUsage(usage string) { m.say("error", "usage: "+usage) }

// ── /help ────────────────────────────────────────────────────────────────────

func (m workspaceModel) cmdHelp(args string) (tea.Model, tea.Cmd) {
	arg := strings.ToLower(strings.TrimSpace(args))
	if arg == "" {
		m.helpName, m.helpPS = "", ui.PanelState{}
		m.overlay = wsOverlayHelp
		return m, nil
	}
	name := "/" + strings.TrimPrefix(arg, "/")
	if hint := removedHint(name); hint != "" {
		m.say("system", hint)
		return m, nil
	}
	c, ok := m.lookup(name)
	if !ok {
		m.say("error", fmt.Sprintf("no command %s — /help lists them", name))
		return m, nil
	}
	m.helpName, m.helpPS = c.name, ui.PanelState{}
	m.overlay = wsOverlayHelp
	return m, nil
}

// helpOpts is the panel of /help: the groups, or the forms of one command.
func (m workspaceModel) helpOpts() ui.PanelOpts {
	o := ui.PanelOpts{Title: "Help", Color: ui.Accent, Scroll: m.helpPS.Scroll, ScreenW: m.width, ScreenH: m.height,
		Keys: []ui.Hint{{Key: "↑↓", Label: "scroll"}, {Key: "Esc", Label: "close"}}}
	inner := ui.PanelInner(m.width)
	cmds := m.commandList()
	if m.helpName != "" {
		if c, ok := m.lookup(m.helpName); ok {
			o.Title, o.Color, o.Body = c.name, ui.GroupColor(c.group), helpDetail(c, inner)
			return o
		}
	}
	o.Body = helpOverview(cmds, inner)
	return o
}

func (m workspaceModel) helpBox() string { return ui.Panel(m.helpOpts()) }

// helpOverview lists the groups, one line each (wrapped when long), with the
// command names in the group colour.
func helpOverview(cmds []wsCommand, inner int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	const labelW = 17
	var out []string
	for _, g := range cmdGroups {
		members := groupMembers(cmds, g)
		if len(members) == 0 {
			continue
		}
		gc := ui.GroupColor(g)
		label := lipgloss.NewStyle().Foreground(gc).Bold(true).Render(padRight(g, labelW))
		var names []string
		for _, c := range members {
			names = append(names, c.name)
		}
		for i, l := range wrapText(strings.Join(names, " "), max(inner-labelW, 10)) {
			lead := strings.Repeat(" ", labelW)
			if i == 0 {
				lead = label
			}
			out = append(out, lead+lipgloss.NewStyle().Foreground(gc).Render(l))
		}
	}
	return append(out, "",
		dim.Render("/help <command> shows its forms and examples"),
		dim.Render("? keyboard shortcuts · Ctrl+K go to · Tab switches between the prompt and the graph"))
}

// helpDetail is the body of /help <command>: the aliases, the forms in two
// columns and the example.
func helpDetail(c wsCommand, inner int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	gc := ui.GroupColor(c.group)
	head := lipgloss.NewStyle().Foreground(gc).Bold(true)
	desc := c.desc
	if c.tag != "" {
		desc += " " + c.tag
	}
	out := []string{lipgloss.NewStyle().Foreground(ui.Text).Render(desc)}
	var meta []string
	if len(c.aliases) > 0 {
		meta = append(meta, "alias "+strings.Join(c.aliases, ", "))
	}
	if c.group != "" {
		meta = append(meta, groupTag(c.group))
	}
	if len(meta) > 0 {
		out = append(out, dim.Render(strings.Join(meta, " · ")))
	}
	forms := c.forms
	if len(forms) == 0 && c.group == ui.GroupNamePlugin {
		usage := c.name
		if c.hint != "" {
			usage += " " + c.hint
		}
		forms = []wsForm{{usage, "run the command with your text as its arguments"}}
	}
	if len(forms) > 0 {
		out = append(out, "")
		var rows [][]string
		usageSt := lipgloss.NewStyle().Foreground(gc)
		for _, f := range forms {
			rows = append(rows, []string{usageSt.Render(f.usage), dim.Render(f.does)})
		}
		out = append(out, ui.Table(ui.TableOpts{Cols: []ui.Col{{Title: "Form"}, {Title: "What it does"}}, Rows: rows, Header: gc, Width: inner})...)
	}
	if c.example != "" {
		out = append(out, "", head.Render("Example"), "  "+lipgloss.NewStyle().Foreground(ui.Text).Render(c.example))
	}
	return out
}

// helpKey handles the keys of the /help panel.
func (m workspaceModel) helpKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "q", "enter", "?":
		m.overlay = wsOverlayNone
		return m, nil
	}
	m.helpPS.Key(k, 0)
	o := m.helpOpts()
	m.helpPS.Scroll = ui.PanelWindow(m.helpPS.Scroll, len(o.Body), ui.PanelRows(o))
	return m, nil
}

// ── /rename and /copy ────────────────────────────────────────────────────────

const maxTitleRunes = 80

func (m workspaceModel) cmdRename(args string) (tea.Model, tea.Cmd) {
	title := strings.TrimSpace(workspace.Sanitize(args))
	if title == "" {
		m.sayUsage("/rename <title>")
		return m, nil
	}
	if r := []rune(title); len(r) > maxTitleRunes {
		title = string(r[:maxTitleRunes])
	}
	m.sess.update(func(s *workspace.Session) { s.Title = title })
	m.saveSession()
	m.say("ok", "session renamed to "+title)
	return m, nil
}

// clipboardOut is where /copy writes its OSC 52 sequence; tests replace it.
var clipboardOut io.Writer = os.Stdout

// maxCopyBytes keeps the sequence within what terminals and tmux accept.
const maxCopyBytes = 100_000

// osc52 is the escape sequence that sets the clipboard of the terminal. Inside
// tmux it is wrapped so tmux passes it on.
func osc52(text string, inTmux bool) string {
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
	if inTmux {
		return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return seq
}

// lastAnswer is the newest answer in the chat: a line of the lead or the
// summary of a node, whichever came last. from names its source.
func (m workspaceModel) lastAnswer() (text, from string) {
	var at time.Time
	for _, l := range m.sess.snapshot().Transcript {
		if l.Who == "lead" && strings.TrimSpace(l.Text) != "" && !l.At.Before(at) {
			text, from, at = l.Text, "the lead", l.At
		}
	}
	if m.g != nil {
		for _, n := range m.g.Nodes {
			if strings.TrimSpace(n.Summary) != "" && n.EndedAt.After(at) {
				text, from, at = n.Summary, n.ID, n.EndedAt
			}
		}
	}
	return strings.TrimSpace(workspace.SanitizeText(text)), from
}

func (m workspaceModel) cmdCopy(string) (tea.Model, tea.Cmd) {
	text, from := m.lastAnswer()
	if text == "" {
		m.say("error", "nothing to copy yet: no answer of the lead or of a node")
		return m, nil
	}
	cut := ""
	if len(text) > maxCopyBytes {
		text, cut = strings.ToValidUTF8(text[:maxCopyBytes], ""), " (cut at 100 KB)"
	}
	tmux := os.Getenv("TMUX") != ""
	if m.deps.getenv != nil {
		tmux = m.deps.getenv("TMUX") != ""
	}
	m.say("ok", fmt.Sprintf("copied the last answer of %s to the clipboard (%d characters, OSC 52)%s", from, len([]rune(text)), cut))
	seq := osc52(text, tmux)
	return m, func() tea.Msg {
		_, _ = io.WriteString(clipboardOut, seq)
		return nil
	}
}

// ── /stop and /retry ─────────────────────────────────────────────────────────

// nodeArg finds the node a command names ("T3", "t3").
func (m workspaceModel) nodeArg(arg string) *workspace.Node {
	if m.g == nil {
		return nil
	}
	arg = strings.TrimSpace(arg)
	if n := m.g.Node(arg); n != nil {
		return n
	}
	return m.g.Node(strings.ToUpper(arg))
}

func (m workspaceModel) cmdStop(args string) (tea.Model, tea.Cmd) {
	arg := strings.TrimSpace(args)
	if m.eng == nil || !m.hasNodes() {
		m.say("system", "nothing is running")
		return m, nil
	}
	if arg == "" {
		if !m.busy() {
			m.say("system", "nothing is running")
			return m, nil
		}
		eng := m.eng
		m.autoPause()
		m.say("ok", "stopping all nodes")
		return m, func() tea.Msg { eng.StopAll(); return wsStoppedMsg{} }
	}
	n := m.nodeArg(arg)
	switch {
	case n == nil:
		m.say("error", fmt.Sprintf("no node %s — /stop <node> takes an id such as T3", workspace.Sanitize(arg)))
	case n.Status != workspace.StatusRunning:
		m.say("error", n.ID+" is not running")
	default:
		m.eng.Stop(n.ID)
		m.say("ok", "stopping "+n.ID)
	}
	return m, nil
}

func (m workspaceModel) cmdRetry(args string) (tea.Model, tea.Cmd) {
	arg := strings.TrimSpace(args)
	if arg == "" {
		m.sayUsage("/retry <node>")
		return m, nil
	}
	if m.eng == nil || !m.hasNodes() {
		m.say("error", "there is no graph yet")
		return m, nil
	}
	n := m.nodeArg(arg)
	if n == nil {
		m.say("error", fmt.Sprintf("no node %s — /retry <node> takes an id such as T3", workspace.Sanitize(arg)))
		return m, nil
	}
	cmd, msg, isErr := m.startNode(n)
	if isErr {
		m.say("error", strings.TrimPrefix(msg, "! "))
		return m, cmd
	}
	m.say("ok", "started "+n.ID)
	return m, cmd
}

// startNode runs n the way the r key does. msg is the notice to show and
// isErr says it is an error; msg is empty when the node was retried after a
// usage limit.
func (m *workspaceModel) startNode(n *workspace.Node) (cmd tea.Cmd, msg string, isErr bool) {
	m.autoPaused = false
	if m.installing != "" {
		return nil, "Wait for the plugin install to finish, then run the node", true
	}
	if n.Limited() {
		m.retryLimitedKey(n)
		return m.startTick(), "", false
	}
	if err := m.eng.Run(m.runCtx, n.ID); err != nil {
		return nil, "! " + err.Error(), true
	}
	m.g = m.eng.Snapshot()
	return m.startTick(), "Started " + n.ID, false
}

// ── /model, /usage, /coordinator ─────────────────────────────────────────────

const modelUsage = "/model [rank | profile [save <name> | apply <name>]]"

func (m workspaceModel) openRoles() workspaceModel {
	m.overlay = wsOverlayRoles
	m.pickIdx = 0
	m.pickNode = ""
	return m
}

func (m workspaceModel) cmdModel(args string) (tea.Model, tea.Cmd) {
	f := strings.Fields(args)
	switch {
	case len(f) == 0:
		return m.openModel(modelTabRoles), nil
	case f[0] == "rank" && len(f) == 1:
		return m.cmdRank("")
	case f[0] == "profile" && len(f) == 1:
		return m.openModel(modelTabProfiles), nil
	case f[0] == "profile" && len(f) == 3 && f[1] == "save":
		if errText := m.saveProfileAs(f[2]); errText != "" {
			m.say("error", errText)
			return m, nil
		}
		m.say("ok", fmt.Sprintf("profile %q saved", f[2]))
		return m, nil
	case f[0] == "profile" && len(f) == 3 && f[1] == "apply":
		return m.applyProfile(f[2])
	}
	m.sayUsage(modelUsage)
	return m, nil
}

const usageUsage = "/usage [cost | budget [amount|off]]"

func (m workspaceModel) cmdUsage(args string) (tea.Model, tea.Cmd) {
	f := strings.Fields(args)
	switch {
	case len(f) == 0:
		return m.openUsage(usageTabSession, false)
	case f[0] == "cost" && len(f) == 1:
		return m.openUsage(usageTabSession, true)
	case f[0] == "budget" && len(f) == 1:
		return m.openUsage(usageTabBudget, false)
	case f[0] == "budget" && len(f) == 2:
		return m.cmdBudget(f[1])
	}
	m.sayUsage(usageUsage)
	return m, nil
}

func (m workspaceModel) cmdCoordinatorForm(args string) (tea.Model, tea.Cmd) {
	if strings.EqualFold(strings.TrimSpace(args), "now") {
		return m.cmdReview("")
	}
	return m.cmdCoordinator(args)
}

// ── /config ──────────────────────────────────────────────────────────────────

const configUsage = "/config [list | get <key> | set <key> <value>]"

func (m workspaceModel) cmdConfig(args string) (tea.Model, tea.Cmd) {
	f := strings.Fields(args)
	switch {
	case len(f) == 0, f[0] == "list" && len(f) == 1:
		return m.openConfigPanel()
	case f[0] == "get" && len(f) == 2:
		s, ok := config.FindSetting(f[1])
		if !ok {
			m.say("error", unknownSetting(f[1]))
			return m, nil
		}
		m.say("system", s.Key+" = "+showSetting(s.Get(m.cfg)))
		return m, nil
	case f[0] == "set" && len(f) >= 3:
		return m.setConfig(f[1], strings.TrimSpace(strings.SplitN(strings.TrimSpace(args), " ", 3)[2]))
	}
	m.sayUsage(configUsage)
	return m, nil
}

func showSetting(v string) string {
	if v == "" {
		return "(not set)"
	}
	return v
}

func unknownSetting(key string) string {
	return fmt.Sprintf("no setting %q — /config lists them", workspace.Sanitize(key))
}

// setConfig validates, saves in project.yaml and applies one setting.
func (m workspaceModel) setConfig(key, value string) (tea.Model, tea.Cmd) {
	s, ok := config.FindSetting(key)
	if !ok {
		m.say("error", unknownSetting(key))
		return m, nil
	}
	if s.Key == "routing.mode" { // the session keeps its own routing mode
		return m.cmdRouting(value)
	}
	next := m.cfg
	if err := s.Apply(&next, value); err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	if err := config.SaveProjectSetting(m.root, s.Key, value); err != nil {
		m.say("error", "not saved: "+oneLine(err.Error()))
		return m, nil
	}
	m.cfg = next
	note := ""
	if s.Key == "ui.theme" || s.Key == "ui.tmux" {
		note = " (takes effect at the next start)"
	}
	m.say("ok", fmt.Sprintf("%s = %s, saved in project.yaml%s", s.Key, showSetting(s.Get(m.cfg)), note))
	return m, nil
}

// ── /provider ────────────────────────────────────────────────────────────────

const providerUsage = "/provider [list | add <name> | remove <name> | test <name>]"

func (m workspaceModel) cmdProvider(args string) (tea.Model, tea.Cmd) {
	f := strings.Fields(args)
	switch {
	case len(f) == 0:
		return m.openProviderPanel()
	case f[0] == "list" && len(f) == 1:
		m.say("system", providerLines())
		return m, nil
	case f[0] == "add" && len(f) <= 2:
		return m.cmdConnect(strings.Join(f[1:], " "))
	case f[0] == "remove" && len(f) == 2:
		return m.askRemoveProvider(f[1])
	case f[0] == "test" && len(f) == 2:
		return m.testProvider(f[1])
	}
	m.sayUsage(providerUsage)
	return m, nil
}

// provRow is a provider with a key: saved (masked) or set in the environment.
type provRow struct {
	name, masked, source string
	saved                bool
}

// providerRows lists the saved providers with masked keys and the preset
// providers whose key is in the environment, by name.
func providerRows() []provRow {
	var rows []provRow
	saved := map[string]bool{}
	if entries, err := credentials.List(); err == nil {
		for _, e := range entries {
			if !credentials.IsProvider(e.Name) {
				continue
			}
			saved[e.Name] = true
			rows = append(rows, provRow{name: e.Name, masked: e.Masked, source: "saved", saved: true})
		}
	}
	for _, p := range runner.Presets() {
		if !saved[p.Name] && p.KeyEnv != "" && os.Getenv(p.KeyEnv) != "" {
			rows = append(rows, provRow{name: p.Name, source: "env " + p.KeyEnv})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows
}

// providerLines lists the saved providers with masked keys and the preset
// providers whose key is in the environment.
func providerLines() string {
	var lines []string
	for _, r := range providerRows() {
		if r.saved {
			lines = append(lines, fmt.Sprintf("%s  %s  saved", padRight(r.name, 12), r.masked))
		} else {
			lines = append(lines, fmt.Sprintf("%s  %s", padRight(r.name, 12), r.source))
		}
	}
	if len(lines) == 0 {
		return "no providers connected — /provider add deepseek"
	}
	return "providers:\n" + strings.Join(lines, "\n")
}

func (m workspaceModel) askRemoveProvider(name string) (tea.Model, tea.Cmd) {
	name = strings.ToLower(name)
	_, ok, err := credentials.Get(name)
	switch {
	case err != nil:
		m.say("error", oneLine(err.Error()))
	case !ok || !credentials.IsProvider(name):
		m.say("error", fmt.Sprintf("no saved key for %q — /provider list shows them", workspace.Sanitize(name)))
	default:
		m.confirm = wsConfirm{kind: "rmprovider", text: fmt.Sprintf("Delete the saved key for %s? y/n", name), arg: name}
		m.overlay = wsOverlayConfirm
	}
	return m, nil
}

func (m workspaceModel) doRemoveProvider(name string) workspaceModel {
	if err := credentials.Delete(name); err != nil {
		m.say("error", oneLine(err.Error()))
		return m
	}
	m.say("ok", "deleted the saved key for "+name)
	if m.panel.kind == panelProvider {
		m.overlay = wsOverlayPanel
		m.panel.note, m.panel.noteKind = "deleted the saved key for "+name, ui.ResultOK
	}
	return m
}

// providerTestMsg is the answer of /provider test.
type providerTestMsg struct {
	name   string
	models int
	err    error
}

// providerBase is the base URL of a provider: the saved one, else the preset's.
func providerBase(name string) string {
	if c, ok, err := credentials.Get(name); err == nil && ok && c.BaseURL != "" {
		return c.BaseURL
	}
	if p, ok := runner.PresetByName(name); ok {
		return p.BaseURL
	}
	return ""
}

// testProvider lists the models of a provider with its key, off the UI thread.
func (m workspaceModel) testProvider(name string) (tea.Model, tea.Cmd) {
	name = strings.ToLower(name)
	base := providerBase(name)
	if base == "" {
		m.say("error", fmt.Sprintf("unknown provider %q — /provider list shows them", workspace.Sanitize(name)))
		return m, nil
	}
	key, _, err := runner.ResolveKey(name, "")
	if err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	probe := m.deps.probeKey
	if probe == nil {
		probe = defaultProbe
	}
	m.say("system", "testing the key of "+name+"…")
	return m, func() tea.Msg {
		models, err := probe(base, key)
		return providerTestMsg{name: name, models: len(models), err: err}
	}
}

func (m workspaceModel) onProviderTest(msg providerTestMsg) (tea.Model, tea.Cmd) {
	if m.overlay == wsOverlayPanel && m.panel.kind == panelProvider {
		if msg.err != nil {
			m.panel.note, m.panel.noteKind = fmt.Sprintf("%s: %s", msg.name, oneLine(msg.err.Error())), ui.ResultError
		} else {
			m.panel.note, m.panel.noteKind = fmt.Sprintf("%s: the key works (%d models)", msg.name, msg.models), ui.ResultOK
		}
		return m, nil
	}
	if msg.err != nil {
		m.say("error", fmt.Sprintf("%s: %s", msg.name, oneLine(msg.err.Error())))
		return m, nil
	}
	m.say("ok", fmt.Sprintf("%s: the key works (%d models)", msg.name, msg.models))
	return m, nil
}
