package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// drainClaude returns every token of a Claude stream run over fixed NDJSON lines.
func drainClaude(t *testing.T, ndjson string) []Token {
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
	var out []Token
	for tok := range ch {
		out = append(out, tok)
	}
	return out
}

func TestClaudeLiveUsageIsCumulativeAndFinalWins(t *testing.T) {
	stream := `{"type":"system","subtype":"init","model":"claude-opus-test"}
{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":2,"output_tokens":1,"cache_creation_input_tokens":10,"cache_read_input_tokens":5}}}
{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","name":"Read","input":{}}],"usage":{"input_tokens":2,"output_tokens":4,"cache_creation_input_tokens":10,"cache_read_input_tokens":5}}}
{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"b"}],"usage":{"input_tokens":3,"output_tokens":6,"cache_creation_input_tokens":0,"cache_read_input_tokens":20}}}
{"type":"result","subtype":"success","result":"ab","usage":{"input_tokens":5,"output_tokens":10,"cache_creation_input_tokens":10,"cache_read_input_tokens":25}}
`
	toks := drainClaude(t, stream)
	var progress []TokenUsage
	var models []string
	var done Token
	for _, tok := range toks {
		if tok.Progress != nil {
			progress = append(progress, *tok.Progress)
		}
		if tok.Model != "" {
			models = append(models, tok.Model)
		}
		if tok.Done {
			done = tok
		}
	}
	if len(models) != 1 || models[0] != "claude-opus-test" {
		t.Errorf("models = %v", models)
	}
	// m1 seen twice must not be counted twice: 17/1 -> 17/4 -> 17+23=40 / 10.
	want := []TokenUsage{
		{InputTokens: 17, CachedTokens: 15, OutputTokens: 1},
		{InputTokens: 17, CachedTokens: 15, OutputTokens: 4},
		{InputTokens: 40, CachedTokens: 35, OutputTokens: 10},
	}
	if fmt.Sprint(progress) != fmt.Sprint(want) {
		t.Errorf("progress = %+v, want %+v", progress, want)
	}
	if done.Usage == nil || *done.Usage != (TokenUsage{InputTokens: 40, CachedTokens: 35, OutputTokens: 10, ContextTokens: 23}) {
		t.Errorf("final usage = %+v", done.Usage)
	}
}

func TestDshStepEndEmitsCumulativeProgress(t *testing.T) {
	p := newDshParser("")
	var progress []TokenUsage
	var models []string
	for _, line := range []string{
		`{"type":"session","sessionId":"s","cwd":"/x"}`,
		`{"type":"status","phase":"step_end","usage":{"inputTokens":10,"outputTokens":2,"cacheReadTokens":5,"cacheWriteTokens":1}}`,
		`{"type":"status","phase":"step_end","usage":{"inputTokens":20,"outputTokens":3}}`,
	} {
		for _, tok := range p.feed([]byte(line)) {
			if tok.Progress != nil {
				progress = append(progress, *tok.Progress)
			}
			if tok.Model != "" {
				models = append(models, tok.Model)
			}
		}
	}
	if len(models) != 1 || models[0] != dshDefaultModel {
		t.Errorf("models = %v", models)
	}
	want := []TokenUsage{
		{InputTokens: 16, CachedTokens: 6, OutputTokens: 2},
		{InputTokens: 36, CachedTokens: 6, OutputTokens: 5},
	}
	if fmt.Sprint(progress) != fmt.Sprint(want) {
		t.Errorf("progress = %+v, want %+v", progress, want)
	}
	if _, usage := p.finish("prompt"); usage != want[1] {
		t.Errorf("final = %+v", usage)
	}
}

func TestDshModelFromRequestAndSession(t *testing.T) {
	if got := dshModelID("deepseek-official/deepseek-pro"); got != "deepseek-pro" {
		t.Errorf("provider/model = %q", got)
	}
	if got := dshModelID("deepseek-pro"); got != "deepseek-pro" {
		t.Errorf("bare model = %q", got)
	}
	p := newDshParser("x/asked")
	toks := p.feed([]byte(`{"type":"session","sessionId":"s","model":"reported"}`))
	if len(toks) != 1 || toks[0].Model != "reported" {
		t.Errorf("session model = %+v", toks)
	}
}

