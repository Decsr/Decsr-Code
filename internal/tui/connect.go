package tui

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

// connectStage is the step of the connect overlay.
type connectStage int

const (
	connList       connectStage = iota // pick a provider
	connName                           // custom: provider name
	connURL                            // custom: base URL
	connKey                            // masked key input
	connTesting                        // GET /models in flight
	connSaveAnyway                     // network error: save untested?
	connRoles                          // lead only / all roles / nothing
	connModel                          // pick a model when the preset has no default
)

var connectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// connectOption is one row of the provider list.
type connectOption struct {
	name, label, baseURL, keyEnv, defModel string
	custom                                 bool // a saved custom provider
	isNew                                  bool // the "custom provider…" row
}

// connectProbeFn lists the models at baseURL with key (the key test).
type connectProbeFn func(baseURL, key string) ([]string, error)

// connectApplyFn writes the chosen runner/model into the global config and
// returns the reloaded config.
type connectApplyFn func(runnerName, provider, model string, allRoles bool) (config.Config, error)

type connectProbeMsg struct {
	models []string
	err    error
}

type connectTickMsg struct{}

// connectSavedMsg reports a finished flow. scope is "lead", "all roles" or ""
// when no role was changed; cfg is the reloaded config when one was.
type connectSavedMsg struct {
	name, label, runner, model, scope string
	models                            int
	cfg                               *config.Config
	err                               error
}

type connectClosedMsg struct{}

// connectModel is the key-setup flow shared by the Workspace /connect overlay
// and the setup screen. The key is only ever held masked in a text input and
// in key until it is saved; it is never rendered.
type connectModel struct {
	stage     connectStage
	opts      []connectOption
	idx       int
	in        textinput.Model
	target    connectOption
	key       string
	models    []string
	modelIdx  int
	err       string
	frame     int
	withRoles bool
	probe     connectProbeFn
	apply     connectApplyFn
	allRoles  bool
	cfgOut    *config.Config
}

func defaultProbe(baseURL, key string) ([]string, error) { return runner.ListModels(baseURL, key) }

// newConnectModel builds the flow. start names a provider to jump to ("" shows
// the list; "custom" asks for a custom provider). withRoles adds the "use it
// for" step after saving.
func newConnectModel(start string, withRoles bool, probe connectProbeFn, apply connectApplyFn) connectModel {
	if probe == nil {
		probe = defaultProbe
	}
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 512
	m := connectModel{in: in, withRoles: withRoles, probe: probe, apply: apply}
	m.opts = connectOptions()
	if start != "" {
		for _, o := range m.opts {
			if o.name == start || (start == "custom" && o.isNew) {
				m.idx = indexOfOption(m.opts, o)
				return m.choose()
			}
		}
	}
	return m
}

func indexOfOption(opts []connectOption, o connectOption) int {
	for i, x := range opts {
		if x.name == o.name && x.isNew == o.isNew {
			return i
		}
	}
	return 0
}

// connectOptions lists the presets, the saved custom providers and the
// "custom provider…" row.
func connectOptions() []connectOption {
	var out []connectOption
	for _, p := range runner.Presets() {
		out = append(out, connectOption{name: p.Name, label: p.Label, baseURL: p.BaseURL, keyEnv: p.KeyEnv, defModel: p.DefaultModel})
	}
	if entries, err := credentials.List(); err == nil {
		for _, e := range entries {
			if !runner.IsPreset(e.Name) {
				out = append(out, connectOption{name: e.Name, label: e.Name, baseURL: e.BaseURL, custom: true})
			}
		}
	}
	return append(out, connectOption{name: "custom", label: "custom provider…", isNew: true})
}

// connectStatus is "✓ env", "✓ saved" or "not set" for a provider.
func connectStatus(o connectOption) string {
	if o.isNew {
		return ""
	}
	if o.keyEnv != "" && os.Getenv(o.keyEnv) != "" {
		return "✓ env"
	}
	if _, ok, err := credentials.Get(o.name); err == nil && ok {
		return "✓ saved"
	}
	return "not set"
}

func (m connectModel) setInput(prompt string, password bool) connectModel {
	m.in.SetValue("")
	m.in.Placeholder = prompt
	if password {
		m.in.EchoMode = textinput.EchoPassword
		m.in.EchoCharacter = '•'
	} else {
		m.in.EchoMode = textinput.EchoNormal
	}
	m.in.Focus()
	return m
}

// choose moves on from the provider list to the selected provider.
func (m connectModel) choose() connectModel {
	m.err = ""
	m.target = m.opts[m.idx]
	if m.target.isNew {
		m.stage = connName
		m = m.setInput("provider-name", false)
		return m
	}
	m.stage = connKey
	return m.setInput("paste the API key", true)
}

