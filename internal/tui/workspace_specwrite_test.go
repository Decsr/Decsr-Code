package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func specFeatureAt(t *testing.T, root, name, tasks string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(root, "specs", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(p, []byte(tasks), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceSpecDetect(t *testing.T) {
	now := time.Now()
	t.Run("none", func(t *testing.T) {
		h := newWSHarness(t)
		h.m.rescanSpec()
		if h.m.spec != nil || h.m.suggestion() != "" {
			t.Fatalf("spec = %+v", h.m.spec)
		}
	})
	t.Run("one", func(t *testing.T) {
		h := newWSHarness(t)
		specFeatureAt(t, h.root, "003-login", importTasks, now)
		h.m.rescanSpec()
		if h.m.spec == nil || h.m.spec.Dir != "specs/003-login" || h.m.spec.Open != 4 {
			t.Fatalf("spec = %+v", h.m.spec)
		}
		if got := h.m.suggestion(); got != "/import speckit" {
			t.Fatalf("suggestion = %q", got)
		}
		if v := h.view(); !strings.Contains(v, "Spec Kit: specs/003-login has 4 open tasks — /import speckit") {
			t.Fatalf("empty state:\n%s", v)
		}
	})
	t.Run("newest of several", func(t *testing.T) {
		h := newWSHarness(t)
		specFeatureAt(t, h.root, "001-old", "- [ ] T001 a\n", now.Add(-time.Hour))
		specFeatureAt(t, h.root, "002-new", "- [ ] T001 a\n- [ ] T002 b\n", now)
		specFeatureAt(t, h.root, "003-done-newest", "- [x] T001 a\n", now.Add(time.Hour))
		h.m.rescanSpec()
		if h.m.spec == nil || h.m.spec.Dir != "specs/002-new" || h.m.spec.Open != 2 {
			t.Fatalf("spec = %+v", h.m.spec)
		}
	})
	t.Run("all done", func(t *testing.T) {
		h := newWSHarness(t)
		specFeatureAt(t, h.root, "001-x", "- [x] T001 a\n- [X] T002 b\n", now)
		h.m.rescanSpec()
		if h.m.spec != nil || h.m.suggestion() != "" {
			t.Fatalf("spec = %+v", h.m.spec)
		}
	})
	t.Run("priority over resume, none with content", func(t *testing.T) {
		h := newWSHarness(t)
		specFeatureAt(t, h.root, "001-x", "- [ ] T001 a\n", now)
		h.m.rescanSpec()
		h.m.nSaved = 2
		if got := h.m.suggestion(); got != "/import speckit" {
			t.Fatalf("got %q", got)
		}
		h.m.sess.addLine("you", "hello")
		if got := h.m.suggestion(); got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("rescan after import", func(t *testing.T) {
		h := newWSHarness(t)
		writeSpecFeature(t, h.root)
		h.m.rescanSpec()
		h.command("/import speckit")
		if h.m.spec == nil {
			t.Fatal("open tasks remain until they are ticked")
		}
		if n := h.m.g.Node("T1"); n == nil || n.Spec == nil || n.Spec.File != "specs/003-login/tasks.md" || n.Spec.TaskID != "T002" {
			t.Fatalf("node spec = %+v", n)
		}
	})
}

func tuiGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestWorkspaceImportWarnsForUntrackedSpecs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	const warn = "specs/003-login is not committed: agents in worktrees can't read spec.md/plan.md — run git add specs/003-login && git commit, then import again"
	setup := func(t *testing.T) *wsHarness {
		h := newWSHarness(t)
		tuiGit(t, h.root, "init", "-q")
		tuiGit(t, h.root, "config", "user.name", "t")
		tuiGit(t, h.root, "config", "user.email", "t@example.com")
		if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		tuiGit(t, h.root, "add", "-A")
		tuiGit(t, h.root, "commit", "-q", "-m", "init")
		writeSpecFeature(t, h.root)
		h.m.deps.isolator = func(session string) *workspace.Isolator {
			return workspace.NewIsolator(h.root, session, "")
		}
		return h
	}
	t.Run("untracked", func(t *testing.T) {
		h := setup(t)
		h.command("/import speckit")
		if !transcriptHas(h, warn) || !h.m.hasNodes() {
			t.Fatalf("transcript: %+v", h.m.sess.snapshot().Transcript)
		}
	})
	t.Run("tracked with uncommitted changes", func(t *testing.T) {
		h := setup(t)
		tuiGit(t, h.root, "add", "specs")
		tuiGit(t, h.root, "commit", "-q", "-m", "spec")
		if err := os.WriteFile(filepath.Join(h.root, "specs/003-login/tasks.md"), []byte(importTasks+"- [ ] T006 more\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		h.command("/import speckit")
		if transcriptHas(h, "is not committed") || !h.m.hasNodes() {
			t.Fatalf("transcript: %+v", h.m.sess.snapshot().Transcript)
		}
	})
	t.Run("shared isolation", func(t *testing.T) {
		h := setup(t)
		h.m.deps.isolator = func(string) *workspace.Isolator { return nil }
		h.command("/import speckit")
		if transcriptHas(h, "is not committed") {
			t.Fatal("no worktrees, no warning")
		}
	})
}
