package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// providerEntry is one choice of the setup provider row: a built-in runner, a
// saved custom provider (runner "openai" plus provider name), a custom provider
// named by the config but no longer saved, or the "+ custom…" row.
type providerEntry struct {
	label    string
	runner   string // value of `runner:`
	provider string // value of `provider:` ("" unless a custom provider)
	baseURL  string // saved base URL of a custom provider
	missing  bool
	isNew    bool
}

const newCustomLabel = "+ custom…"

// key identifies the entry across list reloads.
func (e providerEntry) key() string {
	if e.isNew {
		return "+new"
	}
	return e.runner + "|" + e.provider
}

// credName is the credential/preset name behind an API entry, "" for the rest.
func (e providerEntry) credName() string {
	switch {
	case e.isNew:
		return ""
	case e.provider != "":
		return e.provider
	case runner.IsPreset(e.runner):
		return e.runner
	}
	return ""
}

// isAPI reports whether the entry is an OpenAI-compatible API provider.
func (e providerEntry) isAPI() bool { return e.credName() != "" }

// custom reports whether the entry is a custom provider.
func (e providerEntry) custom() bool { return e.provider != "" }

// canEditURL reports whether `u` applies to the entry.
func (e providerEntry) canEditURL() bool { return e.isAPI() && !e.missing }

// loadEntries builds the provider row: the built-ins, the saved custom
// providers, a "(missing)" entry for each name in extra that is not saved, and
// the "+ custom…" row.
func loadEntries(extra ...providerEntry) []providerEntry {
	var out []providerEntry
	for i, l := range providerLabels {
		out = append(out, providerEntry{label: l, runner: providerToString(providerKind(i))})
	}
	saved := map[string]bool{}
	if list, err := credentials.List(); err == nil {
		for _, e := range list {
			if runner.IsPreset(e.Name) {
				continue
			}
			saved[e.Name] = true
			out = append(out, providerEntry{label: e.Name + " (custom)", runner: "openai", provider: e.Name, baseURL: e.BaseURL})
		}
	}
	var missing []providerEntry
	for _, x := range extra {
		if x.provider != "" && !saved[x.provider] {
			saved[x.provider] = true
			missing = append(missing, providerEntry{label: x.provider + " (missing)", runner: "openai", provider: x.provider, missing: true})
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].provider < missing[j].provider })
	out = append(out, missing...)
	return append(out, providerEntry{label: newCustomLabel, isNew: true})
}

// entry returns the entry of kind p (the first one when out of range).
func (m SetupModel) entry(p providerKind) providerEntry {
	if int(p) < 0 || int(p) >= len(m.entries) {
		return m.entries[0]
	}
	return m.entries[p]
}

// kindOf maps a config runner and provider to an entry, adding a "(missing)"
// entry for a custom provider that is no longer saved.
func (m *SetupModel) kindOf(runnerName, provider string) providerKind {
	if runnerName == "openai" && provider != "" && !runner.IsPreset(provider) {
		for i, e := range m.entries {
			if e.provider == provider {
				return providerKind(i)
			}
		}
		m.reloadEntries(providerEntry{provider: provider})
		for i, e := range m.entries {
			if e.provider == provider {
				return providerKind(i)
			}
		}
	}
	return providerFromString(runnerName)
}

// reloadEntries rebuilds the row from the credential store, keeping the
// selection and keeping (or adding) "(missing)" entries.
func (m *SetupModel) reloadEntries(extra ...providerEntry) {
	selKey, agentKey := m.entry(m.provider).key(), m.entry(m.agentEditProv).key()
	for _, e := range m.entries {
		if e.missing {
			extra = append(extra, e)
		}
	}
	m.entries = loadEntries(extra...)
	for i, e := range m.entries {
		if e.key() == selKey {
			m.provider = providerKind(i)
		}
		if e.key() == agentKey {
			m.agentEditProv = providerKind(i)
		}
	}
	for _, e := range m.entries {
		if e.custom() && !e.missing {
			if _, ok := m.apiModelsLoading[e.key()]; !ok {
				m.apiModelsLoading[e.key()] = false
			}
		}
	}
}

// selectByName selects the saved provider with this credential name in the
// scope that opened the connect overlay.
func (m *SetupModel) selectByName(name string) {
	for i, e := range m.entries {
		if e.credName() == name && !e.missing {
			if m.connectAgent {
				m.agentEditProv = providerKind(i)
			} else if m.provider != providerKind(i) {
				m.provider = providerKind(i)
				m.resetForNewProvider()
			}
			return
		}
	}
}

// resetForNewProvider clears the per-provider state after the provider changed.
func (m *SetupModel) resetForNewProvider() {
	m.modelIdx = 0
	m.pingErr = nil
	m.effort = setupEffortDefault
	m.effortTouched = true
	m.effortIdx = 0
	m.effortOptions = nil
	m.effortFor = ""
	m.effortUnsupported = ""
	m.typed.SetValue("")
}

