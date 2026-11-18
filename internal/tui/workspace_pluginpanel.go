package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// The tabs of /plugin.
const (
	pluginTabInstalled = iota
	pluginTabDiscover
	pluginTabMarkets
	pluginTabStats
)

// installedRow is a row of the Installed tab: a plugin decsr loads, or a
// Claude Code plugin that is not loaded.
type installedRow struct {
	name, source       string
	enabled, notLoaded bool
	skills, roles, cmd int
	known              bool // the counts are known (not for a plugin that is off and not read yet)
	p                  plugins.Plugin
	c                  plugins.ClaudePlugin
}

// installedRows lists the loaded plugins, then the Claude Code plugins that are
// not loaded.
func installedRows(set plugins.Set, d *pluginData) []installedRow {
	var rows []installedRow
	have := map[string]bool{}
	for _, p := range set.Plugins {
		have[p.Name] = true
		r := installedRow{name: p.Name, source: "decsr", enabled: p.Enabled, p: p, known: true,
			skills: len(p.Skills), roles: len(p.Agents), cmd: len(p.Commands)}
		if !p.Enabled {
			r.known = false
			if d != nil {
				if c, ok := d.counts[p.Name]; ok {
					r.skills, r.roles, r.cmd, r.known = c.skills, c.roles, c.cmds, true
				}
			}
		}
		rows = append(rows, r)
	}
	if d != nil {
		for _, c := range d.claude {
			if !have[c.Name] {
				rows = append(rows, installedRow{name: c.Name, source: "claude", notLoaded: true, known: true, c: c,
					skills: c.Skills, roles: c.Agents, cmd: c.Commands})
			}
		}
	}
	return rows
}

func (m workspaceModel) pluginRowsInstalled() []installedRow {
	return installedRows(m.plug, m.panel.pdata)
}

func (m workspaceModel) pluginRowsDiscover() []discoverRow {
	return filterDiscover(discoverRows(m.panel.markets), m.panel.ps.Filter)
}

func (m workspaceModel) pluginRowCount() int {
	switch m.panel.ps.Tab {
	case pluginTabInstalled:
		return len(m.pluginRowsInstalled())
	case pluginTabDiscover:
		return len(m.pluginRowsDiscover())
	case pluginTabMarkets:
		return len(m.panel.markets)
	}
	return 0
}

// ── keys ─────────────────────────────────────────────────────────────────────

func (m workspaceModel) pluginKey(msg tea.KeyMsg) (workspaceModel, tea.Cmd, bool) {
	k := msg.String()
	m.panel.note = ""
	tab := m.panel.ps.Tab
	if tab == pluginTabDiscover && msg.Type == tea.KeyRunes && !msg.Alt {
		// typed text, one rune or several when the terminal sent them together
		var text []rune
		for _, r := range msg.Runes {
			if r >= ' ' && r != 0x7f {
				text = append(text, r)
			}
		}
		if len(text) > 0 {
			m.panel.ps.TypeFilter(string(text))
			m.panel.sel = 0
		}
		return m, nil, true
	}
	n := m.pluginRowCount()
	typing := tab == pluginTabDiscover
	switch k {
	case "tab", "shift+tab", "left", "right":
		m.panel.sel, m.panel.ps.Filter = 0, ""
		return m, nil, false
	case "up":
		m.panel.sel = max(m.panel.sel-1, 0)
		return m, nil, true
	case "down":
		m.panel.sel = min(m.panel.sel+1, max(n-1, 0))
		return m, nil, true
	case "pgup":
		m.panel.sel = max(m.panel.sel-10, 0)
		return m, nil, true
	case "pgdown":
		m.panel.sel = min(m.panel.sel+10, max(n-1, 0))
		return m, nil, true
	case "k", "j":
		if !typing {
			return m.pluginKey(tea.KeyMsg{Type: map[string]tea.KeyType{"k": tea.KeyUp, "j": tea.KeyDown}[k]})
		}
	}
	switch tab {
	case pluginTabInstalled:
		return m.installedKey(k)
	case pluginTabDiscover:
		return m.discoverKey(k)
	case pluginTabMarkets:
		return m.marketsKey(k)
	}
	return m, nil, false
}

func (m workspaceModel) pluginDo(c pluginCmd) (workspaceModel, tea.Cmd, bool) {
	next, cmd := m.runPlugin(c)
	return next.(workspaceModel), cmd, true
}

