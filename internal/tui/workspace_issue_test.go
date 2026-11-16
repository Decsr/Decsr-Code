package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const wsIssueJSON = `{"number":42,"title":"Fix flaky login test","body":"It fails on CI.","url":"https://github.com/o/r/issues/42","state":"%s","labels":[{"name":"bug"}]}`

func issueGH(state string) func(context.Context, string, string, ...string) (string, error) {
	return func(_ context.Context, _ string, name string, args ...string) (string, error) {
		if name == "gh" && len(args) > 1 && args[0] == "issue" && args[1] == "view" {
			return strings.Replace(wsIssueJSON, "%s", state, 1), nil
		}
		return "", errors.New("unexpected " + name + " " + strings.Join(args, " "))
	}
}

// issueMsg runs the fetch command of /issue and returns its message.
func issueMsg(t *testing.T, cmd tea.Cmd) wsIssueMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no fetch command")
	}
	msg, ok := cmd().(wsIssueMsg)
	if !ok {
		t.Fatalf("not an issue message")
	}
	return msg
}

func TestWorkspaceIssueStartsTask(t *testing.T) {
	h := newWSHarness(t)
	h.m.deps.ghRun = issueGH("OPEN")
	cmd := h.command("/issue 42")
	h.send(issueMsg(t, cmd))
	if !h.m.splitting {
		t.Fatalf("the task was not submitted:\n%s", h.view())
	}
	h.finishSplit()
	want := "Issue #42: Fix flaky login test\n\nIt fails on CI.\n\nLabels: bug"
	if len(h.tasks) != 1 || h.tasks[0] != want {
		t.Fatalf("tasks = %q", h.tasks)
	}
	s := h.m.sess.snapshot()
	if s.Issue == nil || s.Issue.ID != "#42" || s.Issue.Title != "Fix flaky login test" || s.Issue.URL == "" {
		t.Fatalf("session issue = %+v", s.Issue)
	}
	found := false
	for _, l := range s.Transcript {
		found = found || (l.Who == "you" && l.Text == "Issue #42: Fix flaky login test")
	}
	if !found {
		t.Fatalf("no summary line in %+v", s.Transcript)
	}
}

func TestWorkspaceIssueClosedNeedsForce(t *testing.T) {
	h := newWSHarness(t)
	h.m.deps.ghRun = issueGH("CLOSED")
	h.send(issueMsg(t, h.command("/issue #42")))
	if h.m.splitting || h.m.sess.snapshot().Issue != nil {
		t.Fatal("a closed issue must not start")
	}
	h.want("is closed")
	h.send(issueMsg(t, h.command("/issue 42 --force")))
	if !h.m.splitting {
		t.Fatal("--force must start a closed issue")
	}
}

func TestWorkspaceIssueRefusedWhileRunning(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.deps.ghRun = issueGH("OPEN")
	h.key("A", "R")
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusRunning })
	cmd := h.command("/issue 42")
	if cmd != nil {
		t.Fatal("a fetch started while a node runs")
	}
	h.want(msgStopFirst)
}

func TestWorkspaceIssueFetchError(t *testing.T) {
	h := newWSHarness(t)
	h.m.deps.ghRun = func(context.Context, string, string, ...string) (string, error) {
		return "", errors.New("gh: Could not resolve to an issue")
	}
	h.send(issueMsg(t, h.command("/issue 9")))
	h.want("not found")
	if h.m.splitting {
		t.Fatal("an error must not start a task")
	}
}

func TestWorkspaceIssueUsageAndCommandList(t *testing.T) {
	h := newWSHarness(t)
	if cmd := h.command("/issue"); cmd != nil {
		t.Fatal("no fetch without a ref")
	}
	h.want("usage: /issue")
	if _, ok := h.m.lookup("/issue"); !ok {
		t.Fatal("/issue is not registered")
	}
}

func TestWorkspaceStartIssueOption(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := newWSHarnessAt(t, t.TempDir())
	h.m.deps.ghRun = issueGH("OPEN")
	h.m.startIssue = "42"
	h.send(issueMsg(t, h.m.Init()))
	if !h.m.splitting {
		t.Fatal("the start issue was not submitted")
	}
}
