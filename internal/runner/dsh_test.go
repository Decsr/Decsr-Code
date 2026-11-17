package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDshParserFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/dsh_stream.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	p := newDshParser()
	var text strings.Builder
	acts := map[string][]*Activity{}
	var order []string
	for _, line := range strings.Split(string(data), "\n") {
		for _, tok := range p.feed([]byte(strings.TrimSpace(line))) {
			text.WriteString(tok.Text)
			if tok.Activity != nil {
				if _, seen := acts[tok.Activity.ID]; !seen {
					order = append(order, tok.Activity.ID)
				}
				acts[tok.Activity.ID] = append(acts[tok.Activity.ID], tok.Activity)
			}
		}
	}
	if want := "Let me read main.go.\n\nDone: main.go now prints hi."; text.String() != want {
		t.Errorf("text = %q, want %q", text.String(), want)
	}
	last := func(id string) *Activity { a := acts[id]; return a[len(a)-1] }
	if a := acts["call_1"]; len(a) != 2 || a[0].State != ActRunning || a[1].State != ActDone || a[1].Kind != ActRead || a[1].Title != "main.go" {
		t.Errorf("read activity = %+v", a)
	}
	if a := last("call_2"); a.Kind != ActEdit || a.State != ActDone || a.Added != 3 || a.Removed != 1 {
		t.Errorf("edit activity = %+v", a)
	}
	if a := last("call_3"); a.Kind != ActCommand || a.State != ActFailed || a.Title != "go build ./..." || !strings.Contains(a.Detail, "SANDBOX_DENIED") {
		t.Errorf("bash activity = %+v", a)
	}
	thinking := 0
	for _, id := range order {
		if acts[id][0].Kind == ActThinking {
			thinking++
		}
	}
	if thinking != 1 {
		t.Errorf("thinking activities = %d", thinking)
	}
	if err := p.failure(nil, ""); err != nil {
		t.Errorf("completed run reported %v", err)
	}
	finalText, usage := p.finish("prompt")
	if finalText != text.String() {
		t.Errorf("finish text = %q", finalText)
	}
	if usage.InputTokens != 5600 || usage.OutputTokens != 220 || usage.Estimated {
		t.Errorf("usage = %+v", usage)
	}
}

func TestDshParserFallsBackToFinalTextAndEstimate(t *testing.T) {
	p := newDshParser()
	p.feed([]byte(`{"type":"final","sessionId":"s","reason":{"kind":"completed"},"text":"only final"}`))
	text, usage := p.finish("some prompt text")
	if text != "only final" || !usage.Estimated || usage.OutputTokens == 0 {
		t.Errorf("text = %q, usage = %+v", text, usage)
	}
}

func TestDshFailureMapping(t *testing.T) {
	feed := func(lines ...string) *dshParser {
		p := newDshParser()
		for _, l := range lines {
			p.feed([]byte(l))
		}
		return p
	}
	cases := []struct {
		name    string
		p       *dshParser
		wait    error
		stderr  string
		wantErr string // "" = success
	}{
		{"completed", feed(`{"type":"final","reason":{"kind":"completed"},"text":"x"}`), nil, "", ""},
		{"aborted", feed(`{"type":"final","reason":{"kind":"aborted","reason":{"kind":"user"}},"text":""}`), nil, "", "dsh: aborted"},
		{"null reason", feed(`{"type":"final","reason":null,"text":"x"}`), nil, "", "dsh: no completed turn"},
		{"no final", feed(), nil, "boom", "dsh: no completed turn: boom"},
		{"in-turn error", feed(`{"type":"final","reason":{"kind":"error","error":{"code":"BAD","message":"model not found"}},"text":""}`), nil, "", "dsh: error: model not found"},
		{"runner error event", feed(`{"type":"error","message":"unknown option"}`), nil, "", "dsh: unknown option"},
		{"nonzero exit after completed", feed(`{"type":"final","reason":{"kind":"completed"},"text":"x"}`), errors.New("exit status 1"), "", "dsh: completed: exit status 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.failure(tc.wait, tc.stderr)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
			if errors.Is(err, ErrRateLimited) {
				t.Errorf("not a rate limit: %v", err)
			}
		})
	}
}

