package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// deniedCommands are the Bash commands the runner refused in the node's
// current attempt, each once.
func deniedCommands(n *workspace.Node) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range n.Denied {
		if d.Tool == "Bash" && d.Command != "" && !seen[d.Command] {
			seen[d.Command] = true
			out = append(out, d.Command)
		}
	}
	return out
}

// deniedPrograms are the programs of the refused commands that the project
// and the node do not already allow, each once.
func (m workspaceModel) deniedPrograms(n *workspace.Node, cmds []string) []string {
	have := map[string]bool{}
	for _, p := range m.cfg.Project.AllowedCommands {
		have[strings.TrimSpace(p)] = true
	}
	for _, p := range n.ExtraAllowed {
		have[p] = true
	}
	var out []string
	for _, c := range cmds {
		for _, p := range runner.CommandPrograms(c) {
			if !have[p] {
				have[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// nodeReadOnly is true when the role of the node never writes.
func (m workspaceModel) nodeReadOnly(n *workspace.Node) bool {
	r, ok := workspace.FindRole(m.cfg, n.Role)
	return ok && r.ReadOnly
}

// deniedCard is the card for the commands refused in the node's last attempt;
// ok is false when there is nothing the user can allow.
func (m workspaceModel) deniedCard(n *workspace.Node) (askCard, bool) {
	switch n.Status {
	case workspace.StatusDone, workspace.StatusWaiting, workspace.StatusFailed:
	default:
		return askCard{}, false
	}
	cmds := deniedCommands(n)
	if len(cmds) == 0 || m.nodeReadOnly(n) {
		return askCard{}, false
	}
	progs := m.deniedPrograms(n, cmds)
	if len(progs) == 0 {
		return askCard{}, false
	}
	quoted := make([]string, len(progs))
	for i, p := range progs {
		quoted[i] = "`" + p + "`"
	}
	c := askCard{
		Key: fmt.Sprintf("deny:%s:%d", n.ID, n.AttemptNumber()), Node: n.ID, Warn: true, Rec: 0,
		Head:  fmt.Sprintf("⚠ %s was refused %d %s", n.ID, len(cmds), pluralize(len(cmds), "command", "commands")),
		Progs: progs, Cmds: cmds,
		Opts: []askOpt{
			{Label: "Allow for " + n.ID + " and retry", Act: askActAllow},
			{Label: "Always allow " + strings.Join(quoted, ", ") + " in this project and retry", Act: askActAlways},
			{Label: "No", Act: askActNo},
		},
	}
	for i, cmd := range cmds {
		if i == 3 {
			c.Body = append(c.Body, fmt.Sprintf("… and %d more", len(cmds)-3))
			break
		}
		c.Body = append(c.Body, oneLine(workspace.SanitizeText(cmd)))
	}
	return c, true
}

// sayRefusal tells in the chat about the refused calls of a node that no card
// covers: tools other than Bash, and commands of a read-only role.
func (m *workspaceModel) sayRefusal(n *workspace.Node) {
	if len(n.Denied) == 0 {
		return
	}
	var other []string
	seen := map[string]bool{}
	for _, d := range n.Denied {
		if d.Tool != "Bash" && !seen[d.Tool] {
			seen[d.Tool] = true
			other = append(other, d.Tool)
		}
	}
	if len(other) > 0 {
		m.say("system", n.ID+" was refused "+strings.Join(other, ", ")+": the runner did not allow it")
	}
	if cmds := deniedCommands(n); len(cmds) > 0 && m.nodeReadOnly(n) {
		m.say("system", fmt.Sprintf("%s was refused %d %s: its role is read-only, so nothing is allowed", n.ID, len(cmds), pluralize(len(cmds), "command", "commands")))
	}
}

// allowAndRetry allows the programs of card c for its node (and in the
// project when always is set), then replies so the node runs again.
func (m workspaceModel) allowAndRetry(c askCard, always bool) (tea.Model, tea.Cmd) {
	if m.eng == nil {
		return m, nil
	}
	if always {
		for _, p := range c.Progs {
			if err := config.SaveProjectAllowedCommand(m.root, p); err != nil {
				m.say("error", "project.yaml was not saved: "+oneLine(err.Error()))
				return m, nil
			}
			m.cfg.Project.AllowedCommands = append(m.cfg.Project.AllowedCommands, p)
		}
		m.say("system", "allowed in this project: "+strings.Join(c.Progs, ", "))
	}
	if err := m.eng.AllowCommands(c.Node, c.Progs); err != nil {
		m.say("error", err.Error())
		return m, nil
	}
	m.g = m.eng.Snapshot()
	quoted := make([]string, len(c.Cmds))
	for i, cmd := range c.Cmds {
		quoted[i] = "`" + cmd + "`"
	}
	msg := fmt.Sprintf("The command %s is now allowed. Run it and continue.", quoted[0])
	if len(quoted) > 1 {
		msg = fmt.Sprintf("The commands %s are now allowed. Run them and continue.", strings.Join(quoted, ", "))
	}
	return m.replyKeepingDraft(c.Node, msg)
}
