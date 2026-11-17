package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/lang"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

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
	if m.connect != nil {
		body := lipgloss.Place(m.width, max(m.height-2, 4), lipgloss.Center, lipgloss.Center, m.connect.box(m.width))
		return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	}
	if m.urlEdit != nil {
		body := lipgloss.Place(m.width, max(m.height-2, 4), lipgloss.Center, lipgloss.Center, m.urlEditBox())
		return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
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
	for i, ent := range m.entries {
		lbl := ent.label
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
	lines := packTabs(providerTabs, contentWidth-8)
	sel := m.entry(m.provider)
	if kl := m.apiKeyLine(); kl != "" {
		style := lipgloss.NewStyle().Foreground(p.dim)
		if strings.Contains(kl, "not set") {
			style = lipgloss.NewStyle().Foreground(crt.warn)
		}
		lines = append(lines, "", "  "+style.Render(kl))
	}
	if bl := m.baseURLLine(sel, m.baseURLOverride(sel)); bl != "" {
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(p.dim).Render(bl))
	}
	if sel.missing {
		lines = append(lines, "", "  "+lipgloss.NewStyle().Foreground(crt.warn).Render(missingHint(sel)))
	}
	return setupCard("Default Provider", m.section == sectionProvider, contentWidth, lines)
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
	case providerDeepSeek:
		modelLines = append(modelLines, dim.Render("  DeepSeek API ("+m.apiKeyLine()+")"))
		modelLines = append(modelLines, m.viewDeepSeekModels(active, inactive, dim)...)
	case providerOpenAI, providerOpenRouter, providerGroq:
		modelLines = append(modelLines, dim.Render("  "+m.entry(m.provider).label+" API ("+m.apiKeyLine()+")"))
		modelLines = append(modelLines, m.viewAPIModels(m.provider, active, inactive, dim)...)
	case providerDsh:
		modelLines = append(modelLines, dim.Render("  DeepSeek Harness (dsh)"))
		switch {
		case m.dshModelsLoading:
			modelLines = append(modelLines, dim.Render("  loading models…"))
		case m.typedMode(providerDsh):
			modelLines = append(modelLines, dim.Render("  "+dshTypedHint()), "  "+m.typed.View())
		default:
			modelLines = append(modelLines, renderWindowedList(m.dshRows(), m.modelIdx, m.section == sectionModel, active, inactive, dim)...)
		}
	default:
		if e := m.entry(m.provider); e.custom() {
			modelLines = append(modelLines, dim.Render("  "+e.label+" API ("+m.apiKeyLine()+")"))
			if e.missing {
				modelLines = append(modelLines, dim.Render("  "+missingHint(e)))
			}
			modelLines = append(modelLines, m.viewAPIModels(m.provider, active, inactive, dim)...)
		}
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

	if model == "" {
		model = "(default)"
	}
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
		lines = append(lines, renderWindowedList(effortLabels(choices, m.provider == providerClaude), cursor, m.section == sectionEffort, active, inactive, dim)...)
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

// progLangAuto is the setup-list entry that saves an empty language so the
// language is detected from the project files.
const progLangAuto = "auto"

// progLangLabels returns the list rows; the auto row shows what is detected.
func (m SetupModel) progLangLabels() []string {
	labels := append([]string(nil), m.progLanguages...)
	for i, l := range labels {
		if l != progLangAuto {
			continue
		}
		detected := ""
		if m.root != "" {
			detected = lang.Detect(m.root)
		}
		if detected == "" {
			labels[i] = "auto (not detected)"
		} else {
			labels[i] = "auto (" + detected + ")"
		}
	}
	return labels
}

func (m SetupModel) renderProgLangCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {
	isFocused := m.section == sectionProgLang

	var lines []string
	lines = append(lines, dim.Render("  Primary language for generated code (overrides auto-detection):"))
	lines = append(lines, "")

	lines = append(lines, renderWindowedList(m.progLangLabels(), m.progLangIdx, isFocused, active, inactive, dim)...)

	return setupCard("Programming Language", isFocused, contentWidth, lines)
}

func (m SetupModel) renderAgentCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {
	p := homePalette
	goldStyle := lipgloss.NewStyle().Foreground(p.gold).Bold(true)
	cyanStyle := lipgloss.NewStyle().Foreground(p.cyan)

	var agentLines []string
	agentLines = append(agentLines, dim.Render("  Override runner, model, effort & mode per agent (this project only):"))
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
				if ov.runner == "claude" {
					modelPart = dim.Render("default → " + runner.ClaudeDefaultModel + " · " + runner.ClaudeDefaultEffort)
				}
			}
			display = runnerPart + dim.Render(" / ") + modelPart
			if ov.effort != "" && ov.effort != setupEffortDefault {
				display += dim.Render(" · ") + goldStyle.Render(ov.effort)
			}
			if ov.mode != "" {
				display += dim.Render(" · mode ") + cyanStyle.Render(ov.mode)
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
		agentLines = append(agentLines, m.agentEditorLines(contentWidth, focusLabel, active, inactive, dim, goldStyle)...)
	}

	return setupCard("Agent Overrides", m.section == sectionAgentSetup, contentWidth, agentLines)
}

