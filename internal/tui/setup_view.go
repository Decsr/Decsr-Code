package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/lang"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// setupSideBySideMin is the width from which Model and Effort sit side by side.
const setupSideBySideMin = 110

// setupListWindow is the number of rows of a list shown at once.
const setupListWindow = 7

// progLangAuto is the setup-list entry that saves an empty language so the
// language is detected from the project files.
const progLangAuto = "auto"

// projectInfo is the read-only part of the Project column.
type projectInfo struct {
	isolation string
	autonomy  string
	budget    string
}

// WithProjectInfo gives the screen the workspace settings it shows next to the
// editable project fields.
func (m SetupModel) WithProjectInfo(cfg config.Config) SetupModel {
	w := cfg.Workspace
	m.info.isolation = w.Isolation
	if m.info.isolation == "" {
		m.info.isolation = "auto"
	}
	m.info.autonomy = fmt.Sprintf("%d %s", max(w.Autonomy, 1), autonomyName(max(w.Autonomy, 1)))
	m.info.budget = "none"
	if w.BudgetUSD > 0 {
		m.info.budget = fmt.Sprintf("$%.2f", w.BudgetUSD)
	}
	return m
}

func sfg(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// ── viewport ─────────────────────────────────────────────────────────────────

// setupBodyHeight is the number of rows between the header and the status line.
func (m SetupModel) setupBodyHeight() int { return max(m.height-4, 4) }

func (m *SetupModel) syncViewport() {
	h := m.setupBodyHeight()
	if !m.ready {
		m.viewport = viewport.New(m.width, h)
		m.viewport.Style = lipgloss.NewStyle()
		m.ready = true
	} else {
		m.viewport.Width = m.width
		m.viewport.Height = h
	}
	m.viewport.SetContent(m.renderContent())
}

// ── view ─────────────────────────────────────────────────────────────────────

func (m SetupModel) headerLine() string {
	if m.globalOnly {
		return ui.Header("decsr · Global settings", "saved to ~/.decsr/config.yaml", m.width)
	}
	name := strings.TrimSpace(m.projectNameInput.Value())
	if name == "" && m.root != "" {
		name = filepath.Base(m.root)
	}
	left := "decsr · Project setup"
	if name != "" {
		left += " · " + name
	}
	return ui.Header(left, "saved to .decsr/project.yaml", m.width)
}

func (m SetupModel) statusLine() string {
	if m.saveStatus == "" {
		return ""
	}
	c := ui.OK
	switch {
	case !m.saveStatusOK:
		c = ui.Fail
	case strings.HasPrefix(m.saveStatus, "Saving"):
		c = ui.Warn
	}
	return ui.RightAlign("", sfg(c).Bold(true).Render(ui.Truncate(m.saveStatus, max(m.width-4, 1)))+"  ", m.width)
}

func (m SetupModel) View() string {
	header := m.headerLine()
	footer := m.renderFooter()
	if !m.ready {
		return strings.Join([]string{header, "", footer}, "\n")
	}
	base := strings.Join([]string{header, "", m.viewport.View(), m.statusLine(), footer}, "\n")
	switch {
	case m.connect != nil:
		return ui.PlaceOverlay(base, m.connect.box(m.width), m.width, m.height)
	case m.urlEdit != nil:
		return ui.PlaceOverlay(base, m.urlEditBox(), m.width, m.height)
	case m.agentEditing:
		return ui.PlaceOverlay(base, m.agentEditorBox(), m.width, m.height)
	}
	return base
}

// ── content ──────────────────────────────────────────────────────────────────

func (m SetupModel) renderContent() string {
	lines, _ := m.layout()
	return strings.Join(lines, "\n")
}

// layout is the body lines and the first line of each section.
func (m SetupModel) layout() ([]string, map[setupSection]int) {
	var lines []string
	var anchors map[setupSection]int
	if m.globalOnly {
		lines, anchors = m.globalLines()
	} else {
		lines, anchors = m.projectLines()
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "")
	}
	return lines, anchors
}

