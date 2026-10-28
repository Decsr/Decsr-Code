package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeEffort(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"default": "",
		" HIGH ":  "high",
		"Low":     "low",
		"xhigh":   "xhigh",
	}
	for in, want := range cases {
		if got := NormalizeEffort(in); got != want {
			t.Errorf("NormalizeEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEffortRunner_DefaultAndOverride(t *testing.T) {
	inner := &recordingRunner{}
	r := NewEffortRunner(inner, "high")
	if _, err := r.Complete(context.Background(), CompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	if inner.gotEffort != "high" {
		t.Fatalf("default effort = %q, want high", inner.gotEffort)
	}
	if _, err := r.Complete(context.Background(), CompletionRequest{Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	if inner.gotEffort != "low" {
		t.Fatalf("override effort = %q, want low", inner.gotEffort)
	}
}

func TestEffortRunner_DefaultSentinelIsNoop(t *testing.T) {
	inner := &recordingRunner{}
	r := NewEffortRunner(inner, "default")
	if r != inner {
		t.Fatal("default effort should return the inner runner unchanged")
	}
}

type recordingRunner struct {
	gotEffort string
}

func (r *recordingRunner) Complete(_ context.Context, req CompletionRequest) (<-chan Token, error) {
	r.gotEffort = req.Effort
	ch := make(chan Token, 1)
	ch <- Token{Done: true}
	close(ch)
	return ch, nil
}

func TestEffortOptions_StaticProviders(t *testing.T) {
	if got := EffortOptions("claude", "sonnet"); !containsString(got, "max") {
		t.Fatalf("claude options = %v, want max", got)
	}
	if got := EffortOptions("codex", "gpt-5.3-codex"); !containsString(got, "minimal") {
		t.Fatalf("codex options = %v, want minimal", got)
	}
	if got := EffortOptions("mlx", "some-model"); len(got) == 0 {
		t.Fatal("mlx should expose a conservative fallback")
	}
}

func TestEffortOptions_OpenCodeCache(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	t.Setenv("HOME", cacheDir)

	cache := map[string]any{
		"prov": map[string]any{
			"models": map[string]any{
				"m1": map[string]any{
					"reasoning_options": []any{
						map[string]any{"type": "effort", "values": []string{"low", "high"}},
					},
				},
				"m2": map[string]any{"reasoning_options": []any{}},
			},
		},
	}
	cacheBase, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cacheBase, "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := EffortOptions("opencode", "prov/m1"); !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Fatalf("m1 options = %v", got)
	}
	if got := EffortOptions("opencode", "prov/m2"); len(got) != 0 {
		t.Fatalf("m2 options = %v, want none", got)
	}
	if got := EffortOptions("opencode", "prov/unknown"); !reflect.DeepEqual(got, openCodeFallbacks) {
		t.Fatalf("unknown model options = %v, want fallback", got)
	}
	if got := EffortOptions("opencode", "prov/m1#high"); len(got) != 0 {
		t.Fatalf("variant model should have no separate effort, got %v", got)
	}
}

func TestEffortOptions_OllamaShow(t *testing.T) {
	old := ollamaShowEndpoint
	defer func() { ollamaShowEndpoint = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte(`{"thinking":{"values":["low","medium","high"],"default":"medium"}}`))
	}))
	defer srv.Close()
	ollamaShowEndpoint = srv.URL

	if got := EffortOptions("ollama", "gpt-oss"); !reflect.DeepEqual(got, []string{"low", "medium", "high"}) {
		t.Fatalf("ollama options = %v", got)
	}
}

func TestEffortOptions_OllamaBooleanControls(t *testing.T) {
	old := ollamaShowEndpoint
	defer func() { ollamaShowEndpoint = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"thinking":{"values":[true,false]}}`))
	}))
	defer srv.Close()
	ollamaShowEndpoint = srv.URL

	if got := EffortOptions("ollama", "qwen3"); !reflect.DeepEqual(got, []string{"on", "off"}) {
		t.Fatalf("ollama boolean options = %v", got)
	}
}

func TestEffortOptions_LMStudioCapabilities(t *testing.T) {
	old := lmStudioModelsURL
	defer func() { lmStudioModelsURL = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
			{"id":"reasoner","capabilities":["reasoning"]},
			{"id":"plain","capabilities":["tool_use"]}
		]}`))
	}))
	defer srv.Close()
	lmStudioModelsURL = srv.URL

	if got := EffortOptions("lmstudio", "reasoner"); len(got) == 0 {
		t.Fatal("reasoning model should expose effort options")
	}
	if got := EffortOptions("lmstudio", "plain"); len(got) != 0 {
		t.Fatalf("non-reasoning model options = %v, want none", got)
	}
}

func TestEffortMappings(t *testing.T) {
	if got := mapClaudeEffort("none"); got != "low" {
		t.Errorf("claude none = %q, want low", got)
	}
	if got := mapCodexEffort("xhigh"); got != "high" {
		t.Errorf("codex xhigh = %q, want high", got)
	}
	if got := mapCodexEffort("none"); got != "minimal" {
		t.Errorf("codex none = %q, want minimal", got)
	}
	if got := mapCopilotEffort("minimal"); got != "low" {
		t.Errorf("copilot minimal = %q, want low", got)
	}
	if got := mapOpenAIReasoningEffort("max"); got != "xhigh" {
		t.Errorf("openai max = %q, want xhigh", got)
	}
	if got := ollamaThinkValue("off"); got != false {
		t.Errorf("ollama off = %v, want false", got)
	}
	if got := ollamaThinkValue("high"); got != "high" {
		t.Errorf("ollama high = %v, want high", got)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
