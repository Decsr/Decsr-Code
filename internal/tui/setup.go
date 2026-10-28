package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/config"
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
)

var providerLabels = []string{"OpenCode", "Claude", "Ollama", "oMLX", "Codex", "Copilot", "LM Studio"}

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
	runner string
	model  string
	effort string
}

// ── messages ──────────────────────────────────────────────────────────────────

type setupDoneMsg struct {
	provider       string
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

	// Project display name (project setup only).
	projectNameInput textinput.Model

	// Per-agent configuration.
	agentRoles     []string
	agentOverrides map[string]agentSetupOverride
	agentCursor    int
	agentEditing   bool
	agentEditStep  int          // 0 = runner, 1 = model, 2 = effort
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

var setupAgentRoles = []string{"pm", "coder", "coder_fixer", "qa", "ux_reviewer", "security"}

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
		providerAvailability:  make(map[providerKind]error),
		progLanguages:         config.SupportedProgrammingLanguages,
		projectNameInput:      nameInput,
		agentRoles:            setupAgentRoles,
		agentOverrides:        make(map[string]agentSetupOverride),
		baseAgents:            cloneAgentConfigs(agentCfgs),
		effort:                normalizeSetupEffort(currentEffort),
		spinner:               sp,
		width:                 80,
		height:                24,
	}

	m.provider = providerFromString(currentRunner)

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
				m.agentOverrides[role] = agentSetupOverride{runner: r, model: mdl, effort: eff}
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
		if ov.runner != "" || ov.model != "" || ov.effort != "" {
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
	n := providerKind(len(providerLabels))
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
		return m.claudeModels
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
	default:
		return m.models
	}
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

