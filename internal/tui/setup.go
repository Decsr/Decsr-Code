package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/prompts"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

// ── provider ─────────────────────────────────────────────────────────────────

type providerKind int

const (
	providerOpenCode providerKind = iota
	providerClaude
	providerOllama
	providerMLX
	providerCodex
	providerCopilot
	providerLMStudio
	providerDeepSeek
	providerOpenAI
	providerOpenRouter
	providerGroq
	providerDsh
)

var providerLabels = []string{"OpenCode", "Claude", "Ollama", "oMLX", "Codex", "Copilot", "LM Studio", "DeepSeek", "OpenAI", "OpenRouter", "Groq", "DeepSeek Harness"}

// apiPresetName is the runner/preset name of an API provider, "" for the rest.
func apiPresetName(p providerKind) string {
	switch p {
	case providerDeepSeek:
		return "deepseek"
	case providerOpenAI:
		return "openai"
	case providerOpenRouter:
		return "openrouter"
	case providerGroq:
		return "groq"
	}
	return ""
}

func providerFromString(s string) providerKind {
	switch s {
	case "claude":
		return providerClaude
	case "ollama":
		return providerOllama
	case "mlx":
		return providerMLX
	case "codex":
		return providerCodex
	case "copilot":
		return providerCopilot
	case "lmstudio":
		return providerLMStudio
	case "deepseek":
		return providerDeepSeek
	case "openai":
		return providerOpenAI
	case "openrouter":
		return providerOpenRouter
	case "groq":
		return providerGroq
	case "dsh":
		return providerDsh
	default:
		return providerOpenCode
	}
}

func providerToString(p providerKind) string {
	switch p {
	case providerClaude:
		return "claude"
	case providerOllama:
		return "ollama"
	case providerMLX:
		return "mlx"
	case providerCodex:
		return "codex"
	case providerCopilot:
		return "copilot"
	case providerLMStudio:
		return "lmstudio"
	case providerDeepSeek:
		return "deepseek"
	case providerOpenAI:
		return "openai"
	case providerOpenRouter:
		return "openrouter"
	case providerGroq:
		return "groq"
	case providerDsh:
		return "dsh"
	default:
		return "opencode"
	}
}

// ── sections within the setup screen ─────────────────────────────────────────

type setupSection int

const (
	sectionProvider setupSection = iota
	sectionModel
	sectionEffort
	sectionProgLang
	sectionAgentSetup
	sectionProjectName
)

// ── per-agent override ───────────────────────────────────────────────────────

type agentSetupOverride struct {
	runner   string
	provider string // saved custom provider name (runner openai)
	baseURL  string // base_url override of a preset
	model    string
	effort   string
	mode     string // workspace mode: "" = runner default, text or agent
	modeSet  bool   // mode is authoritative; false keeps the saved mode as it is
}

// ── messages ──────────────────────────────────────────────────────────────────

type setupDoneMsg struct {
	provider       string
	providerName   string // saved custom provider name, "" for the rest
	baseURL        string // base_url override of a preset, "" for none
	model          string
	effort         string
	progLanguage   string
	projectName    string
	agentOverrides map[string]agentSetupOverride
}

type setupCancelledMsg struct{}

// setupClosedMsg closes the setup screen as "Done" after incremental saves.
// The parent reloads config from disk; it does not re-save the screen state.
type setupClosedMsg struct{}

// saveStatusClearMsg clears a transient "✓ Saved" message if it is still current.
type saveStatusClearMsg struct{ id int }

// pendingSave is a save waiting for a model reachability check.
type pendingSave struct {
	msg   setupDoneMsg
	next  setupSection
	close bool
}

type modelsListMsg struct {
	models []string
	err    error
}

type ollamaModelsListMsg struct {
	models []string
	err    error
}

type mlxModelsListMsg struct {
	models []string
	err    error
}

type codexModelsListMsg struct {
	models []string
	err    error
}

type claudeModelsListMsg struct {
	models []string
	err    error
}

type copilotModelsListMsg struct {
	models []string
	err    error
}

type lmstudioModelsListMsg struct {
	models []string
	err    error
}

type deepseekModelsListMsg struct {
	models []string
	err    error
}

// apiModelsListMsg carries the /models list of OpenAI, OpenRouter, Groq or a
// saved custom provider (key is the providerEntry key).
type apiModelsListMsg struct {
	key    string
	models []string
	err    error
}

// pingResultMsg carries the result of an async model reachability test.
type pingResultMsg struct {
	err error
}

// effortOptionsMsg carries the model-specific effort levels for a provider/model.
type effortOptionsMsg struct {
	provider string
	model    string
	options  []string
}

// providerAvailabilityMsg carries the result of an async provider
// reachability check started during setup initialisation.
type providerAvailabilityMsg struct {
	kind providerKind
	err  error
}

// ── SetupModel ────────────────────────────────────────────────────────────────

type SetupModel struct {
	provider providerKind
	section  setupSection

	models        []string
	modelIdx      int
	modelsLoading bool
	modelsStatus  string

	claudeModels        []string
	claudeModelsLoading bool
	// claudeCustom is a saved or typed model id that is not in claudeModels.
	claudeCustom string
	// claudeTyping is set while a model id is typed instead of picked.
	claudeTyping  bool
	claudeTypeErr string
	// globalModelEmpty is set while the global model is unset and untouched.
	globalModelEmpty bool

	ollamaModels        []string
	ollamaModelsLoading bool

	mlxModels        []string
	mlxModelsLoading bool

	codexModels        []string
	codexModelsLoading bool

	copilotModels        []string
	copilotModelsLoading bool

	lmstudioModels        []string
	lmstudioModelsLoading bool

	deepseekModels        []string
	deepseekModelsLoading bool
	// dshModels are the DeepSeek API ids offered to dsh; dshCustom is a saved
	// id that may not be among them.
	dshModels        []string
	dshModelsLoading bool
	dshCustom        string

	// Models of the other API providers (OpenAI, OpenRouter, Groq, saved custom
	// providers), listed from /models once a key exists. Keyed by
	// providerEntry.key.
	apiModels        map[string][]string
	apiModelsLoading map[string]bool

	// entries is the provider row; providerKind indexes into it.
	entries []providerEntry
	// baseURLs holds the global base_url override of each preset, by runner.
	baseURLs map[string]string
	// typed is the model id typed when an API provider lists no models.
	typed textinput.Model

	// connect is the masked API-key input opened with k.
	connect *connectModel
	// connectAgent is true when connect was opened from the per-agent runner step.
	connectAgent bool
	// urlEdit is the Base URL input opened with u.
	urlEdit *urlEdit
	// agentBaseURL is the preset base_url override of the agent being edited.
	agentBaseURL string

	// Programming language selection.
	progLanguages []string
	progLangIdx   int

	// Global model effort selection.
	effort            string
	effortIdx         int
	effortOptions     []string
	effortLoading     bool
	effortFor         string // provider/model whose options are loaded
	effortUnsupported string // configured value not offered by this model
	effortTouched     bool   // user changed the effort in this session (or it had to fall back)

	// Project display name (project setup only).
	projectNameInput textinput.Model

	// Per-agent configuration.
	agentRoles     []string
	agentOverrides map[string]agentSetupOverride
	agentCursor    int
	agentEditing   bool
	agentField     int          // focused field of the agent editor: runner, model, effort, mode
	agentEditMode  string       // mode being edited: "", text or agent
	agentEditErr   string       // why the editor refused to save
	agentEditProv  providerKind // provider being edited for current agent
	agentEditIdx   int          // model index within the editing provider

	agentEditEffort            string
	agentEditEffortIdx         int
	agentEditEffortUnsupported string
	agentEffortOptions         []string
	agentEffortFor             string
	agentEffortLoading         bool

	// baseAgents is the pre-setup agent map used to write global settings.
	baseAgents map[string]config.AgentConfig

	// Incremental save feedback.
	saving       bool
	saveStatus   string
	saveStatusOK bool
	saveStatusID int

	globalOnly bool   // when true, hide agent overrides section
	root       string // project root for prompt export

	spinner  spinner.Model
	viewport viewport.Model
	ready    bool
	scrollX  int // horizontal scroll offset

	promptStatus string // status message after prompt export

	// Provider availability — populated asynchronously on Init.
	// Nil means "not yet checked". A non-error key means the provider's
	// backend is reachable; a non-nil error explains why it is unavailable.
	providerAvailability map[providerKind]error

	// Model validation state.
	pingTesting bool         // true while ping is in progress
	pingErr     error        // non-nil after a failed ping
	pendingSave *pendingSave // save waiting for ping result

	width  int
	height int
}

