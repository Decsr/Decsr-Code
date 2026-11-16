package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

const testSession = "20260101-000000-abcd"

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return out
}

func writeT(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a git repository with one commit holding a.go and b.go.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.name", "tester")
	gitT(t, dir, "config", "user.email", "tester@example.com")
	writeT(t, dir, "a.go", "package a\n\nvar A = 1\n")
	writeT(t, dir, "b.go", "package b\n\nvar B = 1\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func newIso(t *testing.T, root string) *Isolator {
	t.Helper()
	iso := NewIsolator(root, testSession, "")
	if iso == nil {
		t.Fatal("expected an isolator")
	}
	return iso
}

func TestNewIsolatorDisabled(t *testing.T) {
	repo := newRepo(t)
	if NewIsolator(repo, testSession, IsolationShared) != nil {
		t.Error("shared mode must not isolate")
	}
	if NewIsolator(t.TempDir(), testSession, "") != nil {
		t.Error("a directory outside git must not isolate")
	}
	empty := t.TempDir()
	gitT(t, empty, "init", "-q")
	if NewIsolator(empty, testSession, "") != nil {
		t.Error("a repository without a commit must not isolate")
	}
	if NewIsolator(repo, "bad", "") != nil {
		t.Error("an invalid session id must not isolate")
	}
}

func TestIsolatorBaseFromHead(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	root, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, root, "rev-parse", "HEAD"); got != gitT(t, repo, "rev-parse", "HEAD") {
		t.Errorf("base = %s, want HEAD", got)
	}
	if !strings.HasPrefix(root, filepath.Join(repo, ".decsr", "worktrees", testSession)) {
		t.Errorf("worktree outside .decsr/worktrees: %s", root)
	}
}

func TestIsolatorBaseFromStashCreate(t *testing.T) {
	repo := newRepo(t)
	writeT(t, repo, "a.go", "package a\n\nvar A = 2\n") // uncommitted tracked change
	writeT(t, repo, "new.txt", "untracked")
	iso := newIso(t, repo)
	root, warns, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "a.go"))
	if err != nil || !strings.Contains(string(data), "A = 2") {
		t.Errorf("worktree should hold the uncommitted change, got %q (%v)", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); err == nil {
		t.Error("untracked file must not be copied")
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "untracked") {
		t.Errorf("want one untracked warning, got %v", warns)
	}
	// The user's tree is untouched and the stash list stays empty.
	if data, _ := os.ReadFile(filepath.Join(repo, "a.go")); !strings.Contains(string(data), "A = 2") {
		t.Error("main tree changed")
	}
	if got := gitT(t, repo, "stash", "list"); got != "" {
		t.Errorf("stash list = %q", got)
	}
	if _, w2, _ := iso.Start("T2"); len(w2) != 0 {
		t.Errorf("warning must be given once, got %v", w2)
	}
}

func finishWith(t *testing.T, iso *Isolator, id, file, content string) error {
	t.Helper()
	root, _, err := iso.Start(id)
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, root, file, content)
	return iso.Finish(id, "title of "+id)
}

func TestIsolatorTwoNodesMergeAndDependentSeesFile(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	r1, _, _ := iso.Start("T1")
	r2, _, _ := iso.Start("T2") // both running at once
	writeT(t, r1, "a.go", "package a\n\nvar A = 10\n")
	writeT(t, r2, "b.go", "package b\n\nvar B = 20\n")
	if err := iso.Finish("T1", "first"); err != nil {
		t.Fatal(err)
	}
	if err := iso.Finish("T2", "second"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{r1, r2} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("worktree %s should be removed", p)
		}
	}
	if out := gitT(t, repo, "branch", "--list", "decsr/*"); strings.Contains(out, "-T1") || strings.Contains(out, "-T2") {
		t.Errorf("node branches should be deleted:\n%s", out)
	}
	r3, _, err := iso.Start("T3") // a dependent
	if err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(r3, "a.go"))
	b, _ := os.ReadFile(filepath.Join(r3, "b.go"))
	if !strings.Contains(string(a), "A = 10") || !strings.Contains(string(b), "B = 20") {
		t.Errorf("dependent must see merged work, a=%q b=%q", a, b)
	}
	if msgs := gitT(t, repo, "log", "--format=%s", iso.Branch()); !strings.Contains(msgs, "T1: first") {
		t.Errorf("commit message missing:\n%s", msgs)
	}
	// The user's checkout is untouched until /merge.
	if data, _ := os.ReadFile(filepath.Join(repo, "a.go")); !strings.Contains(string(data), "A = 1") {
		t.Error("main checkout changed before merge")
	}
}

