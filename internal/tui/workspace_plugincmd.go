package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const pluginUsage = "/plugin [list | search <q> | info <name> | install <name> | enable <name> | disable <name> | remove <name> | import <name> | reload | stats | marketplace add|remove <owner/repo>]"

// ── parser ───────────────────────────────────────────────────────────────────

// pluginCmd is a parsed /plugin line. verb is "" (open the manager), list,
// search, info, install, enable, disable, remove, import, reload, stats,
// available, market-add, market-remove or market-refresh (panel only).
type pluginCmd struct{ verb, arg string }

// parsePluginArgs reads what follows /plugin.
func parsePluginArgs(args string) (pluginCmd, error) {
	f := strings.Fields(args)
	bad := fmt.Errorf("usage: %s", pluginUsage)
	if len(f) == 0 {
		return pluginCmd{}, nil
	}
	switch v := f[0]; v {
	case "list", "reload", "stats", "available":
		if len(f) != 1 {
			return pluginCmd{}, bad
		}
		return pluginCmd{verb: v}, nil
	case "search":
		if len(f) < 2 {
			return pluginCmd{}, bad
		}
		return pluginCmd{verb: v, arg: strings.Join(f[1:], " ")}, nil
	case "info", "install", "enable", "disable", "remove", "import":
		if len(f) != 2 {
			return pluginCmd{}, bad
		}
		return pluginCmd{verb: v, arg: f[1]}, nil
	case "marketplace":
		if len(f) != 3 || (f[1] != "add" && f[1] != "remove") {
			return pluginCmd{}, bad
		}
		if !plugins.ValidRepo(f[2]) {
			return pluginCmd{}, fmt.Errorf("%q is not owner/repo", cutText(workspace.Sanitize(f[2]), 40))
		}
		return pluginCmd{verb: "market-" + f[1], arg: f[2]}, nil
	}
	return pluginCmd{}, bad
}

// ── Discover data ────────────────────────────────────────────────────────────

// discoverRow is a plugin that can be installed: of the curated catalog
// (market "") or of an added marketplace.
type discoverRow struct {
	e       plugins.CatalogEntry
	market  string
	trusted bool
}

// discoverRows lists the curated catalog, then the plugins of the marketplaces.
// A name that is already listed is skipped, so the catalog wins.
func discoverRows(markets []plugins.Marketplace) []discoverRow {
	var rows []discoverRow
	seen := map[string]bool{}
	for _, e := range plugins.Catalog() {
		seen[e.Name] = true
		rows = append(rows, discoverRow{e: e, trusted: true})
	}
	for _, mk := range markets {
		for _, me := range mk.Entries {
			if seen[me.Name] {
				continue
			}
			seen[me.Name] = true
			rows = append(rows, discoverRow{e: me.Entry(), market: mk.Repo, trusted: mk.Trusted})
		}
	}
	return rows
}

// filterDiscover keeps the rows that contain every word of q in their name,
// description, tags, skills, agents or marketplace.
func filterDiscover(rows []discoverRow, q string) []discoverRow {
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return rows
	}
	var out []discoverRow
	for _, r := range rows {
		hay := strings.ToLower(strings.Join(append(append(append([]string{r.e.Name, r.e.Description, r.market}, r.e.Tags...), r.e.Skills...), r.e.Agents...), " "))
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(hay, w)
		}
		if ok {
			out = append(out, r)
		}
	}
	return out
}

func findDiscover(rows []discoverRow, name string) (discoverRow, bool) {
	for _, r := range rows {
		if r.e.Name == name {
			return r, true
		}
	}
	return discoverRow{}, false
}

// ── usage stats ──────────────────────────────────────────────────────────────

// pluginUse is what the saved sessions say about one plugin.
type pluginUse struct {
	nodes int
	use   useRow
	last  time.Time
}

// pluginOfID is the plugin of a "<plugin>:<name>" skill id or role.
func pluginOfID(id string) string {
	p, _, ok := strings.Cut(id, ":")
	if !ok || p == "" {
		return ""
	}
	return p
}

