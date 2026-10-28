package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/decsr"
)

// TaskHistoryClosedMsg is emitted when the task history view is dismissed.
type TaskHistoryClosedMsg struct{}

// TaskHistoryModel is a read-only list of completed decsr tasks.
type TaskHistoryModel struct {
	tasks  []decsr.DoneTask
	cursor int
	width  int
	height int
}

// NewTaskHistory creates a history view for the given completed tasks.
func NewTaskHistory(tasks []decsr.DoneTask) TaskHistoryModel {
	return TaskHistoryModel{tasks: tasks, width: 80, height: 24}
}

func (m TaskHistoryModel) Init() tea.Cmd { return nil }

func (m *TaskHistoryModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m TaskHistoryModel) Update(msg tea.Msg) (TaskHistoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "enter":
			return m, func() tea.Msg { return TaskHistoryClosedMsg{} }
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.tasks)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m TaskHistoryModel) View() string {
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	doneStyle := lipgloss.NewStyle().Foreground(galaxyCyan).Bold(true)
	selStyle := lipgloss.NewStyle().Background(surfaceRaised).Foreground(crt.bright).Bold(true)

	var b strings.Builder
	b.WriteString(screenHeader(m.width, "Done Tasks", ""))
	b.WriteString("\n\n")

	if len(m.tasks) == 0 {
		b.WriteString(dimStyle.Render("  No completed tasks yet."))
		b.WriteString("\n\n" + hintBar(m.width, []keyHint{{"Esc", "back"}}, ""))
		return b.String()
	}

	for i, t := range m.tasks {
		if i == m.cursor {
			b.WriteString(lipgloss.NewStyle().Foreground(focusBorder).Bold(true).Render("▌") +
				selStyle.Render(" "+t.Title+"  ("+t.ID+")") + "\n")
		} else {
			b.WriteString("  " + doneStyle.Render("✓ ") + t.Title + " " + dimStyle.Render("("+t.ID+")") + "\n")
		}
	}
	b.WriteString("\n" + hintBar(m.width, []keyHint{{"↑↓", "scroll"}, {"Esc", "back"}}, ""))
	return b.String()
}