// ensureSectionVisible scrolls the viewport so the focused section is visible.
func (m *SetupModel) ensureSectionVisible() {
	if !m.ready {
		return
	}
	lines, anchors := m.layout()
	m.viewport.SetContent(strings.Join(lines, "\n"))
	target := max(anchors[m.section]-1, 0)
	m.viewport.SetYOffset(min(target, max(len(lines)-m.viewport.Height, 0)))
}

// ── global settings ──────────────────────────────────────────────────────────

func (m SetupModel) globalLines() ([]string, map[setupSection]int) {
	w := m.width
	anchors := map[setupSection]int{}
	lines := strings.Split(ui.Box("DEFAULT PROVIDER", m.providerLines(w-4), w, m.section == sectionProvider), "\n")

	anchors[sectionModel] = len(lines)
	anchors[sectionEffort] = len(lines)
	// The model list shows in full when it fits the rows left on screen.
	rows := m.setupBodyHeight() - len(lines) - 2
	if w >= setupSideBySideMin {
		lw := w * 3 / 5
		model := m.modelColumn(lw-4, rows)
		effort := m.effortColumn(w - lw - 4)
		lines = append(lines, boxPair(model, effort, "DEFAULT MODEL", "EFFORT", lw, w-lw, m.section)...)
	} else {
		effort := m.effortColumn(w - 4)
		model := m.modelColumn(w-4, rows-len(effort)-4)
		lines = append(lines, strings.Split(ui.Box("DEFAULT MODEL", model, w, m.section == sectionModel), "\n")...)
		anchors[sectionEffort] = len(lines)
		lines = append(lines, strings.Split(ui.Box("EFFORT", effort, w, m.section == sectionEffort), "\n")...)
	}
	return lines, anchors
}

// boxPairTitled draws two boxes next to each other, as tall as the taller one.
func boxPairTitled(left, right []string, lt, rt string, lw, rw int, lf, rf bool) []string {
	n := max(len(left), len(right))
	for len(left) < n {
		left = append(left, "")
	}
	for len(right) < n {
		right = append(right, "")
	}
	l := strings.Split(ui.Box(lt, left, lw, lf), "\n")
	r := strings.Split(ui.Box(rt, right, rw, rf), "\n")
	return sideBySide(l, r, lw, 0)
}

// boxPair draws the Model and Effort boxes next to each other.
func boxPair(model, effort []string, modelTitle, effortTitle string, lw, rw int, section setupSection) []string {
	return boxPairTitled(model, effort, modelTitle, effortTitle, lw, rw, section == sectionModel, section == sectionEffort)
}

// providerTabs is the label of every provider that can be chosen: the
// reachable ones and the selected one.
func (m SetupModel) providerLines(width int) []string {
	var tabs []string
	var offline []string
	for i, ent := range m.entries {
		kind := providerKind(i)
		unavailable := m.providerUnavailable(kind)
		switch {
		case kind == m.provider:
			label := ent.label
			if unavailable {
				label += " (offline)"
			}
			mark := ui.Muted
			if m.section == sectionProvider {
				mark = ui.Accent
			}
			tabs = append(tabs, sfg(mark).Render("▌")+lipgloss.NewStyle().Foreground(ui.Text).Bold(true).Render(label)+sfg(mark).Render("▐"))
		case unavailable:
			offline = append(offline, ent.label)
		case ent.missing:
			tabs = append(tabs, sfg(ui.Warn).Render(ent.label))
		default:
			tabs = append(tabs, sfg(ui.Text).Render(ent.label))
		}
	}
	lines := wrapItems(tabs, 0, max(width, 20), "  ")
	if len(offline) > 0 {
		lines = append(lines, sfg(ui.Muted).Render("offline: "+strings.Join(offline, " · ")))
	}
	sel := m.entry(m.provider)
	if kl := m.apiKeyLine(); kl != "" {
		c := ui.Muted
		if strings.Contains(kl, "not set") {
			c = ui.Warn
		}
		lines = append(lines, sfg(c).Render(kl))
	}
	if bl := m.baseURLLine(sel, m.baseURLOverride(sel)); bl != "" {
		lines = append(lines, sfg(ui.Muted).Render(bl))
	}
	if sel.missing {
		lines = append(lines, sfg(ui.Warn).Render(missingHint(sel)))
	}
	return lines
}

