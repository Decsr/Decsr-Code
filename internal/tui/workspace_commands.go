package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/lang"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const msgStopFirst = "stop the running nodes first"

// wsCommand is one slash command. Custom commands come from files.
type wsCommand struct {
	name    string
	aliases []string
	args    string // shown after the name, e.g. "[args]"
	desc    string
	hint    string // argument hint from the command file, shown in the palette
	tag     string // "(project)" | "(user)" | "(plugin:<name>)" for custom commands
	run     func(m workspaceModel, args string) (tea.Model, tea.Cmd)
}

// commandList is the registry: built-ins first, then custom commands that do
// not shadow a built-in name or alias.
func (m workspaceModel) commandList() []wsCommand {
	cmds := []wsCommand{
		{name: "/help", desc: "keys and commands", run: func(m workspaceModel, _ string) (tea.Model, tea.Cmd) {
			m.showHelp = true
			return m, nil
		}},
		{name: "/clear", desc: "save this session and start a new one", run: workspaceModel.cmdClear},
		{name: "/compact", desc: "fold finished nodes into the session summary", run: workspaceModel.cmdCompact},
		{name: "/resume", desc: "continue a saved session of this project", run: workspaceModel.cmdResume},
		{name: "/project", desc: "switch to another project", run: workspaceModel.cmdProject},
		{name: "/profile", desc: "save or apply a set of role settings", run: workspaceModel.cmdProfile},
		{name: "/roles", aliases: []string{"/agents"}, desc: "list roles, create a new one", run: func(m workspaceModel, _ string) (tea.Model, tea.Cmd) {
			m.overlay = wsOverlayRoles
			m.pickIdx = 0
			m.pickNode = ""
			return m, nil
		}},
		{name: "/plugin", aliases: []string{"/plugins"}, args: "[available|install <name>|enable|disable <name>|reload]", desc: "list plugins, see or install catalog plugins, enable, disable or reload", run: workspaceModel.cmdPlugin},
		{name: "/status", desc: "project, session, roles and limits", run: workspaceModel.cmdStatus},
		{name: "/cost", desc: "tokens per node and per role", run: workspaceModel.cmdCost},
		{name: "/export", desc: "write this session to a markdown file", run: workspaceModel.cmdExport},
		{name: "/config", aliases: []string{"/menu"}, desc: "settings, project setup, classic pipeline", run: func(m workspaceModel, _ string) (tea.Model, tea.Cmd) {
			return m.requestQuit(true)
		}},
		{name: "/quit", aliases: []string{"/exit"}, desc: "leave the workspace", run: func(m workspaceModel, _ string) (tea.Model, tea.Cmd) {
			return m.requestQuit(false)
		}},
	}
	taken := map[string]bool{}
	for _, c := range cmds {
		taken[c.name] = true
		for _, a := range c.aliases {
			taken[a] = true
		}
	}
	for _, cc := range append(append([]workspace.CustomCommand(nil), m.custom...), workspace.PluginCommands(m.plug)...) {
		name := "/" + cc.Name
		if taken[name] {
			continue
		}
		taken[name] = true
		cc := cc
		desc := cc.Description
		if desc == "" {
			desc = "custom command"
		}
		cmds = append(cmds, wsCommand{name: name, args: "[args]", hint: cc.ArgHint, desc: desc, tag: "(" + cc.Source + ")",
			run: func(m workspaceModel, args string) (tea.Model, tea.Cmd) {
				return m.sendTask(cc.Expand(args), strings.TrimSpace(name+" "+args))
			}})
	}
	return cmds
}

// lookup finds a command by its exact name or alias.
func (m workspaceModel) lookup(name string) (wsCommand, bool) {
	for _, c := range m.commandList() {
		if c.name == name {
			return c, true
		}
		for _, a := range c.aliases {
			if a == name {
				return c, true
			}
		}
	}
	return wsCommand{}, false
}