// agentModeLabel is the Mode field text.
func agentModeLabel(mode string) string {
	if mode == "" {
		return "runner default"
	}
	return mode
}

// agentEditorLines is the editing panel of an agent: the focused field is
// open, the others show their value on one line.
func (m SetupModel) agentEditorLines(contentWidth int, focusLabel, active, inactive, dim, gold lipgloss.Style) []string {
	role := m.agentRoles[m.agentCursor]
	ent := m.entry(m.agentEditProv)
	provLabel := ent.label
	if m.providerUnavailable(m.agentEditProv) {
		provLabel += " (offline)"
	}

	lines := []string{gold.Render("  Edit " + role + ":")}
	head := func(field int, label, value string) {
		mark := "    "
		ls := dim
		if m.agentField == field {
			mark = "  ▶ "
			ls = focusLabel
		}
		line := ls.Render(mark + fmt.Sprintf("%-7s", label))
		if m.agentField != field && value != "" {
			line += inactive.Render(value)
		}
		lines = append(lines, line)
	}

	// Runner.
	head(agentFieldRunner, "Runner", provLabel)
	if m.agentField == agentFieldRunner {
		var tabs []string
		offline := lipgloss.NewStyle().Foreground(homePalette.dim).Strikethrough(true)
		for i, e := range m.entries {
			kind := providerKind(i)
			unavailable := m.providerUnavailable(kind)
			switch {
			case kind == m.agentEditProv && unavailable:
				tabs = append(tabs, focusLabel.Render("[ "+e.label+" ]")+" "+offline.Render("(offline)"))
			case kind == m.agentEditProv:
				tabs = append(tabs, focusLabel.Render("[ "+e.label+" ]"))
			case unavailable:
				tabs = append(tabs, offline.Render("  "+e.label+" (offline)"))
			default:
				tabs = append(tabs, inactive.Render("  "+e.label))
			}
		}
		lines = append(lines, packTabs(tabs, contentWidth-8)...)
		lines = append(lines, m.agentEndpointLines(dim)...)
	}

	// Model.
	models := m.modelsForProvider(m.agentEditProv)
	modelValue := "(default)"
	switch {
	case m.typedMode(m.agentEditProv):
		if v := strings.TrimSpace(m.typed.Value()); v != "" {
			modelValue = v
		}
	case m.agentEditIdx >= 0 && m.agentEditIdx < len(models) && models[m.agentEditIdx] != claudeTypeRow:
		modelValue = models[m.agentEditIdx]
	}
	head(agentFieldModel, "Model", modelValue)
	if m.agentField == agentFieldModel {
		switch {
		case m.typedMode(m.agentEditProv):
			hint := "no model list — type a model id:"
			if ent.runner == "dsh" {
				hint = dshTypedHint()
			}
			lines = append(lines, dim.Render("    "+hint), "    "+m.typed.View())
		case m.claudeTyping && m.agentEditProv == providerClaude:
			lines = append(lines, m.claudeTypingLines(dim)...)
		case len(models) == 0:
			lines = append(lines, dim.Render("    no models available"))
		case m.agentEditProv == providerClaude:
			if line := m.claudeDefaultLine(m.agentClaudeModelEmpty()); line != "" {
				lines = append(lines, dim.Render("    "+line))
			}
			labels, hints := m.claudeLabels()
			lines = append(lines, renderWindowedListHints(labels, hints, m.agentEditIdx, true, active, inactive, dim)...)
		default:
			lines = append(lines, renderWindowedList(models, m.agentEditIdx, true, active, inactive, dim)...)
		}
	}

	// Effort.
	effortValue := "…"
	choices := effortChoices(m.agentEffortOptions)
	if !m.agentEffortLoading && m.agentEditEffortIdx >= 0 && m.agentEditEffortIdx < len(choices) {
		effortValue = effortLabels(choices, m.agentEditProv == providerClaude)[m.agentEditEffortIdx]
	}
	head(agentFieldEffort, "Effort", effortValue)
	if m.agentField == agentFieldEffort {
		if m.agentEffortLoading {
			lines = append(lines, dim.Render("    "+m.spinner.View()+" loading effort levels…"))
		} else {
			cursor := min(max(m.agentEditEffortIdx, 0), len(choices)-1)
			lines = append(lines, renderWindowedList(effortLabels(choices, m.agentEditProv == providerClaude), cursor, true, active, inactive, dim)...)
		}
	}
	if m.agentEditEffortUnsupported != "" {
		warn := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		lines = append(lines, warn.Render(fmt.Sprintf("    ⚠ %q is not offered by this model — choose a level or keep the default", m.agentEditEffortUnsupported)))
	}

	// Mode.
	head(agentFieldMode, "Mode", agentModeLabel(m.agentEditMode))
	if m.agentField == agentFieldMode {
		var tabs []string
		for _, v := range agentModes {
			if v == m.agentEditMode {
				tabs = append(tabs, focusLabel.Render("[ "+agentModeLabel(v)+" ]"))
			} else {
				tabs = append(tabs, inactive.Render("  "+agentModeLabel(v)))
			}
		}
		lines = append(lines, packTabs(tabs, contentWidth-8)...)
	}

	if m.agentEditErr != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(crt.warn).Bold(true).Render("  ✗ "+m.agentEditErr))
	}
	return append(lines, "", dim.Render("  ↑↓ field  ←→ change  Enter save  Bksp reset  Esc cancel"))
}