func TestIsolatorConflict(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	r1, _, _ := iso.Start("T1")
	r2, _, _ := iso.Start("T2")
	writeT(t, r1, "a.go", "package a\n\nvar A = 10\n")
	writeT(t, r2, "a.go", "package a\n\nvar A = 20\n")
	if err := iso.Finish("T1", "first"); err != nil {
		t.Fatal(err)
	}
	tip := gitT(t, repo, "rev-parse", iso.Branch())
	err := iso.Finish("T2", "second")
	if err == nil || !strings.Contains(err.Error(), "merge conflict with earlier nodes in: a.go") || !strings.Contains(err.Error(), "decsr/"+testSession+"-T2") {
		t.Fatalf("want conflict error, got %v", err)
	}
	if got := gitT(t, repo, "rev-parse", iso.Branch()); got != tip {
		t.Error("integration branch must be unchanged after a conflict")
	}
	if st := gitT(t, filepath.Join(worktreesDir(repo, testSession), "_integration"), "status", "--porcelain"); st != "" {
		t.Errorf("integration worktree must be clean after abort:\n%s", st)
	}
	if _, err := os.Stat(r2); err != nil {
		t.Error("conflicting node worktree must be kept")
	}
	// Running the node again replaces the stale worktree and branch.
	r2b, _, err := iso.Start("T2")
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := os.ReadFile(filepath.Join(r2b, "a.go")); !strings.Contains(string(a), "A = 10") {
		t.Errorf("retry must start from the latest work, got %q", a)
	}
}

func TestIsolatorDiscardAndNoChange(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	r1, _, _ := iso.Start("T1")
	writeT(t, r1, "x.txt", "x")
	iso.Discard("T1")
	if _, err := os.Stat(r1); err == nil {
		t.Error("discarded worktree must be removed")
	}
	if out := gitT(t, repo, "branch", "--list", "decsr/*-T1"); out != "" {
		t.Errorf("discarded branch must be deleted: %s", out)
	}
	tip := gitT(t, repo, "rev-parse", iso.Branch())
	if _, _, err := iso.Start("T2"); err != nil {
		t.Fatal(err)
	}
	if err := iso.Finish("T2", "nothing"); err != nil {
		t.Fatal(err)
	}
	if gitT(t, repo, "rev-parse", iso.Branch()) != tip {
		t.Error("a node without changes must not add a commit")
	}
}

func TestIsolatorExcludeOnce(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	for _, id := range []string{"T1", "T2"} {
		if _, _, err := iso.Start(id); err != nil {
			t.Fatal(err)
		}
	}
	iso2 := newIso(t, repo)
	if _, _, err := iso2.Start("T3"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), ".decsr/worktrees/"); n != 1 {
		t.Errorf("exclude entry count = %d, want 1:\n%s", n, data)
	}
	if st := gitT(t, repo, "status", "--porcelain"); strings.Contains(st, "worktrees") {
		t.Errorf("worktrees should be ignored:\n%s", st)
	}
}

func TestIsolatorSubdirectoryRoot(t *testing.T) {
	repo := newRepo(t)
	writeT(t, repo, "sub/s.go", "package s\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "sub")
	sub := filepath.Join(repo, "sub")
	iso := newIso(t, sub)
	root, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "s.go")); err != nil {
		t.Fatalf("node root should be the project subdirectory: %v", err)
	}
	writeT(t, root, "t.go", "package s\n")
	if err := iso.Finish("T1", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeSession(sub, testSession); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sub, "t.go")); err != nil {
		t.Error("merged file missing in the subdirectory project")
	}
}