// cmdMatches lists the commands whose name or alias starts with prefix.
func (m workspaceModel) cmdMatches(prefix string) []wsCommand {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if !strings.HasPrefix(prefix, "/") {
		return nil
	}
	var out []wsCommand
	for _, c := range m.commandList() {
		ok := strings.HasPrefix(c.name, prefix)
		for _, a := range c.aliases {
			ok = ok || strings.HasPrefix(a, prefix)
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// busy is true while any node is running; some commands refuse then.
func (m workspaceModel) busy() bool { return m.running() > 0 }

func (m workspaceModel) refuseBusy() (tea.Model, tea.Cmd) {
	m.say("error", msgStopFirst)
	return m, nil
}

// ── /clear and /compact ──────────────────────────────────────────────────────

func (m workspaceModel) cmdClear(string) (tea.Model, tea.Cmd) {
	if m.busy() {
		return m.refuseBusy()
	}
	if m.hasNodes() {
		if _, _, done, _ := m.g.Counts(); done < len(m.g.Nodes) {
			m.confirm = wsConfirm{kind: "clear", text: "Start a new session? y/n"}
			m.overlay = wsOverlayConfirm
			return m, nil
		}
	}
	return m.doClear(), nil
}

func (m workspaceModel) doClear() workspaceModel {
	m.saveSession()
	m = m.resetSession(workspace.NewSession())
	m.refreshSaved()
	m.setNotice("New session started. The previous one is listed by /resume", false)
	return m
}

func (m workspaceModel) cmdCompact(string) (tea.Model, tea.Cmd) {
	if m.busy() {
		return m.refuseBusy()
	}
	if m.eng == nil {
		m.say("system", "nothing to compact")
		return m, nil
	}
	var folded []*workspace.Node
	kept := 0 // finished nodes left because unfinished nodes still depend on them
	if err := m.eng.Replace(func(g *workspace.Graph) {
		folded = g.Compact()
		for _, n := range g.Nodes {
			if n.Kind == workspace.KindWork && n.Status == workspace.StatusDone {
				kept++
			}
		}
	}); err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	if len(folded) == 0 {
		if kept > 0 {
			m.say("system", fmt.Sprintf("nothing to compact; %d finished %s still needed by unfinished nodes", kept, pluralize(kept, "node is", "nodes are")))
		} else {
			m.say("system", "nothing to compact")
		}
		return m, nil
	}
	m.sess.update(func(s *workspace.Session) { s.Summary = workspace.CompactSummary(s.Summary, folded) })
	msg := fmt.Sprintf("compacted %d finished %s", len(folded), pluralize(len(folded), "node", "nodes"))
	if kept > 0 {
		msg += fmt.Sprintf("; kept %d that unfinished nodes still depend on", kept)
	}
	m.say("system", msg)
	m.g = m.eng.Snapshot()
	if m.g.Node(m.sel) == nil {
		m.sel = ""
		if len(m.g.Nodes) > 0 {
			m.sel = m.g.Nodes[0].ID
		}
	}
	m.saveSession()
	return m, nil
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ── /resume ──────────────────────────────────────────────────────────────────

func (m workspaceModel) cmdResume(string) (tea.Model, tea.Cmd) {
	if m.busy() {
		return m.refuseBusy()
	}
	return m.openSessions(), nil
}

func (m workspaceModel) openSessions() workspaceModel {
	m.saveSession() // so the current session is in the list
	m.sessions = workspace.ListSessions(m.root)
	m.listIdx = 0
	m.overlay = wsOverlaySessions
	return m
}

func (m workspaceModel) loadSession(id string) (tea.Model, tea.Cmd) {
	m.overlay = wsOverlayNone
	if id == m.sess.snapshot().ID {
		return m, nil
	}
	s, err := workspace.LoadSession(m.root, id)
	if err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	m.saveSession()
	m = m.resetSession(s)
	var cmd tea.Cmd
	if s.Graph != nil && len(s.Graph.Nodes) > 0 {
		workspace.Reconcile(s.Graph, m.cfg)
		if s.NextID == 0 {
			s.NextID = nextWorkNumber(s.Graph)
		}
		m, cmd = m.attachGraph(s.Graph, false)
		m.setFocus(wsFocusGraph)
	}
	title := s.Title
	if title == "" {
		title = "untitled"
	}
	m.setNotice("Resumed session: "+title, false)
	return m, cmd
}

func (m workspaceModel) doDeleteSession(id string) workspaceModel {
	if err := workspace.DeleteSession(m.root, id); err != nil {
		m.say("error", oneLine(err.Error()))
	}
	if id == m.sess.snapshot().ID {
		m = m.resetSession(workspace.NewSession())
	}
	m.refreshSaved()
	m.sessions = workspace.ListSessions(m.root)
	m.listIdx = min(m.listIdx, max(len(m.sessions)-1, 0))
	m.overlay = wsOverlaySessions
	return m
}

// agoText is a short relative time: "just now", "5m ago", "2h ago", "3d ago".
func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// ── /project ─────────────────────────────────────────────────────────────────

type wsProject struct {
	path, name       string
	current, missing bool
}

func (m workspaceModel) cmdProject(string) (tea.Model, tea.Cmd) {
	if m.busy() {
		return m.refuseBusy()
	}
	cur := filepath.Clean(m.root)
	name := m.project()
	if name == "" {
		name = cur
	}
	list := []wsProject{{path: cur, name: name, current: true}}
	seen := map[string]bool{cur: true}
	for _, p := range m.deps.recent() {
		path := filepath.Clean(p.Path)
		if seen[path] {
			continue
		}
		seen[path] = true
		n := p.Name
		if n == "" {
			n = filepath.Base(path)
		}
		fi, err := os.Stat(path)
		list = append(list, wsProject{path: path, name: n, missing: err != nil || !fi.IsDir()})
	}
	m.projects = list
	m.listIdx = 0
	if len(list) > 1 {
		m.listIdx = 1
	}
	m.overlay = wsOverlayProjects
	return m, nil
}

// switchProject saves the session and quits with the directory to reopen in.
func (m workspaceModel) switchProject(path string) (tea.Model, tea.Cmd) {
	m.overlay = wsOverlayNone
	m.switchTo = path
	return m.quit(false)
}

// ── /profile ─────────────────────────────────────────────────────────────────

func (m workspaceModel) cmdProfile(string) (tea.Model, tea.Cmd) {
	m.profiles = m.deps.listProfiles()
	m.listIdx = 0
	m.overlay = wsOverlayProfiles
	return m, nil
}

func (m workspaceModel) applyProfile(name string) (tea.Model, tea.Cmd) {
	m.overlay = wsOverlayNone
	if m.busy() { // roles would change under the running nodes
		return m.refuseBusy()
	}
	c, err := m.deps.applyProfile(name)
	if err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	m.cfg = m.withPluginAgents(c)
	if m.eng != nil {
		if err := m.eng.Replace(func(g *workspace.Graph) { workspace.Reconcile(g, m.cfg) }); err != nil {
			m.say("error", oneLine(err.Error()))
		}
		m.g = m.eng.Snapshot()
	}
	m.say("system", fmt.Sprintf("profile %q applied", name))
	return m, nil
}

func (m workspaceModel) doDeleteProfile(name string) workspaceModel {
	if err := m.deps.deleteProfile(name); err != nil {
		m.say("error", oneLine(err.Error()))
	}
	m.profiles = m.deps.listProfiles()
	m.listIdx = min(m.listIdx, len(m.profiles))
	m.overlay = wsOverlayProfiles
	return m
}

// effectiveAgents are the roles' own configured values, for SaveProfile.
// Values a role only inherits (a custom role's runner and model from pm) are
// not saved, and neither are the description and read-only flag of a built-in
// role, which are built in.
func (m workspaceModel) effectiveAgents() map[string]config.AgentConfig {
	out := map[string]config.AgentConfig{}
	for _, r := range workspace.Roles(m.cfg) {
		if r.Plugin { // plugin roles are never saved
			continue
		}
		own := m.cfg.Agents[r.Name]
		ac := config.AgentConfig{Runner: own.Runner, Model: own.Model, Effort: own.Effort, UserSkills: own.UserSkills}
		if !r.Builtin {
			ac.Description, ac.ReadOnly = own.Description, own.ReadOnly
		}
		out[r.Name] = ac
	}
	return out
}

// ── list overlays (sessions, projects, profiles) ─────────────────────────────

func (m workspaceModel) listLen() int {
	switch m.overlay {
	case wsOverlaySessions:
		return len(m.sessions)
	case wsOverlayProjects:
		return len(m.projects) + 1
	case wsOverlayProfiles:
		return len(m.profiles) + 1
	}
	return 0
}

func (m workspaceModel) listKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = wsOverlayNone
	case "up", "k":
		m.listIdx = max(m.listIdx-1, 0)
	case "down", "j":
		m.listIdx = min(m.listIdx+1, max(m.listLen()-1, 0))
	case "enter":
		return m.listChoose()
	case "d":
		switch {
		case m.overlay == wsOverlaySessions && m.listIdx < len(m.sessions):
			m.confirm = wsConfirm{kind: "delsession", text: "Delete this session? y/n", arg: m.sessions[m.listIdx].ID, back: wsOverlaySessions}
			m.overlay = wsOverlayConfirm
		case m.overlay == wsOverlayProfiles && m.listIdx < len(m.profiles):
			m.confirm = wsConfirm{kind: "delprofile", text: fmt.Sprintf("Delete profile %q? y/n", m.profiles[m.listIdx]), arg: m.profiles[m.listIdx], back: wsOverlayProfiles}
			m.overlay = wsOverlayConfirm
		}
	}
	return m, nil
}

func (m workspaceModel) listChoose() (tea.Model, tea.Cmd) {
	switch m.overlay {
	case wsOverlaySessions:
		if m.listIdx < len(m.sessions) {
			return m.loadSession(m.sessions[m.listIdx].ID)
		}
	case wsOverlayProjects:
		if m.listIdx >= len(m.projects) {
			return m.openText("path", ""), nil
		}
		p := m.projects[m.listIdx]
		switch {
		case p.missing:
			return m, nil
		case p.current:
			m.overlay = wsOverlayNone
			m.setNotice("Already in this project", false)
			return m, nil
		}
		return m.switchProject(p.path)
	case wsOverlayProfiles:
		if m.listIdx >= len(m.profiles) {
			return m.openText("profile", ""), nil
		}
		return m.applyProfile(m.profiles[m.listIdx])
	}
	return m, nil
}

// openText opens the one-line input: kind "path" (project directory) or
// "profile" (name to save the current roles under).
func (m workspaceModel) openText(kind, value string) workspaceModel {
	m.overlay = wsOverlayText
	m.textKind = kind
	m.textErr = ""
	m.textIn.SetValue(value)
	m.textIn.Placeholder = "~/path/to/project"
	if kind == "profile" {
		m.textIn.Placeholder = "profile_name"
	}
	m.textIn.Focus()
	return m
}

func (m workspaceModel) textKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = wsOverlayNone
		m.textIn.Blur()
		return m, nil
	case "enter":
		return m.submitText()
	}
	var cmd tea.Cmd
	m.textIn, cmd = m.textIn.Update(msg)
	m.textErr = ""
	return m, cmd
}