// revertKind is the entry before the "+ custom…" row.
func (m SetupModel) revertKind() providerKind { return providerKind(max(len(m.entries)-2, 0)) }

// ── models of API entries ────────────────────────────────────────────────────

// typedMode reports whether a model id has to be typed: dsh (no model list) or
// an API entry whose model list is empty and not loading.
func (m SetupModel) typedMode(p providerKind) bool {
	e := m.entry(p)
	if e.runner == "dsh" {
		return true
	}
	if !e.isAPI() {
		return false
	}
	if e.runner == "deepseek" && !e.custom() {
		return len(m.deepseekModels) == 0 && !m.deepseekModelsLoading
	}
	return len(m.apiModels[e.key()]) == 0 && !m.apiModelsLoading[e.key()]
}

// typedModel is the model id typed for the entry in the model input.
func (m SetupModel) typedModel() string { return strings.TrimSpace(m.typed.Value()) }

// effectiveBaseURL is the endpoint the entry uses and whether a config
// override is in effect. override is the config value for a preset.
func effectiveBaseURL(e providerEntry, override string) (string, bool) {
	if e.custom() {
		return e.baseURL, false
	}
	if override != "" {
		return override, true
	}
	if p, ok := runner.PresetByName(e.runner); ok {
		return p.BaseURL, false
	}
	return "", false
}

// baseURLOverride is the preset override of the entry in the global scope.
func (m SetupModel) baseURLOverride(e providerEntry) string {
	if e.custom() {
		return ""
	}
	return m.baseURLs[e.runner]
}

// fetchEntryModels lists the models of an API entry from its effective URL.
func fetchEntryModels(e providerEntry, override string) tea.Cmd {
	key, name := e.key(), e.credName()
	url, _ := effectiveBaseURL(e, override)
	if e.runner == "deepseek" && !e.custom() {
		return func() tea.Msg {
			models, err := runner.ListProviderModels("deepseek", override)
			return deepseekModelsListMsg{models: models, err: err}
		}
	}
	return func() tea.Msg {
		models, err := runner.ListProviderModels(name, url)
		return apiModelsListMsg{key: key, models: models, err: err}
	}
}

// startEntryFetch marks the entry loading and returns its fetch command.
func (m *SetupModel) startEntryFetch(e providerEntry, override string) tea.Cmd {
	if e.missing || !e.isAPI() {
		return nil
	}
	if e.runner == "deepseek" && !e.custom() {
		m.deepseekModelsLoading = true
	} else {
		m.apiModelsLoading[e.key()] = true
	}
	return tea.Batch(m.spinner.Tick, fetchEntryModels(e, override))
}

// ── display lines ────────────────────────────────────────────────────────────

// baseURLLine is the dim line under the key line.
func (m SetupModel) baseURLLine(e providerEntry, override string) string {
	if !e.canEditURL() {
		return ""
	}
	url, over := effectiveBaseURL(e, override)
	if url == "" {
		return "Base URL: not set — press u to set"
	}
	if over {
		return "Base URL: " + url + " (override)"
	}
	return "Base URL: " + url
}

// keyLineFor says where the key of an API entry comes from.
func (m SetupModel) keyLineFor(e providerEntry) string {
	name := e.credName()
	if name == "" {
		return ""
	}
	switch src := runner.KeySource(name, ""); {
	case src == "":
		return "API key: not set — press k to add"
	case strings.HasPrefix(src, "env "):
		return "API key: from " + strings.TrimPrefix(src, "env ")
	default:
		return "API key: saved"
	}
}

// missingHint explains a "(missing)" entry.
func missingHint(e providerEntry) string {
	return fmt.Sprintf("Provider %q is not saved — run: decsr connect custom", e.provider)
}

// ── base URL editor ──────────────────────────────────────────────────────────

// urlEdit is the one-line Base URL input opened with u.
type urlEdit struct {
	in    textinput.Model
	entry providerEntry
	agent bool // edits the per-agent override instead of the global one
	err   string
}

func (m SetupModel) openURLEdit(p providerKind, agent bool) SetupModel {
	e := m.entry(p)
	if !e.canEditURL() {
		return m
	}
	override := m.baseURLOverride(e)
	if agent {
		override = m.agentBaseURL
	}
	url, _ := effectiveBaseURL(e, override)
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 512
	in.Placeholder = "https://api.example.com/v1"
	in.SetValue(url)
	in.CursorEnd()
	in.Focus()
	m.urlEdit = &urlEdit{in: in, entry: e, agent: agent}
	return m
}