// buildPluginStats adds up, per plugin, the nodes that ran with one of its
// skills or with one of its roles: their tokens, cost and the last time.
func buildPluginStats(sessions []workspace.Session) map[string]*pluginUse {
	out := map[string]*pluginUse{}
	for _, s := range sessions {
		if s.Graph == nil {
			continue
		}
		for _, n := range s.Graph.Nodes {
			if n.StartedAt.IsZero() && n.EndedAt.IsZero() && n.InputTokens+n.OutputTokens == 0 {
				continue // never ran
			}
			names := map[string]bool{}
			for _, id := range append([]string{n.Role}, n.Skills...) {
				if p := pluginOfID(id); p != "" {
					names[p] = true
				}
			}
			t := n.EndedAt
			if t.IsZero() {
				t = n.StartedAt
			}
			if t.IsZero() {
				t = s.UpdatedAt
			}
			for p := range names {
				u := out[p]
				if u == nil {
					u = &pluginUse{}
					out[p] = u
				}
				u.nodes++
				u.use.add(n.InputTokens, n.OutputTokens, n.CostUSD, n.CostKnown, n.Estimated)
				if t.After(u.last) {
					u.last = t
				}
			}
		}
	}
	return out
}

// lastUsedCell is the "last used" text of a plugin; warn marks an enabled
// plugin that has not been used for 30 days.
func lastUsedCell(u *pluginUse, now time.Time, enabled bool) (text string, warn bool) {
	if u == nil || u.last.IsZero() {
		return "never used", false
	}
	d := now.Sub(u.last)
	days := int(dayStart(now).Sub(dayStart(u.last)).Hours()/24 + 0.5)
	switch {
	case enabled && days >= 30:
		return fmt.Sprintf("unused %dd ⚠", days), true
	case d < time.Minute:
		return "used just now", false
	case d < time.Hour:
		return fmt.Sprintf("used %dm ago", int(d.Minutes())), false
	case days == 0 && d < 12*time.Hour:
		return fmt.Sprintf("used %dh ago", int(d.Hours())), false
	case days == 0:
		return "used today", false
	case days == 1:
		return "used yesterday", false
	}
	return fmt.Sprintf("used %dd ago", days), false
}

// pluginData is what the plugin manager reads off the UI thread.
type pluginData struct {
	stats  map[string]*pluginUse
	claude []plugins.ClaudePlugin
	counts map[string]partCounts // parts of the plugins that are off
	loaded bool
}

type partCounts struct{ skills, roles, cmds int }

// wsPluginDataMsg carries pluginData; say asks for the /plugin stats line.
type wsPluginDataMsg struct {
	data pluginData
	say  bool
}

// pluginDataCmd reads the saved sessions, Claude Code's plugin list and the
// parts of the disabled plugins in the background.
func (m workspaceModel) pluginDataCmd(say bool) tea.Cmd {
	root, now := m.root, m.now()
	cur := m.sess.snapshot()
	if m.g != nil {
		cur.Graph = m.g.Clone()
		cur.UpdatedAt = now
	}
	off := map[string]string{}
	for _, p := range m.plug.Plugins {
		if !p.Enabled {
			off[p.Name] = p.Dir
		}
	}
	return func() tea.Msg {
		d := pluginData{loaded: true, counts: map[string]partCounts{}}
		d.stats = buildPluginStats(loadSessionsSince(root, now, cur, now.AddDate(-1, 0, 0)))
		d.claude = plugins.ClaudeInstalled()
		for name, dir := range off {
			if p, ok := plugins.Inspect(dir); ok {
				d.counts[name] = partCounts{len(p.Skills), len(p.Agents), len(p.Commands)}
			}
		}
		return wsPluginDataMsg{data: d, say: say}
	}
}

// pluginRefreshCmd reloads the data of an open plugin panel.
func (m workspaceModel) pluginRefreshCmd() tea.Cmd {
	if m.overlay == wsOverlayPanel && m.panel.kind == panelPlugin {
		return m.pluginDataCmd(false)
	}
	return nil
}

func (m workspaceModel) onPluginData(msg wsPluginDataMsg) (tea.Model, tea.Cmd) {
	if m.overlay == wsOverlayPanel && m.panel.kind == panelPlugin {
		d := msg.data
		m.panel.pdata = &d
	}
	if msg.say {
		m.say("ok", pluginStatsLine(msg.data, m.plug))
	}
	return m, nil
}