func TestMergeSession(t *testing.T) {
	repo := newRepo(t)
	if _, err := MergeSession(repo, testSession); !errors.Is(err, ErrNothingMerged) {
		t.Fatalf("no branch: got %v", err)
	}
	iso := newIso(t, repo)
	if _, _, err := iso.Start("T1"); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeSession(repo, testSession); !errors.Is(err, ErrNothingMerged) {
		t.Fatalf("nothing merged yet: got %v", err)
	}
	if err := finishWith(t, iso, "T2", "c.go", "package c\n"); err != nil {
		t.Fatal(err)
	}

	writeT(t, repo, "b.go", "package b\n\nvar B = 99\n") // dirty tracked file
	if _, err := MergeSession(repo, testSession); !errors.Is(err, ErrDirtyTree) {
		t.Fatalf("dirty tree: got %v", err)
	}
	gitT(t, repo, "checkout", "--", "b.go")

	msg, err := MergeSession(repo, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, SessionBranch(testSession)) {
		t.Errorf("summary = %q", msg)
	}
	if _, err := os.Stat(filepath.Join(repo, "c.go")); err != nil {
		t.Error("c.go should be in the checkout after /merge")
	}
	if _, err := os.Stat(worktreesDir(repo, testSession)); err == nil {
		t.Error("session worktrees should be removed after a merge")
	}
	if gitT(t, repo, "branch", "--list", SessionBranch(testSession)) == "" {
		t.Error("integration branch must be kept")
	}
}

func TestMergeSessionKeepsUncommittedBase(t *testing.T) {
	repo := newRepo(t)
	writeT(t, repo, "a.go", "package a\n\nvar A = 2\n")
	iso := newIso(t, repo)
	if err := finishWith(t, iso, "T1", "c.go", "package c\n"); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "checkout", "--", "a.go") // the user commits or stashes first
	if _, err := MergeSession(repo, testSession); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "a.go")); !strings.Contains(string(data), "A = 2") {
		t.Errorf("snapshot of the uncommitted change should come back: %q", data)
	}
}

func TestMergeSessionConflictAborts(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	if err := finishWith(t, iso, "T1", "a.go", "package a\n\nvar A = 10\n"); err != nil {
		t.Fatal(err)
	}
	writeT(t, repo, "a.go", "package a\n\nvar A = 77\n")
	gitT(t, repo, "commit", "-q", "-am", "mine")
	head := gitT(t, repo, "rev-parse", "HEAD")
	_, err := MergeSession(repo, testSession)
	if err == nil || !strings.Contains(err.Error(), "a.go") {
		t.Fatalf("want conflict naming a.go, got %v", err)
	}
	if gitT(t, repo, "rev-parse", "HEAD") != head || gitT(t, repo, "status", "--porcelain") != "" {
		t.Error("checkout must be unchanged after an aborted merge")
	}
}

func TestRemoveSessionWorktrees(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	if _, _, err := iso.Start("T1"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSessionWorktrees(repo, testSession); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktreesDir(repo, testSession)); err == nil {
		t.Error("worktrees dir should be gone")
	}
	if gitT(t, repo, "branch", "--list", SessionBranch(testSession)) == "" {
		t.Error("branches must be kept")
	}
	if err := RemoveSessionWorktrees(repo, testSession); err != nil {
		t.Errorf("second call: %v", err)
	}
	// A later node of the same session recreates the integration worktree.
	if _, _, err := newIso(t, repo).Start("T2"); err != nil {
		t.Fatal(err)
	}
}