// modelPane is what a provider offers in the Model column.
type modelPane struct {
	head  []string // dim lines above the list
	items []string
	hints []string
	notes []string // dim lines shown instead of a list
	input []string // text input lines shown instead of a list
}

// modelPaneFor describes the models of provider p for the selection screens.
func (m SetupModel) modelPaneFor(p providerKind) modelPane {
	loading := func(on bool) (modelPane, bool) {
		if on {
			return modelPane{notes: []string{"loading models…"}}, true
		}
		return modelPane{}, false
	}
	empty := func(items []string, note string) modelPane {
		if len(items) == 0 {
			return modelPane{notes: []string{note}}
		}
		return modelPane{items: items}
	}
	e := m.entry(p)
	switch p {
	case providerOpenCode:
		if pane, ok := loading(m.modelsLoading); ok {
			return pane
		}
		hints := make([]string, len(m.models))
		for i, mdl := range m.models {
			hints[i] = "local"
			if strings.Contains(mdl, "/") {
				hints[i] = "cloud"
			}
		}
		pane := modelPane{items: m.models, hints: hints}
		if m.modelsStatus != "" {
			pane.notes = []string{m.modelsStatus}
		}
		return pane
	case providerClaude:
		if pane, ok := loading(m.claudeModelsLoading); ok {
			return pane
		}
		if len(m.claudeModels) == 0 {
			return modelPane{notes: []string{"no models found — is claude CLI installed?"}}
		}
		if m.claudeTyping {
			return modelPane{input: m.claudeTypingLines()}
		}
		pane := modelPane{}
		if line := m.claudeDefaultLine(m.globalClaudeModelEmpty()); line != "" {
			pane.head = []string{line}
		}
		pane.items, pane.hints = m.claudeLabels()
		return pane
	case providerOllama:
		if pane, ok := loading(m.ollamaModelsLoading); ok {
			return pane
		}
		return empty(m.ollamaModels, "no models found — run: ollama pull <model>")
	case providerMLX:
		if pane, ok := loading(m.mlxModelsLoading); ok {
			return pane
		}
		return empty(m.mlxModels, "no models found — install via LM Studio or `mlx_lm.convert`")
	case providerCodex:
		if pane, ok := loading(m.codexModelsLoading); ok {
			return pane
		}
		return empty(m.codexModels, "no models found — check OPENAI_API_KEY")
	case providerCopilot:
		if pane, ok := loading(m.copilotModelsLoading); ok {
			return pane
		}
		return empty(m.copilotModels, "copilot CLI did not return any models — pass --model manually")
	case providerLMStudio:
		if pane, ok := loading(m.lmstudioModelsLoading); ok {
			return pane
		}
		return empty(m.lmstudioModels, "no models found — start the LM Studio server and load a model")
	case providerDeepSeek:
		if pane, ok := loading(m.deepseekModelsLoading); ok {
			return pane
		}
		return empty(m.deepseekModels, "no models found")
	}
	if e.runner == "dsh" {
		return modelPane{head: []string{"type a model id, blank for dsh's default:"}, input: []string{m.typed.View()}}
	}
	if e.isNew {
		return modelPane{notes: []string{"press Enter to add a provider"}}
	}
	if !e.isAPI() {
		return modelPane{}
	}
	key := e.key()
	pane := modelPane{}
	if e.missing {
		pane.head = []string{missingHint(e)}
	}
	if m.apiModelsLoading[key] {
		pane.notes = []string{"loading models…"}
		return pane
	}
	if len(m.apiModels[key]) == 0 {
		hint := "no model list — the provider did not return models; type a model id:"
		if runner.KeySource(e.credName(), "") == "" {
			hint = "no model list — add an API key with k in the provider row, or type a model id:"
		}
		pane.head = append(pane.head, hint)
		pane.input = []string{m.typed.View()}
		return pane
	}
	pane.items = m.apiModels[key]
	return pane
}