// pluginStatsLine is the one line of /plugin stats.
func pluginStatsLine(d pluginData, set plugins.Set) string {
	var total useRow
	nodes, used := 0, 0
	for name, u := range d.stats {
		if set.State(name) == plugins.StateMissing {
			continue
		}
		used++
		nodes += u.nodes
		total.add(u.use.in, u.use.out, u.use.usd, u.use.priced, u.use.estimated)
		if u.use.unpriced {
			total.unpriced = true
		}
	}
	if used == 0 {
		return "no node of the saved sessions used a plugin yet"
	}
	return fmt.Sprintf("%d of %d plugins used · %d %s · %s tokens · %s", used, len(set.Plugins), nodes, pluralize(nodes, "node", "nodes"), ui.Num(total.in+total.out), total.costCell())
}

// ── dispatch ─────────────────────────────────────────────────────────────────

// pluginSay prints a result line and, while the plugin panel is open, shows it
// at the foot of the panel.
func (m *workspaceModel) pluginSay(who, text string) {
	m.say(who, text)
	if m.overlay == wsOverlayPanel && m.panel.kind == panelPlugin {
		m.panel.note, m.panel.noteKind = text, resultKindOf(who)
	}
}

func resultKindOf(who string) string {
	switch who {
	case "ok":
		return ui.ResultOK
	case "error":
		return ui.ResultError
	case "warn":
		return ui.ResultWarn
	}
	return ui.ResultInfo
}

// askConfirm opens a question; from the plugin panel "no" returns to it.
func (m workspaceModel) askConfirm(c wsConfirm) workspaceModel {
	if m.overlay == wsOverlayPanel && m.panel.kind == panelPlugin {
		c.back = wsOverlayPanel
	}
	m.confirm = c
	m.overlay = wsOverlayConfirm
	return m
}

func (m workspaceModel) cmdPlugin(args string) (tea.Model, tea.Cmd) {
	c, err := parsePluginArgs(args)
	if err != nil {
		m.say("error", err.Error())
		return m, nil
	}
	return m.runPlugin(c)
}

// runPlugin runs a parsed /plugin line; the panel keys use it too.
func (m workspaceModel) runPlugin(c pluginCmd) (tea.Model, tea.Cmd) {
	switch c.verb {
	case "":
		m = m.openPanel(panelPlugin, 0)
		m.panel.markets = plugins.LoadMarketplaces()
		return m, m.pluginDataCmd(false)
	case "available":
		m.pluginSay("system", "/plugin has moved on: /plugin search <q> finds plugins, /plugin opens the manager")
		return m, nil
	case "list":
		m.pluginSay("system", m.pluginListLine())
		return m, nil
	case "search":
		m.pluginSay("system", m.pluginSearchLine(c.arg))
		return m, nil
	case "info":
		return m.pluginInfo(c.arg)
	case "stats":
		return m, m.pluginDataCmd(true)
	}
	if m.installing != "" {
		m.pluginSay("error", "a plugin install is running; try again when it is done")
		return m, nil
	}
	switch c.verb {
	case "market-add", "market-refresh":
		return m.fetchMarketCmd(c.arg, c.verb == "market-refresh")
	case "market-remove":
		return m.removeMarket(c.arg)
	}
	if m.busy() { // roles would change under the running nodes
		return m.refuseBusy()
	}
	switch c.verb {
	case "install":
		return m.pluginInstallCmd(c.arg)
	case "reload":
		m = m.reloadPlugins()
		m.pluginSay("ok", fmt.Sprintf("plugins reloaded: %d found", len(m.plug.Plugins)))
		return m, m.pluginRefreshCmd()
	case "enable", "disable":
		if !m.hasPlugin(c.arg) {
			m.pluginSay("error", fmt.Sprintf("no plugin named %q (see /plugin list)", cutText(workspace.Sanitize(c.arg), 40)))
			return m, nil
		}
		on := c.verb == "enable"
		if err := plugins.SetEnabled(c.arg, on); err != nil {
			m.pluginSay("error", oneLine(err.Error()))
			return m, nil
		}
		m = m.reloadPlugins()
		m.pluginSay("ok", fmt.Sprintf("plugin %s %sd", workspace.Sanitize(c.arg), c.verb))
		return m, m.pluginRefreshCmd()
	case "remove":
		if !m.hasPlugin(c.arg) {
			m.pluginSay("error", fmt.Sprintf("no plugin named %q (see /plugin list)", cutText(workspace.Sanitize(c.arg), 40)))
			return m, nil
		}
		m = m.askConfirm(wsConfirm{kind: "rmplugin", arg: c.arg, text: fmt.Sprintf("Remove plugin %s and its files? y/n", workspace.Sanitize(c.arg))})
		return m, nil
	case "import":
		return m.pluginImport(c.arg)
	}
	return m, nil
}

