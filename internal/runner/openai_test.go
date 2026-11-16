package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func collect(t *testing.T, ch <-chan Token) (text, reasoning string, done Token, errs []error) {
	t.Helper()
	for tok := range ch {
		text += tok.Text
		reasoning += tok.Reasoning
		if tok.Error != nil {
			errs = append(errs, tok.Error)
		}
		if tok.Done {
			done = tok
		}
	}
	return
}

func newOpenAITestServer(t *testing.T, status int, stream string, got *map[string]any, hdr *http.Header, path *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path != nil {
			*path = r.URL.Path
		}
		if hdr != nil {
			*hdr = r.Header.Clone()
		}
		if got != nil {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, got)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, stream)
	}))
}

func TestOpenAIRunner_StreamsAndSendsRequest(t *testing.T) {
	t.Setenv("TEST_API_KEY", "secret-key")
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"think"}}]}`,
		`data: {"choices":[{"delta":{"content":"hello "}}]}`,
		`data: {"choices":[{"delta":{"content":"world"}}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`,
		`data: [DONE]`, ``,
	}, "\n")
	var body map[string]any
	var hdr http.Header
	var path string
	srv := newOpenAITestServer(t, http.StatusOK, stream, &body, &hdr, &path)
	defer srv.Close()

	r := &OpenAIRunner{Provider: "openai", Model: "m1", BaseURL: srv.URL + "/v1/", APIKeyEnv: "TEST_API_KEY"}
	ch, err := r.Complete(context.Background(), CompletionRequest{SystemPrompt: "sys", Messages: []ConvMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	text, reasoning, done, errs := collect(t, ch)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if text != "hello world" || reasoning != "think" {
		t.Errorf("text/reasoning = %q/%q", text, reasoning)
	}
	if done.Usage == nil || done.Usage.InputTokens != 12 || done.Usage.OutputTokens != 5 || done.Usage.Estimated {
		t.Errorf("usage = %+v", done.Usage)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("path = %q", path)
	}
	if hdr.Get("Authorization") != "Bearer secret-key" {
		t.Errorf("auth header = %q", hdr.Get("Authorization"))
	}
	if body["stream"] != true {
		t.Errorf("stream = %v", body["stream"])
	}
	if so, _ := body["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Errorf("stream_options = %v", body["stream_options"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Error("max_tokens must not be sent")
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Error("reasoning_effort must be omitted without an effort")
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["content"] != "sys" {
		t.Errorf("messages = %v", body["messages"])
	}
}

func TestOpenAIRunner_SendsReasoningEffort(t *testing.T) {
	t.Setenv("TEST_API_KEY", "k")
	var body map[string]any
	srv := newOpenAITestServer(t, http.StatusOK, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\ndata: [DONE]\n", &body, nil, nil)
	defer srv.Close()
	r := &OpenAIRunner{Provider: "openai", Model: "m", BaseURL: srv.URL, APIKeyEnv: "TEST_API_KEY"}
	ch, err := r.Complete(context.Background(), CompletionRequest{Effort: "max"})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
	if body["reasoning_effort"] != "xhigh" {
		t.Errorf("reasoning_effort = %v", body["reasoning_effort"])
	}
}

func TestOpenAIRunner_MissingKey(t *testing.T) {
	t.Setenv("TEST_API_KEY", "")
	r := &OpenAIRunner{Provider: "deepseek", Model: "m", BaseURL: "http://x", APIKeyEnv: "TEST_API_KEY"}
	_, err := r.Complete(context.Background(), CompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "TEST_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	r.APIKeyEnv = ""
	if _, err := r.Complete(context.Background(), CompletionRequest{}); err == nil || !strings.Contains(err.Error(), "api_key_env") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIRunner_RequiresBaseURLAndModel(t *testing.T) {
	t.Setenv("TEST_API_KEY", "k")
	r := &OpenAIRunner{Provider: "openai", Model: "m", APIKeyEnv: "TEST_API_KEY"}
	if _, err := r.Complete(context.Background(), CompletionRequest{}); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("err = %v", err)
	}
	r = &OpenAIRunner{Provider: "openai", BaseURL: "http://x", APIKeyEnv: "TEST_API_KEY"}
	if _, err := r.Complete(context.Background(), CompletionRequest{}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIRunner_RateLimit(t *testing.T) {
	t.Setenv("TEST_API_KEY", "k")
	srv := newOpenAITestServer(t, http.StatusTooManyRequests, `{"error":"slow down"}`, nil, nil, nil)
	defer srv.Close()
	r := &OpenAIRunner{Provider: "deepseek", Model: "m", BaseURL: srv.URL, APIKeyEnv: "TEST_API_KEY"}
	_, err := r.Complete(context.Background(), CompletionRequest{})
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.Provider != "deepseek" {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIRunner_HTTPError(t *testing.T) {
	t.Setenv("TEST_API_KEY", "k")
	srv := newOpenAITestServer(t, http.StatusUnauthorized, `{"error":"bad key"}`, nil, nil, nil)
	defer srv.Close()
	r := &OpenAIRunner{Provider: "openai", Model: "m", BaseURL: srv.URL, APIKeyEnv: "TEST_API_KEY"}
	_, err := r.Complete(context.Background(), CompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "k\"") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIRunner_EmptyContentIsError(t *testing.T) {
	t.Setenv("TEST_API_KEY", "k")
	for name, stream := range map[string]string{
		"empty":     "data: [DONE]\n",
		"reasoning": "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hmm\"}}]}\ndata: [DONE]\n",
	} {
		t.Run(name, func(t *testing.T) {
			srv := newOpenAITestServer(t, http.StatusOK, stream, nil, nil, nil)
			defer srv.Close()
			r := &OpenAIRunner{Provider: "openai", Model: "m", BaseURL: srv.URL, APIKeyEnv: "TEST_API_KEY"}
			ch, err := r.Complete(context.Background(), CompletionRequest{})
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, errs := collect(t, ch)
			if len(errs) != 1 {
				t.Fatalf("errs = %v", errs)
			}
		})
	}
}

func TestNew_DeepSeekDefaultsAndOverride(t *testing.T) {
	got, err := New(config.AgentConfig{Runner: "deepseek"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	r := unwrapOpenAI(t, got)
	if r.Provider != "deepseek" || r.BaseURL != "https://api.deepseek.com" || r.APIKeyEnv != "DEEPSEEK_API_KEY" || r.Model != "deepseek-v4-flash" {
		t.Errorf("defaults = %+v", r)
	}

	got, err = New(config.AgentConfig{Runner: "deepseek", Model: "deepseek-v4-pro", BaseURL: "http://proxy", APIKeyEnv: "MY_KEY"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	r = unwrapOpenAI(t, got)
	if r.BaseURL != "http://proxy" || r.APIKeyEnv != "MY_KEY" || r.Model != "deepseek-v4-pro" {
		t.Errorf("override = %+v", r)
	}

	got, err = New(config.AgentConfig{Runner: "openai", Model: "m", BaseURL: "http://b", APIKeyEnv: "K"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if r = unwrapOpenAI(t, got); r.Provider != "openai" || r.BaseURL != "http://b" {
		t.Errorf("openai = %+v", r)
	}

	if IsLocalRunner(config.AgentConfig{Runner: "openai"}) || IsLocalRunner(config.AgentConfig{Runner: "deepseek"}) {
		t.Error("API runners must not be local")
	}
}

func unwrapOpenAI(t *testing.T, r LLMRunner) *OpenAIRunner {
	t.Helper()
	for {
		switch v := r.(type) {
		case *OpenAIRunner:
			return v
		case *EffortRunner:
			r = v.inner
		default:
			t.Fatalf("unexpected runner type %T", r)
		}
	}
}

func TestPingAPI(t *testing.T) {
	t.Setenv("TEST_API_KEY", "k")
	ctx := context.Background()
	for status, wantErr := range map[int]string{200: "", 401: "rejected", 403: "rejected", 500: "HTTP 500"} {
		var path string
		var hdr http.Header
		srv := newOpenAITestServer(t, status, `{}`, nil, &hdr, &path)
		err := pingAPI(ctx, "deepseek", srv.URL, "TEST_API_KEY")
		srv.Close()
		if wantErr == "" && err != nil || wantErr != "" && (err == nil || !strings.Contains(err.Error(), wantErr)) {
			t.Errorf("status %d: err = %v", status, err)
		}
		if path != "/models" || hdr.Get("Authorization") != "Bearer k" {
			t.Errorf("status %d: path %q auth %q", status, path, hdr.Get("Authorization"))
		}
	}
	t.Setenv("TEST_API_KEY", "")
	if err := pingAPI(ctx, "deepseek", "http://127.0.0.1:1", "TEST_API_KEY"); err == nil || !strings.Contains(err.Error(), "TEST_API_KEY") {
		t.Errorf("missing key err = %v", err)
	}
}