// modelColumn is the Model pane of the global screen, width cells wide.
// rows is the number of rows the model list may use before it is windowed.
func (m SetupModel) modelColumn(width, rows int) []string {
	var lines []string
	pane := m.modelPaneFor(m.provider)
	for _, h := range pane.head {
		lines = append(lines, wrapDim("  ", h, width, ui.Muted)...)
	}
	switch {
	case len(pane.input) > 0:
		for _, in := range pane.input {
			lines = append(lines, "  "+in)
		}
	case len(pane.items) > 0:
		lines = append(lines, listLinesIn(pane.items, pane.hints, m.modelIdx, width, rows-len(pane.head)-len(pane.notes))...)
	}
	for _, n := range pane.notes {
		c := ui.Muted
		if m.provider == providerOpenCode && n == m.modelsStatus {
			c = ui.Warn
		}
		lines = append(lines, wrapDim("  ", n, width, c)...)
	}
	switch {
	case m.pingTesting:
		lines = append(lines, "", "  "+sfg(ui.Muted).Render(m.spinner.View()+" Testing connection…"))
	case m.pingErr != nil:
		lines = append(lines, "")
		lines = append(lines, wrapDim("  ", "! "+m.pingErr.Error(), width, ui.Warn)...)
		lines = append(lines, "  "+sfg(ui.Muted).Render("Change selection or press Enter to retry"))
	}
	return lines
}

// effortColumn is the Effort pane of the global screen, width cells wide.
func (m SetupModel) effortColumn(width int) []string {
	var lines []string
	dim := sfg(ui.Muted)
	loaded := m.effortFor != "" && !m.effortLoading
	switch {
	case m.effortLoading:
		lines = append(lines, "  "+dim.Render(m.spinner.View()+" loading effort levels…"))
	case m.effortFor == "":
		// Not loaded yet: claude and codex have fixed levels, so show them
		// now; other runners load their levels on the first visit.
		_, model := m.selectedProviderModel()
		if model == "" && m.provider == providerClaude {
			model = runner.ClaudeDefaultModel
		}
		rn := m.entry(m.provider).runner
		if opts := runner.EffortOptions(rn, model); (rn == "claude" || rn == "codex" || rn == "dsh") && len(opts) > 0 {
			choices := effortChoices(opts)
			idx, _ := effortChoiceIndex(choices, m.effort)
			lines = append(lines, listLines(effortLabels(choices, rn == "claude"), nil, idx, width)...)
			break
		}
		lines = append(lines, wrapDim("  ", "Tab or Enter loads this model's effort levels", width, ui.Muted)...)
		if m.effort != "" && m.effort != setupEffortDefault {
			lines = append(lines, "  "+dim.Render("current: "+m.effort))
		}
	case len(m.effortOptions) == 0:
		lines = append(lines, wrapDim("  ", "no effort control for this model — provider default", width, ui.Muted)...)
	}
	if loaded {
		choices := effortChoices(m.effortOptions)
		cursor := min(max(m.effortIdx, 0), len(choices)-1)
		lines = append(lines, listLines(effortLabels(choices, m.provider == providerClaude), nil, cursor, width)...)
	}
	if m.effortUnsupported != "" {
		lines = append(lines, wrapDim("  ", fmt.Sprintf("%q is not offered by this model — choose a level or keep the default", m.effortUnsupported), width, ui.Warn)...)
	}
	return lines
}

// ── project setup ────────────────────────────────────────────────────────────

