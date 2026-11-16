package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTickSpecTask(t *testing.T) {
	tick := func(in, id string) (string, bool, error) {
		out, ch, err := TickSpecTask([]byte(in), id)
		return string(out), ch, err
	}
	t.Run("first match only", func(t *testing.T) {
		out, ch, err := tick("- [ ] T003 a\n- [ ] T003 again\n", "T003")
		if err != nil || !ch || out != "- [x] T003 a\n- [ ] T003 again\n" {
			t.Fatalf("%q %v %v", out, ch, err)
		}
	})
	t.Run("crlf, indentation, star bullet, no trailing newline", func(t *testing.T) {
		in := "## P\r\n  * [ ] T002 [P] x\r\n    - [ ] T003 y"
		out, ch, err := tick(in, "T003")
		if err != nil || !ch || out != "## P\r\n  * [ ] T002 [P] x\r\n    - [x] T003 y" {
			t.Fatalf("%q %v %v", out, ch, err)
		}
		out, _, _ = tick(in, "T002")
		if out != "## P\r\n  * [x] T002 [P] x\r\n    - [ ] T003 y" {
			t.Fatalf("%q", out)
		}
	})
	t.Run("already done", func(t *testing.T) {
		for _, mark := range []string{"x", "X"} {
			in := "- [" + mark + "] T003 a\n"
			out, ch, err := tick(in, "T003")
			if err != nil || ch || out != in {
				t.Fatalf("%q %v %v", out, ch, err)
			}
		}
	})
	t.Run("not found", func(t *testing.T) {
		in := "- [ ] T004 a\n"
		out, ch, err := tick(in, "T003")
		if err != ErrSpecTaskMissing || ch || out != in {
			t.Fatalf("%q %v %v", out, ch, err)
		}
	})
	t.Run("id prefix safety", func(t *testing.T) {
		out, ch, err := tick("- [ ] T10 ten\n- [ ] T1 one\n", "T1")
		if err != nil || !ch || out != "- [ ] T10 ten\n- [x] T1 one\n" {
			t.Fatalf("%q %v %v", out, ch, err)
		}
		if _, _, err := tick("- [ ] T10 ten\n", "T1"); err != ErrSpecTaskMissing {
			t.Fatalf("T1 matched T10: %v", err)
		}
	})
}

const specTasks = "## Phase 1\r\n\r\n- [ ] T001 first\r\n- [ ] T002 second\r\n"

// specExec runs one node answering with a file (or nothing) and returns the
// result of Run.
func runSpecNode(t *testing.T, root string, iso *Isolator, n *Node, text string) (Result, error) {
	t.Helper()
	g := &Graph{Nodes: []*Node{n}}
	f := &execFake{text: text}
	return isolatedExec(t, root, iso, f).Run(context.Background(), g, n, func(Event) {})
}

func specNode(id string) *Node {
	return &Node{ID: "T1", Title: "x", Kind: KindWork, Role: "coder", Spec: &SpecRef{File: "specs/001-x/tasks.md", TaskID: id}}
}

func readT(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func specRepo(t *testing.T, commitTasks bool) string {
	t.Helper()
	repo := newRepo(t)
	writeT(t, repo, "specs/001-x/tasks.md", specTasks)
	if commitTasks {
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "spec")
	}
	return repo
}

func TestSpecWriteBackTrackedWorktree(t *testing.T) {
	repo := specRepo(t, true)
	iso := newIso(t, repo)
	tasks := filepath.Join(repo, "specs/001-x/tasks.md")
	res, err := runSpecNode(t, repo, iso, specNode("T001"), "**c.go**\n```go\npackage c\n```\n===SUMMARY===\ndone")
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("%v %v", err, res.Warnings)
	}
	if got := readT(t, tasks); got != specTasks {
		t.Fatalf("checkout changed before merge: %q", got)
	}
	log := gitT(t, repo, "log", "--format=%s", iso.Branch())
	if !strings.Contains(log, "decsr: mark T001 done in specs/001-x/tasks.md") {
		t.Fatalf("log:\n%s", log)
	}
	if out := gitT(t, repo, "show", iso.Branch()+":specs/001-x/tasks.md"); !strings.Contains(out, "[x] T001") || !strings.Contains(out, "[ ] T002") {
		t.Fatalf("branch file: %q", out)
	}
	if _, err := MergeSession(repo, testSession); err != nil {
		t.Fatal(err)
	}
	if got := readT(t, tasks); got != strings.Replace(specTasks, "[ ] T001", "[x] T001", 1) {
		t.Fatalf("after merge: %q", got)
	}
}