func (m connectModel) update(msg tea.Msg) (connectModel, tea.Cmd) {
	switch msg := msg.(type) {
	case connectTickMsg:
		if m.stage == connTesting {
			m.frame++
			return m, connectTick()
		}
	case connectProbeMsg:
		return m.onProbe(msg)
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func connectTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return connectTickMsg{} })
}

func (m connectModel) closeCmd() tea.Cmd { return func() tea.Msg { return connectClosedMsg{} } }

func (m connectModel) onKey(msg tea.KeyMsg) (connectModel, tea.Cmd) {
	k := msg.String()
	switch m.stage {
	case connList:
		switch k {
		case "esc":
			return m, m.closeCmd()
		case "up", "k":
			m.idx = max(m.idx-1, 0)
		case "down", "j":
			m.idx = min(m.idx+1, len(m.opts)-1)
		case "enter":
			return m.choose(), nil
		}
	case connName, connURL, connKey:
		return m.onInputKey(msg)
	case connTesting:
		if k == "esc" {
			m.key = ""
			return m, m.closeCmd()
		}
	case connSaveAnyway:
		switch k {
		case "y", "Y":
			return m.save()
		case "n", "N", "esc":
			m.key = ""
			m.stage = connKey
			m = m.setInput("paste the API key", true)
		}
	case connRoles:
		switch k {
		case "1":
			return m.useFor(false)
		case "2":
			return m.useFor(true)
		case "3", "n", "esc", "enter":
			return m.finish("", "")
		}
	case connModel:
		switch k {
		case "esc":
			return m.finish("", "")
		case "up", "k":
			m.modelIdx = max(m.modelIdx-1, 0)
		case "down", "j":
			m.modelIdx = min(m.modelIdx+1, len(m.models)-1)
		case "enter":
			return m.applyRoles(m.models[m.modelIdx], m.allRoles)
		}
	}
	return m, nil
}

func (m connectModel) onInputKey(msg tea.KeyMsg) (connectModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.key = ""
		return m, m.closeCmd()
	case "enter":
	default:
		var cmd tea.Cmd
		m.in, cmd = m.in.Update(msg)
		return m, cmd
	}
	val := strings.TrimSpace(m.in.Value())
	switch m.stage {
	case connName:
		if !connectNameRe.MatchString(val) {
			m.err = "use lowercase letters, digits and -"
			return m, nil
		}
		if runner.IsPreset(val) || val == "custom" {
			m.err = val + " is a built-in provider name"
			return m, nil
		}
		m.target.name, m.target.label, m.target.custom = val, val, true
		m.err, m.stage = "", connURL
		return m.setInput("https://api.example.com/v1", false), nil
	case connURL:
		if err := connectCheckURL(val); err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.target.baseURL = strings.TrimRight(val, "/")
		m.err, m.stage = "", connKey
		return m.setInput("paste the API key", true), nil
	}
	// connKey
	if val == "" {
		m.err = "paste a key first"
		return m, nil
	}
	m.key, m.err, m.stage, m.frame = val, "", connTesting, 0
	m = m.setInput("", true) // clear the input at once
	probe, base, key := m.probe, m.target.baseURL, m.key
	return m, tea.Batch(connectTick(), func() tea.Msg {
		models, err := probe(base, key)
		return connectProbeMsg{models: models, err: err}
	})
}

// connectCheckURL requires https, except http for localhost and 127.0.0.1.
func connectCheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("not a valid URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if h := u.Hostname(); u.Scheme == "http" && (h == "localhost" || h == "127.0.0.1") {
		return nil
	}
	return errors.New("use https (http only for localhost and 127.0.0.1)")
}

func (m connectModel) onProbe(msg connectProbeMsg) (connectModel, tea.Cmd) {
	if m.stage != connTesting {
		return m, nil
	}
	var rejected *runner.KeyRejectedError
	switch {
	case errors.As(msg.err, &rejected):
		m.key, m.stage = "", connKey
		m.err = fmt.Sprintf("key rejected (HTTP %d) — not saved", rejected.Status)
		return m.setInput("paste the API key", true), nil
	case msg.err != nil:
		m.stage = connSaveAnyway
		m.err = oneLine(msg.err.Error())
		return m, nil
	}
	m.models = msg.models
	return m.save()
}

// save stores the key, then moves to the roles step or finishes.
func (m connectModel) save() (connectModel, tea.Cmd) {
	c := credentials.Cred{APIKey: m.key}
	if m.target.custom {
		c.BaseURL = m.target.baseURL
	}
	err := credentials.Set(m.target.name, c)
	m.key = ""
	if err != nil {
		m.stage, m.err = connKey, oneLine(err.Error())
		return m.setInput("paste the API key", true), nil
	}
	m.err = ""
	if m.withRoles && m.apply != nil {
		m.stage = connRoles
		return m, nil
	}
	return m.finish("", "")
}

