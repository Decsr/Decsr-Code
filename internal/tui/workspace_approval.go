package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ranking"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── Rankings for the card and the editor ─────────────────────────────────────

var rankCache struct {
	sync.Mutex
	path string
	mod  time.Time
	size int64
	tab  ranking.Table
}

// approvalTable is the saved rankings with the settings of cfg. The parsed
// file is kept until it changes, because the card is drawn on every frame.
func approvalTable(cfg config.Config) ranking.Table {
	path := ranking.Path()
	st, err := os.Stat(path)
	if err != nil {
		return ranking.Table{}.With(ranking.FromConfig(cfg))
	}
	rankCache.Lock()
	defer rankCache.Unlock()
	if rankCache.path != path || !rankCache.mod.Equal(st.ModTime()) || rankCache.size != st.Size() {
		t, lerr := ranking.Load()
		if lerr != nil {
			return ranking.Table{}.With(ranking.FromConfig(cfg))
		}
		rankCache.path, rankCache.mod, rankCache.size, rankCache.tab = path, st.ModTime(), st.Size(), t
	}
	return rankCache.tab.With(ranking.FromConfig(cfg))
}

// ── The approval card ────────────────────────────────────────────────────────

// approvalRow is one pending node on the card.
type approvalRow struct {
	ID, Title, Grade string
	Agent            string  // runner · model · effort
	Runner           string  // the runner of the assignment, for its colour
	Marker           string  // ↓ cheaper than the role's agent, = the same, ↑ dearer, ≠ differs
	Cost             float64 // estimated USD per task; 0 = unknown
	Reason           string
	Role             string   // the node's role (or the suggested one)
	NewRole          bool     // the role does not exist yet: y creates it for this session
	Access           string   // writes or read-only
	MCP              []string // MCP servers the lead marked for the node
	MCPWarn          bool     // the node's agent is not claude: it runs without MCP
}

// approvalCard is everything the card shows.
type approvalCard struct {
	Mode    string
	Rows    []approvalRow
	Sel     int
	Total   float64
	Changes []string // one line per change of an existing node, mark included
}

// planPending reports whether the plan card has anything to answer: nodes
// that wait for the approval of their agent, or changes of existing nodes.
func (m workspaceModel) planPending() bool {
	return len(m.pendingChanges) > 0 || (m.g != nil && len(m.g.PendingApproval()) > 0)
}

// changeLine is the card line of one change: its mark, the node, what
// happens and the lead's reason.
func (m workspaceModel) changeLine(c workspace.Change) string {
	reason := ""
	if c.Reason != "" {
		reason = " (" + oneLine(c.Reason) + ")"
	}
	switch c.Action {
	case workspace.ChangeUpdate:
		what := oneLine(c.Description)
		if what == "" {
			what = "dependencies: " + orDefault(strings.Join(c.DependsOn, ", "), "none")
		}
		return "~ " + c.Node + " changed: " + what + reason
	case workspace.ChangeCancel:
		return "✗ " + c.Node + " cancelled: " + orDefault(oneLine(c.Reason), "no longer needed")
	}
	when := "after it finishes"
	if n := m.g.Node(c.Node); n != nil && n.Status != workspace.StatusRunning {
		when = "starts again"
	}
	return "↻ " + c.Node + " " + when + ": " + oneLine(c.Message) + reason
}

// agentLabel is "runner · model · effort" for an assignment.
func agentLabel(runnerName, model, effort string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	parts := []string{orDefault(runnerName, "opencode"), orDefault(model, "default")}
	if effort != "" {
		parts = append(parts, effort)
	}
	return strings.Join(parts, " · ")
}

// roleAgentOf is the agent of the role a node runs as, and whether it exists.
func (m workspaceModel) roleAgentOf(n *workspace.Node) (config.AgentConfig, bool) {
	role := n.Role
	if role == "" {
		role = n.SuggestedRole
	}
	r, ok := workspace.FindRole(m.cfg, role)
	if !ok {
		return config.AgentConfig{}, false
	}
	ac := r.Agent
	ac.Model = effectiveModel(m.cfg, ac)
	return ac, true
}