func TestIsolatorFallbackIdentity(t *testing.T) {
	repo := newRepo(t)
	gitT(t, repo, "config", "--unset", "user.name")
	gitT(t, repo, "config", "--unset", "user.email")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	iso := newIso(t, repo)
	if err := finishWith(t, iso, "T1", "c.go", "package c\n"); err != nil {
		t.Fatalf("commit without a configured identity: %v", err)
	}
}

// textModeConfig is the default config with every role on claude, whose
// default mode is text (an empty runner is opencode, which defaults to agent).
func textModeConfig() config.Config {
	cfg := config.DefaultConfig()
	agents := map[string]config.AgentConfig{}
	for k, v := range cfg.Agents {
		v.Runner = "claude"
		agents[k] = v
	}
	cfg.Agents = agents
	return cfg
}

// fileAnswer is an LLM that answers with one file block.
func fileAnswer(path, content string) *execFake {
	return &execFake{text: "**" + path + "**\n```go\n" + content + "```\n===SUMMARY===\nwrote " + path}
}

func isolatedExec(t *testing.T, root string, iso *Isolator, f *execFake) *executor {
	t.Helper()
	return &executor{
		root: root, cfg: textModeConfig(), iso: iso,
		newRunner: func(config.AgentConfig) (runner.LLMRunner, error) { return f, nil },
	}
}

func TestExecIsolatedWorkNodeMergesAndSharedKeepsOldBehaviour(t *testing.T) {
	repo := newRepo(t)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Add c", Kind: KindWork, Role: "coder"}}}

	// Isolated: the file lands in the integration branch, not in the checkout, and
	// the runner is told to work in the node's worktree.
	f := fileAnswer("c.go", "package c\n")
	iso := newIso(t, repo)
	res, err := isolatedExec(t, repo, iso, f).Run(context.Background(), g, g.Node("T1"), func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0] != "c.go" {
		t.Errorf("files = %v", res.Files)
	}
	if _, err := os.Stat(filepath.Join(repo, "c.go")); err == nil {
		t.Error("isolated node must not write into the checkout")
	}
	if !strings.HasPrefix(f.reqs[0].Dir, filepath.Join(repo, ".decsr", "worktrees")) {
		t.Errorf("req.Dir = %q", f.reqs[0].Dir)
	}
	if out := gitT(t, repo, "show", iso.Branch()+":c.go"); out != "package c" {
		t.Errorf("c.go on the integration branch = %q", out)
	}

	// Shared: exactly the old behaviour, files written in the root.
	shared := t.TempDir()
	f2 := fileAnswer("c.go", "package c\n")
	if _, err := isolatedExec(t, shared, nil, f2).Run(context.Background(), g, g.Node("T1"), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shared, "c.go")); err != nil {
		t.Error("shared mode must write into the root")
	}
	if f2.reqs[0].Dir != shared {
		t.Errorf("req.Dir = %q, want root", f2.reqs[0].Dir)
	}
}

func TestExecIsolatedFailureDiscardsWorktree(t *testing.T) {
	repo := newRepo(t)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "x", Kind: KindWork, Role: "no_such_role"}}}
	iso := newIso(t, repo)
	if _, err := isolatedExec(t, repo, iso, &execFake{}).Run(context.Background(), g, g.Node("T1"), func(Event) {}); err == nil {
		t.Fatal("want an error")
	}
	g.Nodes[0].Role = "coder"
	// An empty answer makes the worker fail; nothing may be left behind.
	if _, err := isolatedExec(t, repo, iso, &execFake{text: ""}).Run(context.Background(), g, g.Node("T1"), func(Event) {}); err == nil {
		t.Fatal("want an error for an empty answer")
	}
	if _, err := os.Stat(iso.nodePath("T1")); err == nil {
		t.Error("worktree of a failed node must be removed")
	}
}

func TestExecIsolatedCheckRunsInIntegrationWorktree(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	if err := finishWith(t, iso, "T1", "c.go", "package c\n"); err != nil {
		t.Fatal(err)
	}
	ex := isolatedExec(t, repo, iso, &execFake{})
	g := &Graph{Nodes: []*Node{{ID: "CHECK", Title: "Build & test", Kind: KindCheck, Commands: []string{"test -f c.go"}}}}
	if _, err := ex.Run(context.Background(), g, g.Node("CHECK"), func(Event) {}); err != nil {
		t.Fatalf("check should see merged work: %v", err)
	}
}

