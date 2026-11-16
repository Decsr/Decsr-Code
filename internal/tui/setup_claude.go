package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// claudeTypeRow is the last row of the Claude model list; it opens a text
// input for a model id that is not listed.
const claudeTypeRow = "type a model id…"

// claudeRows is the selectable Claude model rows: the known models, a saved id
// that is not among them, and the row that opens the typed input.
func (m SetupModel) claudeRows() []string {
	rows := append([]string(nil), m.claudeModels...)
	if m.claudeCustom != "" && !containsString(rows, m.claudeCustom) {
		rows = append(rows, m.claudeCustom)
	}
	return append(rows, claudeTypeRow)
}

// claudeLabels is the display text and the dim hint of every claudeRows entry.
func (m SetupModel) claudeLabels() (labels, hints []string) {
	rows := m.claudeRows()
	labels = make([]string, len(rows))
	hints = make([]string, len(rows))
	for i, r := range rows {
		labels[i] = r
		switch {
		case r == claudeTypeRow:
		case !containsString(m.claudeModels, r):
			labels[i] = r + " (custom)"
		default:
			hints[i] = runner.ClaudeModelHint(r)
		}
	}
	return labels, hints
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// noteClaudeCustom remembers a model id that is not in the list so it shows as
// a selected custom row.
func (m *SetupModel) noteClaudeCustom(id string) {
	if id != "" && !containsString(m.claudeModels, id) {
		m.claudeCustom = id
	}
}

// claudeDefaultLine says which model an empty setting uses. It is empty when a
// model is set.
func (m SetupModel) claudeDefaultLine(modelEmpty bool) string {
	if !modelEmpty {
		return ""
	}
	if m.claudeDefault == "" {
		return "default → Claude Code's own default"
	}
	return "default → " + m.claudeDefault + " (from ~/.claude/settings.json)"
}

// globalClaudeModelEmpty reports whether the global setting names no model.
func (m SetupModel) globalClaudeModelEmpty() bool { return m.globalModelEmpty }

// agentClaudeModelEmpty reports whether the agent being edited has no model.
func (m SetupModel) agentClaudeModelEmpty() bool {
	if m.agentEditing && m.agentEditStep == 1 && m.agentCursor < len(m.agentRoles) {
		return m.agentOverrides[m.agentRoles[m.agentCursor]].model == ""
	}
	return false
}

// startClaudeTyping switches the model step to the text input.
func (m *SetupModel) startClaudeTyping() {
	m.claudeTyping = true
	m.claudeTypeErr = ""
	m.typed.SetValue("")
	m.typed.Focus()
}

// claudeTypedKey handles a key while a Claude model id is typed. It reports
// true when Enter accepted a valid id; idx is then the row of that id.
func (m *SetupModel) claudeTypedKey(msg tea.KeyMsg, idx *int) (bool, tea.Cmd) {
	if msg.String() != "enter" {
		var cmd tea.Cmd
		m.typed, cmd = m.typed.Update(msg)
		m.claudeTypeErr = ""
		return false, cmd
	}
	id := m.typedModel()
	switch {
	case id == "":
		m.claudeTypeErr = "enter a model id"
		return false, nil
	case strings.ContainsAny(id, " \t"):
		m.claudeTypeErr = "a model id has no spaces"
		return false, nil
	}
	m.noteClaudeCustom(id)
	m.claudeTyping = false
	m.claudeTypeErr = ""
	*idx = findModelIndex(m.claudeRows(), id)
	return true, nil
}

// claudeTypingLines is the typed model id input with its error.
func (m SetupModel) claudeTypingLines(dim lipgloss.Style) []string {
	lines := []string{dim.Render("  type a Claude model id:"), "  " + m.typed.View()}
	if m.claudeTypeErr != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(crt.warn).Render("  "+m.claudeTypeErr))
	}
	return lines
}