func (m workspaceModel) installedKey(k string) (workspaceModel, tea.Cmd, bool) {
	rows := m.pluginRowsInstalled()
	if len(rows) == 0 {
		return m, nil, false
	}
	r := rows[min(max(m.panel.sel, 0), len(rows)-1)]
	switch k {
	case " ", "space":
		if r.notLoaded {
			m.panel.note, m.panel.noteKind = r.name+" is not loaded: i imports it from Claude Code", ui.ResultWarn
			return m, nil, true
		}
		verb := "disable"
		if !r.enabled {
			verb = "enable"
		}
		return m.pluginDo(pluginCmd{verb: verb, arg: r.name})
	case "i":
		if r.notLoaded {
			return m.pluginDo(pluginCmd{verb: "import", arg: r.name})
		}
		return m.pluginDo(pluginCmd{verb: "info", arg: r.name})
	case "x":
		if r.notLoaded {
			m.panel.note, m.panel.noteKind = r.name+" belongs to Claude Code: remove it there", ui.ResultWarn
			return m, nil, true
		}
		return m.pluginDo(pluginCmd{verb: "remove", arg: r.name})
	}
	return m, nil, false
}

func (m workspaceModel) discoverKey(k string) (workspaceModel, tea.Cmd, bool) {
	switch k {
	case "enter":
		rows := m.pluginRowsDiscover()
		if len(rows) == 0 {
			return m, nil, true
		}
		return m.pluginDo(pluginCmd{verb: "install", arg: rows[min(max(m.panel.sel, 0), len(rows)-1)].e.Name})
	case "backspace":
		m.panel.ps.BackspaceFilter()
		m.panel.sel = 0
		return m, nil, true
	case "esc":
		if m.panel.ps.Filter != "" {
			m.panel.ps.Filter, m.panel.sel = "", 0
			return m, nil, true
		}
		return m, nil, false
	case " ", "space":
		m.panel.ps.TypeFilter(" ")
		m.panel.sel = 0
		return m, nil, true
	}
	return m, nil, false
}

func (m workspaceModel) marketsKey(k string) (workspaceModel, tea.Cmd, bool) {
	var repo string
	if len(m.panel.markets) > 0 {
		repo = m.panel.markets[min(max(m.panel.sel, 0), len(m.panel.markets)-1)].Repo
	}
	switch k {
	case "a":
		return m.startInput(""), nil, true
	case "x":
		if repo != "" {
			return m.pluginDo(pluginCmd{verb: "market-remove", arg: repo})
		}
		return m, nil, true
	case "r":
		if repo != "" {
			return m.pluginDo(pluginCmd{verb: "market-refresh", arg: repo})
		}
		return m, nil, true
	}
	return m, nil, false
}

// applyPluginInput handles the owner/repo typed on the Marketplaces tab.
func (m workspaceModel) applyPluginInput(val string) (tea.Model, tea.Cmd) {
	if val == "" {
		return m, nil
	}
	c, err := parsePluginArgs("marketplace add " + val)
	if err != nil {
		m.panel.note, m.panel.noteKind = "type owner/repo, such as anthropics/skills", ui.ResultError
		return m, nil
	}
	return m.runPlugin(c)
}

// ── drawing ──────────────────────────────────────────────────────────────────

func (m workspaceModel) pluginOpts(o *ui.PanelOpts) {
	o.Title = "Plugins"
	o.Filter = ""
	inner := m.panelInner(o.Width)
	var keys []ui.Hint
	sel := m.panel.sel
	switch m.panel.ps.Tab {
	case pluginTabInstalled:
		keys = []ui.Hint{{Key: "↑↓", Label: "move"}, {Key: "Space", Label: "on/off"}, {Key: "i", Label: "info/import"}, {Key: "x", Label: "remove"}}
		o.Body, sel = m.pluginInstalledBody(o.Color, inner)
	case pluginTabDiscover:
		o.Filter = m.panel.ps.Filter
		keys = []ui.Hint{{Key: "↑↓", Label: "move"}, {Key: "Enter", Label: "install"}, {Key: "type", Label: "filter"}}
		o.Body, sel = m.pluginDiscoverBody(o.Color, inner)
	case pluginTabMarkets:
		keys = []ui.Hint{{Key: "a", Label: "add"}, {Key: "x", Label: "remove"}, {Key: "r", Label: "refresh"}}
		o.Body, sel = m.pluginMarketsBody(o.Color, inner)
	default:
		keys = []ui.Hint{{Key: "↑↓", Label: "scroll"}}
		o.Body, sel = m.pluginStatsBody(o.Color, inner), -1
	}
	o.Keys = hintsFor(true, keys...)
	if m.installing != "" {
		o.Body = append(o.Body, "", dimText(clipRow(m.installLine(), inner)))
	}
	if m.marketBusy != "" {
		o.Body = append(o.Body, "", dimText("reading marketplace "+workspace.Sanitize(m.marketBusy)+"…"))
	}
	if m.panel.inputing {
		o.Body = append(o.Body, "", m.panelInputLine("Marketplace (owner/repo):"))
		o.Keys = []ui.Hint{{Key: "Enter", Label: "add"}, {Key: "Esc", Label: "cancel"}}
		sel = len(o.Body) - 1
	}
	if sel >= 0 {
		followSel(o, sel)
	}
}