func TestOpenCodeStepFinishEmitsCumulativeProgress(t *testing.T) {
	p := newOpenCodeParser()
	var progress []TokenUsage
	for _, line := range []string{
		`{"type":"step_finish","part":{"tokens":{"input":10,"output":2,"cache":{"read":5,"write":0}}}}`,
		`{"type":"step_finish","part":{"tokens":{"input":4,"output":3,"cache":{"read":0,"write":2}}}}`,
	} {
		for _, tok := range p.feed([]byte(line)) {
			if tok.Progress != nil {
				progress = append(progress, *tok.Progress)
			}
		}
	}
	want := []TokenUsage{
		{InputTokens: 15, CachedTokens: 5, OutputTokens: 2},
		{InputTokens: 21, CachedTokens: 7, OutputTokens: 5},
	}
	if fmt.Sprint(progress) != fmt.Sprint(want) {
		t.Errorf("progress = %+v, want %+v", progress, want)
	}
	if _, usage := p.finish(nil, "prompt"); usage != want[1] {
		t.Errorf("final = %+v", usage)
	}
}

func TestCodexTurnCompletedProgressAndCache(t *testing.T) {
	p := newCodexParser()
	p.feed([]byte(`{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":60,"output_tokens":7}}`), time.Now())
	if p.progress == nil || *p.progress != (TokenUsage{InputTokens: 100, CachedTokens: 60, OutputTokens: 7}) {
		t.Errorf("progress = %+v", p.progress)
	}
	if p.usage.CachedTokens != 60 {
		t.Errorf("usage = %+v", p.usage)
	}
}

func TestCopilotProgressIsCumulative(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	ch := make(chan Token, 128)
	go streamCopilotOutput(cmd, nopReadCloser{strings.NewReader("hello world answer")}, &bytes.Buffer{}, "prompt text", ch)
	var last *TokenUsage
	for tok := range ch {
		if tok.Progress != nil {
			last = tok.Progress
		}
	}
	if last == nil || last.InputTokens == 0 || last.OutputTokens == 0 || !last.Estimated {
		t.Errorf("last progress = %+v", last)
	}
}

type nopReadCloser struct{ *strings.Reader }

func (nopReadCloser) Close() error { return nil }

func TestLiveUsageThrottle(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newLiveUsage(strings.Repeat("x", 40))
	l.now = func() time.Time { return now }
	ch := make(chan Token, 16)

	l.add(strings.Repeat("y", 8), ch) // first call always reports
	for i := 0; i < 5; i++ {
		now = now.Add(100 * time.Millisecond)
		l.add(strings.Repeat("y", 8), ch)
	}
	if len(ch) != 1 {
		t.Fatalf("progress tokens within one second = %d, want 1", len(ch))
	}
	now = now.Add(time.Second)
	l.add("yyyy", ch)
	if len(ch) != 2 {
		t.Fatalf("progress tokens after the interval = %d, want 2", len(ch))
	}
	<-ch
	got := <-ch
	want := TokenUsage{InputTokens: 10, OutputTokens: 13, Estimated: true} // 8*6+4 = 52 bytes
	if *got.Progress != want {
		t.Errorf("estimate = %+v, want %+v", *got.Progress, want)
	}
}

func TestOpenAIStreamProgressIsThrottled(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"word \"}}]}\n")
	}
	sb.WriteString("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":60}}\ndata: [DONE]\n")
	r := &OpenAIRunner{Provider: "openai", Model: "m"}
	ch := make(chan Token, 256)
	r.streamFromReader(strings.NewReader(sb.String()), "input text", ch)
	progress := 0
	var done Token
	for tok := range ch {
		if tok.Progress != nil {
			progress++
			if !tok.Progress.Estimated {
				t.Errorf("progress must be estimated: %+v", tok.Progress)
			}
		}
		if tok.Done {
			done = tok
		}
	}
	if progress < 1 || progress > 2 {
		t.Errorf("progress tokens = %d, want 1 or 2 for a stream read in well under a second", progress)
	}
	if done.Usage == nil || done.Usage.InputTokens != 12 || done.Usage.OutputTokens != 60 || done.Usage.Estimated {
		t.Errorf("final usage = %+v", done.Usage)
	}
}