func TestIsolatorBaseIsPlainCommit(t *testing.T) {
	repo := newRepo(t)
	head := gitT(t, repo, "rev-parse", "HEAD")
	writeT(t, repo, "a.go", "package a\n\nvar A = 2\n") // uncommitted tracked change
	iso := newIso(t, repo)
	if err := finishWith(t, iso, "T1", "c.go", "package c\n"); err != nil {
		t.Fatal(err)
	}
	branch := SessionBranch(testSession)
	if log := gitT(t, repo, "log", "--format=%s", branch); strings.Contains(log, "WIP on") || strings.Contains(log, "index on") {
		t.Errorf("stash commits in the session history:\n%s", log)
	}
	base := gitT(t, repo, "rev-list", "--max-parents=1", "--first-parent", "-n1", branch+"^")
	if parents := strings.Fields(gitT(t, repo, "rev-list", "--parents", "-n1", base)); len(parents) != 2 || parents[1] != head {
		t.Errorf("base must have exactly the old HEAD as parent: %v", parents)
	}
	if got := gitT(t, repo, "log", "-n1", "--format=%s", base); got != "decsr: uncommitted changes at session start" {
		t.Errorf("base message = %q", got)
	}
	if got := gitT(t, repo, "show", base+":a.go"); !strings.Contains(got, "A = 2") {
		t.Errorf("base lacks the uncommitted change: %q", got)
	}
}

func TestIsolatorFinishStats(t *testing.T) {
	repo := newRepo(t) // a.go holds "var A = 1"
	iso := newIso(t, repo)
	root, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, root, "a.go", "package a\n\nvar A = 2\nvar B = 3\n")
	writeT(t, root, "new.txt", "one\ntwo\nthree\n")
	if err := os.WriteFile(filepath.Join(root, "pic.bin"), []byte{0, 1, 2, 0, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	stats, err := iso.FinishStats("T1", "stats")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]FileStat{}
	for _, s := range stats {
		got[s.Path] = s
	}
	if s := got["new.txt"]; s.Added != 3 || s.Removed != 0 || s.Binary {
		t.Errorf("new.txt = %+v", s)
	}
	if !got["new.txt"].New || got["a.go"].New {
		t.Errorf("New flags wrong: %+v", got)
	}
	if s := got["a.go"]; s.Added != 2 || s.Removed != 1 {
		t.Errorf("a.go = %+v", s)
	}
	if s := got["pic.bin"]; !s.Binary {
		t.Errorf("pic.bin = %+v, want binary", s)
	}
}

func TestIsolatorFinishStatsEmptyWithoutChanges(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	if _, _, err := iso.Start("T1"); err != nil {
		t.Fatal(err)
	}
	stats, err := iso.FinishStats("T1", "nothing")
	if err != nil || len(stats) != 0 {
		t.Errorf("stats = %v, err = %v", stats, err)
	}
}

func TestIsolatorNodeAndIntegrationDir(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	if _, ok := iso.NodeDir("T1"); ok {
		t.Error("no worktree yet")
	}
	if _, ok := iso.IntegrationDir(); ok {
		t.Error("the integration worktree must not exist before first use")
	}
	root, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := iso.NodeDir("T1"); !ok || d != root {
		t.Errorf("NodeDir = %q, %v; want %q", d, ok, root)
	}
	if _, ok := iso.IntegrationDir(); !ok {
		t.Error("starting a node creates the integration worktree")
	}
	iso.Discard("T1")
	if _, ok := iso.NodeDir("T1"); ok {
		t.Error("a discarded node has no worktree")
	}
	if _, ok := iso.NodeDir("../x"); ok {
		t.Error("an invalid id has no worktree")
	}
}
