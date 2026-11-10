package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
)

func TestCleanDecsrDirKeepsMemoryAndConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), artifacts.DirName)
	write := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{
		"memory/MEMORY.md", "memory.db", "memory.db-wal", "project.yaml",
		"requirements.md", "task_plan.md", "decsr.log",
	} {
		write(f)
	}

	kept, err := cleanDecsrDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{"memory/MEMORY.md", "memory.db", "memory.db-wal", "project.yaml", "requirements.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s should be kept: %v", f, err)
		}
	}
	for _, f := range []string{"task_plan.md", "decsr.log"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s should be removed", f)
		}
	}

	got := cleanSummary(dir, kept)
	want := "cleaned " + dir + " (kept: memory.db, memory.db-wal, memory/, project.yaml, requirements.md)"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}
