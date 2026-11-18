package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// newRoleOf is the role a node waits to create: the one the lead suggested
// when it is not defined and the node has no role. "" for any other node.
func (m workspaceModel) newRoleOf(n *workspace.Node) string {
	if n.Role == "" && n.SuggestedRole != "" && !m.defined(n.SuggestedRole) {
		return n.SuggestedRole
	}
	return ""
}

// readOnlyNode reports whether a node only reads: the lead marked its task
// read-only, or its role cannot write.
func (m workspaceModel) readOnlyNode(n *workspace.Node) bool {
	if n.ReadOnlyTask {
		return true
	}
	r, ok := workspace.FindRole(m.cfg, orDefault(n.Role, n.SuggestedRole))
	return ok && r.ReadOnly
}

// addSessionRole defines a role for this session only. It has no agent of its
// own, so it uses the pm's.
func (m workspaceModel) addSessionRole(name, desc string) workspaceModel {
	ac := config.AgentConfig{Description: workspace.Sanitize(desc)}
	m.sessRoles[name] = ac
	m.cfg = m.withPluginAgents(m.cfg)
	if m.deps.sessionRole != nil {
		m.deps.sessionRole(name, ac)
	}
	return m
}

// adoptNewRoles creates, for this session, the roles the pending nodes wait
// for and gives them to those nodes. It is what y does about a new role.
func (m workspaceModel) adoptNewRoles() workspaceModel {
	var made []string
	for _, n := range m.eng.PendingApproval() {
		name := m.newRoleOf(n)
		if name == "" {
			continue
		}
		if !m.defined(name) {
			desc := ""
			for _, nr := range m.eng.Snapshot().NewRoles {
				if nr.Name == name {
					desc = nr.Description
				}
			}
			m = m.addSessionRole(name, desc)
			made = append(made, name)
		}
		_ = m.eng.AdoptRole(n.ID, name)
	}
	if len(made) > 0 {
		m.say("system", fmt.Sprintf("created %s for this session; /roles save <name> keeps a role in project.yaml", idList(made)))
	}
	return m
}

// cmdRolesSave is /roles save <name>: it writes a role of this session to
// project.yaml.
func (m workspaceModel) cmdRolesSave(args string) (tea.Model, tea.Cmd) {
	verb, name, _ := strings.Cut(strings.TrimSpace(args), " ")
	name = strings.TrimSpace(name)
	if verb != "save" || name == "" {
		m.say("error", "usage: /roles [save <name>]")
		return m, nil
	}
	ac, ok := m.sessRoles[name]
	if !ok {
		m.say("error", fmt.Sprintf("%q is not a role of this session", name))
		return m, nil
	}
	cfg, err := m.deps.saveRole(name, ac)
	if err != nil {
		m.say("error", "could not save the role: "+oneLine(err.Error()))
		return m, nil
	}
	delete(m.sessRoles, name)
	m.cfg = m.withPluginAgents(cfg)
	m.say("system", fmt.Sprintf("role %q saved in project.yaml", name))
	return m, nil
}

// newWorkNodes are the work nodes of g that are not in before.
func newWorkNodes(g *workspace.Graph, before map[string]bool) []*workspace.Node {
	var out []*workspace.Node
	for _, n := range g.Nodes {
		if n.Kind == workspace.KindWork && !before[n.ID] {
			out = append(out, n)
		}
	}
	return out
}

// startReadOnlyPlan approves and starts a split whose work nodes all have
// writes false, without the plan card. It does nothing when a node writes or
// has no defined role (a new role needs the card), in an automation run, when
// automation acts (those have their own approval rules) or when the budget is
// reached.
func (m workspaceModel) startReadOnlyPlan(fresh []*workspace.Node) (workspaceModel, tea.Cmd) {
	if m.eng == nil || len(fresh) == 0 || m.auto != nil || m.autoActive() != autoManual {
		return m, nil
	}
	ids := make([]string, 0, len(fresh))
	for _, n := range fresh {
		if !n.ReadOnlyTask || n.Role == "" {
			return m, nil
		}
		ids = append(ids, n.ID)
	}
	if m.budgetReached() {
		m.say("system", "budget reached, the read-only plan was not started")
		return m, nil
	}
	m.eng.ApproveNodes(ids...)
	m.g = m.eng.Snapshot()
	m.say("system", "read-only plan: started without approval")
	next, cmd := m.runAll()
	return asWorkspace(next, cmd, m)
}

// cmdCheck is /check: it runs Build & test now. The node is added when the
// graph has none and reset to todo otherwise; the gate on changed files does
// not apply.
func (m workspaceModel) cmdCheck(string) (tea.Model, tea.Cmd) {
	if m.eng == nil || !m.hasNodes() {
		m.say("error", "nothing to check yet: send a task first")
		return m, nil
	}
	if m.installing != "" {
		m.say("error", "wait for the plugin install to finish, then run /check")
		return m, nil
	}
	var cmds []string
	if m.deps.checkCmds != nil {
		cmds = m.deps.checkCmds()
	}
	if err := m.eng.QueueCheck(cmds); err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	m.autoPaused = false
	m.g = m.eng.Snapshot()
	if err := m.eng.Run(m.runCtx, "CHECK"); err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	m.g = m.eng.Snapshot()
	m.setNotice("Started Build & test", false)
	return m, m.startTick()
}
