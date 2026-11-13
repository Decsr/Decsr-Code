package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
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
	if !strings.Contains(f.got.SystemPrompt, "ROLEDESC") || !strings.Contains(f.got.SystemPrompt, "CTX") || !strings.Contains(f.got.SystemPrompt, "do it") {
		t.Errorf("system prompt missing parts: %q", f.got.SystemPrompt)
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
		WorkerRequest{RolePrompt: "skill says {{TASK}} and {{CONTEXT}}", Title: "REALTASK", Context: "CTX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := f.got.SystemPrompt
	if !strings.Contains(p, "skill says {{TASK}} and {{CONTEXT}}") {
		t.Errorf("placeholder in the role text was substituted:\n%s", p)
	}
	if strings.Count(p, "REALTASK") != 1 {
		t.Errorf("task appears %d times", strings.Count(p, "REALTASK"))
	}
}
