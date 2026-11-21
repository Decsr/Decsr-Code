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
	var sb strings.Builder
	for _, tok := range streamClaudeTokens(t, ndjson) {
		sb.WriteString(tok.Text)
	}
	return sb.String()
}

// streamClaudeTokens drives streamClaudeOutput with a fake NDJSON stream and
// returns the tokens it sent.
func streamClaudeTokens(t *testing.T, ndjson string) []Token {
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
	var toks []Token
	for tok := range ch {
		toks = append(toks, tok)
	}
	return toks
}

func TestStreamClaudeOutput_MarksNewTurns(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the fix."}]}}
{"type":"result","subtype":"success","result":"Here is the fix.","usage":{"input_tokens":10,"output_tokens":3}}
`
	var turns []bool
	for _, tok := range streamClaudeTokens(t, stream) {
		if tok.Text != "" {
			turns = append(turns, tok.NewTurn)
		}
	}
	if len(turns) != 2 || turns[0] || !turns[1] {
		t.Errorf("new-turn flags = %v, want [false true]", turns)
	}
}

func TestStreamClaudeOutput_NoDuplicateFinalResult(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the fix."}]}}
{"type":"result","subtype":"success","result":"Here is the fix.","usage":{"input_tokens":10,"output_tokens":3}}
`
	if got, want := runStreamClaude(t, stream), "Let me look.\n\nHere is the fix."; got != want {
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
	want := `--output-format stream-json --verbose --include-partial-messages --strict-mcp-config --mcp-config {"mcpServers":{}} --setting-sources project,local ` +
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

// partialStream is a run with partial messages: a thinking block and a text
// block streamed in pieces, a tool call, then a second message. Each full
// assistant event arrives after the stream events of its message.
const partialStream = `{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1","role":"assistant","content":[]}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"secret plan"}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_stop","index":0},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Let me "}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"look."}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_stop","index":1},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu_1","name":"Read","input":{}}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"a.go\"}"}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_stop","index":2},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"message_stop"},"parent_tool_use_id":null}
{"type":"assistant","message":{"id":"msg_1","content":[{"type":"thinking","thinking":"secret plan"}]},"parent_tool_use_id":null}
{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"Let me look."}]},"parent_tool_use_id":null}
{"type":"assistant","message":{"id":"msg_1","content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"a.go"}}]},"parent_tool_use_id":null}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_1","content":"ok"}]},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_2","role":"assistant","content":[]}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Here is "}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"the fix."}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_stop","index":0},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"message_stop"},"parent_tool_use_id":null}
{"type":"assistant","message":{"id":"msg_2","content":[{"type":"text","text":"Here is the fix."}]},"parent_tool_use_id":null}
{"type":"result","subtype":"success","result":"Here is the fix.","usage":{"input_tokens":10,"output_tokens":3}}
`

func TestStreamClaudeOutput_PartialTextStreamsInPieces(t *testing.T) {
	var pieces []string
	var turns []bool
	for _, tok := range streamClaudeTokens(t, partialStream) {
		if tok.Text != "" {
			pieces = append(pieces, tok.Text)
			turns = append(turns, tok.NewTurn)
		}
	}
	wantPieces := []string{"Let me ", "look.", "\n\nHere is ", "the fix."}
	if !reflect.DeepEqual(pieces, wantPieces) {
		t.Errorf("pieces = %q, want %q", pieces, wantPieces)
	}
	// Only the first piece of the second message starts a new turn, and the
	// full assistant events add no text a second time.
	if !reflect.DeepEqual(turns, []bool{false, false, true, false}) {
		t.Errorf("new-turn flags = %v", turns)
	}
}

func TestStreamClaudeOutput_PartialThinkingIsNotText(t *testing.T) {
	var think []Activity
	var text strings.Builder
	for _, tok := range streamClaudeTokens(t, partialStream) {
		text.WriteString(tok.Text)
		if tok.Activity != nil && tok.Activity.Kind == ActThinking {
			think = append(think, *tok.Activity)
		}
	}
	if strings.Contains(text.String(), "secret") {
		t.Errorf("thinking leaked into the text: %q", text.String())
	}
	if len(think) != 2 || think[0].State != ActRunning || think[1].State != ActDone || think[0].ID != think[1].ID {
		t.Errorf("thinking activities = %+v, want one block running then done", think)
	}
}

func TestStreamClaudeOutput_PartialToolLineOnce(t *testing.T) {
	var notes, tools int
	for _, tok := range streamClaudeTokens(t, partialStream) {
		if strings.HasPrefix(tok.Reasoning, "⚙ Read a.go") {
			notes++
		}
		if tok.Activity != nil && tok.Activity.Kind == ActRead && tok.Activity.State == ActRunning {
			tools++
		}
	}
	if notes != 1 || tools != 1 {
		t.Errorf("tool notices = %d, running tool activities = %d, want 1 each", notes, tools)
	}
}

func TestStreamClaudeOutput_PartialSubAgentTextIgnored(t *testing.T) {
	stream := `{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_s"}},"parent_tool_use_id":"tu_9"}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"inner"}},"parent_tool_use_id":"tu_9"}
{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1"}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"outer"}},"parent_tool_use_id":null}
{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"outer"}]},"parent_tool_use_id":null}
{"type":"result","subtype":"success","result":"outer","usage":{"input_tokens":1,"output_tokens":1}}
`
	if got := runStreamClaude(t, stream); got != "outer" {
		t.Errorf("text = %q, want only the node's own", got)
	}
}

func TestStreamClaudeOutput_PartialMissedDeltaIsCompleted(t *testing.T) {
	stream := `{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1"}},"parent_tool_use_id":null}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}},"parent_tool_use_id":null}
{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"Hello"}]},"parent_tool_use_id":null}
{"type":"result","subtype":"success","result":"Hello","usage":{"input_tokens":1,"output_tokens":1}}
`
	if got := runStreamClaude(t, stream); got != "Hello" {
		t.Errorf("text = %q", got)
	}
}

// TestStreamClaudeOutput_RealPartialRun feeds a captured run: one message with
// text and a Read call (one assistant event per content block, same id), then
// a second message with text only.
func TestStreamClaudeOutput_RealPartialRun(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "claude_partial_tool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	const (
		first  = "I'll read a.txt and summarize what it says."
		second = `The file a.txt contains only the word "hello" on its first line. Nothing else appears in the file.`
	)
	var text, answer strings.Builder
	newTurns, reads := 0, 0
	for _, tok := range streamClaudeTokens(t, string(raw)) {
		if tok.NewTurn {
			newTurns++
			answer.Reset() // the worker takes the last turn as the answer
		}
		text.WriteString(tok.Text)
		answer.WriteString(tok.Text)
		if strings.HasPrefix(tok.Reasoning, "⚙ Read") {
			reads++
		}
	}
	if want := first + "\n\n" + second; text.String() != want {
		t.Errorf("text = %q, want %q", text.String(), want)
	}
	if got := strings.TrimSpace(answer.String()); got != second {
		t.Errorf("answer = %q", got)
	}
	if newTurns != 1 || reads != 1 {
		t.Errorf("new turns = %d, read notices = %d, want 1 each", newTurns, reads)
	}
}
