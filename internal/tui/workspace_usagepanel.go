package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── data builders ────────────────────────────────────────────────────────────

// useRow is one row of a usage table: a role with its agent, or a node.
type useRow struct {
	key, role, agent string
	in, out          int
	usd              float64
	priced, unpriced bool // some tokens have a known price / have none
	estimated        bool
}

// add counts tokens and cost into the row.
func (r *useRow) add(in, out int, usd float64, known, est bool) {
	r.in += in
	r.out += out
	r.estimated = r.estimated || est
	if in+out == 0 {
		return
	}
	if known {
		r.priced = true
		r.usd += usd
	} else {
		r.unpriced = true
	}
}

// costCell is the cost of a row: "$0.42", "≥ $0.42" when a price is missing
// for part of it, "–" when for all of it.
func (r useRow) costCell() string {
	switch {
	case !r.priced && r.unpriced:
		return "–"
	case r.unpriced:
		return "≥ " + ui.Money(r.usd)
	case !r.priced && r.in+r.out == 0:
		return "–"
	}
	return ui.Money(r.usd)
}

// usageAgent is the runner, model and effort a node ran with.
func usageAgent(c config.Config, n *workspace.Node) (runnerName, model, effort string) {
	role, ok := workspace.FindRole(c, n.Role)
	ac := role.Agent
	if !ok {
		ac = c.Agents["pm"]
	}
	runnerName, model, effort = ac.Runner, effectiveModel(c, ac), ac.Effort
	if a := n.Assign; a != nil && a.Runner != "" {
		runnerName, model, effort = a.Runner, a.Model, a.Effort
	}
	if n.Model != "" {
		model = n.Model
	}
	if n.Effort != "" {
		effort = n.Effort
	}
	return
}

// sessionUsage is the table of the Session tab: tokens and cost per role and
// agent (the lead counts as pm), per node, and the totals.
type sessionUsage struct {
	roles, nodes []useRow
	total        useRow
	folded       float64 // cost of folded and cancelled nodes, in the total only
}

func buildSessionUsage(c config.Config, s workspace.Session, g *workspace.Graph) sessionUsage {
	var u sessionUsage
	byKey := map[string]int{}
	roleRow := func(role, agent string) *useRow {
		k := role + "\x00" + agent
		if i, ok := byKey[k]; ok {
			return &u.roles[i]
		}
		byKey[k] = len(u.roles)
		u.roles = append(u.roles, useRow{key: k, role: role, agent: agent})
		return &u.roles[len(u.roles)-1]
	}
	pm := c.Agents["pm"]
	if s.In+s.Out > 0 {
		usd, known := leadCost(c, s)
		roleRow("pm", agentLabel(pm.Runner, effectiveModel(c, pm), pm.Effort)).add(s.In, s.Out, usd, known, false)
		u.total.add(s.In, s.Out, usd, known, false)
	}
	if g != nil {
		for _, n := range g.Nodes {
			if n.InputTokens == 0 && n.OutputTokens == 0 {
				continue
			}
			rn, md, ef := usageAgent(c, n)
			role := n.Role
			if role == "" {
				role = "-"
			}
			roleRow(role, agentLabel(rn, md, ef)).add(n.InputTokens, n.OutputTokens, n.CostUSD, n.CostKnown, n.Estimated)
			nr := useRow{role: role, key: n.ID}
			nr.add(n.InputTokens, n.OutputTokens, n.CostUSD, n.CostKnown, n.Estimated)
			u.nodes = append(u.nodes, nr)
			u.total.add(n.InputTokens, n.OutputTokens, n.CostUSD, n.CostKnown, n.Estimated)
		}
		if g.RemovedCostUSD > 0 {
			u.folded = g.RemovedCostUSD
			u.total.usd += g.RemovedCostUSD
			u.total.priced = true
		}
	}
	return u
}

// usageSpan is the usage of a period.
type usageSpan struct {
	sessions, tokens int
	usd              float64
	priced, unpriced bool
}

func (u *usageSpan) add(tokens int, usd float64, known bool) {
	u.tokens += tokens
	if tokens == 0 {
		return
	}
	if known {
		u.priced = true
		u.usd += usd
	} else {
		u.unpriced = true
	}
}

// costCell is "$1.20", "≥ $1.20" or "–" when no price is known.
func (u usageSpan) costCell() string {
	switch {
	case u.unpriced && !u.priced:
		return "–"
	case u.unpriced:
		return "≥ " + ui.Money(u.usd)
	case u.tokens == 0:
		return "–"
	}
	return ui.Money(u.usd)
}

