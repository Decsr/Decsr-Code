package runner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeModelsOrder(t *testing.T) {
	want := []string{"fable", "opus", "sonnet", "haiku", "claude-fable-5-2", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
	got, err := ClaudeListModels()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %v, %v", got, err)
	}
}

func TestClaudeModelHint(t *testing.T) {
	if got := ClaudeModelHint("fable"); got != "latest Fable" {
		t.Errorf("fable hint = %q", got)
	}
	if got := ClaudeModelHint("claude-opus-5-5"); got != "" {
		t.Errorf("full name hint = %q", got)
	}
}

func writeClaudeSettings(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestClaudeSettingsModelIn(t *testing.T) {
	if m, ok := ClaudeSettingsModelIn(writeClaudeSettings(t, `{"model":"opus","x":1}`)); !ok || m != "opus" {
		t.Errorf("present: %q %v", m, ok)
	}
	if m, ok := ClaudeSettingsModelIn(t.TempDir()); ok || m != "" {
		t.Errorf("missing: %q %v", m, ok)
	}
	if m, ok := ClaudeSettingsModelIn(writeClaudeSettings(t, `{not json`)); ok || m != "" {
		t.Errorf("malformed: %q %v", m, ok)
	}
	if _, ok := ClaudeSettingsModelIn(writeClaudeSettings(t, `{"theme":"dark"}`)); ok {
		t.Error("no model key reported as set")
	}
}

func TestClaudeRunnerModelArgv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	bin := filepath.Join(dir, "fakeclaude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + out + "'\ncat >/dev/null\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(model string) []string {
		ch, err := ClaudeRunner{Binary: bin, Model: model}.Complete(context.Background(), CompletionRequest{
			Messages: []ConvMessage{{Role: "user", Content: "hi"}},
			Dir:      dir,
		})
		if err != nil {
			t.Fatal(err)
		}
		for range ch {
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	has := func(args []string, model string) bool {
		for i, a := range args {
			if a == "--model" && i+1 < len(args) && args[i+1] == model {
				return true
			}
		}
		return false
	}
	for _, model := range []string{"fable", "claude-opus-5-5"} {
		if args := run(model); !has(args, model) {
			t.Errorf("model %q not passed unchanged: %v", model, args)
		}
	}
	for _, a := range run("") {
		if a == "--model" {
			t.Error("--model passed for an empty model")
		}
	}
}