func (m workspaceModel) pluginLoadingLine() []string {
	if m.panel.pdata == nil {
		return []string{dimText("reading the saved sessions…")}
	}
	return nil
}

// pluginInstalledBody draws the Installed tab and returns the body line of
// the cursor.
func (m workspaceModel) pluginInstalledBody(c lipgloss.TerminalColor, inner int) ([]string, int) {
	rows := m.pluginRowsInstalled()
	if len(rows) == 0 {
		return []string{dimText("no plugins installed"), "", dimText("Discover lists the plugins decsr can install")}, -1
	}
	cur := min(max(m.panel.sel, 0), len(rows)-1)
	var stats map[string]*pluginUse
	if d := m.panel.pdata; d != nil {
		stats = d.stats
	}
	now := m.now()
	var body []string
	for i, r := range rows {
		isSel := i == cur
		glyph, gc := "●", lipgloss.TerminalColor(ui.OK)
		switch {
		case r.notLoaded:
			glyph, gc = "◌", ui.Muted
		case !r.enabled:
			glyph, gc = "○", ui.Muted
		}
		name := lipgloss.NewStyle().Foreground(ui.Text).Bold(isSel)
		nameC := name.Render(padRight(truncateOneLine(workspace.Sanitize(r.name), 22), 22))
		if r.notLoaded || !r.enabled {
			nameC = name.Foreground(ui.Muted).Render(padRight(truncateOneLine(workspace.Sanitize(r.name), 22), 22))
		}
		left := selMark(isSel, c) + lipgloss.NewStyle().Foreground(gc).Render(glyph) + " " + nameC + " " +
			lipgloss.NewStyle().Foreground(ui.RunnerColor(map[bool]string{true: "claude", false: "decsr"}[r.source == "claude"])).Render(padRight(r.source, 7))
		counts := ""
		if r.known {
			counts = partsText(r.skills, r.roles, r.cmd)
		}
		left += " " + dimText(padRight(counts, 28))
		var right string
		switch {
		case r.notLoaded:
			right = dimText("not loaded · i to import")
		case m.panel.pdata == nil:
			right = dimText("…")
		default:
			text, warn := lastUsedCell(stats[r.name], now, r.enabled)
			if warn {
				right = lipgloss.NewStyle().Foreground(ui.Warn).Render(text)
			} else {
				right = dimText(text)
			}
		}
		body = append(body, ui.RightAlign(left, right, inner))
	}
	selLine := cur
	body = append(body, "", clipRow(m.pluginDetail(rows[cur]), inner))
	for _, w := range m.pluginWarnings() {
		body = append(body, clipRow(lipgloss.NewStyle().Foreground(ui.Warn).Render(w), inner))
	}
	return body, selLine
}

// pluginDetail is the line under the list: what the plugin under the cursor
// holds.
func (m workspaceModel) pluginDetail(r installedRow) string {
	head := lipgloss.NewStyle().Foreground(ui.GroupColor(ui.GroupNameProject)).Bold(true).Render("› " + workspace.Sanitize(r.name))
	switch {
	case r.notLoaded:
		return head + dimText(" · Claude Code ("+workspace.Sanitize(r.c.Marketplace)+") · "+partsText(r.skills, r.roles, r.cmd)+" · import copies it; hooks and MCP servers stay unused")
	case !r.enabled:
		return head + dimText(" · off · Space turns it on")
	}
	var items []string
	for _, s := range r.p.Skills {
		items = append(items, s.Name)
	}
	for _, a := range r.p.Agents {
		items = append(items, a.Role())
	}
	text := fmt.Sprintf("· %s", partsText(r.skills, r.roles, r.cmd))
	if len(items) > 0 {
		text += ": " + strings.Join(items, ", ")
	} else if r.p.Description != "" {
		text += " · " + r.p.Description
	}
	if r.p.HasHooks || r.p.HasMCP {
		text += " · hooks/MCP unused"
	}
	return head + " " + dimText(workspace.Sanitize(text))
}

