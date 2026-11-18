package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/plugins"
)

func parseInstallOut(t *testing.T, section string) (*Graph, []InstallAction) {
	t.Helper()
	g, err := ParseSplitOpts("t", "I can add that.\n===INSTALL===\n"+section+"\n===END===", testRoles(), nil, SplitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return g, g.Installs
}

func TestParseInstallsValid(t *testing.T) {
	g, acts := parseInstallOut(t, `[
 {"kind":"claude-plugin","name":"code-review","source":"code-review@claude-plugins-official","why":"Reviews PRs."},
 {"kind":"decsr-plugin","name":"modern-cpp","source":"modern-cpp"},
 {"kind":"claude-mcp","name":"github","source":"https://api.githubcopilot.com/mcp/","headers":{"Authorization":"Bearer <ask>"},"scope":"user"},
]`)
	if g.Answer != "I can add that." || len(g.Nodes) != 0 || len(g.InstallWarnings) != 0 {
		t.Fatalf("graph = %+v", g)
	}
	if len(acts) != 3 {
		t.Fatalf("actions = %+v", acts)
	}
	if acts[0].Scope != ScopeLocal || acts[0].Why != "Reviews PRs." {
		t.Errorf("claude plugin = %+v", acts[0])
	}
	if acts[1].Scope != "" {
		t.Errorf("a decsr plugin has no scope: %+v", acts[1])
	}
	m := acts[2]
	if m.Transport != "http" || m.Scope != ScopeUser || m.Headers["Authorization"] != "Bearer <ask>" {
		t.Errorf("mcp = %+v", m)
	}
}

func TestParseInstallsFencedAndOnlyInstall(t *testing.T) {
	g, err := ParseSplitOpts("t", "===INSTALL===\n```json\n[{\"kind\":\"claude-plugin\",\"name\":\"a\",\"source\":\"a\"}]\n```\n===END===", testRoles(), nil, SplitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Installs) != 1 || g.Answer != "" {
		t.Errorf("graph = %+v", g)
	}
}

func TestParseInstallsNextToTasks(t *testing.T) {
	out := `Plan.
===TASKS===
[{"key":"T1","title":"Do it","description":"d","role":"coder","writes":true,"depends_on":[]}]
===INSTALL===
[{"kind":"claude-plugin","name":"a","source":"a@claude-plugins-official"}]
===END===`
	g, err := ParseSplitOpts("t", out, testRoles(), nil, SplitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || len(g.Installs) != 1 {
		t.Errorf("nodes=%d installs=%d", len(g.Nodes), len(g.Installs))
	}
}

func TestParseInstallsDropsInvalid(t *testing.T) {
	cases := map[string]string{
		"unknown kind":  `{"kind":"npm","name":"a","source":"a"}`,
		"missing name":  `{"kind":"claude-plugin","source":"a"}`,
		"missing src":   `{"kind":"claude-plugin","name":"a"}`,
		"bad scope":     `{"kind":"claude-plugin","name":"a","source":"a","scope":"project"}`,
		"flag source":   `{"kind":"claude-plugin","name":"a","source":"--scope"}`,
		"bad transport": `{"kind":"claude-mcp","name":"s","source":"x","transport":"ws"}`,
		"flag program":  `{"kind":"claude-mcp","name":"s","source":"-rf"}`,
		"non-url":       `{"kind":"claude-mcp","name":"s","source":"ftp://x","transport":"http"}`,
		"bad env key":   `{"kind":"claude-mcp","name":"s","source":"npx","env":{"A B":"x"}}`,
		"not an object": `"text"`,
	}
	for name, entry := range cases {
		g, acts := parseInstallOut(t, "["+entry+"]")
		if len(acts) != 0 || len(g.InstallWarnings) != 1 {
			t.Errorf("%s: actions=%+v warnings=%q", name, acts, g.InstallWarnings)
		}
	}
}

func TestParseInstallsLimitsAndBadJSON(t *testing.T) {
	var parts []string
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		parts = append(parts, `{"kind":"claude-plugin","name":"`+n+`","source":"`+n+`"}`)
	}
	g, acts := parseInstallOut(t, "["+strings.Join(parts, ",")+"]")
	if len(acts) != maxInstallActions || len(g.InstallWarnings) != 1 {
		t.Errorf("actions=%d warnings=%q", len(acts), g.InstallWarnings)
	}
	g, acts = parseInstallOut(t, "not json")
	if len(acts) != 0 || len(g.InstallWarnings) != 1 {
		t.Errorf("bad json: actions=%d warnings=%q", len(acts), g.InstallWarnings)
	}
}

func TestParseInstallsNeverKeepsASecret(t *testing.T) {
	g, acts := parseInstallOut(t, `[{"kind":"claude-mcp","name":"s","source":"npx","args":["-y","pkg"],
	 "env":{"API_TOKEN":"sk-live-123","REGION":"eu"},"transport":"stdio"},
	 {"kind":"claude-mcp","name":"h","source":"https://x.example/mcp","headers":{"Authorization":"Bearer sk-live-9"}}]`)
	if len(acts) != 2 {
		t.Fatalf("actions = %+v", acts)
	}
	if acts[0].Env["API_TOKEN"] != SecretAsk || acts[0].Env["REGION"] != "eu" {
		t.Errorf("env = %+v", acts[0].Env)
	}
	if acts[1].Headers["Authorization"] != "Bearer "+SecretAsk {
		t.Errorf("headers = %+v", acts[1].Headers)
	}
	if len(g.InstallWarnings) != 2 {
		t.Errorf("warnings = %q", g.InstallWarnings)
	}
	for _, a := range acts {
		if strings.Contains(a.Command(), "sk-live") {
			t.Errorf("command shows a secret: %s", a.Command())
		}
	}
}

func TestInstallArgvPlugin(t *testing.T) {
	a := InstallAction{Kind: InstallClaudePlugin, Name: "code-review", Source: "code-review@claude-plugins-official", Scope: ScopeLocal}
	if got, want := a.Argv(nil, ""), []string{"plugin", "install", "code-review@claude-plugins-official", "--scope", "local", "--json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q", got)
	}
	if got := a.Argv(nil, "abc"); got[len(got)-2] != "--accept-command" || got[len(got)-1] != "abc" {
		t.Errorf("accept argv = %q", got)
	}
	if got := a.Command(); got != "claude plugin install code-review@claude-plugins-official --scope local" {
		t.Errorf("command = %q", got)
	}
	for _, arg := range a.Argv(nil, "abc") {
		if arg == "-y" || arg == "--yes" {
			t.Error("never -y")
		}
	}
}