// effortLabels renders the default sentinel as a human-readable option.
func effortLabels(choices []string) []string {
	out := make([]string, len(choices))
	for i, choice := range choices {
		if choice == setupEffortDefault {
			out[i] = "(provider default)"
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
	if model == "" {
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
	provider := providerToString(m.agentEditProv)
	models := m.modelsForProvider(m.agentEditProv)
	if m.agentEditIdx >= len(models) {
		return nil
	}
	model := models[m.agentEditIdx]
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
	if m.effortLoading || m.effortFor == "" {
		return normalizeSetupEffort(m.effort)
	}
	choices := effortChoices(m.effortOptions)
	if m.effortIdx >= 0 && m.effortIdx < len(choices) {
		return choices[m.effortIdx]
	}
	return normalizeSetupEffort(m.effort)
}

func (m SetupModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchOpenCodeModels(), fetchOllamaModels(), fetchMLXModels(), fetchCodexModels(), fetchClaudeModels(), fetchCopilotModels(), fetchLMStudioModels(), checkProviderAvailability())
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
			if !found {
				m.effortUnsupported = m.effort
				m.effort = setupEffortDefault
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
			if m.provider == providerClaude && m.modelIdx < len(m.claudeModels) {
				prevModel = m.claudeModels[m.modelIdx]
			}
			m.claudeModels = msg.models
			if m.provider == providerClaude {
				m.modelIdx = findModelIndex(m.claudeModels, prevModel)
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
		prevEditStep := m.agentEditStep
		m, cmd = m.handleKey(msg)
		cmds = append(cmds, cmd)
		m.syncProjectNameFocus()

		// Update viewport content after key handling.
		if m.ready {
			m.viewport.SetContent(m.renderContent())

			// If section, editing state, or edit step changed, the handler
			// already called ensureSectionVisible which set the correct
			// YOffset. Re-apply it since SetContent may have adjusted it.
			if m.section != prevSection || m.agentEditing != prevEditing || m.agentEditStep != prevEditStep {
				m.ensureSectionVisible()
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m SetupModel) handleKey(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	if msg.String() == "ctrl+s" {
		return m.startValidation()
	}
	if m.saving {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		if m.agentEditing {
			if m.agentEditStep > 0 {
				m.agentEditStep--
				if m.agentEditStep == 1 {
					m.agentEditEffortUnsupported = ""
				}
				return m, nil
			}
			m.agentEditing = false
			return m, nil
		}
		return m, func() tea.Msg { return setupClosedMsg{} }
	case "[":
		if m.scrollX > 0 {
			m.scrollX -= horizontalScrollStep
			if m.scrollX < 0 {
				m.scrollX = 0
			}
		}
		return m, nil
	case "]":
		m.scrollX += horizontalScrollStep
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
	case "tab", "down", "enter":
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
		m.modelIdx = 0
		m.pingErr = nil
		m.effort = setupEffortDefault
		m.effortIdx = 0
		m.effortOptions = nil
		m.effortFor = ""
		m.effortUnsupported = ""
	}
	return m, cmd
}

func (m SetupModel) handleModelKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {

	models := m.activeModels()
	switch msg.String() {
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
		// Move to the effort picker instead of saving immediately, so choosing
		// a model always presents its effort options.
		m.section = sectionEffort
		cmd := m.startEffortLoad()
		m.ensureSectionVisible()
		return m, cmd
	}
	return m, nil
}

// resetEffortForNewModel clears a global effort that may not be supported by
// the newly selected model. The user can pick a new level in the effort section.
func (m *SetupModel) resetEffortForNewModel() {
	m.effort = setupEffortDefault
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
			m.effortUnsupported = ""
		}
	case "down", "j":
		if m.effortLoading {
			return m, cmd
		}
		if m.effortIdx < len(choices)-1 {
			m.effortIdx++
			m.effortUnsupported = ""
		}
	case "enter":
		if m.effortLoading {
			return m, cmd
		}
		if m.effortIdx < 0 || m.effortIdx >= len(choices) {
			m.effortIdx = 0
		}
		m.effort = choices[m.effortIdx]
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
		m.agentEditing = true
		m.agentEditStep = 0
		role := m.agentRoles[m.agentCursor]
		m.agentEditEffort = setupEffortDefault
		m.agentEditEffortIdx = 0
		m.agentEditEffortUnsupported = ""
		m.agentEffortFor = ""
		m.agentEffortOptions = nil
		m.agentEffortLoading = false
		if ov, ok := m.agentOverrides[role]; ok {
			m.agentEditProv = providerFromString(ov.runner)
			if ov.effort != "" {
				m.agentEditEffort = normalizeSetupEffort(ov.effort)
			}
		} else {
			m.agentEditProv = m.provider
		}
		m.agentEditIdx = 0
		m.ensureSectionVisible()
	case "backspace", "delete":
		role := m.agentRoles[m.agentCursor]
		delete(m.agentOverrides, role)
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

func (m SetupModel) handleAgentEditKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	if m.agentEditStep == 0 {
		// Step 0: pick runner.
		switch msg.String() {
		case "left", "h":
			m.agentEditProv = m.nextAvailableProvider(m.agentEditProv, -1)
		case "right", "l":
			m.agentEditProv = m.nextAvailableProvider(m.agentEditProv, +1)
		case "enter":
			m.agentEditStep = 1
			m.agentEditIdx = 0
			// Pre-select current model if exists.
			role := m.agentRoles[m.agentCursor]
			if ov, ok := m.agentOverrides[role]; ok {
				for i, mdl := range m.modelsForProvider(m.agentEditProv) {
					if mdl == ov.model {
						m.agentEditIdx = i
						break
					}
				}
			}
			m.ensureSectionVisible()
		}
		return m, nil
	}

	if m.agentEditStep == 1 {
		// Step 1: pick model.
		models := m.modelsForProvider(m.agentEditProv)
		switch msg.String() {
		case "up", "k":
			if m.agentEditIdx > 0 {
				m.agentEditIdx--
			}
		case "down", "j":
			if m.agentEditIdx < len(models)-1 {
				m.agentEditIdx++
			}
		case "enter":
			// Move to effort. Keep the existing effort only when the model did
			// not change; otherwise start from the global effort.
			modelStr := ""
			if m.agentEditIdx < len(models) {
				modelStr = models[m.agentEditIdx]
			}
			role := m.agentRoles[m.agentCursor]
			effort := normalizeSetupEffort(m.effort)
			if ov, ok := m.agentOverrides[role]; ok {
				if ov.model == modelStr && providerFromString(ov.runner) == m.agentEditProv && ov.effort != "" {
					effort = normalizeSetupEffort(ov.effort)
				}
			}
			m.agentEditEffort = effort
			m.agentEditEffortIdx = 0
			m.agentEditEffortUnsupported = ""
			m.agentEditStep = 2
			cmd := m.startAgentEffortLoad()
			m.ensureSectionVisible()
			return m, cmd
		case "backspace", "delete":
			role := m.agentRoles[m.agentCursor]
			delete(m.agentOverrides, role)
			m.agentEditing = false
			return m.commitProjectSelection(sectionAgentSetup)
		}
		return m, nil
	}

	// Step 2: pick effort.
	choices := effortChoices(m.agentEffortOptions)
	switch msg.String() {
	case "up", "k":
		if !m.agentEffortLoading && m.agentEditEffortIdx > 0 {
			m.agentEditEffortIdx--
			m.agentEditEffortUnsupported = ""
		}
	case "down", "j":
		if !m.agentEffortLoading && m.agentEditEffortIdx < len(choices)-1 {
			m.agentEditEffortIdx++
			m.agentEditEffortUnsupported = ""
		}
	case "enter":
		if m.agentEffortLoading {
			return m, nil
		}
		role := m.agentRoles[m.agentCursor]
		runnerStr := providerToString(m.agentEditProv)
		modelStr := ""
		if models := m.modelsForProvider(m.agentEditProv); m.agentEditIdx < len(models) {
			modelStr = models[m.agentEditIdx]
		}
		effortStr := setupEffortDefault
		if m.agentEditEffortIdx >= 0 && m.agentEditEffortIdx < len(choices) {
			effortStr = choices[m.agentEditEffortIdx]
		}
		m.agentOverrides[role] = agentSetupOverride{runner: runnerStr, model: modelStr, effort: effortStr}
		m.agentEditing = false
		return m.commitProjectSelection(sectionAgentSetup)
	case "backspace", "delete":
		role := m.agentRoles[m.agentCursor]
		delete(m.agentOverrides, role)
		m.agentEditing = false
		return m.commitProjectSelection(sectionAgentSetup)
	}
	return m, nil
}

// currentPayload snapshots the setup values for a save.
func (m SetupModel) currentPayload() setupDoneMsg {
	provider, model := m.selectedProviderModel()

	overrides := make(map[string]agentSetupOverride)
	for k, v := range m.agentOverrides {
		overrides[k] = v
	}
	progLang := ""
	if m.progLangIdx < len(m.progLanguages) {
		progLang = m.progLanguages[m.progLangIdx]
	}

	return setupDoneMsg{
		provider:       provider,
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
		return saveGlobalSetup(m.baseAgents, payload)
	}
	return saveProjectSetup(m.root, payload)
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
	return m, tea.Batch(m.spinner.Tick, pingModel(payload.provider, payload.model))
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

	return m, tea.Batch(m.spinner.Tick, pingModel(payload.provider, payload.model))
}

// selectedProviderModel returns the currently selected provider string and model name.
func (m SetupModel) selectedProviderModel() (string, string) {
	models := m.activeModels()
	provider := providerToString(m.provider)
	model := ""
	if m.modelIdx < len(models) {
		model = models[m.modelIdx]
	}
	return provider, model
}

// pingModel returns a tea.Cmd that tests provider/model reachability.
func pingModel(provider, model string) tea.Cmd {
	return func() tea.Msg {
		return pingResultMsg{err: runner.Ping(provider, model)}
	}
}

// ── Viewport ──────────────────────────────────────────────────────────────────

func (m *SetupModel) syncViewport() {
	vpHeight := m.height - 2 // header + footer
	if vpHeight < 4 {
		vpHeight = 4
	}
	if !m.ready {
		m.viewport = viewport.New(m.width, vpHeight)
		m.viewport.Style = lipgloss.NewStyle()
		m.ready = true
	} else {
		m.viewport.Width = m.width
		m.viewport.Height = vpHeight
	}
	m.viewport.SetContent(m.renderContent())
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m SetupModel) View() string {
	header := screenHeader(m.width, "Global Settings", "")
	if !m.globalOnly {
		header = screenHeader(m.width, "Project Setup", "")
	}
	footer := m.renderFooter()

	if !m.ready {
		return lipgloss.JoinVertical(lipgloss.Left, header, "", footer)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		m.viewport.View(),
		footer,
	)
}

// ── Content ───────────────────────────────────────────────────────────────────

func (m SetupModel) renderContent() string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(p.bright)
	focusLabelStyle := lipgloss.NewStyle().Bold(true).Foreground(p.accent)
	activeItemStyle := lipgloss.NewStyle().Bold(true).Foreground(p.green)
	inactiveItemStyle := lipgloss.NewStyle().Foreground(p.bright)
	sectionHeaderStyle := lipgloss.NewStyle().Bold(true).Foreground(p.gold)

	contentWidth := m.width - 4
	if contentWidth < 60 {
		contentWidth = 60
	}

	sep := dimStyle.Render(strings.Repeat("─", contentWidth))

	providerCard := m.renderProviderCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle)
	modelCard := m.renderModelCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle, dimStyle)
	effortCard := m.renderEffortCard(contentWidth, activeItemStyle, inactiveItemStyle, dimStyle)

	globalHeader := sectionHeaderStyle.Render("  ◆ Global Defaults") + dimStyle.Render("  — saved to ~/.decsr/config.yaml")

	var parts []string
	if !m.globalOnly {
		projectNameCard := m.renderProjectNameCard(contentWidth, labelStyle, focusLabelStyle, dimStyle)
		progLangCard := m.renderProgLangCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle, dimStyle)
		agentCard := m.renderAgentCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle, dimStyle)
		projectHeader := sectionHeaderStyle.Render("  ◆ Project Overrides") + dimStyle.Render("  — saved to .decsr/project.yaml")
		parts = []string{
			"",
			projectHeader,
			projectNameCard,
			sep,
			agentCard,
			sep,
			progLangCard,
			"",
			dimStyle.Render("  Global provider, model & effort are configured in Global Settings (g)."),
		}
	} else {
		parts = []string{
			"",
			globalHeader,
			providerCard,
			sep,
			modelCard,
			sep,
			effortCard,
		}
	}

	parts = append(parts, "")

	content := strings.Join(parts, "\n")

	vpH := m.height - 2
	if vpH < 4 {
		vpH = 4
	}

	natW := maxLineWidth(content)
	if natW <= m.width {
		return lipgloss.Place(m.width, vpH, lipgloss.Center, lipgloss.Top, content)
	}

	scrollX := m.scrollX
	maxScrollX := natW - m.width
	if maxScrollX < 0 {
		maxScrollX = 0
	}
	if scrollX > maxScrollX {
		scrollX = maxScrollX
	}
	return horizontalSlice(content, scrollX, m.width)
}

