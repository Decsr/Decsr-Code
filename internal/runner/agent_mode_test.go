package runner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// argvOf runs a fake CLI that records its arguments one per line and returns
// them.
func argvOf(t *testing.T, name string, run func(bin string) error) []string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, name)
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\necho '{\"type\":\"result\",\"result\":\"ok\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run(bin); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func claudeArgv(t *testing.T, req CompletionRequest) []string {
	t.Helper()
	req.Messages = []ConvMessage{{Role: "user", Content: "hi"}}
	return argvOf(t, "claude", func(bin string) error {
		ch, err := ClaudeRunner{Binary: bin}.Complete(context.Background(), req)
		if err != nil {
			return err
		}
		drain(t, ch)
		return nil
	})
}

func TestClaudeTextModeKeepsToolsOff(t *testing.T) {
	got := claudeArgv(t, CompletionRequest{})
	want := []string{"--output-format", "stream-json", "--verbose", "--no-session-persistence", "--allowedTools", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestClaudeAgentModeAllowList(t *testing.T) {
	tools := AgentTools([]string{"go build ./...", "go test ./...", "go test ./..."}, false)
	wantTools := []string{"Read", "Edit", "Write", "Glob", "Grep",
		"Bash(go build ./...:*)", "Bash(go test ./...:*)",
		"Bash(git status:*)", "Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)"}
	if !reflect.DeepEqual(tools, wantTools) {
		t.Fatalf("tools = %q", tools)
	}
	got := claudeArgv(t, CompletionRequest{Agent: true, AllowedTools: tools})
	want := []string{"--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--tools", "Read,Edit,Write,Glob,Grep,Bash",
		"--allowedTools", strings.Join(wantTools, ","),
		"--permission-mode", "dontAsk"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
}

func TestClaudeAgentModeReadOnlyRole(t *testing.T) {
	tools := AgentTools([]string{"go test ./..."}, true)
	wantTools := []string{"Read", "Glob", "Grep", "Bash(git status:*)", "Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)"}
	if !reflect.DeepEqual(tools, wantTools) {
		t.Fatalf("read-only tools = %q", tools)
	}
	got := claudeArgv(t, CompletionRequest{Agent: true, AllowedTools: tools})
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "Edit") || strings.Contains(joined, "Write") || strings.Contains(joined, "go test") {
		t.Errorf("read-only argv has write or build tools: %q", got)
	}
	if !strings.Contains(joined, "--tools Read,Glob,Grep,Bash ") {
		t.Errorf("argv = %q", got)
	}
}

func TestAgentToolsSkipsUnsafeCommands(t *testing.T) {
	tools := AgentTools([]string{"", "  ", "make (all)", "go vet ./..."}, false)
	joined := strings.Join(tools, ",")
	if strings.Contains(joined, "make") || !strings.Contains(joined, "Bash(go vet ./...:*)") {
		t.Errorf("tools = %q", tools)
	}
}

func codexArgv(t *testing.T, agent bool) []string {
	t.Helper()
	return argvOf(t, "codex", func(bin string) error {
		ch, err := CodexRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Agent: agent, Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
		if err != nil {
			return err
		}
		drain(t, ch)
		return nil
	})
}

func TestCodexSandboxFollowsMode(t *testing.T) {
	if got := codexArgv(t, false); !reflect.DeepEqual(got, []string{"exec", "--sandbox", "read-only", "--json"}) {
		t.Errorf("text argv = %q", got)
	}
	if got := codexArgv(t, true); !reflect.DeepEqual(got, []string{"exec", "--sandbox", "workspace-write", "--json"}) {
		t.Errorf("agent argv = %q", got)
	}
}
