package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	script := "#!/bin/sh\ncat \"" + filepath.Join(dir, "out.ndjson") + "\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestOpenCodeRunRaw_AnswerTextMentioningRateLimitIsNotRateLimit(t *testing.T) {
	bin := writeFakeOpenCode(t, `{"type":"text","part":{"text":"To handle a rate limit, back off on HTTP 429 (too many requests)."}}`+"\n")
	out, err := OpenCodeRunner{Binary: bin}.runRaw(context.Background(), "hi", "m")
	if err != nil {
		t.Fatalf("answer text must not be classified as rate limit: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected output")
	}
}

func TestOpenCodeRunRaw_ErrorEventIsRateLimit(t *testing.T) {
	bin := writeFakeOpenCode(t, `{"type":"error","error":{"data":{"message":"429 too many requests"}}}`+"\n")
	_, err := OpenCodeRunner{Binary: bin}.runRaw(context.Background(), "hi", "m")
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected RateLimitError, got %v", err)
	}
}