func (m workspaceModel) submitText() (tea.Model, tea.Cmd) {
	val := strings.TrimRight(m.textIn.Value(), "\r\n") // spaces in a path are meaningful
	if m.textKind == "profile" {
		val = strings.TrimSpace(val)
		if !config.ValidName(val) {
			m.textErr = "Name must be 1-32 characters: lowercase letters, digits, - and _, starting with a letter or digit"
			return m, nil
		}
		if err := m.deps.saveProfile(val, m.effectiveAgents()); err != nil {
			m.textErr = oneLine(err.Error())
			return m, nil
		}
		m.overlay = wsOverlayNone
		m.say("system", fmt.Sprintf("profile %q saved", val))
		return m, nil
	}
	path := val
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(m.root, path)
	}
	path = filepath.Clean(path)
	if val == "" {
		m.textErr = "Type the path of an existing directory"
		return m, nil
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		m.textErr = "Not a directory: " + path
		return m, nil
	}
	if path == filepath.Clean(m.root) {
		m.overlay = wsOverlayNone
		m.setNotice("Already in this project", false)
		return m, nil
	}
	return m.switchProject(path)
}

// wsListItem is one row of a list overlay.
type wsListItem struct {
	text string
	dim  bool
}

func (m workspaceModel) listBox() string {
	w, h := m.width, m.height
	switch m.overlay {
	case wsOverlaySessions:
		cur := m.sess.snapshot().ID
		var rows []wsListItem
		for _, s := range m.sessions {
			title := s.Title
			if title == "" {
				title = "(untitled)"
			}
			total := s.Todo + s.Running + s.Done + s.Failed
			t := fmt.Sprintf("%s · %d/%d done · %s", title, s.Done, total, agoText(m.now().Sub(s.UpdatedAt)))
			if s.ID == cur {
				t += "  (current)"
			}
			rows = append(rows, wsListItem{text: t})
		}
		return renderListBox("Resume a session", rows, m.listIdx, "no saved sessions in this project",
			"↑↓ move · Enter load · d delete · Esc close", w, h)
	case wsOverlayProjects:
		var rows []wsListItem
		for _, p := range m.projects {
			t := p.name
			switch {
			case p.current:
				t += "  (current)"
			case p.missing:
				t += "  (missing)"
			}
			t += " · " + p.path
			rows = append(rows, wsListItem{text: t, dim: p.missing})
		}
		rows = append(rows, wsListItem{text: "open path…"})
		return renderListBox("Open a project", rows, m.listIdx, "",
			"↑↓ move · Enter open · Esc close", w, h)
	case wsOverlayProfiles:
		var rows []wsListItem
		for _, p := range m.profiles {
			t := p
			if p == m.cfg.Project.Profile {
				t += "  (current)"
			}
			rows = append(rows, wsListItem{text: t})
		}
		rows = append(rows, wsListItem{text: "save current roles as…"})
		return renderListBox("Profiles", rows, m.listIdx, "",
			"↑↓ move · Enter apply · d delete · Esc close", w, h)
	case wsOverlayText:
		boxW := min(70, w-2)
		dim := lipgloss.NewStyle().Foreground(crt.dim)
		title, label := "Open a project", "Path of the project directory"
		if m.textKind == "profile" {
			title, label = "Save profile", "Name for the current roles"
		}
		body := []string{dim.Render(label), m.textIn.View()}
		if m.textErr != "" {
			body = append(body, "")
			for _, l := range wrapText("! "+m.textErr, boxW-4) {
				body = append(body, lipgloss.NewStyle().Foreground(crt.warn).Render(l))
			}
		}
		body = append(body, "", dim.Render("Enter confirm · Esc cancel"))
		return wsBox(title, boxW, body)
	case wsOverlayInfo:
		boxW := min(80, w-2)
		rows := max(min(len(m.infoText), h-6), 1)
		top := min(m.infoTop, max(len(m.infoText)-rows, 0))
		var body []string
		for i := top; i < top+rows && i < len(m.infoText); i++ {
			body = append(body, m.infoText[i])
		}
		foot := "Esc close"
		if len(m.infoText) > rows {
			foot = "↑↓ scroll · Esc close"
		}
		body = append(body, "", lipgloss.NewStyle().Foreground(crt.dim).Render(foot))
		return wsBox(m.infoName, boxW, body)
	}
	return ""
}