// runnerFor is the runner and provider names that select this target.
func (m connectModel) runnerFor() (runnerName, provider string) {
	if m.target.custom {
		return "openai", m.target.name
	}
	return m.target.name, ""
}

func (m connectModel) useFor(all bool) (connectModel, tea.Cmd) {
	if m.target.defModel != "" {
		return m.applyRoles(m.target.defModel, all)
	}
	if len(m.models) == 0 {
		// Nothing to choose from: keep the key, change no role.
		return m.finish("", "")
	}
	m.stage, m.modelIdx = connModel, 0
	m.allRoles = all
	return m, nil
}

func (m connectModel) applyRoles(model string, all bool) (connectModel, tea.Cmd) {
	rn, prov := m.runnerFor()
	c, err := m.apply(rn, prov, model, all)
	if err != nil {
		return m.finishErr(err)
	}
	scope := "lead"
	if all {
		scope = "all roles"
	}
	m.cfgOut = &c
	return m.finish(model, scope)
}

func (m connectModel) finishErr(err error) (connectModel, tea.Cmd) {
	out := connectSavedMsg{name: m.target.name, label: m.target.label, models: len(m.models), err: err}
	return m, func() tea.Msg { return out }
}

func (m connectModel) finish(model, scope string) (connectModel, tea.Cmd) {
	rn, _ := m.runnerFor()
	out := connectSavedMsg{name: m.target.name, label: m.target.label, runner: rn, model: model, scope: scope, models: len(m.models), cfg: m.cfgOut}
	return m, func() tea.Msg { return out }
}

// box renders the overlay body for width w.
func (m connectModel) box(w int) string {
	boxW := min(64, w-2)
	dim := lipgloss.NewStyle().Foreground(colMuted)
	ok := lipgloss.NewStyle().Foreground(colOK)
	fail := lipgloss.NewStyle().Foreground(colFail)
	warn := lipgloss.NewStyle().Foreground(colWarn)
	var body []string
	title := "Connect a provider"
	switch m.stage {
	case connList:
		for i, o := range m.opts {
			st := lipgloss.NewStyle().Foreground(colText)
			marker := "  "
			if i == m.idx {
				st, marker = st.Bold(true).Background(colSel), "▸ "
			}
			status := connectStatus(o)
			line := padRight(marker+o.label, 24) + " " + status
			body = append(body, st.Render(line))
		}
		body = append(body, "", dim.Render("↑↓ move · Enter choose · Esc close"))
	case connName:
		title = "Connect · custom provider"
		body = []string{dim.Render("Name (a-z, 0-9, -)"), m.in.View()}
	case connURL:
		title = "Connect · " + m.target.label
		body = []string{dim.Render("Base URL (https; http only for localhost)"), m.in.View()}
	case connKey:
		title = "Connect · " + m.target.label
		body = []string{dim.Render("API key (hidden) · tested with GET " + strings.TrimRight(m.target.baseURL, "/") + "/models"), m.in.View()}
	case connTesting:
		title = "Connect · " + m.target.label
		fr := pixelSpinner.Frames
		body = []string{dim.Render(fr[m.frame%len(fr)] + " testing the key…"), "", dim.Render("Esc cancel")}
	case connSaveAnyway:
		title = "Connect · " + m.target.label
		body = append(wrapStyled("Could not test the key: "+m.err, boxW-4, warn), "", wsKey("y")+" save anyway   "+wsKey("n")+" back")
	case connRoles:
		title = "Connect · " + m.target.label
		body = []string{ok.Render("✓ key saved"), "", "Use " + m.target.name + " for:",
			wsKey("1") + " the lead only", wsKey("2") + " all roles", wsKey("3") + " nothing now"}
	case connModel:
		title = "Connect · " + m.target.label + " · model"
		vis := min(len(m.models), 12)
		start := 0
		if m.modelIdx >= vis {
			start = m.modelIdx - vis + 1
		}
		for i := start; i < start+vis; i++ {
			st := lipgloss.NewStyle().Foreground(colText)
			marker := "  "
			if i == m.modelIdx {
				st, marker = st.Bold(true).Background(colSel), "▸ "
			}
			body = append(body, st.Render(marker+m.models[i]))
		}
		body = append(body, "", dim.Render("↑↓ move · Enter choose · Esc skip"))
	}
	if m.err != "" && m.stage != connSaveAnyway {
		body = append(body, "", fail.Render("! "+m.err))
	}
	return wsBox(title, boxW, body)
}

func wrapStyled(s string, w int, st lipgloss.Style) []string {
	var out []string
	for _, l := range wrapText(s, w) {
		out = append(out, st.Render(l))
	}
	return out
}

// connectSummary is the conversation line for a finished flow.
func connectSummary(msg connectSavedMsg) string {
	n := ""
	if msg.models > 0 {
		n = fmt.Sprintf(", %d models", msg.models)
	}
	return fmt.Sprintf("Connected %s (key saved%s)", msg.name, n)
}
