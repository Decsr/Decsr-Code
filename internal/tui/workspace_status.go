package tui

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/pricing"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── Cost and budget ──────────────────────────────────────────────────────────

// effectiveModel is the model an agent runs: its own, else the lead's when it
// uses the same runner. "" when it is the runner's own default.
func effectiveModel(c config.Config, ac config.AgentConfig) string {
	if ac.Model != "" {
		return ac.Model
	}
	if pm := c.Agents["pm"]; pm.Model != "" && pm.Runner == ac.Runner {
		return pm.Model
	}
	return ""
}

// liveCfg is the current config for pricing: the dependency's, else the model's.
func (m workspaceModel) liveCfg() config.Config {
	if m.deps.cfg != nil {
		return m.deps.cfg()
	}
	return m.cfg
}

// budgetOf is the budget in force: the session's /budget override, else the
// configured one.
func budgetOf(c config.Config, s workspace.Session) (limit float64, warnPct int) {
	limit = c.Workspace.BudgetUSD
	if s.BudgetUSD > 0 {
		limit = s.BudgetUSD
	}
	return limit, workspace.Budget{WarnPct: c.Workspace.BudgetWarnPct}.WarnShare()
}

// leadCost is the known cost of the lead's tokens, priced with the pm agent.
func leadCost(c config.Config, s workspace.Session) (usd float64, known bool) {
	pm := c.Agents["pm"]
	return pricing.Cost(c, pm.Runner, pm.Model, s.In, s.Out)
}

// wireBudget gives the engine the prices and the budget of the session.
func (m workspaceModel) wireBudget(sess *wsSess) {
	if m.eng == nil {
		return
	}
	cfgFn := m.liveCfg
	if m.deps.cfg == nil {
		c := m.cfg
		cfgFn = func() config.Config { return c }
	}
	m.eng.SetCost(func(runner, model string, in, out int) (float64, bool) {
		return pricing.Cost(cfgFn(), runner, model, in, out)
	})
	m.eng.SetBudget(func() workspace.Budget {
		c, s := cfgFn(), sess.snapshot()
		limit, _ := budgetOf(c, s)
		extra, _ := leadCost(c, s)
		return workspace.Budget{LimitUSD: limit, WarnPct: c.Workspace.BudgetWarnPct, ExtraUSD: extra}
	})
}

// onBudgetEvent shows the warning or the stop and tells the user.
func (m *workspaceModel) onBudgetEvent(ev workspace.Event) {
	m.say("system", ev.Text)
	m.notifyNote(notify.NeedsYou, ev.Text)
}

// runnerUse is the usage of one runner in the session.
type runnerUse struct {
	name    string
	tokens  int
	usd     float64
	unknown bool // some of its tokens have no price
	models  []string
}

// sessionUse is what the session has used so far.
type sessionUse struct {
	runners []runnerUse
	spent   float64 // known cost
	tokens  int
	unknown bool // some tokens have no price
	priced  bool // some cost is known
}

func (u *sessionUse) add(runner, model string, tokens int, usd float64, known bool) {
	if tokens <= 0 {
		return
	}
	name := runnerKey(runner)
	idx := -1
	for i := range u.runners {
		if u.runners[i].name == name {
			idx = i
		}
	}
	if idx < 0 {
		u.runners = append(u.runners, runnerUse{name: name})
		idx = len(u.runners) - 1
	}
	r := &u.runners[idx]
	r.tokens += tokens
	u.tokens += tokens
	r.usd += usd // priced attempts count even when another attempt had no price
	u.spent += usd
	if known {
		u.priced = true
		return
	}
	r.unknown, u.unknown = true, true
	label := model
	if label == "" {
		label = "default model"
	}
	for _, x := range r.models {
		if x == label {
			return
		}
	}
	r.models = append(r.models, label)
}

// use collects the tokens and the known cost of the lead and every node.
func (m workspaceModel) use() sessionUse {
	var u sessionUse
	c, s := m.liveCfg(), m.sess.snapshot()
	pm := c.Agents["pm"]
	usd, known := leadCost(c, s)
	u.add(pm.Runner, effectiveModel(c, pm), s.In+s.Out, usd, known)
	if m.g != nil {
		for _, n := range m.g.Nodes {
			role, ok := workspace.FindRole(c, n.Role)
			if !ok {
				role = workspace.Role{Agent: pm}
			}
			u.add(role.Agent.Runner, effectiveModel(c, role.Agent), n.InputTokens+n.OutputTokens, n.CostUSD, n.CostKnown)
		}
	}
	sort.SliceStable(u.runners, func(i, j int) bool { return u.runners[i].tokens > u.runners[j].tokens })
	return u
}

