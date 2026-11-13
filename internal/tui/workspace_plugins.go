package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsInstallMsg is the result of an install started by the plugin dialog.
type wsInstallMsg struct {
	gen  int
	name string
	err  error
}

// ── Plugin dialog ────────────────────────────────────────────────────────────

// pluginConfirm builds the question for a catalog plugin. reason is the
// lead's; enable asks to enable an installed but disabled plugin.
func pluginConfirm(e plugins.CatalogEntry, reason string, enable bool) wsConfirm {
	reason = strings.TrimRight(workspace.Sanitize(reason), ". ")
	who := "The lead wants plugin " + e.Name
	if reason != "" {
		who += ": " + reason
	}
	c := wsConfirm{kind: "plugin", arg: e.Name, verb: "install"}
	if enable {
		c.verb = "enable"
		c.text = who + ". It is installed but disabled. Enable it?"
	} else {
		c.text = who + ". Install from github.com/" + e.Repo + "?"
	}
	return c
}

// askPlugins opens the question for the first plugin request nobody answered.
// A request for a plugin that is not in the catalog is declined without
// asking; one for a plugin that is already enabled is marked done. The
// dialog waits while an install runs.
func (m workspaceModel) askPlugins() workspaceModel {
	if m.eng == nil || m.installing != "" {
		return m
	}
	for _, r := range m.g.PendingPlugins() {
		e, ok := plugins.FindCatalog(r.Name)
		switch {
		case !ok:
			m, _ = m.markPlugin(r.Name, workspace.PluginDeclined)
		case m.plug.State(r.Name) == plugins.StateEnabled:
			m, _ = m.markPlugin(r.Name, workspace.PluginInstalled)
		default:
			m.confirm = pluginConfirm(e, r.Reason, m.plug.State(r.Name) == plugins.StateDisabled)
			m.overlay = wsOverlayConfirm
			return m
		}
	}
	return m
}

// markPlugin records the answer to the request for name in the graph (a
// plugin asked for by /plugin install has none). ok is false when it could
// not be saved.
func (m workspaceModel) markPlugin(name, status string) (workspaceModel, bool) {
	if m.eng == nil {
		return m, true
	}
	asked := false
	for _, r := range m.g.PluginRequests {
		asked = asked || r.Name == name
	}
	if !asked {
		return m, true
	}
	if err := m.eng.Replace(func(g *workspace.Graph) { g.SetPluginStatus(name, status) }); err != nil {
		m.say("error", oneLine(err.Error()))
		return m, false
	}
	m.g = m.eng.Snapshot()
	return m, true
}

// pluginAnswer handles a key of the plugin dialog: y installs (or enables),
// n or Esc skips this plugin, a skips every plugin still pending.
func (m workspaceModel) pluginAnswer(k string) (tea.Model, tea.Cmd) {
	c := m.confirm
	name := workspace.Sanitize(c.arg)
	switch k {
	case "y", "Y":
		m.overlay = wsOverlayNone
		e, ok := plugins.FindCatalog(c.arg)
		if !ok { // the dialog is only built for catalog plugins
			return m, nil
		}
		if c.verb == "enable" {
			if err := plugins.SetEnabled(e.Name, true); err != nil {
				m.say("error", oneLine(err.Error()))
				return m, nil
			}
			m = m.reloadPlugins()
			m, _ = m.markPlugin(e.Name, workspace.PluginInstalled)
			m.say("system", fmt.Sprintf("plugin %s enabled", name))
			return m.askPlugins(), nil
		}
		return m.startInstall(e)
	case "n", "N", "esc":
		m.overlay = wsOverlayNone
		m, _ = m.markPlugin(c.arg, workspace.PluginDeclined)
		m.say("system", fmt.Sprintf("skipped plugin %s", name))
		return m.askPlugins(), nil
	case "a", "A":
		m.overlay = wsOverlayNone
		skipped := 0
		for _, r := range m.g.PendingPlugins() {
			var ok bool
			if m, ok = m.markPlugin(r.Name, workspace.PluginDeclined); !ok {
				break
			}
			skipped++
		}
		if skipped == 0 { // a /plugin install question has no request behind it
			skipped = 1
		}
		m.say("system", fmt.Sprintf("skipped %d %s", skipped, pluralize(skipped, "plugin", "plugins")))
	}
	return m, nil
}

// startInstall installs e in the background; wsInstallMsg reports the result.
func (m workspaceModel) startInstall(e plugins.CatalogEntry) (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.runCtx)
	m.installGen++
	gen := m.installGen
	m.installing = e.Name
	m.cancelInstall = cancel
	install := m.deps.installPlugin
	cmd := func() tea.Msg {
		err := install(ctx, e)
		cancel()
		return wsInstallMsg{gen: gen, name: e.Name, err: err}
	}
	return m, tea.Batch(cmd, m.startTick())
}

