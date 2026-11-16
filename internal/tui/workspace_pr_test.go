package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func prGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// prGH answers gh from a script and runs git for real.
type prGH struct {
	mu      sync.Mutex
	calls   []string
	checks  string
	fixSHA  string
	created bool
}

func (g *prGH) run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" {
		return workspace.ExecRunner(ctx, dir, name, args...)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	line := strings.Join(args, " ")
	g.calls = append(g.calls, line)
	switch {
	case strings.HasPrefix(line, "auth status"):
		return "ok", nil
	case strings.HasPrefix(line, "pr view decsr/"):
		return "", errors.New("no pull requests found")
	case strings.HasPrefix(line, "pr create"):
		g.created = true
		return "https://github.com/o/r/pull/9\n", nil
	case strings.HasPrefix(line, "pr view 9"):
		return `{"headRefOid":"` + g.fixSHA + `"}`, nil
	case strings.HasPrefix(line, "pr checks"):
		return g.checks, nil
	case strings.HasPrefix(line, "run view"):
		return "FAIL: TestThing\nexit status 1\n", nil
	case strings.HasPrefix(line, "api"):
		return "[]", nil
	}
	return "", errors.New("unexpected gh " + line)
}

func (g *prGH) count(prefix string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// prMsgs runs cmd and returns the wsPRMsg it produces, looking into batches.
// Other commands (ticks, event waits) are left to time out.
func prMsgs(cmd tea.Cmd) []wsPRMsg {
	if cmd == nil {
		return nil
	}
	var mu sync.Mutex
	var out []wsPRMsg
	var wg sync.WaitGroup
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case wsPRMsg:
			mu.Lock()
			out = append(out, msg)
			mu.Unlock()
		case tea.BatchMsg:
			for _, sub := range msg {
				wg.Add(1)
				go func() { defer wg.Done(); run(sub) }()
			}
		}
	}
	done := make(chan struct{})
	go func() { run(cmd); wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(700 * time.Millisecond):
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]wsPRMsg(nil), out...)
}