// agentUse is the usage of one role on one agent over the last 30 days.
type agentUse struct {
	role, agent string
	tokens      int
	span        usageSpan
}

// usageHist is the usage of the project's saved sessions.
type usageHist struct {
	today, week, month usageSpan
	daily              [30]float64 // tokens per day, the oldest first, today last
	agents             []agentUse  // 30 days, most tokens first
}

// dayStart is local midnight of t.
func dayStart(t time.Time) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, t.Location())
}

// buildUsageHistory adds up the sessions of the last 30 days. Node tokens fall
// on the day the node ended (started, else the session's last update); the
// lead's tokens and the session count on the day of the last update. Cost is
// the cost saved with each node (from the configured prices): never guessed.
func buildUsageHistory(sessions []workspace.Session, now time.Time, c config.Config) usageHist {
	var h usageHist
	today := dayStart(now)
	byAgent := map[string]*agentUse{}
	// spanAdd calls f for each span that t falls into and returns its day slot;
	// ok is false when t is older than 30 days or in the future.
	spanAdd := func(t time.Time, f func(*usageSpan)) (slot int, ok bool) {
		d := dayStart(t.In(now.Location()))
		days := int(today.Sub(d).Hours()/24 + 0.5)
		if days < 0 || days > 29 {
			return 0, false
		}
		f(&h.month)
		if days <= 6 {
			f(&h.week)
		}
		if days == 0 {
			f(&h.today)
		}
		return 29 - days, true
	}
	for _, s := range sessions {
		if _, ok := spanAdd(s.UpdatedAt, func(sp *usageSpan) { sp.sessions++ }); !ok {
			continue
		}
		pm := c.Agents["pm"]
		if s.In+s.Out > 0 {
			usd, known := leadCost(c, s)
			slot, _ := spanAdd(s.UpdatedAt, func(sp *usageSpan) { sp.add(s.In+s.Out, usd, known) })
			h.daily[slot] += float64(s.In + s.Out)
			a := agentFor(byAgent, "pm", agentLabel(pm.Runner, effectiveModel(c, pm), pm.Effort))
			a.span.add(s.In+s.Out, usd, known)
			a.tokens += s.In + s.Out
		}
		if s.Graph == nil {
			continue
		}
		for _, n := range s.Graph.Nodes {
			tok := n.InputTokens + n.OutputTokens
			if tok == 0 {
				continue
			}
			t := n.EndedAt
			if t.IsZero() {
				t = n.StartedAt
			}
			if t.IsZero() {
				t = s.UpdatedAt
			}
			slot, ok := spanAdd(t, func(sp *usageSpan) { sp.add(tok, n.CostUSD, n.CostKnown) })
			if !ok {
				continue
			}
			h.daily[slot] += float64(tok)
			rn, md, ef := usageAgent(c, n)
			role := n.Role
			if role == "" {
				role = "-"
			}
			a := agentFor(byAgent, role, agentLabel(rn, md, ef))
			a.span.add(tok, n.CostUSD, n.CostKnown)
			a.tokens += tok
		}
	}
	for _, a := range byAgent {
		h.agents = append(h.agents, *a)
	}
	sort.SliceStable(h.agents, func(i, j int) bool {
		if h.agents[i].tokens != h.agents[j].tokens {
			return h.agents[i].tokens > h.agents[j].tokens
		}
		return h.agents[i].role+h.agents[i].agent < h.agents[j].role+h.agents[j].agent
	})
	return h
}

func agentFor(m map[string]*agentUse, role, agent string) *agentUse {
	k := role + "\x00" + agent
	if a, ok := m[k]; ok {
		return a
	}
	a := &agentUse{role: role, agent: agent}
	m[k] = a
	return a
}

// limitRow is the last limit a runner reported in a saved session.
type limitRow struct {
	runner  string
	at      time.Time // when the node that hit it ended
	resetAt time.Time // zero when the runner gave none
	message string
}

