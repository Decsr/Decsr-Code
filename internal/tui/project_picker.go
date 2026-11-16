package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// ProjectSelectedMsg is sent when user picks a project directory.
type ProjectSelectedMsg struct {
	Path string
}

// ProjectPickerCancelledMsg is sent when user cancels the project picker.
type ProjectPickerCancelledMsg struct{}

type projectPickerMode int

const (
	projectPickerModeList   projectPickerMode = iota
	projectPickerModeBrowse                   // filesystem directory browser
)

// ProjectPickerModel shows the project selection screen with current directory,
// an "Open project…" browser, and a list of recently opened projects.
type ProjectPickerModel struct {
	projects   []RecentProject
	cursor     int
	currentDir string
	confirmDel int // index in projects pending deletion, -1 = none
	mode       projectPickerMode
	dirBrowser DirBrowserModel
	width      int
	height     int
}

// NewProjectPicker creates the project selection screen.
func NewProjectPicker(currentDir string) ProjectPickerModel {
	return ProjectPickerModel{
		projects:   LoadRecentProjects(),
		currentDir: currentDir,
		confirmDel: -1,
		width:      80,
		height:     24,
	}
}

func (m ProjectPickerModel) Init() tea.Cmd { return nil }

// showCurrentDir returns true when the current directory is a valid
// project directory (i.e. not the user's home directory).
func (m ProjectPickerModel) showCurrentDir() bool {
	return !isHomeDir(m.currentDir)
}

// totalItems returns the number of selectable rows.
// When the current dir is the home directory, it is hidden.
func (m ProjectPickerModel) totalItems() int {
	n := 1 + len(m.projects) // "Open project…" + recent
	if m.showCurrentDir() {
		n++ // "Use current directory"
	}
	return n
}

// itemIndex maps a cursor position to a logical item.
// Returns: "current", "browse", or 0-based recent project index.
func (m ProjectPickerModel) itemAt(cursor int) (kind string, recentIdx int) {
	i := 0
	if m.showCurrentDir() {
		if cursor == i {
			return "current", -1
		}
		i++
	}
	if cursor == i {
		return "browse", -1
	}
	i++
	return "recent", cursor - i
}

func (m ProjectPickerModel) Update(msg tea.Msg) (ProjectPickerModel, tea.Cmd) {
	// Directory browser sub-mode.
	if m.mode == projectPickerModeBrowse {
		switch msg := msg.(type) {
		case DirSelectedMsg:
			return m, func() tea.Msg { return ProjectSelectedMsg(msg) }
		case DirBrowserCancelledMsg:
			m.mode = projectPickerModeList
			return m, nil
		}
		var cmd tea.Cmd
		m.dirBrowser, cmd = m.dirBrowser.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		// Deletion confirmation intercepts all keys.
		if m.confirmDel >= 0 {
			switch msg.String() {
			case "y", "Y":
				if m.confirmDel < len(m.projects) {
					_ = RemoveRecentProject(m.projects[m.confirmDel].Path)
					m.projects = LoadRecentProjects()
					if m.cursor >= m.totalItems() {
						m.cursor = m.totalItems() - 1
					}
					if m.cursor < 0 {
						m.cursor = 0
					}
				}
				m.confirmDel = -1
			default:
				m.confirmDel = -1
			}
			return m, nil
		}

		total := m.totalItems()

		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < total-1 {
				m.cursor++
			}
		case "enter", " ":
			kind, recentIdx := m.itemAt(m.cursor)
			switch kind {
			case "current":
				dir := m.currentDir
				return m, func() tea.Msg { return ProjectSelectedMsg{Path: dir} }
			case "browse":
				browser, err := NewDirBrowser()
				if err == nil {
					browser.SetSize(m.width, m.height)
					m.dirBrowser = browser
					m.mode = projectPickerModeBrowse
				}
				return m, nil
			case "recent":
				if recentIdx >= 0 && recentIdx < len(m.projects) {
					proj := m.projects[recentIdx]
					if dirExists(proj.Path) {
						path := proj.Path
						return m, func() tea.Msg { return ProjectSelectedMsg{Path: path} }
					}
				}
			}
		case "d", "D", "delete", "backspace":
			kind, recentIdx := m.itemAt(m.cursor)
			if kind == "recent" && recentIdx >= 0 && recentIdx < len(m.projects) {
				m.confirmDel = recentIdx
			}
		case "q", "Q", "esc":
			return m, func() tea.Msg { return ProjectPickerCancelledMsg{} }
		}
	}
	return m, nil
}

func (m ProjectPickerModel) View() string {
	if m.mode == projectPickerModeBrowse {
		return m.dirBrowser.View()
	}
	w := max(m.width, 1)
	lines := []string{ui.Header("decsr · select project", "", w), ""}
	sel := 0
	row := func(left, right string, selected bool) {
		if selected {
			sel = len(lines)
		}
		lines = append(lines, ui.ListRow(ui.RowOpts{Left: left, Right: right, Width: w, Selected: selected, Indent: 1}))
	}
	kind, recentIdx := m.itemAt(m.cursor)

	lines = append(lines, ui.PaneLabel("Start", false, w))
	if m.showCurrentDir() {
		row("Use current directory", ui.TruncateMiddle(tildePath(m.currentDir), max(w/2, 8)), kind == "current")
	}
	row("Open project…", "browse the filesystem", kind == "browse")
	lines = append(lines, "")

	lines = append(lines, ui.PaneLabel("Recent projects", false, w))
	if len(m.projects) == 0 {
		lines = append(lines, dim("   no recent projects"))
	}
	for i, proj := range m.projects {
		if m.confirmDel == i {
			lines = append(lines, lipgloss.NewStyle().Foreground(ui.Fail).Render(ui.Truncate(fmt.Sprintf("   Remove %s? (y/N)", proj.Name), w)))
			continue
		}
		name := ui.Truncate(proj.Name, max((w-2)/2, 4))
		meta := ui.TruncateMiddle(tildePath(proj.Path), max(w-4-ansi.StringWidth(name), 0))
		if !dirExists(proj.Path) {
			meta = "not found"
		}
		row(name, meta, kind == "recent" && recentIdx == i)
	}

	// Keep the selected row on screen when the list is longer than the screen.
	if bodyH := max(m.height, 4) - 3; len(lines)-2 > bodyH {
		start := 2
		if sel-2 >= bodyH {
			start = sel - bodyH + 1
		}
		start = min(start, len(lines)-bodyH)
		lines = append(lines[:2:2], lines[start:start+bodyH]...)
	}
	for len(lines) < max(m.height, 4)-1 {
		lines = append(lines, "")
	}
	lines = append(lines, ui.HintBar([]ui.Hint{{Key: "↑↓", Label: "move"}, {Key: "Enter", Label: "select"}, {Key: "d", Label: "remove"}, {Key: "Esc", Label: "back"}}, w))
	return strings.Join(lines, "\n")
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
