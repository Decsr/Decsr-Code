package runner

import (
	"errors"
	"testing"
)

func TestClassifyRateLimit(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		input    []string
		want     bool
	}{
		{"claude usage limit", "claude", []string{"5-hour usage limit reached. Please retry after 14:00."}, true},
		{"claude rate limit", "claude", []string{"Error: rate_limit_error from Anthropic API"}, true},
		{"claude overloaded", "claude", []string{`{"type":"error","error":{"type":"overloaded_error"}}`}, true},
		{"codex 429", "codex", []string{"request failed: status 429: too many requests"}, true},
		{"codex insufficient_quota", "codex", []string{`{"error":{"code":"insufficient_quota"}}`}, true},
		{"opencode too many requests", "opencode", []string{"HTTP 429 Too Many Requests"}, true},
		{"ollama quota", "ollama", []string{"upstream returned: quota exceeded for cloud model"}, true},
		{"benign network error", "claude", []string{"connection refused"}, false},
		{"benign auth error", "codex", []string{"401 unauthorized"}, false},
		{"empty fragments", "claude", []string{"", ""}, false},
		{"claude hit your limit", "claude", []string{"You've hit your limit · resets 2pm"}, true},
		{"claude 5-hour limit reached", "claude", []string{"5-hour limit reached ∙ resets 2pm"}, true},
		{"claude weekly limit reached", "claude", []string{"Weekly limit reached ∙ resets Nov 20, 9am"}, true},
		{"codex usage limit", "codex", []string{"You’ve hit your usage limit. Try again at 2:30 PM."}, true},
		{"rate limit tokens per minute", "openai", []string{"Rate limit reached for gpt in organization on tokens per min"}, true},
		{"context length limit reached", "claude", []string{"context length limit reached"}, false},
		{"token limit reached", "claude", []string{"token limit reached"}, false},
		{"max context length", "openai", []string{"This model's maximum context length is 128000 tokens. Rate limit"}, false},
		{"prompt too long", "claude", []string{"Prompt is too long"}, false},
		{"max_tokens", "claude", []string{"max_tokens: limit reached, rate limit"}, false},
		{"bare limit reached", "claude", []string{"output limit reached"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRateLimit(tt.provider, tt.input...)
			if (got != nil) != tt.want {
				t.Fatalf("ClassifyRateLimit got=%v, want match=%v", got, tt.want)
			}
			if got != nil && !errors.Is(got, ErrRateLimited) {
				t.Errorf("returned error does not match ErrRateLimited sentinel")
			}
			if got != nil && got.Provider != tt.provider {
				t.Errorf("provider = %q, want %q", got.Provider, tt.provider)
			}
		})
	}
}

func TestRateLimitError_WrappedSurvivesErrorsIs(t *testing.T) {
	rl := &RateLimitError{Provider: "claude", Detail: "usage limit"}
	wrapped := errors.Join(errors.New("agent failed"), rl)
	if !errors.Is(wrapped, ErrRateLimited) {
		t.Fatal("errors.Is should detect ErrRateLimited through wrapping")
	}
}
