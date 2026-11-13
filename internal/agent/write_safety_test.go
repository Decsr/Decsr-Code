package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestWriteProjectFileProtectedPaths(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{".git/config", ".git/hooks/pre-commit", ".decsr/project.yaml", "./.decsr/x"} {
		err := writeProjectFile(root, p, "x\n", true, config.ScopeConfig{})
		if err == nil || !strings.Contains(err.Error(), "protected path") {
			t.Errorf("%s: err = %v", p, err)
		}
	}
	if err := writeProjectFile(root, "docs/.git-notes.md", "x\n", true, config.ScopeConfig{}); err != nil {
		t.Errorf("lookalike path rejected: %v", err)
	}
}

func TestWriteProjectFileSymlinkDirEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	err := writeProjectFile(root, "link/sub/evil.txt", "x\n", true, config.ScopeConfig{})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if _, serr := os.Stat(filepath.Join(outside, "sub", "evil.txt")); serr == nil {
		t.Fatal("file written outside root")
	}
}

func TestWriteProjectFileSymlinkFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(outside, []byte("orig"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "f.txt")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := writeProjectFile(root, "f.txt", "new\n", true, config.ScopeConfig{}); err == nil {
		t.Fatal("expected rejection")
	}
	if b, _ := os.ReadFile(outside); string(b) != "orig" {
		t.Errorf("target modified: %q", b)
	}
}

func TestWriteProjectFileSymlinkInsideRootAllowed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := writeProjectFile(root, "alias/a.txt", "x\n", true, config.ScopeConfig{}); err != nil {
		t.Fatalf("in-root symlink dir rejected: %v", err)
	}
}

func TestWriteProjectFileScope(t *testing.T) {
	root := t.TempDir()
	sc := config.ScopeConfig{Allow: []string{"src/**", "*.md"}, Deny: []string{"src/secret/**", "*.lock"}}
	cases := []struct {
		path string
		ok   bool
		msg  string
	}{
		{"src/a/b.go", true, ""},
		{"README.md", true, ""},
		{"src/secret/k.txt", false, "denied by project scope"},
		{"src/yarn.lock", false, "denied by project scope"},
		{"other/x.go", false, "outside project scope"},
	}
	for _, c := range cases {
		err := writeProjectFile(root, c.path, "x\n", true, sc)
		if c.ok && err != nil {
			t.Errorf("%s: unexpected %v", c.path, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), c.msg)) {
			t.Errorf("%s: err = %v, want %q", c.path, err, c.msg)
		}
	}
}

func TestWriteProjectFileNestedNormal(t *testing.T) {
	root := t.TempDir()
	if err := writeProjectFile(root, "a/b/c/d.txt", "hi\n", true, config.ScopeConfig{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a/b/c/d.txt")); string(b) != "hi\n" {
		t.Errorf("content = %q", b)
	}
}
