package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// ── /provider ────────────────────────────────────────────────────────────────

func (m workspaceModel) openProviderPanel() (tea.Model, tea.Cmd) {
	return m.openPanel(panelProvider, 0), nil
}

// selectedProvider is the row under the cursor.
func (m workspaceModel) selectedProvider() (provRow, bool) {
	rows := providerRows()
	if len(rows) == 0 {
		return provRow{}, false
	}
	return rows[min(max(m.panel.sel, 0), len(rows)-1)], true
}

func (m workspaceModel) providerKey(k string) (workspaceModel, tea.Cmd, bool) {
	n := len(providerRows())
	switch k {
	case "up", "k":
		m.panel.sel = max(m.panel.sel-1, 0)
	case "down", "j":
		m.panel.sel = min(m.panel.sel+1, max(n-1, 0))
	case "a":
		next, cmd := m.cmdConnect("")
		return next.(workspaceModel), cmd, true
	case "x":
		if r, ok := m.selectedProvider(); ok {
			if !r.saved {
				m.panel.note, m.panel.noteKind = r.name+" comes from the environment: unset "+r.source[len("env "):]+" to remove it", ui.ResultWarn
				return m, nil, true
			}
			next, cmd := m.askRemoveProvider(r.name)
			m = next.(workspaceModel)
			m.confirm.back = wsOverlayPanel
			return m, cmd, true
		}
	case "t":
		if r, ok := m.selectedProvider(); ok {
			m.panel.note = ""
			next, cmd := m.testProvider(r.name)
			m = next.(workspaceModel)
			m.panel.note, m.panel.noteKind = "testing the key of "+r.name+"…", ui.ResultInfo
			return m, cmd, true
		}
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m workspaceModel) providerOpts(o *ui.PanelOpts) {
	o.Title = "Providers"
	o.Keys = hintsFor(false, ui.Hint{Key: "a", Label: "add"}, ui.Hint{Key: "x", Label: "remove"}, ui.Hint{Key: "t", Label: "test"})
	rows := providerRows()
	if len(rows) == 0 {
		o.Body = []string{dimText("no providers connected"), "", dimText("a adds one: deepseek, openai, openrouter, groq or custom")}
		return
	}
	hc := lipgloss.NewStyle().Foreground(o.Color).Bold(true)
	o.Body = []string{"  " + hc.Render(padRight("PROVIDER", 14)+padRight("KEY", 14)+"SOURCE")}
	for i, r := range rows {
		sel := i == m.panel.sel
		key := r.masked
		if key == "" {
			key = "—"
		}
		name := plainText(padRight(r.name, 14))
		if sel {
			name = lipgloss.NewStyle().Foreground(ui.Text).Bold(true).Render(padRight(r.name, 14))
		}
		o.Body = append(o.Body, selMark(sel, o.Color)+name+dimText(padRight(key, 14)+r.source))
	}
	followSel(o, m.panel.sel+1)
}

// ── /config ──────────────────────────────────────────────────────────────────

func (m workspaceModel) openConfigPanel() (tea.Model, tea.Cmd) {
	return m.openPanel(panelConfig, 0), nil
}

func (m workspaceModel) selectedSetting() (config.Setting, bool) {
	all := config.Settings()
	if len(all) == 0 {
		return config.Setting{}, false
	}
	return all[min(max(m.panel.sel, 0), len(all)-1)], true
}

func (m workspaceModel) configKey(k string) (workspaceModel, tea.Cmd, bool) {
	n := len(config.Settings())
	switch k {
	case "up", "k":
		m.panel.sel = max(m.panel.sel-1, 0)
	case "down", "j":
		m.panel.sel = min(m.panel.sel+1, max(n-1, 0))
	case "pgup":
		m.panel.sel = max(m.panel.sel-10, 0)
	case "pgdown":
		m.panel.sel = min(m.panel.sel+10, max(n-1, 0))
	case "enter":
		if s, ok := m.selectedSetting(); ok {
			return m.startInput(s.Get(m.cfg)), nil, true
		}
	default:
		return m, nil, false
	}
	return m, nil, true
}

// applyConfigInput saves the typed value with the checks of /config set; the
// answer of that command is shown as the note of the panel.
func (m workspaceModel) applyConfigInput(val string) (tea.Model, tea.Cmd) {
	s, ok := m.selectedSetting()
	if !ok {
		return m, nil
	}
	if val == "" && s.Get(m.cfg) == "" {
		return m, nil
	}
	if val == "" {
		val = `""`
	}
	next, cmd := m.setConfig(s.Key, val)
	m = next.(workspaceModel)
	lines := m.sess.snapshot().Transcript
	if len(lines) > 0 {
		l := lines[len(lines)-1]
		kind := ui.ResultOK
		if l.Who == "error" {
			kind = ui.ResultError
		}
		m.panel.note, m.panel.noteKind = l.Text, kind
	}
	return m, cmd
}

func (m workspaceModel) configOpts(o *ui.PanelOpts) {
	o.Title = "Settings"
	o.Keys = hintsFor(false, ui.Hint{Key: "↑↓", Label: "move"}, ui.Hint{Key: "Enter", Label: "edit"})
	inner := m.panelInner(o.Width)
	all := config.Settings()
	keyW, valW := 0, 8
	for _, s := range all {
		keyW = max(keyW, len(s.Key))
		valW = max(valW, len([]rune(showSetting(s.Get(m.cfg)))))
	}
	valW = min(valW, 18)
	keyW = min(keyW, 30)
	last, selLine := "", 0
	for i, s := range all {
		if s.Section != last {
			if last != "" {
				o.Body = append(o.Body, "")
			}
			o.Body = append(o.Body, sectionHead(ui.GroupNameProject, s.Section))
			last = s.Section
		}
		sel := i == m.panel.sel
		if sel {
			selLine = len(o.Body)
		}
		val := showSetting(s.Get(m.cfg))
		keySt := lipgloss.NewStyle().Foreground(ui.Text).Bold(sel)
		row := selMark(sel, o.Color) + keySt.Render(padRight(s.Key, keyW)) + "  " + plainText(padRight(truncateOneLine(val, valW), valW)) + "  " + dimText(s.Help)
		o.Body = append(o.Body, clipRow(row, inner))
	}
	if m.panel.inputing {
		if s, ok := m.selectedSetting(); ok {
			o.Body = append(o.Body, "", m.panelInputLine(fmt.Sprintf("%s (%s):", s.Key, s.Hint())))
			o.Keys = []ui.Hint{{Key: "Enter", Label: "save"}, {Key: "Esc", Label: "cancel"}}
		}
	}
	followSel(o, selLine)
	if m.panel.inputing {
		followSel(o, len(o.Body)-1)
	}
}