func (m SetupModel) renderProviderCard(contentWidth int, label, focusLabel, active, inactive lipgloss.Style) string {
	p := homePalette
	offline := lipgloss.NewStyle().Foreground(p.dim).Strikethrough(true)

	var providerTabs []string
	for i, lbl := range providerLabels {
		kind := providerKind(i)
		unavailable := m.providerUnavailable(kind)
		displayLbl := lbl
		if unavailable {
			displayLbl = lbl + " (offline)"
		}
		switch {
		case kind == m.provider && unavailable:
			// Selected but unreachable — keep cursor visible but mark it.
			tab := offline.Render("▶ " + displayLbl)
			if m.section == sectionProvider {
				tab = focusLabel.Render("[ "+lbl+" ]") + " " + offline.Render("(offline)")
			}
			providerTabs = append(providerTabs, tab)
		case kind == m.provider:
			tab := active.Render("▶ " + lbl)
			if m.section == sectionProvider {
				tab = focusLabel.Render("[ " + lbl + " ]")
			}
			providerTabs = append(providerTabs, tab)
		case unavailable:
			providerTabs = append(providerTabs, offline.Render("  "+displayLbl))
		default:
			providerTabs = append(providerTabs, inactive.Render("  "+lbl))
		}
	}
	providerLine := "  " + strings.Join(providerTabs, "  ")

	return setupCard("Default Provider", m.section == sectionProvider, contentWidth, []string{providerLine})
}

