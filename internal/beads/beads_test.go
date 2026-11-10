package beads

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fakeBD(t *testing.T, stdout string) (argvFile string) {
	t.Helper()
	dir := t.TempDir()
	argvFile = filepath.Join(dir, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argvFile + "; done\nprintf '%s' '" + stdout + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvFile
}

func TestCreate_Argv(t *testing.T) {
	argvFile := fakeBD(t, "bd-abc\n")
	desc := "line1\n`code`\n- item"
	id, err := Create(context.Background(), t.TempDir(), "My title", desc, 1, "decsr-task", "x")
	if err != nil || id != "bd-abc" {
		t.Fatalf("Create = (%q, %v), want (bd-abc, nil)", id, err)
	}
	b, _ := os.ReadFile(argvFile)
	// the fake writes one argv element per line, so the multi-line description spans three lines
	want := []string{"create", "--silent", "--title", "My title", "--description", "line1", "`code`", "- item",
		"--type", "task", "--priority", "1", "--labels", "decsr-task,x"}
	got := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestIssueDecodesIssueType(t *testing.T) {
	fakeBD(t, `[{"id":"a","title":"t","status":"open","priority":2,"issue_type":"task"}]`)
	issues, err := Ready(context.Background(), t.TempDir(), "p")
	if err != nil || len(issues) != 1 || issues[0].Type != "task" {
		t.Fatalf("Ready = (%+v, %v), want one issue with Type task", issues, err)
	}
}

func TestRealBD(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed")
	}
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("BEADS_DIR", filepath.Join(root, ".beads"))
	for _, c := range [][]string{{"git", "init", "-q"}, {"bd", "init", "-q", "--non-interactive", "-p", "itest"}} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("%v failed (needs interaction or env?): %v: %s", c, err, out)
		}
	}
	ctx := context.Background()
	top, err := Create(ctx, root, "top", "top desc", 1, "decsr-task")
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	var kids []string
	for _, title := range []string{"one", "two"} {
		id, err := Create(ctx, root, title, "line1\n`code` and \"quotes\"\n\n- item", 2)
		if err != nil {
			t.Fatalf("create child %s: %v", title, err)
		}
		if err := Link(ctx, root, id, top, "parent-child"); err != nil {
			t.Fatalf("link: %v", err)
		}
		kids = append(kids, id)
	}
	ready, err := Ready(ctx, root, top)
	if err != nil || len(ready) != 2 {
		t.Fatalf("Ready = (%d, %v), want 2", len(ready), err)
	}
	if want := "line1\n`code` and \"quotes\"\n\n- item"; ready[0].Description != want || ready[0].Type != "task" {
		t.Fatalf("child = %+v, want multi-line description and Type task", ready[0])
	}
	if err := Claim(ctx, root, kids[0]); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if ip, err := Children(ctx, root, top, "in_progress"); err != nil || len(ip) != 1 || ip[0].ID != kids[0] {
		t.Fatalf("Children in_progress = (%+v, %v)", ip, err)
	}
	if err := Close(ctx, root, kids[0], "done"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if open, err := Children(ctx, root, top, "open", "in_progress"); err != nil || len(open) != 1 || open[0].ID != kids[1] {
		t.Fatalf("Children open = (%+v, %v)", open, err)
	}
	if err := Claim(ctx, root, top); err != nil {
		t.Fatal(err)
	}
	if act, err := ActiveTasks(ctx, root, ""); err != nil || len(act) != 1 || act[0].ID != top {
		t.Fatalf("ActiveTasks = (%+v, %v)", act, err)
	}
	if err := Close(ctx, root, kids[1], ""); err != nil {
		t.Fatal(err)
	}
	if err := Close(ctx, root, top, ""); err != nil {
		t.Fatalf("close top: %v", err)
	}
	if done, err := ClosedTasks(ctx, root, ""); err != nil || len(done) != 1 || done[0].ID != top {
		t.Fatalf("ClosedTasks = (%+v, %v)", done, err)
	}
}