func (m SetupModel) projectLines() ([]string, map[setupSection]int) {
	w := m.width
	leftW := w / 2
	if w < 90 {
		leftW = max(w-38, 30)
	}
	rightW := max(w-leftW, 10)
	anchors := map[setupSection]int{}

	left, right := m.agentLines(leftW-4), m.infoLines(rightW-4, anchors)
	for k := range anchors {
		anchors[k]++ // the top border of the box
	}
	lines := boxPairTitled(left, right, "AGENTS", "PROJECT", leftW, rightW, m.section == sectionAgentSetup, m.section == sectionProjectName || m.section == sectionProgLang)
	lines = append(lines, "", "  "+sfg(ui.Muted).Render("Global provider, model & effort are configured in Global Settings (g)."))
	return lines, anchors
}

// agentSummary is the dim text after a role in the Agents list.
func (m SetupModel) agentSummary(role string) string {
	ov, ok := m.agentOverrides[role]
	if !ok || (ov.runner == "" && ov.model == "" && ov.effort == "") {
		s := "inherits global"
		if ok && ov.mode != "" {
			s += " · " + ov.mode
		}
		return s
	}
	name := ov.runner
	if ov.provider != "" {
		name = ov.provider
	}
	if name == "" {
		name = "global"
	}
	model := ov.model
	if ov.runner == "claude" {
		model = strings.TrimPrefix(model, "claude-")
	}
	if model == "" {
		model = "default"
	}
	effort := ov.effort
	if effort == "" {
		effort = setupEffortDefault
	}
	s := name + " · " + model + " · " + effort
	if ov.mode != "" {
		s += " · " + ov.mode
	}
	return s
}

func (m SetupModel) agentLines(width int) []string {
	focused := m.section == sectionAgentSetup
	var lines []string
	for i, role := range m.agentRoles {
		name := role
		if m.hasCustomPrompt(role) {
			name += " ✦"
		}
		lines = append(lines, "  "+ui.ListRow(ui.RowOpts{
			Left: name, Right: m.agentSummary(role), Width: width - 2, Selected: focused && i == m.agentCursor, Indent: 1,
		}))
	}
	if m.promptStatus != "" {
		lines = append(lines, "")
		lines = append(lines, wrapDim("  ", m.promptStatus, width, ui.Warn)...)
	}
	return lines
}

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

// languageValue is the language row text: "go (auto)" or the chosen language.
func (m SetupModel) languageValue() string {
	if m.progLangIdx < 0 || m.progLangIdx >= len(m.progLanguages) {
		return "auto"
	}
	l := m.progLanguages[m.progLangIdx]
	if l != progLangAuto {
		return l
	}
	detected := ""
	if m.root != "" {
		detected = lang.Detect(m.root)
	}
	if detected == "" {
		return "auto (not detected)"
	}
	return detected + " (auto)"
}

func (m SetupModel) infoLines(width int, anchors map[setupSection]int) []string {
	var lines []string
	row := func(label, value string, focus bool) string {
		ls := sfg(ui.Muted)
		if focus {
			ls = sfg(ui.Text).Bold(true)
		}
		return "  " + ls.Render(fmt.Sprintf("%-10s", label)) + sfg(ui.Text).Render(ui.Truncate(value, max(width-14, 1)))
	}
	nameFocus := m.section == sectionProjectName
	anchors[sectionProjectName] = len(lines)
	if nameFocus {
		lines = append(lines, "  "+sfg(ui.Text).Bold(true).Render(fmt.Sprintf("%-10s", "name"))+m.projectNameInput.View())
	} else {
		name := strings.TrimSpace(m.projectNameInput.Value())
		if name == "" {
			name = "(unnamed)"
		}
		lines = append(lines, row("name", name, false))
	}
	langFocus := m.section == sectionProgLang
	anchors[sectionProgLang] = len(lines)
	lines = append(lines, row("language", m.languageValue(), langFocus))
	if langFocus {
		lines = append(lines, listLines(m.progLangLabels(), nil, m.progLangIdx, width)...)
	}
	lines = append(lines,
		row("isolation", m.info.isolation, false),
		row("autonomy", m.info.autonomy, false),
		row("budget", m.info.budget, false),
	)
	return lines
}