func (m SetupModel) renderModelCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {

	var modelLines []string

	switch m.provider {
	case providerOpenCode:
		modelLines = append(modelLines, m.viewOpenCodeModels(active, inactive, dim, focusLabel)...)
		if m.modelsStatus != "" {
			warnStyle := lipgloss.NewStyle().Foreground(crt.warn)
			modelLines = append(modelLines, "", warnStyle.Render("  "+m.modelsStatus))
		}
	case providerClaude:
		modelLines = append(modelLines, dim.Render("  Claude CLI (--model flag)"))
		modelLines = append(modelLines, m.viewClaudeModels(active, inactive, dim)...)
	case providerOllama:
		modelLines = append(modelLines, dim.Render("  Ollama local models (REST API)"))
		modelLines = append(modelLines, m.viewOllamaModels(active, inactive, dim)...)
	case providerMLX:
		modelLines = append(modelLines, dim.Render("  oMLX local models (OpenAI-compatible API on :8000)"))
		modelLines = append(modelLines, m.viewMLXModels(active, inactive, dim)...)
	case providerCodex:
		modelLines = append(modelLines, dim.Render("  Codex CLI (OpenAI)"))
		modelLines = append(modelLines, m.viewCodexModels(active, inactive, dim)...)
	case providerCopilot:
		modelLines = append(modelLines, dim.Render("  GitHub Copilot CLI"))
		modelLines = append(modelLines, m.viewCopilotModels(active, inactive, dim)...)
	case providerLMStudio:
		modelLines = append(modelLines, dim.Render("  LM Studio local models (OpenAI-compatible API on :1234)"))
		modelLines = append(modelLines, m.viewLMStudioModels(active, inactive, dim)...)
	}

	// Model validation status.
	if m.pingTesting {
		modelLines = append(modelLines, "")
		modelLines = append(modelLines, dim.Render("  "+m.spinner.View()+" Testing connection…"))
	} else if m.pingErr != nil {
		warnStyle := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		modelLines = append(modelLines, "")
		modelLines = append(modelLines, warnStyle.Render("  ✗ "+m.pingErr.Error()))
		modelLines = append(modelLines, dim.Render("  Change selection or press Enter to retry"))
	}

	return setupCard("Default Model", m.section == sectionModel, contentWidth, modelLines)
}