// agentEndpointLines are the dim key and base URL lines of the runner being
// edited for an agent.
func (m SetupModel) agentEndpointLines(dim lipgloss.Style) []string {
	e := m.entry(m.agentEditProv)
	var out []string
	if kl := m.keyLineFor(e); kl != "" {
		out = append(out, dim.Render("  "+kl))
	}
	if bl := m.baseURLLine(e, m.agentBaseURL); bl != "" {
		out = append(out, dim.Render("  "+bl))
	}
	if e.missing {
		out = append(out, dim.Render("  "+missingHint(e)))
	}
	return out
}

// setupCard wraps card lines in a pixel panel titled in its top border. The
// focused card gets the azure focus border.
func setupCard(title string, focused bool, contentWidth int, lines []string) string {
	tc := crt.primary
	if focused {
		tc = galaxyAzure
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

	if m.claudeTyping {
		return append(lines, m.claudeTypingLines(dim)...)
	}
	if line := m.claudeDefaultLine(m.globalClaudeModelEmpty()); line != "" {
		lines = append(lines, dim.Render("  "+line))
	}
	labels, hints := m.claudeLabels()
	lines = append(lines, renderWindowedListHints(labels, hints, m.modelIdx, isModelSection, active, inactive, dim)...)
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

func (m SetupModel) viewDeepSeekModels(active, inactive, dim lipgloss.Style) []string {
	if m.deepseekModelsLoading {
		return []string{dim.Render("  loading models…")}
	}
	return renderWindowedList(m.deepseekModels, m.modelIdx, m.section == sectionModel, active, inactive, dim)
}

func (m SetupModel) viewAPIModels(kind providerKind, active, inactive, dim lipgloss.Style) []string {
	key := m.entry(kind).key()
	if m.apiModelsLoading[key] {
		return []string{dim.Render("  loading models…")}
	}
	if len(m.apiModels[key]) == 0 {
		hint := "  no model list — the provider did not return models; type a model id:"
		if runner.KeySource(m.entry(kind).credName(), "") == "" {
			hint = "  no model list — add an API key with k in the provider row, or type a model id:"
		}
		return []string{dim.Render(hint), "  " + m.typed.View()}
	}
	return renderWindowedList(m.apiModels[key], m.modelIdx, m.section == sectionModel, active, inactive, dim)
}

// ── Footer ───────────────────────────────────────────────────────────────────

func (m SetupModel) renderFooter() string {
	hint := func(k, desc string) keyHint { return keyHint{k, desc} }

	var hints []keyHint
	switch {
	case m.agentEditing && m.claudeTyping && m.agentEditProv == providerClaude && m.agentField == agentFieldModel:
		hints = []keyHint{
			hint("type", "model id"),
			hint("Enter", "select"),
			hint("Esc", "back"),
		}
	case m.agentEditing && m.agentEditTyping():
		hints = []keyHint{
			hint("type", "model id"),
			hint("↑↓", "field"),
			hint("Enter", "save"),
			hint("Esc", "cancel"),
		}
	case m.agentEditing:
		hints = []keyHint{hint("↑↓", "field"), hint("←→", "change")}
		if m.entry(m.agentEditProv).isAPI() {
			hints = append(hints, hint("k", "API key"))
		}
		if m.entry(m.agentEditProv).canEditURL() {
			hints = append(hints, hint("u", "base URL"))
		}
		hints = append(hints, hint("Enter", "save"), hint("Bksp", "reset"), hint("Esc", "cancel"))
	case m.connect != nil:
		hints = []keyHint{
			hint("Enter", "test & save"),
			hint("Esc", "cancel"),
		}
	case m.urlEdit != nil:
		hints = []keyHint{
			hint("Enter", "save"),
			hint("empty", "reset"),
			hint("Esc", "cancel"),
		}
	case m.section == sectionProvider:
		hints = []keyHint{hint("←→", "provider"), hint("k", "API key")}
		if m.entry(m.provider).canEditURL() {
			hints = append(hints, hint("u", "base URL"))
		}
		hints = append(hints,
			hint("Tab/↓", "models"),
			hint("[]", "scroll"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		)
	case m.section == sectionModel && m.claudeTyping && m.provider == providerClaude:
		hints = []keyHint{
			hint("type", "model id"),
			hint("Enter", "effort"),
			hint("Esc", "back"),
		}
	case m.section == sectionModel && m.typedMode(m.provider):
		hints = []keyHint{
			hint("type", "model id"),
			hint("Enter", "effort"),
			hint("↑", "provider"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		}
	case m.section == sectionModel:
		hints = []keyHint{hint("↑↓", "navigate"), hint("Enter", "effort")}
		if m.entry(m.provider).canEditURL() {
			hints = append(hints, hint("u", "base URL"))
		}
		hints = append(hints,
			hint("Tab", "next"),
			hint("Ctrl+S", "save & close"),
			hint("Esc", "done"),
		)
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
			// Anchor on the editing panel so the fields stay in view.
			marker := "Edit " + m.agentRoles[m.agentCursor] + ":"
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
	return renderWindowedListHints(items, nil, cursor, isFocused, active, inactive, dim)
}

// renderWindowedListHints is renderWindowedList with an optional dim hint after
// each item; hints is empty or as long as items.
func renderWindowedListHints(items, hints []string, cursor int, isFocused bool, active, inactive, dim lipgloss.Style) []string {
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
		hint := ""
		if i < len(hints) && hints[i] != "" {
			hint = dim.Render("  " + hints[i])
		}
		if sel {
			lines = append(lines, active.Render(marker+items[i])+hint)
		} else {
			lines = append(lines, inactive.Render(marker+items[i])+hint)
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
