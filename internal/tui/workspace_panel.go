package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/doctor"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// The panels of the slash commands: one overlay, one kind per command.
const (
	panelUsage    = "usage"
	panelStatus   = "status"
	panelModel    = "model"
	panelProvider = "provider"
	panelConfig   = "config"
	panelContext  = "context"
	panelDoctor   = "doctor"
	panelMemory   = "memory"
	panelPlugin   = "plugin"
)

// wsPanel is the state of the open panel. Only the fields of its kind are used.
type wsPanel struct {
	kind string
	ps   ui.PanelState
	sel  int // cursor row of a list panel

	// note is the result line at the foot of the body; noteKind is a ui.Result*.
	note, noteKind string

	// text entry (budget amount, config value)
	inputing bool

	// /usage
	perNode bool
	hist    *usageHist
	limits  []limitRow
	loaded  bool

	// /status
	probe []frEntry
	docs  []doctor.Result
	// docsDone is set once the doctor checks ended (also used by /doctor).
	docsDone bool

	// /model
	edit    map[string]config.AgentConfig // pending role edits, by role
	editing bool
	field   int

	// /context
	ctx *ctxData

	// /plugin
	pdata   *pluginData
	markets []plugins.Marketplace

	// /memory
	memTitle string
	memLines []string
	memBusy  bool
}

// tabsOf are the tabs of a panel kind.
func tabsOf(kind string) []string {
	switch kind {
	case panelUsage:
		return []string{"Session", "Budget", "History", "Limits"}
	case panelModel:
		return []string{"Roles", "Rankings", "Profiles"}
	case panelPlugin:
		return []string{"Installed", "Discover", "Marketplaces", "Stats"}
	}
	return nil
}

func groupOfPanel(kind string) string {
	switch kind {
	case panelUsage, panelStatus, panelContext:
		return ui.GroupNameUsage
	case panelModel:
		return ui.GroupNameAgents
	}
	return ui.GroupNameProject
}

// openPanel shows a panel of kind, starting from a clean state.
func (m workspaceModel) openPanel(kind string, tab int) workspaceModel {
	m.panel = wsPanel{kind: kind}
	m.panel.ps.Tab = tab
	m.panelIn.Blur()
	m.overlay = wsOverlayPanel
	return m
}

func (m workspaceModel) closePanel() workspaceModel {
	m.panel = wsPanel{}
	m.panelIn.Blur()
	m.overlay = wsOverlayNone
	return m
}

// panelOpts builds the Panel of the open kind.
func (m workspaceModel) panelOpts() ui.PanelOpts {
	p := m.panel
	o := ui.PanelOpts{Color: ui.GroupColor(groupOfPanel(p.kind)), Tabs: tabsOf(p.kind), Active: p.ps.Tab,
		Scroll: p.ps.Scroll, ScreenW: m.width, ScreenH: m.height}
	if p.note != "" {
		o.Foot = []string{"", ui.ResultLine(p.noteKind, p.note)}
	}
	switch p.kind {
	case panelUsage:
		m.usageOpts(&o)
	case panelStatus:
		m.statusOpts(&o)
	case panelModel:
		m.modelOpts(&o)
	case panelProvider:
		m.providerOpts(&o)
	case panelConfig:
		m.configOpts(&o)
	case panelContext:
		m.contextOpts(&o)
	case panelDoctor:
		m.doctorOpts(&o)
	case panelMemory:
		m.memoryOpts(&o)
	case panelPlugin:
		m.pluginOpts(&o)
	}
	return o
}

func (m workspaceModel) panelBox() string { return ui.Panel(m.panelOpts()) }

// followSel scrolls o so that body line sel stays in view.
func followSel(o *ui.PanelOpts, sel int) {
	rows := ui.PanelRows(*o)
	if sel >= rows {
		o.Scroll = sel - rows + 1
	} else {
		o.Scroll = 0
	}
}

