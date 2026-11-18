package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ranking"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	modelTabRoles = iota
	modelTabRank
	modelTabProfiles
)

// ── open ─────────────────────────────────────────────────────────────────────

func (m workspaceModel) openModel(tab int) workspaceModel {
	m = m.openPanel(panelModel, tab)
	if tab == modelTabProfiles {
		m.profiles = m.deps.listProfiles()
	}
	return m
}

// cmdRank opens the Rankings tab and fetches the rankings from Artificial
// Analysis with the saved key, off the UI thread.
func (m workspaceModel) cmdRank(string) (tea.Model, tea.Cmd) {
	m = m.openModel(modelTabRank)
	return m.fetchRank()
}

func (m workspaceModel) fetchRank() (tea.Model, tea.Cmd) {
	if m.rankBusy {
		return m, nil
	}
	m.rankBusy = true
	m.rankLines = nil
	settings := ranking.FromConfig(m.cfg)
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return wsRankMsg{rep: ranking.Run(ctx, ranking.RunOptions{Settings: settings})}
	}
}

func (m workspaceModel) onRank(msg wsRankMsg) (tea.Model, tea.Cmd) {
	m.rankBusy = false
	m.rankLines = msg.rep.Lines()
	if msg.rep.Err != nil && !(m.overlay == wsOverlayPanel && m.panel.kind == panelModel) {
		m.setNotice("! rank: "+msg.rep.Err.Error(), true)
	}
	return m, nil
}

// ── roles ────────────────────────────────────────────────────────────────────

// roleAgent is the agent of a role as the panel shows it: the pending edit
// when there is one.
func (m workspaceModel) roleAgent(r workspace.Role) config.AgentConfig {
	if ac, ok := m.panel.edit[r.Name]; ok {
		return ac
	}
	return r.Agent
}

// editableRoles are the roles whose agent the panel can change.
func (m workspaceModel) panelRoles() []workspace.Role { return workspace.Roles(m.cfg) }

func (m workspaceModel) runnerOptions(cur string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	add(orDefault(cur, "opencode"))
	conn := ranking.Connected()
	for _, r := range ranking.RankableRunners {
		if conn[r] {
			add(r)
		}
	}
	for _, r := range localRunners {
		add(r)
	}
	return out
}

// cycleAgent moves one field of ac to the next or previous option. A new
// runner starts on its best model; a new model drops an effort it lacks.
func (m workspaceModel) cycleAgent(ac config.AgentConfig, field, dir int) config.AgentConfig {
	rn := orDefault(ac.Runner, "opencode")
	switch field {
	case apprFieldRunner:
		next := cycle(m.runnerOptions(ac.Runner), rn, dir)
		if next != rn {
			ac.Runner, ac.Model, ac.Effort = next, "", ""
			if opts := m.modelOptions(next, nil); len(opts) > 0 {
				ac.Model = opts[0]
			}
		}
	case apprFieldModel:
		ac.Model = cycle(m.modelOptions(rn, nil), ac.Model, dir)
		if !containsString(effortOptions(rn, ac.Model), ac.Effort) {
			ac.Effort = ""
		}
	case apprFieldEffort:
		ac.Effort = cycle(effortOptions(rn, ac.Model), ac.Effort, dir)
	}
	return ac
}

func (m workspaceModel) modelKey(k string) (workspaceModel, tea.Cmd, bool) {
	switch m.panel.ps.Tab {
	case modelTabRoles:
		return m.rolesTabKey(k)
	case modelTabRank:
		if k == "r" {
			next, cmd := m.fetchRank()
			return next.(workspaceModel), cmd, true
		}
	case modelTabProfiles:
		return m.profilesTabKey(k)
	}
	return m, nil, false
}

