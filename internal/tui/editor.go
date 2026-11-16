package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// styleTextarea gives a textarea the shared look: no prompt, no cursor-line
// highlight, Text for input and Muted for the placeholder and line numbers.
func styleTextarea(ta *textarea.Model) {
	ta.Prompt = ""
	for _, st := range []*textarea.Style{&ta.FocusedStyle, &ta.BlurredStyle} {
		st.Base = lipgloss.NewStyle()
		st.CursorLine = lipgloss.NewStyle()
		st.CursorLineNumber = lipgloss.NewStyle().Foreground(ui.Muted)
		st.LineNumber = lipgloss.NewStyle().Foreground(ui.Muted)
		st.EndOfBuffer = lipgloss.NewStyle().Foreground(ui.Muted)
		st.Text = lipgloss.NewStyle().Foreground(ui.Text)
		st.Placeholder = lipgloss.NewStyle().Foreground(ui.Muted)
	}
}

// EditorModel is a full-screen requirements editor using an embedded textarea.
type EditorModel struct {
	ta          textarea.Model
	width       int
	height      int
	confirmExit bool // true when waiting for save/discard confirmation
}

func NewEditor(initialContent string) EditorModel {
	ta := textarea.New()
	styleTextarea(&ta)
	ta.Placeholder = "# Task\nDescribe what you want to implement…"
	ta.SetValue(initialContent)
	ta.Focus()
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(20)

	return EditorModel{ta: ta, width: 80, height: 24}
}

func (m EditorModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m EditorModel) Update(msg tea.Msg) (EditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ta.SetWidth(msg.Width - 4)
		m.ta.SetHeight(msg.Height - 3)
		return m, nil

	case tea.KeyMsg:
		if m.confirmExit {
			switch msg.String() {
			case "ctrl+s", "s":
				content := m.ta.Value()
				m.confirmExit = false
				return m, func() tea.Msg { return RequirementsSavedMsg{Path: content} }
			case "d":
				m.confirmExit = false
				return m, func() tea.Msg { return EditorCancelledMsg{} }
			case "esc":
				m.confirmExit = false
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+s":
			content := m.ta.Value()
			return m, func() tea.Msg {
				return RequirementsSavedMsg{Path: content}
			}
		case "esc":
			if strings.TrimSpace(m.ta.Value()) != "" {
				m.confirmExit = true
				return m, nil
			}
			return m, func() tea.Msg { return EditorCancelledMsg{} }
		}
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *EditorModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.ta.SetWidth(w - 4)
	m.ta.SetHeight(h - 3)
}

func (m EditorModel) View() string {
	w := m.width
	var bottom string
	if m.confirmExit {
		bottom = "  " + lipgloss.NewStyle().Bold(true).Foreground(ui.Warn).Render("Unsaved changes: S save & start · D discard · Esc back to editing")
	} else {
		bottom = ui.HintBar([]ui.Hint{{Key: "Ctrl+S", Label: "save & start"}, {Key: "Esc", Label: "back"}}, w)
	}

	body := strings.Split(m.ta.View(), "\n")
	for i, l := range body {
		body[i] = ui.Truncate("  "+l, w)
	}
	lines := []string{ui.Header("Requirements editor", "", w), ""}
	lines = append(lines, body...)
	lines = append(lines, ui.Truncate(bottom, w))
	return strings.Join(lines, "\n")
}