// approvalTarget is the pending node the card's selection and the editor use:
// the selected node when it is pending, else the first pending one.
func (m workspaceModel) approvalTarget() *workspace.Node {
	if m.g == nil {
		return nil
	}
	pend := m.g.PendingApproval()
	for _, n := range pend {
		if n.ID == m.sel {
			return n
		}
	}
	if len(pend) > 0 {
		return pend[0]
	}
	return nil
}

// approvalSelect moves the selection of the card by dir over the pending
// nodes, without wrapping.
func (m workspaceModel) approvalSelect(dir int) workspaceModel {
	pend := m.g.PendingApproval()
	cur := m.approvalTarget()
	for i, n := range pend {
		if cur != nil && n.ID == cur.ID {
			m.sel = pend[min(max(i+dir, 0), len(pend)-1)].ID
			return m
		}
	}
	return m
}

// approvalCardData builds the card of the pending nodes; ok is false when
// none wait.
func (m workspaceModel) approvalCardData() (approvalCard, bool) {
	if m.g == nil {
		return approvalCard{}, false
	}
	pend := m.g.PendingApproval()
	if len(pend) == 0 && len(m.pendingChanges) == 0 {
		return approvalCard{}, false
	}
	tab := approvalTable(m.cfg)
	target := m.approvalTarget()
	c := approvalCard{Mode: m.routingMode()}
	for _, ch := range m.pendingChanges {
		c.Changes = append(c.Changes, m.changeLine(ch))
	}
	for i, n := range pend {
		a := n.Assign
		if target != nil && n.ID == target.ID {
			c.Sel = i
		}
		row := approvalRow{
			ID: n.ID, Title: oneLine(n.Title), Grade: orDefault(a.Grade, workspace.GradeMedium),
			Agent: agentLabel(a.Runner, a.Model, a.Effort), Runner: a.Runner, Cost: a.EstCostUSD, Reason: oneLine(a.Reason),
		}
		if a.Source == workspace.SourceManual {
			row.Reason = strings.TrimSuffix("manual pick · "+row.Reason, " · ")
		}
		if ra, ok := m.roleAgentOf(n); ok {
			row.Marker = agentMarker(tab, ra, a)
		}
		row.Role = orDefault(n.Role, n.SuggestedRole)
		row.NewRole = m.newRoleOf(n) != ""
		row.Access = "writes"
		if m.readOnlyNode(n) {
			row.Access = "read-only"
		}
		row.MCP = n.MCP
		row.MCPWarn = len(n.MCP) > 0 && a.Runner != "claude"
		c.Total += a.EstCostUSD
		c.Rows = append(c.Rows, row)
	}
	return c, true
}

// agentMarker compares an assignment with the role's agent: = the same pair,
// ↓ cheaper, ↑ dearer, ≠ different with an unknown cost.
func agentMarker(tab ranking.Table, role config.AgentConfig, a *workspace.AgentAssign) string {
	if orDefault(role.Runner, "opencode") == a.Runner && role.Model == a.Model && role.Effort == a.Effort {
		return "="
	}
	if rc, ok := tab.Lookup(role.Runner, role.Model, role.Effort); ok && rc.CostPerTask > 0 && a.EstCostUSD > 0 {
		switch {
		case a.EstCostUSD < rc.CostPerTask:
			return "↓"
		case a.EstCostUSD > rc.CostPerTask:
			return "↑"
		}
		return "="
	}
	return "≠"
}

func gradeColour(g string) lipgloss.TerminalColor {
	switch g {
	case workspace.GradeSmall:
		return ui.OK
	case workspace.GradeLarge:
		return ui.Warn
	}
	return ui.Text
}

