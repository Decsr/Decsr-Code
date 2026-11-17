package runner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
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
	want := []string{"--output-format", "stream-json", "--verbose", "--no-session-persistence", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "project,local", "--allowedTools", "", "--model", "claude-opus-5-5", "--effort", "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestClaudeMCPRoleKeepsUserServers(t *testing.T) {
	for _, agent := range []bool{false, true} {
		got := claudeArgv(t, CompletionRequest{Agent: agent, MCP: true})
		joined := strings.Join(got, " ")
		if strings.Contains(joined, "--strict-mcp-config") || strings.Contains(joined, "--mcp-config") {
			t.Errorf("agent=%v: mcp role still passes the MCP flags: %q", agent, got)
		}
		if !strings.Contains(joined, "--setting-sources project,local") {
			t.Errorf("agent=%v: --setting-sources missing: %q", agent, got)
		}
	}
}

func TestClaudeRunSetsNonessentialTrafficEnv(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	out := filepath.Join(dir, "env.txt")
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	script := "#!/bin/sh\nprintf '%s' \"$CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC\" > " + out + "\necho '{\"type\":\"result\",\"result\":\"ok\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ch, err := ClaudeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1" {
		t.Errorf("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = %q, want 1", data)
	}
}

func TestClaudeAgentModeAllowList(t *testing.T) {
	tools := AgentTools([]string{"go build ./...", "go test ./...", "go test ./..."}, nil, false)
	wantTools := []string{"Read", "Edit", "Write", "Glob", "Grep",
		"Bash(go:*)", "Bash(ls:*)", "Bash(cat:*)", "Bash(head:*)", "Bash(tail:*)", "Bash(wc:*)",
		"Bash(pwd:*)", "Bash(which:*)", "Bash(file:*)", "Bash(stat:*)", "Bash(du:*)", "Bash(tree:*)",
		"Bash(git status:*)", "Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)"}
	if !reflect.DeepEqual(tools, wantTools) {
		t.Fatalf("tools = %q", tools)
	}
	got := claudeArgv(t, CompletionRequest{Agent: true, AllowedTools: tools})
	want := []string{"--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "project,local",
		"--tools", "Read,Edit,Write,Glob,Grep,Bash",
		"--allowedTools", strings.Join(wantTools, ","),
		"--permission-mode", "dontAsk",
		"--model", "claude-opus-5-5", "--effort", "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
}

func TestClaudeAgentModeReadOnlyRole(t *testing.T) {
	tools := AgentTools([]string{"go test ./..."}, []string{"dpkg -l"}, true)
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
	tools := AgentTools([]string{"", "  ", "make (all)", "go vet ./..."}, nil, false)
	joined := strings.Join(tools, ",")
	if strings.Contains(joined, "(all)") || !strings.Contains(joined, "Bash(go:*)") {
		t.Errorf("tools = %q", tools)
	}
}

func TestAgentToolsSplitsCommandsIntoPrograms(t *testing.T) {
	cmds := []string{
		"cmake -S . -B build && cmake --build build",
		"/opt/Qt/bin/ctest --test-dir build; CC=gcc make -j4 | tee log",
		"cd sub && make || true",
	}
	tools := AgentTools(cmds, []string{"/opt/Qt/Tools/CMake/bin/cmake", " dpkg -l ", "dpkg -l"}, false)
	have := map[string]int{}
	for _, tl := range tools {
		have[tl]++
	}
	for _, w := range []string{"Bash(cmake:*)", "Bash(/opt/Qt/bin/ctest:*)", "Bash(make:*)", "Bash(tee:*)", "Bash(true:*)",
		"Bash(ls:*)", "Bash(/opt/Qt/Tools/CMake/bin/cmake:*)", "Bash(dpkg -l:*)"} {
		if have[w] != 1 {
			t.Errorf("%s count = %d, tools = %q", w, have[w], tools)
		}
	}
	for _, bad := range []string{"Bash(cd:*)", "Bash(CC=gcc:*)", "Bash(sed:*)", "Bash(find:*)", "Bash(rm:*)"} {
		if have[bad] != 0 {
			t.Errorf("unexpected %s in %q", bad, tools)
		}
	}
}

func TestAgentToolsReadOnlyHasNoGeneralBash(t *testing.T) {
	for _, tl := range AgentTools([]string{"go test ./..."}, []string{"dpkg -l"}, true) {
		if strings.HasPrefix(tl, "Bash(") && !strings.HasPrefix(tl, "Bash(git ") {
			t.Errorf("read-only role got %s", tl)
		}
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
