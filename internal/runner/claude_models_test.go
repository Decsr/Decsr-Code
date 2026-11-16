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

func TestClaudeRunnerDefaultsArgv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	bin := filepath.Join(dir, "fakeclaude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + out + "'\ncat >/dev/null\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	flag := func(model, effort, name string) string {
		ch, err := ClaudeRunner{Binary: bin, Model: model}.Complete(context.Background(), CompletionRequest{
			Messages: []ConvMessage{{Role: "user", Content: "hi"}},
			Dir:      dir,
			Effort:   effort,
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
		args := strings.Split(strings.TrimSpace(string(data)), "\n")
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	cases := []struct{ model, effort, wantModel, wantEffort string }{
		{"", "", "claude-opus-5-5", "high"},
		{"", "default", "claude-opus-5-5", "high"},
		{"fable", "low", "fable", "low"},
		{"claude-sonnet-5-5", "max", "claude-sonnet-5-5", "max"},
		{"", "xhigh", "claude-opus-5-5", "xhigh"},
	}
	for _, c := range cases {
		if got := flag(c.model, c.effort, "--model"); got != c.wantModel {
			t.Errorf("model %q effort %q: --model = %q, want %q", c.model, c.effort, got, c.wantModel)
		}
		if got := flag(c.model, c.effort, "--effort"); got != c.wantEffort {
			t.Errorf("model %q effort %q: --effort = %q, want %q", c.model, c.effort, got, c.wantEffort)
		}
	}
}

func TestEffectiveModelAndEffort(t *testing.T) {
	if EffectiveModel("claude", "") != ClaudeDefaultModel || EffectiveModel("claude", "haiku") != "haiku" || EffectiveModel("codex", "") != "" {
		t.Error("EffectiveModel")
	}
	if EffectiveEffort("claude", "default") != "high" || EffectiveEffort("claude", "") != "high" || EffectiveEffort("claude", "low") != "low" || EffectiveEffort("codex", "") != "" {
		t.Error("EffectiveEffort")
	}
}
