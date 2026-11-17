package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

type workerFake struct {
	chunks []string
	got    runner.CompletionRequest
}

func (f *workerFake) Complete(_ context.Context, req runner.CompletionRequest) (<-chan runner.Token, error) {
	f.got = req
	ch := make(chan runner.Token, len(f.chunks)+2)
	for _, c := range f.chunks {
		ch <- runner.Token{Text: c}
	}
	ch <- runner.Token{Done: true, Usage: &runner.TokenUsage{InputTokens: 7, OutputTokens: 3}}
	close(ch)
	return ch, nil
}

func TestRunWorkerWritesFiles(t *testing.T) {
	root := t.TempDir()
	out := "**a/one.txt**\n```\nhello\n```\n\n**b/two.go**\n```go\npackage b\n```\n===SUMMARY===\nwrote two files\n"
	f := &workerFake{chunks: []string{out}}
	res, err := RunWorker(context.Background(), f, root, config.DefaultConfig(),
		WorkerRequest{RolePrompt: "ROLEDESC", Skills: []string{"s1"}, Model: "m", Title: "T", Description: "do it", Context: "CTX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %v", res.Files)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a/one.txt")); string(b) != "hello\n" {
		t.Errorf("one.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "b/two.go")); err != nil {
		t.Error(err)
	}
	if res.Summary != "wrote two files" {
		t.Errorf("summary = %q", res.Summary)
	}
	if res.Usage.InputTokens != 7 {
		t.Errorf("usage = %+v", res.Usage)
	}
	if !strings.Contains(f.got.SystemPrompt, "ROLEDESC") || !strings.Contains(f.got.SystemPrompt, "CTX") || strings.Contains(f.got.SystemPrompt, "do it") {
		t.Errorf("system prompt missing parts or holds the task: %q", f.got.SystemPrompt)
	}
	if !strings.Contains(f.got.Messages[0].Content, "do it") {
		t.Errorf("user message lacks the task: %q", f.got.Messages[0].Content)
	}
	sp := f.got.SystemPrompt
	if !(strings.Index(sp, "## Output format") < strings.Index(sp, "## Your role") && strings.Index(sp, "## Your role") < strings.Index(sp, "## Project context")) {
		t.Errorf("sections out of order: %q", sp)
	}
	if len(f.got.Skills) != 1 || f.got.Model != "m" {
		t.Errorf("request = %+v", f.got)
	}
}

func TestRunWorkerReportOnly(t *testing.T) {
	root := t.TempDir()
	f := &workerFake{chunks: []string{"l1\nl2\nl3\nl4\nl5\nl6\n"}}
	res, err := RunWorker(context.Background(), f, root, config.DefaultConfig(), WorkerRequest{Title: "r"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 0 {
		t.Errorf("files = %v", res.Files)
	}
	if res.Summary != "l2\nl3\nl4\nl5\nl6" {
		t.Errorf("summary = %q", res.Summary)
	}
}

func TestRunWorkerRejectsOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	f := &workerFake{chunks: []string{"**../evil.txt**\n```\nx\n```\n"}}
	res, _ := RunWorker(context.Background(), f, root, config.DefaultConfig(), WorkerRequest{Title: "x"}, nil)
	if len(res.Files) != 0 {
		t.Errorf("files = %v", res.Files)
	}
	if _, err := os.Stat(filepath.Join(parent, "evil.txt")); err == nil {
		t.Fatal("file written outside root")
	}
}

func TestRunWorkerEmpty(t *testing.T) {
	f := &workerFake{chunks: []string{"  \n"}}
	_, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(), WorkerRequest{Title: "x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "worker: empty response") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunWorkerStreams(t *testing.T) {
	f := &workerFake{chunks: []string{"ab", "cd"}}
	var got strings.Builder
	_, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(), WorkerRequest{Title: "x"}, func(s string) { got.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "abcd" {
		t.Errorf("streamed = %q", got.String())
	}
}

func TestRunWorkerAllowsTestFiles(t *testing.T) {
	root := t.TempDir()
	f := &workerFake{chunks: []string{"**x_test.go**\n```go\npackage x\n```\n"}}
	res, err := RunWorker(context.Background(), f, root, config.DefaultConfig(), WorkerRequest{Title: "x"}, nil)
	if err != nil || len(res.Files) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunWorkerSkippedWrites(t *testing.T) {
	root := t.TempDir()
	out := "**ok.txt**\n```\nhi\n```\n\n**.git/config**\n```\nx\n```\n\n**../evil.txt**\n```\nx\n```\n"
	f := &workerFake{chunks: []string{out}}
	res, err := RunWorker(context.Background(), f, root, config.DefaultConfig(), WorkerRequest{Title: "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0] != "ok.txt" {
		t.Errorf("files = %v", res.Files)
	}
	if len(res.Skipped) != 1 || !strings.HasPrefix(res.Skipped[0], ".git/config: ") || !strings.Contains(res.Skipped[0], "protected path") {
		t.Errorf("skipped = %v", res.Skipped)
	}
}

func TestRunWorkerAllFailedStillErrors(t *testing.T) {
	f := &workerFake{chunks: []string{"**.git/config**\n```\nx\n```\n"}}
	res, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(), WorkerRequest{Title: "x"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(res.Skipped) != 1 {
		t.Errorf("skipped = %v", res.Skipped)
	}
}

func TestRunWorkerReadOnlyWritesNothing(t *testing.T) {
	root := t.TempDir()
	f := &workerFake{chunks: []string{"**a.txt**\n```\nhi\n```\n===SUMMARY===\nreport\n"}}
	res, err := RunWorker(context.Background(), f, root, config.DefaultConfig(), WorkerRequest{Title: "x", ReadOnly: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(root, "a.txt")); serr == nil {
		t.Fatal("read-only worker wrote a file")
	}
	if len(res.Files) != 0 || len(res.Skipped) != 0 {
		t.Errorf("res = %+v", res)
	}
	if !strings.Contains(f.got.SystemPrompt, "read-only") || strings.Contains(f.got.SystemPrompt, "{{MODE}}") {
		t.Errorf("prompt lacks read-only mode: %q", f.got.SystemPrompt)
	}
	if res.Summary != "report" {
		t.Errorf("summary = %q", res.Summary)
	}
}

func TestRunWorkerWritableModeText(t *testing.T) {
	f := &workerFake{chunks: []string{"ok\n"}}
	if _, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(), WorkerRequest{Title: "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.got.SystemPrompt, "{{MODE}}") || strings.Contains(f.got.SystemPrompt, "read-only") {
		t.Errorf("prompt = %q", f.got.SystemPrompt)
	}
}

func TestRunWorkerIgnoresGuessedPaths(t *testing.T) {
	root := t.TempDir()
	// The first block has no path (only a comment hint in content): it must
	// not be written. The second has an explicit path.
	out := "Here is an example:\n```python\n# file: main.py\nprint('x')\n```\n\n**real/file.txt**\n```\nhi\n```\n===SUMMARY===\nok\n"
	res, err := RunWorker(context.Background(), &workerFake{chunks: []string{out}}, root, config.DefaultConfig(), WorkerRequest{Title: "T"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(root, "main.py")); serr == nil {
		t.Error("guessed path main.py was written")
	}
	if len(res.Files) != 1 || res.Files[0] != "real/file.txt" || len(res.Skipped) != 0 {
		t.Errorf("files=%v skipped=%v", res.Files, res.Skipped)
	}
}

func TestExtractFileBlocksMarksGuessed(t *testing.T) {
	bs := ExtractFileBlocks("```python\n# file: main.py\nprint('x')\n```\n")
	if len(bs) != 1 || bs[0].Path != "main.py" || !bs[0].Guessed {
		t.Fatalf("blocks = %+v", bs)
	}
	bs = ExtractFileBlocks("**a.txt**\n```\nx\n```\n")
	if len(bs) != 1 || bs[0].Guessed {
		t.Fatalf("blocks = %+v", bs)
	}
}

func TestRunWorkerDedupesFiles(t *testing.T) {
	root := t.TempDir()
	blk := "**greet.py**\n```\nprint(1)\n```\n\n**./test_greet.py**\n```\nx\n```\n"
	out := blk + blk + "===SUMMARY===\nok\n"
	res, err := RunWorker(context.Background(), &workerFake{chunks: []string{out}}, root, config.DefaultConfig(), WorkerRequest{Title: "T"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Files, ",") != "greet.py,test_greet.py" && strings.Join(res.Files, ",") != "greet.py,./test_greet.py" {
		t.Errorf("files = %v", res.Files)
	}
	if len(res.Files) != 2 {
		t.Errorf("files = %v", res.Files)
	}
}

func TestRunWorkerPlaceholderInRoleNotSubstituted(t *testing.T) {
	f := &workerFake{chunks: []string{"done\n===SUMMARY===\nok\n"}}
	_, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(),
		WorkerRequest{RolePrompt: "skill says {{MODE}} and {{CONTEXT}}", Title: "REALTASK", Context: "CTX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := f.got.SystemPrompt
	if !strings.Contains(p, "skill says {{MODE}} and {{CONTEXT}}") {
		t.Errorf("placeholder in the role text was substituted:\n%s", p)
	}
	if strings.Contains(p, "REALTASK") {
		t.Errorf("task is in the system prompt, it belongs in the user message:\n%s", p)
	}
	if f.got.Messages[0].Content != "REALTASK" {
		t.Errorf("user message = %q", f.got.Messages[0].Content)
	}
}

func TestRunWorkerReportsContextOnce(t *testing.T) {
	f := &workerFake{chunks: []string{"done\n===SUMMARY===\nok\n"}}
	var calls []int
	_, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(),
		WorkerRequest{Title: "T", Description: "do it", Context: "CTX", ReadOnly: true, OnContext: func(n int) { calls = append(calls, n) }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := tokenutil.EstimateTokens(f.got.SystemPrompt) + tokenutil.EstimateTokens(f.got.Messages[0].Content)
	if len(calls) != 1 || calls[0] != want || want == 0 {
		t.Errorf("OnContext calls = %v, want one call with %d", calls, want)
	}
}

// reasoningFake streams display-only reasoning (tool-use notices, heartbeats)
// next to the real text.
type reasoningFake struct{}

func (reasoningFake) Complete(context.Context, runner.CompletionRequest) (<-chan runner.Token, error) {
	ch := make(chan runner.Token, 4)
	ch <- runner.Token{Reasoning: "⚙ Read main.go\n"}
	ch <- runner.Token{Text: "done"}
	ch <- runner.Token{Reasoning: "⏳ still working… (30s)\n"}
	ch <- runner.Token{Done: true}
	close(ch)
	return ch, nil
}

func TestRunWorkerDoesNotStreamReasoningNotices(t *testing.T) {
	var got strings.Builder
	res, err := RunWorker(context.Background(), reasoningFake{}, t.TempDir(), config.DefaultConfig(), WorkerRequest{Title: "x", ReadOnly: true}, func(s string) { got.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "done" || res.Output != "done" {
		t.Errorf("streamed %q, output %q: reasoning notices must stay out of the node output", got.String(), res.Output)
	}
}

func TestRunWorkerAgentModeIgnoresFileBlocks(t *testing.T) {
	root := t.TempDir()
	out := "**a/one.txt**\n```\nhello\n```\n===SUMMARY===\nchanged one file\n"
	f := &workerFake{chunks: []string{out}}
	res, err := RunWorker(context.Background(), f, root, config.DefaultConfig(),
		WorkerRequest{Title: "T", Mode: config.ModeAgent, BuildCommands: []string{"go test ./..."}, AllowedCommands: []string{"dpkg -l"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a/one.txt")); err == nil {
		t.Error("agent mode must not write file blocks")
	}
	if len(res.Files) != 0 || len(res.Skipped) != 0 {
		t.Errorf("files = %v, skipped = %v", res.Files, res.Skipped)
	}
	if res.Summary != "changed one file" {
		t.Errorf("summary = %q", res.Summary)
	}
	if !f.got.Agent || !strings.Contains(strings.Join(f.got.AllowedTools, ","), "Bash(go:*)") || !strings.Contains(strings.Join(f.got.AllowedTools, ","), "Bash(dpkg -l:*)") || !strings.Contains(strings.Join(f.got.AllowedTools, ","), "Edit") {
		t.Errorf("request = %+v", f.got)
	}
	if !strings.Contains(f.got.SystemPrompt, "coding agent") || strings.Contains(f.got.SystemPrompt, "write its path in bold") {
		t.Errorf("prompt lacks the agent text:\n%s", f.got.SystemPrompt)
	}
}

func TestRunWorkerAgentModeReadOnlyPromptAndTools(t *testing.T) {
	f := &workerFake{chunks: []string{"all fine\n"}}
	if _, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(),
		WorkerRequest{Title: "T", Mode: config.ModeAgent, ReadOnly: true, BuildCommands: []string{"go test ./..."}}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.got.SystemPrompt, "read-only agent") {
		t.Errorf("prompt lacks the read-only agent text:\n%s", f.got.SystemPrompt)
	}
	tools := strings.Join(f.got.AllowedTools, ",")
	if strings.Contains(tools, "Edit") || strings.Contains(tools, "Write") || strings.Contains(tools, "Bash(go:") {
		t.Errorf("read-only tools = %s", tools)
	}
}

func TestRunWorkerTextModeRequestIsNotAgent(t *testing.T) {
	f := &workerFake{chunks: []string{"x\n===SUMMARY===\nok"}}
	if _, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(), WorkerRequest{Title: "T"}, nil); err != nil {
		t.Fatal(err)
	}
	if f.got.Agent || len(f.got.AllowedTools) != 0 || !strings.Contains(f.got.SystemPrompt, "write its path in bold") {
		t.Errorf("request = %+v", f.got)
	}
}

type progressFake struct{ toks []runner.Token }

func (f progressFake) Complete(_ context.Context, _ runner.CompletionRequest) (<-chan runner.Token, error) {
	ch := make(chan runner.Token, len(f.toks))
	for _, tk := range f.toks {
		ch <- tk
	}
	close(ch)
	return ch, nil
}

func TestRunWorkerLiveUsageModelAndFinal(t *testing.T) {
	f := progressFake{toks: []runner.Token{
		{Model: "real-model"},
		{Text: "hi\n===SUMMARY===\nok", Progress: &runner.TokenUsage{InputTokens: 5, OutputTokens: 1, Estimated: true}},
		{Progress: &runner.TokenUsage{InputTokens: 5, OutputTokens: 4, Estimated: true}},
		{Done: true, Usage: &runner.TokenUsage{InputTokens: 9, CachedTokens: 3, OutputTokens: 6}},
	}}
	var live []runner.TokenUsage
	var models []string
	res, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(), WorkerRequest{
		Title:   "T",
		OnUsage: func(u runner.TokenUsage) { live = append(live, u) },
		OnModel: func(m string) { models = append(models, m) },
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 2 || live[1].OutputTokens != 4 {
		t.Errorf("live = %+v", live)
	}
	if len(models) != 1 || models[0] != "real-model" {
		t.Errorf("models = %v", models)
	}
	if want := (runner.TokenUsage{InputTokens: 9, CachedTokens: 3, OutputTokens: 6}); res.Usage != want {
		t.Errorf("final usage = %+v, want %+v", res.Usage, want)
	}
}

func TestRunWorkerDoneWithoutUsageKeepsUsage(t *testing.T) {
	// Copilot style: additive deltas, then a Done token without usage.
	f := progressFake{toks: []runner.Token{
		{Usage: &runner.TokenUsage{InputTokens: 8, Estimated: true}},
		{Text: "hi\n===SUMMARY===\nok", Usage: &runner.TokenUsage{OutputTokens: 3, Estimated: true}},
		{Done: true},
	}}
	res, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(), WorkerRequest{Title: "T"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (runner.TokenUsage{InputTokens: 8, OutputTokens: 3, Estimated: true}); res.Usage != want {
		t.Errorf("usage = %+v, want %+v", res.Usage, want)
	}
	// Only progress and a Done token without usage: the last progress stays.
	g := progressFake{toks: []runner.Token{
		{Text: "hi\n===SUMMARY===\nok", Progress: &runner.TokenUsage{InputTokens: 4, OutputTokens: 2, Estimated: true}},
		{Done: true},
	}}
	res, err = RunWorker(context.Background(), g, t.TempDir(), config.DefaultConfig(), WorkerRequest{Title: "T"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.InputTokens != 4 || res.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", res.Usage)
	}
}
