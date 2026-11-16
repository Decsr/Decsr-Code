package doctor

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

func baseDeps() Deps {
	return Deps{
		LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
		Getenv:   func(k string) string { return map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}[k] },
		Run: func(n string, a ...string) (string, error) {
			if len(a) > 0 && a[len(a)-1] == "--is-inside-work-tree" {
				return "true\n", nil
			}
			return n + " v1\nline2", nil
		},
		Reachable: func(string) error { return nil },
		Writable:  func(string) error { return nil },
		Root:      "/proj",
		Home:      "/home/u",
		Agents:    []Agent{{Name: "coder", Runner: "claude"}, {Name: "pm"}},
	}
}

func find(rs []Result, name string) (Result, bool) {
	for _, r := range rs {
		if r.Name == name {
			return r, true
		}
	}
	return Result{}, false
}

func TestRunAllGood(t *testing.T) {
	rs := Run(baseDeps())
	if Failed(rs) {
		t.Fatalf("unexpected failure: %+v", rs)
	}
	for _, n := range []string{"git", "git repo", "config", "runner claude", "runner opencode", "backend", "tmux", "gh", "gh auth", "terminal"} {
		r, ok := find(rs, n)
		if !ok || r.Status != OK {
			t.Errorf("%s: got %+v (found=%v), want OK", n, r, ok)
		}
	}
	if r, _ := find(rs, "git"); r.Detail != "git v1" {
		t.Errorf("git detail = %q, want first line only", r.Detail)
	}
}

func TestRequiredFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Deps)
		check  string
	}{
		{"no git", func(d *Deps) {
			d.LookPath = func(n string) (string, error) {
				if n == "git" {
					return "", errors.New("nope")
				}
				return "/x", nil
			}
		}, "git"},
		{"not writable", func(d *Deps) { d.Writable = func(string) error { return errors.New("denied") } }, ".decsr writable"},
		{"config error", func(d *Deps) { d.ConfigErr = errors.New("bad yaml") }, "config"},
		{"no runner usable", func(d *Deps) { d.Reachable = func(string) error { return errors.New("down") } }, "backend"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := baseDeps()
			tc.mutate(&d)
			rs := Run(d)
			r, ok := find(rs, tc.check)
			if !ok || r.Status != Fail || !r.Required || r.Hint == "" {
				t.Errorf("%s: got %+v, want required Fail with hint", tc.check, r)
			}
			if !Failed(rs) {
				t.Error("Failed() = false, want true")
			}
		})
	}
}

func TestNotGitRepoWarns(t *testing.T) {
	d := baseDeps()
	d.Run = func(n string, a ...string) (string, error) {
		if len(a) > 0 && a[len(a)-1] == "--is-inside-work-tree" {
			return "", errors.New("fatal")
		}
		return "ok", nil
	}
	rs := Run(d)
	if r, _ := find(rs, "git repo"); r.Status != Warn {
		t.Errorf("git repo = %+v, want Warn", r)
	}
	if Failed(rs) {
		t.Error("not a git repo must not fail the run")
	}
}

func TestWritableFallsBackToHome(t *testing.T) {
	d := baseDeps()
	d.Writable = func(dir string) error {
		if dir == "/proj/.decsr" {
			return errors.New("ro")
		}
		return nil
	}
	r, _ := find(Run(d), ".decsr writable")
	if r.Status != Warn || !strings.Contains(r.Detail, "/home/u/.decsr") {
		t.Errorf("got %+v, want Warn mentioning home fallback", r)
	}
}

func TestConfigLayers(t *testing.T) {
	d := baseDeps()
	d.Layers = []Layer{{"global", "/home/u/.decsr/config.yaml", true}, {"project", "/proj/.decsr/project.yaml", false}}
	r, _ := find(Run(d), "config")
	if !strings.Contains(r.Detail, "/home/u/.decsr/config.yaml") || strings.Contains(r.Detail, "project.yaml") {
		t.Errorf("config detail = %q", r.Detail)
	}
}

func TestAPIRunners(t *testing.T) {
	env := map[string]string{"MY_KEY": "sekret-value"}
	d := baseDeps()
	d.Getenv = func(k string) string { return env[k] }
	d.Agents = []Agent{
		{Name: "coder", Runner: "deepseek"},                                          // default DEEPSEEK_API_KEY, unset
		{Name: "qa", Runner: "openai", BaseURL: "https://x/v1", APIKeyEnv: "MY_KEY"}, // fine
		{Name: "pm", Runner: "openai", APIKeyEnv: "MY_KEY"},                          // missing base_url
		{Name: "sec", Runner: "openai", BaseURL: "https://x/v1", APIKeyEnv: "OTHER"}, // key unset
	}
	rs := Run(d)

	var buf bytes.Buffer
	Print(&buf, rs)
	out := buf.String()
	if strings.Contains(out, "sekret-value") {
		t.Fatal("key value leaked into output")
	}
	for _, want := range []string{"DEEPSEEK_API_KEY is not set (agents: coder)", "OTHER is not set (agents: sec)", "MY_KEY is set (agents: pm, qa)", "base_url is not set (agents: pm)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// API-runner problems are not required checks.
	if r, _ := find(rs, "runner deepseek key"); r.Required {
		t.Error("api key check must not be required")
	}
}

func TestDeepSeekReachability(t *testing.T) {
	d := baseDeps()
	d.Getenv = func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "k"
		}
		return ""
	}
	d.Agents = []Agent{{Name: "coder", Runner: "deepseek"}}
	var mu sync.Mutex
	var asked []string
	d.Reachable = func(p string) error {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, p)
		return errors.New("unreachable")
	}
	r, _ := find(Run(d), "runner deepseek")
	if r.Status != Warn {
		t.Errorf("got %+v, want Warn", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) == 0 {
		t.Error("reachability not consulted")
	}
}

