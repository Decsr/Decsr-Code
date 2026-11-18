package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDiffBasic(t *testing.T) {
	text := "diff --git a/x.go b/x.go\nindex 1..2 100644\n--- a/x.go\n+++ b/x.go\n@@ -1,3 +1,4 @@ func f\n a\n-b\n+B\n+C\n c\n"
	files := ParseDiff(text)
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	f := files[0]
	if f.Path != "x.go" || f.Added != 2 || f.Removed != 1 || len(f.Hunks) != 1 {
		t.Fatalf("file = %+v", f)
	}
	l := f.Hunks[0].Lines
	if l[0].Kind != ' ' || l[0].OldNo != 1 || l[0].NewNo != 1 {
		t.Errorf("line0 = %+v", l[0])
	}
	if l[1].Kind != '-' || l[1].OldNo != 2 || l[1].NewNo != 0 {
		t.Errorf("line1 = %+v", l[1])
	}
	if l[2].Kind != '+' || l[2].NewNo != 2 || l[3].NewNo != 3 || l[4].OldNo != 3 || l[4].NewNo != 4 {
		t.Errorf("numbers wrong: %+v", l)
	}
	if f.Hunks[0].Header != "@@ -1,3 +1,4 @@ func f" {
		t.Errorf("header = %q", f.Hunks[0].Header)
	}
}

func TestParseDiffRename(t *testing.T) {
	text := "diff --git a/old name.txt b/new name.txt\nsimilarity index 100%\nrename from old name.txt\nrename to new name.txt\n" +
		"diff --git a/p.txt b/q.txt\nsimilarity index 80%\nrename from p.txt\nrename to q.txt\nindex 1..2 100644\n--- a/p.txt\n+++ b/q.txt\n@@ -1 +1 @@\n-x\n+y\n"
	files := ParseDiff(text)
	if len(files) != 2 {
		t.Fatalf("files = %d", len(files))
	}
	if files[0].Path != "new name.txt" || files[0].OldPath != "old name.txt" || len(files[0].Hunks) != 0 {
		t.Errorf("pure rename = %+v", files[0])
	}
	if files[1].Path != "q.txt" || files[1].OldPath != "p.txt" || files[1].Added != 1 || files[1].Removed != 1 {
		t.Errorf("rename with change = %+v", files[1])
	}
}

func TestParseDiffBinaryNewDeleted(t *testing.T) {
	text := "diff --git a/img.png b/img.png\nnew file mode 100644\nindex 0..1\nBinary files /dev/null and b/img.png differ\n" +
		"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\nindex 1..0\n--- a/gone.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-one\n-two\n" +
		"diff --git a/fresh.txt b/fresh.txt\nnew file mode 100644\nindex 0..1\n--- /dev/null\n+++ b/fresh.txt\n@@ -0,0 +1 @@\n+hello\n"
	files := ParseDiff(text)
	if len(files) != 3 {
		t.Fatalf("files = %d", len(files))
	}
	if !files[0].Binary || !files[0].New || files[0].Path != "img.png" || len(files[0].Hunks) != 0 {
		t.Errorf("binary = %+v", files[0])
	}
	if !files[1].Deleted || files[1].Path != "gone.txt" || files[1].Removed != 2 {
		t.Errorf("deleted = %+v", files[1])
	}
	if !files[2].New || files[2].Path != "fresh.txt" || files[2].Added != 1 || files[2].Hunks[0].Lines[0].NewNo != 1 {
		t.Errorf("new = %+v", files[2])
	}
}

func TestParseDiffNoNewlineAndCRLF(t *testing.T) {
	text := "diff --git a/c.txt b/c.txt\n--- a/c.txt\n+++ b/c.txt\n@@ -1 +1 @@\n-old\r\n\\ No newline at end of file\n+new\r\n+--- dash\n"
	f := ParseDiff(text)[0]
	l := f.Hunks[0].Lines
	if len(l) != 4 || l[1].Kind != '\\' || !strings.Contains(l[1].Text, "No newline") {
		t.Fatalf("lines = %+v", l)
	}
	if l[0].Text != "old" || l[2].Text != "new" {
		t.Errorf("CR not stripped: %q %q", l[0].Text, l[2].Text)
	}
	if l[3].Text != "--- dash" || f.Added != 2 || f.Removed != 1 {
		t.Errorf("content that looks like a header: %+v (%d/%d)", l[3], f.Added, f.Removed)
	}
}

func TestParseDiffEmpty(t *testing.T) {
	if len(ParseDiff("")) != 0 || len(ParseDiff("\n")) != 0 {
		t.Error("empty text should give no files")
	}
}

func fileByPath(d Diff, p string) *DiffFile {
	for i := range d.Files {
		if d.Files[i].Path == p {
			return &d.Files[i]
		}
	}
	return nil
}

