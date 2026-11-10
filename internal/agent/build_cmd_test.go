package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/executil"
)

func TestSafeBuildCmdRewritesArtifactWritingBuilds(t *testing.T) {
	want := goBuildCmd
	for _, in := range []string{"go build", "go build .", "go build ./...", "  go build ./... "} {
		if got := safeBuildCmd(in); got != want {
			t.Errorf("safeBuildCmd(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"go build -o bin/app .", "go test ./...", "go build ./cmd/app"} {
		if got := safeBuildCmd(in); got != in {
			t.Errorf("safeBuildCmd(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestParseCmdsRewritesGoBuild(t *testing.T) {
	got := parseCmds("$ go build\n$ go build .\n$ go test ./...")
	want := []string{goBuildCmd, goBuildCmd, "go test ./..."}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cmd %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGoBuildCmdLeavesNoBinaryInMainPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/wordcount\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := executil.NewRunner(dir).RunUnchecked(goBuildCmd)
	if res.ExitCode != 0 {
		t.Fatalf("build failed: %s %s", res.Stdout, res.Stderr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "go.mod" && e.Name() != "main.go" {
			t.Errorf("unexpected artifact %q left in project", e.Name())
		}
	}
}
