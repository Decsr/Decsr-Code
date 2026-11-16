package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// DirSelectedMsg is emitted when a directory is chosen as a project.
type DirSelectedMsg struct {
	Path string
}

// DirBrowserCancelledMsg is emitted when the user cancels browsing.
type DirBrowserCancelledMsg struct{}

type dirEntry struct {
	name      string
	isProject bool
}

var projectMarkers = []string{
	".git", "go.mod", "package.json", "Cargo.toml",
	"pom.xml", "pyproject.toml", "setup.py", "Gemfile",
}

// DirBrowserModel is a filesystem navigator that shows only directories.
// Directories detected as projects (containing .git, go.mod, etc.) are
// highlighted and selectable with Enter. Regular directories are entered.
type DirBrowserModel struct {
	cwd        string
	entries    []dirEntry
	cursor     int
	showHidden bool
	width      int
	height     int
	err        string // last navigation error, shown until the next successful move
}

// NewDirBrowser creates a directory browser starting at $HOME.
func NewDirBrowser() (DirBrowserModel, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	m := DirBrowserModel{cwd: home, width: 80, height: 24}
	if err := m.reload(); err != nil {
		return m, err
	}
	return m, nil
}

func detectProject(path string) bool {
	for _, marker := range projectMarkers {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

func (m *DirBrowserModel) reload() error {
	rawEntries, err := os.ReadDir(m.cwd)
	if err != nil {
		return err
	}

	m.entries = nil

	if m.cwd != "/" {
		m.entries = append(m.entries, dirEntry{name: ".."})
	}

	var dirs []dirEntry
	for _, e := range rawEntries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !m.showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if name == "node_modules" || name == "vendor" || name == "__pycache__" {
			continue
		}
		fullPath := filepath.Join(m.cwd, name)
		dirs = append(dirs, dirEntry{
			name:      name,
			isProject: detectProject(fullPath),
		})
	}

	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].isProject != dirs[j].isProject {
			return dirs[i].isProject
		}
		return dirs[i].name < dirs[j].name
	})

	m.entries = append(m.entries, dirs...)
	m.cursor = 0
	return nil
}

// chdir switches to path and reloads the listing. If the directory cannot be
// read, the previous directory and entries are kept and the error is recorded.
func (m *DirBrowserModel) chdir(path string) bool {
	prev := m.cwd
	m.cwd = path
	if err := m.reload(); err != nil {
		m.cwd = prev
		m.err = err.Error()
		return false
	}
	m.err = ""
	return true
}

func (m DirBrowserModel) Init() tea.Cmd { return nil }

func (m DirBrowserModel) Update(msg tea.Msg) (DirBrowserModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
		case "pgup":
			m.cursor -= m.visibleRows()
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "pgdown":
			m.cursor += m.visibleRows()
			if m.cursor >= len(m.entries) {
				m.cursor = len(m.entries) - 1
			}

		case "enter", "right", "l":
			if len(m.entries) == 0 {
				break
			}
			e := m.entries[m.cursor]
			if e.name == ".." {
				prevDir := filepath.Base(m.cwd)
				if m.chdir(filepath.Dir(m.cwd)) {
					for i, re := range m.entries {
						if re.name == prevDir {
							m.cursor = i
							break
						}
					}
				}
				return m, nil
			}

			fullPath := filepath.Join(m.cwd, e.name)
			if e.isProject {
				return m, func() tea.Msg { return DirSelectedMsg{Path: fullPath} }
			}
			m.chdir(fullPath)

		case "o", "O":
			// Force-select current highlighted directory as project.
			if len(m.entries) == 0 {
				break
			}
			e := m.entries[m.cursor]
			if e.name == ".." {
				// Select current cwd itself.
				cwd := m.cwd
				return m, func() tea.Msg { return DirSelectedMsg{Path: cwd} }
			}
			fullPath := filepath.Join(m.cwd, e.name)
			return m, func() tea.Msg { return DirSelectedMsg{Path: fullPath} }

		case "backspace", "left", "h":
			if m.cwd != "/" {
				prevDir := filepath.Base(m.cwd)
				if m.chdir(filepath.Dir(m.cwd)) {
					for i, re := range m.entries {
						if re.name == prevDir {
							m.cursor = i
							break
						}
					}
				}
			}

		case ".":
			m.showHidden = !m.showHidden
			_ = m.reload()

		case "~":
			if home, err := os.UserHomeDir(); err == nil {
				m.chdir(home)
			}

		case "esc", "q":
			return m, func() tea.Msg { return DirBrowserCancelledMsg{} }
		}
	}
	return m, nil
}

func (m DirBrowserModel) View() string {
	w := max(m.width, 1)
	lines := []string{
		ui.Header("decsr · open project", ui.TruncateMiddle(tildePath(m.cwd), max(w/2, 8)), w),
		"",
		ui.PaneLabel("Directories", false, w),
	}

	rows := m.visibleRows()
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	for i := start; i < len(m.entries) && i < start+rows; i++ {
		e := m.entries[i]
		var left, meta string
		switch {
		case e.name == "..":
			left = "↑ .."
		case e.isProject:
			left, meta = "◆ "+e.name+"/", "project"
		default:
			left = "▪ " + e.name + "/"
		}
		lines = append(lines, ui.ListRow(ui.RowOpts{Left: left, Right: meta, Width: w, Selected: i == m.cursor, Indent: 1}))
	}
	if len(m.entries) > rows {
		lines = append(lines, dim(fmt.Sprintf("   %d/%d", m.cursor+1, len(m.entries))))
	}
	if m.err != "" {
		reason := m.err // "open /path: permission denied" shows its reason
		if i := strings.LastIndex(reason, ": "); i >= 0 {
			reason = reason[i+2:]
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(ui.Fail).Render(ui.Truncate("  cannot open: "+reason, w)))
	}

	enterLabel := "open"
	if len(m.entries) > 0 && m.cursor < len(m.entries) && m.entries[m.cursor].isProject {
		enterLabel = "select"
	}
	// The hint bar sits on the last row of the screen.
	for len(lines) < max(m.height, 4)-1 {
		lines = append(lines, "")
	}
	lines = append(lines, ui.HintBar([]ui.Hint{
		{Key: "↑↓", Label: "move"}, {Key: "Enter", Label: enterLabel}, {Key: "o", Label: "force-select"},
		{Key: ".", Label: "hidden"}, {Key: "~", Label: "home"}, {Key: "←", Label: "back"}, {Key: "Esc", Label: "cancel"},
	}, w))

	return strings.Join(lines, "\n")
}

func (m DirBrowserModel) visibleRows() int {
	return max(m.height-7, 4)
}

// SetSize updates the browser dimensions.
func (m *DirBrowserModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}