// ── agent editor ─────────────────────────────────────────────────────────────

// agentModeLabel is the Mode field text.
func agentModeLabel(mode string) string {
	if mode == "" {
		return "default"
	}
	return mode
}

func (m SetupModel) agentEditorBox() string {
	role := ""
	if m.agentCursor < len(m.agentRoles) {
		role = m.agentRoles[m.agentCursor]
	}
	width := min(64, max(m.width-4, 20))
	inner := width - 4
	ent := m.entry(m.agentEditProv)
	dim := sfg(ui.Muted)

	field := func(idx int, label, value, note string) string {
		ls := dim
		val := sfg(ui.Text).Render(value)
		if m.agentField == idx {
			ls = sfg(ui.Text).Bold(true)
			val = sfg(ui.Accent).Render("‹ ") + sfg(ui.Text).Bold(true).Render(value) + sfg(ui.Accent).Render(" ›")
		}
		line := ls.Render(fmt.Sprintf("%-8s", label)) + val
		if note != "" {
			line += "  " + dim.Render(note)
		}
		return line
	}

	runnerVal := ent.label
	if m.providerUnavailable(m.agentEditProv) {
		runnerVal += " (offline)"
	}
	var body []string
	body = append(body, field(agentFieldRunner, "Runner", runnerVal, ""))
	for _, l := range m.agentEndpointLines() {
		body = append(body, "        "+dim.Render(ui.Truncate(l, max(inner-8, 1))))
	}

	// Model.
	switch {
	case m.typedMode(m.agentEditProv):
		body = append(body, sfg(ui.Text).Render(fmt.Sprintf("%-8s", "Model"))+m.typed.View())
	case m.claudeTyping && m.agentEditProv == providerClaude:
		for i, l := range m.claudeTypingLines() {
			if i == 0 {
				l = fmt.Sprintf("%-8s", "Model") + l
			} else {
				l = "        " + l
			}
			body = append(body, l)
		}
	default:
		models := m.modelsForProvider(m.agentEditProv)
		switch {
		case ent.isNew:
			body = append(body, field(agentFieldModel, "Model", "press Enter to add a provider", ""))
		case len(models) == 0:
			body = append(body, field(agentFieldModel, "Model", "no models available", ""))
		default:
			labels, hints := models, []string(nil)
			if m.agentEditProv == providerClaude {
				labels, hints = m.claudeLabels()
			}
			idx := min(max(m.agentEditIdx, 0), len(labels)-1)
			note := ""
			if idx < len(hints) {
				note = hints[idx]
			}
			body = append(body, field(agentFieldModel, "Model", labels[idx], note))
		}
	}

	// Effort.
	if m.agentEffortLoading {
		body = append(body, field(agentFieldEffort, "Effort", "loading…", ""))
	} else {
		labels := effortLabels(effortChoices(m.agentEffortOptions), m.agentEditProv == providerClaude)
		idx := min(max(m.agentEditEffortIdx, 0), len(labels)-1)
		body = append(body, field(agentFieldEffort, "Effort", labels[idx], ""))
	}
	if m.agentEditEffortUnsupported != "" {
		body = append(body, sfg(ui.Warn).Render(ui.Truncate(fmt.Sprintf("%q is not offered by this model", m.agentEditEffortUnsupported), inner)))
	}

	// Mode.
	effective := config.AgentConfig{Runner: ent.runner, Mode: m.agentEditMode}.EffectiveMode()
	modeNote := ""
	if effective != config.ModeAgent && (config.AgentConfig{Runner: ent.runner, Mode: config.ModeAgent}).ModeError() == nil {
		modeNote = "(agent available)"
	}
	modeVal := agentModeLabel(m.agentEditMode)
	if m.agentEditMode == "" {
		modeVal += " (" + effective + ")"
	}
	body = append(body, field(agentFieldMode, "Mode", modeVal, modeNote))

	if m.agentEditErr != "" {
		body = append(body, "")
		for _, l := range wrapText("! "+m.agentEditErr, inner) {
			body = append(body, sfg(ui.Fail).Render(l))
		}
	}

	hints := []ui.Hint{{Key: "↑↓", Label: "field"}, {Key: "←→", Label: "change"}, {Key: "Enter", Label: "save"}, {Key: "Esc", Label: "cancel"}}
	if m.claudeTyping && m.agentEditProv == providerClaude {
		hints = []ui.Hint{{Key: "type", Label: "model id"}, {Key: "Enter", Label: "select"}, {Key: "Esc", Label: "back"}}
	} else if ent.isAPI() {
		hints = append(hints[:3], append([]ui.Hint{{Key: "k", Label: "key"}}, hints[3:]...)...)
	}
	return ui.Overlay(role, body, hints, width)
}

