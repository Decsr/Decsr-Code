package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCmd(t *testing.T, base, file, body string) {
	t.Helper()
	d := filepath.Join(base, ".decsr", "commands")
	if err := os.MkdirAll(d, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, file), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func cmdNames(cs []CustomCommand) string {
	var n []string
	for _, c := range cs {
		n = append(n, c.Name+":"+c.Source)
	}
	return strings.Join(n, ",")
}

func TestLoadCustomCommandsPrecedenceAndSort(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, home, "shared.md", "user shared")
	writeCmd(t, home, "zed.md", "user zed")
	writeCmd(t, root, "shared.md", "project shared")
	writeCmd(t, root, "alpha.md", "project alpha")
	got := LoadCustomCommands(root)
	if cmdNames(got) != "alpha:project,shared:project,zed:user" {
		t.Fatalf("got %s", cmdNames(got))
	}
	if got[1].Body != "project shared" {
		t.Errorf("body = %q", got[1].Body)
	}
}

func TestLoadCustomCommandsSkips(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, root, "good.md", "do it")
	writeCmd(t, root, "Bad Name.md", "x")
	writeCmd(t, root, "UPPER.md", "x")
	writeCmd(t, root, ".hidden.md", "x")
	writeCmd(t, root, "notmd.txt", "x")
	writeCmd(t, root, "empty.md", "  \n\t\n")
	writeCmd(t, root, "headonly.md", "# Only a heading\n")
	writeCmd(t, root, "big.md", strings.Repeat("x", 64*1024+1))
	writeCmd(t, root, "exact.md", strings.Repeat("x", 64*1024))
	dir := filepath.Join(root, ".decsr", "commands")
	if err := os.Mkdir(filepath.Join(dir, "adir.md"), 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	_ = os.WriteFile(outside, []byte("secret"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	// A name with path parts cannot even exist as a file name, but a file
	// literally called "x.md" next to a "..": the loader never joins user text.
	got := LoadCustomCommands(root)
	if cmdNames(got) != "exact:project,good:project" {
		t.Fatalf("got %s", cmdNames(got))
	}
}

func TestLoadCustomCommandsNoDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := LoadCustomCommands(t.TempDir()); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestCustomCommandDescription(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	writeCmd(t, root, "withhead.md", "\n\n# Review the diff\nLook at $ARGUMENTS carefully.\n")
	writeCmd(t, root, "nohead.md", "Please "+strings.Repeat("a", 100))
	writeCmd(t, root, "hashtag.md", "#nospace is not a heading\nbody")
	by := map[string]CustomCommand{}
	for _, c := range LoadCustomCommands(root) {
		by[c.Name] = c
	}
	h := by["withhead"]
	if h.Description != "Review the diff" || h.Body != "Look at $ARGUMENTS carefully." {
		t.Errorf("withhead = %+v", h)
	}
	n := by["nohead"]
	if len([]rune(n.Description)) != 60 || !strings.HasPrefix(n.Description, "Please aaa") || !strings.HasPrefix(n.Body, "Please a") {
		t.Errorf("nohead = %+v", n)
	}
	if by["hashtag"].Description != "#nospace is not a heading body" {
		t.Errorf("hashtag = %+v", by["hashtag"])
	}
}

func TestCustomCommandExpand(t *testing.T) {
	c := CustomCommand{Body: "Fix $ARGUMENTS now. Really $ARGUMENTS."}
	if got := c.Expand("  the bug \n"); got != "Fix the bug now. Really the bug." {
		t.Errorf("got %q", got)
	}
	if got := c.Expand(""); got != "Fix  now. Really ." {
		t.Errorf("empty args: %q", got)
	}
	plain := CustomCommand{Body: "Run the audit"}
	if got := plain.Expand(" src/ "); got != "Run the audit\n\nsrc/" {
		t.Errorf("append: %q", got)
	}
	if got := plain.Expand("  "); got != "Run the audit" {
		t.Errorf("no args: %q", got)
	}
}