var setupAgentRoles = []string{"pm", "coder", "coder_fixer", "qa", "ux_reviewer", "security", "reviewer"}

func NewSetupModel(currentRunner, currentModel, currentEffort string, agentCfgs map[string]config.AgentConfig) SetupModel {
	sp := spinner.New()
	sp.Spinner = pixelSpinner
	sp.Style = lipgloss.NewStyle().Foreground(homePalette.accent)

	nameInput := textinput.New()
	nameInput.CharLimit = 80
	nameInput.Prompt = ""
	nameInput.Placeholder = "(unnamed)"

	m := SetupModel{
		models:                runner.OpenCodeAvailableModels,
		modelsLoading:         true,
		claudeModels:          runner.ClaudeModels,
		claudeModelsLoading:   true,
		ollamaModels:          runner.OllamaPopularModels,
		ollamaModelsLoading:   true,
		mlxModels:             nil,
		mlxModelsLoading:      true,
		codexModels:           runner.CodexModels,
		codexModelsLoading:    true,
		copilotModels:         nil,
		copilotModelsLoading:  true,
		lmstudioModels:        nil,
		lmstudioModelsLoading: true,
		deepseekModels:        runner.DeepSeekFallbackModels(),
		deepseekModelsLoading: true,
		dshModelsLoading:      true,
		apiModels:             make(map[string][]string),
		apiModelsLoading:      make(map[string]bool),
		baseURLs:              make(map[string]string),
		entries:               loadEntries(),
		providerAvailability:  make(map[providerKind]error),
		progLanguages:         append([]string{progLangAuto}, config.SupportedProgrammingLanguages...),
		projectNameInput:      nameInput,
		agentRoles:            setupAgentRoles,
		agentOverrides:        make(map[string]agentSetupOverride),
		baseAgents:            cloneAgentConfigs(agentCfgs),
		effort:                normalizeSetupEffort(currentEffort),
		spinner:               sp,
		width:                 80,
		height:                24,
	}

	typed := textinput.New()
	typed.Prompt = ""
	typed.CharLimit = 200
	typed.Placeholder = "model id"
	typed.Focus()
	m.typed = typed
	for _, e := range m.entries {
		if e.isAPI() && !e.missing && e.runner != "deepseek" {
			m.apiModelsLoading[e.key()] = true
		}
	}

	m.globalModelEmpty = currentModel == ""
	if currentRunner == "claude" {
		m.noteClaudeCustom(currentModel)
	}
	if currentRunner == "dsh" {
		m.noteDshCustom(currentModel)
	}
	m.provider = providerFromString(currentRunner)
	m.applyGlobalEndpoint(agentCfgs, currentRunner)
	if m.typedMode(m.provider) || m.entry(m.provider).isAPI() || m.provider == providerDsh {
		m.typed.SetValue(currentModel)
	}

	if currentModel == "" && m.provider == providerClaude {
		m.modelIdx = findModelIndex(m.claudeRows(), runner.ClaudeDefaultModel)
	}
	if currentModel != "" {
		for i, mdl := range m.activeModels() {
			if mdl == currentModel {
				m.modelIdx = i
				break
			}
		}
	}

	// Pre-populate per-agent overrides from current config.
	for _, role := range setupAgentRoles {
		if ac, ok := agentCfgs[role]; ok {
			r := ac.Runner
			mdl := ac.Model
			eff := ac.Effort
			if r != currentRunner || mdl != currentModel || eff != "" {
				m.kindOf(r, ac.Provider)
				m.agentOverrides[role] = agentSetupOverride{runner: r, provider: ac.Provider, baseURL: ac.BaseURL, model: mdl, effort: eff}
			}
		}
	}

	m.section = sectionProvider
	return m
}

// NewSetupModelWithOverrides creates a setup model pre-populated with explicit
// project-level overrides (from project.yaml), instead of detecting them from
// merged config.
func NewSetupModelWithOverrides(currentRunner, currentModel, currentEffort, currentProgLang, currentProjectName string, projectAgents map[string]config.AgentConfig) SetupModel {
	m := NewSetupModel(currentRunner, currentModel, currentEffort, nil)
	m.projectNameInput.SetValue(currentProjectName)
	m.projectNameInput.CursorEnd()
	// Pre-select programming language.
	if currentProgLang != "" {
		for i, pl := range m.progLanguages {
			if pl == currentProgLang {
				m.progLangIdx = i
				break
			}
		}
	}
	// Custom workspace roles are listed after the built-ins.
	var custom []string
	for role := range projectAgents {
		// "pr" is the pull-request agent, not a workspace role.
		if !config.IsBuiltinRole(role) && role != "pr" {
			custom = append(custom, role)
		}
	}
	sort.Strings(custom)
	m.agentRoles = append(append([]string(nil), setupAgentRoles...), custom...)
	for role, ac := range projectAgents {
		ov := agentSetupOverride{}
		if ac.Runner != "" {
			ov.runner = ac.Runner
		}
		if ac.Model != "" {
			ov.model = ac.Model
		}
		if ac.Effort != "" {
			ov.effort = ac.Effort
		}
		if ov.runner != "" {
			ov.provider, ov.baseURL = ac.Provider, ac.BaseURL
			m.kindOf(ov.runner, ov.provider)
		}
		ov.mode, ov.modeSet = ac.Mode, true
		if ov.runner != "" || ov.model != "" || ov.effort != "" || ov.mode != "" {
			m.agentOverrides[role] = ov
		}
	}
	// In project setup (non-global), start on the project name section.
	m.section = sectionProjectName
	m.projectNameInput.Focus()
	return m
}

func (m SetupModel) activeModels() []string {
	return m.modelsForProvider(m.provider)
}

// providerUnavailable reports whether availability checks have completed for
// the given provider and concluded the backend is unreachable. A provider
// that has not yet been probed is treated as available so the UI does not
// flicker during startup.
func (m SetupModel) providerUnavailable(p providerKind) bool {
	if m.providerAvailability == nil {
		return false
	}
	err, checked := m.providerAvailability[p]
	return checked && err != nil
}

