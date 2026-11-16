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

type ftEntry struct {
	name  string
	isDir bool
}

// FileTreeModel is an MC-style single-pane directory navigator.
// Directories are navigable; .md files are selectable.
// Any file can be selected with Ctrl+Enter.
type FileTreeModel struct {
	root    string // repo root (never go above this)
	cwd     string // current directory being displayed
	entries []ftEntry
	cursor  int
	width   int
	height  int
}

func NewFileTree(root string) (FileTreeModel, error) {
	m := FileTreeModel{root: root, cwd: root, width: 80, height: 20}
	if err := m.reload(); err != nil {
		return m, err
	}
	return m, nil
}

// reload reads cwd and populates entries: dirs first (sorted), then files.
func (m *FileTreeModel) reload() error {
	entries, err := os.ReadDir(m.cwd)
	if err != nil {
		return err
	}

	m.entries = nil

	// ".." unless already at root
	if m.cwd != m.root {
		m.entries = append(m.entries, ftEntry{name: "..", isDir: true})
	}

	var dirs, files []ftEntry
	for _, e := range entries {
		name := e.Name()
		// skip common noise directories
		if name == "node_modules" || name == "vendor" {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, ftEntry{name: name + "/", isDir: true})
		} else {
			files = append(files, ftEntry{name: name})
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	m.entries = append(m.entries, dirs...)
	m.entries = append(m.entries, files...)
	m.cursor = 0
	return nil
}

func (m FileTreeModel) Init() tea.Cmd { return nil }

func (m FileTreeModel) Update(msg tea.Msg) (FileTreeModel, tea.Cmd) {
	switch msg := msg.(type) {
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
			if e.isDir {
				next := m.cwdJoin(e.name)
				m.cwd = next
				_ = m.reload()
			} else {
				// select file
				path := filepath.Join(m.cwd, e.name)
				return m, func() tea.Msg { return FileSelectedMsg{Path: path} }
			}

		case "backspace", "left", "h":
			if m.cwd != m.root {
				parent := filepath.Dir(m.cwd)
				if !strings.HasPrefix(parent, m.root) {
					parent = m.root
				}
				prevDir := filepath.Base(m.cwd)
				m.cwd = parent
				_ = m.reload()
				// restore cursor on the dir we came from
				for i, e := range m.entries {
					if strings.TrimSuffix(e.name, "/") == prevDir {
						m.cursor = i
						break
					}
				}
			}

		case "esc":
			return m, func() tea.Msg { return EditorCancelledMsg{} }
		}
	}
	return m, nil
}

func (m FileTreeModel) View() string {
	w, h := max(m.width, 20), max(m.height, 6)
	dim := lipgloss.NewStyle().Foreground(ui.Muted)

	rel, _ := filepath.Rel(m.root, m.cwd)
	if rel == "." || rel == "" {
		rel = "/"
	} else {
		rel = "/" + rel + "/"
	}

	rows := max(h-4, 3)
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	lines := []string{ui.PaneLabel(rel, true, w)}
	for i := start; i < len(m.entries) && i < start+rows-1; i++ {
		e := m.entries[i]
		icon := " "
		switch {
		case e.name == "..":
			icon = "↩"
		case e.isDir:
			icon = "■"
		case strings.HasSuffix(e.name, ".md"):
			icon = "□"
		}
		lines = append(lines, "  "+ui.ListRow(ui.RowOpts{Left: icon + " " + e.name, Width: max(w-4, 10), Selected: i == m.cursor, Indent: 1}))
	}
	if len(m.entries) > rows-1 {
		lines = append(lines, "  "+dim.Render(fmt.Sprintf("%d/%d", m.cursor+1, len(m.entries))))
	}

	out := append([]string{ui.Header("decsr · Browse files · "+filepath.Base(m.root), "", w), ""}, lines...)
	for len(out) < h-1 {
		out = append(out, "")
	}
	out = out[:h-1]
	hints := []ui.Hint{{Key: "↑↓", Label: "navigate"}, {Key: "Enter/→", Label: "open"}, {Key: "←", Label: "back"}, {Key: "PgUp/Dn", Label: "page"}, {Key: "Esc", Label: "cancel"}}
	return strings.Join(append(out, ui.HintBar(hints, w)), "\n")
}

func (m FileTreeModel) visibleRows() int {
	rows := m.height - 5 // header + sep + sep + hints
	if rows < 4 {
		rows = 4
	}
	return rows
}

func (m *FileTreeModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// cwdJoin resolves a relative entry name (possibly "..") against cwd, clamped to root.
func (m *FileTreeModel) cwdJoin(name string) string {
	name = strings.TrimSuffix(name, "/")
	if name == ".." {
		p := filepath.Dir(m.cwd)
		if strings.HasPrefix(p, m.root) {
			return p
		}
		return m.root
	}
	return filepath.Join(m.cwd, name)
}