func TestNodeDiffIsolatedCommit(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	root, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, root, "a.go", "package a\n\nvar A = 2\n")
	writeT(t, root, "new.go", "package n\n")
	_, commit, err := iso.FinishCommit("T1", "edit")
	if err != nil || commit == "" {
		t.Fatalf("commit = %q, err = %v", commit, err)
	}
	n := &Node{ID: "T1", Kind: KindWork, Status: StatusDone, Commit: commit}
	d, err := NodeDiff(repo, n, iso)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 2 || d.Note != "" {
		t.Fatalf("files = %+v", d.Files)
	}
	if a := fileByPath(d, "a.go"); a == nil || a.Added != 1 || a.Removed != 1 {
		t.Errorf("a.go = %+v", a)
	}
	if nf := fileByPath(d, "new.go"); nf == nil || !nf.New {
		t.Errorf("new.go = %+v", nf)
	}
	// The commit survives the node branch.
	if _, err := git(repo, "rev-parse", "--verify", "-q", "refs/heads/"+nodeBranch(testSession, "T1")); err == nil {
		t.Error("node branch should be gone")
	}

	// A commit that is missing is a clear error, not a crash.
	bad := &Node{ID: "T9", Kind: KindWork, Status: StatusDone, Commit: strings.Repeat("0", 40)}
	if _, err := NodeDiff(repo, bad, iso); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("err = %v", err)
	}
	// Done without a commit (older session).
	old := &Node{ID: "T8", Kind: KindWork, Status: StatusDone, FileStats: []FileStat{{Path: "a.go"}}}
	if _, err := NodeDiff(repo, old, iso); err == nil {
		t.Error("expected an error for a node without a recorded commit")
	}
}

func TestNodeDiffSubdirectory(t *testing.T) {
	repo := newRepo(t)
	writeT(t, repo, "sub/s.go", "package s\n")
	writeT(t, repo, "top.go", "package top\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "sub")
	sub := filepath.Join(repo, "sub")
	iso := newIso(t, sub)
	root, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, root, "s.go", "package s\n\nvar S = 1\n")
	_, commit, err := iso.FinishCommit("T1", "s")
	if err != nil {
		t.Fatal(err)
	}
	d, err := NodeDiff(sub, &Node{ID: "T1", Kind: KindWork, Status: StatusDone, Commit: commit}, iso)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 1 || d.Files[0].Path != "s.go" {
		t.Fatalf("paths should be relative to the project: %+v", d.Files)
	}
}

func TestNodeDiffRunningWorktreeWithUntracked(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	root, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, root, "a.go", "package a\n\nvar A = 3\n")
	writeT(t, root, "fresh.txt", "one\ntwo\n")
	before := gitT(t, root, "status", "--porcelain")
	n := &Node{ID: "T1", Kind: KindWork, Status: StatusRunning}
	d, err := NodeDiff(repo, n, iso)
	if err != nil {
		t.Fatal(err)
	}
	if a := fileByPath(d, "a.go"); a == nil || a.Added != 1 {
		t.Errorf("a.go = %+v", a)
	}
	if f := fileByPath(d, "fresh.txt"); f == nil || !f.New || f.Added != 2 {
		t.Errorf("untracked file = %+v", f)
	}
	if after := gitT(t, root, "status", "--porcelain"); after != before {
		t.Errorf("the worktree index changed:\n%s\nvs\n%s", before, after)
	}
}

func TestNodeDiffShared(t *testing.T) {
	repo := newRepo(t)
	writeT(t, repo, "a.go", "package a\n\nvar A = 9\n")
	writeT(t, repo, "mine.go", "package m\n")
	writeT(t, repo, "other.go", "package o\n")
	n := &Node{ID: "T1", Kind: KindWork, Status: StatusDone, Files: []string{"a.go", "mine.go"}}
	d, err := NodeDiff(repo, n, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Note == "" {
		t.Error("shared mode should carry a note")
	}
	if len(d.Files) != 2 || fileByPath(d, "a.go") == nil || fileByPath(d, "mine.go") == nil {
		t.Fatalf("files = %+v", d.Files)
	}
	if fileByPath(d, "other.go") != nil {
		t.Error("other.go is not a file of the node")
	}
	empty, err := NodeDiff(repo, &Node{ID: "T2", Kind: KindWork, Status: StatusDone}, nil)
	if err != nil || len(empty.Files) != 0 {
		t.Errorf("empty = %+v, %v", empty, err)
	}
}

func TestSessionDiff(t *testing.T) {
	repo := newRepo(t)
	iso := newIso(t, repo)
	if _, err := SessionDiff(repo, testSession); err == nil {
		t.Error("expected an error before the branch exists")
	}
	if err := finishWith(t, iso, "T1", "a.go", "package a\n\nvar A = 5\n"); err != nil {
		t.Fatal(err)
	}
	if err := finishWith(t, iso, "T2", "z.go", "package z\n"); err != nil {
		t.Fatal(err)
	}
	d, err := SessionDiff(repo, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 2 {
		t.Fatalf("files = %+v", d.Files)
	}
	if _, err := os.Stat(filepath.Join(repo, "z.go")); err == nil {
		t.Error("the checkout must not be touched")
	}
}

func TestEngineKeepsNodeCommitAndClearsItOnRestart(t *testing.T) {
	f := newFakeExec()
	f.behavior["T1"] = func(context.Context) (Result, error) {
		return Result{Summary: "ok", Commit: "abc123", FileStats: []FileStat{{Path: "a.go", Added: 1}}}, nil
	}
	g := &Graph{Nodes: []*Node{work("T1")}}
	e := NewEngineWithSave(g, f, 1, nil)
	defer e.Close()
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, e, 1)
	if got := e.Snapshot().Node("T1").Commit; got != "abc123" {
		t.Fatalf("commit = %q", got)
	}
	if got := e.Snapshot().Clone().Node("T1").Commit; got != "abc123" {
		t.Fatalf("clone commit = %q", got)
	}
	f.behavior["T1"] = func(context.Context) (Result, error) { return Result{Summary: "again"}, nil }
	if err := e.Replace(func(g *Graph) { g.Node("T1").Status = StatusFailed }); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, e, 1)
	if got := e.Snapshot().Node("T1").Commit; got != "" {
		t.Errorf("commit after a rerun = %q", got)
	}
}

