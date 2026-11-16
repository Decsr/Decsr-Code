package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	importUsage = "usage: /import speckit [--phase N] [path]"
	// specManyTasks is the size above which the import hints at --phase.
	specManyTasks = 40
)

// parseImportArgs splits "speckit [--phase N] [path]".
func parseImportArgs(args string) (phase int, path string, err error) {
	f := strings.Fields(args)
	if len(f) == 0 || f[0] != "speckit" {
		return 0, "", fmt.Errorf("%s", importUsage)
	}
	f = f[1:]
	for i := 0; i < len(f); i++ {
		switch {
		case f[i] == "--phase" && i+1 < len(f):
			n, e := strconv.Atoi(f[i+1])
			if e != nil || n < 0 {
				return 0, "", fmt.Errorf("%s", importUsage)
			}
			phase = n
			i++
		case path == "" && !strings.HasPrefix(f[i], "--"):
			path = f[i]
		default:
			return 0, "", fmt.Errorf("%s", importUsage)
		}
	}
	return phase, path, nil
}

// cmdImport is /import speckit: add the open tasks of a Spec Kit tasks.md to
// the graph.
func (m workspaceModel) cmdImport(args string) (tea.Model, tea.Cmd) {
	phase, arg, err := parseImportArgs(args)
	if err != nil {
		m.say("error", err.Error())
		return m, nil
	}
	if m.busy() || m.splitting {
		return m.refuseBusy()
	}
	tasks, err := workspace.FindSpecTasks(m.root, arg)
	if err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	data, err := os.ReadFile(tasks)
	if err != nil {
		m.say("error", oneLine(err.Error()))
		return m, nil
	}
	rel := m.relPath(tasks)
	dir := filepath.Dir(tasks)
	spec, _ := os.ReadFile(filepath.Join(dir, "spec.md"))
	_, hasQA := workspace.FindRole(m.cfg, "qa")
	res := workspace.BuildSpecGraph(workspace.ParseSpecKit(string(data)), workspace.SpecImport{
		Feature: workspace.SpecFeatureName(string(spec), filepath.Base(dir)),
		Dir:     filepath.ToSlash(m.relPath(dir)),
		Tasks:   filepath.ToSlash(rel),
		Phase:   phase,
		HasQA:   hasQA,
	})
	if res.Imported == 0 {
		m.say("error", "no open tasks to import from "+rel)
		return m, nil
	}
	if arg == "" {
		m.say("system", "Using the most recently modified tasks.md: "+rel)
	}
	featDir := filepath.ToSlash(m.relPath(dir))
	warnUntracked := m.worktreesLikely() && !filepath.IsAbs(featDir)
	var untracked []string
	if warnUntracked {
		untracked = workspace.SpecUntracked(m.root, featDir)
	}
	var next tea.Model
	var cmd tea.Cmd
	if m.eng == nil || !m.hasNodes() {
		next, cmd = m.openGraph(res.Graph)
	} else {
		next, cmd = m.appendGraph(res.Graph)
	}
	nm, ok := next.(workspaceModel)
	if !ok {
		return next, cmd
	}
	nm.say("system", fmt.Sprintf("Imported %d %s from %s (%d done %s skipped, %d %s). Assign roles with A, then R to run.",
		res.Imported, pluralize(res.Imported, "task", "tasks"), filepath.ToSlash(rel),
		res.Skipped, pluralize(res.Skipped, "task", "tasks"), res.Phases, pluralize(res.Phases, "phase", "phases")))
	if len(untracked) > 0 {
		nm.say("system", fmt.Sprintf("%s is not committed: agents in worktrees can't read spec.md/plan.md — run git add %s && git commit, then import again", featDir, featDir))
	}
	nm.rescanSpec()
	if res.Imported > specManyTasks && phase == 0 {
		nm.say("system", "That is a lot of nodes. /import speckit --phase N imports one phase at a time.")
	}
	if nm.autoActive() == autoManual {
		nm.notifySplitDone()
	}
	nm, autoCmd := nm.autoAfterGraph()
	nm.saveSession()
	return nm, tea.Batch(cmd, autoCmd)
}

// worktreesLikely is true when this session's nodes run, or will run, in
// their own worktrees.
func (m workspaceModel) worktreesLikely() bool {
	if m.iso != nil {
		return true
	}
	return m.eng == nil && m.deps.isolator != nil && m.deps.isolator(m.sess.snapshot().ID) != nil
}

// relPath is p relative to the project root, or p itself when outside it.
func (m workspaceModel) relPath(p string) string {
	if r, err := filepath.Rel(m.root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}
