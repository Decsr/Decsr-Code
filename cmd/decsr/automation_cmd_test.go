package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/automation"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/tui"
)

type autoCrontab struct {
	content string
	writes  int
}

func (c *autoCrontab) List() (string, error) { return c.content, nil }
func (c *autoCrontab) Write(s string) error  { c.content = s; c.writes++; return nil }

type autoHarness struct {
	t      *testing.T
	d      autoDeps
	out    *bytes.Buffer
	ct     *autoCrontab
	runner *fakeRunner
	gh     [][]string
	ghOut  string
	ran    []tui.WorkspaceOptions
	result tui.AutomationResult
	runErr error
	notes  []string
	alive  bool
}

const autoYAML = `automations:
  - name: deps-bump
    schedule: "0 3 * * 1-5"
    task: "Update Go dependencies"
    autonomy: 3
    budget_usd: 2.5
  - name: triage
    schedule: "30 6 * * *"
    issue_query: "label:decsr is:open"
    autonomy: 2
    budget_usd: 1
    timeout: 1h
`

func newAutoHarness(t *testing.T, yaml string) *autoHarness {
	t.Helper()
	root := t.TempDir()
	if yaml != "" {
		if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(automation.ConfigPath(root), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := &autoHarness{t: t, out: &bytes.Buffer{}, ct: &autoCrontab{}, runner: &fakeRunner{}, alive: true,
		result: tui.AutomationResult{Finished: true, Result: "pr #12 checks passed", CostUSD: 0.42}}
	fk := newFake("")
	fk.d.runner = h.runner
	h.d = autoDeps{
		root: root, out: h.out,
		now:     func() time.Time { return time.Date(2026, 11, 16, 2, 0, 0, 0, time.Local) },
		goos:    "linux",
		crontab: h.ct,
		exe:     func() (string, error) { return "/opt/decsr", nil },
		path:    "/usr/bin:/bin",
		tmux:    fk.d,
		ghRun: func(_ context.Context, _ string, name string, args ...string) (string, error) {
			h.gh = append(h.gh, append([]string{name}, args...))
			return h.ghOut, nil
		},
		pid:         os.Getpid(),
		alive:       func(int) bool { return h.alive },
		isTTY:       func() bool { return true },
		in:          strings.NewReader(""),
		authMode:    func() string { return runner.AuthAPIKey },
		claudeRoles: func(string) []string { return nil },
		runWorkspace: func(_ string, o tui.WorkspaceOptions) (tui.AutomationResult, error) {
			h.ran = append(h.ran, o)
			return h.result, h.runErr
		},
		notify: func(_ string, _ notify.Level, body string) { h.notes = append(h.notes, body) },
	}
	return h
}

func (h *autoHarness) logLines(name string) []string {
	b, err := os.ReadFile(automation.LogPath(h.d.root, name))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestAutomationUsage(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	for _, args := range [][]string{nil, {"bogus"}, {"run"}, {"run", "--tmux"}, {"run", "a", "b"}, {"install", "a", "b"}} {
		if err := automationMain(h.d, args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestRunRefusesInvalidConfig(t *testing.T) {
	for name, yaml := range map[string]string{
		"autonomy 1": "automations:\n  - name: a\n    schedule: \"0 3 * * *\"\n    task: x\n    autonomy: 1\n    budget_usd: 1\n",
		"no budget":  "automations:\n  - name: a\n    schedule: \"0 3 * * *\"\n    task: x\n    autonomy: 2\n",
	} {
		h := newAutoHarness(t, yaml)
		err := automationMain(h.d, []string{"run", "a"})
		if err == nil || len(h.ran) != 0 {
			t.Errorf("%s: err %v, ran %d", name, err, len(h.ran))
		}
	}
	h := newAutoHarness(t, autoYAML)
	if err := automationMain(h.d, []string{"run", "nope"}); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("unknown name: %v", err)
	}
	h = newAutoHarness(t, "")
	if err := automationMain(h.d, []string{"run", "a"}); !errors.Is(err, automation.ErrNoConfig) {
		t.Errorf("no config: %v", err)
	}
}

func TestRunNeedsTerminal(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.d.isTTY = func() bool { return false }
	if err := automationMain(h.d, []string{"run", "deps-bump"}); err == nil || !strings.Contains(err.Error(), "--tmux") {
		t.Errorf("err = %v", err)
	}
	if len(h.ran) != 0 {
		t.Error("ran without a terminal")
	}
}

func TestRunTaskLogsAndReleasesLock(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	if err := automationMain(h.d, []string{"run", "deps-bump"}); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 1 {
		t.Fatalf("ran %d times", len(h.ran))
	}
	o := h.ran[0]
	if o.Issue != "" || o.Automation.Name != "deps-bump" || o.Automation.Task != "Update Go dependencies" ||
		o.Automation.Autonomy != 3 || o.Automation.BudgetUSD != 2.5 || o.Automation.Timeout != 2*time.Hour {
		t.Errorf("options %+v", o)
	}
	lines := h.logLines("deps-bump")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "cost=$0.42 result=pr #12 checks passed") || !strings.HasPrefix(lines[0], "start=2026-11-16T02:00:00") {
		t.Errorf("log %q", lines)
	}
	if _, err := os.Stat(automation.LockPath(h.d.root, "deps-bump")); !os.IsNotExist(err) {
		t.Errorf("lock left behind: %v", err)
	}
}

func TestRunInterruptedAndFailed(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.result = tui.AutomationResult{}
	_ = automationMain(h.d, []string{"run", "deps-bump"})
	h.runErr = errors.New("boom\nline")
	_ = automationMain(h.d, []string{"run", "deps-bump"})
	lines := h.logLines("deps-bump")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "result=interrupted") || !strings.HasSuffix(lines[1], "result=failed: boom line") {
		t.Errorf("log %q", lines)
	}
}

func TestRunSkippedWhenLocked(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	l := h.d.locker(2 * time.Hour)
	release, ok, err := l.Acquire("deps-bump")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	defer release()
	for _, args := range [][]string{{"run", "deps-bump"}, {"run", "deps-bump", "--tmux"}} {
		if err := automationMain(h.d, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if len(h.ran) != 0 {
		t.Error("ran although locked")
	}
	for _, c := range h.runner.calls {
		if c[1] == "new-session" {
			t.Error("created a tmux session although locked")
		}
	}
	lines := h.logLines("deps-bump")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "result=skipped: previous run still active") {
		t.Errorf("log %q", lines)
	}
}

func TestRunReplacesStaleLock(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	l := h.d.locker(2 * time.Hour)
	if _, ok, _ := l.Acquire("deps-bump"); !ok {
		t.Fatal("lock")
	}
	h.alive = false
	if err := automationMain(h.d, []string{"run", "deps-bump"}); err != nil || len(h.ran) != 1 {
		t.Fatalf("err %v ran %d", err, len(h.ran))
	}
}

func TestRunTmuxCreatesDetachedSession(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	if err := automationMain(h.d, []string{"run", "deps-bump", "--tmux"}); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 0 {
		t.Error("--tmux must not run the workspace itself")
	}
	var create []string
	for _, c := range h.runner.calls {
		switch c[1] {
		case "new-session":
			create = c
		case "attach-session", "switch-client":
			t.Errorf("attached: %v", c)
		}
	}
	if create == nil {
		t.Fatalf("no new-session call: %v", h.runner.calls)
	}
	project, _ := filepath.Abs(h.d.root)
	wantName := autoSessionName(project, "deps-bump")
	if !strings.HasPrefix(wantName, "decsr-auto-") || !strings.HasSuffix(wantName, "-deps-bump") {
		t.Errorf("session name %q", wantName)
	}
	joined := strings.Join(create, " ")
	for _, want := range []string{"-d", "-s " + wantName, "-c " + project, "DECSR_TMUX=1 'env' 'PATH=/usr/bin:/bin' '/opt/decsr' 'automation' 'run' 'deps-bump'"} {
		if !strings.Contains(joined, want) {
			t.Errorf("new-session misses %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--tmux") {
		t.Errorf("the inner command must not carry --tmux: %s", joined)
	}
}

func TestRunTmuxSkipsExistingSession(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	project, _ := filepath.Abs(h.d.root)
	h.runner.listed = autoSessionName(project, "deps-bump") + "\t0\t0\t" + project + "\n"
	if err := automationMain(h.d, []string{"run", "deps-bump", "--tmux"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.runner.calls {
		if c[1] == "new-session" {
			t.Error("second session created")
		}
	}
	if lines := h.logLines("deps-bump"); len(lines) != 1 || !strings.Contains(lines[0], "skipped") {
		t.Errorf("log %q", lines)
	}
}

func TestRunTmuxNeedsTmux(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.d.tmux.lookPath = func(string) (string, error) { return "", errors.New("no") }
	if err := automationMain(h.d, []string{"run", "deps-bump", "--tmux"}); err == nil || !strings.Contains(err.Error(), "tmux is not installed") {
		t.Errorf("err = %v", err)
	}
}

func TestRunIssueQueryPicksOldest(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.ghOut = `[{"number":17}]`
	if err := automationMain(h.d, []string{"run", "triage"}); err != nil {
		t.Fatal(err)
	}
	if len(h.gh) != 1 {
		t.Fatalf("gh calls %v", h.gh)
	}
	got := strings.Join(h.gh[0], " ")
	for _, want := range []string{"gh issue list", "--state open", "label:decsr is:open sort:created-asc", "--json number", "--limit 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("gh call misses %q: %s", want, got)
		}
	}
	if len(h.ran) != 1 || h.ran[0].Issue != "17" || h.ran[0].Automation.Task != "" || h.ran[0].Automation.Autonomy != 2 || h.ran[0].Automation.Timeout != time.Hour {
		t.Errorf("ran %+v", h.ran)
	}
}

func TestRunIssueQueryNoMatchIsNothingToDo(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.ghOut = `[]`
	if err := automationMain(h.d, []string{"run", "triage"}); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 0 {
		t.Error("ran without an issue")
	}
	if lines := h.logLines("triage"); len(lines) != 1 || !strings.HasSuffix(lines[0], "cost=$0.00 result=nothing to do") {
		t.Errorf("log %q", lines)
	}
	if len(h.notes) != 1 || h.notes[0] != "automation triage: nothing to do" {
		t.Errorf("notes %q", h.notes)
	}
}

func TestRunIssueQueryGHFailure(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.d.ghRun = func(context.Context, string, string, ...string) (string, error) {
		return "", errors.New("gh: not logged in")
	}
	if err := automationMain(h.d, []string{"run", "triage"}); err != nil {
		t.Fatal(err)
	}
	lines := h.logLines("triage")
	if len(lines) != 1 || !strings.Contains(lines[0], "result=failed: gh issue list: gh: not logged in") || len(h.ran) != 0 {
		t.Errorf("log %q ran %d", lines, len(h.ran))
	}
}

func TestInstallAndUninstall(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.ct.content = "0 1 * * * /usr/bin/backup\n"
	if err := automationMain(h.d, []string{"install"}); err != nil {
		t.Fatal(err)
	}
	project, _ := filepath.Abs(h.d.root)
	wantA := "0 3 * * 1-5 cd '" + project + "' && PATH='/usr/bin:/bin' '/opt/decsr' automation run deps-bump --tmux >> '" +
		project + "/.decsr/automations/deps-bump.log' 2>&1 # decsr:" + project + ":deps-bump"
	if !strings.Contains(h.ct.content, wantA+"\n") || !strings.Contains(h.out.String(), wantA) {
		t.Errorf("crontab:\n%s\noutput:\n%s", h.ct.content, h.out)
	}
	if !strings.HasPrefix(h.ct.content, "0 1 * * * /usr/bin/backup\n") || strings.Count(h.ct.content, "# decsr:") != 2 {
		t.Errorf("crontab:\n%s", h.ct.content)
	}
	before, writes := h.ct.content, h.ct.writes
	if err := automationMain(h.d, []string{"install"}); err != nil || h.ct.content != before || h.ct.writes != writes {
		t.Errorf("not idempotent: %v\n%s", err, h.ct.content)
	}
	if err := automationMain(h.d, []string{"uninstall", "triage"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.ct.content, ":triage") || !strings.Contains(h.ct.content, ":deps-bump") {
		t.Errorf("crontab after uninstall triage:\n%s", h.ct.content)
	}
	if err := automationMain(h.d, []string{"uninstall"}); err != nil {
		t.Fatal(err)
	}
	if h.ct.content != "0 1 * * * /usr/bin/backup\n" {
		t.Errorf("crontab after uninstall: %q", h.ct.content)
	}
	h.out.Reset()
	if err := automationMain(h.d, []string{"uninstall"}); err != nil || !strings.Contains(h.out.String(), "no decsr crontab lines") {
		t.Errorf("empty uninstall: %v %q", err, h.out)
	}
}

func TestInstallOneAndErrors(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	if err := automationMain(h.d, []string{"install", "triage"}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(h.ct.content, "# decsr:") != 1 || !strings.Contains(h.ct.content, "30 6 * * *") {
		t.Errorf("crontab:\n%s", h.ct.content)
	}
	if err := automationMain(h.d, []string{"install", "nope"}); err == nil {
		t.Error("unknown name installed")
	}
	h.d.goos = "windows"
	if err := automationMain(h.d, []string{"install"}); err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Errorf("windows install: %v", err)
	}
	if err := automationMain(h.d, []string{"uninstall"}); err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Errorf("windows uninstall: %v", err)
	}
	bad := newAutoHarness(t, "automations:\n  - name: a\n")
	if err := automationMain(bad.d, []string{"install"}); err == nil || bad.ct.writes != 0 {
		t.Errorf("invalid config installed: %v", err)
	}
}

func TestList(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	if err := automationMain(h.d, []string{"install", "deps-bump"}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC)
	if err := automation.AppendLog(h.d.root, "deps-bump", automation.Entry{Start: start, End: start.Add(time.Minute), Result: "nothing to do"}); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	if err := automationMain(h.d, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if len(rows) != 3 {
		t.Fatalf("output:\n%s", h.out)
	}
	for _, want := range []string{"NAME", "NEXT RUN", "LAST RESULT"} {
		if !strings.Contains(rows[0], want) {
			t.Errorf("header misses %q: %s", want, rows[0])
		}
	}
	// now is Mon 2026-11-16 02:00 local: 0 3 * * 1-5 next fires at 03:00 the same day.
	a := strings.Fields(rows[1])
	if a[0] != "deps-bump" || !strings.Contains(rows[1], "2026-11-16 03:00") || !strings.Contains(rows[1], "L3") ||
		!strings.Contains(rows[1], "$2.50") || !strings.Contains(rows[1], "yes") || !strings.Contains(rows[1], "nothing to do") ||
		!strings.Contains(rows[1], "task: Update Go dependencies") {
		t.Errorf("row 1: %s", rows[1])
	}
	if !strings.Contains(rows[2], "triage") || !strings.Contains(rows[2], "2026-11-16 06:30") || !strings.Contains(rows[2], "issue: label:decsr") ||
		!strings.Contains(rows[2], "no") || !strings.HasSuffix(strings.TrimSpace(rows[2]), "-") {
		t.Errorf("row 2: %s", rows[2])
	}
}

func TestListWithoutConfig(t *testing.T) {
	h := newAutoHarness(t, "")
	if err := automationMain(h.d, []string{"list"}); err != nil || !strings.Contains(h.out.String(), "no automations") {
		t.Errorf("%v %q", err, h.out)
	}
}

func TestAutomationDoctor(t *testing.T) {
	h := newAutoHarness(t, "")
	if r := automationDoctor(h.d.root, h.ct); r != nil {
		t.Errorf("line without a config: %+v", r)
	}
	h = newAutoHarness(t, "automations:\n  - name: a\n")
	if r := automationDoctor(h.d.root, h.ct); r == nil || r.Status == 0 {
		t.Errorf("invalid file: %+v", r)
	}
	h = newAutoHarness(t, autoYAML)
	exe := filepath.Join(t.TempDir(), "decsr")
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.d.exe = func() (string, error) { return exe, nil }
	if err := automationMain(h.d, []string{"install"}); err != nil {
		t.Fatal(err)
	}
	r := automationDoctor(h.d.root, h.ct)
	if r == nil || r.Status != 0 || !strings.Contains(r.Detail, "2 defined, 2 installed") {
		t.Errorf("healthy: %+v", r)
	}
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	r = automationDoctor(h.d.root, h.ct)
	if r == nil || r.Status == 0 || !strings.Contains(r.Detail, "binary missing") {
		t.Errorf("missing binary: %+v", r)
	}
}

func TestRunLogsNoChecksResult(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.result = tui.AutomationResult{Finished: true, Result: "pr #9 opened (no checks)", CostUSD: 1}
	if err := automationMain(h.d, []string{"run", "deps-bump"}); err != nil {
		t.Fatal(err)
	}
	if lines := h.logLines("deps-bump"); len(lines) != 1 || !strings.HasSuffix(lines[0], "cost=$1.00 result=pr #9 opened (no checks)") {
		t.Errorf("log %q", lines)
	}
}