func (m workspaceModel) rolesTabKey(k string) (workspaceModel, tea.Cmd, bool) {
	roles := m.panelRoles()
	if len(roles) == 0 {
		return m, nil, false
	}
	m.panel.sel = min(max(m.panel.sel, 0), len(roles)-1)
	r := roles[m.panel.sel]
	if k == "s" {
		return m.saveRoleEdits(), nil, true
	}
	if m.panel.editing {
		switch k {
		case "left", "h":
			m = m.editRole(r, -1)
		case "right", "l":
			m = m.editRole(r, 1)
		case "tab", "down", "j":
			m.panel.field = (m.panel.field + 1) % apprFields
		case "shift+tab", "up", "k":
			m.panel.field = (m.panel.field + apprFields - 1) % apprFields
		case "enter", "esc":
			m.panel.editing = false
		}
		return m, nil, true
	}
	switch k {
	case "up", "k":
		m.panel.sel = max(m.panel.sel-1, 0)
	case "down", "j":
		m.panel.sel = min(m.panel.sel+1, len(roles)-1)
	case "enter":
		if r.Plugin {
			m.panel.note, m.panel.noteKind = r.Name+" comes from a plugin: its agent is not edited here", ui.ResultWarn
			return m, nil, true
		}
		m.panel.editing, m.panel.field, m.panel.note = true, 0, ""
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m workspaceModel) editRole(r workspace.Role, dir int) workspaceModel {
	if m.panel.edit == nil {
		m.panel.edit = map[string]config.AgentConfig{}
	}
	m.panel.edit[r.Name] = m.cycleAgent(m.roleAgent(r), m.panel.field, dir)
	return m
}

// saveRoleEdits writes the edited roles to project.yaml.
func (m workspaceModel) saveRoleEdits() workspaceModel {
	if len(m.panel.edit) == 0 {
		m.panel.note, m.panel.noteKind = "nothing changed", ui.ResultInfo
		return m
	}
	if m.busy() { // roles would change under the running nodes
		m.panel.note, m.panel.noteKind = msgStopFirst, ui.ResultError
		return m
	}
	names := make([]string, 0, len(m.panel.edit))
	for n := range m.panel.edit {
		names = append(names, n)
	}
	sort.Strings(names)
	save := m.deps.saveRole
	if save == nil {
		save = func(name string, ac config.AgentConfig) (config.Config, error) {
			if err := config.SaveProjectRole(m.root, name, ac); err != nil {
				return config.Config{}, err
			}
			return config.Load(m.root)
		}
	}
	for _, name := range names {
		e := m.panel.edit[name]
		own := m.cfg.Agents[name]
		own.Runner, own.Model, own.Effort = e.Runner, e.Model, e.Effort
		c, err := save(name, own)
		if err != nil {
			m.panel.note, m.panel.noteKind = "not saved: "+oneLine(err.Error()), ui.ResultError
			return m
		}
		m.cfg = m.withPluginAgents(c)
	}
	if m.eng != nil {
		if err := m.eng.Replace(func(g *workspace.Graph) { workspace.Reconcile(g, m.cfg) }); err != nil {
			m.panel.note, m.panel.noteKind = oneLine(err.Error()), ui.ResultError
			return m
		}
		m.g = m.eng.Snapshot()
	}
	m.panel.note, m.panel.noteKind = fmt.Sprintf("%d %s saved in project.yaml", len(names), pluralize(len(names), "role", "roles")), ui.ResultOK
	m.panel.edit, m.panel.editing = nil, false
	return m
}

// rolesBody lists the roles with their runner, model and effort. The field
// being edited is shown between arrows.
func (m workspaceModel) rolesBody(o *ui.PanelOpts) {
	roles := m.panelRoles()
	hc := lipgloss.NewStyle().Foreground(o.Color).Bold(true)
	o.Body = []string{"  " + hc.Render(padRight("ROLE", 13)+padRight("RUNNER", 11)+padRight("MODEL", 26)+padRight("EFFORT", 9))}
	for i, r := range roles {
		ac := m.roleAgent(r)
		sel := i == m.panel.sel
		field := func(idx int, text string, w int) string {
			if sel && m.panel.editing && m.panel.field == idx {
				return lipgloss.NewStyle().Foreground(o.Color).Bold(true).Render(padRight("‹"+truncateOneLine(text, w-2)+"›", w))
			}
			return padRight(truncateOneLine(text, w-1), w)
		}
		model := ac.Model
		if model == "" {
			model = "(default)"
		}
		effort := ac.Effort
		if effort == "" {
			effort = "default"
		}
		rn := orDefault(ac.Runner, "(not set)")
		name := lipgloss.NewStyle().Foreground(ui.RoleColor(r.Name)).Bold(sel).Render(padRight(r.Name, 13))
		runnerCell := lipgloss.NewStyle().Foreground(ui.RunnerColor(ac.Runner)).Render(field(apprFieldRunner, rn, 11))
		if sel && m.panel.editing && m.panel.field == apprFieldRunner {
			runnerCell = field(apprFieldRunner, rn, 11)
		}
		modelCell := plainText(field(apprFieldModel, model, 26))
		if sel && m.panel.editing && m.panel.field == apprFieldModel {
			modelCell = field(apprFieldModel, model, 26)
		}
		effortCell := dimText(field(apprFieldEffort, effort, 9))
		if sel && m.panel.editing && m.panel.field == apprFieldEffort {
			effortCell = field(apprFieldEffort, effort, 9)
		}
		flags := ""
		if _, dirty := m.panel.edit[r.Name]; dirty {
			flags += " *"
		}
		if r.ReadOnly {
			flags += " read-only"
		}
		if r.Plugin {
			flags += " plugin"
		}
		o.Body = append(o.Body, selMark(sel, o.Color)+name+runnerCell+modelCell+effortCell+dimText(flags))
	}
	if len(m.panel.edit) > 0 {
		o.Body = append(o.Body, "", dimText("* changed, not saved yet: s saves it in project.yaml"))
	}
	followSel(o, m.panel.sel+1)
}

// ── profiles ─────────────────────────────────────────────────────────────────

func (m workspaceModel) profilesTabKey(k string) (workspaceModel, tea.Cmd, bool) {
	n := len(m.profiles) + 1
	switch k {
	case "up", "k":
		m.panel.sel = max(m.panel.sel-1, 0)
	case "down", "j":
		m.panel.sel = min(m.panel.sel+1, n-1)
	case "enter":
		if m.panel.sel >= len(m.profiles) {
			return m.openText("profile", ""), nil, true
		}
		next, cmd := m.applyProfile(m.profiles[m.panel.sel])
		return next.(workspaceModel), cmd, true
	case "d":
		if m.panel.sel < len(m.profiles) {
			name := m.profiles[m.panel.sel]
			m.confirm = wsConfirm{kind: "delprofile", text: fmt.Sprintf("Delete profile %q? y/n", name), arg: name, back: wsOverlayPanel}
			m.overlay = wsOverlayConfirm
		}
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m workspaceModel) profilesBody(o *ui.PanelOpts) {
	for i, p := range m.profiles {
		sel := i == m.panel.sel
		t := plainText(p)
		if p == m.cfg.Project.Profile {
			t += dimText("  (current)")
		}
		o.Body = append(o.Body, selMark(sel, o.Color)+t)
	}
	sel := m.panel.sel >= len(m.profiles)
	o.Body = append(o.Body, selMark(sel, o.Color)+dimText("save current roles as…"))
	followSel(o, m.panel.sel)
}

// ── panel ────────────────────────────────────────────────────────────────────

func (m workspaceModel) modelOpts(o *ui.PanelOpts) {
	o.Title = "Model"
	inner := m.panelInner(o.Width)
	switch m.panel.ps.Tab {
	case modelTabRoles:
		keys := []ui.Hint{{Key: "↑↓", Label: "move"}, {Key: "Enter", Label: "edit"}, {Key: "s", Label: "save"}}
		if m.panel.editing {
			keys = []ui.Hint{{Key: "←→", Label: "change"}, {Key: "Tab", Label: "field"}, {Key: "s", Label: "save"}, {Key: "Enter", Label: "done"}}
			o.Keys = keys
		} else {
			o.Keys = hintsFor(true, keys...)
		}
		m.rolesBody(o)
	case modelTabRank:
		o.Keys = hintsFor(true, ui.Hint{Key: "r", Label: "fetch again"})
		switch {
		case m.rankBusy:
			o.Body = []string{dimText("Fetching the rankings from Artificial Analysis…"), "", dimText(ranking.Attribution)}
		case len(m.rankLines) == 0:
			o.Body = []string{dimText("r fetches the rankings from Artificial Analysis (needs a key: /provider add artificialanalysis)")}
		default:
			for _, l := range m.rankLines {
				o.Body = append(o.Body, clipRow(l, inner))
			}
		}
	case modelTabProfiles:
		o.Keys = hintsFor(true, ui.Hint{Key: "Enter", Label: "apply"}, ui.Hint{Key: "d", Label: "delete"})
		m.profilesBody(o)
	}
}