func (m SetupModel) handleURLEditKey(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	ue := m.urlEdit
	switch msg.String() {
	case "esc":
		m.urlEdit = nil
		return m, nil
	case "enter":
		return m.applyURLEdit()
	}
	var cmd tea.Cmd
	ue.in, cmd = ue.in.Update(msg)
	ue.err = ""
	return m, cmd
}

// applyURLEdit validates and stores the typed URL, then re-fetches the models.
func (m SetupModel) applyURLEdit() (SetupModel, tea.Cmd) {
	ue := m.urlEdit
	e := ue.entry
	val := strings.TrimRight(strings.TrimSpace(ue.in.Value()), "/")
	if e.custom() {
		if val == "" {
			ue.err = "a custom provider needs a base URL"
			return m, nil
		}
		if err := connectCheckURL(val); err != nil {
			ue.err = err.Error()
			return m, nil
		}
		c, ok, err := credentials.Get(e.provider)
		if err != nil || !ok {
			ue.err = "provider " + e.provider + " is not saved"
			return m, nil
		}
		c.BaseURL = val
		if err := credentials.Set(e.provider, c); err != nil {
			ue.err = oneLine(err.Error())
			return m, nil
		}
		m.urlEdit = nil
		m.reloadEntries()
		m.setSaveStatus("✓ Base URL saved for "+e.provider, true)
		cur := m.entries[m.indexOfKey(e.key())]
		return m, tea.Batch(m.startEntryFetch(cur, ""), m.clearSaveStatusLater())
	}
	if val != "" {
		if err := connectCheckURL(val); err != nil {
			ue.err = err.Error()
			return m, nil
		}
		if p, ok := runner.PresetByName(e.runner); ok && val == strings.TrimRight(p.BaseURL, "/") {
			val = ""
		}
	}
	if ue.agent {
		m.agentBaseURL = val
	} else if val == "" {
		delete(m.baseURLs, e.runner)
	} else {
		m.baseURLs[e.runner] = val
	}
	m.urlEdit = nil
	m.pingErr = nil
	status := "Base URL reset to the preset"
	if val != "" {
		status = "Base URL set"
	}
	m.setSaveStatus("✓ "+status+" · saved with the next Enter or Ctrl+S", true)
	return m, tea.Batch(m.startEntryFetch(e, val), m.clearSaveStatusLater())
}

func (m SetupModel) indexOfKey(key string) int {
	for i, e := range m.entries {
		if e.key() == key {
			return i
		}
	}
	return 0
}

func (m SetupModel) urlEditBox() string {
	ue := m.urlEdit
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	emptyHint := "empty resets to the default"
	if ue.entry.custom() {
		emptyHint = "required"
	}
	body := []string{dim.Render("Base URL (https; http only for localhost) · " + emptyHint), ue.in.View(), "", dim.Render("Enter save · Esc cancel")}
	if ue.err != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(ui.Fail).Render("! "+ue.err))
	}
	return ui.Overlay("Base URL · "+ue.entry.label, body, nil, min(64, m.width-2))
}

// ── global provider detection ────────────────────────────────────────────────

// globalEndpoint finds the provider name and base_url the global config pairs
// with runnerName (the first role, in name order, that sets one).
func globalEndpoint(agents map[string]config.AgentConfig, runnerName string) (provider, baseURL string) {
	roles := make([]string, 0, len(agents))
	for r := range agents {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		ac := agents[r]
		if ac.Runner == runnerName && (ac.Provider != "" || ac.BaseURL != "") {
			return ac.Provider, ac.BaseURL
		}
	}
	return "", ""
}

// WithGlobalAgents tells the screen which provider and base_url the global
// config pairs with the current runner, so a custom provider shows selected.
func (m SetupModel) WithGlobalAgents(agents map[string]config.AgentConfig) SetupModel {
	m.applyGlobalEndpoint(agents, m.entry(m.provider).runner)
	return m
}

func (m *SetupModel) applyGlobalEndpoint(agents map[string]config.AgentConfig, runnerName string) {
	prov, base := globalEndpoint(agents, runnerName)
	if prov != "" && runnerName == "openai" && !runner.IsPreset(prov) {
		m.provider = m.kindOf(runnerName, prov)
		return
	}
	if base != "" {
		m.baseURLs[runnerName] = base
	}
}

// packTabs lays the provider tabs out in rows that fit width, so a tab is
// never split across lines.
func packTabs(tabs []string, width int) []string {
	var rows []string
	row, rowW := "  ", 2
	for i, t := range tabs {
		w := lipgloss.Width(t)
		if i > 0 && rowW+2+w > width {
			rows = append(rows, row)
			row, rowW = "  ", 2
		} else if i > 0 {
			row += "  "
			rowW += 2
		}
		row += t
		rowW += w
	}
	return append(rows, row)
}