// agentEndpointLines are the key and base URL lines of the runner being edited
// for an agent.
func (m SetupModel) agentEndpointLines() []string {
	e := m.entry(m.agentEditProv)
	var out []string
	if kl := m.keyLineFor(e); kl != "" {
		out = append(out, kl)
	}
	if bl := m.baseURLLine(e, m.agentBaseURL); bl != "" {
		out = append(out, bl)
	}
	if e.missing {
		out = append(out, missingHint(e))
	}
	return out
}

// ── footer ───────────────────────────────────────────────────────────────────

func (m SetupModel) renderFooter() string {
	h := func(k, l string) ui.Hint { return ui.Hint{Key: k, Label: l} }
	var hints []ui.Hint
	switch {
	case m.agentEditing && m.claudeTyping && m.agentEditProv == providerClaude:
		hints = []ui.Hint{h("type", "model id"), h("Enter", "select"), h("Esc", "back")}
	case m.agentEditing:
		hints = []ui.Hint{h("Esc", "cancel")}
	case m.connect != nil:
		hints = []ui.Hint{h("Enter", "test & save"), h("Esc", "cancel")}
	case m.urlEdit != nil:
		hints = []ui.Hint{h("Enter", "save"), h("empty", "reset"), h("Esc", "cancel")}
	case m.section == sectionProvider:
		hints = []ui.Hint{h("←→", "provider"), h("Tab", "model/effort")}
		hints = append(hints, h("k", "key"))
		if m.entry(m.provider).canEditURL() {
			hints = append(hints, h("u", "base URL"))
		}
		hints = append(hints, h("Ctrl+S", "save & close"), h("Esc", "done"))
	case m.section == sectionModel && m.claudeTyping && m.provider == providerClaude:
		hints = []ui.Hint{h("type", "model id"), h("Enter", "effort"), h("Esc", "back")}
	case m.section == sectionModel && m.typedMode(m.provider):
		hints = []ui.Hint{h("type", "model id"), h("Enter", "effort"), h("↑", "provider"), h("Ctrl+S", "save & close"), h("Esc", "done")}
	case m.section == sectionModel:
		hints = []ui.Hint{h("↑↓", "model"), h("Enter", "effort")}
		if m.entry(m.provider).canEditURL() {
			hints = append(hints, h("u", "base URL"))
		}
		hints = append(hints, h("Tab", "next"), h("Ctrl+S", "save & close"), h("Esc", "done"))
	case m.section == sectionEffort:
		hints = []ui.Hint{h("↑↓", "effort"), h("Enter", "save"), h("Tab", "next"), h("Ctrl+S", "save & close"), h("Esc", "done")}
	case m.section == sectionProgLang:
		hints = []ui.Hint{h("↑↓", "language"), h("Enter", "save"), h("Tab", "project"), h("Ctrl+S", "save & close"), h("Esc", "done")}
	case m.section == sectionAgentSetup:
		hints = []ui.Hint{h("Enter", "edit"), h("Bksp", "reset to global"), h("p", "export prompt"), h("d", "delete prompt"), h("Tab", "language"), h("Ctrl+S", "save & close"), h("Esc", "done")}
	case m.section == sectionProjectName:
		hints = []ui.Hint{h("type", "edit name"), h("Enter", "save"), h("Tab", "agents"), h("Ctrl+S", "save & close"), h("Esc", "done")}
	}
	return ui.HintBar(hints, m.width)
}

