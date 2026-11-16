package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeCLI writes an executable that records its arguments in args.txt next to
// it and prints the fixture on stdout. It returns the binary and the args file.
func fakeCLI(t *testing.T, name, fixture string) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, name)
	argsFile = filepath.Join(dir, "args.txt")
	abs, err := filepath.Abs(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\ncat %q\n", argsFile, abs)
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

type collected struct {
	text  string
	acts  []Activity
	usage *TokenUsage
	err   error
}

func drain(t *testing.T, ch <-chan Token) collected {
	t.Helper()
	var c collected
	var sb strings.Builder
	timeout := time.After(20 * time.Second)
	for {
		select {
		case tok, ok := <-ch:
			if !ok {
				c.text = sb.String()
				return c
			}
			if tok.Error != nil {
				c.err = tok.Error
			}
			sb.WriteString(tok.Text)
			if tok.Activity != nil {
				c.acts = append(c.acts, *tok.Activity)
			}
			if tok.Usage != nil {
				u := *tok.Usage
				c.usage = &u
			}
		case <-timeout:
			t.Fatal("stream did not finish")
		}
	}
}

// seq renders activities as "id kind state title +added -removed" lines; IDs
// that are generated are replaced by their prefix.
func seq(acts []Activity) []string {
	var out []string
	for _, a := range acts {
		id := a.ID
		if i := strings.LastIndex(id, "-"); i > 0 && strings.Trim(id[i+1:], "0123456789") == "" {
			id = id[:i]
		}
		out = append(out, fmt.Sprintf("%s %s %s %q +%d -%d", id, a.Kind, a.State, a.Title, a.Added, a.Removed))
	}
	return out
}

func equalLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("activities differ\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestClip(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hell…"},
		{"héllo wörld", 6, "héllo…"},
		{"日本語のテキスト", 4, "日本語…"},
		{"abc", 1, "…"},
		{"abc", 0, ""},
	}
	for _, c := range cases {
		got := Clip(c.in, c.n)
		if got != c.want {
			t.Errorf("Clip(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Clip(%q,%d) is not valid UTF-8", c.in, c.n)
		}
	}
}

func TestClaudeStreamActivities(t *testing.T) {
	bin, _ := fakeCLI(t, "claude", "claude_stream.jsonl")
	ch, err := ClaudeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := drain(t, ch)
	if c.err != nil {
		t.Fatal(c.err)
	}
	if c.text != "All done." {
		t.Errorf("text = %q", c.text)
	}
	if c.usage == nil || c.usage.InputTokens != 10 || c.usage.OutputTokens != 5 || c.usage.Estimated {
		t.Errorf("usage = %+v", c.usage)
	}
	equalLines(t, seq(c.acts), []string{
		`think thinking done "Thinking (455 chars)" +0 -0`,
		`toolu_read read running "main.go" +0 -0`,
		`toolu_read read done "main.go" +0 -0`,
		`toolu_edit edit running "main.go" +4 -2`,
		`toolu_edit edit done "main.go" +4 -2`,
		`toolu_bash command running "go test ./..." +0 -0`,
		`toolu_bash command failed "go test ./..." +0 -0`,
		`toolu_write write running "new.txt" +3 -0`,
		`toolu_grep search running "TODO" +0 -0`,
		`toolu_mcp tool running "mcp__x__thing" +0 -0`,
		`toolu_write write done "new.txt" +3 -0`,
		`msg message done "All done." +0 -0`,
	})
	if d := []rune(c.acts[0].Detail); len(d) > thinkingExcerpt {
		t.Errorf("thinking detail has %d runes", len(d))
	}
	if got := c.acts[6].Detail; got != "FAIL main" {
		t.Errorf("failed tool detail = %q", got)
	}
	if got := c.acts[4].Detail; got != "edited" {
		t.Errorf("tool_result block list detail = %q", got)
	}
	if c.acts[2].ID != c.acts[1].ID || c.acts[2].Duration < 0 {
		t.Errorf("result must reuse the tool_use ID: %+v %+v", c.acts[1], c.acts[2])
	}
}