func costCell(v float64) string {
	if v <= 0 {
		return "–"
	}
	if v < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

// renderApprovalCard draws the approval card in at most width cells: a title
// line, two lines per node (the choice, then the reason) and the keys.
func renderApprovalCard(c approvalCard, width int) []string {
	iw := max(width-4, 4)
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	text := lipgloss.NewStyle().Foreground(ui.Text)
	acc := lipgloss.NewStyle().Foreground(ui.Accent)

	title, info := "Approve agents", fmt.Sprintf(" · %d %s · mode %s", len(c.Rows), pluralize(len(c.Rows), "node", "nodes"), c.Mode)
	switch {
	case len(c.Rows) == 0:
		title, info = "Approve changes", fmt.Sprintf(" · %d %s", len(c.Changes), pluralize(len(c.Changes), "change", "changes"))
	case len(c.Changes) > 0:
		title = "Approve plan"
		info += fmt.Sprintf(" · %d %s", len(c.Changes), pluralize(len(c.Changes), "change", "changes"))
	}
	head := lipgloss.NewStyle().Foreground(ui.Accent).Bold(true).Render(title) + dim.Render(info)
	if c.Total > 0 {
		head += dim.Render(" · est. " + costCell(c.Total))
	}
	rows := []string{head}

	idW, agentW := 2, 0
	for _, r := range c.Rows {
		idW = max(idW, ansi.StringWidth(r.ID))
		agentW = max(agentW, ansi.StringWidth(r.Agent))
	}
	const gradeW, costW = 6, 6
	agentW = min(agentW, max(iw/2, 14))
	fixed := func() int { return 2 + idW + 1 + 1 + gradeW + 2 + agentW + 1 + 1 + 1 + costW }
	for agentW > 12 && iw-fixed() < 14 {
		agentW--
	}
	titleW := max(iw-fixed(), 4)
	for i, r := range c.Rows {
		mark := "  "
		idSt := text.Bold(true)
		if i == c.Sel {
			mark, idSt = acc.Render("▸ "), acc.Bold(true)
		}
		cost := padLeft(costCell(r.Cost), costW)
		line := mark + idSt.Render(padRight(r.ID, idW)) + " " +
			text.Render(padRight(truncateOneLine(r.Title, titleW), titleW)) + " " +
			lipgloss.NewStyle().Foreground(gradeColour(r.Grade)).Render(padRight(r.Grade, gradeW)) + "  " +
			lipgloss.NewStyle().Foreground(ui.RunnerColor(r.Runner)).Render(padRight(truncateOneLine(r.Agent, agentW), agentW)) + " " +
			acc.Render(orDefault(r.Marker, " ")) + " " + dim.Render(cost)
		rows = append(rows, line)
		ind := strings.Repeat(" ", 2+idW+1)
		var facts []string
		lead := ""
		if r.Role != "" {
			lead = lipgloss.NewStyle().Foreground(ui.RoleColor(r.Role)).Render(r.Role)
			if r.NewRole {
				lead += dim.Render(" (new · uses the pm's agent)")
			}
		}
		if r.Access != "" {
			facts = append(facts, r.Access)
		}
		if r.Reason != "" {
			facts = append(facts, r.Reason)
		}
		if line := strings.Join(facts, " · "); lead != "" || line != "" {
			if lead != "" && line != "" {
				line = dim.Render(" · " + line)
			} else {
				line = dim.Render(line)
			}
			rows = append(rows, ansi.Truncate(ind+lead+line, max(iw, 4), "…"))
		}
		if len(r.MCP) > 0 {
			rows = append(rows, ansi.Truncate(ind+dim.Render("uses MCP: "+strings.Join(r.MCP, ", ")), max(iw, 4), "…"))
		}
		if r.MCPWarn {
			rows = append(rows, ansi.Truncate(ind+lipgloss.NewStyle().Foreground(ui.Warn).Render("⚠ MCP needs a claude agent; "+r.ID+" will run without it"), max(iw, 4), "…"))
		}
	}
	if len(c.Changes) > 0 {
		if len(c.Rows) > 0 {
			rows = append(rows, "")
		}
		for _, l := range c.Changes {
			rows = append(rows, text.Render(l))
		}
	}
	keys := "y approve · n cancel · e edit agent · a role · ↑↓ select"
	if len(c.Rows) == 0 {
		keys = "y apply · n drop"
	}
	rows = append(rows, "", text.Render(keys)+dim.Render("  (/answer shows it again)"))

	b := lipgloss.NewStyle().Foreground(ui.Warn)
	out := make([]string, 0, len(rows)+2)
	out = append(out, b.Render("╭"+strings.Repeat("─", iw+2)+"╮"))
	for _, row := range rows {
		out = append(out, b.Render("│")+" "+padRight(ansi.Truncate(row, iw, "…"), iw)+" "+b.Render("│"))
	}
	return append(out, b.Render("╰"+strings.Repeat("─", iw+2)+"╯"))
}

func padLeft(s string, w int) string {
	if d := w - ansi.StringWidth(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

// ── Actions: y, n, /approve, /reject ─────────────────────────────────────────

// approveAgents applies the pending changes, approves every pending agent and
// starts the runnable nodes.
func (m workspaceModel) approveAgents() (workspaceModel, tea.Cmd) {
	if m.eng == nil || (len(m.eng.PendingApproval()) == 0 && len(m.pendingChanges) == 0) {
		m.setNotice("No agents are waiting for approval", true)
		return m, nil
	}
	m = m.adoptNewRoles()
	m = m.applyChanges()
	n := m.eng.ApproveAssignments()
	m.g = m.eng.Snapshot()
	if n > 0 {
		m.say("system", fmt.Sprintf("approved the agent of %d %s", n, pluralize(n, "node", "nodes")))
	}
	cmd := m.startTick()
	if len(m.g.Runnable()) > 0 && m.eng.Running() == 0 {
		next, rc := m.runAll()
		m = next.(workspaceModel)
		cmd = rc
	}
	if !m.noticeErr && n > 0 {
		m.setNotice(fmt.Sprintf("approved the agent of %d %s", n, pluralize(n, "node", "nodes")), false)
	}
	return m, cmd
}

// applyChanges applies the pending changes of existing nodes: update and
// cancel through the engine, a note as a reply (queued for a running node).
// Running nodes are never stopped. Every result is said in the chat.
func (m workspaceModel) applyChanges() workspaceModel {
	changes := m.pendingChanges
	m.pendingChanges = nil
	for _, c := range changes {
		var err error
		switch c.Action {
		case workspace.ChangeUpdate:
			var deps *[]string
			if c.SetDeps {
				d := c.DependsOn
				deps = &d
			}
			if err = m.eng.UpdateNode(c.Node, c.Description, deps); err == nil {
				m.say("system", c.Node+" updated")
			}
		case workspace.ChangeCancel:
			if err = m.eng.CancelNode(c.Node); err == nil {
				m.say("system", c.Node+" cancelled")
			}
		case workspace.ChangeNote:
			var res workspace.ReplyResult
			if res, err = m.eng.Reply(c.Node, c.Message); err == nil {
				switch res.State {
				case workspace.ReplyQueued:
					m.say("system", fmt.Sprintf("%s continues with your note after its current attempt (attempt %d)", c.Node, res.Attempt))
				case workspace.ReplyWaiting:
					m.say("system", fmt.Sprintf("%s will start attempt %d with your note: %s", c.Node, res.Attempt, res.Reason))
				default:
					m.say("system", fmt.Sprintf("%s started attempt %d with your note", c.Node, res.Attempt))
				}
			}
		}
		if err != nil {
			m.say("error", oneLine(err.Error()))
		}
	}
	m.g = m.eng.Snapshot()
	if m.g.Node(m.sel) == nil && len(m.g.Nodes) > 0 {
		m.sel = m.g.Nodes[0].ID
	}
	return m
}

// rejectAgents cancels the plan: its pending nodes are removed and its
// changes dropped.
func (m workspaceModel) rejectAgents() (workspaceModel, tea.Cmd) {
	if m.eng == nil || (len(m.eng.PendingApproval()) == 0 && len(m.pendingChanges) == 0) {
		m.setNotice("No agents are waiting for approval", true)
		return m, nil
	}
	dropped := len(m.pendingChanges)
	m.pendingChanges = nil
	ids := m.eng.RejectSplit()
	m.g = m.eng.Snapshot()
	m.sel = ""
	if len(m.g.Nodes) > 0 {
		m.sel = m.g.Nodes[0].ID
	}
	msg := fmt.Sprintf("cancelled the split: removed %s", idList(ids))
	if len(ids) == 0 {
		msg = fmt.Sprintf("dropped %d %s of existing nodes", dropped, pluralize(dropped, "change", "changes"))
	} else if dropped > 0 {
		msg += fmt.Sprintf(" and dropped %d %s", dropped, pluralize(dropped, "change", "changes"))
	}
	m.say("system", msg)
	m.setNotice(msg, false)
	return m, nil
}

func (m workspaceModel) cmdApprove(string) (tea.Model, tea.Cmd) { return m.approveAgents() }
func (m workspaceModel) cmdReject(string) (tea.Model, tea.Cmd)  { return m.rejectAgents() }

// ── The agent editor (e) ─────────────────────────────────────────────────────

const (
	apprFieldRunner = iota
	apprFieldModel
	apprFieldEffort
	apprFields
)

var localRunners = []string{"ollama", "lmstudio", "mlx"}

// agentEditor is the state of the editor overlay of one pending node.
type agentEditor struct {
	node    string
	field   int
	runners []string
	runner  string
	model   textinput.Model
	effort  string
	errText string
}

// openAgentEditor opens the editor on the selected pending node.
func (m workspaceModel) openAgentEditor() (workspaceModel, tea.Cmd) {
	n := m.approvalTarget()
	if n == nil || n.Assign == nil {
		m.setNotice("No agents are waiting for approval", true)
		return m, nil
	}
	a := n.Assign
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 120
	ti.Cursor.SetMode(cursor.CursorStatic)
	ti.TextStyle = lipgloss.NewStyle().Foreground(ui.Text)
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(ui.Muted)
	ti.Placeholder = "model id"
	ti.SetValue(a.Model)
	ed := &agentEditor{node: n.ID, runner: orDefault(a.Runner, "opencode"), model: ti, effort: a.Effort}
	roleRunner := ""
	if ra, ok := m.roleAgentOf(n); ok {
		roleRunner = orDefault(ra.Runner, "opencode")
	}
	conn := ranking.Connected()
	seen := map[string]bool{}
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			ed.runners = append(ed.runners, r)
		}
	}
	add(roleRunner)
	add(ed.runner)
	for _, r := range ranking.RankableRunners {
		if conn[r] {
			add(r)
		}
	}
	for _, r := range localRunners {
		add(r)
	}
	m.agentEd = ed
	m.overlay = wsOverlayAgent
	return m, nil
}

// modelOptions lists the models to pick from for a runner: the ranked ones,
// best balance first, then the role's own when it uses that runner.
func (m workspaceModel) modelOptions(runnerName string, n *workspace.Node) []string {
	var opts []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			opts = append(opts, s)
		}
	}
	cs := approvalTable(m.cfg).Candidates([]string{runnerName})
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Balance > cs[j].Balance })
	if n != nil {
		if ra, ok := m.roleAgentOf(n); ok && orDefault(ra.Runner, "opencode") == runnerName {
			add(ra.Model)
		}
	}
	for _, c := range cs {
		add(c.Model)
	}
	return opts
}