func (m workspaceModel) pluginDiscoverBody(c lipgloss.TerminalColor, inner int) ([]string, int) {
	all := discoverRows(m.panel.markets)
	rows := m.pluginRowsDiscover()
	hint := "type to filter"
	if m.panel.ps.Filter != "" {
		hint = "Esc clears the filter"
	}
	body := []string{dimText(fmt.Sprintf("%d of %d · %s", len(rows), len(all), hint))}
	if len(rows) == 0 {
		return append(body, "", dimText("no plugin matches")), -1
	}
	cur := min(max(m.panel.sel, 0), len(rows)-1)
	for i, r := range rows {
		isSel := i == cur
		src := "curated"
		if r.market != "" {
			src = r.market
		}
		status := ""
		stc := lipgloss.NewStyle().Foreground(ui.Muted)
		switch {
		case m.plug.State(r.e.Name) == plugins.StateEnabled:
			status, stc = "✓ installed", lipgloss.NewStyle().Foreground(ui.OK)
		case m.plug.State(r.e.Name) == plugins.StateDisabled:
			status = "○ installed, off"
		case !r.trusted:
			status, stc = "⚠ untrusted", lipgloss.NewStyle().Foreground(ui.Warn)
		}
		name := lipgloss.NewStyle().Foreground(ui.Text).Bold(isSel).Render(padRight(truncateOneLine(workspace.Sanitize(r.e.Name), 22), 22))
		line := selMark(isSel, c) + name + " " + dimText(padRight(truncateOneLine(workspace.Sanitize(src), 22), 22)) + " " +
			stc.Render(padRight(status, 16)) + " " + dimText(workspace.Sanitize(oneLine(r.e.Description)))
		body = append(body, clipRow(line, inner))
	}
	return body, cur + 1
}

func (m workspaceModel) pluginMarketsBody(c lipgloss.TerminalColor, inner int) ([]string, int) {
	if len(m.panel.markets) == 0 {
		return []string{dimText("no marketplaces added"), "",
			dimText("a adds one: a GitHub repo (owner/repo) with .claude-plugin/marketplace.json"),
			dimText("Plugins of an added marketplace are untrusted until you trust it on the first install.")}, -1
	}
	cur := min(max(m.panel.sel, 0), len(m.panel.markets)-1)
	var body []string
	for i, mk := range m.panel.markets {
		isSel := i == cur
		trust, tc := "⚠ untrusted", lipgloss.NewStyle().Foreground(ui.Warn)
		if mk.Trusted {
			trust, tc = "✓ trusted", lipgloss.NewStyle().Foreground(ui.OK)
		}
		fetched := "never read"
		if !mk.FetchedAt.IsZero() {
			fetched = "read " + marketAge(m.now().Sub(mk.FetchedAt))
		}
		name := lipgloss.NewStyle().Foreground(ui.Text).Bold(isSel).Render(padRight(truncateOneLine(workspace.Sanitize(mk.Repo), 34), 34))
		line := selMark(isSel, c) + name + " " + tc.Render(padRight(trust, 12)) + " " +
			dimText(padRight(fmt.Sprintf("%d %s", len(mk.Entries), pluralize(len(mk.Entries), "plugin", "plugins")), 11)+" "+fetched)
		body = append(body, clipRow(line, inner))
	}
	return body, cur
}

func marketAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "just now"
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func (m workspaceModel) pluginStatsBody(c lipgloss.TerminalColor, inner int) []string {
	d := m.panel.pdata
	if d == nil {
		return m.pluginLoadingLine()
	}
	type srow struct {
		name string
		u    *pluginUse
	}
	var rows []srow
	seen := map[string]bool{}
	for _, p := range m.plug.Plugins {
		seen[p.Name] = true
		rows = append(rows, srow{p.Name, d.stats[p.Name]})
	}
	for name, u := range d.stats {
		if !seen[name] {
			rows = append(rows, srow{name + " (gone)", u})
		}
	}
	tok := func(u *pluginUse) int {
		if u == nil {
			return 0
		}
		return u.use.in + u.use.out
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if tok(rows[i].u) != tok(rows[j].u) {
			return tok(rows[i].u) > tok(rows[j].u)
		}
		return rows[i].name < rows[j].name
	})
	now := m.now()
	var trs [][]string
	for _, r := range rows {
		enabled := m.plug.State(r.name) == plugins.StateEnabled
		last, _ := lastUsedCell(r.u, now, enabled)
		if r.u == nil {
			trs = append(trs, []string{workspace.Sanitize(r.name), "0", "–", "–", last})
			continue
		}
		trs = append(trs, []string{workspace.Sanitize(r.name), fmt.Sprint(r.u.nodes), ui.Num(tok(r.u)), r.u.use.costCell(), last})
	}
	if len(trs) == 0 {
		return []string{dimText("no plugins installed")}
	}
	body := ui.Table(ui.TableOpts{Cols: []ui.Col{{Title: "PLUGIN"}, {Title: "NODES", Align: ui.Right}, {Title: "TOKENS", Align: ui.Right}, {Title: "COST", Align: ui.Right}, {Title: "LAST USED"}},
		Rows: trs, Header: c, Width: inner})
	return append(body, "", dimText("nodes that ran with a skill or a role of the plugin: saved sessions of this project and this one"))
}