func TestSpecWriteBackNoCodeChanges(t *testing.T) {
	repo := specRepo(t, true)
	iso := newIso(t, repo)
	res, err := runSpecNode(t, repo, iso, specNode("T002"), "===SUMMARY===\nnothing to change")
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0] != "no files were written" {
		t.Fatalf("%v %v", err, res.Warnings)
	}
	if out := gitT(t, repo, "show", iso.Branch()+":specs/001-x/tasks.md"); !strings.Contains(out, "[x] T002") {
		t.Fatalf("not ticked: %q", out)
	}
	if _, err := MergeSession(repo, testSession); err != nil {
		t.Fatal(err)
	}
}

func TestSpecWriteBackUntrackedEditsCheckout(t *testing.T) {
	repo := specRepo(t, false)
	iso := newIso(t, repo)
	// A node that merges something, so /merge has work.
	if _, err := runSpecNode(t, repo, iso, specNode("T001"), "**c.go**\n```go\npackage c\n```\n===SUMMARY===\ndone"); err != nil {
		t.Fatal(err)
	}
	tasks := filepath.Join(repo, "specs/001-x/tasks.md")
	if got := readT(t, tasks); got != strings.Replace(specTasks, "[ ] T001", "[x] T001", 1) {
		t.Fatalf("checkout file: %q", got)
	}
	if _, err := MergeSession(repo, testSession); err != nil {
		t.Fatalf("untracked edit must not block /merge: %v", err)
	}
}

func TestSpecWriteBackShared(t *testing.T) {
	root := t.TempDir()
	writeT(t, root, "specs/001-x/tasks.md", specTasks)
	if _, err := runSpecNode(t, root, nil, specNode("T002"), "===SUMMARY===\nok"); err != nil {
		t.Fatal(err)
	}
	if got := readT(t, filepath.Join(root, "specs/001-x/tasks.md")); got != strings.Replace(specTasks, "[ ] T002", "[x] T002", 1) {
		t.Fatalf("%q", got)
	}
}

func TestSpecWriteBackSkipped(t *testing.T) {
	root := t.TempDir()
	writeT(t, root, "specs/001-x/tasks.md", specTasks)
	tasks := filepath.Join(root, "specs/001-x/tasks.md")
	// failed node
	if _, err := runSpecNode(t, root, nil, specNode("T001"), ""); err == nil {
		t.Fatal("want an error")
	}
	// node without Spec
	n := specNode("T001")
	n.Spec = nil
	if _, err := runSpecNode(t, root, nil, n, "===SUMMARY===\nok"); err != nil {
		t.Fatal(err)
	}
	if got := readT(t, tasks); got != specTasks {
		t.Fatalf("changed: %q", got)
	}
}

func TestSpecWriteBackMissingTaskWarns(t *testing.T) {
	root := t.TempDir()
	writeT(t, root, "specs/001-x/tasks.md", specTasks)
	res, err := runSpecNode(t, root, nil, specNode("T009"), "===SUMMARY===\nok")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range res.Warnings {
		found = found || w == "tasks.md: T009 not found, not marked done"
	}
	if !found {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestSpecUntrackedAndDetect(t *testing.T) {
	repo := specRepo(t, false)
	if got := SpecUntracked(repo, "specs/001-x"); len(got) != 1 || got[0] != "tasks.md" {
		t.Fatalf("untracked = %v", got)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "spec")
	if got := SpecUntracked(repo, "specs/001-x"); len(got) != 0 {
		t.Fatalf("untracked = %v", got)
	}
	if f, ok := DetectSpec(repo); !ok || f.Dir != "specs/001-x" || f.Open != 2 {
		t.Fatalf("detect = %+v %v", f, ok)
	}
}

func TestNodeSpecJSONCompat(t *testing.T) {
	var g Graph
	err := json.Unmarshal([]byte(`{"nodes":[{"id":"T1","title":"x","kind":"work","status":"todo"}]}`), &g)
	if err != nil || g.Nodes[0].Spec != nil {
		t.Fatalf("%v %+v", err, g)
	}
	n := &Node{ID: "T1", Spec: &SpecRef{File: "specs/a/tasks.md", TaskID: "T003"}}
	c := (&Graph{Nodes: []*Node{n}}).Clone()
	c.Nodes[0].Spec.TaskID = "T9"
	if n.Spec.TaskID != "T003" {
		t.Fatal("Clone shares Spec")
	}
}