// effortOptions lists the effort levels of the pair, "" (default) first.
func effortOptions(runnerName, model string) []string {
	opts := runner.EffortOptions(runnerName, model)
	if len(opts) == 0 {
		return nil
	}
	return append([]string{""}, opts...)
}

func cycle(opts []string, cur string, dir int) string {
	if len(opts) == 0 {
		return cur
	}
	i := -1
	for j, o := range opts {
		if o == cur {
			i = j
		}
	}
	if i < 0 {
		if dir > 0 {
			return opts[0]
		}
		return opts[len(opts)-1]
	}
	return opts[(i+dir+len(opts))%len(opts)]
}

func (m workspaceModel) agentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ed := m.agentEd
	var n *workspace.Node
	if ed != nil {
		n = m.g.Node(ed.node)
	}
	if n == nil {
		m.overlay, m.agentEd = wsOverlayNone, nil
		return m, nil
	}
	k := msg.String()
	switch k {
	case "esc":
		m.overlay, m.agentEd = wsOverlayNone, nil
		return m, nil
	case "tab", "down":
		ed.field = (ed.field + 1) % apprFields
	case "shift+tab", "up":
		ed.field = (ed.field + apprFields - 1) % apprFields
	case "enter":
		return m.saveAgentEditor(n)
	case "left", "right":
		dir := 1
		if k == "left" {
			dir = -1
		}
		switch ed.field {
		case apprFieldRunner:
			next := cycle(ed.runners, ed.runner, dir)
			if next != ed.runner {
				ed.runner = next
				ed.model.SetValue(m.defaultModel(next, n))
				ed.effort = ""
			}
		case apprFieldModel:
			ed.model.SetValue(cycle(m.modelOptions(ed.runner, n), ed.model.Value(), dir))
			ed.effort = m.fitEffort(ed)
		case apprFieldEffort:
			ed.effort = cycle(effortOptions(ed.runner, ed.model.Value()), ed.effort, dir)
		}
	default:
		if ed.field == apprFieldModel {
			ed.model.Focus()
			ed.model, _ = ed.model.Update(msg)
			ed.effort = m.fitEffort(ed)
		}
	}
	ed.errText = ""
	if ed.field == apprFieldModel {
		ed.model.Focus()
	} else {
		ed.model.Blur()
	}
	return m, nil
}

