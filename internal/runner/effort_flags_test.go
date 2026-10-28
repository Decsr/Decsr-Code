package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeArgCaptureScript creates a fake CLI that records its argv to argsFile
// and prints stdout, then exits 0.
func writeArgCaptureScript(t *testing.T, stdout string) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "fake-cli")
	argsFile = filepath.Join(dir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nprintf '%%s' %q\n", argsFile, stdout)
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func readArgs(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestClaudeRunner_PassesEffort(t *testing.T) {
	bin, argsFile := writeArgCaptureScript(t, `{"type":"result","result":"ok"}`+"\n")
	r := ClaudeRunner{Binary: bin, Model: "sonnet"}
	ch, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
		Effort:   "high",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for range ch {
	}
	args := readArgs(t, argsFile)
	if !strings.Contains(args, "--effort\nhigh") {
		t.Fatalf("expected --effort high, got:\n%s", args)
	}
}

func TestCodexRunner_MapsEffort(t *testing.T) {
	bin, argsFile := writeArgCaptureScript(t, "ok\n")
	r := CodexRunner{Binary: bin, Model: "gpt-5.3-codex"}
	ch, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
		Effort:   "xhigh",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for range ch {
	}
	args := readArgs(t, argsFile)
	if !strings.Contains(args, `model_reasoning_effort="high"`) {
		t.Fatalf("expected codex high mapping, got:\n%s", args)
	}
}

func TestCopilotRunner_PassesEffort(t *testing.T) {
	bin, argsFile := writeArgCaptureScript(t, "ok\n")
	r := CopilotRunner{Binary: bin, Model: "claude-sonnet-4.6"}
	ch, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
		Effort:   "xhigh",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for range ch {
	}
	args := readArgs(t, argsFile)
	if !strings.Contains(args, "--effort\nxhigh") {
		t.Fatalf("expected --effort xhigh, got:\n%s", args)
	}
}