func TestOptionalOnlyWarn(t *testing.T) {
	d := baseDeps()
	d.LookPath = func(n string) (string, error) {
		if n == "tmux" || n == "gh" {
			return "", errors.New("missing")
		}
		return "/x", nil
	}
	d.Getenv = func(string) string { return "" }
	rs := Run(d)
	for _, n := range []string{"tmux", "gh", "gh auth", "terminal"} {
		if r, _ := find(rs, n); r.Status != Warn || r.Hint == "" {
			t.Errorf("%s = %+v, want Warn with hint", n, r)
		}
	}
	if Failed(rs) {
		t.Error("optional checks must not fail the run")
	}
}

func TestGHNotLoggedIn(t *testing.T) {
	d := baseDeps()
	d.Run = func(n string, a ...string) (string, error) {
		if n == "gh" {
			return "", errors.New("exit 1")
		}
		return "ok", nil
	}
	if r, _ := find(Run(d), "gh auth"); r.Status != Warn {
		t.Errorf("gh auth = %+v, want Warn", r)
	}
}

func TestPrintHasGlyphAndWord(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, []Result{
		{Name: "a", Status: OK, Detail: "fine"},
		{Name: "b", Status: Warn, Detail: "meh", Hint: "do x"},
		{Name: "c", Status: Fail, Required: true, Detail: "bad", Hint: "do y"},
	})
	out := buf.String()
	for _, want := range []string{"✓ OK", "! WARN", "✗ FAIL", "fix: do x", "fix: do y", "1 ok, 1 warnings, 1 failed: not ready"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fix: fine") {
		t.Error("OK lines must not print hints")
	}
}

func TestNotifyCheck(t *testing.T) {
	const secret = "tok3n-secret"
	tests := []struct {
		name     string
		goos     string
		n        NotifyInfo
		noTool   bool
		env      map[string]string
		want     Status
		contains []string
		absent   []string
	}{
		{"linux all", "linux", NotifyInfo{Webhook: "https://ntfy.sh/" + secret}, false, map[string]string{"DISPLAY": ":0"}, OK, []string{"cmux", "bell", "desktop", "webhook ntfy.sh (ntfy)"}, []string{secret}},
		{"linux no display", "linux", NotifyInfo{}, false, nil, Warn, []string{"DISPLAY"}, []string{"desktop,"}},
		{"linux no tool", "linux", NotifyInfo{}, true, map[string]string{"DISPLAY": ":0"}, Warn, []string{"notify-send"}, nil},
		{"linux desktop off", "linux", NotifyInfo{DisableDesktop: true}, true, nil, OK, []string{"cmux", "bell"}, []string{"desktop"}},
		{"windows", "windows", NotifyInfo{}, false, nil, OK, []string{"cmux"}, []string{"bell"}},
		{"slack", "darwin", NotifyInfo{DisableCMux: true, DisableBell: true, Webhook: "https://hooks.slack.com/x", WebhookFormat: "slack"}, false, nil, OK, []string{"desktop", "hooks.slack.com (slack)"}, []string{"cmux"}},
		{"bad webhook", "linux", NotifyInfo{DisableDesktop: true, Webhook: "ftp://x"}, false, nil, Warn, []string{"not a valid"}, nil},
		{"nothing", "linux", NotifyInfo{DisableCMux: true, DisableBell: true, DisableDesktop: true}, false, nil, OK, []string{"no channel active"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := baseDeps()
			d.GOOS, d.Notify = tc.goos, tc.n
			d.Getenv = func(k string) string { return tc.env[k] }
			d.LookPath = func(n string) (string, error) {
				if tc.noTool && n == "notify-send" {
					return "", errors.New("missing")
				}
				return "/usr/bin/" + n, nil
			}
			r, ok := find(Run(d), "notify")
			if !ok || r.Status != tc.want {
				t.Fatalf("notify = %+v (found=%v), want status %v", r, ok, tc.want)
			}
			if r.Required {
				t.Error("notify must be optional")
			}
			if tc.want == Warn && r.Hint == "" {
				t.Error("warning without a hint")
			}
			for _, s := range tc.contains {
				if !strings.Contains(r.Detail, s) {
					t.Errorf("detail %q lacks %q", r.Detail, s)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(r.Detail+r.Hint, s) {
					t.Errorf("detail %q must not contain %q", r.Detail, s)
				}
			}
		})
	}
}