func TestDshRateLimitDetectedOnFailureOnly(t *testing.T) {
	p := newDshParser()
	p.feed([]byte(`{"type":"final","reason":{"kind":"error","error":{"code":"RATE_LIMITED","message":"429 Too Many Requests"}},"text":""}`))
	if err := p.failure(nil, ""); !errors.Is(err, ErrRateLimited) {
		t.Errorf("err = %v, want a rate limit", err)
	}
	// An answer that talks about rate limits is not one.
	ok := newDshParser()
	ok.feed([]byte(`{"type":"text","text":"add a rate limit of 429 per hour"}`))
	ok.feed([]byte(`{"type":"final","reason":{"kind":"completed"},"text":"add a rate limit"}`))
	if err := ok.failure(nil, ""); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestDshArgs(t *testing.T) {
	cases := []struct {
		name string
		opts dshOptions
		want []string
	}{
		{"agent", dshOptions{Agent: true, GitDir: "/repo/.git"},
			[]string{"--profile", "headless", "--json", "--permission-mode", "workspace-write", "--approval-policy", "never", "--add-dir", "/repo/.git", "-"}},
		{"agent without a git repo", dshOptions{Agent: true},
			[]string{"--profile", "headless", "--json", "--permission-mode", "workspace-write", "--approval-policy", "never", "-"}},
		{"text", dshOptions{},
			[]string{"--profile", "headless", "--json", "--permission-mode", "read-only", "--approval-policy", "never", "-"}},
		{"read-only agent", dshOptions{Agent: true, ReadOnly: true, GitDir: "/repo/.git"},
			[]string{"--profile", "headless", "--json", "--permission-mode", "read-only", "--approval-policy", "never", "-"}},
		{"patch comes before app args", dshOptions{PatchFile: "/tmp/p.yml"},
			[]string{"--profile", "headless", "--patch", "/tmp/p.yml", "--json", "--permission-mode", "read-only", "--approval-policy", "never", "-"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dshArgs(tc.opts); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %q\nwant   %q", got, tc.want)
			}
		})
	}
}

func TestDshPatch(t *testing.T) {
	head := "- id: agent-default-model\n  name: \"@deepseek-ai/dsh-agent-default-model\"\n  config:\n"
	cases := []struct{ model, effort, want string }{
		{"", "", ""},
		{"deepseek-official/deepseek-v4-pro", "high", head + "    provider: \"deepseek-official\"\n    model: \"deepseek-v4-pro\"\n    reasoningEffort: \"high\"\n"},
		{"deepseek-official/deepseek-flash", "", head + "    provider: \"deepseek-official\"\n    model: \"deepseek-flash\"\n"},
		{"deepseek-flash", "xhigh", head + "    provider: \"deepseek-official\"\n    model: \"deepseek-flash\"\n    reasoningEffort: \"max\"\n"},
		{"", "low", head + "    provider: \"deepseek-official\"\n    model: \"deepseek-flash\"\n    reasoningEffort: \"low\"\n"},
		{"p/m", "medium", head + "    provider: \"p\"\n    model: \"m\"\n    reasoningEffort: \"high\"\n"},
	}
	for _, tc := range cases {
		if got := dshPatch(tc.model, tc.effort); got != tc.want {
			t.Errorf("dshPatch(%q, %q) =\n%s\nwant\n%s", tc.model, tc.effort, got, tc.want)
		}
	}
}

