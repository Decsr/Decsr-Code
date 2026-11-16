package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const issueUsage = "usage: /issue <number|URL|bead-id> [--force]"

// wsIssueMsg is the result of fetching the issue of /issue.
type wsIssueMsg struct {
	session string
	force   bool
	issue   workspace.Issue
	err     error
}

// parseIssueArgs splits the arguments of /issue into the ref and --force.
func parseIssueArgs(args string) (ref string, force bool, err error) {
	for _, f := range strings.Fields(args) {
		switch {
		case f == "--force" || f == "-f":
			force = true
		case ref == "":
			ref = f
		default:
			return "", false, fmt.Errorf("%s", issueUsage)
		}
	}
	if ref == "" {
		return "", false, fmt.Errorf("%s", issueUsage)
	}
	return ref, force, nil
}

// cmdIssue is /issue: fetch the issue, then send it to the lead as a task.
func (m workspaceModel) cmdIssue(args string) (tea.Model, tea.Cmd) {
	ref, force, err := parseIssueArgs(args)
	if err != nil {
		m.say("error", err.Error())
		return m, nil
	}
	if m.busy() || m.splitting {
		return m.refuseBusy()
	}
	sid := m.sess.snapshot().ID
	run, root := m.deps.ghRun, m.root
	m.setNotice("Fetching issue "+ref+"…", false)
	return m, func() tea.Msg {
		is, err := workspace.FetchIssue(context.Background(), root, ref, run)
		return wsIssueMsg{session: sid, force: force, issue: is, err: err}
	}
}

// startIssueCmd is the command that runs the issue given on the command line.
func (m workspaceModel) startIssueCmd() tea.Cmd {
	if m.startIssue == "" {
		return nil
	}
	_, cmd := m.cmdIssue(m.startIssue)
	return cmd
}

func (m workspaceModel) onIssueMsg(msg wsIssueMsg) (tea.Model, tea.Cmd) {
	if msg.session != m.sess.snapshot().ID {
		return m, nil
	}
	is := msg.issue
	if msg.err != nil {
		m.say("error", oneLine(msg.err.Error()))
		return m, nil
	}
	if is.Closed && !msg.force {
		m.say("error", fmt.Sprintf("%s is closed. Use /issue %s --force to start anyway", is.Summary(), strings.TrimPrefix(is.ID, "#")))
		return m, nil
	}
	if m.busy() || m.splitting {
		return m.refuseBusy()
	}
	m.sess.update(func(s *workspace.Session) { s.Issue = is.Ref() })
	return m.sendTask(is.Task(), is.Summary())
}