func TestExecIsolatedWorkNodeResultHasCommit(t *testing.T) {
	repo := newRepo(t)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Add c", Kind: KindWork, Role: "coder"}}}
	iso := newIso(t, repo)
	res, err := isolatedExec(t, repo, iso, fileAnswer("c.go", "package c\n")).Run(context.Background(), g, g.Node("T1"), func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" {
		t.Fatal("no commit in the result")
	}
	d, err := NodeDiff(repo, &Node{ID: "T1", Kind: KindWork, Status: StatusDone, Commit: res.Commit}, iso)
	if err != nil || len(d.Files) != 1 || d.Files[0].Path != "c.go" {
		t.Fatalf("diff = %+v, %v", d, err)
	}
}

func TestAddWork(t *testing.T) {
	g := &Graph{Nodes: []*Node{work("T1"), work("T2"), {ID: "CHECK", Kind: KindCheck, Status: StatusDone, DependsOn: []string{"T1", "T2"}}}}
	e := NewEngineWithSave(g, newFakeExec(), 1, nil)
	defer e.Close()
	id, next := e.AddWork("  Address   notes ", "desc", "coder", 1)
	if id != "T3" || next != 4 {
		t.Fatalf("id = %s next = %d", id, next)
	}
	s := e.Snapshot()
	n := s.Node("T3")
	if n == nil || n.Title != "Address notes" || n.Role != "coder" || len(n.DependsOn) != 0 || n.Status != StatusTodo {
		t.Fatalf("node = %+v", n)
	}
	if c := s.Node("CHECK"); c.Status != StatusTodo || c.DependsOn[len(c.DependsOn)-1] != "T3" {
		t.Errorf("check = %+v", c)
	}
}

func TestSessionNotesRoundTripAndOldFiles(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	s.AddLine("you", "x")
	s.Notes = []DiffNote{{NodeID: "T1", Path: "a.go", Line: 3, Side: "new", Text: "why?"}}
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(root, s.ID)
	if err != nil || len(got.Notes) != 1 || got.Notes[0] != s.Notes[0] {
		t.Fatalf("notes = %+v, %v", got.Notes, err)
	}
	// A session without notes writes no "notes" key and an old file still loads.
	s2 := NewSession()
	s2.AddLine("you", "x")
	if err := s2.Save(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(SessionsDir(root), s2.ID+".json"))
	if strings.Contains(string(data), `"notes"`) {
		t.Error("empty notes should be omitted")
	}
	old, err := LoadSession(root, s2.ID)
	if err != nil || old.Notes != nil {
		t.Fatalf("old = %+v, %v", old.Notes, err)
	}
}

func TestWorkingTreeDiffHoldsChangedAndUntrackedFiles(t *testing.T) {
	repo := newRepo(t)
	writeT(t, repo, "a.go", "package a\n\nvar A = 2\n")
	writeT(t, repo, "new.go", "package n\n")
	d, err := WorkingTreeDiff(repo)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range d.Files {
		names = append(names, f.Path)
	}
	if got := strings.Join(names, ","); !strings.Contains(got, "a.go") || !strings.Contains(got, "new.go") || strings.Contains(got, "b.go") {
		t.Errorf("files = %v", names)
	}
	if clean, err := WorkingTreeDiff(newRepo(t)); err != nil || len(clean.Files) != 0 {
		t.Errorf("a clean tree has no diff: %+v %v", clean, err)
	}
	if _, err := WorkingTreeDiff(t.TempDir()); err == nil {
		t.Error("a directory outside git is an error")
	}
}