func (m workspaceModel) hasPlugin(name string) bool {
	for _, p := range m.plug.Plugins {
		if p.Name == name {
			return true
		}
	}
	return false
}

// pluginListLine is /plugin list: the plugins, those that are off marked.
func (m workspaceModel) pluginListLine() string {
	if len(m.plug.Plugins) == 0 {
		return "no plugins installed · /plugin opens the manager"
	}
	var names []string
	for _, p := range m.plug.Plugins {
		n := workspace.Sanitize(p.Name)
		if !p.Enabled {
			n += " (off)"
		}
		names = append(names, n)
	}
	return fmt.Sprintf("%d %s: %s", len(names), pluralize(len(names), "plugin", "plugins"), strings.Join(names, ", "))
}

// pluginSearchLine is /plugin search: the matches of the catalog and of the
// added marketplaces.
func (m workspaceModel) pluginSearchLine(q string) string {
	rows := filterDiscover(discoverRows(plugins.LoadMarketplaces()), q)
	if len(rows) == 0 {
		return fmt.Sprintf("no plugin matches %q", cutText(workspace.Sanitize(q), 40))
	}
	var names []string
	for i, r := range rows {
		if i == 8 {
			names = append(names, fmt.Sprintf("+%d more", len(rows)-8))
			break
		}
		n := workspace.Sanitize(r.e.Name)
		if m.plug.State(r.e.Name) != plugins.StateMissing {
			n += " (installed)"
		}
		names = append(names, n)
	}
	return fmt.Sprintf("%d %s for %q: %s", len(rows), pluralize(len(rows), "match", "matches"), cutText(workspace.Sanitize(q), 40), strings.Join(names, ", "))
}

func partsText(skills, roles, cmds int) string {
	var parts []string
	if skills > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", skills, pluralize(skills, "skill", "skills")))
	}
	if roles > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", roles, pluralize(roles, "role", "roles")))
	}
	if cmds > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", cmds, pluralize(cmds, "command", "commands")))
	}
	if len(parts) == 0 {
		return "no parts"
	}
	return strings.Join(parts, " · ")
}

// pluginInfo is /plugin info <name>.
func (m workspaceModel) pluginInfo(name string) (tea.Model, tea.Cmd) {
	safe := workspace.Sanitize(name)
	for _, p := range m.plug.Plugins {
		if p.Name != name {
			continue
		}
		sk, ro, cm := len(p.Skills), len(p.Agents), len(p.Commands)
		state := "on"
		if !p.Enabled {
			state = "off"
			if q, ok := plugins.Inspect(p.Dir); ok {
				sk, ro, cm = len(q.Skills), len(q.Agents), len(q.Commands)
			}
		}
		line := fmt.Sprintf("%s · decsr (%s) · %s · %s", safe, p.Scope, state, partsText(sk, ro, cm))
		if p.Version != "" {
			line = strings.Replace(line, safe, safe+" "+p.Version, 1)
		}
		if p.Description != "" {
			line += " · " + p.Description
		}
		if p.HasHooks || p.HasMCP {
			line += " · hooks and MCP servers unused"
		}
		m.pluginSay("system", line)
		return m, nil
	}
	if r, ok := findDiscover(discoverRows(plugins.LoadMarketplaces()), name); ok {
		src := "curated catalog"
		if r.market != "" {
			src = "marketplace " + r.market
		}
		m.pluginSay("system", fmt.Sprintf("%s · not installed · %s · github.com/%s · %s", safe, src, r.e.Repo, workspace.Sanitize(r.e.Description)))
		return m, nil
	}
	for _, c := range plugins.ClaudeInstalled() {
		if c.Name == name {
			m.pluginSay("system", fmt.Sprintf("%s · Claude Code (%s) · not loaded · %s · /plugin import %s copies it", safe, c.Marketplace, partsText(c.Skills, c.Agents, c.Commands), safe))
			return m, nil
		}
	}
	m.pluginSay("error", fmt.Sprintf("no plugin named %q (try /plugin search)", cutText(safe, 40)))
	return m, nil
}