// renderListBox draws a selectable list. empty is shown when there are no
// rows at all.
func renderListBox(title string, rows []wsListItem, idx int, empty, footer string, width, height int) string {
	boxW := min(76, width-2)
	inner := boxW - 4
	vis := min(max(height-2-2-2, 1), len(rows))
	start := 0
	if idx >= vis {
		start = idx - vis + 1
	}
	var body []string
	if len(rows) == 0 && empty != "" {
		body = append(body, lipgloss.NewStyle().Foreground(crt.dim).Render(empty))
	}
	for i := start; i < start+vis && i < len(rows); i++ {
		sel := i == idx
		fg := crt.primary
		if rows[i].dim {
			fg = crt.dim
		}
		st := lipgloss.NewStyle().Foreground(fg)
		marker := "  "
		if sel {
			st = st.Foreground(crt.bright).Bold(true).Background(surfaceRaised)
			marker = "▸ "
		}
		row := st.Render(marker + ansi.Truncate(rows[i].text, inner-2, "…"))
		if pad := inner - ansi.StringWidth(row); pad > 0 {
			ps := lipgloss.NewStyle()
			if sel {
				ps = ps.Background(surfaceRaised)
			}
			row += ps.Render(strings.Repeat(" ", pad))
		}
		body = append(body, row)
	}
	body = append(body, "", lipgloss.NewStyle().Foreground(crt.dim).Render(footer))
	return wsBox(ansi.Truncate(title, boxW-6, "…"), boxW, body)
}

