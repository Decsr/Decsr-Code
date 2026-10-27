package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile_RefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	ws, err := EnsureWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws.Dir, SummaryFile)); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteFile(SummaryFile, []byte("pwned")); err == nil {
		t.Fatal("expected error writing through escaping symlink")
	}
	got, _ := os.ReadFile(outside)
	if string(got) != "original" {
		t.Errorf("outside file modified: %q", got)
	}
}

func TestWriteFile_Normal(t *testing.T) {
	ws, err := EnsureWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteFile(SummaryFile, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	got, err := ws.ReadFile(SummaryFile)
	if err != nil || string(got) != "ok" {
		t.Fatalf("got %q, %v", got, err)
	}
}
