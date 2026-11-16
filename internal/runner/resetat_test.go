package runner

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseResetAt(t *testing.T) {
	utc := time.UTC
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, utc)
	at := func(y int, mo time.Month, d, h, m int) time.Time { return time.Date(y, mo, d, h, m, 0, 0, utc) }
	tests := []struct {
		name string
		text string
		want time.Time
	}{
		{"relative h m", "You hit the limit, try again in 3h 2m", now.Add(3*time.Hour + 2*time.Minute)},
		{"relative words", "Please try again in 2 hours and 5 minutes.", now.Add(2*time.Hour + 5*time.Minute)},
		{"relative minutes", "limit reached, in 45 minutes", now.Add(45 * time.Minute)},
		{"relative seconds short", "slow down, in 30s", now.Add(30 * time.Second)},
		{"retry after seconds", "retry after 120 seconds", now.Add(120 * time.Second)},
		{"relative days", "try again in 2 days 4 hours", now.Add(52 * time.Hour)},
		{"absolute pm", "5-hour limit reached, resets at 2pm", at(2026, 11, 16, 14, 0)},
		{"absolute minutes pm", "resets 2:30pm", at(2026, 11, 16, 14, 30)},
		{"absolute 24h", "usage limit, reset at 14:30", at(2026, 11, 16, 14, 30)},
		{"past becomes tomorrow", "resets at 9am", at(2026, 11, 17, 9, 0)},
		{"codex clock", "You've hit your usage limit. Try again at 2:30 PM.", at(2026, 11, 16, 14, 30)},
		{"codex date", "You’ve hit your usage limit. Try again at Nov 20th, 2026 3:15 AM.", at(2026, 11, 20, 3, 15)},
		{"month day no year", "limit reached, resets Nov 18, 9am", at(2026, 11, 18, 9, 0)},
		{"utc zone", "resets 4pm UTC", at(2026, 11, 16, 16, 0)},
		{"unix json", `{"error":{"resets_at": 1763300000}}`, time.Unix(1763300000, 0)},
		{"unix millis", `{"reset_at":1763300000000}`, time.Unix(1763300000, 0)},
		{"rfc3339 json", `{"reset_at":"2026-11-16T14:30:00Z"}`, at(2026, 11, 16, 14, 30)},
		{"claude legacy pipe", "Claude AI usage limit reached|1763300000", time.Unix(1763300000, 0)},
		{"resets in seconds", `{"type":"usage_limit_reached","resets_in_seconds":600}`, now.Add(10 * time.Minute)},
		{"garbage", "connection refused, rate limit", time.Time{}},
		{"bare number", "resets 5 times a day", time.Time{}},
		{"empty", "", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseResetAt(now, tt.text)
			if !got.Equal(tt.want) {
				t.Fatalf("parseResetAt(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestParseResetAt_Timezone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC) // 17:00 in Ho Chi Minh
	got := parseResetAt(now, "5-hour limit reached ∙ resets 9pm (Asia/Ho_Chi_Minh)")
	want := time.Date(2026, 11, 16, 21, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// 2pm Ho Chi Minh already passed today (17:00 there) -> tomorrow.
	got = parseResetAt(now, "resets 2pm (Asia/Ho_Chi_Minh)")
	want = time.Date(2026, 11, 17, 14, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("past-today: got %v, want %v", got, want)
	}
	// Unknown zone falls back to the clock's own location.
	got = parseResetAt(now, "resets 2pm (Mars/Olympus)")
	want = time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("unknown zone: got %v, want %v", got, want)
	}
}

func TestResetFromHeaders(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	hdr := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	tests := []struct {
		name string
		h    http.Header
		want time.Time
	}{
		{"retry-after seconds", hdr("Retry-After", "90"), now.Add(90 * time.Second)},
		{"retry-after date", hdr("Retry-After", "Mon, 16 Nov 2026 12:00:00 GMT"), now.Add(2 * time.Hour)},
		{"ratelimit durations take latest", hdr("x-ratelimit-reset-requests", "1s", "x-ratelimit-reset-tokens", "6m0s"), now.Add(6 * time.Minute)},
		{"hour minute", hdr("x-ratelimit-reset-tokens", "1h2m"), now.Add(62 * time.Minute)},
		{"anthropic rfc3339", hdr("anthropic-ratelimit-tokens-reset", "2026-11-16T14:30:00Z"), now.Add(4*time.Hour + 30*time.Minute)},
		{"retry-after wins", hdr("Retry-After", "10", "x-ratelimit-reset-tokens", "1h"), now.Add(10 * time.Second)},
		{"none", hdr(), time.Time{}},
		{"nil", nil, time.Time{}},
		{"garbage", hdr("Retry-After", "soon"), time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resetFromHeaders(now, tt.h); !got.Equal(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func fixedNow(t *testing.T, now time.Time) {
	t.Helper()
	old := rateLimitNow
	rateLimitNow = func() time.Time { return now }
	t.Cleanup(func() { rateLimitNow = old })
}

func TestClassifyRateLimit_FillsResetAt(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	fixedNow(t, now)
	rl := ClassifyRateLimit("claude", "5-hour limit reached ∙ resets 2pm")
	if rl == nil || !rl.ResetAt.Equal(time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("rl = %+v", rl)
	}
	if rl := ClassifyRateLimit("claude", "rate limit hit"); rl == nil || !rl.ResetAt.IsZero() {
		t.Fatalf("unknown reset must stay zero: %+v", rl)
	}
}

func TestOpenAIRunner_RateLimitResetFromHeader(t *testing.T) {
	t.Setenv("TEST_API_KEY", "k")
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	fixedNow(t, now)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"slow down"}`))
	}))
	defer srv.Close()
	r := &OpenAIRunner{Provider: "deepseek", Model: "m", BaseURL: srv.URL, APIKeyEnv: "TEST_API_KEY"}
	_, err := r.Complete(context.Background(), CompletionRequest{})
	var rl *RateLimitError
	if !errors.As(err, &rl) || !rl.ResetAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("err = %v, rl = %+v", err, rl)
	}
}

func TestLMStudioRunner_RateLimitResetFromBody(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	fixedNow(t, now)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-ratelimit-reset-requests", "6m0s")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	r := &LMStudioRunner{Model: "m", BaseURL: srv.URL}
	_, err := r.Complete(context.Background(), CompletionRequest{})
	var rl *RateLimitError
	if !errors.As(err, &rl) || !rl.ResetAt.Equal(now.Add(6*time.Minute)) {
		t.Fatalf("err = %v, rl = %+v", err, rl)
	}
}

func TestStreamClaudeOutput_UsageLimitResult(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	fixedNow(t, now)
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"You've hit your limit · resets 2pm"}]}}
{"type":"result","subtype":"success","is_error":true,"result":"You've hit your limit · resets 2pm"}
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
	var rl *RateLimitError
	for tok := range ch {
		if tok.Error != nil {
			errors.As(tok.Error, &rl)
		}
	}
	if rl == nil || rl.Provider != "claude" || !rl.ResetAt.Equal(time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("rl = %+v", rl)
	}
}

func TestStreamCodexOutput_UsageLimitEvent(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	fixedNow(t, now)
	stream := `{"type":"turn.started"}
{"type":"turn.failed","error":{"message":"You’ve hit your usage limit. Try again at 2:30 PM."}}
`
	dir := t.TempDir()
	f := filepath.Join(dir, "stream.jsonl")
	if err := os.WriteFile(f, []byte(stream), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "cat "+f+"; exit 1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ch := make(chan Token, 64)
	go streamCodexOutput(cmd, stdout, &bytes.Buffer{}, "m", "in", ch)
	var rl *RateLimitError
	for tok := range ch {
		if tok.Error != nil {
			errors.As(tok.Error, &rl)
		}
	}
	if rl == nil || rl.Provider != "codex" || !rl.ResetAt.Equal(time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)) {
		t.Fatalf("rl = %+v", rl)
	}
}

func TestOpenCodeRunRaw_RateLimitResetAt(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	fixedNow(t, now)
	bin := writeFakeOpenCode(t, `{"type":"error","error":{"data":{"message":"429 too many requests, try again in 10 minutes"}}}`+"\n")
	_, err := OpenCodeRunner{Binary: bin}.runRaw(context.Background(), "hi", "m", "", "")
	var rl *RateLimitError
	if !errors.As(err, &rl) || !rl.ResetAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("err = %v rl = %+v", err, rl)
	}
}