func (m workspaceModel) onInstalled(msg wsInstallMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.installGen || m.installing == "" {
		return m, nil
	}
	m.installing, m.cancelInstall = "", nil
	name := workspace.Sanitize(msg.name)
	if msg.err != nil {
		m, _ = m.markPlugin(msg.name, workspace.PluginDeclined)
		m.say("error", fmt.Sprintf("could not install %s: %s (try again with /plugin install %s)", name, oneLine(workspace.SanitizeText(msg.err.Error())), name))
		return m.askPlugins(), nil
	}
	m = m.reloadPlugins()
	m, _ = m.markPlugin(msg.name, workspace.PluginInstalled)
	sum := fmt.Sprintf("plugin %s installed", name)
	for _, p := range m.plug.Plugins {
		if p.Name == msg.name {
			sum += fmt.Sprintf(": %d %s, %d %s, %d %s", len(p.Skills), pluralize(len(p.Skills), "skill", "skills"),
				len(p.Agents), pluralize(len(p.Agents), "agent", "agents"), len(p.Commands), pluralize(len(p.Commands), "command", "commands"))
		}
	}
	m.say("system", sum)
	return m.askPlugins(), nil
}

// installLine is the status text while an install runs.
func (m workspaceModel) installLine() string {
	spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
	line := spin + " Installing plugin " + workspace.Sanitize(m.installing)
	if e, ok := plugins.FindCatalog(m.installing); ok {
		line += " from github.com/" + e.Repo
	}
	return line + "…"
}

// cmdPluginInstall is /plugin install <name>: only catalog plugins, after the
// same question the lead's requests get.
func (m workspaceModel) cmdPluginInstall(name string) (tea.Model, tea.Cmd) {
	if m.busy() {
		return m.refuseBusy()
	}
	if m.installing != "" {
		m.say("error", "an install is already running")
		return m, nil
	}
	e, ok := plugins.FindCatalog(name)
	if !ok {
		m.say("error", fmt.Sprintf("plugin %q is not in the catalog (see /plugin available)", cutText(workspace.Sanitize(name), 40)))
		return m, nil
	}
	switch m.plug.State(name) {
	case plugins.StateEnabled:
		m.say("system", fmt.Sprintf("plugin %s is already installed", name))
		return m, nil
	case plugins.StateDisabled:
		m.confirm = pluginConfirm(e, "", true)
		m.confirm.text = fmt.Sprintf("Plugin %s is installed but disabled. Enable it?", name)
	default:
		m.confirm = pluginConfirm(e, "", false)
		m.confirm.text = fmt.Sprintf("Install plugin %s from github.com/%s?", name, e.Repo)
	}
	m.overlay = wsOverlayConfirm
	return m, nil
}

func cutText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// catalogLines is the text of /plugin available: every catalog plugin with its
// state spelled out.
func (m workspaceModel) catalogLines() []string {
	var lines []string
	for i, e := range plugins.Catalog() {
		if i > 0 {
			lines = append(lines, "")
		}
		state := "not installed"
		switch m.plug.State(e.Name) {
		case plugins.StateEnabled:
			state = "installed"
		case plugins.StateDisabled:
			state = "installed, disabled"
		}
		lines = append(lines, e.Name+" · "+state)
		for _, l := range wrapText(e.Description, pluginListWidth+9) {
			lines = append(lines, "  "+l)
		}
		for _, part := range []struct {
			label string
			items []string
		}{{"Skills", e.Skills}, {"Agents", e.Agents}} {
			if len(part.items) == 0 {
				continue
			}
			for j, l := range wrapText(strings.Join(part.items, ", "), pluginListWidth) {
				if j == 0 {
					lines = append(lines, kv("  "+part.label, l))
				} else {
					lines = append(lines, strings.Repeat(" ", 11)+l)
				}
			}
		}
	}
	lines = append(lines, "", "/plugin install <name> installs one after you confirm.")
	return lines
}

// ── Skill picker ─────────────────────────────────────────────────────────────

// wsSkillItem is one row of the skill picker.
type wsSkillItem struct {
	id, desc string
	role     bool // a skill of the node's role: always on
	missing  bool // chosen for the node but no enabled plugin provides it
}