// pluginImport is /plugin import <name>.
func (m workspaceModel) pluginImport(name string) (tea.Model, tea.Cmd) {
	if m.hasPlugin(name) {
		m.pluginSay("warn", fmt.Sprintf("plugin %s is already installed", workspace.Sanitize(name)))
		return m, nil
	}
	p, err := plugins.ImportClaude(name)
	if err != nil {
		m.pluginSay("error", oneLine(workspace.SanitizeText(err.Error())))
		return m, nil
	}
	m = m.reloadPlugins()
	m.pluginSay("ok", fmt.Sprintf("imported %s from Claude Code: %s · hooks and MCP servers stay unused", workspace.Sanitize(name), partsText(len(p.Skills), len(p.Agents), len(p.Commands))))
	return m, m.pluginRefreshCmd()
}

// pluginInstallCmd is /plugin install <name>: a plugin of the curated catalog
// or of an added marketplace, after a question.
func (m workspaceModel) pluginInstallCmd(name string) (tea.Model, tea.Cmd) {
	safe := workspace.Sanitize(name)
	markets := plugins.LoadMarketplaces()
	if m.panel.kind == panelPlugin && m.panel.markets != nil && m.overlay == wsOverlayPanel {
		markets = m.panel.markets
	}
	r, ok := findDiscover(discoverRows(markets), name)
	if !ok {
		m.pluginSay("error", fmt.Sprintf("plugin %q is not in the catalog or a marketplace (try /plugin search)", cutText(safe, 40)))
		return m, nil
	}
	switch m.plug.State(name) {
	case plugins.StateEnabled:
		m.pluginSay("system", fmt.Sprintf("plugin %s is already installed", safe))
		return m, nil
	case plugins.StateDisabled:
		c := pluginConfirm(r.e, "", true)
		c.text = fmt.Sprintf("Plugin %s is installed but disabled. Enable it?", safe)
		return m.askConfirm(c), nil
	}
	if r.market == "" {
		c := pluginConfirm(r.e, "", false)
		c.text = fmt.Sprintf("Install plugin %s from github.com/%s?", safe, r.e.Repo)
		return m.askConfirm(c), nil
	}
	m.marketPick = pluginPick{market: r.market, e: r.e}
	if !r.trusted {
		return m.askConfirm(wsConfirm{kind: "trust", arg: r.market,
			text: fmt.Sprintf("Trust %s? Plugins can add skills, roles and commands that agents will follow. y/n", workspace.Sanitize(r.market))}), nil
	}
	return m.askMarketInstall(), nil
}

// pluginPick is the marketplace plugin a question is about.
type pluginPick struct {
	market string
	e      plugins.CatalogEntry
}

func (m workspaceModel) askMarketInstall() workspaceModel {
	p := m.marketPick
	return m.askConfirm(wsConfirm{kind: "mplugin", arg: p.e.Name,
		text: fmt.Sprintf("Install plugin %s from github.com/%s (marketplace %s)? y/n", workspace.Sanitize(p.e.Name), p.e.Repo, workspace.Sanitize(p.market))})
}

// pluginConfirmed carries out a "yes" to a question of the plugin manager.
func (m workspaceModel) pluginConfirmed(c wsConfirm) (tea.Model, tea.Cmd) {
	m.overlay = c.back
	switch c.kind {
	case "rmplugin":
		if m.busy() {
			return m.refuseBusy()
		}
		if err := plugins.Remove(m.root, c.arg); err != nil {
			m.pluginSay("error", oneLine(workspace.SanitizeText(err.Error())))
			return m, nil
		}
		m = m.reloadPlugins()
		m.pluginSay("ok", fmt.Sprintf("plugin %s removed", workspace.Sanitize(c.arg)))
		return m, m.pluginRefreshCmd()
	case "rmmarket":
		return m.removeMarketNow(c.arg)
	case "trust":
		if err := plugins.TrustMarketplace(c.arg); err != nil {
			m.pluginSay("error", oneLine(err.Error()))
			return m, nil
		}
		m.panel.markets = plugins.LoadMarketplaces()
		m.pluginSay("ok", "trusted "+workspace.Sanitize(c.arg))
		return m.askMarketInstall(), nil
	case "mplugin":
		p := m.marketPick
		if p.e.Name != c.arg || p.market == "" {
			return m, nil
		}
		install := m.deps.installMarket
		return m.runInstall(p.e, func(ctx context.Context) error { return install(ctx, p.market, p.e) })
	}
	return m, nil
}

