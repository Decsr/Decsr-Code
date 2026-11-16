package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCLIRunnersUseRequestDir checks that each CLI runner starts its process
// in CompletionRequest.Dir.
func TestCLIRunnersUseRequestDir(t *testing.T) {
	bins := t.TempDir()
	work := t.TempDir()
	out := filepath.Join(t.TempDir(), "pwd.txt")
	script := "#!/bin/sh\npwd > " + out + "\n"

	cases := map[string]func(bin string) LLMRunner{
		"claude":   func(bin string) LLMRunner { return ClaudeRunner{Binary: bin} },
		"codex":    func(bin string) LLMRunner { return CodexRunner{Binary: bin} },
		"copilot":  func(bin string) LLMRunner { return CopilotRunner{Binary: bin} },
		"opencode": func(bin string) LLMRunner { return OpenCodeRunner{Binary: bin} },
	}
	want, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			bin := filepath.Join(bins, name)
			if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(out)
			ch, err := mk(bin).Complete(context.Background(), CompletionRequest{
				Messages: []ConvMessage{{Role: "user", Content: "hi"}},
				Dir:      work,
			})
			if err == nil {
				for range ch { // drain
					continue
				}
			}
			got, rerr := os.ReadFile(out)
			if rerr != nil {
				t.Fatalf("fake %s did not run: %v", name, rerr)
			}
			if g := string(got); g != want+"\n" {
				t.Errorf("working dir = %q, want %q", g, want)
			}
		})
	}
}

// TestWrappersKeepDir checks that wrapper runners pass Dir through.
func TestWrappersKeepDir(t *testing.T) {
	var seen string
	inner := mockRunnerFunc(func(req CompletionRequest) { seen = req.Dir })
	for name, w := range map[string]LLMRunner{
		"effort": NewEffortRunner(inner, "high"),
		"budget": &BudgetRunner{inner: inner},
		"skill":  &SkillRunner{inner: inner},
	} {
		seen = ""
		ch, err := w.Complete(context.Background(), CompletionRequest{Dir: "/x"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for range ch {
			continue
		}
		if seen != "/x" {
			t.Errorf("%s: Dir = %q, want /x", name, seen)
		}
	}
}

type mockRunnerFunc func(CompletionRequest)

func (f mockRunnerFunc) Complete(_ context.Context, req CompletionRequest) (<-chan Token, error) {
	f(req)
	ch := make(chan Token)
	close(ch)
	return ch, nil
}