// ── /status and /cost ────────────────────────────────────────────────────────

func (m workspaceModel) openInfo(title string, lines []string) workspaceModel {
	m.overlay = wsOverlayInfo
	m.infoName = title
	m.infoText = lines
	m.infoTop = 0
	return m
}

func kv(k, v string) string { return padRight(k, 11) + v }

// listUserSkills names the skill files found in the project and home folders.
func listUserSkills(root string) []string {
	dirs := []string{filepath.Join(root, config.GlobalDir, "skills")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, config.GlobalDir, "skills"))
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".md")
			if name != e.Name() && e.Type().IsRegular() && config.ValidName(name) && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (m workspaceModel) cmdStatus(string) (tea.Model, tea.Cmd) {
	s := m.sess.snapshot()
	profile := m.cfg.Project.Profile
	if profile == "" {
		profile = "(none)"
	}
	title := s.Title
	if title == "" {
		title = "(untitled)"
	}
	todo, running, done, failed := m.g.Counts()
	lines := []string{
		kv("Project", m.root),
		kv("Language", orDefault(lang.Resolve(m.cfg.Project.Language, m.root), "(not detected)")),
		kv("Profile", profile),
		kv("Session", s.ID+" · "+title),
		kv("Nodes", fmt.Sprintf("%d: %d todo · %d running · %d done · %d failed", len(m.g.Nodes), todo, running, done, failed)),
		kv("Parallel", fmt.Sprintf("%d nodes at once", workspace.MaxParallel(m.cfg))),
		"",
		"Roles",
	}
	for _, r := range workspace.Roles(m.cfg) {
		meta := shortModel(r.Agent.Runner, r.Agent.Model)
		if meta == "" {
			meta = "(runner not set)"
		}
		if r.Agent.Effort != "" {
			meta += " / " + r.Agent.Effort
		}
		if r.ReadOnly {
			meta += " / read-only"
		}
		lines = append(lines, "  "+padRight(r.Name, 13)+meta)
	}
	lines = append(lines, "")
	if len(m.custom) == 0 {
		lines = append(lines, kv("Commands", "no custom commands"))
	} else {
		var names []string
		for _, c := range m.custom {
			names = append(names, fmt.Sprintf("/%s (%s)", c.Name, c.Source))
		}
		lines = append(lines, kv("Commands", strings.Join(names, ", ")))
	}
	if sk := listUserSkills(m.root); len(sk) == 0 {
		lines = append(lines, kv("Skills", "no skill files"))
	} else {
		lines = append(lines, kv("Skills", strings.Join(sk, ", ")))
	}
	return m.openInfo("Status", lines), nil
}