func (m SetupModel) renderEffortCard(contentWidth int, active, inactive, dim lipgloss.Style) string {
	provider, model := m.selectedProviderModel()

	var lines []string
	lines = append(lines, dim.Render(fmt.Sprintf("  Reasoning effort for %s / %s:", provider, model)))
	lines = append(lines, "")

	loaded := m.effortFor != "" && !m.effortLoading
	switch {
	case m.effortLoading:
		lines = append(lines, dim.Render("  "+m.spinner.View()+" loading effort levels…"))
	case m.effortFor == "":
		lines = append(lines, dim.Render("  Press Tab or Enter here to load this model's effort levels."))
		if m.effort != "" && m.effort != setupEffortDefault {
			lines = append(lines, dim.Render("  Current: "+m.effort))
		}
	case len(m.effortOptions) == 0:
		lines = append(lines, dim.Render("  This model exposes no effort control — provider default is used."))
	}

	if loaded {
		choices := effortChoices(m.effortOptions)
		cursor := m.effortIdx
		if cursor < 0 {
			cursor = 0
		}
		if cursor >= len(choices) {
			cursor = len(choices) - 1
		}
		lines = append(lines, renderWindowedList(effortLabels(choices), cursor, m.section == sectionEffort, active, inactive, dim)...)
	}

	if m.effortUnsupported != "" {
		warnStyle := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		lines = append(lines, "")
		lines = append(lines, warnStyle.Render(fmt.Sprintf("  ⚠ %q is not offered by this model — choose a level or keep the default", m.effortUnsupported)))
	}

	lines = append(lines, "")
	lines = append(lines, dim.Render("  ↑↓ navigate  Enter save  Tab next"))

	return setupCard("Effort", m.section == sectionEffort, contentWidth, lines)
}

func (m SetupModel) renderProjectNameCard(contentWidth int, label, focusLabel, dim lipgloss.Style) string {
	isFocused := m.section == sectionProjectName

	var lines []string
	lines = append(lines, dim.Render("  Display name for this project:"))
	lines = append(lines, "")
	lines = append(lines, "  "+m.projectNameInput.View())

	return setupCard("Project Name", isFocused, contentWidth, lines)
}

func (m SetupModel) renderProgLangCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {
	isFocused := m.section == sectionProgLang

	var lines []string
	lines = append(lines, dim.Render("  Primary language for generated code (overrides auto-detection):"))
	lines = append(lines, "")

	lines = append(lines, renderWindowedList(m.progLanguages, m.progLangIdx, isFocused, active, inactive, dim)...)

	return setupCard("Programming Language", isFocused, contentWidth, lines)
}

