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
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const msgStopFirst = "stop the running nodes first"

// wsCommand is one slash command. Custom commands come from files.
type wsCommand struct {
	name    string
	aliases []string
	group   string   // Session | Run | Agents | Usage | Project | Plugin commands
	args    string   // shown after the name, e.g. "[args]"
	desc    string   // one line
	forms   []wsForm // the ways to call it, for /help <command>
	example string   // one example, for /help <command>
	hint    string   // argument hint from the command file, shown in the palette
	tag     string   // "(project)" | "(user)" | "(plugin:<name>)" for custom commands
	run     func(m workspaceModel, args string) (tea.Model, tea.Cmd)
}

// commandList is the registry: the built-in commands by group, then custom
// commands that do not shadow a built-in name or alias.
func (m workspaceModel) commandList() []wsCommand {
	cmds := builtinCommands()
	taken := map[string]bool{}
	for _, c := range cmds {
		taken[c.name] = true
		for _, a := range c.aliases {
			taken[a] = true
		}
	}
	for n := range removedCommands {
		taken[n] = true
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
		cmds = append(cmds, wsCommand{name: name, group: ui.GroupNamePlugin, args: "[args]", hint: cc.ArgHint, desc: desc, tag: "(" + cc.Source + ")",
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

// cmdMatches lists the commands that match what is typed after the slash,
// best first: names that start with it, then names that contain it, then names
// that hold its letters in order. A bare "/" lists every command, by group.
func (m workspaceModel) cmdMatches(prefix string) []wsCommand {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if !strings.HasPrefix(prefix, "/") {
		return nil
	}
	return rankCommands(m.commandList(), prefix)
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
	m.dropWorktrees(m.sess.snapshot().ID)
	m = m.resetSession(workspace.NewSession())
	m.refreshSaved()
	m.setNotice("New session started. The previous one is listed by /resume", false)
	return m
}

// dropWorktrees removes the worktrees of a session (not its branches).
func (m workspaceModel) dropWorktrees(session string) {
	if err := workspace.RemoveSessionWorktrees(m.root, session); err != nil {
		m.say("error", "could not remove the session worktrees: "+oneLine(err.Error()))
	}
}

// cmdMerge merges the session's integration branch into the checked-out branch.
func (m workspaceModel) cmdMerge(string) (tea.Model, tea.Cmd) {
	if m.busy() {
		return m.refuseBusy()
	}
	msg, err := workspace.MergeSession(m.root, m.sess.snapshot().ID)
	if err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	m.say("system", msg)
	return m, nil
}

func (m workspaceModel) cmdCompact(string) (tea.Model, tea.Cmd) {
	if m.busy() {
		return m.refuseBusy()
	}
	if m.eng == nil {
		m.say("system", "nothing to compact")
		return m, nil
	}
	folded, kept, err := m.applyCompact()
	if err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	if folded == 0 {
		if kept > 0 {
			m.say("system", fmt.Sprintf("nothing to compact; %d finished %s still needed by unfinished nodes", kept, pluralize(kept, "node is", "nodes are")))
		} else {
			m.say("system", "nothing to compact")
		}
		return m, nil
	}
	msg := fmt.Sprintf("compacted %d finished %s", folded, pluralize(folded, "node", "nodes"))
	if kept > 0 {
		msg += fmt.Sprintf("; kept %d that unfinished nodes still depend on", kept)
	}
	m.say("system", msg)
	return m, nil
}

// applyCompact folds the finished nodes into the session summary. It returns
// how many nodes were folded and how many finished ones stay because
// unfinished nodes depend on them. m.eng must not be nil.
func (m *workspaceModel) applyCompact() (folded, kept int, err error) {
	var gone []*workspace.Node
	if err := m.eng.Replace(func(g *workspace.Graph) {
		gone = g.Compact()
		for _, n := range g.Nodes {
			if n.Kind == workspace.KindWork && n.Status == workspace.StatusDone {
				kept++
			}
		}
	}); err != nil {
		return 0, 0, err
	}
	if len(gone) == 0 {
		return 0, kept, nil
	}
	m.sess.update(func(s *workspace.Session) { s.Summary = workspace.CompactSummary(s.Summary, gone) })
	m.g = m.eng.Snapshot()
	if m.g.Node(m.sel) == nil {
		m.sel = ""
		if len(m.g.Nodes) > 0 {
			m.sel = m.g.Nodes[0].ID
		}
	}
	m.saveSession()
	return len(gone), kept, nil
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
	m.dropWorktrees(id)
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
	m.panel.sel = min(m.panel.sel, len(m.profiles))
	m.overlay = wsOverlayPanel
	return m
}

// saveProfileAs saves the current roles as a profile and returns the problem,
// or "" when it is saved.
func (m workspaceModel) saveProfileAs(name string) string {
	if !config.ValidName(name) {
		return "Name must be 1-32 characters: lowercase letters, digits, - and _, starting with a letter or digit"
	}
	if err := m.deps.saveProfile(name, m.effectiveAgents(), config.ProfileRouting(m.cfg.Routing)); err != nil {
		return oneLine(err.Error())
	}
	return ""
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
		ac := config.AgentConfig{Runner: own.Runner, Model: own.Model, Effort: own.Effort, BaseURL: own.BaseURL, APIKeyEnv: own.APIKeyEnv, Mode: own.Mode, UserSkills: own.UserSkills}
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
		if errText := m.saveProfileAs(val); errText != "" {
			m.textErr = errText
			return m, nil
		}
		m.overlay = wsOverlayNone
		m.say("ok", fmt.Sprintf("profile %q saved", val))
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
		return renderListBox("Resume a session", ui.GroupSession, rows, m.listIdx, "no saved sessions in this project",
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
		return renderListBox("Open a project", ui.GroupProject, rows, m.listIdx, "",
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
		return renderListBox("Profiles", ui.GroupAgents, rows, m.listIdx, "",
			"↑↓ move · Enter apply · d delete · Esc close", w, h)
	case wsOverlayText:
		boxW := min(70, w-2)
		dim := lipgloss.NewStyle().Foreground(ui.Muted)
		title, label := "Open a project", "Path of the project directory"
		if m.textKind == "profile" {
			title, label = "Save profile", "Name for the current roles"
		}
		body := []string{dim.Render(label), m.textIn.View()}
		if m.textErr != "" {
			body = append(body, "")
			for _, l := range wrapText("! "+m.textErr, boxW-4) {
				body = append(body, lipgloss.NewStyle().Foreground(ui.Fail).Render(l))
			}
		}
		body = append(body, "", dim.Render("Enter confirm · Esc cancel"))
		return ui.Overlay(title, body, nil, boxW, ui.GroupSession)
	case wsOverlayInfo:
		return framed(m.infoName, m.infoColor, m.infoText, -1, m.infoTop, "↑↓ scroll · Esc close", w, h)
	}
	return ""
}

// renderListBox draws a selectable list. empty is shown when there are no
// rows at all.
func renderListBox(title string, color lipgloss.TerminalColor, rows []wsListItem, idx int, empty, footer string, width, height int) string {
	inner := ui.PanelInner(width)
	var body []string
	if len(rows) == 0 && empty != "" {
		body = append(body, lipgloss.NewStyle().Foreground(ui.Muted).Render(empty))
	}
	first := len(body)
	for i := range rows {
		sel := i == idx
		fg := ui.Text
		if rows[i].dim {
			fg = ui.Muted
		}
		st := lipgloss.NewStyle().Foreground(fg)
		marker := "  "
		if sel {
			st = st.Foreground(ui.Text).Bold(true).Background(ui.Sel)
			marker = "▸ "
		}
		row := st.Render(marker + ansi.Truncate(rows[i].text, inner-2, "…"))
		if pad := inner - ansi.StringWidth(row); pad > 0 {
			ps := lipgloss.NewStyle()
			if sel {
				ps = ps.Background(ui.Sel)
			}
			row += ps.Render(strings.Repeat(" ", pad))
		}
		body = append(body, row)
	}
	return framed(title, color, body, first+idx, 0, footer, width, height)
}

// ── /status and /cost ────────────────────────────────────────────────────────

func (m workspaceModel) openInfo(title string, color lipgloss.TerminalColor, lines []string) workspaceModel {
	m.overlay = wsOverlayInfo
	m.infoName = title
	m.infoColor = color
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

func (m workspaceModel) cmdStatus(string) (tea.Model, tea.Cmd) { return m.openStatus() }

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
	if len(m.sessRoles) > 0 {
		agents := make(map[string]config.AgentConfig, len(c.Agents)+len(m.sessRoles))
		for k, v := range m.sessRoles {
			agents[k] = v
		}
		for k, v := range c.Agents { // a saved role wins over the session one
			agents[k] = v
		}
		c.Agents = agents
	}
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

func (m workspaceModel) pluginWarnings() []string {
	var out []string
	for _, w := range m.plug.Warnings {
		out = append(out, "! "+workspace.Sanitize(w))
	}
	return out
}

// ── /connect ─────────────────────────────────────────────────────────────────

func (m workspaceModel) cmdConnect(args string) (tea.Model, tea.Cmd) {
	start := strings.ToLower(strings.TrimSpace(args))
	if start != "" && start != "custom" && !runner.IsPreset(start) {
		known := false
		for _, o := range connectOptions() {
			known = known || o.name == start
		}
		if !known {
			m.say("error", fmt.Sprintf("unknown provider %q (try deepseek, openai, openrouter, groq or custom)", start))
			return m, nil
		}
	}
	c := newConnectModel(start, true, m.deps.probeKey, m.deps.useProvider)
	m.conn = &c
	m.overlay = wsOverlayConnect
	return m, nil
}

func (m workspaceModel) connectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.conn == nil {
		m.overlay = wsOverlayNone
		return m, nil
	}
	c, cmd := m.conn.update(msg)
	m.conn = &c
	return m, cmd
}

func (m workspaceModel) onConnectMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case connectClosedMsg:
		m.conn, m.overlay = nil, wsOverlayNone
		return m, nil
	case connectSavedMsg:
		m.conn, m.overlay = nil, wsOverlayNone
		if msg.err != nil {
			m.say("error", oneLine(msg.err.Error()))
			return m, nil
		}
		m.say("system", connectSummary(msg))
		if msg.cfg != nil {
			m.cfg = m.withPluginAgents(*msg.cfg)
			if m.eng != nil {
				if err := m.eng.Replace(func(g *workspace.Graph) { workspace.Reconcile(g, m.cfg) }); err != nil {
					m.say("error", oneLine(err.Error()))
				}
				m.g = m.eng.Snapshot()
			}
			m.say("system", fmt.Sprintf("%s / %s now runs %s", msg.runner, msg.model, msg.scope))
		}
		return m, nil
	}
	if m.conn == nil {
		return m, nil
	}
	c, cmd := m.conn.update(msg)
	m.conn = &c
	return m, cmd
}