// fakeDsh is a stand-in dsh that records its argv, environment key and a copy
// of the patch file it is given, then prints the fixture.
func fakeDsh(t *testing.T, stdout string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	fixture := filepath.Join(dir, "out.ndjson")
	if err := os.WriteFile(fixture, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + dir + `/args.txt"
printf '%s' "$DEEPSEEK_API_KEY" > "` + dir + `/key.txt"
cat > "` + dir + `/stdin.txt"
prev=""
for a in "$@"; do
  if [ "$prev" = "--patch" ]; then cp "$a" "` + dir + `/patch.yml"; printf '%s' "$a" > "` + dir + `/patchpath.txt"; fi
  prev="$a"
done
cat "` + fixture + `"
`
	bin = filepath.Join(dir, "dsh")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func collectDsh(t *testing.T, ch <-chan Token) (text string, usage *TokenUsage, err error) {
	t.Helper()
	var sb strings.Builder
	for tok := range ch {
		sb.WriteString(tok.Text)
		if tok.Error != nil {
			err = tok.Error
		}
		if tok.Usage != nil {
			usage = tok.Usage
		}
	}
	return sb.String(), usage, err
}

func TestDshRunnerEndToEnd(t *testing.T) {
	data, err := os.ReadFile("testdata/dsh_stream.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	bin, dir := fakeDsh(t, string(data))
	ch, err := DshRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{
		SystemPrompt: "SYS",
		Messages:     []ConvMessage{{Role: "user", Content: "do it"}},
		Model:        "deepseek-official/deepseek-v4-pro",
		Effort:       "high",
		Dir:          dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	text, usage, rerr := collectDsh(t, ch)
	if rerr != nil {
		t.Fatalf("run error: %v", rerr)
	}
	if !strings.HasSuffix(text, "Done: main.go now prints hi.") || usage == nil || usage.InputTokens != 5600 {
		t.Errorf("text = %q, usage = %+v", text, usage)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if !strings.HasPrefix(string(args), "--profile\nheadless\n--patch\n") || !strings.Contains(string(args), "--permission-mode\nread-only\n") {
		t.Errorf("argv = %q", args)
	}
	if stdin, _ := os.ReadFile(filepath.Join(dir, "stdin.txt")); string(stdin) != "SYS\n\nUSER:\ndo it\n\n" {
		t.Errorf("stdin = %q", stdin)
	}
	patch, _ := os.ReadFile(filepath.Join(dir, "patch.yml"))
	if string(patch) != dshPatch("deepseek-official/deepseek-v4-pro", "high") {
		t.Errorf("patch = %q", patch)
	}
	path, _ := os.ReadFile(filepath.Join(dir, "patchpath.txt"))
	if _, err := os.Stat(string(path)); !os.IsNotExist(err) {
		t.Errorf("patch file %s was not removed", path)
	}
	if key, _ := os.ReadFile(filepath.Join(dir, "key.txt")); string(key) != "sk-test" {
		t.Errorf("key = %q", key)
	}
}

func TestDshRunnerNoModelNoPatch(t *testing.T) {
	data, _ := os.ReadFile("testdata/dsh_stream.ndjson")
	bin, dir := fakeDsh(t, string(data))
	ch, err := DshRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{{Role: "user", Content: "x"}}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rerr := collectDsh(t, ch); rerr != nil {
		t.Fatal(rerr)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if strings.Contains(string(args), "--patch") {
		t.Errorf("unexpected patch: %q", args)
	}
}

func TestDshRunnerAgentGrantsGitDir(t *testing.T) {
	repo := t.TempDir()
	for _, c := range [][]string{{"init", "-q"}} {
		cmd := execGit(repo, c...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	real, err := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if got := dshGitCommonDir(repo); got != real {
		t.Errorf("git common dir = %q, want %q", got, real)
	}
	if got := dshGitCommonDir(t.TempDir()); got != "" {
		t.Errorf("outside a repo: %q", got)
	}
	data, _ := os.ReadFile("testdata/dsh_stream.ndjson")
	bin, dir := fakeDsh(t, string(data))
	ch, err := DshRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Agent: true, Dir: repo, Messages: []ConvMessage{{Role: "user", Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rerr := collectDsh(t, ch); rerr != nil {
		t.Fatal(rerr)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if !strings.Contains(string(args), "--permission-mode\nworkspace-write\n--approval-policy\nnever\n--add-dir\n"+real+"\n") {
		t.Errorf("argv = %q", args)
	}
}

func TestDshRunnerFailedRunIsAnError(t *testing.T) {
	bin, _ := fakeDsh(t, `{"type":"final","sessionId":"s","reason":{"kind":"error","error":{"code":"X","message":"model refused"}},"text":""}`+"\n")
	ch, err := DshRunner{Binary: bin}.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{{Role: "user", Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rerr := collectDsh(t, ch); rerr == nil || !strings.Contains(rerr.Error(), "model refused") {
		t.Errorf("run error = %v", rerr)
	}
}

func execGit(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd
}