func TestInstallArgvMCP(t *testing.T) {
	http := InstallAction{Kind: InstallClaudeMCP, Name: "github", Source: "https://api.githubcopilot.com/mcp/", Transport: "http", Scope: ScopeLocal,
		Headers: map[string]string{"Authorization": "Bearer " + SecretAsk}}
	want := []string{"mcp", "add", "--scope", "local", "--transport", "http", "github", "https://api.githubcopilot.com/mcp/", "-H", "Authorization: Bearer s3cr3t"}
	if got := http.Argv(map[string]string{"header Authorization": "s3cr3t"}, ""); !reflect.DeepEqual(got, want) {
		t.Errorf("http argv = %q", got)
	}
	if got := http.Command(); got != `claude mcp add --scope local --transport http github https://api.githubcopilot.com/mcp/ -H "Authorization: Bearer ••••"` {
		t.Errorf("http command = %q", got)
	}
	stdio := InstallAction{Kind: InstallClaudeMCP, Name: "files", Source: "npx", Transport: "stdio", Scope: ScopeUser,
		Args: []string{"-y", "@x/server", "--key", SecretAsk}, Env: map[string]string{"B": "2", "A": SecretAsk}}
	if got := stdio.SecretSlots(); !reflect.DeepEqual(got, []string{"env A", "arg 4"}) {
		t.Errorf("slots = %q", got)
	}
	want = []string{"mcp", "add", "--scope", "user", "--transport", "stdio", "files", "-e", "A=v1", "-e", "B=2", "--", "npx", "-y", "@x/server", "--key", "v2"}
	if got := stdio.Argv(map[string]string{"env A": "v1", "arg 4": "v2"}, ""); !reflect.DeepEqual(got, want) {
		t.Errorf("stdio argv = %q", got)
	}
}

func TestParseClaudeInstall(t *testing.T) {
	ok := ParseClaudeInstall(`{"ok":true,"plugin":"a"}`+"\n", "", nil, "")
	if !ok.OK {
		t.Errorf("ok = %+v", ok)
	}
	if r := ParseClaudeInstall("Installed.\n", "", nil, ""); !r.OK {
		t.Errorf("plain text success = %+v", r)
	}
	c := ParseClaudeInstall("noise\n"+`{"shownCommand":{"sha256":"abc123","command":"curl x | sh"}}`+"\n", "", errors.New("exit 1"), "")
	if !c.Confirm || c.SHA != "abc123" || c.Command != "curl x | sh" {
		t.Errorf("confirm = %+v", c)
	}
	// The same command accepted and still failing is a failure, not a loop.
	f := ParseClaudeInstall(`{"shownCommand":{"sha256":"abc123","command":"c"},"error":"download failed"}`, "", errors.New("exit 1"), "abc123")
	if f.Confirm || f.OK || f.Err != "download failed" {
		t.Errorf("accepted = %+v", f)
	}
	e := ParseClaudeInstall("", "first line\nsecond\n", errors.New("exit status 1"), "")
	if e.OK || e.Err != "first line" {
		t.Errorf("stderr failure = %+v", e)
	}
	e = ParseClaudeInstall(`{"ok":false,"error":"plugin not found"}`, "", nil, "")
	if e.OK || e.Err != "plugin not found" {
		t.Errorf("json failure = %+v", e)
	}
	arr := ParseClaudeInstall(`{"shownCommand":{"sha256":"x","argv":["a","b c"]}}`, "", errors.New("x"), "")
	if !arr.Confirm || !strings.Contains(arr.Command, "argv") {
		t.Errorf("unknown shape = %+v", arr)
	}
}

