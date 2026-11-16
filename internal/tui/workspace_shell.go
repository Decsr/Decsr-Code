package tui

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/executil"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsShellDoneMsg reports the end of a shell opened with "!".
type wsShellDoneMsg struct{ err error }

// shellCommand is the user's shell ($SHELL, else /bin/sh) started in dir.
func shellCommand(dir string, getenv func(string) string) *exec.Cmd {
	sh := getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	cmd := exec.Command(sh)
	cmd.Dir = dir
	return cmd
}

// tmuxSplit opens a shell in a side pane of the current tmux window.
func tmuxSplit(dir string) error {
	return executil.Command("tmux", "split-window", "-h", "-c", dir).Run()
}

// shellTarget picks where a shell for node n opens: the node's own worktree
// while it exists, else the integration worktree (the merged result), else the
// project root. where names the choice for the conversation line.
func (m workspaceModel) shellTarget(n *workspace.Node) (dir, where string) {
	if m.iso != nil {
		if d, ok := m.iso.NodeDir(n.ID); ok {
			return d, "the worktree of " + n.ID
		}
		if d, ok := m.iso.IntegrationDir(); ok {
			return d, "the integration worktree (merged result)"
		}
	}
	return m.root, "the project root"
}

// openShell opens a shell for node n. Under tmux it splits the window and the
// TUI keeps running; otherwise the TUI is suspended until the shell exits.
func (m *workspaceModel) openShell(n *workspace.Node) tea.Cmd {
	dir, where := m.shellTarget(n)
	getenv := m.deps.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv("TMUX") != "" {
		split := m.deps.tmuxSplit
		if split == nil {
			split = tmuxSplit
		}
		m.say("system", fmt.Sprintf("Opened a tmux split in %s: %s", where, dir))
		return func() tea.Msg { return wsShellDoneMsg{err: split(dir)} }
	}
	m.say("system", fmt.Sprintf("Opening a shell in %s: %s (exit to come back)", where, dir))
	return tea.ExecProcess(shellCommand(dir, getenv), func(err error) tea.Msg { return wsShellDoneMsg{err: err} })
}