// nextAvailableProvider returns the next reachable provider in the given
// direction (+1 or -1), or the starting kind when none is found. Used to
// skip unavailable providers when cycling with left/right.
func (m SetupModel) nextAvailableProvider(start providerKind, dir int) providerKind {
	n := providerKind(len(m.entries))
	for step := 1; step < int(n); step++ {
		candidate := providerKind((int(start) + dir*step + int(n)) % int(n))
		if !m.providerUnavailable(candidate) {
			return candidate
		}
	}
	return start
}

func (m SetupModel) modelsForProvider(p providerKind) []string {
	switch p {
	case providerClaude:
		return m.claudeRows()
	case providerOllama:
		return m.ollamaModels
	case providerMLX:
		return m.mlxModels
	case providerCodex:
		return m.codexModels
	case providerCopilot:
		return m.copilotModels
	case providerLMStudio:
		return m.lmstudioModels
	case providerDeepSeek:
		return m.deepseekModels
	case providerDsh:
		return m.dshRows()
	case providerOpenAI, providerOpenRouter, providerGroq:
		return m.apiModels[m.entry(p).key()]
	default:
		if int(p) >= len(providerLabels) {
			return m.apiModels[m.entry(p).key()]
		}
		return m.models
	}
}

// agentModel is the model chosen in the per-agent model step: the picked list
// row, or the typed id when the provider lists none.
func (m SetupModel) agentModel() string {
	if m.typedMode(m.agentEditProv) {
		return m.typedModel()
	}
	if models := m.modelsForProvider(m.agentEditProv); m.agentEditIdx < len(models) && models[m.agentEditIdx] != claudeTypeRow {
		if m.agentEditProv == providerDsh {
			return dshRowModel(models[m.agentEditIdx])
		}
		return models[m.agentEditIdx]
	}
	return ""
}

// ── effort choices ───────────────────────────────────────────────────────────

const setupEffortDefault = "default"

// normalizeSetupEffort keeps the explicit "default" sentinel and normalizes
// provider values to lower case.
func normalizeSetupEffort(effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return setupEffortDefault
	}
	return effort
}

// effortChoices returns the provider-default option followed by the model's
// supported levels.
func effortChoices(options []string) []string {
	out := []string{setupEffortDefault}
	for _, opt := range options {
		if opt != setupEffortDefault {
			out = append(out, opt)
		}
	}
	return out
}

// effortLabels renders the default sentinel as a human-readable option;
// claude names the effort its default stands for.
func effortLabels(choices []string, claude bool) []string {
	defaultLabel := "(provider default)"
	if claude {
		defaultLabel = "default (" + runner.ClaudeDefaultEffort + ")"
	}
	out := make([]string, len(choices))
	for i, choice := range choices {
		if choice == setupEffortDefault {
			out[i] = defaultLabel
			continue
		}
		out[i] = choice
	}
	return out
}

// effortChoiceIndex returns the index of value in choices. found is false when
// the configured value is not offered by the model.
func effortChoiceIndex(choices []string, value string) (idx int, found bool) {
	value = normalizeSetupEffort(value)
	for i, choice := range choices {
		if choice == value {
			return i, true
		}
	}
	return 0, false
}

func effortKey(provider, model string) string {
	return provider + "/" + model
}

// startEffortLoad fetches effort options for the global model selection.
func (m *SetupModel) startEffortLoad() tea.Cmd {
	provider, model := m.selectedProviderModel()
	if model == "" && provider != "dsh" {
		return nil
	}
	key := effortKey(provider, model)
	if m.effortFor == key && !m.effortLoading {
		return nil
	}
	m.effortLoading = true
	m.effortFor = key
	m.effortOptions = nil
	m.effortUnsupported = ""
	return fetchEffortOptions(provider, model)
}

// startAgentEffortLoad fetches effort options for the model currently being
// edited in the per-agent panel.
func (m *SetupModel) startAgentEffortLoad() tea.Cmd {
	provider := m.entry(m.agentEditProv).runner
	model := m.agentModel()
	if model == "" && provider != "dsh" {
		return nil
	}
	key := effortKey(provider, model)
	if m.agentEffortFor == key && !m.agentEffortLoading {
		return nil
	}
	m.agentEffortLoading = true
	m.agentEffortFor = key
	m.agentEffortOptions = nil
	m.agentEditEffortUnsupported = ""
	return fetchEffortOptions(provider, model)
}

func fetchEffortOptions(provider, model string) tea.Cmd {
	return func() tea.Msg {
		return effortOptionsMsg{
			provider: provider,
			model:    model,
			options:  runner.EffortOptions(provider, model),
		}
	}
}

// selectedEffort returns the global effort value to persist.
func (m SetupModel) selectedEffort() string {
	if m.effortLoading || m.effortFor == "" || !m.effortTouched {
		return normalizeSetupEffort(m.effort)
	}
	choices := effortChoices(m.effortOptions)
	if m.effortIdx >= 0 && m.effortIdx < len(choices) {
		return choices[m.effortIdx]
	}
	return normalizeSetupEffort(m.effort)
}

func (m SetupModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchOpenCodeModels(), fetchOllamaModels(), fetchMLXModels(), fetchCodexModels(), fetchClaudeModels(), fetchCopilotModels(), fetchLMStudioModels(), fetchDshModels(), m.fetchAPIModelsCmd(), checkProviderAvailability())
}