// prHarness is a Workspace on a real repository with one merged node, a bare
// origin and a fake gh.
func prHarness(t *testing.T) (*wsHarness, *prGH, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	repo, origin := t.TempDir(), t.TempDir()
	prGit(t, origin, "init", "-q", "--bare")
	prGit(t, repo, "init", "-q")
	prGit(t, repo, "config", "user.name", "tester")
	prGit(t, repo, "config", "user.email", "t@example.com")
	prGit(t, repo, "remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prGit(t, repo, "add", "-A")
	prGit(t, repo, "commit", "-q", "-m", "init")

	h := newWSHarnessAt(t, repo)
	gh := &prGH{checks: `[{"name":"build","state":"FAILURE","bucket":"fail","link":"https://github.com/o/r/actions/runs/55/job/1"}]`, fixSHA: "sha1"}
	h.m.deps.ghRun = gh.run
	h.toGraph()
	sid := h.m.sess.snapshot().ID
	iso := workspace.NewIsolator(repo, sid, "")
	if iso == nil {
		t.Fatal("no isolator")
	}
	h.m.iso = iso
	mergeNode(t, iso, "T1", "c.go")
	return h, gh, repo, origin
}

func mergeNode(t *testing.T, iso *workspace.Isolator, id, file string) {
	t.Helper()
	root, _, err := iso.Start(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, file), []byte("package c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := iso.Finish(id, "node "+id); err != nil {
		t.Fatal(err)
	}
}

func (h *wsHarness) command(line string) tea.Cmd {
	h.t.Helper()
	h.m.screen = wsScreenMain
	h.m.setFocus(wsFocusPrompt)
	h.m.input.SetValue(line)
	return h.key("enter")
}

func (h *wsHarness) feed(cmd tea.Cmd, kind string) tea.Cmd {
	h.t.Helper()
	for _, msg := range prMsgs(cmd) {
		if msg.kind == kind {
			return h.send(msg)
		}
	}
	h.t.Fatalf("no %q message from the command; view:\n%s", kind, h.view())
	return nil
}

func TestPRCommandNeedsIsolation(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	if cmd := h.command("/pr"); len(prMsgs(cmd)) != 0 {
		t.Error("/pr without isolation must not start anything")
	}
	h.want("needs worktree isolation")
	h.command("/pr bogus")
	h.want("usage: /pr [watch|stop]")
	h.command("/pr stop")
	h.want("Not watching")
}

func TestPRCommandCreatesPR(t *testing.T) {
	h, gh, _, origin := prHarness(t)
	sid := h.m.sess.snapshot().ID
	h.feed(h.command("/pr"), "ensured")
	h.want("Pull request #9 created: https://github.com/o/r/pull/9")
	if h.m.pr.watching {
		t.Error("/pr alone must not watch")
	}
	pr := h.m.sess.snapshot().PR
	if pr == nil || pr.Number != 9 || !strings.HasSuffix(pr.URL, "/pull/9") {
		t.Fatalf("persisted PR = %+v", pr)
	}
	if got := prGit(t, origin, "rev-parse", "--verify", "refs/heads/"+workspace.SessionBranch(sid)); got == "" {
		t.Error("branch not pushed")
	}
	if gh.count("pr create") != 1 {
		t.Errorf("calls: %v", gh.calls)
	}
}

func TestPRWatchFixNodeAndAutoPush(t *testing.T) {
	h, gh, repo, origin := prHarness(t)
	sid := h.m.sess.snapshot().ID
	branch := workspace.SessionBranch(sid)

	pollCmd := h.feed(h.command("/pr watch"), "ensured")
	if !h.m.pr.watching {
		t.Fatal("/pr watch must start watching")
	}
	h.want("Watching the pull request every 60 s")
	h.feed(pollCmd, "poll")
	n := h.m.g.Node("F1")
	if n == nil || n.Title != "Fix CI: build" || !strings.Contains(n.Description, "FAIL: TestThing") {
		t.Fatalf("fix node = %+v", n)
	}
	if n.Role != "coder_fixer" {
		t.Errorf("role = %q", n.Role)
	}
	h.want("Added F1: press r to run it.")
	if h.status("F1") != workspace.StatusTodo {
		t.Error("the fix node must not run by itself")
	}
	if pr := h.m.sess.snapshot().PR; pr.CIFixes != 1 || pr.FixedSHA != "sha1" {
		t.Errorf("state = %+v", pr)
	}

	// The tick polls again; the same head adds nothing.
	tick := wsPRMsg{kind: "tick", gen: h.m.pr.gen, session: sid}
	h.feed(h.send(tick), "poll")
	if h.m.g.Node("F2") != nil {
		t.Error("one fix node per head commit")
	}

	// The fix node finishes and merges: the branch is pushed again.
	mergeNode(t, h.m.iso, "F1", "fix.go")
	cmd := h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "F1", Type: workspace.EventDone}})
	h.feed(cmd, "pushed")
	h.want("Pushed the fix of F1")
	if prGit(t, origin, "rev-parse", branch) != prGit(t, repo, "rev-parse", branch) {
		t.Error("origin does not hold the fix")
	}

	// Stopping drops the loop and every message of it.
	h.command("/pr stop")
	if h.m.pr.watching {
		t.Fatal("still watching")
	}
	if cmd := h.send(tick); cmd != nil {
		t.Error("a tick of a stopped loop must be dropped")
	}
	cmd = h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "F1", Type: workspace.EventDone}})
	if len(prMsgs(cmd)) != 0 {
		t.Error("no push while not watching")
	}
	_ = gh
}

func TestPRWatchAutoFixRunsTheNode(t *testing.T) {
	h, _, _, _ := prHarness(t)
	h.m.cfg.Workspace.PRAutoFix = true
	pollCmd := h.feed(h.command("/pr watch"), "ensured")
	h.feed(pollCmd, "poll")
	h.want("Added F1 and started it.")
	h.pump(func(m workspaceModel) bool { return m.g.Node("F1").Status == workspace.StatusRunning })
	h.exec.release("F1", nil)
}

func TestPRWatchToggleAndSessionSwitch(t *testing.T) {
	h, _, _, _ := prHarness(t)
	h.feed(h.command("/pr watch"), "ensured")
	if !h.m.pr.watching {
		t.Fatal("not watching")
	}
	h.command("/pr watch")
	if h.m.pr.watching {
		t.Error("/pr watch again must stop")
	}
	h.feed(h.command("/pr watch"), "ensured")
	old := wsPRMsg{kind: "tick", gen: h.m.pr.gen, session: h.m.sess.snapshot().ID}
	h.m = h.m.resetSession(workspace.NewSession())
	if h.m.pr.watching {
		t.Error("a new session must not keep watching")
	}
	if cmd := h.send(old); cmd != nil {
		t.Error("a tick of the old session must be dropped")
	}
}