func (m workspaceModel) cmdCost(string) (tea.Model, tea.Cmd) {
	est := func(e bool) string {
		if e {
			return "~"
		}
		return ""
	}
	tok := func(mark string, n int) string { return mark + formatTokensCompact(n) }
	row := func(name, role, in, out string) string {
		return padRight(name, 8) + padRight(role, 14) + padRight(in, 10) + out
	}
	lines := []string{row("NODE", "ROLE", "IN", "OUT")}
	type sum struct{ in, out int }
	roles := map[string]*sum{}
	var order []string
	totIn, totOut := 0, 0
	anyEst := false
	for _, n := range m.g.Nodes {
		if n.InputTokens == 0 && n.OutputTokens == 0 {
			continue
		}
		mark := est(n.Estimated)
		anyEst = anyEst || n.Estimated
		role := n.Role
		if role == "" {
			role = "-"
		}
		lines = append(lines, row(n.ID, role, tok(mark, n.InputTokens), tok(mark, n.OutputTokens)))
		if roles[role] == nil {
			roles[role] = &sum{}
			order = append(order, role)
		}
		roles[role].in += n.InputTokens
		roles[role].out += n.OutputTokens
		totIn += n.InputTokens
		totOut += n.OutputTokens
	}
	if len(lines) == 1 {
		lines = append(lines, "no node has used tokens yet")
	}
	lines = append(lines, "", "By role")
	for _, r := range order {
		lines = append(lines, "  "+padRight(r, 20)+padRight(tok("", roles[r].in), 10)+tok("", roles[r].out))
	}
	s := m.sess.snapshot()
	lines = append(lines, "", row("Lead", "", tok("", s.In), tok("", s.Out)))
	mark := est(anyEst)
	lines = append(lines, row("Total", "", tok(mark, totIn+s.In), tok(mark, totOut+s.Out)))
	if strings.TrimSpace(s.Summary) != "" {
		lines = append(lines, "", "Nodes folded by /compact are not counted.")
	}
	if anyEst {
		lines = append(lines, "~ marks estimates")
	}
	return m.openInfo("Cost", lines), nil
}