// buildLimits keeps, for each runner, the newest limit that a node of the
// sessions recorded. Only what the runners printed is read: no account or
// usage endpoint is asked.
func buildLimits(sessions []workspace.Session) []limitRow {
	by := map[string]limitRow{}
	for _, s := range sessions {
		if s.Graph == nil {
			continue
		}
		for _, n := range s.Graph.Nodes {
			if n.Limit == nil {
				continue
			}
			at := n.EndedAt
			if at.IsZero() {
				at = n.StartedAt
			}
			if at.IsZero() {
				at = s.UpdatedAt
			}
			name := runnerKey(n.Limit.Provider)
			if old, ok := by[name]; ok && !at.After(old.at) {
				continue
			}
			by[name] = limitRow{runner: name, at: at, resetAt: n.Limit.ResetAt, message: oneLine(workspace.SanitizeText(n.Limit.Message))}
		}
	}
	out := make([]limitRow, 0, len(by))
	for _, r := range by {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].runner < out[j].runner })
	return out
}

// limitState says when the limit lifts: "resets in 2h 10m (14:30)", "reset time
// passed" or "no reset time given".
func limitState(now time.Time, r limitRow) string {
	switch {
	case r.resetAt.IsZero():
		return "no reset time given"
	case !r.resetAt.After(now):
		return "reset time passed (" + limitClock(now, r.resetAt) + ")"
	}
	d := r.resetAt.Sub(now).Round(time.Minute)
	in := fmt.Sprintf("%dm", int(d.Minutes()))
	if d >= time.Hour {
		in = fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return "resets in " + in + " (" + limitClock(now, r.resetAt) + ")"
}

// loadSessions reads the sessions of the project that changed in the last 31
// days. cur replaces the saved copy of the open session, which may be stale.
func loadSessions(root string, now time.Time, cur workspace.Session) []workspace.Session {
	return loadSessionsSince(root, now, cur, now.AddDate(0, 0, -31))
}

// loadSessionsSince is loadSessions for the sessions saved after cutoff.
func loadSessionsSince(root string, now time.Time, cur workspace.Session, cutoff time.Time) []workspace.Session {
	var out []workspace.Session
	entries, _ := os.ReadDir(workspace.SessionsDir(root))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if id == cur.ID {
			continue
		}
		if info, err := e.Info(); err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		s, err := workspace.LoadSession(root, id)
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	if (cur.Graph != nil && len(cur.Graph.Nodes) > 0) || cur.In+cur.Out > 0 {
		if cur.UpdatedAt.IsZero() {
			cur.UpdatedAt = now
		}
		out = append(out, cur)
	}
	return out
}

// wsHistMsg carries the history and the limits read off the UI thread.
type wsHistMsg struct {
	hist   usageHist
	limits []limitRow
}

// loadHistoryCmd reads the saved sessions in the background.
func (m workspaceModel) loadHistoryCmd() tea.Cmd {
	root, now, c := m.root, m.now(), m.liveCfg()
	cur := m.sess.snapshot()
	if m.g != nil {
		cur.Graph = m.g.Clone()
		cur.UpdatedAt = now
	}
	return func() tea.Msg {
		ss := loadSessions(root, now, cur)
		return wsHistMsg{hist: buildUsageHistory(ss, now, c), limits: buildLimits(ss)}
	}
}

func (m workspaceModel) onHist(msg wsHistMsg) (tea.Model, tea.Cmd) {
	if m.overlay == wsOverlayPanel && (m.panel.kind == panelUsage || m.panel.kind == panelStatus) {
		h := msg.hist
		m.panel.hist, m.panel.limits, m.panel.loaded = &h, msg.limits, true
	}
	return m, nil
}

// ── /usage ───────────────────────────────────────────────────────────────────

const (
	usageTabSession = iota
	usageTabBudget
	usageTabHistory
	usageTabLimits
)

func (m workspaceModel) openUsage(tab int, perNode bool) (tea.Model, tea.Cmd) {
	m = m.openPanel(panelUsage, tab)
	m.panel.perNode = perNode
	return m, m.loadHistoryCmd()
}

func (m workspaceModel) usageKey(k string) (workspaceModel, tea.Cmd, bool) {
	switch {
	case k == "n" && m.panel.ps.Tab == usageTabSession:
		m.panel.perNode = !m.panel.perNode
		m.panel.ps.Scroll = 0
		return m, nil, true
	case k == "enter" && m.panel.ps.Tab == usageTabBudget:
		return m.startInput(""), nil, true
	}
	return m, nil, false
}

// applyBudgetInput sets the budget from the amount typed on the Budget tab.
func (m workspaceModel) applyBudgetInput(val string) (tea.Model, tea.Cmd) {
	if val == "" {
		return m, nil
	}
	if _, err := parseBudget(val); err != nil {
		m.panel.note, m.panel.noteKind = "type an amount such as 10, or off", ui.ResultError
		return m, nil
	}
	next, cmd := m.cmdBudget(val)
	m = next.(workspaceModel)
	if strings.EqualFold(val, "off") {
		m.panel.note, m.panel.noteKind = "session budget removed", ui.ResultOK
	} else {
		m.panel.note, m.panel.noteKind = "budget of this session set", ui.ResultOK
	}
	return m, cmd
}

func (m workspaceModel) usageOpts(o *ui.PanelOpts) {
	o.Title = "Usage"
	inner := ui.PanelInner(m.width)
	keys := []ui.Hint{{Key: "n", Label: "per node"}}
	switch m.panel.ps.Tab {
	case usageTabBudget:
		keys = []ui.Hint{{Key: "Enter", Label: "set amount"}}
	case usageTabHistory, usageTabLimits:
		keys = nil
	}
	o.Keys = hintsFor(true, keys...)
	switch m.panel.ps.Tab {
	case usageTabSession:
		o.Body = m.usageSessionBody(inner)
	case usageTabBudget:
		o.Body = m.usageBudgetBody(inner)
		if m.panel.inputing {
			o.Body = append(o.Body, "", m.panelInputLine("Amount (or off):"))
			o.Keys = []ui.Hint{{Key: "Enter", Label: "apply"}, {Key: "Esc", Label: "cancel"}}
		}
	case usageTabHistory:
		o.Body = m.usageHistoryBody(inner)
	case usageTabLimits:
		o.Body = m.usageLimitsBody(inner)
	}
}

func (m workspaceModel) usageSessionBody(inner int) []string {
	c, s := m.liveCfg(), m.sess.snapshot()
	u := buildSessionUsage(c, s, m.g)
	nodes := 0
	if m.g != nil {
		nodes = len(m.g.Nodes)
	}
	head := fmt.Sprintf("This session · %d %s · %s", nodes, pluralize(nodes, "node", "nodes"), sessionAge(m.now().Sub(s.CreatedAt)))
	cached := 0
	if m.g != nil {
		for _, n := range m.g.Nodes {
			cached += n.CachedTokens
		}
	}
	totals := fmt.Sprintf("in %s · out %s · cache %s · %s", formatTokensCompact(u.total.in), formatTokensCompact(u.total.out), formatTokensCompact(cached), u.total.costCell())
	body := []string{plainText(head), dimText(totals), ""}
	body = append(body, planLimitLines(inner, m.now())...)
	body = append(body, "")
	tok := func(r useRow, n int) string {
		if r.estimated {
			return "~" + ui.Num(n)
		}
		return ui.Num(n)
	}
	var rows [][]string
	var cols []ui.Col
	if m.panel.perNode {
		cols = []ui.Col{{Title: "NODE"}, {Title: "ROLE"}, {Title: "TOKENS IN", Align: ui.Right}, {Title: "OUT", Align: ui.Right}, {Title: "COST", Align: ui.Right}}
		for _, r := range u.nodes {
			rows = append(rows, []string{r.key, lipgloss.NewStyle().Foreground(ui.RoleColor(r.role)).Render(r.role), tok(r, r.in), tok(r, r.out), r.costCell()})
		}
		if s.In+s.Out > 0 {
			lead := useRow{}
			usd, known := leadCost(c, s)
			lead.add(s.In, s.Out, usd, known, false)
			rows = append(rows, []string{"lead", lipgloss.NewStyle().Foreground(ui.RoleColor("pm")).Render("pm"), tok(lead, lead.in), tok(lead, lead.out), lead.costCell()})
		}
	} else {
		cols = []ui.Col{{Title: "ROLE"}, {Title: "AGENT"}, {Title: "TOKENS IN", Align: ui.Right}, {Title: "OUT", Align: ui.Right}, {Title: "COST", Align: ui.Right}}
		for _, r := range u.roles {
			rows = append(rows, []string{lipgloss.NewStyle().Foreground(ui.RoleColor(r.role)).Render(r.role), agentCell(r.agent), tok(r, r.in), tok(r, r.out), r.costCell()})
		}
	}
	if len(rows) == 0 {
		return append(body, dimText("no node has used tokens yet"))
	}
	rows = append(rows, []string{"total", "", tok(u.total, u.total.in), tok(u.total, u.total.out), u.total.costCell()})
	body = append(body, ui.Table(ui.TableOpts{Cols: cols, Rows: rows, Header: ui.GroupColor(ui.GroupNameUsage), Width: inner})...)
	if u.folded > 0 {
		body = append(body, dimText(fmt.Sprintf("the total includes %s of folded or cancelled nodes", ui.Money(u.folded))))
	}
	if u.total.estimated {
		body = append(body, dimText("~ marks estimates"))
	}
	limit, _ := budgetOf(c, s)
	body = append(body, "")
	if limit > 0 {
		body = append(body, dimText("Budget  ")+ui.Meter(u.total.usd/limit, 20)+dimText(" of "+ui.Money(limit)))
	} else {
		body = append(body, dimText("Budget  none set (Budget tab)"))
	}
	return body
}

// agentCell colours the runner of "runner · model · effort".
func agentCell(label string) string {
	name, rest, found := strings.Cut(label, " · ")
	out := lipgloss.NewStyle().Foreground(ui.RunnerColor(name)).Render(name)
	if found {
		out += dimText(" · " + rest)
	}
	return out
}

func sessionAge(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Minute:
		return "just started"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func (m workspaceModel) usageBudgetBody(inner int) []string {
	c, s := m.liveCfg(), m.sess.snapshot()
	limit, _ := budgetOf(c, s)
	var body []string
	if limit > 0 {
		body = append(body, dimText("Used    ")+ui.Meter(m.use().spent/limit, 20)+dimText(" of "+ui.Money(limit)), "")
	}
	for _, l := range m.budgetLines() {
		switch {
		case l == "":
			body = append(body, "")
		case l == "By runner":
			body = append(body, sectionHead(ui.GroupNameUsage, l))
		case ansi.StringWidth(l) > inner:
			body = append(body, wrapText(l, inner)...)
		default:
			body = append(body, l)
		}
	}
	return body
}

func (m workspaceModel) usageHistoryBody(inner int) []string {
	h := m.panel.hist
	if h == nil {
		return []string{dimText("reading the saved sessions of this project…")}
	}
	gc := ui.GroupColor(ui.GroupNameUsage)
	span := func(name string, s usageSpan) []string {
		return []string{name, ui.Num(s.sessions), ui.Num(s.tokens), s.costCell()}
	}
	body := ui.Table(ui.TableOpts{
		Cols: []ui.Col{{Title: "PERIOD"}, {Title: "SESSIONS", Align: ui.Right}, {Title: "TOKENS", Align: ui.Right}, {Title: "COST", Align: ui.Right}},
		Rows: [][]string{span("today", h.today), span("7 days", h.week), span("30 days", h.month)}, Header: gc, Width: inner})
	body = append(body, "", dimText("Tokens per day, last 30 days"), ui.Sparkline(h.daily[:]))
	body = append(body, "", sectionHead(ui.GroupNameUsage, "BY AGENT (30 days)"))
	if len(h.agents) == 0 {
		return append(body, dimText("no saved session used tokens in the last 30 days"))
	}
	var rows [][]string
	for _, a := range h.agents {
		rows = append(rows, []string{lipgloss.NewStyle().Foreground(ui.RoleColor(a.role)).Render(a.role), agentCell(a.agent), ui.Num(a.tokens), a.span.costCell()})
	}
	body = append(body, ui.Table(ui.TableOpts{
		Cols: []ui.Col{{Title: "ROLE"}, {Title: "AGENT"}, {Title: "TOKENS", Align: ui.Right}, {Title: "COST", Align: ui.Right}},
		Rows: rows, Header: gc, Width: inner})...)
	return append(body, dimText("– means no price is configured for the model (pricing: in the config)"))
}

func (m workspaceModel) usageLimitsBody(inner int) []string {
	plan := append(planLimitLines(inner, m.now()), "")
	if !m.panel.loaded {
		return append(plan, dimText("reading the saved sessions of this project…"))
	}
	if len(m.panel.limits) == 0 {
		return append(plan, dimText("No runner has reported a limit in the saved sessions of this project."))
	}
	now := m.now()
	var rows [][]string
	for _, r := range m.panel.limits {
		rows = append(rows, []string{lipgloss.NewStyle().Foreground(ui.RunnerColor(r.runner)).Render(r.runner), r.at.In(now.Location()).Format("Jan 2 15:04"), limitState(now, r), r.message})
	}
	body := ui.Table(ui.TableOpts{
		Cols: []ui.Col{{Title: "RUNNER"}, {Title: "HIT"}, {Title: "STATE"}, {Title: "MESSAGE"}},
		Rows: rows, Header: ui.GroupColor(ui.GroupNameUsage), Width: inner})
	body = append(plan, body...)
	return append(body, "", dimText("What each runner last printed when it limited a node."), dimText("decsr asks no account or usage service."))
}