// ── helpers ──────────────────────────────────────────────────────────────────

// listLines renders items as list rows under a pane label: two spaces of
// indent, the row under the cursor selected, and "… N more" when the list is
// longer than the window. hints are dim text at the right of each row.
func listLines(items, hints []string, cursor, width int) []string {
	return listLinesIn(items, hints, cursor, width, setupListWindow+2)
}

// listLinesIn is listLines for a list that is shown in full when it fits in
// rows lines; otherwise it shows a window around the cursor between two
// "… N more" markers.
func listLinesIn(items, hints []string, cursor, width, rows int) []string {
	if len(items) == 0 {
		return nil
	}
	cursor = min(max(cursor, 0), len(items)-1)
	start, end := 0, len(items)
	if len(items) > rows {
		win := max(rows-2, 3)
		start = min(max(cursor-win/2, 0), len(items)-win)
		end = start + win
	}
	// Size the rows to their content so a hint sits next to its label.
	lw, hw := 0, 0
	for i := start; i < end; i++ {
		lw = max(lw, ansi.StringWidth(items[i]))
		if i < len(hints) {
			hw = max(hw, ansi.StringWidth(hints[i]))
		}
	}
	rowW := 2 + lw
	if hw > 0 {
		rowW += 2 + hw
	}
	rowW = min(rowW, max(width-2, 4))

	var lines []string
	if start > 0 {
		lines = append(lines, "    "+sfg(ui.Muted).Render(fmt.Sprintf("… %d more", start)))
	}
	for i := start; i < end; i++ {
		right := ""
		if i < len(hints) {
			right = hints[i] + strings.Repeat(" ", max(hw-ansi.StringWidth(hints[i]), 0))
		}
		lines = append(lines, "  "+ui.ListRow(ui.RowOpts{Left: items[i], Right: right, Width: rowW, Selected: i == cursor, Indent: 1}))
	}
	if end < len(items) {
		lines = append(lines, "    "+sfg(ui.Muted).Render(fmt.Sprintf("… %d more", len(items)-end)))
	}
	return lines
}

// sideBySide puts right next to left, left being lw cells wide, with gap
// spaces between them.
func sideBySide(left, right []string, lw, gap int) []string {
	n := max(len(left), len(right))
	out := make([]string, n)
	for i := range out {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		l = ansi.Truncate(l, lw, "")
		out[i] = l + strings.Repeat(" ", max(lw-ansi.StringWidth(l), 0)+gap) + r
	}
	return out
}

// wrapItems lays items out in rows of at most width cells, indent spaces in
// from the left, sep between neighbours.
func wrapItems(items []string, indent, width int, sep string) []string {
	var rows []string
	row, rowW := strings.Repeat(" ", indent), indent
	for i, it := range items {
		w := ansi.StringWidth(it)
		if i > 0 && rowW+len(sep)+w > width {
			rows = append(rows, row)
			row, rowW = strings.Repeat(" ", indent), indent
		} else if i > 0 {
			row += sep
			rowW += len(sep)
		}
		row += it
		rowW += w
	}
	return append(rows, row)
}

// wrapDim renders text in colour c, wrapped to width cells after prefix.
func wrapDim(prefix, text string, width int, c lipgloss.TerminalColor) []string {
	var out []string
	for _, l := range wrapText(text, max(width-len(prefix)-1, 8)) {
		out = append(out, prefix+sfg(c).Render(l))
	}
	return out
}