func usd(v float64) string { return fmt.Sprintf("$%.2f", v) }

// budgetShort is "$5" or "$5.50" for a limit.
func budgetShort(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("$%d", int(v))
	}
	return usd(v)
}

// cmdBudget is /budget [amount|off].
func (m workspaceModel) cmdBudget(args string) (tea.Model, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		return m.openInfo("Budget", m.budgetLines()), nil
	}
	v, err := parseBudget(args)
	if err != nil {
		m.say("error", err.Error())
		return m, nil
	}
	m.sess.update(func(s *workspace.Session) { s.BudgetUSD = v })
	m.saveSession()
	if v == 0 {
		if lim := m.cfg.Workspace.BudgetUSD; lim > 0 {
			m.say("system", fmt.Sprintf("Session budget override removed; workspace.budget_usd is %s.", usd(lim)))
		} else {
			m.say("system", "Session budget override removed; no budget is set.")
		}
	} else {
		m.say("system", fmt.Sprintf("Budget of this session set to %s.", usd(v)))
	}
	if m.eng != nil {
		m.eng.CheckBudget()
	}
	return m, nil
}

// parseBudget reads "10", "$10" or "10.50"; "off" and "0" give 0 (no override).
func parseBudget(s string) (float64, error) {
	if strings.EqualFold(s, "off") {
		return 0, nil
	}
	v, err := strconv.ParseFloat(strings.TrimPrefix(s, "$"), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e9 {
		return 0, fmt.Errorf("usage: /budget [amount|off]  (for example /budget 10)")
	}
	return v, nil
}

// budgetLines is the body of /budget.
func (m workspaceModel) budgetLines() []string {
	c, s := m.liveCfg(), m.sess.snapshot()
	limit, warn := budgetOf(c, s)
	u := m.use()
	budget := "none"
	switch {
	case s.BudgetUSD > 0:
		budget = usd(s.BudgetUSD) + " (this session, /budget)"
	case limit > 0:
		budget = usd(limit) + " (workspace.budget_usd)"
	}
	spent := usd(u.spent)
	if u.unknown {
		spent = "≥ " + spent
	}
	lines := []string{kv("Budget", budget), kv("Spent", spent)}
	if limit > 0 {
		lines = append(lines, kv("Warn at", fmt.Sprintf("%d%% (%s)", warn, usd(limit*float64(warn)/100))))
	}
	lines = append(lines, "", "By runner")
	if len(u.runners) == 0 {
		lines = append(lines, "  nothing used yet")
	}
	for _, r := range u.runners {
		cost := usd(r.usd)
		if r.unknown {
			cost = "≥ " + cost
		}
		lines = append(lines, "  "+padRight(r.name, 12)+padRight(formatTokensCompact(r.tokens)+" tok", 12)+cost)
	}
	if u.unknown {
		lines = append(lines, "", "No price for:")
		for _, r := range u.runners {
			for _, md := range r.models {
				lines = append(lines, "  "+r.name+" · "+md)
			}
		}
		lines = append(lines, "Add them under pricing: in the config. Only known cost counts toward the budget.")
	}
	return lines
}

// ── Status bar ───────────────────────────────────────────────────────────────

// statusBar is the dim bottom line: tokens per runner and the cost on the
// left, the branch and pull request on the right. It is one line of m.width
// cells; elements drop out when it is narrow.
func (m workspaceModel) statusBar() string {
	w := m.width
	dim := lipgloss.NewStyle().Foreground(colMuted)
	u := m.use()
	c, s := m.liveCfg(), m.sess.snapshot()
	limit, warn := budgetOf(c, s)

	cost := m.costSegment(u, limit, warn)
	sep := dim.Render("  ·  ")
	var runners []string
	for _, r := range u.runners {
		runners = append(runners, dim.Render(runnerGlyph(r.name, m.cfg.UI.Icons)+" "+r.name+" "+formatTokensCompact(r.tokens)))
	}
	join := func(rs []string, tail string) string {
		parts := append([]string(nil), rs...)
		if tail != "" {
			if len(parts) > 0 {
				return strings.Join(parts, dim.Render("  ")) + sep + tail
			}
			return tail
		}
		return strings.Join(parts, dim.Render("  "))
	}
	var lefts []string
	for n := len(runners); n >= 0; n-- {
		if l := join(runners[:n], cost); l != "" {
			lefts = append(lefts, l)
		}
	}
	if len(lefts) == 0 {
		lefts = []string{dim.Render("no usage yet")}
	}
	rights := m.statusRights()
	rights = append(rights, "")

	avail := w - 4
	for _, l := range lefts {
		for _, r := range rights {
			need := ansi.StringWidth(l)
			if r != "" {
				need += ansi.StringWidth(r) + 3
			}
			if need <= avail {
				gap := max(avail-ansi.StringWidth(l)-ansi.StringWidth(r), 0)
				return "  " + l + strings.Repeat(" ", gap) + r + "  "
			}
		}
	}
	return "  " + ansi.Truncate(lefts[len(lefts)-1], max(avail, 1), "…")
}

// costSegment is "$0.38 / $5.00", "$0.38", "≥ $0.38" or "tokens only".
func (m workspaceModel) costSegment(u sessionUse, limit float64, warnPct int) string {
	dim := lipgloss.NewStyle().Foreground(colMuted)
	if !u.priced && limit == 0 {
		if u.tokens > 0 {
			return dim.Render("tokens only")
		}
		return ""
	}
	text := usd(u.spent)
	if u.unknown {
		text = "≥ " + text
	}
	col := colMuted
	if limit > 0 {
		text += " / " + usd(limit)
		pct := u.spent / limit * 100
		switch {
		case pct >= 100:
			col = colFail
		case pct >= float64(warnPct):
			col = colWarn
		}
	}
	return lipgloss.NewStyle().Foreground(col).Render(text)
}

// statusRights are the right-hand variants, richest first.
func (m workspaceModel) statusRights() []string {
	dim := lipgloss.NewStyle().Foreground(colMuted)
	branch := ""
	if m.iso != nil {
		branch = "⎇ " + m.iso.Branch()
	}
	pr := ""
	if p := m.sess.snapshot().PR; p != nil && p.Number > 0 {
		pr = fmt.Sprintf("PR #%d", p.Number)
		switch {
		case m.pr.passedSHA != "":
			pr += " ● checks passing"
		case m.pr.watching:
			pr += " ● watching"
		}
	}
	var out []string
	add := func(parts ...string) {
		var keep []string
		for _, p := range parts {
			if p != "" {
				keep = append(keep, p)
			}
		}
		if len(keep) > 0 {
			out = append(out, dim.Render(strings.Join(keep, " · ")))
		}
	}
	add(branch, pr)
	if branch != "" {
		short := "⎇ " + truncateOneLine(strings.TrimPrefix(branch, "⎇ "), 16)
		add(short, pr)
		add(short)
	}
	if pr != "" {
		add(strings.SplitN(pr, " ●", 2)[0])
	}
	return out
}

// ── Prompt chips ─────────────────────────────────────────────────────────────

// promptChips is the dim row under the typing area: isolation, the lead's
// runner and model, the budget. It is empty when the typing area is a single
// line. The chips are display only: there is no model picker for the lead
// (change it in /config).
func (m workspaceModel) promptChips() string {
	if m.promptRows() < 2 {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(colMuted)
	c, s := m.liveCfg(), m.sess.snapshot()
	var chips []string
	switch {
	case m.iso != nil:
		chips = append(chips, "⚠ worktree")
	case m.eng != nil || c.Workspace.Isolation == workspace.IsolationShared:
		chips = append(chips, "shared")
	}
	pm := c.Agents["pm"]
	model := shortModel(pm.Runner, effectiveModel(c, pm))
	if model == "" {
		model = runnerKey(pm.Runner)
	}
	chips = append(chips, runnerGlyph(pm.Runner, m.cfg.UI.Icons)+" "+model)
	if limit, _ := budgetOf(c, s); limit > 0 {
		chips = append(chips, usd(m.use().spent)+"/"+budgetShort(limit))
	}
	row := strings.Join(chips, "   ")
	return " " + dim.Render(ansi.Truncate(row, max(m.width-9, 4), "…"))
}