// ── marketplaces ─────────────────────────────────────────────────────────────

// wsMarketMsg is the result of reading a marketplace.
type wsMarketMsg struct {
	repo    string
	fetch   plugins.MarketFetch
	err     error
	refresh bool
}

func (m workspaceModel) fetchMarketCmd(repo string, refresh bool) (tea.Model, tea.Cmd) {
	if m.marketBusy != "" {
		m.pluginSay("error", "a marketplace is being read; try again when it is done")
		return m, nil
	}
	_, added := plugins.FindMarketplace(plugins.LoadMarketplaces(), repo)
	switch {
	case !refresh && added:
		m.pluginSay("warn", fmt.Sprintf("marketplace %s is already added (r in the Marketplaces tab refreshes it)", workspace.Sanitize(repo)))
		return m, nil
	case refresh && !added:
		m.pluginSay("error", fmt.Sprintf("marketplace %s is not added", workspace.Sanitize(repo)))
		return m, nil
	}
	m.marketBusy = repo
	fetch, ctx := m.deps.fetchMarket, m.runCtx
	m.pluginSay("system", "reading marketplace "+workspace.Sanitize(repo)+"…")
	return m, func() tea.Msg {
		f, err := fetch(ctx, repo)
		return wsMarketMsg{repo: repo, fetch: f, err: err, refresh: refresh}
	}
}

func (m workspaceModel) onMarket(msg wsMarketMsg) (tea.Model, tea.Cmd) {
	m.marketBusy = ""
	repo := workspace.Sanitize(msg.repo)
	if msg.err != nil {
		m.pluginSay("error", fmt.Sprintf("marketplace %s: %s", repo, oneLine(workspace.SanitizeText(msg.err.Error()))))
		return m, nil
	}
	ms := plugins.SetMarketplace(plugins.LoadMarketplaces(), plugins.Marketplace{Repo: msg.repo, FetchedAt: m.now(), Entries: msg.fetch.Entries})
	if err := plugins.SaveMarketplaces(ms); err != nil {
		m.pluginSay("error", oneLine(err.Error()))
		return m, nil
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Repo < ms[j].Repo })
	if m.panel.kind == panelPlugin {
		m.panel.markets = ms
	}
	verb := "added"
	if msg.refresh {
		verb = "refreshed"
	}
	n := len(msg.fetch.Entries)
	line := fmt.Sprintf("marketplace %s %s: %d %s", repo, verb, n, pluralize(n, "plugin", "plugins"))
	if s := msg.fetch.Skipped; s > 0 {
		line += fmt.Sprintf(" (%d skipped: other source kinds)", s)
	}
	m.pluginSay("ok", line)
	return m, nil
}

// removeMarket is /plugin marketplace remove: from the panel it asks first.
func (m workspaceModel) removeMarket(repo string) (tea.Model, tea.Cmd) {
	if _, ok := plugins.FindMarketplace(plugins.LoadMarketplaces(), repo); !ok {
		m.pluginSay("error", fmt.Sprintf("marketplace %s is not added", workspace.Sanitize(repo)))
		return m, nil
	}
	if m.overlay == wsOverlayPanel && m.panel.kind == panelPlugin {
		m = m.askConfirm(wsConfirm{kind: "rmmarket", arg: repo, text: fmt.Sprintf("Remove marketplace %s? Installed plugins stay. y/n", workspace.Sanitize(repo))})
		return m, nil
	}
	return m.removeMarketNow(repo)
}

func (m workspaceModel) removeMarketNow(repo string) (tea.Model, tea.Cmd) {
	ms := plugins.RemoveMarketplace(plugins.LoadMarketplaces(), repo)
	if err := plugins.SaveMarketplaces(ms); err != nil {
		m.pluginSay("error", oneLine(err.Error()))
		return m, nil
	}
	if m.panel.kind == panelPlugin {
		m.panel.markets = ms
		m.panel.sel = max(min(m.panel.sel, len(ms)-1), 0)
	}
	m.pluginSay("ok", "marketplace "+workspace.Sanitize(repo)+" removed")
	return m, nil
}