// fitEffort keeps the effort when the pair supports it, else the default.
func (m workspaceModel) fitEffort(ed *agentEditor) string {
	for _, o := range effortOptions(ed.runner, ed.model.Value()) {
		if o == ed.effort {
			return ed.effort
		}
	}
	return ""
}

// defaultModel is the model to start from on a runner: the role's own on the
// role's runner, else the best-balanced ranked one.
func (m workspaceModel) defaultModel(runnerName string, n *workspace.Node) string {
	if opts := m.modelOptions(runnerName, n); len(opts) > 0 {
		return opts[0]
	}
	return ""
}

func (m workspaceModel) saveAgentEditor(n *workspace.Node) (tea.Model, tea.Cmd) {
	ed := m.agentEd
	a := workspace.AgentAssign{Runner: ed.runner, Model: strings.TrimSpace(ed.model.Value()), Effort: ed.effort}
	if err := m.eng.SetAssignment(n.ID, a); err != nil {
		ed.errText = oneLine(err.Error())
		return m, nil
	}
	m.g = m.eng.Snapshot()
	m.overlay, m.agentEd = wsOverlayNone, nil
	m.setNotice(fmt.Sprintf("%s: %s (manual); y approves", n.ID, agentLabel(a.Runner, a.Model, a.Effort)), false)
	return m, nil
}