func TestOpenCodeStreamActivities(t *testing.T) {
	bin, argsFile := fakeCLI(t, "opencode", "opencode_stream.ndjson")
	ch, err := OpenCodeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{
		Model: "prov/model", Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := drain(t, ch)
	if c.err != nil {
		t.Fatal(c.err)
	}
	if c.text != "All done." {
		t.Errorf("text = %q", c.text)
	}
	if c.usage == nil || c.usage.InputTokens != 100 || c.usage.OutputTokens != 20 || c.usage.Estimated {
		t.Errorf("usage = %+v", c.usage)
	}
	equalLines(t, seq(c.acts), []string{
		`prt_r thinking done "Thinking (21 chars)" +0 -0`,
		`prt_read read done "a.go" +0 -0`,
		`prt_edit edit running "a.go" +2 -1`,
		`prt_edit edit done "a.go" +3 -1`,
		`prt_bash command failed "go build ./..." +0 -0`,
		`prt_ls command done "ls" +0 -0`,
		`prt_write write done "b.txt" +2 -0`,
		`prt_grep search done "TODO" +0 -0`,
	})
	if c.acts[4].Detail != "exit status 2" || c.acts[4].Duration != time.Second {
		t.Errorf("failed bash = %+v", c.acts[4])
	}
	if e := c.acts[5].ExitCode; e == nil || *e != 0 {
		t.Errorf("ls exit code = %v", e)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"run", "--format", "json", "prov/model"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args missing %q: %s", want, args)
		}
	}
}

func TestOpenCodeStreamTextMatchesBatchExtraction(t *testing.T) {
	data, err := os.ReadFile("testdata/opencode_stream.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	text, usage := extractOpenCodeResponseWithUsage(data, "input")
	if text != "All done." || usage.Estimated || usage.InputTokens != 100 {
		t.Errorf("batch parse = %q %+v", text, usage)
	}
}

func TestOpenCodeStreamsBeforeExit(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	marker := filepath.Join(dir, "finished")
	script := "#!/bin/sh\n" +
		`echo '{"type":"tool_use","part":{"id":"p1","tool":"bash","state":{"status":"completed","input":{"command":"ls"},"output":"x"}}}'` + "\n" +
		`echo '{"type":"text","part":{"text":"live"}}'` + "\n" +
		"sleep 1\n" +
		"touch " + marker + "\n" +
		`echo '{"type":"text","part":{"text":" end"}}'` + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ch, err := OpenCodeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var gotAct, gotText bool
	var all strings.Builder
	for tok := range ch {
		if tok.Error != nil {
			t.Fatal(tok.Error)
		}
		_, statErr := os.Stat(marker)
		running := statErr != nil
		if tok.Activity != nil && running {
			gotAct = true
		}
		if tok.Text == "live" && running {
			gotText = true
		}
		all.WriteString(tok.Text)
	}
	if !gotAct || !gotText {
		t.Errorf("activity and text must arrive before the process exits (activity=%v text=%v)", gotAct, gotText)
	}
	if all.String() != "live end" {
		t.Errorf("text = %q", all.String())
	}
}

func TestOpenCodeFallbackTextWhenNoTextEvents(t *testing.T) {
	bin := writeFakeOpenCode(t, "plain answer\n")
	ch, err := OpenCodeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := drain(t, ch)
	if c.err != nil || c.text != "plain answer" || c.usage == nil || !c.usage.Estimated {
		t.Errorf("got %+v", c)
	}
}

func TestOpenCodeErrorsArriveAsTokens(t *testing.T) {
	bin := writeFakeOpenCode(t, `{"type":"error","error":{"data":{"message":"429 too many requests"}}}`+"\n")
	ch, err := OpenCodeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := drain(t, ch)
	var rl *RateLimitError
	if !errors.As(c.err, &rl) {
		t.Fatalf("want a rate limit error token, got %v", c.err)
	}
	if len(c.acts) != 1 || c.acts[0].Kind != ActError || c.acts[0].Title != "429 too many requests" {
		t.Errorf("acts = %+v", c.acts)
	}
}

func TestCodexExecActivities(t *testing.T) {
	bin, argsFile := fakeCLI(t, "codex", "codex_exec.jsonl")
	ch, err := CodexRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Model: "gpt-5.4", Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := drain(t, ch)
	if c.err != nil {
		t.Fatal(c.err)
	}
	// Plain `codex exec` prints only the final agent message.
	if c.text != "All done.\n" {
		t.Errorf("text = %q", c.text)
	}
	if c.usage == nil || c.usage.InputTokens != 1000 || c.usage.OutputTokens != 50 || c.usage.Estimated {
		t.Errorf("usage = %+v", c.usage)
	}
	equalLines(t, seq(c.acts[:11]), []string{
		`item_0 thinking done "Thinking (15 chars)" +0 -0`,
		`item_1 command running "bash -lc ls" +0 -0`,
		`item_1 command done "bash -lc ls" +0 -0`,
		`item_2 command running "go test" +0 -0`,
		`item_2 command failed "go test" +0 -0`,
		`item_3#0 edit done "a.go" +0 -0`,
		`item_3#1 write done "b.go" +0 -0`,
		`item_4 message done "working on it" +0 -0`,
		`item_5 tool running "Todo list (0/2 done)" +0 -0`,
		`item_5 tool done "Todo list (1/2 done)" +0 -0`,
		`item_6 search done "go slices package" +0 -0`,
	})
	if len(c.acts) != 13 {
		t.Fatalf("want 13 activities, got %d", len(c.acts))
	}
	last := c.acts[len(c.acts)-2:]
	if last[0].Kind != ActError || last[0].Title != "stream hiccup" {
		t.Errorf("error activity = %+v", last[0])
	}
	if last[1].Kind != ActMessage || last[1].Title != "All done." || last[1].ID != "item_8" {
		t.Errorf("final message activity = %+v", last[1])
	}
	if e := c.acts[2].ExitCode; e == nil || *e != 0 || c.acts[2].Detail != "a.go\n" {
		t.Errorf("command result = %+v", c.acts[2])
	}
	if e := c.acts[4].ExitCode; e == nil || *e != 2 {
		t.Errorf("failed command exit code = %v", e)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"exec", "--sandbox", "read-only", "--json", "--model", "gpt-5.4"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args missing %q: %s", want, args)
		}
	}
}

