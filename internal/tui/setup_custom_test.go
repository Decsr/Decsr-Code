package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
)

// modelServer answers GET /models with ids and records the last Authorization.
func modelServer(t *testing.T, ids ...string) (*httptest.Server, *string) {
	t.Helper()
	auth := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*auth = r.Header.Get("Authorization")
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		out := `{"data":[`
		for i, id := range ids {
			if i > 0 {
				out += ","
			}
			out += `{"id":"` + id + `"}`
		}
		_, _ = w.Write([]byte(out + `]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, auth
}

func saveCustom(t *testing.T, name, base string) {
	t.Helper()
	if err := credentials.Set(name, credentials.Cred{APIKey: tuiKey, BaseURL: base}); err != nil {
		t.Fatal(err)
	}
}

func globalSetupFor(t *testing.T, runnerName, model string, agents map[string]config.AgentConfig) SetupModel {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, v := range []string{"DEEPSEEK_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "GROQ_API_KEY"} {
		t.Setenv(v, "")
	}
	return NewSetupModel(runnerName, model, "", agents)
}

func prep(m SetupModel) SetupModel {
	m.globalOnly = true
	m.width, m.height = 140, 40
	m.syncViewport()
	return m
}

func press(m SetupModel, s string) (SetupModel, tea.Cmd) {
	switch s {
	case "enter":
		return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	case "esc":
		return m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	case "right":
		return m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

// run feeds the messages of cmd back into the model until none are left.
func run(m SetupModel, cmd tea.Cmd) SetupModel {
	for _, msg := range drain(cmd) {
		if _, tick := msg.(spinner.TickMsg); tick {
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		m = run(m, next)
	}
	return m
}

func kindByLabel(t *testing.T, m SetupModel, label string) providerKind {
	t.Helper()
	for i, e := range m.entries {
		if e.label == label {
			return providerKind(i)
		}
	}
	t.Fatalf("no provider entry %q", label)
	return 0
}

func TestSetupRowListsSavedCustomProviders(t *testing.T) {
	_ = globalSetupFor(t, "opencode", "", nil)
	saveCustom(t, "acme", "https://api.acme.test/v1")
	m := prep(NewSetupModel("opencode", "", "", nil))
	view := ansi.Strip(m.View())
	for _, want := range []string{"acme (custom)", "+ custom…", "Groq"} {
		if !strings.Contains(view, want) {
			t.Errorf("row lacks %q:\n%s", want, view)
		}
	}
	n := len(m.entries)
	if m.entries[n-1].label != "+ custom…" || m.entries[n-2].label != "acme (custom)" {
		t.Errorf("order: %+v", m.entries)
	}
}

func TestSetupGlobalCustomProviderRoundTrip(t *testing.T) {
	_ = globalSetupFor(t, "claude", "sonnet", map[string]config.AgentConfig{"pm": {Runner: "claude", Model: "sonnet"}, "coder": {Runner: "claude", Model: "sonnet"}})
	saveCustom(t, "acme", "https://api.acme.test/v1")
	base := map[string]config.AgentConfig{"pm": {Runner: "claude", Model: "sonnet"}, "coder": {Runner: "claude", Model: "sonnet"}}
	m := prep(NewSetupModel("claude", "sonnet", "", base))
	m.provider = kindByLabel(t, m, "acme (custom)")
	m.apiModels[m.entry(m.provider).key()] = []string{"gpt-x"}
	m.apiModelsLoading[m.entry(m.provider).key()] = false
	m.effortTouched = true
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"pm", "coder"} {
		ac := cfg.Agents[role]
		if ac.Runner != "openai" || ac.Provider != "acme" || ac.Model != "gpt-x" || ac.BaseURL != "" {
			t.Fatalf("%s = %+v", role, ac)
		}
	}
	re := NewSetupModel("openai", "gpt-x", "", cfg.Agents)
	if e := re.entry(re.provider); e.provider != "acme" || e.missing {
		t.Fatalf("reloaded entry = %+v", e)
	}
	// Runner openai without a provider is the OpenAI preset.
	plain := NewSetupModel("openai", "gpt-4o", "", map[string]config.AgentConfig{"pm": {Runner: "openai"}})
	if plain.provider != providerOpenAI {
		t.Errorf("plain openai = %v", plain.provider)
	}
	// Switching to a built-in runner clears provider.
	re = prep(re)
	re.provider = providerClaude
	re.effortTouched = true
	if err := re.savePayload(re.currentPayload()); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ac := cfg.Agents["pm"]; ac.Runner != "claude" || ac.Provider != "" {
		t.Fatalf("after switch: %+v", ac)
	}
	if data, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".decsr", "config.yaml")); strings.Contains(string(data), "provider:") {
		t.Errorf("provider still in file:\n%s", data)
	}
}

func TestSetupAgentCustomProviderRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saveCustom(t, "acme", "https://api.acme.test/v1")
	root := t.TempDir()
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "", "proj", nil)
	m.root = root
	m.width, m.height = 140, 40
	m.syncViewport()
	acme := kindByLabel(t, m, "acme (custom)")
	m.section = sectionAgentSetup
	m.agentCursor = 1 // coder
	m.agentEditing, m.agentEditStep, m.agentEditProv = true, 0, acme
	m.apiModelsLoading[m.entry(acme).key()] = false
	m, _ = press(m, "enter")
	if m.agentEditStep != 1 || !m.typedMode(acme) {
		t.Fatalf("step %d typed %v", m.agentEditStep, m.typedMode(acme))
	}
	m.typed.SetValue("gpt-x")
	m, _ = press(m, "enter")
	m.agentEffortLoading = false
	m, _ = press(m, "enter")
	ac := config.LoadProject(root).Agents["coder"]
	if ac.Runner != "openai" || ac.Provider != "acme" || ac.Model != "gpt-x" || ac.BaseURL != "" {
		t.Fatalf("coder = %+v", ac)
	}
	re := NewSetupModelWithOverrides("claude", "sonnet", "default", "", "proj", config.LoadProject(root).Agents)
	re.agentCursor = 1
	m2, _ := re.handleAgentSetupKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if e := m2.entry(m2.agentEditProv); e.provider != "acme" {
		t.Fatalf("reloaded agent entry = %+v", e)
	}
	// Switching the agent to a built-in runner clears provider.
	m.agentOverrides["coder"] = agentSetupOverride{runner: "claude", model: "sonnet"}
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	if ac := config.LoadProject(root).Agents["coder"]; ac.Provider != "" || ac.Runner != "claude" {
		t.Fatalf("after switch: %+v", ac)
	}
}

func TestSetupMissingCustomProvider(t *testing.T) {
	_ = globalSetupFor(t, "openai", "m", nil)
	m := prep(NewSetupModel("openai", "m", "", map[string]config.AgentConfig{"pm": {Runner: "openai", Provider: "ghost", Model: "m"}}))
	view := ansi.Strip(m.View())
	for _, want := range []string{"ghost (missing)", "decsr connect custom"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if e := m.entry(m.provider); !e.missing || e.provider != "ghost" {
		t.Fatalf("entry = %+v", e)
	}
	m.section = sectionModel
	_ = ansi.Strip(m.View()) // must not panic
	m, _ = press(m, "u")
	if m.urlEdit != nil {
		t.Error("u opened on a missing provider")
	}
}

func TestSetupBaseURLValidationAndPresetOverride(t *testing.T) {
	srv, auth := modelServer(t, "zzz")
	_ = globalSetupFor(t, "openai", "", map[string]config.AgentConfig{"pm": {Runner: "openai"}, "coder": {Runner: "openai"}})
	t.Setenv("OPENAI_API_KEY", tuiKey)
	m := prep(NewSetupModel("openai", "", "", map[string]config.AgentConfig{"pm": {Runner: "openai"}, "coder": {Runner: "openai"}}))
	m, _ = press(m, "u")
	if m.urlEdit == nil || m.urlEdit.in.Value() != "https://api.openai.com/v1" {
		t.Fatalf("not prefilled: %+v", m.urlEdit)
	}
	m.urlEdit.in.SetValue("http://example.com/v1")
	m, _ = press(m, "enter")
	if m.urlEdit == nil || m.urlEdit.err == "" {
		t.Fatal("http remote accepted")
	}
	if _, ok := m.baseURLs["openai"]; ok {
		t.Fatal("rejected URL stored")
	}
	m.urlEdit.in.SetValue("ftp://localhost/v1")
	m, _ = press(m, "enter")
	if m.urlEdit == nil {
		t.Fatal("ftp accepted")
	}
	m.urlEdit.in.SetValue(srv.URL + "/")
	m, cmd := press(m, "enter")
	if m.urlEdit != nil {
		t.Fatalf("http localhost rejected: %s", m.urlEdit.err)
	}
	if m.baseURLs["openai"] != srv.URL {
		t.Fatalf("override = %q", m.baseURLs["openai"])
	}
	// The model list is fetched again from the new URL, with the key.
	m = run(m, cmd)
	if got := m.apiModels["openai|"]; len(got) != 1 || got[0] != "zzz" {
		t.Fatalf("models = %v", got)
	}
	if *auth != "Bearer "+tuiKey {
		t.Errorf("fetch did not use the key")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Base URL: "+srv.URL+" (override)") || strings.Contains(view, tuiKey) {
		t.Fatalf("view:\n%s", view)
	}
	// Saved as base_url of every agent.
	m.effortTouched = true
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(t.TempDir())
	if cfg.Agents["pm"].BaseURL != srv.URL || cfg.Agents["coder"].BaseURL != srv.URL {
		t.Fatalf("saved = %+v", cfg.Agents)
	}
	// Empty input removes the override.
	m, _ = press(m, "u")
	m.urlEdit.in.SetValue("")
	m, _ = press(m, "enter")
	if m.urlEdit != nil || m.baseURLs["openai"] != "" {
		t.Fatalf("reset failed: %v %q", m.urlEdit, m.baseURLs["openai"])
	}
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(t.TempDir())
	if cfg.Agents["pm"].BaseURL != "" {
		t.Fatalf("override not removed: %+v", cfg.Agents["pm"])
	}
	if !strings.Contains(ansi.Strip(m.View()), "Base URL: https://api.openai.com/v1") {
		t.Error("preset URL not shown after reset")
	}
	// Esc cancels without changes.
	m, _ = press(m, "u")
	m.urlEdit.in.SetValue("https://other.test/v1")
	m, _ = press(m, "esc")
	if m.urlEdit != nil || m.baseURLs["openai"] != "" {
		t.Fatal("esc changed the override")
	}
}

func TestSetupBaseURLUpdatesCustomProviderKeepsKey(t *testing.T) {
	old, _ := modelServer(t, "old-model")
	fresh, auth := modelServer(t, "new-model")
	_ = globalSetupFor(t, "openai", "", map[string]config.AgentConfig{"pm": {Runner: "openai", Provider: "acme"}})
	saveCustom(t, "acme", old.URL)
	m := prep(NewSetupModel("openai", "", "", map[string]config.AgentConfig{"pm": {Runner: "openai", Provider: "acme"}}))
	if e := m.entry(m.provider); e.provider != "acme" || e.baseURL != old.URL {
		t.Fatalf("entry = %+v", e)
	}
	m, _ = press(m, "u")
	m.urlEdit.in.SetValue("")
	m, _ = press(m, "enter")
	if m.urlEdit == nil || m.urlEdit.err == "" {
		t.Fatal("empty URL accepted for a custom provider")
	}
	m.urlEdit.in.SetValue("http://example.com/v1")
	m, _ = press(m, "enter")
	if m.urlEdit == nil {
		t.Fatal("http remote accepted")
	}
	m.urlEdit.in.SetValue(fresh.URL)
	m, cmd := press(m, "enter")
	if m.urlEdit != nil {
		t.Fatalf("rejected: %s", m.urlEdit.err)
	}
	c, ok, _ := credentials.Get("acme")
	if !ok || c.BaseURL != fresh.URL || c.APIKey != tuiKey {
		t.Fatalf("stored = %+v ok=%v", c, ok)
	}
	m = run(m, cmd)
	if got := m.apiModels["openai|acme"]; len(got) != 1 || got[0] != "new-model" {
		t.Fatalf("models = %v", got)
	}
	if *auth != "Bearer "+tuiKey {
		t.Error("fetch did not use the saved key")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Base URL: "+fresh.URL) || strings.Contains(view, "(override)") || strings.Contains(view, tuiKey) {
		t.Fatalf("view:\n%s", view)
	}
	// BaseURL stays empty in the config so the saved one is used.
	m.effortTouched = true
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(t.TempDir())
	if ac := cfg.Agents["pm"]; ac.Provider != "acme" || ac.BaseURL != "" {
		t.Fatalf("pm = %+v", ac)
	}
}

func TestSetupTypedModelFallback(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(bad.Close)
	_ = globalSetupFor(t, "openai", "", nil)
	saveCustom(t, "acme", bad.URL)
	m := prep(NewSetupModel("openai", "", "", map[string]config.AgentConfig{"pm": {Runner: "openai", Provider: "acme"}}))
	if m.typedMode(m.provider) {
		t.Fatal("typed mode while loading")
	}
	m = run(m, fetchEntryModels(m.entry(m.provider), ""))
	if !m.typedMode(m.provider) {
		t.Fatal("no typed mode after a failed fetch")
	}
	m.section = sectionModel
	m, _ = press(m, "u") // typed into the input, not a shortcut
	m, _ = press(m, "k")
	if m.urlEdit != nil || m.typedModel() != "uk" {
		t.Fatalf("typed = %q urlEdit=%v", m.typedModel(), m.urlEdit)
	}
	m.typed.SetValue("my-model")
	if _, model := m.selectedProviderModel(); model != "my-model" {
		t.Fatalf("model = %q", model)
	}
	if p := m.currentPayload(); p.model != "my-model" || p.provider != "openai" || p.providerName != "acme" {
		t.Fatalf("payload = %+v", p)
	}
	if !strings.Contains(ansi.Strip(m.View()), "type a model id") {
		t.Error("typed input not shown")
	}
	m, _ = press(m, "enter")
	if m.section != sectionEffort {
		t.Errorf("enter did not move on: %v", m.section)
	}
}

func TestSetupNewCustomRow(t *testing.T) {
	m := prep(globalSetupFor(t, "groq", "", nil))
	m.provider = m.revertKind() + 1 // + custom…
	if !m.entry(m.provider).isNew {
		t.Fatal("not the new row")
	}
	m, _ = press(m, "enter")
	if m.connect == nil || m.connect.stage != connName {
		t.Fatalf("connect = %+v", m.connect)
	}
	m, _ = press(m, "esc")
	m = run(m, func() tea.Msg { return connectClosedMsg{} })
	if m.connect != nil || m.entry(m.provider).isNew {
		t.Fatalf("not reverted: connect=%v entry=%+v", m.connect, m.entry(m.provider))
	}

	srv, _ := modelServer(t, "m1")
	m.provider = m.revertKind() + 1
	m, _ = press(m, "enter")
	m.connect.probe = func(string, string) ([]string, error) { return []string{"m1"}, nil }
	for _, step := range []string{"acme2", srv.URL, tuiKey} {
		m, _ = press(m, step)
		var cmd tea.Cmd
		m, cmd = press(m, "enter")
		m = run(m, cmd)
		if strings.Contains(ansi.Strip(m.View()), tuiKey) {
			t.Fatal("key rendered")
		}
	}
	if m.connect != nil {
		t.Fatalf("overlay open: stage %v err %q", m.connect.stage, m.connect.err)
	}
	if e := m.entry(m.provider); e.provider != "acme2" || e.missing || e.isNew {
		t.Fatalf("selected = %+v", e)
	}
	if c, ok, _ := credentials.Get("acme2"); !ok || c.BaseURL != srv.URL || c.APIKey != tuiKey {
		t.Fatalf("stored = %+v", c)
	}
	if !strings.Contains(ansi.Strip(m.View()), "acme2 (custom)") {
		t.Error("row lacks the new provider")
	}
}

func TestSetupViewNeverShowsKey(t *testing.T) {
	_ = globalSetupFor(t, "openai", "", nil)
	saveCustom(t, "acme", "https://api.acme.test/v1")
	t.Setenv("OPENAI_API_KEY", tuiKey)
	m := prep(NewSetupModel("openai", "", "", map[string]config.AgentConfig{"pm": {Runner: "openai", Provider: "acme"}}))
	for _, sec := range []setupSection{sectionProvider, sectionModel, sectionEffort} {
		m.section = sec
		m.syncViewport()
		if v := m.View(); strings.Contains(v, tuiKey) || strings.Contains(ansi.Strip(v), tuiKey) {
			t.Fatalf("key in view of section %v", sec)
		}
	}
	m, _ = press(m, "u")
	if v := m.View(); strings.Contains(v, tuiKey) {
		t.Fatal("key in URL editor view")
	}
}
