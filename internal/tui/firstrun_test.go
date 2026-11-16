package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/doctor"
)

// frDeps is a probe set: installed lists the CLIs on PATH, running the local
// servers that answer, keys the key source per provider.
func frDeps(t *testing.T, installed, running []string, keys map[string]string) FirstRunDeps {
	t.Helper()
	has := func(list []string, n string) bool {
		for _, x := range list {
			if x == n {
				return true
			}
		}
		return false
	}
	return FirstRunDeps{
		LookPath: func(n string) (string, error) {
			if has(installed, n) {
				return "/bin/" + n, nil
			}
			return "", errors.New("missing")
		},
		Reachable: func(p string) error {
			if has(running, p) {
				return nil
			}
			return errors.New("down")
		},
		KeySource: func(cred, _ string) string { return keys[cred] },
		LoggedIn:  func(n string) bool { return n == "claude" },
		Probe:     func(string, string) ([]string, error) { return []string{"m1"}, nil },
		Doctor: func() []doctor.Result {
			return []doctor.Result{
				{Name: "git", Status: doctor.OK, Detail: "git version 2"},
				{Name: "tmux", Status: doctor.Warn, Detail: "not found on PATH"},
			}
		},
	}
}

func frHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "")
	return home
}

func frKeyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// frSend feeds msg and every message its commands produce.
func frSend(t *testing.T, m firstRunModel, msg tea.Msg) (firstRunModel, bool) {
	t.Helper()
	quit := false
	var run func(tea.Model, tea.Cmd)
	cur := m
	run = func(nm tea.Model, cmd tea.Cmd) {
		cur = nm.(firstRunModel)
		for _, out := range drain(cmd) {
			if _, ok := out.(tea.QuitMsg); ok {
				quit = true
				continue
			}
			nm2, cmd2 := cur.Update(out)
			run(nm2, cmd2)
		}
	}
	nm, cmd := m.Update(msg)
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			return nm.(firstRunModel), true
		}
	}
	run(nm, cmd)
	return cur, quit
}

func frPlain(m firstRunModel, w, h int) string {
	m.width, m.height = w, h
	return ansi.Strip(m.View())
}

