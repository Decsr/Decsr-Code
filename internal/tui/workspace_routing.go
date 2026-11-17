package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/ranking"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// routingMode is the routing mode of the session: the one frozen at its first
// split, else the configured one.
func (m workspaceModel) routingMode() string {
	if s := m.sess.snapshot().RoutingMode; s != "" {
		return config.NormalizeRoutingMode(s)
	}
	return m.cfg.Routing.EffectiveMode()
}

// freezeRoutingMode saves the routing mode in the session at the first split
// and returns it. Later splits keep it, so a resumed session keeps its picks.
func (m workspaceModel) freezeRoutingMode() string {
	mode := m.routingMode()
	m.sess.update(func(s *workspace.Session) {
		if s.RoutingMode == "" {
			s.RoutingMode = mode
		}
	})
	return mode
}

// proposeAgents gives the new nodes their proposed agent, marked pending
// until the user approves (or an unattended run does).
func (m workspaceModel) proposeAgents(ids []string) workspaceModel {
	if m.eng == nil || len(ids) == 0 {
		return m
	}
	cfg := m.cfg
	r := &workspace.Router{
		Mode:      m.routingMode(),
		Connected: m.routingConnected(),
		Role: func(name string) (config.AgentConfig, bool) {
			role, ok := workspace.FindRole(cfg, name)
			return role.Agent, ok
		},
	}
	if r.Mode != config.RoutingManual {
		if t, err := ranking.Load(); err == nil {
			r.Table = t.With(ranking.FromConfig(cfg))
		} else {
			r.Table = ranking.Table{}.With(ranking.FromConfig(cfg))
		}
	}
	m.eng.SetRouter(r)
	m.eng.Propose(ids...)
	m.g = m.eng.Snapshot()
	if p := len(m.g.PendingApproval()); p > 0 && !(m.autoActive() != autoManual && m.cfg.Routing.ApproveUnattended()) {
		m.say("system", fmt.Sprintf("%d %s wait for approval of their agent (y approve · e edit · n cancel)", p, pluralize(p, "node", "nodes")))
	}
	return m
}

// routingConnected is the set of runners routing may pick. An unattended
// automation run on a claude subscription login without allow_subscription
// never gets claude; the approval card editor of an interactive session is not
// limited by this.
func (m workspaceModel) routingConnected() map[string]bool {
	conn := ranking.Connected()
	if m.auto != nil && !m.auto.opts.AllowSubscription && claudeAuthMode() == runner.AuthSubscription {
		delete(conn, "claude")
	}
	return conn
}

// autoApproveAgents is the unattended approval: it approves the proposed
// agents unless routing.auto_approve is false. wait is true when it left them
// pending and the run must not go on.
func (m workspaceModel) autoApproveAgents() (_ workspaceModel, wait bool) {
	p := len(m.g.PendingApproval())
	if p == 0 {
		return m, false
	}
	if !m.cfg.Routing.ApproveUnattended() {
		m.autoSay(fmt.Sprintf("%d %s wait for the approval of their agent (routing.auto_approve is off)", p, pluralize(p, "node", "nodes")))
		m.notifyNote(notify.NeedsYou, "The agents of the split need your approval")
		return m, true
	}
	n := m.eng.ApproveAssignments()
	m.g = m.eng.Snapshot()
	m.autoSay(fmt.Sprintf("approved the agent of %d %s", n, pluralize(n, "node", "nodes")))
	return m, false
}

const routingUsage = "usage: /routing [auto-connected|auto-role|manual]"

// routingSource says where the routing mode in force comes from.
func (m workspaceModel) routingSource() string {
	if m.sess.snapshot().RoutingMode != "" {
		return "this session"
	}
	if proj := config.LoadProject(m.root).Routing.Mode; proj != "" {
		if name := m.cfg.Project.Profile; name != "" {
			if p, err := config.LoadProfile(name); err == nil && p.Routing != nil && config.NormalizeRoutingMode(p.Routing.Mode) == config.NormalizeRoutingMode(proj) {
				return "profile " + name
			}
		}
		return "project"
	}
	return "default"
}

// cmdRouting is /routing [mode]: show the mode, or set it for this session
// and save it in project.yaml.
func (m workspaceModel) cmdRouting(args string) (tea.Model, tea.Cmd) {
	arg := strings.ToLower(strings.TrimSpace(args))
	if arg == "" {
		m.say("system", fmt.Sprintf("Routing mode is %s (from %s). auto-connected picks any connected runner, auto-role only the role's own, manual keeps each role's agent. /routing <mode> changes it for this session and the project.", m.routingMode(), m.routingSource()))
		return m, nil
	}
	switch arg {
	case config.RoutingManual, config.RoutingAutoRole, config.RoutingAutoConnected:
	default:
		m.say("error", routingUsage)
		return m, nil
	}
	m.cfg.Routing.Mode = arg
	m.sess.update(func(s *workspace.Session) { s.RoutingMode = arg })
	if err := config.SaveProjectRoutingMode(m.root, arg); err != nil {
		m.say("error", "routing set for this session, but project.yaml was not saved: "+oneLine(err.Error()))
		m.saveSession()
		return m, nil
	}
	m.saveSession()
	m.say("system", fmt.Sprintf("Routing mode is %s for this session and saved in project.yaml. It applies to the next split.", arg))
	return m, nil
}