// ── /export ──────────────────────────────────────────────────────────────────

func (m workspaceModel) cmdExport(string) (tea.Model, tea.Cmd) {
	s := m.sess.snapshot()
	var sb strings.Builder
	title := s.Title
	if title == "" {
		title = "Session " + s.ID
	}
	fmt.Fprintf(&sb, "# %s\n\n", title)
	fmt.Fprintf(&sb, "- Session: %s\n- Project: %s\n- Created: %s\n\n", s.ID, m.root, s.CreatedAt.Format(time.RFC3339))
	if sum := strings.TrimSpace(s.Summary); sum != "" {
		fmt.Fprintf(&sb, "## Summary\n\n%s\n\n", sum)
	}
	sb.WriteString("## Transcript\n\n")
	for _, l := range s.Transcript {
		fmt.Fprintf(&sb, "- **%s**: %s\n", l.Who, strings.ReplaceAll(strings.TrimSpace(l.Text), "\n", "\n  "))
	}
	sb.WriteString("\n## Nodes\n\n")
	if len(m.g.Nodes) == 0 {
		sb.WriteString("No nodes.\n")
	}
	for _, n := range m.g.Nodes {
		fmt.Fprintf(&sb, "### %s %s\n\n", n.ID, n.Title)
		role := n.Role
		if role == "" {
			role = "unassigned"
		}
		fmt.Fprintf(&sb, "- Role: %s\n- Status: %s\n", role, n.Status)
		if files := n.AllFiles(); len(files) > 0 {
			fmt.Fprintf(&sb, "- Files: %s\n", strings.Join(files, ", "))
		}
		if n.Summary != "" {
			fmt.Fprintf(&sb, "\n%s\n", strings.TrimSpace(n.Summary))
		}
		sb.WriteString("\n")
	}
	dir := filepath.Join(m.root, config.GlobalDir, "exports")
	rel := filepath.Join(config.GlobalDir, "exports", s.ID+".md")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		m.say("error", "could not write the export: "+oneLine(err.Error()))
		return m, nil
	}
	if err := os.WriteFile(filepath.Join(m.root, rel), []byte(sb.String()), 0o600); err != nil {
		m.say("error", "could not write the export: "+oneLine(err.Error()))
		return m, nil
	}
	m.say("system", "exported to "+rel)
	return m, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ── /plugin ──────────────────────────────────────────────────────────────────

// withPluginAgents returns c with the roles of the loaded plugins, which are
// never read from or written to a config file.
func (m workspaceModel) withPluginAgents(c config.Config) config.Config {
	c.PluginAgents = m.plug.AgentRoles()
	return c
}

// reloadPlugins rescans the plugin folders and refreshes the roles; nodes
// whose plugin role is gone lose their assignment.
func (m workspaceModel) reloadPlugins() workspaceModel {
	m.plug = m.deps.loadPlugins()
	m.cfg = m.withPluginAgents(m.cfg)
	if m.eng != nil {
		cfg := m.cfg
		if err := m.eng.Replace(func(g *workspace.Graph) { workspace.Reconcile(g, cfg) }); err != nil {
			m.say("error", oneLine(err.Error()))
		}
		m.g = m.eng.Snapshot()
	}
	return m
}