func TestFirstRunStatusLines(t *testing.T) {
	frHome(t)
	d := frDeps(t, []string{"claude", "opencode"}, []string{"ollama"}, map[string]string{"openai": "env OPENAI_API_KEY", "groq": "saved (~/.decsr/credentials.json)"})
	got := map[string]string{}
	for _, e := range firstRunEntries(d.withDefaults()) {
		got[e.name] = e.status
	}
	want := map[string]string{
		"claude": "claude CLI found · logged in", "opencode": "opencode found", "codex": "not installed",
		"deepseek": "needs an API key", "openai": "key from env", "openrouter": "needs an API key", "groq": "key saved",
		"ollama": "running", "lmstudio": "not running", "mlx": "not running",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s status = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["copilot"]; ok {
		t.Error("copilot listed although not installed")
	}
	d = frDeps(t, []string{"copilot"}, nil, nil)
	found := false
	for _, e := range firstRunEntries(d.withDefaults()) {
		found = found || e.name == "copilot"
	}
	if !found {
		t.Error("installed copilot missing")
	}
}

func frSelect(t *testing.T, m firstRunModel, name string) firstRunModel {
	t.Helper()
	for i, e := range m.entries {
		if e.name == name {
			m.idx = i
			return m
		}
	}
	t.Fatalf("no entry %s", name)
	return m
}

func TestFirstRunCLIFlowSavesAllRoles(t *testing.T) {
	home := frHome(t)
	m := newFirstRunModel(frDeps(t, []string{"claude"}, nil, nil))
	m = frSelect(t, m, "claude")
	m, _ = frSend(t, m, frKeyMsg("enter"))
	if m.step != frDoctor || !m.saved {
		t.Fatalf("step = %v saved = %v, want doctor and saved", m.step, m.saved)
	}
	if !m.checked || len(m.results) != 2 {
		t.Fatalf("doctor results not shown: %+v", m.results)
	}
	if v := frPlain(m, 120, 34); !strings.Contains(v, "All roles now use claude; change any role in /config.") {
		t.Errorf("step 3 lacks the roles note:\n%s", v)
	}
	m, _ = frSend(t, m, frKeyMsg("enter"))
	if !m.done {
		t.Error("Enter on step 3 did not finish")
	}
	data, err := os.ReadFile(filepath.Join(home, ".decsr", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Count(s, "runner: claude") < len(config.BuiltinRoles) || !strings.Contains(s, "model: claude-opus-5-5") {
		t.Errorf("config does not name claude for every role:\n%s", s)
	}
}

func TestFirstRunReplacesOtherRunnerModels(t *testing.T) {
	home := frHome(t)
	dir := filepath.Join(home, ".decsr")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	old := "agents:\n  coder:\n    runner: deepseek\n    model: deepseek-v4-pro\n    effort: max\n    base_url: https://x.example\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newFirstRunModel(frDeps(t, []string{"codex"}, nil, nil))
	m = frSelect(t, m, "codex")
	_, _ = frSend(t, m, frKeyMsg("enter"))
	data, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	s := string(data)
	for _, bad := range []string{"deepseek", "effort", "base_url"} {
		if strings.Contains(s, bad) {
			t.Errorf("leftover %q in config:\n%s", bad, s)
		}
	}
	if !strings.Contains(s, "runner: codex") {
		t.Errorf("codex not saved:\n%s", s)
	}
}

func TestFirstRunCLINotInstalledStays(t *testing.T) {
	home := frHome(t)
	m := newFirstRunModel(frDeps(t, nil, nil, nil))
	m = frSelect(t, m, "codex")
	m, _ = frSend(t, m, frKeyMsg("enter"))
	if m.step != frPick || m.notice == "" || m.saved {
		t.Fatalf("step = %v notice = %q saved = %v", m.step, m.notice, m.saved)
	}
	if _, err := os.Stat(filepath.Join(home, ".decsr", "config.yaml")); err == nil {
		t.Error("config written for a runner that is not installed")
	}
}

func TestFirstRunAPIWithoutKeyGoesThroughStep2(t *testing.T) {
	home := frHome(t)
	m := newFirstRunModel(frDeps(t, nil, nil, nil))
	m = frSelect(t, m, "deepseek")
	m, _ = frSend(t, m, frKeyMsg("enter"))
	if m.step != frKey || m.conn == nil {
		t.Fatalf("step = %v, want key", m.step)
	}
	if v := frPlain(m, 120, 34); !strings.Contains(v, "2 of 3") || !strings.Contains(v, "paste the API key") {
		t.Errorf("step 2 view:\n%s", v)
	}
	m, _ = frSend(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-test-1234567890")})
	m, _ = frSend(t, m, frKeyMsg("enter"))
	if m.step != frDoctor || !m.saved {
		t.Fatalf("step = %v saved = %v, want doctor after the key", m.step, m.saved)
	}
	if _, err := os.Stat(filepath.Join(home, ".decsr", "credentials.json")); err != nil {
		t.Errorf("key not saved: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".decsr", "config.yaml"))
	if !strings.Contains(string(data), "model: deepseek-v4-flash") {
		t.Errorf("deepseek default model missing:\n%s", data)
	}
	if strings.Contains(string(data), "sk-test") {
		t.Error("key leaked into the config")
	}
}

func TestFirstRunEscSkips(t *testing.T) {
	home := frHome(t)
	m := newFirstRunModel(frDeps(t, []string{"claude"}, nil, nil))
	m, quit := frSend(t, m, frKeyMsg("esc"))
	if !quit || m.done || m.saved {
		t.Fatalf("quit = %v done = %v saved = %v", quit, m.done, m.saved)
	}
	if _, err := os.Stat(filepath.Join(home, ".decsr", "config.yaml")); err == nil {
		t.Error("skip wrote a config")
	}
}

func TestFirstRunEscInStep2GoesBack(t *testing.T) {
	frHome(t)
	m := newFirstRunModel(frDeps(t, nil, nil, nil))
	m = frSelect(t, m, "deepseek")
	m, _ = frSend(t, m, frKeyMsg("enter"))
	m, _ = frSend(t, m, frKeyMsg("esc"))
	if m.step != frPick {
		t.Fatalf("step = %v, want pick", m.step)
	}
}

func TestFirstRunRenderWidths(t *testing.T) {
	frHome(t)
	for _, sz := range [][2]int{{80, 24}, {120, 34}} {
		w, h := sz[0], sz[1]
		m := newFirstRunModel(frDeps(t, []string{"claude", "opencode"}, nil, nil))
		check := func(label string, m firstRunModel) {
			v := frPlain(m, w, h)
			lines := strings.Split(v, "\n")
			if len(lines) > h {
				t.Errorf("%s %dx%d: %d lines", label, w, h, len(lines))
			}
			for _, l := range lines {
				if ansi.StringWidth(l) > w {
					t.Errorf("%s %dx%d: line wider than %d: %q", label, w, h, w, l)
				}
			}
		}
		check("step1", m)
		m2, _ := frSend(t, frSelect(t, m, "claude"), frKeyMsg("enter"))
		check("step3", m2)
		m3 := newFirstRunModel(frDeps(t, nil, nil, nil))
		m3, _ = frSend(t, frSelect(t, m3, "deepseek"), frKeyMsg("enter"))
		check("step2", m3)
		if w == 120 {
			t.Logf("step 1:\n%s", frPlain(m, w, h))
			t.Logf("step 3:\n%s", frPlain(m2, w, h))
		}
	}
}
