package runner

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeParseJSONResponse(t *testing.T) {
	input := []byte(`{"type":"result","subtype":"success","result":"hello world","total_input_tokens":120,"total_output_tokens":45,"usage":{"input_tokens":120,"output_tokens":45,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`)

	text, usage, err := parseClaudeJSONResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if usage.InputTokens != 120 {
		t.Errorf("InputTokens: want 120, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 45 {
		t.Errorf("OutputTokens: want 45, got %d", usage.OutputTokens)
	}
	if usage.Estimated {
		t.Error("Estimated should be false for Claude JSON response")
	}
}

func TestClaudeParseJSONResponse_FallbackOnMalformed(t *testing.T) {
	input := []byte("plain text response without json")

	text, usage, err := parseClaudeJSONResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "plain text response without json" {
		t.Errorf("unexpected text: %q", text)
	}
	if !usage.Estimated {
		t.Error("Estimated should be true for fallback")
	}
}

func TestComputeDelta(t *testing.T) {
	cases := []struct {
		name    string
		already string
		next    string
		want    string
	}{
		{"empty next", "abc", "", ""},
		{"cumulative growth", "Hello", "Hello world", " world"},
		{"identical", "Hello", "Hello", ""},
		{"non-prefix treated as full chunk", "Hello", " world", " world"},
		{"first chunk", "", "Hi", "Hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeDelta(tc.already, tc.next); got != tc.want {
				t.Errorf("computeDelta(%q, %q) = %q, want %q", tc.already, tc.next, got, tc.want)
			}
		})
	}
}

func TestParseClaudeStreamEvent_AssistantText(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "},{"type":"text","text":"world"}]}}`)
	evt, err := parseClaudeStreamEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if evt.Type != "assistant" {
		t.Errorf("type: want %q, got %q", "assistant", evt.Type)
	}
	if got := assistantText(evt.Message.Content); got != "Hello world" {
		t.Errorf("assistantText: want %q, got %q", "Hello world", got)
	}
}

func TestParseClaudeStreamEvent_Result(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","result":"final","usage":{"input_tokens":10,"output_tokens":3}}`)
	evt, err := parseClaudeStreamEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if evt.Result != "final" {
		t.Errorf("result: want %q, got %q", "final", evt.Result)
	}
	if evt.Usage.InputTokens != 10 || evt.Usage.OutputTokens != 3 {
		t.Errorf("usage: want 10/3, got %d/%d", evt.Usage.InputTokens, evt.Usage.OutputTokens)
	}
}

// runStreamClaude drives streamClaudeOutput with a fake NDJSON stream and
// returns the concatenated emitted text.
func runStreamClaude(t *testing.T, ndjson string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "stream.ndjson")
	if err := os.WriteFile(f, []byte(ndjson), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cat", f)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ch := make(chan Token, 64)
	go streamClaudeOutput(cmd, stdout, &bytes.Buffer{}, CompletionRequest{}, "p", ch)
	var sb strings.Builder
	for tok := range ch {
		sb.WriteString(tok.Text)
	}
	return sb.String()
}

func TestStreamClaudeOutput_NoDuplicateFinalResult(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the fix."}]}}
{"type":"result","subtype":"success","result":"Here is the fix.","usage":{"input_tokens":10,"output_tokens":3}}
`
	if got, want := runStreamClaude(t, stream), "Let me look.Here is the fix."; got != want {
		t.Errorf("emitted text: want %q, got %q", want, got)
	}
}

func TestStreamClaudeOutput_ResultOnlyFallback(t *testing.T) {
	stream := `{"type":"result","subtype":"success","result":"only result","usage":{"input_tokens":10,"output_tokens":3}}
`
	if got, want := runStreamClaude(t, stream), "only result"; got != want {
		t.Errorf("emitted text: want %q, got %q", want, got)
	}
}

func TestParseClaudeStreamEvent_CachedTokensInPrompt(t *testing.T) {
	line := []byte(`{"type":"result","result":"x","usage":{"input_tokens":2,"output_tokens":5,"cache_creation_input_tokens":68894,"cache_read_input_tokens":100}}`)
	evt, err := parseClaudeStreamEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := evt.Usage.promptTokens(); got != 68996 {
		t.Errorf("promptTokens: want 68996, got %d", got)
	}
}

func TestParseClaudeJSONResponse_CachedTokensInPrompt(t *testing.T) {
	_, usage, err := parseClaudeJSONResponse([]byte(`{"result":"x","usage":{"input_tokens":2,"output_tokens":5,"cache_creation_input_tokens":68894,"cache_read_input_tokens":100}}`))
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 68996 {
		t.Errorf("InputTokens: want 68996, got %d", usage.InputTokens)
	}
}

func TestStreamClaudeOutput_PermissionDenials(t *testing.T) {
	stream := `{"type":"result","subtype":"success","result":"x","usage":{"input_tokens":1,"output_tokens":1},"permission_denials":[` +
		`{"tool_name":"Bash","tool_use_id":"a","tool_input":{"command":"cmake --build build","description":"d"}},` +
		`{"tool_name":"Bash","tool_use_id":"b","tool_input":{"command":"cmake --build build"}},` +
		`{"tool_name":"Write","tool_use_id":"c","tool_input":{"file_path":"/x"}}]}
`
	f := filepath.Join(t.TempDir(), "stream.ndjson")
	if err := os.WriteFile(f, []byte(stream), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cat", f)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ch := make(chan Token, 64)
	go streamClaudeOutput(cmd, stdout, &bytes.Buffer{}, CompletionRequest{}, "p", ch)
	var done Token
	for tok := range ch {
		if tok.Done {
			done = tok
		}
	}
	want := []Denial{{Tool: "Bash", Command: "cmake --build build"}, {Tool: "Write"}}
	if !reflect.DeepEqual(done.Denials, want) {
		t.Errorf("denials = %+v, want %+v", done.Denials, want)
	}
}

func TestClaudeLeadArgsAreReadOnly(t *testing.T) {
	args := claudeBaseArgs(true, false, LeadTools(), true)
	got := strings.Join(args, " ")
	want := `--output-format stream-json --verbose --strict-mcp-config --mcp-config {"mcpServers":{}} --setting-sources project,local ` +
		`--tools Read,Grep,Glob,Bash ` +
		`--allowedTools Read,Grep,Glob,Bash(git log:*),Bash(git diff:*),Bash(git status:*),Bash(git show:*),Bash(claude plugin list:*),Bash(claude plugin marketplace list:*),Bash(claude mcp list:*) --permission-mode dontAsk`
	if got != want {
		t.Errorf("args = %s", got)
	}
	for _, bad := range []string{"Write", "Edit", "--no-session-persistence"} {
		if strings.Contains(got, bad) {
			t.Errorf("a lead turn must not have %s", bad)
		}
	}
}

func TestContextWindow(t *testing.T) {
	for _, c := range []struct {
		runner, model string
		want          int
	}{
		{"claude", "claude-opus-5-5", 200000}, {"claude", "", 200000}, {"claude", "opus[1m]", 1000000},
		{"codex", "gpt-5", 128000}, {"deepseek", "x", 128000},
	} {
		if got := ContextWindow(c.runner, c.model); got != c.want {
			t.Errorf("ContextWindow(%q, %q) = %d, want %d", c.runner, c.model, got, c.want)
		}
	}
}
