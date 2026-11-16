package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

const tuiKey = "sk-tui-secret-0123456789zz"

// drain runs cmd (flattening batches) and returns the messages it produced.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, drain(c)...)
		}
		return out
	case connectTickMsg:
		return nil
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

func (h *wsHarness) typeKey(s string) {
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

// settle feeds the messages of cmd back, repeating until no command is left.
func (h *wsHarness) settle(cmd tea.Cmd) {
	h.t.Helper()
	for _, msg := range drain(cmd) {
		h.settle(h.send(msg))
	}
}

func TestWorkspaceConnectFlow(t *testing.T) {
	h := newWSHarness(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	var probed []string
	h.m.deps.probeKey = func(base, key string) ([]string, error) {
		probed = append(probed, base+"|"+key)
		return []string{"deepseek-v4-flash", "deepseek-v4-pro"}, nil
	}
	h.m.input.SetValue("/connect")
	h.key("enter")
	if h.m.overlay != wsOverlayConnect {
		t.Fatalf("overlay = %v\n%s", h.m.overlay, h.view())
	}
	h.want("DeepSeek")
	h.want("not set")
	h.key("enter") // DeepSeek is first
	h.typeKey(tuiKey)
	if v := h.m.View(); strings.Contains(ansi.Strip(v), tuiKey) || strings.Contains(v, tuiKey) {
		t.Fatalf("key rendered:\n%s", ansi.Strip(v))
	}
	h.want("••••")
	h.settle(h.key("enter"))
	if len(probed) != 1 || probed[0] != "https://api.deepseek.com|"+tuiKey {
		t.Fatalf("probed = %v", probed)
	}
	h.wantNot(tuiKey)
	if c, ok, _ := credentials.Get("deepseek"); !ok || c.APIKey != tuiKey {
		t.Fatal("key not saved")
	}
	h.want("the lead only")
	h.settle(h.key("2"))
	if h.m.overlay != wsOverlayNone {
		t.Fatalf("overlay still open:\n%s", h.view())
	}
	h.want("Connected deepseek (key saved, 2 models)")
	for _, r := range []string{"pm", "coder"} {
		if a := h.m.cfg.Agents[r]; a.Runner != "deepseek" || a.Model != "deepseek-v4-flash" {
			t.Errorf("%s = %+v", r, a)
		}
	}
}

func TestWorkspaceConnectRejectedAndCancel(t *testing.T) {
	h := newWSHarness(t)
	h.m.deps.probeKey = func(string, string) ([]string, error) { return nil, &runner.KeyRejectedError{Status: 401} }
	h.m.input.SetValue("/connect groq")
	h.key("enter")
	h.typeKey(tuiKey)
	h.settle(h.key("enter"))
	h.want("key rejected")
	h.wantNot(tuiKey)
	if _, ok, _ := credentials.Get("groq"); ok {
		t.Fatal("rejected key saved")
	}
	h.key("esc")
	if h.m.overlay != wsOverlayNone {
		h.settle(h.key("esc"))
	}
	if h.m.overlay != wsOverlayNone {
		t.Fatalf("esc did not close:\n%s", h.view())
	}
}

func TestWorkspaceConnectNetworkErrorSaveAnyway(t *testing.T) {
	h := newWSHarness(t)
	h.m.deps.probeKey = func(string, string) ([]string, error) { return nil, errors.New("dial tcp: no route") }
	h.m.input.SetValue("/connect openrouter")
	h.key("enter")
	h.typeKey(tuiKey)
	h.settle(h.key("enter"))
	h.want("save anyway")
	h.settle(h.key("y"))
	if _, ok, _ := credentials.Get("openrouter"); !ok {
		t.Fatal("not saved after y")
	}
}

func TestWorkspaceConnectCustomValidation(t *testing.T) {
	h := newWSHarness(t)
	h.m.deps.probeKey = func(string, string) ([]string, error) { return []string{"x"}, nil }
	h.m.input.SetValue("/connect custom")
	h.key("enter")
	h.typeKey("deepseek")
	h.key("enter")
	h.want("built-in provider name")
	for range "deepseek" {
		h.key("backspace")
	}
	h.typeKey("acme")
	h.key("enter")
	h.typeKey("http://example.com/v1")
	h.key("enter")
	h.want("use https")
	for range "http://example.com/v1" {
		h.key("backspace")
	}
	h.typeKey("http://localhost:8080/v1")
	h.key("enter")
	h.typeKey(tuiKey)
	h.settle(h.key("enter"))
	c, ok, _ := credentials.Get("acme")
	if !ok || c.BaseURL != "http://localhost:8080/v1" {
		t.Fatalf("cred = %+v ok=%v", c, ok)
	}
}

func apiSetup(t *testing.T) SetupModel {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, v := range []string{"DEEPSEEK_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "GROQ_API_KEY"} {
		t.Setenv(v, "")
	}
	m := NewSetupModel("opencode", "", "", map[string]config.AgentConfig{})
	m.globalOnly = true
	m.width, m.height = 100, 40
	m.syncViewport()
	return m
}

func TestSetupKeyInputIsMasked(t *testing.T) {
	m := apiSetup(t)
	m.provider = providerDeepSeek
	m.syncViewport()
	if !strings.Contains(ansi.Strip(m.View()), "API key: not set — press k to add") {
		t.Fatalf("no key hint:\n%s", ansi.Strip(m.View()))
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.connect == nil {
		t.Fatal("k did not open the key input")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tuiKey)})
	if v := m.View(); strings.Contains(ansi.Strip(v), tuiKey) || strings.Contains(v, tuiKey) {
		t.Fatalf("setup view shows the key:\n%s", ansi.Strip(v))
	}
	m.connect.probe = func(string, string) ([]string, error) { return []string{"deepseek-v4-flash"}, nil }
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, msg := range drain(cmd) {
		var next tea.Cmd
		m, next = m.Update(msg)
		for _, msg2 := range drain(next) {
			m, _ = m.Update(msg2)
		}
	}
	if m.connect != nil {
		t.Fatalf("input still open: stage %v err %q", m.connect.stage, m.connect.err)
	}
	if got := m.apiKeyLine(); got != "API key: saved" {
		t.Errorf("key line = %q", got)
	}
	if strings.Contains(ansi.Strip(m.View()), tuiKey) {
		t.Error("key in view after save")
	}
}

func TestSetupEnvKeyLineAndNewProviders(t *testing.T) {
	m := apiSetup(t)
	t.Setenv("GROQ_API_KEY", tuiKey)
	m.provider = providerGroq
	m.syncViewport()
	if got := m.apiKeyLine(); got != "API key: from GROQ_API_KEY" {
		t.Errorf("line = %q", got)
	}
	for _, want := range []string{"OpenAI", "OpenRouter", "Groq", "DeepSeek"} {
		if !strings.Contains(ansi.Strip(m.View()), want) {
			t.Errorf("provider list lacks %s", want)
		}
	}
	if providerFromString("openrouter") != providerOpenRouter || providerToString(providerGroq) != "groq" {
		t.Error("provider mapping")
	}
	m.provider = providerOpenCode
	if m.apiKeyLine() != "" {
		t.Error("non-API provider shows a key line")
	}
}
