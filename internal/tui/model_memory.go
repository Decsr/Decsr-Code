package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// modelMemory mode indices for the on-screen toggle.
const (
	mmModeOff = iota
	mmModeRAM
	mmModeContext
)

var mmModeLabels = []string{"Off", "RAM limit (GB)", "Context limit (tokens)"}
var mmModeKeys = []string{"off", "ram", "context"}

// modelMemoryDoneMsg is emitted when the user saves the model-memory setting.
type modelMemoryDoneMsg struct{ cfg config.ModelMemoryConfig }

// modelMemoryCancelledMsg is emitted when the user cancels without saving.
type modelMemoryCancelledMsg struct{}

// modelMemoryModel is a small standalone screen for the global "Model Memory"
// setting: a toggle between bounding local models by RAM or by context size
// (or off), plus a single numeric value for the selected mode.
type modelMemoryModel struct {
	mode       int
	value      textinput.Model
	totalRAMGB float64

	// Per-mode value memory so toggling between RAM and context modes preserves
	// what the user typed in each, instead of resetting to the default.
	ramValue string
	ctxValue string

	// fresh is true while the input shows a prefilled value the user has not
	// edited yet; the first typed character replaces it instead of appending.
	fresh bool

	// Available local models discovered across backends, for the live
	// RAM→context preview. Loaded asynchronously on Init.
	localModels  []runner.LocalModel
	localLoading bool

	width  int
	height int
}

// localModelsMsg carries the result of the async local-model discovery.
type localModelsMsg struct {
	models []runner.LocalModel
}

func fetchLocalModelsCmd() tea.Cmd {
	return func() tea.Msg {
		return localModelsMsg{models: runner.ListLocalModels()}
	}
}

func newModelMemoryModel(cfg config.Config) modelMemoryModel {
	ti := textinput.New()
	ti.CharLimit = 12
	ti.Prompt = ""
	ti.Focus()

	mode := mmModeOff
	switch cfg.ModelMemory.Mode {
	case "ram":
		mode = mmModeRAM
	case "context":
		mode = mmModeContext
	}

	var total float64
	if vm, err := mem.VirtualMemory(); err == nil && vm.Total > 0 {
		total = float64(vm.Total) / (1 << 30)
	}

	m := modelMemoryModel{
		mode:         mode,
		value:        ti,
		totalRAMGB:   total,
		localLoading: true,
		width:        80,
		height:       24,
	}
	// Seed both per-mode slots from saved config / defaults so switching modes
	// always shows a sensible value, and the active mode's slot drives the input.
	m.ramValue = m.valueForMode(cfg, mmModeRAM)
	m.ctxValue = m.valueForMode(cfg, mmModeContext)
	m.value.SetValue(m.currentValue())
	m.fresh = true
	return m
}

// valueForMode returns the initial input string for a given mode, preferring a
// saved config value and falling back to a sensible default.
func (m modelMemoryModel) valueForMode(cfg config.Config, mode int) string {
	switch mode {
	case mmModeRAM:
		if cfg.ModelMemory.MaxRAMGB > 0 {
			return trimFloat(cfg.ModelMemory.MaxRAMGB)
		}
		if m.totalRAMGB > 0 {
			return trimFloat(roundHalf(m.totalRAMGB * 0.7))
		}
	case mmModeContext:
		if cfg.ModelMemory.MaxContextTokens > 0 {
			return strconv.Itoa(cfg.ModelMemory.MaxContextTokens)
		}
		return "8192"
	}
	return ""
}

// currentValue returns the stored value for the active mode.
func (m modelMemoryModel) currentValue() string {
	switch m.mode {
	case mmModeRAM:
		return m.ramValue
	case mmModeContext:
		return m.ctxValue
	}
	return ""
}

func (m modelMemoryModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, fetchLocalModelsCmd())
}

func (m modelMemoryModel) Update(msg tea.Msg) (modelMemoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case localModelsMsg:
		m.localModels = msg.models
		m.localLoading = false
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return modelMemoryCancelledMsg{} }
		case "left", "h":
			m.stashValue()
			m.mode = (m.mode + len(mmModeKeys) - 1) % len(mmModeKeys)
			m.value.SetValue(m.currentValue())
			m.fresh = true
			return m, nil
		case "right", "l":
			m.stashValue()
			m.mode = (m.mode + 1) % len(mmModeKeys)
			m.value.SetValue(m.currentValue())
			m.fresh = true
			return m, nil
		case "enter", "ctrl+s":
			m.stashValue()
			return m, func() tea.Msg { return modelMemoryDoneMsg{cfg: m.result()} }
		}
	}
	if m.mode == mmModeOff {
		return m, nil
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		if m.fresh && km.Type == tea.KeyRunes {
			m.value.SetValue("")
		}
		m.fresh = false
	}
	var cmd tea.Cmd
	m.value, cmd = m.value.Update(msg)
	m.value.SetValue(digitsOnly(m.value.Value(), m.mode == mmModeRAM))
	m.stashValue()
	return m, cmd
}

// stashValue saves the current input text into the slot for the active mode so
// it survives mode toggles.
func (m *modelMemoryModel) stashValue() {
	switch m.mode {
	case mmModeRAM:
		m.ramValue = m.value.Value()
	case mmModeContext:
		m.ctxValue = m.value.Value()
	}
}

