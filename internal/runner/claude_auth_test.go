package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestClaudeAuthModeFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"nothing set", nil, AuthSubscription},
		{"empty key", map[string]string{"ANTHROPIC_API_KEY": "  "}, AuthSubscription},
		{"api key", map[string]string{"ANTHROPIC_API_KEY": "k"}, AuthAPIKey},
		{"bedrock", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, AuthAPIKey},
		{"vertex", map[string]string{"CLAUDE_CODE_USE_VERTEX": "1"}, AuthAPIKey},
		{"foundry", map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1"}, AuthAPIKey},
	}
	for _, c := range cases {
		if got := ClaudeAuthModeFrom(envOf(c.env), nil); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestClaudeAuthModeFromSettings(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	none := envOf(nil)
	helper := write("a.json", `{"apiKeyHelper": "/bin/true"}`)
	plain := write("b.json", `{"model": "x"}`)
	bad := write("c.json", `not json`)
	missing := filepath.Join(dir, "missing.json")
	if got := ClaudeAuthModeFrom(none, []string{missing, bad, plain}); got != AuthSubscription {
		t.Errorf("no helper: got %s", got)
	}
	if got := ClaudeAuthModeFrom(none, []string{plain, helper}); got != AuthAPIKey {
		t.Errorf("helper: got %s", got)
	}
}