// agentEditorBox draws the editor.
func (m workspaceModel) agentEditorBox() string {
	ed := m.agentEd
	if ed == nil {
		return ""
	}
	n := m.g.Node(ed.node)
	if n == nil {
		return ""
	}
	boxW := min(78, m.width-2)
	inner := boxW - 4
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	text := lipgloss.NewStyle().Foreground(ui.Text)
	label := func(s string, f int) string {
		if ed.field == f {
			return lipgloss.NewStyle().Foreground(ui.Text).Bold(true).Render("▸ " + padRight(s, 8))
		}
		return dim.Render("  " + padRight(s, 8))
	}
	model := ed.model.Value()
	var body []string
	if g := n.Assign; g != nil {
		body = append(body, dim.Render(truncateOneLine(fmt.Sprintf("%s · %s", orDefault(g.Grade, workspace.GradeMedium), g.Reason), inner)))
	}
	body = append(body, "")
	body = append(body, label("Runner", apprFieldRunner)+text.Render(ed.runner)+dim.Render("   ←/→"))
	mv := model
	if ed.field == apprFieldModel {
		mv = ed.model.View()
	} else if mv == "" {
		mv = dim.Render("default")
	}
	body = append(body, label("Model", apprFieldModel)+mv)
	ev := orDefault(ed.effort, "default")
	if len(effortOptions(ed.runner, model)) == 0 {
		ev = "–  (no effort control)"
	}
	body = append(body, label("Effort", apprFieldEffort)+text.Render(ev)+dim.Render("   ←/→"))

	tab := approvalTable(m.cfg)
	body = append(body, "")
	cur, curOK := tab.Lookup(ed.runner, model, ed.effort)
	if curOK {
		body = append(body, text.Render(fmt.Sprintf("Choice   balance %.0f · intelligence %.0f · %s per task", cur.Balance, cur.Intelligence, costCell(cur.CostPerTask))))
	} else {
		body = append(body, dim.Render("Choice   not ranked: no balance or cost known"))
	}
	var role *ranking.Candidate
	if ra, ok := m.roleAgentOf(n); ok {
		line := "Role     " + agentLabel(ra.Runner, ra.Model, ra.Effort)
		if rc, ok := tab.Lookup(ra.Runner, ra.Model, ra.Effort); ok {
			role = &rc
			line += fmt.Sprintf(" · balance %.0f · %s", rc.Balance, costCell(rc.CostPerTask))
		}
		body = append(body, dim.Render(truncateOneLine(line, inner)))
	}
	if curOK && role != nil && cur.CostPerTask > 0 && role.CostPerTask > 0 && cur.CostPerTask > role.CostPerTask {
		body = append(body, lipgloss.NewStyle().Foreground(ui.Warn).Render(truncateOneLine(
			fmt.Sprintf("! costs more than the role's agent (%s vs %s); allowed, it is your choice", costCell(cur.CostPerTask), costCell(role.CostPerTask)), inner)))
	}
	if opts := m.modelOptions(ed.runner, n); len(opts) > 0 {
		body = append(body, "", dim.Render("Models on "+ed.runner+" (balance · cost at this effort)"))
		shown := 0
		for _, o := range opts {
			if shown == 6 {
				body = append(body, dim.Render(fmt.Sprintf("  … %d more", len(opts)-shown)))
				break
			}
			shown++
			mark := "  "
			if o == model {
				mark = "▸ "
			}
			stats := "not ranked"
			if c, ok := tab.Lookup(ed.runner, o, ed.effort); ok {
				stats = fmt.Sprintf("%3.0f · %s", c.Balance, costCell(c.CostPerTask))
			}
			st := dim
			if o == model {
				st = text
			}
			body = append(body, st.Render(truncateOneLine(mark+padRight(truncateOneLine(o, max(inner-22, 10)), max(inner-22, 10))+"  "+stats, inner)))
		}
	}
	if ed.errText != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(ui.Fail).Render("! "+truncateOneLine(ed.errText, inner-2)))
	}
	body = append(body, "", dim.Render("Tab next · ←/→ change · type a model · Enter save · Esc cancel"))
	return ui.Overlay("Agent of "+n.ID+" · "+truncateOneLine(n.Title, boxW-20), body, nil, boxW, ui.GroupAgents)
}