// selMark is the two-cell cursor of a list row.
func selMark(sel bool, c lipgloss.TerminalColor) string {
	if !sel {
		return "  "
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render("› ")
}

func dimText(s string) string   { return lipgloss.NewStyle().Foreground(ui.Muted).Render(s) }
func plainText(s string) string { return lipgloss.NewStyle().Foreground(ui.Text).Render(s) }

// glyphLine is a result glyph and text, for the status and doctor lines.
func glyphLine(kind, text string) string { return ui.ResultLine(kind, text) }

// sectionHead is a small heading in the colour of the panel.
func sectionHead(group, title string) string {
	return lipgloss.NewStyle().Foreground(ui.GroupColor(group)).Bold(true).Render(title)
}

// panelKey routes a key to the open panel.
func (m workspaceModel) panelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.panel.inputing {
		return m.panelInputKey(msg)
	}
	var handled bool
	var cmd tea.Cmd
	switch m.panel.kind {
	case panelUsage:
		m, cmd, handled = m.usageKey(k)
	case panelModel:
		m, cmd, handled = m.modelKey(k)
	case panelProvider:
		m, cmd, handled = m.providerKey(k)
	case panelConfig:
		m, cmd, handled = m.configKey(k)
	case panelPlugin:
		m, cmd, handled = m.pluginKey(msg)
	}
	if handled {
		return m, cmd
	}
	switch k {
	case "esc", "q":
		return m.closePanel(), nil
	}
	m.panel.ps.Key(k, len(tabsOf(m.panel.kind)))
	o := m.panelOpts()
	m.panel.ps.Scroll = ui.PanelWindow(m.panel.ps.Scroll, len(o.Body), ui.PanelRows(o))
	return m, nil
}

// startInput opens the one-line entry of the panel with value; the prompt line
// is drawn by panelInputLine.
func (m workspaceModel) startInput(value string) workspaceModel {
	m.panel.inputing = true
	m.panel.note = ""
	m.panelIn.SetValue(value)
	m.panelIn.CursorEnd()
	m.panelIn.Focus()
	return m
}

func (m workspaceModel) stopInput() workspaceModel {
	m.panel.inputing = false
	m.panelIn.Blur()
	return m
}

// panelInputLine is the entry line: a label and the text typed so far.
func (m workspaceModel) panelInputLine(label string) string {
	return dimText(label+" ") + m.panelIn.View()
}

// panelInputKey handles the keys of the entry line: Enter applies, Esc cancels.
func (m workspaceModel) panelInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.stopInput(), nil
	case "enter":
		val := strings.TrimSpace(m.panelIn.Value())
		m = m.stopInput()
		switch m.panel.kind {
		case panelUsage:
			return m.applyBudgetInput(val)
		case panelConfig:
			return m.applyConfigInput(val)
		case panelPlugin:
			return m.applyPluginInput(val)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.panelIn, cmd = m.panelIn.Update(msg)
	return m, cmd
}

// hintsFor returns the footer keys, with the tab key first when there are tabs.
func hintsFor(tabs bool, rest ...ui.Hint) []ui.Hint {
	var out []ui.Hint
	if tabs {
		out = append(out, ui.Hint{Key: "Tab", Label: "next"})
	}
	out = append(out, rest...)
	return append(out, ui.Hint{Key: "Esc", Label: "close"})
}

// clipRow cuts a styled row to w cells.
func clipRow(s string, w int) string { return ansi.Truncate(s, max(w, 1), "…") }

// framed draws a list or info overlay in the fixed Panel frame. A sel of 0 or
// more keeps that body row in view; otherwise scroll is the first row. foot is
// "key label · key label": it becomes the key bar in the bottom border.
func framed(title string, color lipgloss.TerminalColor, body []string, sel, scroll int, foot string, w, h int) string {
	o := ui.PanelOpts{Title: title, Color: color, Body: body, Scroll: scroll, ScreenW: w, ScreenH: h, Keys: footHints(foot)}
	if sel >= 0 {
		followSel(&o, sel)
	}
	return ui.Panel(o)
}

// footHints splits "↑↓ move · Esc close" into key hints.
func footHints(foot string) []ui.Hint {
	var out []ui.Hint
	for _, p := range strings.Split(foot, " · ") {
		k, l, _ := strings.Cut(strings.TrimSpace(p), " ")
		if k != "" {
			out = append(out, ui.Hint{Key: k, Label: l})
		}
	}
	return out
}