func (m modelMemoryModel) result() config.ModelMemoryConfig {
	cfg := config.ModelMemoryConfig{Mode: mmModeKeys[m.mode]}
	switch m.mode {
	case mmModeRAM:
		if gb, err := strconv.ParseFloat(strings.TrimSpace(m.value.Value()), 64); err == nil {
			cfg.MaxRAMGB = gb
		}
	case mmModeContext:
		if n, err := strconv.Atoi(strings.TrimSpace(m.value.Value())); err == nil {
			cfg.MaxContextTokens = n
		}
	}
	return cfg
}

func (m modelMemoryModel) View() string {
	w := m.width
	iw := max(w-4, 8)
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	lines := []string{ui.Header("decsr · Model memory", "saved to ~/.decsr/config.yaml", w), ""}
	box := func(title string, body []string, focused bool) {
		lines = append(lines, strings.Split(ui.Box(title, body, w, focused), "\n")...)
	}

	mode := wrapDim("", "Cap how much memory local models may use: a RAM budget (converted to a per-agent context window) or a fixed context size.", iw, ui.Muted)
	mode = append(mode, "")
	var tabs []string
	for i, label := range mmModeLabels {
		if i == m.mode {
			tabs = append(tabs, lipgloss.NewStyle().Foreground(ui.Accent).Render("▌")+lipgloss.NewStyle().Foreground(ui.Text).Bold(true).Render(label)+lipgloss.NewStyle().Foreground(ui.Accent).Render("▐"))
		} else {
			tabs = append(tabs, lipgloss.NewStyle().Foreground(ui.Text).Render(label))
		}
	}
	mode = append(mode, wrapItems(tabs, 0, max(iw, 20), "  ")...)
	if m.mode == mmModeOff {
		mode = append(mode, "", dim.Render("Local models run with their own default context window."))
	}
	box("MODE", mode, true)

	switch m.mode {
	case mmModeRAM:
		label := "MAX RAM (GB)"
		if m.totalRAMGB > 0 {
			label += fmt.Sprintf(" · SYSTEM TOTAL %.1f GB", m.totalRAMGB)
		}
		box(label, strings.Split(ui.InputBox(m.value.View(), "", min(24, max(iw, 5)), true, ""), "\n"), false)
		if p := m.renderPreview(iw); len(p) > 0 {
			title := "ESTIMATED CONTEXT PER LOCAL MODEL"
			box(title, p, false)
		}
	case mmModeContext:
		box("MAX CONTEXT (TOKENS)", append(strings.Split(ui.InputBox(m.value.View(), "", min(24, max(iw, 5)), true, ""), "\n"), "", dim.Render("Applied verbatim to every agent.")), false)
	}

	body := lines
	if h := m.height - 1; h > 0 && len(body) > h {
		body = body[:h]
	}
	for len(body) < max(m.height-1, len(body)) {
		body = append(body, "")
	}
	for i, l := range body {
		body[i] = ansi.Truncate(l, w, "")
	}
	hints := ui.HintBar([]ui.Hint{{Key: "←→", Label: "mode"}, {Key: "Enter", Label: "save"}, {Key: "Esc", Label: "cancel"}}, w)
	return strings.Join(append(body, hints), "\n")
}

func indentLines(block, prefix string) []string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return lines
}

// renderPreview lists the estimated context window the RAM limit yields for
// every local model currently available across backends, so the user sees the
// effect on each model before saving.
func (m modelMemoryModel) renderPreview(w int) []string {
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	gb, err := strconv.ParseFloat(strings.TrimSpace(m.value.Value()), 64)
	if err != nil || gb <= 0 {
		return wrapDim("", "Enter a RAM budget to preview the derived context size.", w, ui.Muted)
	}
	if m.localLoading {
		return []string{dim.Render("scanning local backends…")}
	}
	if len(m.localModels) == 0 {
		return wrapDim("", "No local models detected (Ollama / LM Studio / oMLX not running?). Heuristic for a typical 7B model: ≈ "+
			strconv.Itoa(runner.ContextTokensForWeights(0, gb))+" tokens.", w, ui.Muted)
	}

	var lines []string
	// Cap the list so the screen does not overflow; note any remainder.
	maxRows := max(m.height-14, 4)
	shown := m.localModels
	extra := 0
	if len(shown) > maxRows {
		extra = len(shown) - maxRows
		shown = shown[:maxRows]
	}
	rowW := min(w, 72)
	for _, lm := range shown {
		tokens := runner.ContextTokensForWeights(lm.WeightBytes, gb)
		est := ""
		if lm.WeightBytes <= 0 {
			est = " (est)"
		}
		lines = append(lines, ui.ListRow(ui.RowOpts{
			Left: fmt.Sprintf("%-9s %s", lm.Runner, lm.Model), Right: fmt.Sprintf("≈ %d tok%s", tokens, est), Width: rowW, Indent: 1,
		}))
	}
	if extra > 0 {
		lines = append(lines, "  "+dim.Render(fmt.Sprintf("… and %d more", extra)))
	}
	return lines
}

func digitsOnly(s string, allowDot bool) string {
	var b strings.Builder
	seenDot := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if allowDot && r == '.' && !seenDot {
			seenDot = true
			b.WriteRune(r)
		}
	}
	return b.String()
}

func trimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(strconv.FormatFloat(f, 'f', 1, 64), "0"), ".")
}

func roundHalf(f float64) float64 {
	return float64(int(f*2+0.5)) / 2
}