// fetchAPIModelsCmd lists the models of every API entry that has a name.
func (m SetupModel) fetchAPIModelsCmd() tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range m.entries {
		if e.isAPI() && !e.missing {
			cmds = append(cmds, fetchEntryModels(e, m.baseURLOverride(e)))
		}
	}
	return tea.Batch(cmds...)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m SetupModel) Update(msg tea.Msg) (SetupModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scrollX = 0
		m.syncViewport()

	case spinner.TickMsg:
		if m.isLoadingModels() || m.pingTesting || m.effortLoading || m.agentEffortLoading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case effortOptionsMsg:
		key := effortKey(msg.provider, msg.model)
		if key == m.effortFor {
			m.effortLoading = false
			m.effortOptions = msg.options
			choices := effortChoices(m.effortOptions)
			idx, found := effortChoiceIndex(choices, m.effort)
			m.effortIdx = idx
			m.effortUnsupported = ""
			// A model that exposes no levels cannot exclude the current
			// value, so keep it; only fall back when the list rules it out.
			if !found && len(m.effortOptions) > 0 {
				m.effortUnsupported = m.effort
				m.effort = setupEffortDefault
				m.effortTouched = true
			} else if !found {
				m.effortIdx = 0
			}
		}
		if key == m.agentEffortFor {
			m.agentEffortLoading = false
			m.agentEffortOptions = msg.options
			choices := effortChoices(m.agentEffortOptions)
			idx, found := effortChoiceIndex(choices, m.agentEditEffort)
			m.agentEditEffortIdx = idx
			m.agentEditEffortUnsupported = ""
			if !found {
				m.agentEditEffortUnsupported = m.agentEditEffort
				m.agentEditEffort = setupEffortDefault
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
			m.ensureSectionVisible()
		}

	case pingResultMsg:
		m.pingTesting = false
		m.saving = false
		if msg.err != nil {
			m.pingErr = msg.err
			m.pendingSave = nil
			m.setSaveStatus("✗ "+msg.err.Error(), false)
			if m.ready {
				m.viewport.SetContent(m.renderContent())
			}
			return m, nil
		}
		if m.pendingSave != nil {
			pending := *m.pendingSave
			m.pendingSave = nil
			if pending.close {
				if m.globalOnly {
					// Save here so untouched per-agent effort is preserved;
					// the parent only reloads config from disk.
					if err := m.savePayload(pending.msg); err != nil {
						m.setSaveStatus("✗ "+err.Error(), false)
						if m.ready {
							m.viewport.SetContent(m.renderContent())
						}
						return m, nil
					}
					return m, func() tea.Msg { return setupClosedMsg{} }
				}
				return m, func() tea.Msg { return pending.msg }
			}
			if err := m.savePayload(pending.msg); err != nil {
				m.setSaveStatus("✗ "+err.Error(), false)
				if m.ready {
					m.viewport.SetContent(m.renderContent())
				}
				return m, nil
			}
			m.setSaveStatus("✓ Saved", true)
			m.section = pending.next
			m.ensureSectionVisible()
			return m, m.clearSaveStatusLater()
		}

	case saveStatusClearMsg:
		if msg.id == m.saveStatusID {
			m.saveStatus = ""
			m.saveStatusOK = true
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case modelsListMsg:
		m.modelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerOpenCode && m.modelIdx < len(m.models) {
				prevModel = m.models[m.modelIdx]
			}
			m.models = msg.models
			m.modelsStatus = ""
			if m.provider == providerOpenCode {
				m.modelIdx = findModelIndex(m.models, prevModel)
			}
		} else if msg.err != nil {
			m.modelsStatus = fmt.Sprintf("could not fetch models: %v", msg.err)
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case ollamaModelsListMsg:
		m.ollamaModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerOllama && m.modelIdx < len(m.ollamaModels) {
				prevModel = m.ollamaModels[m.modelIdx]
			}
			m.ollamaModels = msg.models
			if m.provider == providerOllama {
				m.modelIdx = findModelIndex(m.ollamaModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case mlxModelsListMsg:
		m.mlxModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerMLX && m.modelIdx < len(m.mlxModels) {
				prevModel = m.mlxModels[m.modelIdx]
			}
			m.mlxModels = msg.models
			if m.provider == providerMLX {
				m.modelIdx = findModelIndex(m.mlxModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case codexModelsListMsg:
		m.codexModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerCodex && m.modelIdx < len(m.codexModels) {
				prevModel = m.codexModels[m.modelIdx]
			}
			m.codexModels = msg.models
			if m.provider == providerCodex {
				m.modelIdx = findModelIndex(m.codexModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case claudeModelsListMsg:
		m.claudeModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if rows := m.claudeRows(); m.provider == providerClaude && m.modelIdx < len(rows) {
				prevModel = rows[m.modelIdx]
			}
			m.claudeModels = msg.models
			if m.provider == providerClaude {
				m.modelIdx = findModelIndex(m.claudeRows(), prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case copilotModelsListMsg:
		m.copilotModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerCopilot && m.modelIdx < len(m.copilotModels) {
				prevModel = m.copilotModels[m.modelIdx]
			}
			m.copilotModels = msg.models
			if m.provider == providerCopilot {
				m.modelIdx = findModelIndex(m.copilotModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case lmstudioModelsListMsg:
		m.lmstudioModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerLMStudio && m.modelIdx < len(m.lmstudioModels) {
				prevModel = m.lmstudioModels[m.modelIdx]
			}
			m.lmstudioModels = msg.models
			if m.provider == providerLMStudio {
				m.modelIdx = findModelIndex(m.lmstudioModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case dshModelsListMsg:
		m.onDshModels(msg)
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case deepseekModelsListMsg:
		m.deepseekModelsLoading = false
		// The runner returns its static list alongside any error, so the list
		// is always usable.
		if len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerDeepSeek && m.modelIdx < len(m.deepseekModels) {
				prevModel = m.deepseekModels[m.modelIdx]
			}
			m.deepseekModels = msg.models
			if m.provider == providerDeepSeek {
				m.modelIdx = findModelIndex(m.deepseekModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case apiModelsListMsg:
		m.apiModelsLoading[msg.key] = false
		if len(msg.models) > 0 {
			prevModel := ""
			cur := m.apiModels[msg.key]
			sel := m.entry(m.provider).key() == msg.key
			switch {
			case sel && m.modelIdx < len(cur):
				prevModel = cur[m.modelIdx]
			case sel && len(cur) == 0:
				prevModel = m.typedModel()
			}
			m.apiModels[msg.key] = msg.models
			if sel {
				m.modelIdx = findModelIndex(msg.models, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case connectProbeMsg, connectTickMsg:
		if m.connect != nil {
			c, cmd := m.connect.update(msg)
			m.connect = &c
			cmds = append(cmds, cmd)
		}

	case connectClosedMsg:
		m.connect = nil
		m.revertNewRow()

	case connectSavedMsg:
		m.connect = nil
		if msg.err != nil {
			m.setSaveStatus("✗ "+msg.err.Error(), false)
		} else {
			m.setSaveStatus(connectSummary(msg), true)
			cmds = append(cmds, m.clearSaveStatusLater())
		}
		m.pingErr = nil
		m.reloadEntries()
		if msg.err == nil && !runner.IsPreset(msg.name) {
			m.selectByName(msg.name)
		}
		m.revertNewRow()
		for _, e := range m.entries {
			if e.credName() == msg.name {
				cmds = append(cmds, m.startEntryFetch(e, m.baseURLOverride(e)))
			}
		}
		cmds = append(cmds, m.spinner.Tick)
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case providerAvailabilityMsg:
		if m.providerAvailability == nil {
			m.providerAvailability = make(map[providerKind]error)
		}
		m.providerAvailability[msg.kind] = msg.err
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}
	case tea.KeyMsg:
		var cmd tea.Cmd
		prevSection := m.section
		prevEditing := m.agentEditing
		m, cmd = m.handleKey(msg)
		cmds = append(cmds, cmd)
		m.syncProjectNameFocus()

		// Update viewport content after key handling.
		if m.ready {
			m.viewport.SetContent(m.renderContent())

			// If section, editing state, or edit step changed, the handler
			// already called ensureSectionVisible which set the correct
			// YOffset. Re-apply it since SetContent may have adjusted it.
			if m.section != prevSection || m.agentEditing != prevEditing {
				m.ensureSectionVisible()
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m SetupModel) handleKey(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	if m.connect != nil {
		c, cmd := m.connect.update(msg)
		m.connect = &c
		return m, cmd
	}
	if m.urlEdit != nil {
		return m.handleURLEditKey(msg)
	}
	if msg.String() == "ctrl+s" {
		if m.globalOnly && m.entry(m.provider).isNew {
			return m.openConnect(false), nil
		}
		return m.startValidation()
	}
	if m.saving {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		if m.claudeTyping {
			m.claudeTyping = false
			m.claudeTypeErr = ""
			return m, nil
		}
		if m.agentEditing {
			m.agentEditing = false
			m.agentEditErr = ""
			return m, nil
		}
		return m, func() tea.Msg { return setupClosedMsg{} }
	case "[", "]":
		// While a text field has the focus the bracket is typed, not a scroll.
		if m.typingText() {
			break
		}
		if msg.String() == "]" {
			m.scrollX += horizontalScrollStep
		} else {
			m.scrollX = max(m.scrollX-horizontalScrollStep, 0)
		}
		return m, nil
	}

	switch m.section {
	case sectionProvider:
		return m.handleProviderKeys(msg)
	case sectionModel:
		return m.handleModelKeys(msg)
	case sectionEffort:
		return m.handleEffortKeys(msg)
	case sectionProgLang:
		return m.handleProgLangKeys(msg)
	case sectionAgentSetup:
		return m.handleAgentSetupKeys(msg)
	case sectionProjectName:
		return m.handleProjectNameKeys(msg)
	}
	return m, nil
}

// syncProjectNameFocus keeps the project-name text input focused only while its
// section is active, so the cursor blinks there and nowhere else.
func (m *SetupModel) syncProjectNameFocus() {
	if m.section == sectionProjectName {
		if !m.projectNameInput.Focused() {
			m.projectNameInput.Focus()
		}
		return
	}
	if m.projectNameInput.Focused() {
		m.projectNameInput.Blur()
	}
}

// handleProjectNameKeys handles navigation and text editing for the project name
// field. Navigation keys move between sections; all other keys edit the text.
func (m SetupModel) handleProjectNameKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	switch msg.String() {
	case "tab", "down":
		m.section = sectionAgentSetup
		m.agentCursor = 0
		m.ensureSectionVisible()
		return m, nil
	case "shift+tab", "up":
		m.section = sectionProgLang
		m.ensureSectionVisible()
		return m, nil
	case "enter":
		return m.commitProjectSelection(sectionAgentSetup)
	}
	var cmd tea.Cmd
	m.projectNameInput, cmd = m.projectNameInput.Update(msg)
	return m, cmd
}

func (m SetupModel) handleProviderKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	prev := m.provider
	var cmd tea.Cmd
	switch msg.String() {
	case "left", "h":
		m.provider = m.nextAvailableProvider(m.provider, -1)
	case "right", "l":
		m.provider = m.nextAvailableProvider(m.provider, +1)
	case "k":
		return m.openConnect(false), nil
	case "u":
		return m.openURLEdit(m.provider, false), nil
	case "tab", "down", "enter":
		if m.entry(m.provider).isNew {
			return m.openConnect(false), nil
		}
		m.section = sectionModel
		m.ensureSectionVisible()
	case "shift+tab":
		if !m.globalOnly {
			m.section = sectionProgLang
			m.progLangIdx = len(m.progLanguages) - 1
		} else {
			m.section = sectionEffort
			cmd = m.startEffortLoad()
		}
		m.ensureSectionVisible()
	}
	if m.provider != prev {
		m.resetForNewProvider()
	}
	return m, cmd
}

// openConnect opens the masked key input for the selected API provider, or the
// custom-provider flow on the "+ custom…" row. agent says it was opened from
// the per-agent runner step.
func (m SetupModel) openConnect(agent bool) SetupModel {
	p := m.provider
	if agent {
		p = m.agentEditProv
	}
	e := m.entry(p)
	name := e.credName()
	switch {
	case e.isNew:
		name = "custom"
	case name == "" || e.missing:
		return m
	}
	c := newConnectModel(name, false, nil, nil)
	m.connect = &c
	m.connectAgent = agent
	return m
}

// revertNewRow leaves the "+ custom…" row after the overlay closed without a
// provider being added.
func (m *SetupModel) revertNewRow() {
	if m.entry(m.provider).isNew {
		m.provider = m.revertKind()
	}
	if m.entry(m.agentEditProv).isNew {
		m.agentEditProv = m.revertKind()
	}
}

// apiKeyLine says where the selected API provider's key comes from.
func (m SetupModel) apiKeyLine() string { return m.keyLineFor(m.entry(m.provider)) }

func (m SetupModel) handleModelKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {

	models := m.activeModels()
	if m.claudeTyping && m.provider == providerClaude {
		accepted, cmd := m.claudeTypedKey(msg, &m.modelIdx)
		if !accepted {
			return m, cmd
		}
		m.pingErr = nil
		m.resetEffortForNewModel()
		m.section = sectionEffort
		cmd = m.startEffortLoad()
		m.ensureSectionVisible()
		return m, cmd
	}
	if m.typedMode(m.provider) {
		return m.handleTypedModelKeys(msg)
	}
	switch msg.String() {
	case "K":
		return m.openConnect(false), nil
	case "u":
		return m.openURLEdit(m.provider, false), nil
	case "up", "k":
		if m.modelIdx > 0 {
			m.modelIdx--
			m.pingErr = nil
			m.resetEffortForNewModel()
		} else {
			m.section = sectionProvider
			m.ensureSectionVisible()
		}
	case "down", "j":
		if m.modelIdx < len(models)-1 {
			m.modelIdx++
			m.pingErr = nil
			m.resetEffortForNewModel()
		}
	case "tab":
		m.section = sectionEffort
		cmd := m.startEffortLoad()
		m.ensureSectionVisible()
		return m, cmd
	case "shift+tab":
		m.section = sectionProvider
		m.ensureSectionVisible()
	case "enter":
		if m.provider == providerClaude && m.modelIdx < len(models) && models[m.modelIdx] == claudeTypeRow {
			m.startClaudeTyping()
			return m, nil
		}
		// Move to the effort picker instead of saving immediately, so choosing
		// a model always presents its effort options.
		m.section = sectionEffort
		cmd := m.startEffortLoad()
		m.ensureSectionVisible()
		return m, cmd
	}
	return m, nil
}

// handleTypedModelKeys edits the model id typed when the provider lists none.
// Letters go to the input, so u and K are not shortcuts here; use the provider
// row for the base URL and the key.
func (m SetupModel) handleTypedModelKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	switch msg.String() {
	case "up", "shift+tab":
		m.section = sectionProvider
		m.ensureSectionVisible()
		return m, nil
	case "tab", "enter", "down":
		m.pingErr = nil
		m.resetEffortForNewModel()
		m.section = sectionEffort
		cmd := m.startEffortLoad()
		m.ensureSectionVisible()
		return m, cmd
	}
	var cmd tea.Cmd
	m.typed, cmd = m.typed.Update(msg)
	m.pingErr = nil
	m.resetEffortForNewModel()
	return m, cmd
}

// resetEffortForNewModel drops the loaded effort levels of the previous model.
// The current effort is kept; it is validated against the new model's levels
// once they load.
func (m *SetupModel) resetEffortForNewModel() {
	m.globalModelEmpty = false
	m.effortIdx = 0
	m.effortOptions = nil
	m.effortFor = ""
	m.effortUnsupported = ""
}

func (m SetupModel) handleEffortKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	var cmd tea.Cmd
	provider, model := m.selectedProviderModel()
	if model != "" && m.effortFor != effortKey(provider, model) {
		cmd = m.startEffortLoad()
	}
	choices := effortChoices(m.effortOptions)
	switch msg.String() {
	case "up", "k":
		if m.effortLoading {
			return m, cmd
		}
		if m.effortIdx > 0 {
			m.effortIdx--
			m.effortTouched = true
			m.effortUnsupported = ""
		}
	case "down", "j":
		if m.effortLoading {
			return m, cmd
		}
		if m.effortIdx < len(choices)-1 {
			m.effortIdx++
			m.effortTouched = true
			m.effortUnsupported = ""
		}
	case "enter":
		if m.effortLoading {
			return m, cmd
		}
		if m.effortIdx < 0 || m.effortIdx >= len(choices) {
			m.effortIdx = 0
		}
		if m.effortTouched {
			m.effort = choices[m.effortIdx]
		}
		if m.globalOnly {
			return m.commitGlobalSelection(sectionProvider)
		}
		return m.commitProjectSelection(sectionProjectName)
	case "tab":
		if !m.globalOnly {
			m.section = sectionProjectName
		} else {
			m.section = sectionProvider
		}
		m.ensureSectionVisible()
	case "shift+tab":
		m.section = sectionModel
		m.ensureSectionVisible()
	}
	return m, cmd
}

func (m SetupModel) handleProgLangKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.progLangIdx > 0 {
			m.progLangIdx--
		} else {
			m.section = sectionAgentSetup
			m.agentCursor = len(m.agentRoles) - 1
			m.ensureSectionVisible()
		}
	case "down", "j":
		if m.progLangIdx < len(m.progLanguages)-1 {
			m.progLangIdx++
		}
	case "tab":
		m.section = sectionProjectName
		m.ensureSectionVisible()
	case "shift+tab":
		m.section = sectionAgentSetup
		m.agentCursor = len(m.agentRoles) - 1
		m.ensureSectionVisible()
	case "enter":
		return m.commitProjectSelection(sectionProjectName)
	}
	return m, nil
}

func (m SetupModel) handleAgentSetupKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	if m.agentEditing {
		return m.handleAgentEditKeys(msg)
	}

	switch msg.String() {
	case "up", "k":
		if m.agentCursor > 0 {
			m.agentCursor--
		} else {
			m.section = sectionProjectName
			m.ensureSectionVisible()
		}
	case "down", "j":
		if m.agentCursor < len(m.agentRoles)-1 {
			m.agentCursor++
		}
	case "enter":
		return m.openAgentEditor()
	case "backspace", "delete":
		m.resetAgentOverride(m.agentRoles[m.agentCursor])
		return m.commitProjectSelection(sectionAgentSetup)
	case "p":
		m.exportAgentPrompts()
	case "d":
		m.deleteAgentPrompts()
	case "tab":
		m.section = sectionProgLang
		m.progLangIdx = 0
		m.ensureSectionVisible()
	case "shift+tab":
		m.section = sectionProjectName
		m.ensureSectionVisible()
	}
	return m, nil
}

// resetAgentOverride drops the runner, model and effort override of a role. An
// explicit mode is kept; it is changed in the editor.
func (m *SetupModel) resetAgentOverride(role string) {
	if ov, ok := m.agentOverrides[role]; ok && ov.mode != "" {
		m.agentOverrides[role] = agentSetupOverride{mode: ov.mode, modeSet: true}
		return
	}
	delete(m.agentOverrides, role)
}

// Fields of the agent editor card.
const (
	agentFieldRunner = iota
	agentFieldModel
	agentFieldEffort
	agentFieldMode
	agentFieldCount
)

// agentModes are the values the Mode field cycles through: "" is the runner's
// default.
var agentModes = []string{"", config.ModeText, config.ModeAgent}

// openAgentEditor opens the editor card for the role under the cursor.
func (m SetupModel) openAgentEditor() (SetupModel, tea.Cmd) {
	role := m.agentRoles[m.agentCursor]
	m.agentEditing = true
	m.agentField = agentFieldRunner
	m.agentEditErr = ""
	m.agentEditMode = ""
	m.agentEditEffort = setupEffortDefault
	m.agentEditEffortIdx = 0
	m.agentEditEffortUnsupported = ""
	m.agentEffortFor = ""
	m.agentEffortOptions = nil
	m.agentEffortLoading = false
	m.claudeTyping = false
	m.claudeTypeErr = ""

	ov, has := m.agentOverrides[role]
	own := has && ov.runner != ""
	if own {
		m.agentEditProv = m.kindOf(ov.runner, ov.provider)
		m.agentBaseURL = ov.baseURL
	} else {
		m.agentEditProv = m.provider
		m.agentBaseURL = m.baseURLOverride(m.entry(m.provider))
	}
	if has {
		if ov.effort != "" {
			m.agentEditEffort = normalizeSetupEffort(ov.effort)
		}
		m.agentEditMode = ov.mode
	}

	// Pre-select the role's model, else the global one when the runner is the same.
	m.agentEditIdx = 0
	m.typed.SetValue("")
	if m.agentEditProv == providerClaude {
		m.agentEditIdx = findModelIndex(m.claudeRows(), runner.ClaudeDefaultModel)
	}
	switch {
	case has && ov.model != "" && (!own || m.agentEditProv == m.kindOf(ov.runner, ov.provider)):
		if m.agentEditProv == providerClaude {
			m.noteClaudeCustom(ov.model)
		}
		if m.agentEditProv == providerDsh {
			m.noteDshCustom(ov.model)
		}
		for i, mdl := range m.modelsForProvider(m.agentEditProv) {
			if mdl == ov.model {
				m.agentEditIdx = i
				break
			}
		}
		m.typed.SetValue(ov.model)
	case !own && m.agentEditProv == m.provider:
		if !m.globalModelEmpty {
			m.agentEditIdx = m.modelIdx
		}
	}
	cmd := m.startAgentEffortLoad()
	m.ensureSectionVisible()
	return m, cmd
}

// typingText reports whether a text input has the focus.
func (m SetupModel) typingText() bool {
	switch {
	case m.agentEditing:
		return m.agentEditTyping()
	case m.section == sectionProjectName:
		return true
	case m.section == sectionModel:
		return m.typedMode(m.provider) || (m.claudeTyping && m.provider == providerClaude)
	}
	return false
}

// agentEditTyping reports whether the model field is a text input.
func (m SetupModel) agentEditTyping() bool {
	if m.agentField != agentFieldModel {
		return false
	}
	return m.typedMode(m.agentEditProv) || (m.claudeTyping && m.agentEditProv == providerClaude)
}

// agentEditModelReset points the model at the default row of the runner being
// edited.
func (m *SetupModel) agentEditModelReset() {
	m.agentEditIdx = 0
	m.typed.SetValue("")
	m.claudeTyping = false
	m.claudeTypeErr = ""
	if m.agentEditProv == providerClaude {
		m.agentEditIdx = findModelIndex(m.claudeRows(), runner.ClaudeDefaultModel)
	}
}

// agentEffortValue is the effort the editor would save.
func (m SetupModel) agentEffortValue() string {
	choices := effortChoices(m.agentEffortOptions)
	if !m.agentEffortLoading && m.agentEffortFor != "" && m.agentEditEffortIdx >= 0 && m.agentEditEffortIdx < len(choices) {
		return choices[m.agentEditEffortIdx]
	}
	return normalizeSetupEffort(m.agentEditEffort)
}

// cycleIdx moves i by dir inside n rows and wraps around.
func cycleIdx(i, dir, n int) int {
	if n <= 0 {
		return 0
	}
	return ((i+dir)%n + n) % n
}

func (m SetupModel) handleAgentEditKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	if m.claudeTyping && m.agentField == agentFieldModel && m.agentEditProv == providerClaude {
		idx := m.agentEditIdx
		accepted, cmd := m.claudeTypedKey(msg, &idx)
		m.agentEditIdx = idx
		if !accepted {
			return m, cmd
		}
		m.agentEditEffortUnsupported = ""
		return m, m.startAgentEffortLoad()
	}
	key := msg.String()
	switch key {
	case "up":
		m.agentField = max(m.agentField-1, 0)
		m.agentEditErr = ""
		return m, m.startAgentEffortLoad()
	case "down":
		m.agentField = min(m.agentField+1, agentFieldCount-1)
		m.agentEditErr = ""
		return m, m.startAgentEffortLoad()
	case "enter":
		return m.saveAgentEditor()
	}
	if m.agentEditTyping() {
		var cmd tea.Cmd
		m.typed, cmd = m.typed.Update(msg)
		m.agentEditErr = ""
		return m, tea.Batch(cmd, m.startAgentEffortLoad())
	}
	switch key {
	case "backspace", "delete":
		role := m.agentRoles[m.agentCursor]
		m.resetAgentOverride(role)
		m.agentEditing = false
		return m.commitProjectSelection(sectionAgentSetup)
	case "k":
		return m.openConnect(true), nil
	case "u":
		return m.openURLEdit(m.agentEditProv, true), nil
	case "left", "h":
		return m.changeAgentField(-1)
	case "right", "l":
		return m.changeAgentField(+1)
	}
	return m, nil
}

// changeAgentField changes the value of the focused field by dir.
func (m SetupModel) changeAgentField(dir int) (SetupModel, tea.Cmd) {
	m.agentEditErr = ""
	switch m.agentField {
	case agentFieldRunner:
		m.agentEditProv = m.nextAvailableProvider(m.agentEditProv, dir)
		m.agentBaseURL = ""
		m.agentEditModelReset()
		m.agentEditEffortUnsupported = ""
		return m, m.startAgentEffortLoad()
	case agentFieldModel:
		models := m.modelsForProvider(m.agentEditProv)
		if len(models) == 0 {
			return m, nil
		}
		m.agentEditIdx = cycleIdx(m.agentEditIdx, dir, len(models))
		m.agentEditEffortUnsupported = ""
		return m, m.startAgentEffortLoad()
	case agentFieldEffort:
		if m.agentEffortLoading {
			return m, nil
		}
		choices := effortChoices(m.agentEffortOptions)
		m.agentEditEffortIdx = cycleIdx(m.agentEditEffortIdx, dir, len(choices))
		m.agentEditEffort = choices[m.agentEditEffortIdx]
		m.agentEditEffortUnsupported = ""
	case agentFieldMode:
		cur := 0
		for i, v := range agentModes {
			if v == m.agentEditMode {
				cur = i
			}
		}
		m.agentEditMode = agentModes[cycleIdx(cur, dir, len(agentModes))]
	}
	return m, nil
}

// saveAgentEditor stores the editor values as the role's override and saves
// the project, unless the mode cannot run with the runner.
func (m SetupModel) saveAgentEditor() (SetupModel, tea.Cmd) {
	models := m.modelsForProvider(m.agentEditProv)
	if m.agentField == agentFieldModel && m.agentEditProv == providerClaude && m.agentEditIdx < len(models) && models[m.agentEditIdx] == claudeTypeRow {
		m.startClaudeTyping()
		return m, nil
	}
	if m.agentEffortLoading {
		return m, nil
	}
	ent := m.entry(m.agentEditProv)
	if ent.isNew {
		return m.openConnect(true), nil
	}
	if err := (config.AgentConfig{Runner: ent.runner, Mode: m.agentEditMode}).ModeError(); err != nil {
		m.agentEditErr = err.Error()
		return m, nil
	}
	role := m.agentRoles[m.agentCursor]
	baseURL := ""
	if ent.isAPI() && !ent.custom() {
		baseURL = m.agentBaseURL
	}
	m.agentOverrides[role] = agentSetupOverride{
		runner: ent.runner, provider: ent.provider, baseURL: baseURL,
		model: m.agentModel(), effort: m.agentEffortValue(),
		mode: m.agentEditMode, modeSet: true,
	}
	m.agentEditing = false
	m.agentEditErr = ""
	return m.commitProjectSelection(sectionAgentSetup)
}

// currentPayload snapshots the setup values for a save.
func (m SetupModel) currentPayload() setupDoneMsg {
	provider, model := m.selectedProviderModel()
	ent := m.entry(m.provider)
	baseURL := ""
	if ent.isAPI() && !ent.custom() {
		baseURL = m.baseURLs[ent.runner]
	}

	overrides := make(map[string]agentSetupOverride)
	for k, v := range m.agentOverrides {
		overrides[k] = v
	}
	progLang := ""
	if m.progLangIdx < len(m.progLanguages) && m.progLanguages[m.progLangIdx] != progLangAuto {
		progLang = m.progLanguages[m.progLangIdx]
	}

	return setupDoneMsg{
		provider:       provider,
		providerName:   ent.provider,
		baseURL:        baseURL,
		model:          model,
		effort:         m.selectedEffort(),
		progLanguage:   progLang,
		projectName:    strings.TrimSpace(m.projectNameInput.Value()),
		agentOverrides: overrides,
	}
}

// setSaveStatus records the footer feedback and invalidates older clear timers.
func (m *SetupModel) setSaveStatus(text string, ok bool) {
	m.saveStatus = text
	m.saveStatusOK = ok
	m.saveStatusID++
}

// clearSaveStatusLater hides a success message after a short delay.
func (m SetupModel) clearSaveStatusLater() tea.Cmd {
	id := m.saveStatusID
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return saveStatusClearMsg{id: id}
	})
}

// savePayload writes the current values to the active config target.
func (m SetupModel) savePayload(payload setupDoneMsg) error {
	if m.globalOnly {
		if !m.effortTouched {
			return saveGlobalSetupKeepEffort(m.baseAgents, payload)
		}
		return saveGlobalSetup(m.baseAgents, payload)
	}
	return saveProjectSetup(m.root, payload)
}

// saveGlobalSetupKeepEffort applies provider and model to every agent but
// leaves each agent's own effort alone, so changing only the model does not
// reset effort levels the user never touched.
func saveGlobalSetupKeepEffort(baseAgents map[string]config.AgentConfig, msg setupDoneMsg) error {
	globalAgents := make(map[string]config.AgentConfig, len(baseAgents))
	for role, ac := range baseAgents {
		ac.Runner = msg.provider
		ac.Provider, ac.BaseURL = msg.providerName, msg.baseURL
		if msg.model != "" {
			ac.Model = msg.model
		}
		if ac.Effort == "" {
			ac.Effort = msg.effort
		}
		globalAgents[role] = ac
	}
	return config.SaveGlobalEndpoints(config.Config{Agents: globalAgents})
}

// commitProjectSelection saves the current project values and advances.
func (m SetupModel) commitProjectSelection(next setupSection) (SetupModel, tea.Cmd) {
	if m.pingTesting || m.saving {
		return m, nil
	}
	if err := m.savePayload(m.currentPayload()); err != nil {
		m.setSaveStatus("✗ "+err.Error(), false)
		return m, nil
	}
	m.setSaveStatus("✓ Saved", true)
	m.section = next
	m.ensureSectionVisible()
	return m, m.clearSaveStatusLater()
}

// commitGlobalSelection pings the selected model, then saves global values and
// advances. A failed ping leaves the screen on the effort section with an error.
func (m SetupModel) commitGlobalSelection(next setupSection) (SetupModel, tea.Cmd) {
	if m.pingTesting || m.saving {
		return m, nil
	}
	payload := m.currentPayload()
	m.pingTesting = true
	m.saving = true
	m.pingErr = nil
	m.setSaveStatus("Saving…", true)
	m.pendingSave = &pendingSave{msg: payload, next: next}
	return m, tea.Batch(m.spinner.Tick, pingModel(payload))
}

// startValidation saves all current values and closes the setup screen. Used by
// Ctrl+S; Enter uses the incremental commit helpers above.
func (m SetupModel) startValidation() (SetupModel, tea.Cmd) {
	if m.pingTesting || m.saving {
		return m, nil
	}

	payload := m.currentPayload()
	m.pingTesting = true
	m.saving = true
	m.pingErr = nil
	m.setSaveStatus("Saving…", true)
	m.pendingSave = &pendingSave{msg: payload, close: true}

	return m, tea.Batch(m.spinner.Tick, pingModel(payload))
}

// selectedProviderModel returns the currently selected provider string and model name.
func (m SetupModel) selectedProviderModel() (string, string) {
	models := m.activeModels()
	provider := m.entry(m.provider).runner
	model := ""
	switch {
	case m.typedMode(m.provider):
		model = m.typedModel()
	case m.modelIdx < len(models) && models[m.modelIdx] != claudeTypeRow:
		model = models[m.modelIdx]
		if m.provider == providerDsh {
			model = dshRowModel(model)
		}
	}
	return provider, model
}

// pingModel returns a tea.Cmd that tests provider/model reachability.
func pingModel(p setupDoneMsg) tea.Cmd {
	return func() tea.Msg {
		return pingResultMsg{err: pingSelection(p)}
	}
}

// pingSelection tests the chosen provider: a custom provider or a preset with
// a base_url override is probed at its own URL.
func pingSelection(p setupDoneMsg) error {
	if p.providerName != "" {
		c, ok, err := credentials.Get(p.providerName)
		if err != nil {
			return err
		}
		if !ok || c.BaseURL == "" {
			return nil // not saved: nothing to probe
		}
		return runner.PingAPI(p.providerName, c.BaseURL)
	}
	if p.baseURL != "" {
		return runner.PingAPI(p.provider, p.baseURL)
	}
	return runner.Ping(p.provider, p.model)
}

func fetchOpenCodeModels() tea.Cmd {
	return func() tea.Msg {
		models, err := runner.OpenCodeListModels()
		return modelsListMsg{models: models, err: err}
	}
}

func fetchOllamaModels() tea.Cmd {
	return func() tea.Msg {
		installed, err := runner.OllamaListInstalled()
		return ollamaModelsListMsg{models: installed, err: err}
	}
}

func fetchMLXModels() tea.Cmd {
	return func() tea.Msg {
		installed, err := runner.MLXListInstalled()
		return mlxModelsListMsg{models: installed, err: err}
	}
}

func fetchCodexModels() tea.Cmd {
	return func() tea.Msg {
		models, err := runner.CodexListModels()
		return codexModelsListMsg{models: models, err: err}
	}
}

func fetchClaudeModels() tea.Cmd {
	return func() tea.Msg {
		models, err := runner.ClaudeListModels()
		return claudeModelsListMsg{models: models, err: err}
	}
}

func fetchCopilotModels() tea.Cmd {
	return func() tea.Msg {
		models, err := runner.CopilotListModels()
		return copilotModelsListMsg{models: models, err: err}
	}
}

func fetchLMStudioModels() tea.Cmd {
	return func() tea.Msg {
		models, err := runner.LMStudioListInstalled()
		return lmstudioModelsListMsg{models: models, err: err}
	}
}

// checkProviderAvailability emits one providerAvailabilityMsg per provider
// once each backend has been probed. Slow providers (HTTP timeouts) do not
// block the others — each runs in its own goroutine via tea.Batch.
func checkProviderAvailability() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(providerLabels))
	for i := range providerLabels {
		kind := providerKind(i)
		cmds = append(cmds, func() tea.Msg {
			// An API provider without a key is not offline: the screen offers
			// to add the key instead.
			if name := apiPresetName(kind); name != "" && runner.KeySource(name, "") == "" {
				return providerAvailabilityMsg{kind: kind}
			}
			err := runner.ProviderReachable(providerToString(kind))
			// dsh without a key is installed: the screen hints at the key.
			if errors.Is(err, runner.ErrDshNoKey) {
				err = nil
			}
			return providerAvailabilityMsg{kind: kind, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// findModelIndex returns the index of name in models, or 0 if not found.
func findModelIndex(models []string, name string) int {
	if name == "" {
		return 0
	}
	for i, m := range models {
		if m == name {
			return i
		}
	}
	return 0
}

func (m SetupModel) apiLoading() bool {
	for _, v := range m.apiModelsLoading {
		if v {
			return true
		}
	}
	return false
}

// isLoadingModels returns true if any model list is still being fetched.
func (m SetupModel) isLoadingModels() bool {
	return m.modelsLoading || m.claudeModelsLoading || m.ollamaModelsLoading || m.mlxModelsLoading || m.codexModelsLoading || m.copilotModelsLoading || m.lmstudioModelsLoading || m.deepseekModelsLoading || m.dshModelsLoading || m.apiLoading()
}

// promptsDir returns the project-level prompts override directory.
func (m SetupModel) promptsDir() string {
	if m.root == "" {
		return ""
	}
	return filepath.Join(m.root, artifacts.DirName, prompts.PromptsDirName)
}

// hasCustomPrompt returns true if any prompt override exists for the given role.
func (m SetupModel) hasCustomPrompt(role string) bool {
	dir := m.promptsDir()
	if dir == "" {
		return false
	}
	for _, name := range prompts.PromptsForRole(role) {
		if prompts.OverrideExists(name, dir) {
			return true
		}
	}
	return false
}

// exportAgentPrompts exports the default prompt templates for the currently selected agent.
func (m *SetupModel) exportAgentPrompts() {
	if m.root == "" {
		m.promptStatus = "no project root — cannot export"
		return
	}
	role := m.agentRoles[m.agentCursor]
	promptNames := prompts.PromptsForRole(role)
	if len(promptNames) == 0 {
		m.promptStatus = fmt.Sprintf("no prompts for %s", role)
		return
	}

	destDir := m.promptsDir()
	var exported []string
	for _, name := range promptNames {
		path, err := prompts.ExportPrompt(name, destDir)
		if err != nil {
			m.promptStatus = fmt.Sprintf("error: %v", err)
			return
		}
		exported = append(exported, filepath.Base(path))
	}

	m.promptStatus = fmt.Sprintf("exported %s → .decsr/prompts/", strings.Join(exported, ", "))
}

// deleteAgentPrompts removes project-level prompt overrides for the selected agent.
func (m *SetupModel) deleteAgentPrompts() {
	if m.root == "" {
		m.promptStatus = "no project root — cannot delete"
		return
	}
	role := m.agentRoles[m.agentCursor]
	promptNames := prompts.PromptsForRole(role)
	destDir := m.promptsDir()

	var deleted []string
	for _, name := range promptNames {
		path := filepath.Join(destDir, name+".md")
		if err := os.Remove(path); err == nil {
			deleted = append(deleted, filepath.Base(path))
		}
	}
	if len(deleted) == 0 {
		m.promptStatus = fmt.Sprintf("no exported prompt for %s", role)
		return
	}
	m.promptStatus = fmt.Sprintf("deleted .decsr/prompts/%s", strings.Join(deleted, ", "))
}