func (m SetupModel) renderAgentCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {
	p := homePalette
	goldStyle := lipgloss.NewStyle().Foreground(p.gold).Bold(true)
	cyanStyle := lipgloss.NewStyle().Foreground(p.cyan)

	var agentLines []string
	agentLines = append(agentLines, dim.Render("  Override runner, model & effort per agent (this project only):"))
	agentLines = append(agentLines, "")

	for i, role := range m.agentRoles {
		c := roleColor(role)
		rs := lipgloss.NewStyle().Foreground(c).Bold(true)

		display := dim.Render("(default)")
		if ov, ok := m.agentOverrides[role]; ok {
			runnerPart := cyanStyle.Render(ov.runner)
			if ov.runner == "" {
				runnerPart = dim.Render("(global)")
			}
			modelPart := inactive.Render(ov.model)
			if ov.model == "" {
				modelPart = dim.Render("(auto)")
			}
			display = runnerPart + dim.Render(" / ") + modelPart
			if ov.effort != "" && ov.effort != setupEffortDefault {
				display += dim.Render(" · ") + goldStyle.Render(ov.effort)
			}
		}

		// Show indicator if custom prompt exists.
		promptTag := ""
		if m.hasCustomPrompt(role) {
			promptTag = goldStyle.Render(" ✦")
		}

		isActive := m.section == sectionAgentSetup && i == m.agentCursor && !m.agentEditing
		if isActive {
			agentLines = append(agentLines,
				active.Render("  ▶ ")+rs.Render(fmt.Sprintf("%-14s", role))+display+promptTag)
		} else {
			agentLines = append(agentLines,
				"    "+rs.Render(fmt.Sprintf("%-14s", role))+display+promptTag)
		}
	}

	// Prompt status message.
	if m.promptStatus != "" {
		agentLines = append(agentLines, "")
		agentLines = append(agentLines, goldStyle.Render("  "+m.promptStatus))
	}

	// Editing panel.
	if m.agentEditing {
		agentLines = append(agentLines, "")
		role := m.agentRoles[m.agentCursor]

		switch m.agentEditStep {
		case 0:
			agentLines = append(agentLines, goldStyle.Render("  Select runner for "+role+":"))
			agentLines = append(agentLines, "")

			var tabs []string
			for i, lbl := range providerLabels {
				kind := providerKind(i)
				unavailable := m.providerUnavailable(kind)
				offline := lipgloss.NewStyle().Foreground(homePalette.dim).Strikethrough(true)
				displayLbl := lbl
				if unavailable {
					displayLbl = lbl + " (offline)"
				}
				switch {
				case kind == m.agentEditProv && unavailable:
					tabs = append(tabs, focusLabel.Render("[ "+lbl+" ]")+" "+offline.Render("(offline)"))
				case kind == m.agentEditProv:
					tabs = append(tabs, focusLabel.Render("[ "+lbl+" ]"))
				case unavailable:
					tabs = append(tabs, offline.Render("  "+displayLbl))
				default:
					tabs = append(tabs, inactive.Render("  "+lbl))
				}
			}
			agentLines = append(agentLines, "  "+strings.Join(tabs, "  "))
			agentLines = append(agentLines, "")
			agentLines = append(agentLines, dim.Render("  ←→ runner  Enter confirm  Esc cancel"))
		case 1:
			provLabel := providerLabels[m.agentEditProv]
			agentLines = append(agentLines, goldStyle.Render(fmt.Sprintf("  Select model for %s (%s):", role, provLabel)))

			models := m.modelsForProvider(m.agentEditProv)
			if len(models) == 0 {
				agentLines = append(agentLines, dim.Render("  no models available"))
			} else {
				agentLines = append(agentLines, renderWindowedList(models, m.agentEditIdx, true, active, inactive, dim)...)
			}
			agentLines = append(agentLines, "")
			agentLines = append(agentLines, dim.Render("  ↑↓ navigate  Enter select  Bksp reset  Esc back"))
		case 2:
			provLabel := providerLabels[m.agentEditProv]
			agentLines = append(agentLines, goldStyle.Render(fmt.Sprintf("  Select effort for %s (%s):", role, provLabel)))
			if m.agentEffortLoading {
				agentLines = append(agentLines, dim.Render("  "+m.spinner.View()+" loading effort levels…"))
			} else {
				choices := effortChoices(m.agentEffortOptions)
				cursor := m.agentEditEffortIdx
				if cursor < 0 {
					cursor = 0
				}
				if cursor >= len(choices) {
					cursor = len(choices) - 1
				}
				agentLines = append(agentLines, renderWindowedList(effortLabels(choices), cursor, true, active, inactive, dim)...)
				if m.agentEditEffortUnsupported != "" {
					warnStyle := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
					agentLines = append(agentLines, "")
					agentLines = append(agentLines, warnStyle.Render(fmt.Sprintf("  ⚠ %q is not offered by this model — choose a level or keep the default", m.agentEditEffortUnsupported)))
				}
			}
			agentLines = append(agentLines, "")
			agentLines = append(agentLines, dim.Render("  ↑↓ navigate  Enter save  Bksp reset  Esc back"))
		}
	}

	return setupCard("Agent Overrides", m.section == sectionAgentSetup, contentWidth, agentLines)
}

// setupCard wraps card lines in a pixel panel titled in its top border. The
// focused card gets the violet focus border.
func setupCard(title string, focused bool, contentWidth int, lines []string) string {
	tc := crt.primary
	if focused {
		tc = galaxyViolet
	}
	return pixelPanel("◆ "+strings.ToUpper(title), tc, focused,
		lipgloss.NewStyle().Padding(0, 1).Width(contentWidth-2), strings.Join(lines, "\n"))
}

// ── Model list renderers ─────────────────────────────────────────────────────

