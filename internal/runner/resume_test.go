package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// collectTokens reads a channel to its end and returns the session id sent, the
// text and the error.
func collectTokens(t *testing.T, ch <-chan Token) (session, text string, err error) {
	t.Helper()
	var sb strings.Builder
	for tok := range ch {
		if tok.SessionID != "" {
			session = tok.SessionID
		}
		if tok.Error != nil && err == nil {
			err = tok.Error
		}
		sb.WriteString(tok.Text)
	}
	return session, sb.String(), err
}

func msgs(s string) []ConvMessage { return []ConvMessage{{Role: "user", Content: s}} }

func TestSessionIDCaptured(t *testing.T) {
	cases := []struct{ name, fixture, want string }{
		{"claude", "claude_stream.jsonl", "s1"},
		{"codex", "codex_exec.jsonl", "t1"},
		{"opencode", "opencode_stream.ndjson", "ses_1"},
		{"dsh", "dsh_stream.ndjson", "session-7b1c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, _ := fakeCLI(t, tc.name, tc.fixture)
			var r LLMRunner
			switch tc.name {
			case "claude":
				r = ClaudeRunner{Binary: bin}
			case "codex":
				r = CodexRunner{Binary: bin}
			case "opencode":
				r = OpenCodeRunner{Binary: bin}
			default:
				r = DshRunner{Binary: bin}
			}
			ch, err := r.Complete(context.Background(), CompletionRequest{Messages: msgs("hi"), KeepSession: true})
			if err != nil {
				t.Fatal(err)
			}
			if got, _, _ := collectTokens(t, ch); got != tc.want {
				t.Errorf("session id = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaudeSessionArgs(t *testing.T) {
	has := func(args []string, s string) bool {
		for _, a := range args {
			if a == s {
				return true
			}
		}
		return false
	}
	if !has(claudeBaseArgs(true, false, []string{"Read"}, false), "--no-session-persistence") {
		t.Error("a one-shot call must not keep its session")
	}
	if has(claudeBaseArgs(true, false, []string{"Read"}, true), "--no-session-persistence") {
		t.Error("a kept session must not pass --no-session-persistence")
	}
	bin, argsFile := fakeCLI(t, "claude", "claude_stream.jsonl")
	ch, err := ClaudeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{
		Messages: msgs("more"), SystemPrompt: "sys", ResumeID: "s1", Agent: true, AllowedTools: []string{"Read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	collectTokens(t, ch)
	args := readArgs(t, argsFile)
	if !strings.Contains(args, "--resume\ns1\n") || strings.Contains(args, "--no-session-persistence") {
		t.Errorf("args:\n%s", args)
	}
	if !strings.Contains(args, "--system-prompt\nsys\n") {
		t.Errorf("claude does not store its system prompt, so a resume sends it again:\n%s", args)
	}
	if !strings.Contains(args, "--permission-mode\ndontAsk") || !strings.Contains(args, "--allowedTools\nRead") {
		t.Errorf("a resume keeps the allow list:\n%s", args)
	}
}

func TestCodexResumeArgsAndPrompt(t *testing.T) {
	dir := t.TempDir()
	stdin := filepath.Join(dir, "stdin.txt")
	bin, argsFile := writeArgCaptureScript(t, "")
	// Capture stdin too.
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat > " + stdin + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ch, err := CodexRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{
		Model: "gpt-5.4", Effort: "high", Agent: true, SystemPrompt: "SYS", ResumeID: "t1", Messages: msgs("the reply"),
	})
	if err != nil {
		t.Fatal(err)
	}
	collectTokens(t, ch)
	args := readArgs(t, argsFile)
	want := "exec\nresume\n--json\n-c\nsandbox_mode=\"workspace-write\"\n--model\ngpt-5.4\n-c\nmodel_reasoning_effort=\"high\"\nt1\n-\n"
	if args != want {
		t.Errorf("args:\n%q\nwant\n%q", args, want)
	}
	in, _ := os.ReadFile(stdin)
	if strings.TrimSpace(string(in)) != "the reply" {
		t.Errorf("stdin = %q, want only the new message", in)
	}
}

func TestOpenCodeResumeArgsAndPrompt(t *testing.T) {
	dir := t.TempDir()
	stdin := filepath.Join(dir, "stdin.txt")
	bin, argsFile := writeArgCaptureScript(t, "")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat > " + stdin + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ch, err := OpenCodeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{
		Model: "prov/model", SystemPrompt: "SYS", ResumeID: "ses_1", Messages: msgs("the reply"),
	})
	if err != nil {
		t.Fatal(err)
	}
	collectTokens(t, ch)
	args := readArgs(t, argsFile)
	if !strings.Contains(args, "--session\nses_1\n") {
		t.Errorf("args:\n%s", args)
	}
	in, _ := os.ReadFile(stdin)
	if strings.TrimSpace(string(in)) != "the reply" {
		t.Errorf("stdin = %q, want only the new message", in)
	}
}

func TestDshResumeArgs(t *testing.T) {
	got := strings.Join(dshArgs(dshOptions{Agent: true, ResumeID: "session-1"}), " ")
	want := "--profile headless --json --session-id session-1 --permission-mode workspace-write --approval-policy never -"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	if strings.Contains(strings.Join(dshArgs(dshOptions{}), " "), "--session-id") {
		t.Error("a fresh run must not pass --session-id")
	}
}

func TestResumeGuard(t *testing.T) {
	run := func(toks ...Token) error {
		in := make(chan Token, len(toks))
		for _, tok := range toks {
			in <- tok
		}
		close(in)
		_, _, err := collectTokens(t, resumeGuard(in))
		return err
	}
	var re *ResumeError
	if err := run(Token{Error: errors.New("no session")}); !errors.As(err, &re) {
		t.Errorf("an error before any output is a ResumeError, got %v", err)
	}
	if err := run(Token{Text: "hi"}, Token{Error: errors.New("boom")}); errors.As(err, &re) || err == nil {
		t.Errorf("an error after output is not a ResumeError, got %v", err)
	}
	rl := &RateLimitError{Provider: "claude"}
	if err := run(Token{Error: rl}); errors.As(err, &re) {
		t.Errorf("a usage limit is not a ResumeError, got %v", err)
	}
}

func TestResumeFailsAtStart(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dsh")
	script := "#!/bin/sh\necho '{\"type\":\"error\",\"message\":\"session does not exist\"}'\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ch, err := DshRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: msgs("x"), ResumeID: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	var re *ResumeError
	if _, _, err := collectTokens(t, ch); !errors.As(err, &re) {
		t.Errorf("err = %v, want a ResumeError", err)
	}
}

func TestCanResume(t *testing.T) {
	for _, n := range []string{"claude", "codex", "opencode", "dsh"} {
		if !CanResume(n) {
			t.Errorf("%s should resume", n)
		}
	}
	if CanResume("ollama") || CanResume("copilot") {
		t.Error("an HTTP runner has no CLI conversation")
	}
}