func (m workspaceModel) cmdPlugin(args string) (tea.Model, tea.Cmd) {
	f := strings.Fields(args)
	if len(f) == 0 {
		return m.openInfo("Plugins", m.pluginLines()), nil
	}
	if m.installing != "" && f[0] != "available" {
		m.say("error", "a plugin install is running; try again when it is done")
		return m, nil
	}
	switch {
	case f[0] == "available" && len(f) == 1:
		return m.openInfo("Plugin catalog", m.catalogLines()), nil
	case f[0] == "install" && len(f) == 2:
		return m.cmdPluginInstall(f[1])
	case f[0] == "reload" && len(f) == 1:
		if m.busy() {
			return m.refuseBusy()
		}
		m = m.reloadPlugins()
		m.say("system", fmt.Sprintf("plugins reloaded: %d found", len(m.plug.Plugins)))
		return m, nil
	case (f[0] == "enable" || f[0] == "disable") && len(f) == 2:
		if m.busy() { // roles would change under the running nodes
			return m.refuseBusy()
		}
		found := false
		for _, p := range m.plug.Plugins {
			found = found || p.Name == f[1]
		}
		if !found {
			m.say("error", fmt.Sprintf("no plugin named %q (see /plugin)", workspace.Sanitize(f[1])))
			return m, nil
		}
		on := f[0] == "enable"
		if err := plugins.SetEnabled(f[1], on); err != nil {
			m.say("error", oneLine(err.Error()))
			return m, nil
		}
		m = m.reloadPlugins()
		m.say("system", fmt.Sprintf("plugin %s %sd", f[1], f[0]))
		return m, nil
	}
	m.say("error", "usage: /plugin [available | install <name> | enable <name> | disable <name> | reload]")
	return m, nil
}

// pluginListWidth is the width the ids of a /plugin list wrap at, so a line
// fits the 80-column overlay after the 11-cell label.
const pluginListWidth = 60

// pluginLines is the text of the /plugin overlay.
func (m workspaceModel) pluginLines() []string {
	if len(m.plug.Plugins) == 0 {
		lines := []string{
			"No plugins found.",
			"",
			"Put a plugin folder (with .claude-plugin/plugin.json) in",
			"  " + filepath.Join(m.root, config.GlobalDir, "plugins") + " (this project) or",
			"  ~/" + config.GlobalDir + "/plugins (all projects).",
			"scripts/install-plugins.sh installs a recommended set;",
			"/plugin available lists the plugins decsr can install.",
		}
		if w := m.pluginWarnings(); len(w) > 0 {
			lines = append(append(lines, ""), w...)
		}
		return lines
	}
	// list wraps the ids under the label instead of cutting them off.
	list := func(label string, items []string) []string {
		if len(items) == 0 {
			return []string{kv("  "+label, "none")}
		}
		var out []string
		for i, l := range wrapText(strings.Join(items, ", "), pluginListWidth) {
			if i == 0 {
				out = append(out, kv("  "+label, l))
			} else {
				out = append(out, strings.Repeat(" ", 11)+l)
			}
		}
		return out
	}
	var lines []string
	for i, p := range m.plug.Plugins {
		if i > 0 {
			lines = append(lines, "")
		}
		state := "enabled"
		if !p.Enabled {
			state = "disabled"
		}
		head := p.Name
		if p.Version != "" {
			head += " " + p.Version
		}
		lines = append(lines, head+" · "+p.Scope+" · "+state)
		if p.Description != "" {
			lines = append(lines, "  "+p.Description)
		}
		if !p.Enabled {
			lines = append(lines, "  (not loaded; /plugin enable "+p.Name+")")
		} else {
			var cmds, skills, agents []string
			for _, c := range p.Commands {
				cmds = append(cmds, "/"+p.Name+":"+c.Name)
			}
			for _, s := range p.Skills {
				skills = append(skills, s.ID)
			}
			for _, a := range p.Agents {
				agents = append(agents, a.Role())
			}
			lines = append(lines, list("Commands", cmds)...)
			lines = append(lines, list("Skills", skills)...)
			lines = append(lines, list("Agents", agents)...)
		}
		if p.HasHooks || p.HasMCP {
			lines = append(lines, "  Note: hooks and MCP servers are not used by decsr")
		}
	}
	if w := m.pluginWarnings(); len(w) > 0 {
		lines = append(append(lines, ""), w...)
	}
	return lines
}

func (m workspaceModel) pluginWarnings() []string {
	var out []string
	for _, w := range m.plug.Warnings {
		out = append(out, "! "+workspace.Sanitize(w))
	}
	return out
}
