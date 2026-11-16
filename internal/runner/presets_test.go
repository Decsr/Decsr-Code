package runner

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
)

func TestPresetsInFactory(t *testing.T) {
	cases := map[string]Preset{
		"deepseek":   {BaseURL: "https://api.deepseek.com", KeyEnv: "DEEPSEEK_API_KEY", DefaultModel: "deepseek-v4-flash"},
		"openai":     {BaseURL: "https://api.openai.com/v1", KeyEnv: "OPENAI_API_KEY"},
		"openrouter": {BaseURL: "https://openrouter.ai/api/v1", KeyEnv: "OPENROUTER_API_KEY"},
		"groq":       {BaseURL: "https://api.groq.com/openai/v1", KeyEnv: "GROQ_API_KEY"},
	}
	for name, want := range cases {
		got, err := New(config.AgentConfig{Runner: name}, nil, "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r := unwrapOpenAI(t, got)
		if r.Provider != name || r.BaseURL != want.BaseURL || r.APIKeyEnv != want.KeyEnv || r.Model != want.DefaultModel {
			t.Errorf("%s = %+v", name, r)
		}
		// config overrides the preset
		got, _ = New(config.AgentConfig{Runner: name, Model: "m", BaseURL: "http://b", APIKeyEnv: "K"}, nil, "")
		r = unwrapOpenAI(t, got)
		if r.BaseURL != "http://b" || r.APIKeyEnv != "K" || r.Model != "m" {
			t.Errorf("%s override = %+v", name, r)
		}
	}
}

func TestKeyResolutionOrder(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("MY_KEY", "")
	if _, _, err := ResolveKey("deepseek", ""); err == nil ||
		err.Error() != `deepseek: no API key — run "decsr connect deepseek" or export DEEPSEEK_API_KEY` {
		t.Fatalf("missing err = %v", err)
	}
	if KeySource("deepseek", "") != "" {
		t.Error("source should be empty")
	}
	if err := credentials.Set("deepseek", credentials.Cred{APIKey: "saved-key-0000000"}); err != nil {
		t.Fatal(err)
	}
	key, src, err := ResolveKey("deepseek", "")
	if err != nil || key != "saved-key-0000000" || src != KeySourceSaved {
		t.Fatalf("saved = %q %q %v", key, src, err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "env-key")
	key, src, _ = ResolveKey("deepseek", "")
	if key != "env-key" || src != "env DEEPSEEK_API_KEY" {
		t.Fatalf("env wins: %q %q", key, src)
	}
	t.Setenv("MY_KEY", "mine")
	if _, src, _ = ResolveKey("deepseek", "MY_KEY"); src != "env MY_KEY" {
		t.Errorf("api_key_env: %q", src)
	}
	t.Cleanup(func() { _ = credentials.Delete("deepseek") })
}

func TestCustomProviderUsesSavedBaseURL(t *testing.T) {
	t.Cleanup(func() { _ = credentials.Delete("acme") })
	if err := credentials.Set("acme", credentials.Cred{APIKey: "acme-key-123456", BaseURL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	got, err := New(config.AgentConfig{Runner: "openai", Provider: "acme", Model: "m"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	r := unwrapOpenAI(t, got)
	if r.BaseURL != "" || r.baseURL() != "http://127.0.0.1:1" || r.CredName != "acme" {
		t.Errorf("runner = %+v", r)
	}
	if k, err := r.apiKey(); err != nil || k != "acme-key-123456" {
		t.Errorf("key = %q %v", k, err)
	}
	r2 := &OpenAIRunner{Provider: "openai", CredName: "ghost", Model: "m"}
	_, err = r2.Complete(context.Background(), CompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("err = %v", err)
	}
}

func TestProbeModels(t *testing.T) {
	var hdr http.Header
	srv := newOpenAITestServer(t, 200, `{"data":[{"id":"a"},{"id":"b"}]}`, nil, &hdr, nil)
	defer srv.Close()
	names, err := ProbeModels(context.Background(), nil, srv.URL, "k")
	if err != nil || len(names) != 2 || hdr.Get("Authorization") != "Bearer k" {
		t.Fatalf("names=%v err=%v", names, err)
	}
	rej := newOpenAITestServer(t, 401, `{}`, nil, nil, nil)
	defer rej.Close()
	_, err = ProbeModels(context.Background(), nil, rej.URL, "secret-key-xyz")
	var kr *KeyRejectedError
	if !errors.As(err, &kr) || strings.Contains(err.Error(), "secret-key-xyz") {
		t.Fatalf("err = %v", err)
	}
	if _, err = ProbeModels(context.Background(), nil, "http://127.0.0.1:1", "secret-key-xyz"); err == nil || strings.Contains(err.Error(), "secret-key-xyz") {
		t.Errorf("network err = %v", err)
	}
}

func TestListProviderModelsFallbackOnlyDeepSeek(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("GROQ_API_KEY", "")
	names, err := ListProviderModels("deepseek", "")
	if err == nil || len(names) != 2 {
		t.Errorf("deepseek: %v %v", names, err)
	}
	names, err = ListProviderModels("groq", "")
	if err == nil || names != nil {
		t.Errorf("groq: %v %v", names, err)
	}
}