// ── The agent a node shows ───────────────────────────────────────────────────

// agentLook is the agent shown for a node. Mark is "↓" when a routed pick
// differs from the role's agent.
type agentLook struct {
	Runner, Model, Effort, Mark string
}

// nodeAgent is the one place that says which agent a node shows: its assigned
// agent when it has one (pending or approved), else the role's agent. When the
// run fell back to the role's agent (FallbackNote), that is the agent shown.
// ok is false when the node has no agent to show.
func nodeAgent(cfg config.Config, n *workspace.Node) (agentLook, bool) {
	var role config.AgentConfig
	name := n.Role
	if name == "" {
		name = n.SuggestedRole
	}
	r, roleOK := workspace.FindRole(cfg, name)
	if roleOK {
		role = r.Agent
	}
	a := n.Assign
	if a == nil || a.FallbackNote != "" || a.Runner == "" {
		if !roleOK {
			return agentLook{}, false
		}
		return agentLook{Runner: role.Runner, Model: role.Model, Effort: role.Effort}, true
	}
	l := agentLook{Runner: a.Runner, Model: a.Model, Effort: a.Effort}
	if a.Source == workspace.SourceAuto && roleOK &&
		(orDefault(role.Runner, "opencode") != orDefault(a.Runner, "opencode") || role.Model != a.Model || role.Effort != a.Effort) {
		l.Mark = "↓"
	}
	return l, true
}

// nodeLook is the runner key and the short model label (runner:model, plus the
// mark) of a node, as the cards, the node list and the canvas show them.
func (m workspaceModel) nodeLook(n *workspace.Node) (runnerName, model string) {
	a, ok := nodeAgent(m.cfg, n)
	if !ok {
		return "", ""
	}
	runnerName = runnerKey(a.Runner)
	model = shortModel(a.Runner, effectiveModel(m.cfg, config.AgentConfig{Runner: a.Runner, Model: a.Model}))
	if model == "" {
		model = runnerName
	}
	if a.Mark != "" {
		model += " " + a.Mark
	}
	return runnerName, model
}