func TestCodexPlainOutputStillWorks(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'plain reply'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ch, err := CodexRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := drain(t, ch)
	if c.err != nil || c.text != "plain reply\n" || c.usage == nil {
		t.Errorf("got %+v", c)
	}
}

func TestCodexParserNeverPanicsOnJunk(t *testing.T) {
	p := newCodexParser()
	for _, l := range []string{``, `{`, `null`, `[]`, `{"type":1}`, `{"type":"item.completed"}`, `{"type":"item.completed","item":{"type":"file_change","changes":null}}`,
		`{"type":"item.completed","item":{"type":"command_execution","exit_code":"x"}}`, `{"type":"turn.completed"}`} {
		p.feed([]byte(l), time.Now())
	}
	_ = p.text()
}

func TestThinkingTrackerCoalesces(t *testing.T) {
	var tr thinkingTracker
	now := time.Now()
	first := tr.add("abc", now)
	if first == nil || first.State != ActRunning || first.Kind != ActThinking {
		t.Fatalf("first = %+v", first)
	}
	if a := tr.add("def", now.Add(10*time.Millisecond)); a != nil {
		t.Errorf("update within the rate limit must be dropped, got %+v", a)
	}
	upd := tr.add(strings.Repeat("x", 400), now.Add(time.Second))
	if upd == nil || upd.ID != first.ID || upd.Title != "Thinking (406 chars)" {
		t.Errorf("update = %+v", upd)
	}
	if len([]rune(upd.Detail)) != thinkingExcerpt {
		t.Errorf("detail runes = %d", len([]rune(upd.Detail)))
	}
	done := tr.finish(now.Add(2 * time.Second))
	if done == nil || done.ID != first.ID || done.State != ActDone || done.Duration != 2*time.Second {
		t.Errorf("done = %+v", done)
	}
	if tr.finish(now) != nil {
		t.Error("finish without thought must emit nothing")
	}
}

func TestOpenAIReasoningDeltasBecomeOneThinkingItem(t *testing.T) {
	sse := "" +
		`data: {"choices":[{"delta":{"reasoning_content":"hmm "}}]}` + "\n" +
		`data: {"choices":[{"delta":{"reasoning_content":"ok"}}]}` + "\n" +
		`data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n" +
		`data: [DONE]` + "\n"
	ch := make(chan Token, 32)
	r := &OpenAIRunner{Provider: "openai"}
	go r.streamFromReader(strings.NewReader(sse), "in", ch)
	c := drain(t, ch)
	if c.text != "hi" {
		t.Errorf("text = %q", c.text)
	}
	if len(c.acts) < 2 || c.acts[0].State != ActRunning || c.acts[len(c.acts)-1].State != ActDone {
		t.Fatalf("acts = %+v", c.acts)
	}
	for _, a := range c.acts {
		if a.ID != c.acts[0].ID || a.Kind != ActThinking {
			t.Errorf("all updates must share one thinking item: %+v", a)
		}
	}
	if got := c.acts[len(c.acts)-1].Title; got != "Thinking (6 chars)" {
		t.Errorf("title = %q", got)
	}
}
