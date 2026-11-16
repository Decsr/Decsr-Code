package tui

import (
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

type pickerMode int

const (
	pickerModeStart    pickerMode = iota
	pickerModeFileTree pickerMode = iota
)

type pickerItem struct {
	label    string
	path     string
	date     string
	isNew    bool
	isPicker bool
}

// PickerModel is the start screen for selecting requirements.
type PickerModel struct {
	items   []pickerItem
	cursor  int
	mode    pickerMode
	tree    FileTreeModel
	treeErr string
	root    string
	wsPath  string
	width   int
	height  int
}

func NewPicker(root, wsPath string) PickerModel {
	items := []pickerItem{
		{label: "New — open editor", isNew: true},
		{label: "Pick file from repo…", isPicker: true},
	}

	for _, path := range LoadHistory(wsPath) {
		items = append(items, pickerItem{
			label: "Recent: " + path,
			path:  path,
			date:  time.Now().Format("2006-01-02"), // placeholder; history has no timestamps
		})
	}

	return PickerModel{
		items:  items,
		root:   root,
		wsPath: wsPath,
		width:  80,
		height: 20,
	}
}

func (m PickerModel) Init() tea.Cmd { return nil }

func (m PickerModel) Update(msg tea.Msg) (PickerModel, tea.Cmd) {
	if m.mode == pickerModeFileTree {
		var cmd tea.Cmd
		m.tree, cmd = m.tree.Update(msg)

		// Handle tree selection.
		switch msg := msg.(type) {
		case FileSelectedMsg:
			_ = SaveHistory(m.wsPath, msg.Path)
			return m, func() tea.Msg {
				return PickerSelectedMsg{Path: msg.Path}
			}
		case EditorCancelledMsg:
			m.mode = pickerModeStart
		}
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			item := m.items[m.cursor]
			switch {
			case item.isNew:
				return m, func() tea.Msg { return PickerSelectedMsg{IsNew: true} }
			case item.isPicker:
				tree, err := NewFileTree(m.root)
				if err != nil {
					m.treeErr = err.Error()
				} else {
					tree.SetSize(m.width, m.height)
					m.tree = tree
					m.mode = pickerModeFileTree
				}
			default:
				_ = SaveHistory(m.wsPath, item.path)
				return m, func() tea.Msg { return PickerSelectedMsg{Path: item.path} }
			}
		case "esc", "q":
			return m, func() tea.Msg { return PickerCancelledMsg{} }
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.tree.SetSize(msg.Width, msg.Height)
	}
	return m, nil
}

func (m PickerModel) View() string {
	if m.mode == pickerModeFileTree {
		return m.tree.View()
	}
	w, h := max(m.width, 20), max(m.height, 6)
	dim := lipgloss.NewStyle().Foreground(ui.Muted)

	lines := []string{ui.PaneLabel("Requirements source", true, w), ""}
	for i, item := range m.items {
		var label, hint string
		switch {
		case item.isNew:
			label, hint = "+ New — open editor", "Create requirements from scratch"
		case item.isPicker:
			label, hint = "▸ Pick file from repo…", "Browse repository files"
		default:
			label, hint = "◇ "+m.shortenRecentPath(item.path), "Re-use previous requirements"
		}
		lines = append(lines, "  "+ui.ListRow(ui.RowOpts{Left: label, Right: item.date, Width: max(w-4, 10), Selected: i == m.cursor, Indent: 1}))
		if i == m.cursor {
			lines = append(lines, "     "+dim.Render(ui.Truncate(hint, max(w-8, 1))))
		}
	}
	if m.treeErr != "" {
		lines = append(lines, "", "  "+lipgloss.NewStyle().Foreground(ui.Fail).Render(ui.Truncate("! "+m.treeErr, max(w-4, 1))))
	}

	out := append([]string{ui.Header("decsr · Requirements · "+filepath.Base(m.root), "", w), ""}, lines...)
	for len(out) < h-1 {
		out = append(out, "")
	}
	out = out[:h-1]
	hints := []ui.Hint{{Key: "↑↓", Label: "navigate"}, {Key: "Enter", Label: "select"}, {Key: "Esc/q", Label: "back"}}
	return strings.Join(append(out, ui.HintBar(hints, w)), "\n")
}

func (m PickerModel) shortenRecentPath(path string) string {
	rel, err := filepath.Rel(m.root, path)
	if err != nil {
		return filepath.Base(path)
	}
	if len(rel) > 45 {
		return "…" + rel[len(rel)-44:]
	}
	return rel
}

func (m *PickerModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}