func (m SetupModel) viewOpenCodeModels(active, inactive, dim, _ lipgloss.Style) []string {
	isModelSection := m.section == sectionModel
	var lines []string

	if m.modelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	// Build labels with cloud/local suffix before windowing.
	labels := make([]string, len(m.models))
	for i, mdl := range m.models {
		suffix := dim.Render("  (local)")
		if strings.Contains(mdl, "/") {
			suffix = dim.Render("  (cloud)")
		}
		labels[i] = mdl + suffix
	}
	lines = append(lines, renderWindowedList(labels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewClaudeModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel

	var lines []string

	if m.claudeModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.claudeModels) == 0 {
		lines = append(lines, dim.Render("  no models found — is claude CLI installed?"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.claudeModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewOllamaModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel
	var lines []string

	if m.ollamaModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.ollamaModels) == 0 {
		lines = append(lines, dim.Render("  no models found — run: ollama pull <model>"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.ollamaModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewMLXModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel
	var lines []string

	if m.mlxModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.mlxModels) == 0 {
		lines = append(lines, dim.Render("  no models found — install via LM Studio or `mlx_lm.convert`"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.mlxModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewCodexModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel

	var lines []string

	if m.codexModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.codexModels) == 0 {
		lines = append(lines, dim.Render("  no models found — check OPENAI_API_KEY"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.codexModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewCopilotModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel

	var lines []string

	if m.copilotModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.copilotModels) == 0 {
		lines = append(lines, dim.Render("  copilot CLI did not return any models — pass --model manually"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.copilotModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewLMStudioModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel

	var lines []string

	if m.lmstudioModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.lmstudioModels) == 0 {
		lines = append(lines, dim.Render("  no models found — start the LM Studio server and load a model"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.lmstudioModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

// ── Footer ───────────────────────────────────────────────────────────────────

func (m SetupModel) renderFooter() string {
	hint := func(k, desc string) keyHint { return keyHint{k, desc} }

	var hints []keyHint
	switch {
	case m.agentEditing && m.agentEditStep == 0:
		hints = []keyHint{
			hint("←→", "runner"),
			hint("Enter", "confirm"),
			hint("Esc", "cancel"),
		}
	case m.agentEditing && m.agentEditStep == 1:
		hints = []keyHint{
			hint("↑↓", "navigate"),
			hint("Enter", "select"),
			hint("Bksp", "reset"),
			hint("Esc", "back"),
		}
	case m.agentEditing && m.agentEditStep == 2:
		hints = []keyHint{
			hint("↑↓", "effort"),
			hint("Enter", "save"),
			hint("Bksp", "reset"),
			hint("Esc", "back"),
		}
	case m.section == sectionProvider:
		hints = []keyHint{
			hint("←→", "provider"),
			hint("Tab/↓", "models"),
			hint("[]", "scroll"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		}
	case m.section == sectionModel:
		hints = []keyHint{
			hint("↑↓", "navigate"),
			hint("Enter", "effort"),
			hint("Tab", "next"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		}
	case m.section == sectionEffort:
		hints = []keyHint{
			hint("↑↓", "effort"),
			hint("Enter", "save"),
			hint("Tab", "next"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		}
	case m.section == sectionProgLang:
		hints = []keyHint{
			hint("↑↓", "language"),
			hint("Enter", "save"),
			hint("Tab", "project"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		}
	case m.section == sectionAgentSetup:
		hints = []keyHint{
			hint("↑↓", "navigate"),
			hint("Enter", "configure"),
			hint("p", "export prompt"),
			hint("d", "delete prompt"),
			hint("Bksp", "reset"),
			hint("Tab", "language"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		}
	case m.section == sectionProjectName:
		hints = []keyHint{
			hint("type", "edit name"),
			hint("Enter", "save"),
			hint("Tab", "agents"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		}
	}

	right := ""
	if m.saveStatus != "" {
		style := lipgloss.NewStyle().Background(crt.panelBg).Foreground(galaxyMint).Bold(true)
		if !m.saveStatusOK {
			style = style.Foreground(galaxyRed)
		} else if strings.HasPrefix(m.saveStatus, "Saving") {
			style = style.Foreground(galaxyGold)
		}
		right = style.Render(m.saveStatus) + "  "
	}
	if m.ready && m.viewport.TotalLineCount() > m.viewport.Height {
		pct := m.viewport.ScrollPercent() * 100
		right += lipgloss.NewStyle().
			Background(crt.panelBg).
			Foreground(crt.dim).
			Render(fmt.Sprintf("%.0f%%", pct))
	}

	return hintBar(m.width, hints, right)
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
			return providerAvailabilityMsg{
				kind: kind,
				err:  runner.ProviderReachable(providerToString(kind)),
			}
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

// isLoadingModels returns true if any model list is still being fetched.
func (m SetupModel) isLoadingModels() bool {
	return m.modelsLoading || m.claudeModelsLoading || m.ollamaModelsLoading || m.mlxModelsLoading || m.codexModelsLoading || m.copilotModelsLoading || m.lmstudioModelsLoading
}

// ensureSectionVisible scrolls the viewport so the focused section is visible.
func (m *SetupModel) ensureSectionVisible() {
	if !m.ready {
		return
	}

	// Re-render content with current state to get accurate line positions.
	content := m.renderContent()
	m.viewport.SetContent(content)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	var targetLine int
	switch m.section {
	case sectionAgentSetup:
		if m.globalOnly {
			targetLine = findLineContaining(lines, "Agent Overrides")
			break
		}
		if m.agentEditing {
			// Anchor on the editing panel marker so the runner/model/effort
			// picker stays in view instead of scrolling past the card.
			marker := "Select runner for"
			switch m.agentEditStep {
			case 1:
				marker = "Select model for"
			case 2:
				marker = "Select effort for"
			}
			targetLine = findLineContaining(lines, marker)
			if targetLine < 0 {
				targetLine = totalLines - m.viewport.Height
			}
		} else {
			// Agent overrides is the first section in project setup — scroll to top.
			m.viewport.SetYOffset(0)
			return
		}
	case sectionProgLang:
		targetLine = findLineContaining(lines, "Programming Language")
	case sectionEffort:
		targetLine = findLineContaining(lines, "Reasoning effort")
		if targetLine < 0 {
			targetLine = findLineContaining(lines, "EFFORT")
		}
		if targetLine < 0 {
			targetLine = findLineContaining(lines, "Default Model")
		}
	case sectionProjectName:
		targetLine = findLineContaining(lines, "Project Name")
		if targetLine < 0 {
			m.viewport.SetYOffset(0)
			return
		}
	case sectionProvider:
		targetLine = findLineContaining(lines, "Default Provider")
		if targetLine < 0 {
			// Fallback: global-only mode, provider is at top.
			m.viewport.SetYOffset(0)
			return
		}
	case sectionModel:
		targetLine = findLineContaining(lines, "Default Model")
	}

	if targetLine < 0 {
		return
	}

	margin := 2
	target := targetLine - margin
	if target < 0 {
		target = 0
	}

	maxOffset := totalLines - m.viewport.Height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if target > maxOffset {
		target = maxOffset
	}

	m.viewport.SetYOffset(target)
}

// renderWindowedList renders a windowed view of items around cursor.
// When there are more items than listWindowSize, only a window is shown with
// "↑ N more" / "↓ N more" indicators. items are raw label strings.
const listWindowSize = 7

func renderWindowedList(items []string, cursor int, isFocused bool, active, inactive, dim lipgloss.Style) []string {
	if len(items) == 0 {
		return nil
	}

	start, end := 0, len(items)
	if len(items) > listWindowSize {
		half := listWindowSize / 2
		start = cursor - half
		if start < 0 {
			start = 0
		}
		end = start + listWindowSize
		if end > len(items) {
			end = len(items)
			start = end - listWindowSize
			if start < 0 {
				start = 0
			}
		}
	}

	var lines []string
	if start > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("    ↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		sel := isFocused && i == cursor
		marker := "    "
		if i == cursor {
			marker = "  ▶ "
		}
		if sel {
			lines = append(lines, active.Render(marker+items[i]))
		} else {
			lines = append(lines, inactive.Render(marker+items[i]))
		}
	}
	if end < len(items) {
		lines = append(lines, dim.Render(fmt.Sprintf("    ↓ %d more", len(items)-end)))
	}
	return lines
}

// findLineContaining returns the 0-based line index containing substr, or -1.
func findLineContaining(lines []string, substr string) int {
	for i, l := range lines {
		if strings.Contains(l, substr) {
			return i
		}
	}
	return -1
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
		return
	}
	role := m.agentRoles[m.agentCursor]
	promptNames := prompts.PromptsForRole(role)
	destDir := m.promptsDir()

	for _, name := range promptNames {
		path := filepath.Join(destDir, name+".md")
		_ = os.Remove(path)
	}
	m.promptStatus = fmt.Sprintf("removed custom prompts for %s", role)
}