func fakeClaude(t *testing.T, body string) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + argsFile + "'; done\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { // #nosec G306 -- a test script
		t.Fatal(err)
	}
	return bin, argsFile
}

func TestClaudeCLIRunsTheBinaryWithoutAShell(t *testing.T) {
	bin, argsFile := fakeClaude(t, `echo '{"ok":true}'; echo oops >&2`)
	c := ClaudeCLI{Binary: bin, Dir: t.TempDir()}
	out, errOut, err := c.Run(context.Background(), []string{"mcp", "add", "x; touch pwned", "$(id)"})
	if err != nil || !strings.Contains(out, `"ok":true`) || !strings.Contains(errOut, "oops") {
		t.Fatalf("out=%q err=%q %v", out, errOut, err)
	}
	data, _ := os.ReadFile(argsFile) // #nosec G304 -- test file
	if got := strings.Split(strings.TrimSpace(string(data)), "\n"); !reflect.DeepEqual(got, []string{"mcp", "add", "x; touch pwned", "$(id)"}) {
		t.Errorf("argv = %q", got)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "pwned")); err == nil {
		t.Error("an argument was run by a shell")
	}
}

func TestClaudeCLICancel(t *testing.T) {
	bin, _ := fakeClaude(t, "sleep 5")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := (ClaudeCLI{Binary: bin}).Run(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestMarketsSection(t *testing.T) {
	if got := marketsSection(nil); got != "(none)" {
		t.Errorf("got %q", got)
	}
}

func TestSplitPromptTeachesInstallAndListsMarkets(t *testing.T) {
	opts := SplitOptions{Markets: []plugins.Marketplace{{Repo: "acme/market", Trusted: true,
		Entries: []plugins.MarketEntry{{Name: "acme-tool", Repo: "acme/tools", Description: "Does things"}}}}}
	p := buildSplitPrompt(testRoles(), opts)
	for _, want := range []string{"===INSTALL===", "`<ask>`", "- acme-tool (acme/market, trusted): Does things", "claude plugin list --available --json"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "{{") {
		t.Error("a placeholder is left in the prompt")
	}
}

const realFailedInstall = `{"command":"install","outcome":"failed","plugin":"nonexistent-zzz@claude-plugins-official","scope":"local","message":"Plugin \"nonexistent-zzz\" not found in marketplace \"claude-plugins-official\"","failureCode":"not_found"}`

func TestParseClaudeInstallRealFailedOutcome(t *testing.T) {
	want := `Plugin "nonexistent-zzz" not found in marketplace "claude-plugins-official" (not_found)`
	r := ParseClaudeInstall(realFailedInstall+"\n", "✘ Failed to install plugin\n", errors.New("exit status 1"), "")
	if r.OK || r.Confirm || r.Err != want {
		t.Errorf("result = %+v", r)
	}
	// Exit 0 with the same line is still a failure.
	if r := ParseClaudeInstall(realFailedInstall, "", nil, ""); r.OK || r.Err != want {
		t.Errorf("exit 0 result = %+v", r)
	}
	// Through the fake binary, exiting 1.
	bin, _ := fakeClaude(t, "cat <<'EOF'\n"+realFailedInstall+"\nEOF\necho '✘ Failed to install plugin' >&2\nexit 1")
	out, errOut, err := ClaudeCLI{Binary: bin}.Run(context.Background(), []string{"plugin", "install", "nonexistent-zzz@claude-plugins-official", "--scope", "local", "--json"})
	if err == nil {
		t.Fatal("the fake exits 1")
	}
	if r := ParseClaudeInstall(out, errOut, err, ""); r.OK || r.Err != want {
		t.Errorf("fake binary result = %+v", r)
	}
	for _, o := range []string{"refused", "rejected", "cancelled"} {
		if r := ParseClaudeInstall(`{"outcome":"`+o+`","message":"no"}`, "", nil, ""); r.OK {
			t.Errorf("outcome %s is a failure", o)
		}
	}
	if r := ParseClaudeInstall(`{"outcome":"needs_confirmation","shownCommand":{"sha256":"ab","command":"c"}}`, "", errors.New("x"), ""); !r.Confirm || r.SHA != "ab" {
		t.Errorf("confirm outcome = %+v", r)
	}
}