// skillItemsFor lists what the picker offers for n: the role's skills (fixed),
// every skill of the enabled plugins, and the node's own ids that no plugin
// provides, so they can be removed.
func (m workspaceModel) skillItemsFor(n *workspace.Node) ([]wsSkillItem, map[string]bool) {
	desc := map[string]string{}
	var installed []string
	for _, p := range m.plug.Plugins {
		if !p.Enabled {
			continue
		}
		for _, s := range p.Skills {
			desc[s.ID] = s.Description
			installed = append(installed, s.ID)
		}
	}
	sort.Strings(installed)
	var items []wsSkillItem
	seen := map[string]bool{}
	if r, ok := workspace.FindRole(m.cfg, n.Role); ok {
		for _, id := range r.UserSkills {
			if !seen[id] {
				seen[id] = true
				items = append(items, wsSkillItem{id: id, desc: desc[id], role: true})
			}
		}
	}
	for _, id := range installed {
		if !seen[id] {
			seen[id] = true
			items = append(items, wsSkillItem{id: id, desc: desc[id]})
		}
	}
	sel := map[string]bool{}
	for _, id := range n.Skills {
		sel[id] = true
		if !seen[id] {
			seen[id] = true
			items = append(items, wsSkillItem{id: id, missing: true})
		}
	}
	return items, sel
}

func (m workspaceModel) openSkillPicker(n *workspace.Node) (tea.Model, tea.Cmd) {
	switch {
	case n == nil:
	case n.Kind != workspace.KindWork:
		m.setNotice(n.ID+" needs no skills: it runs your build and test commands", false)
	case n.Status != workspace.StatusTodo && n.Status != workspace.StatusFailed:
		m.setNotice(fmt.Sprintf("%s is %s and its skills cannot be changed", n.ID, n.Status), true)
	default:
		m.skillNode = n.ID
		m.skillItems, m.skillSel = m.skillItemsFor(n)
		m.skillIdx = 0
		m.overlay = wsOverlaySkills
	}
	return m, nil
}

func (m workspaceModel) skillKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		m.overlay = wsOverlayNone
	case "up", "k":
		m.skillIdx = max(m.skillIdx-1, 0)
	case "down", "j":
		m.skillIdx = min(m.skillIdx+1, max(len(m.skillItems)-1, 0))
	case " ", "space", "x":
		if m.skillIdx < len(m.skillItems) {
			it := m.skillItems[m.skillIdx]
			if !it.role {
				sel := make(map[string]bool, len(m.skillSel))
				for id, v := range m.skillSel {
					sel[id] = v
				}
				sel[it.id] = !sel[it.id]
				m.skillSel = sel
			}
		}
	case "enter":
		m.overlay = wsOverlayNone
		var ids []string
		for _, it := range m.skillItems {
			if !it.role && m.skillSel[it.id] {
				ids = append(ids, it.id)
			}
		}
		if err := m.eng.SetSkills(m.skillNode, ids); err != nil {
			m.setNotice("! "+err.Error(), true)
			return m, nil
		}
		m.g = m.eng.Snapshot()
		m.setNotice(fmt.Sprintf("%s: %d %s chosen", m.skillNode, len(ids), pluralize(len(ids), "skill", "skills")), false)
	}
	return m, nil
}

// renderSkillPicker draws the skill checklist box.
func renderSkillPicker(title string, items []wsSkillItem, sel map[string]bool, idx int, width, height int) string {
	boxW := min(70, width-2)
	inner := boxW - 4
	rows := max(height-2-2-2, 1)
	rows = min(rows, max(len(items), 1))
	start := 0
	if idx >= rows {
		start = idx - rows + 1
	}
	var body []string
	if len(items) == 0 {
		body = append(body, styleWsSkillNote.Render("No skills installed. /plugin available lists plugins you can install."))
	}
	for i := start; i < start+rows && i < len(items); i++ {
		body = append(body, renderSkillRow(items[i], sel[items[i].id] || items[i].role, i == idx, inner))
	}
	body = append(body, "", styleWsSkillNote.Render("↑↓ move · Space toggle · Enter save · Esc cancel"))
	return wsBox(ansi.Truncate(title, boxW-6, "…"), boxW, body)
}

// renderSkillRow draws one row of exactly inner cells. The check box and the
// notes carry the state in text; the row background only marks the cursor.
func renderSkillRow(it wsSkillItem, on, selected bool, inner int) string {
	look := func(st lipgloss.Style) lipgloss.Style {
		if selected {
			return st.Inherit(styleWsPickRow)
		}
		return st
	}
	marker, box := "  ", "[ ]"
	if selected {
		marker = "▸ "
	}
	if on {
		box = "[x]"
	}
	var sb strings.Builder
	sb.WriteString(look(styleWsSkill.Bold(true)).Render(marker + box + " " + workspace.Sanitize(it.id)))
	switch {
	case it.role:
		sb.WriteString(look(styleWsSkillNote).Render(" (role)"))
	case it.missing:
		sb.WriteString(look(styleWsSkillWarn).Render(" (not installed)"))
	}
	if d := workspace.Sanitize(it.desc); d != "" {
		sb.WriteString(look(styleWsSkillNote).Render(" — " + d))
	}
	row := ansi.Truncate(sb.String(), inner, "…")
	if pad := inner - ansi.StringWidth(row); pad > 0 {
		row += look(styleWsSkill).Render(strings.Repeat(" ", pad))
	}
	return row
}
