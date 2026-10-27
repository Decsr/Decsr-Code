package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile_SymlinkEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dir, "link", []byte("pwned"), 0o600); err == nil {
		t.Fatal("expected error")
	}
	got, _ := os.ReadFile(outside)
	if string(got) != "original" {
		t.Errorf("outside file modified: %q", got)
	}
}
