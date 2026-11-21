package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestExtractOpenCodeResponse_WithUsageEvent(t *testing.T) {
	ndjson := []byte(`{"type":"text","part":{"text":"hello "}}
{"type":"text","part":{"text":"world"}}
{"type":"usage","usage":{"input_tokens":80,"output_tokens":12}}
`)

	text, usage := extractOpenCodeResponseWithUsage(ndjson, "input prompt")
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if usage.InputTokens != 80 {
		t.Errorf("InputTokens: want 80, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 12 {
		t.Errorf("OutputTokens: want 12, got %d", usage.OutputTokens)
	}
	if usage.Estimated {
		t.Error("Estimated should be false when usage event found")
	}
}

func TestExtractOpenCodeResponse_FallbackEstimate(t *testing.T) {
	ndjson := []byte(`{"type":"text","part":{"text":"hello world"}}
`)

	text, usage := extractOpenCodeResponseWithUsage(ndjson, "some input prompt for estimation")
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if !usage.Estimated {
		t.Error("Estimated should be true when no usage event in stream")
	}
	if usage.OutputTokens <= 0 {
		t.Error("expected positive OutputTokens on fallback estimate")
	}
	if usage.InputTokens <= 0 {
		t.Error("expected positive InputTokens on fallback estimate (local-model monitoring relies on this)")
	}
}

func writeFakeOpenCode(t *testing.T, stdout string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.ndjson"), []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "opencode")
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	script := "#!/bin/sh\ncat \"" + filepath.Join(dir, "out.ndjson") + "\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestOpenCodeRunRaw_AnswerTextMentioningRateLimitIsNotRateLimit(t *testing.T) {
	bin := writeFakeOpenCode(t, `{"type":"text","part":{"text":"To handle a rate limit, back off on HTTP 429 (too many requests)."}}`+"\n")
	out, err := OpenCodeRunner{Binary: bin}.runRaw(context.Background(), "hi", "m", "", "")
	if err != nil {
		t.Fatalf("answer text must not be classified as rate limit: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected output")
	}
}

func TestOpenCodeRunRaw_ErrorEventIsRateLimit(t *testing.T) {
	bin := writeFakeOpenCode(t, `{"type":"error","error":{"data":{"message":"429 too many requests"}}}`+"\n")
	_, err := OpenCodeRunner{Binary: bin}.runRaw(context.Background(), "hi", "m", "", "")
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected RateLimitError, got %v", err)
	}
}

func TestOpenCodeRunRaw_AppliesEffortVariant(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	argsFile := filepath.Join(dir, "args.txt")
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\n", argsFile)
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := (OpenCodeRunner{Binary: bin}).runRaw(context.Background(), "hi", "prov/model", "high", ""); err != nil {
		t.Fatalf("runRaw: %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "prov/model#high") {
		t.Fatalf("expected effort variant in args, got:\n%s", args)
	}
}

func TestOpenCodeRunRaw_LocalModelSkipsEffortVariant(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	argsFile := filepath.Join(dir, "args.txt")
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\n", argsFile)
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := (OpenCodeRunner{Binary: bin}).runRaw(context.Background(), "hi", "qwen2.5-coder:3b", "high", ""); err != nil {
		t.Fatalf("runRaw: %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "ollama/qwen2.5-coder:3b") {
		t.Fatalf("expected ollama prefix in args, got:\n%s", args)
	}
	if strings.Contains(string(args), "#high") {
		t.Fatalf("local models must not get an effort variant, got:\n%s", args)
	}
}

func TestOpenCodeParser_StepFinishIncludesCache(t *testing.T) {
	p := newOpenCodeParser()
	p.feed([]byte(`{"type":"step_finish","part":{"tokens":{"input":10,"output":4,"reasoning":0,"cache":{"read":100,"write":50}}}}`))
	_, usage := p.finish(nil, "prompt")
	if usage.InputTokens != 160 || usage.OutputTokens != 4 {
		t.Errorf("usage = %+v, want in 160 out 4", usage)
	}
}

func fakeOpenCodeEnv(t *testing.T, readOnly bool) (env, args string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	envFile := filepath.Join(dir, "env.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$OPENCODE_CONFIG_CONTENT\" > %q\nprintf '%%s\\n' \"$@\" > %q\n", envFile, envFile+".args")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theirs":true}`)
	ch, err := OpenCodeRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Model: "p/m", ReadOnly: readOnly, Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch)
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(envFile + ".args")
	if err != nil {
		t.Fatal(err)
	}
	return string(data), string(a)
}

func TestOpenCodeReadOnlyRunGetsPermissions(t *testing.T) {
	got, args := fakeOpenCodeEnv(t, true)
	if !strings.Contains(args, "--standalone\n") || strings.Contains(args, "--auto") || strings.Contains(args, "--agent") {
		t.Errorf("args = %q", args)
	}
	if got != OpenCodeReadOnlyConfig() {
		t.Fatalf("OPENCODE_CONFIG_CONTENT = %s", got)
	}
	var cfg struct {
		Permissions []struct{ Action, Resource, Effect string } `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatal(err)
	}
	last := func(action, res string) string {
		eff := ""
		for _, p := range cfg.Permissions {
			if p.Action == action && wildMatch(p.Resource, res) {
				eff = p.Effect
			}
		}
		return eff
	}
	for res, want := range map[string]string{
		"*": "deny", "rm a": "deny", "cat a": "allow", "cat a > b": "deny", "git status": "allow",
		"git diff --output=x": "deny", "git log --output=x": "deny", "rg --pre sh x": "deny", "find .": "deny", "go test ./...": "deny",
	} {
		if g := last("shell", res); g != want {
			t.Errorf("shell %q = %q, want %q", res, g, want)
		}
	}
	if g := last("edit", "a.go"); g != "deny" {
		t.Errorf("edit = %q", g)
	}
}

func TestOpenCodeWritingRunKeepsTheEnvironment(t *testing.T) {
	got, args := fakeOpenCodeEnv(t, false)
	if got != `{"theirs":true}` || strings.Contains(args, "--standalone") {
		t.Fatalf("OPENCODE_CONFIG_CONTENT = %s, args = %q", got, args)
	}
}

// wildMatch is opencode's whole-value wildcard: * is any run of characters.
// A pattern ending in " *" also matches the command without arguments.
func wildMatch(pat, s string) bool {
	re := "^" + strings.ReplaceAll(regexp.QuoteMeta(pat), `\*`, ".*") + "$"
	if ok, _ := regexp.MatchString(re, s); ok {
		return true
	}
	if strings.HasSuffix(pat, " *") {
		return wildMatch(strings.TrimSuffix(pat, " *"), s)
	}
	return false
}

func TestOpenCodeParserMarksNewTurns(t *testing.T) {
	p := newOpenCodeParser()
	a := p.feed([]byte(`{"type":"text","part":{"text":"Let me look."}}`))
	p.feed([]byte(`{"type":"tool_use","part":{"tool":"read","state":{"status":"completed"}}}`))
	b := p.feed([]byte(`{"type":"text","part":{"text":"All fine."}}`))
	if len(a) != 1 || a[0].NewTurn || len(b) != 1 || !b[0].NewTurn || b[0].Text != "\n\nAll fine." {
		t.Errorf("tokens = %+v / %+v", a, b)
	}
}
